package memory

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"
)

const (
	defaultSummaryWorkerCount = 2
	defaultSummaryQueueSize   = 128
)

type SourceSummaryMetrics struct {
	JobsEnqueued          int64
	JobsCompleted         int64
	JobsFailed            int64
	JobsDropped           int64
	SummaryDocsIndexed    int64
	SessionSummaryIndexed int64
}

type SourceSummaryWorker struct {
	mm         *MemoryManager
	summarizer *SourceSummarizer
	sessionID  string

	queue chan SourceSummaryRequest
	wg    sync.WaitGroup

	mu      sync.Mutex
	metrics SourceSummaryMetrics
}

func NewSourceSummaryWorker(mm *MemoryManager, sessionID string, cfg SourceSummaryConfig, workers int, queueSize int) (*SourceSummaryWorker, error) {
	if mm == nil {
		return nil, fmt.Errorf("memory manager is required")
	}
	if workers <= 0 {
		workers = defaultSummaryWorkerCount
	}
	if queueSize <= 0 {
		queueSize = defaultSummaryQueueSize
	}
	w := &SourceSummaryWorker{
		mm:         mm,
		summarizer: NewSourceSummarizer(mm, cfg),
		sessionID:  strings.TrimSpace(sessionID),
		queue:      make(chan SourceSummaryRequest, queueSize),
	}
	for i := 0; i < workers; i++ {
		w.wg.Add(1)
		go w.run()
	}
	return w, nil
}

func (w *SourceSummaryWorker) Enqueue(req SourceSummaryRequest) {
	if w == nil {
		return
	}
	req.Content = strings.TrimSpace(req.Content)
	if req.Content == "" {
		return
	}
	if strings.TrimSpace(req.SessionID) == "" {
		req.SessionID = w.sessionID
	}
	select {
	case w.queue <- req:
		w.mu.Lock()
		w.metrics.JobsEnqueued++
		w.mu.Unlock()
	default:
		w.mu.Lock()
		w.metrics.JobsDropped++
		w.mu.Unlock()
	}
}

func (w *SourceSummaryWorker) CloseAndFlush(timeout time.Duration) SourceSummaryMetrics {
	if w == nil {
		return SourceSummaryMetrics{}
	}
	close(w.queue)
	done := make(chan struct{})
	go func() {
		defer close(done)
		w.wg.Wait()
	}()
	if timeout <= 0 {
		<-done
	} else {
		select {
		case <-done:
		case <-time.After(timeout):
		}
	}
	return w.Metrics()
}

func (w *SourceSummaryWorker) AddSessionAggregateSummary(summary string, sourceCount int) error {
	if w == nil || w.mm == nil || strings.TrimSpace(summary) == "" {
		return nil
	}
	meta := map[string]string{
		"type":              "knowledge",
		"source_type":       "source_summary",
		"summary_of_type":   "ingest_session",
		"summary_of_ref":    strings.TrimSpace(w.sessionID),
		"ingest_session_id": strings.TrimSpace(w.sessionID),
		"summary_style":     "structured_brief",
		"summary_version":   "v1",
		"source_doc_count":  fmt.Sprintf("%d", sourceCount),
	}
	if err := w.mm.AddKnowledge(summary, meta); err != nil {
		return err
	}
	w.mu.Lock()
	w.metrics.SessionSummaryIndexed = 1
	w.metrics.SummaryDocsIndexed++
	w.mu.Unlock()
	return nil
}

func (w *SourceSummaryWorker) Metrics() SourceSummaryMetrics {
	if w == nil {
		return SourceSummaryMetrics{}
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.metrics
}

func (w *SourceSummaryWorker) run() {
	defer w.wg.Done()
	for req := range w.queue {
		ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
		result, err := w.summarizer.Summarize(ctx, req)
		cancel()
		if err != nil || strings.TrimSpace(result.SummaryText) == "" {
			w.mu.Lock()
			w.metrics.JobsFailed++
			w.mu.Unlock()
			continue
		}
		meta := map[string]string{
			"type":              "knowledge",
			"source_type":       "source_summary",
			"summary_of_type":   strings.TrimSpace(req.SourceType),
			"summary_of_ref":    strings.TrimSpace(req.SourceRef),
			"ingest_session_id": strings.TrimSpace(req.SessionID),
			"summary_style":     "structured_brief",
			"summary_version":   "v1",
			"source_doc_count":  fmt.Sprintf("%d", req.ChunkCount),
		}
		for k, v := range req.Metadata {
			meta[k] = v
		}
		if strings.TrimSpace(result.ModelUsed) != "" {
			meta["summary_model"] = strings.TrimSpace(result.ModelUsed)
		}
		if result.Fallback {
			meta["summary_fallback"] = "true"
		}
		if err := w.mm.AddKnowledge(result.SummaryText, meta); err != nil {
			w.mu.Lock()
			w.metrics.JobsFailed++
			w.mu.Unlock()
			continue
		}
		w.mu.Lock()
		w.metrics.JobsCompleted++
		w.metrics.SummaryDocsIndexed++
		w.mu.Unlock()
	}
}
