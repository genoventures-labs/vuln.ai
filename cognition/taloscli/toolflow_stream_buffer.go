package taloscli

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/Thynaptic/P-LMv1/pkg/toolflow"
)

const (
	collabToolStreamsEnabledEnv = "TALOS_COLLAB_TOOL_STREAMS_ENABLED"
)

type execStreamRecord struct {
	Tool     string
	Kind     string
	Chunk    string
	At       time.Time
	WorkerID string
}

type sharedExecutionBuffer struct {
	mu      sync.Mutex
	records []execStreamRecord
	limit   int
}

func newSharedExecutionBuffer(limit int) *sharedExecutionBuffer {
	if limit <= 0 {
		limit = 128
	}
	return &sharedExecutionBuffer{
		records: make([]execStreamRecord, 0, limit),
		limit:   limit,
	}
}

func (b *sharedExecutionBuffer) append(ev toolflow.ExecEvent) {
	if b == nil {
		return
	}
	r := execStreamRecord{
		Tool:     strings.TrimSpace(ev.Tool),
		Kind:     strings.TrimSpace(ev.Kind),
		Chunk:    strings.TrimSpace(ev.Chunk),
		At:       ev.At,
		WorkerID: strings.TrimSpace(ev.WorkerID),
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.records = append(b.records, r)
	if len(b.records) > b.limit {
		b.records = b.records[len(b.records)-b.limit:]
	}
}

func (b *sharedExecutionBuffer) latest(n int) []execStreamRecord {
	if b == nil {
		return nil
	}
	if n <= 0 {
		n = 8
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.records) <= n {
		out := make([]execStreamRecord, len(b.records))
		copy(out, b.records)
		return out
	}
	out := make([]execStreamRecord, n)
	copy(out, b.records[len(b.records)-n:])
	return out
}

type collaborativeStreamFrame struct {
	Tool     string
	Kind     string
	Chunk    string
	At       time.Time
	WorkerID string
}

type collaborativeStreamWatcherSummary struct {
	Watcher    string
	Frames     int
	Errors     int
	Highlights []string
	Dropped    int
}

type collaborativeStreamHub struct {
	mu      sync.Mutex
	subs    map[string]chan collaborativeStreamFrame
	dropped map[string]int
}

func collaborativeToolStreamsEnabled() bool {
	return envBoolDefault(collabToolStreamsEnabledEnv, true)
}

func newCollaborativeStreamHub() *collaborativeStreamHub {
	return &collaborativeStreamHub{
		subs:    map[string]chan collaborativeStreamFrame{},
		dropped: map[string]int{},
	}
}

func (h *collaborativeStreamHub) subscribe(name string, buffer int) (<-chan collaborativeStreamFrame, func()) {
	if h == nil {
		return nil, func() {}
	}
	name = strings.TrimSpace(name)
	if name == "" {
		name = "watcher"
	}
	if buffer <= 0 {
		buffer = 32
	}
	ch := make(chan collaborativeStreamFrame, buffer)
	h.mu.Lock()
	h.subs[name] = ch
	h.mu.Unlock()
	unsub := func() {
		h.mu.Lock()
		if existing, ok := h.subs[name]; ok {
			delete(h.subs, name)
			close(existing)
		}
		h.mu.Unlock()
	}
	return ch, unsub
}

func (h *collaborativeStreamHub) broadcast(ev toolflow.ExecEvent) {
	if h == nil {
		return
	}
	frame := collaborativeStreamFrame{
		Tool:     strings.TrimSpace(ev.Tool),
		Kind:     strings.TrimSpace(ev.Kind),
		Chunk:    strings.TrimSpace(ev.Chunk),
		At:       ev.At,
		WorkerID: strings.TrimSpace(ev.WorkerID),
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	for name, ch := range h.subs {
		select {
		case ch <- frame:
		default:
			h.dropped[name]++
		}
	}
}

func (h *collaborativeStreamHub) droppedCount(name string) int {
	if h == nil {
		return 0
	}
	name = strings.TrimSpace(name)
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.dropped[name]
}

func watchCollaborativeStream(name string, ch <-chan collaborativeStreamFrame, maxHighlights int) <-chan collaborativeStreamWatcherSummary {
	out := make(chan collaborativeStreamWatcherSummary, 1)
	if maxHighlights <= 0 {
		maxHighlights = 2
	}
	go func() {
		defer close(out)
		summary := collaborativeStreamWatcherSummary{Watcher: strings.TrimSpace(name)}
		for frame := range ch {
			summary.Frames++
			if frame.Kind == "error" {
				summary.Errors++
			}
			if reason, ok := detectSmokingGunStreamSignal(frame.Chunk); ok && len(summary.Highlights) < maxHighlights {
				summary.Highlights = append(summary.Highlights, truncateText(reason, 120))
			}
		}
		out <- summary
	}()
	return out
}

func formatCollaborativeStreamStatus(in []collaborativeStreamWatcherSummary) string {
	if len(in) == 0 {
		return ""
	}
	lines := make([]string, 0, len(in)+1)
	lines = append(lines, "COLLAB_STREAM: active_watchers="+fmt.Sprintf("%d", len(in)))
	for _, s := range in {
		line := fmt.Sprintf("COLLAB_SHARD[%s]: frames=%d errors=%d dropped=%d", strings.TrimSpace(s.Watcher), s.Frames, s.Errors, s.Dropped)
		if len(s.Highlights) > 0 {
			line += " highlights=" + strings.Join(s.Highlights, " | ")
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n") + "\n"
}
