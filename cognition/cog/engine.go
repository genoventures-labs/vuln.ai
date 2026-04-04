package cog

import (
	"container/heap"
	"context"
	"fmt"
	"strings"
	"sync"
	"time"
)

const (
	defaultWorkerCount = 2
)

type cognitiveItem struct {
	thought  ThoughtObject
	priority int
	enqueued time.Time
	index    int
}

type cognitiveHeap []*cognitiveItem

func (h cognitiveHeap) Len() int { return len(h) }

func (h cognitiveHeap) Less(i, j int) bool {
	if h[i].priority == h[j].priority {
		return h[i].enqueued.Before(h[j].enqueued)
	}
	return h[i].priority > h[j].priority
}

func (h cognitiveHeap) Swap(i, j int) {
	h[i], h[j] = h[j], h[i]
	h[i].index = i
	h[j].index = j
}

func (h *cognitiveHeap) Push(x any) {
	item, ok := x.(*cognitiveItem)
	if !ok {
		return
	}
	item.index = len(*h)
	*h = append(*h, item)
}

func (h *cognitiveHeap) Pop() any {
	old := *h
	n := len(old)
	if n == 0 {
		return nil
	}
	item := old[n-1]
	old[n-1] = nil
	item.index = -1
	*h = old[:n-1]
	return item
}

// Engine wires perception, substrate, and registry into a deterministic COG state machine.
type Engine struct {
	mu sync.RWMutex

	registry  *DecisionRegistry
	substrate *CognitiveSubstrate

	queue   cognitiveHeap
	wakeCh  chan struct{}
	stopCh  chan struct{}
	workers int
	wg      sync.WaitGroup
}

func NewEngine(registry *DecisionRegistry, substrate *CognitiveSubstrate, workers int) (*Engine, error) {
	if registry == nil {
		return nil, fmt.Errorf("cog engine: decision registry is required")
	}
	if substrate == nil {
		return nil, fmt.Errorf("cog engine: cognitive substrate is required")
	}
	if workers <= 0 {
		workers = defaultWorkerCount
	}
	engine := &Engine{
		registry:  registry,
		substrate: substrate,
		queue:     make(cognitiveHeap, 0),
		wakeCh:    make(chan struct{}, workers*2),
		stopCh:    make(chan struct{}),
		workers:   workers,
	}
	heap.Init(&engine.queue)
	return engine, nil
}

func (e *Engine) Start(ctx context.Context) error {
	if e == nil {
		return fmt.Errorf("cog engine: nil receiver")
	}
	if err := e.substrate.Start(ctx); err != nil {
		return err
	}

	for i := 0; i < e.workers; i++ {
		e.wg.Add(1)
		go e.worker(ctx)
	}
	return nil
}

func (e *Engine) Stop() {
	if e == nil {
		return
	}
	close(e.stopCh)
	e.wg.Wait()
	e.substrate.Stop()
}

func (e *Engine) Submit(raw RawTelemetry) (ThoughtObject, error) {
	if e == nil {
		return ThoughtObject{}, fmt.Errorf("cog engine: nil receiver")
	}
	thought, err := NormalizeTelemetry(raw)
	if err != nil {
		return ThoughtObject{}, err
	}

	item := &cognitiveItem{
		thought:  thought,
		priority: cognitivePriority(thought),
		enqueued: time.Now().UTC(),
	}

	e.mu.Lock()
	heap.Push(&e.queue, item)
	queueLen := e.queue.Len()
	e.mu.Unlock()

	load := queueLoad(queueLen)
	if err := e.substrate.SetCurrentLoad(load); err != nil {
		return ThoughtObject{}, err
	}

	select {
	case e.wakeCh <- struct{}{}:
	default:
	}

	return thought, nil
}

func (e *Engine) worker(ctx context.Context) {
	defer e.wg.Done()
	for {
		select {
		case <-ctx.Done():
			return
		case <-e.stopCh:
			return
		case <-e.wakeCh:
			if err := e.processNext(); err != nil {
				continue
			}
		}
	}
}

func (e *Engine) processNext() error {
	e.mu.Lock()
	if e.queue.Len() == 0 {
		e.mu.Unlock()
		return fmt.Errorf("cog engine: no queued thoughts")
	}

	itemAny := heap.Pop(&e.queue)
	item, ok := itemAny.(*cognitiveItem)
	queueLen := e.queue.Len()
	e.mu.Unlock()

	if !ok || item == nil {
		return fmt.Errorf("cog engine: invalid cognitive heap item")
	}

	load := queueLoad(queueLen)
	if err := e.substrate.SetCurrentLoad(load); err != nil {
		return err
	}

	route, err := e.registry.Route(item.thought, load)
	if err != nil {
		return err
	}

	if err := e.substrate.Ingest(item.thought, route); err != nil {
		return err
	}
	return nil
}

func (e *Engine) StatusASCII() string {
	if e == nil {
		return "COG STATUS\nERROR: engine is nil"
	}
	snapshot := e.substrate.Snapshot()
	queueLen := e.QueueLen()
	loadPct := int(snapshot.CurrentLoad * 100)

	shards := "none"
	if len(snapshot.ActiveShards) > 0 {
		shards = strings.Join(snapshot.ActiveShards, ",")
	}

	return fmt.Sprintf(
		"COG STATUS\n"+
			"-----------\n"+
			"cognitive_load: %d%%\n"+
			"queue_depth: %d\n"+
			"active_shards: %s\n"+
			"episodic_entries: %d\n"+
			"procedural_skills: %d",
		loadPct,
		queueLen,
		shards,
		len(snapshot.EpisodicMemory),
		len(snapshot.ProceduralMemory),
	)
}

func (e *Engine) QueueLen() int {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.queue.Len()
}

func cognitivePriority(thought ThoughtObject) int {
	priority := 10
	mission := strings.ToLower(strings.TrimSpace(thought.MissionTag))
	if mission == "sasswall" || mission == "scac" {
		priority += 100
	}
	if thought.RequiresTool {
		priority += 15
	}
	if thought.ConfidenceScore >= 0.9 {
		priority += 8
	}
	if thought.ConfidenceScore < 0.35 {
		priority -= 5
	}
	return priority
}

func queueLoad(depth int) float64 {
	if depth <= 0 {
		return 0
	}
	if depth >= 20 {
		return 1
	}
	return float64(depth) / 20.0
}
