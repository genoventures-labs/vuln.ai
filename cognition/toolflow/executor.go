package toolflow

import (
	"context"
	"fmt"
	"math/rand"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

type nodeState struct {
	node     PlanNode
	pending  int
	children []string
}

// ExecutePlan keeps compatibility with existing callers.
func ExecutePlan(ctx context.Context, plan Plan, reg *Registry, opts ExecOptions) ([]ToolResult, ExecutionTrace, error) {
	return ExecutePlanV2(ctx, plan, reg, opts)
}

// ExecutePlanV2 runs a deterministic DAG with bounded concurrency, retries, and explicit errors.
func ExecutePlanV2(ctx context.Context, plan Plan, reg *Registry, opts ExecOptions) ([]ToolResult, ExecutionTrace, error) {
	if reg == nil {
		return nil, ExecutionTrace{}, fmt.Errorf("registry is nil")
	}
	if len(plan.Nodes) == 0 {
		return nil, ExecutionTrace{}, fmt.Errorf("plan is empty")
	}
	if opts.MaxWorkers <= 0 {
		opts.MaxWorkers = 1
	}
	if opts.NodeTimeout <= 0 {
		opts.NodeTimeout = 12 * time.Second
	}
	if opts.GlobalBudget <= 0 {
		opts.GlobalBudget = 20 * time.Second
	}
	if opts.RetryMaxAttempts <= 0 {
		opts.RetryMaxAttempts = 2
	}
	if opts.RetryBackoff <= 0 {
		opts.RetryBackoff = 250 * time.Millisecond
	}
	if opts.RetryJitter <= 0 {
		opts.RetryJitter = 100 * time.Millisecond
	}

	budgetCtx, budgetCancel := context.WithTimeout(ctx, opts.GlobalBudget)
	defer budgetCancel()

	trace := ExecutionTrace{PlanHash: plan.DeterministicHash}
	states := map[string]*nodeState{}
	for _, n := range plan.Nodes {
		states[n.ID] = &nodeState{node: n, pending: len(n.DependsOn)}
	}
	for _, n := range plan.Nodes {
		for _, dep := range n.DependsOn {
			st, ok := states[dep]
			if !ok {
				return nil, trace, fmt.Errorf("dependency state missing dep=%s", dep)
			}
			st.children = append(st.children, n.ID)
		}
	}

	ready := make(chan string, len(plan.Nodes))
	complete := make(chan ToolResult, len(plan.Nodes))
	errCh := make(chan error, 1)
	var wg sync.WaitGroup

	for id, st := range states {
		if st.pending == 0 {
			ready <- id
		}
	}

	ctx, cancel := context.WithCancel(budgetCtx)
	defer cancel()

	worker := func(workerID int) {
		defer wg.Done()
		rng := rand.New(rand.NewSource(time.Now().UnixNano() + int64(workerID)))
		for {
			select {
			case <-ctx.Done():
				return
			case id, ok := <-ready:
				if !ok {
					return
				}
				node := states[id].node
				result := executeNodeWithRetry(ctx, reg, node, opts, workerID, rng)
				select {
				case complete <- result:
				default:
					select {
					case complete <- result:
					case <-ctx.Done():
						return
					}
				}
			}
		}
	}

	for i := 0; i < opts.MaxWorkers; i++ {
		wg.Add(1)
		go worker(i + 1)
	}

	resultsByID := map[string]ToolResult{}
	completed := 0
	for completed < len(plan.Nodes) {
		select {
		case <-ctx.Done():
			drain := true
			for drain {
				select {
				case res := <-complete:
					resultsByID[res.NodeID] = res
				default:
					drain = false
				}
			}
			close(ready)
			wg.Wait()
			if len(errCh) > 0 {
				return orderedResults(plan.OrderedIDs, resultsByID), trace, <-errCh
			}
			return orderedResults(plan.OrderedIDs, resultsByID), trace, ctx.Err()
		case res := <-complete:
			completed++
			resultsByID[res.NodeID] = res
			trace.Steps = append(trace.Steps, TraceStep{NodeID: res.NodeID, Tool: res.Tool, Stage: "execute", Status: statusFromResult(res), Detail: res.ErrMessage, Attempt: maxInt(1, res.Attempts), WorkerID: res.WorkerID, AtUnixMS: time.Now().UnixMilli()})
			if res.Err != nil && opts.FailClosed {
				err := fmt.Errorf("toolflow fail-closed node=%s tool=%s err=%v", res.NodeID, res.Tool, res.Err)
				select {
				case errCh <- err:
				default:
				}
				cancel()
				continue
			}
			for _, childID := range states[res.NodeID].children {
				states[childID].pending--
				if states[childID].pending == 0 {
					ready <- childID
				}
			}
		}
	}
	close(ready)
	wg.Wait()

	if len(errCh) > 0 {
		return orderedResults(plan.OrderedIDs, resultsByID), trace, <-errCh
	}
	ordered := orderedResults(plan.OrderedIDs, resultsByID)
	for _, res := range ordered {
		trace.Citations = append(trace.Citations, res.Citations...)
	}
	trace.Citations = uniqueSorted(trace.Citations)
	return ordered, trace, nil
}

func executeNodeWithRetry(ctx context.Context, reg *Registry, node PlanNode, opts ExecOptions, workerID int, rng *rand.Rand) ToolResult {
	attempts := 1
	maxAttempts := 1
	if opts.RetryEnabled {
		maxAttempts = maxInt(1, minInt(node.RetryPolicy.MaxAttempts, opts.RetryMaxAttempts))
		if maxAttempts == 1 {
			maxAttempts = opts.RetryMaxAttempts
		}
	}
	if maxAttempts <= 0 {
		maxAttempts = 1
	}

	var last ToolResult
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		attempts = attempt
		started := time.Now().UTC()
		if opts.OnEvent != nil {
			opts.OnEvent(ExecEvent{
				NodeID:   node.ID,
				Tool:     node.Tool,
				Kind:     "start",
				Attempt:  attempt,
				WorkerID: fmt.Sprintf("w%d", workerID),
				At:       started,
			})
		}
		timeout := opts.NodeTimeout
		if node.TimeoutMS > 0 {
			timeout = time.Duration(node.TimeoutMS) * time.Millisecond
		}
		nodeCtx, cancel := context.WithTimeout(ctx, timeout)
		res, err := reg.Execute(nodeCtx, node)
		cancel()
		ended := time.Now().UTC()
		if err != nil {
			last = ToolResult{
				NodeID:     node.ID,
				Tool:       node.Tool,
				Err:        err,
				ErrMessage: strings.TrimSpace(err.Error()),
				StartedAt:  started,
				EndedAt:    ended,
				DurationMS: ended.Sub(started).Milliseconds(),
				Attempts:   attempt,
				WorkerID:   fmt.Sprintf("w%d", workerID),
			}
			if opts.OnEvent != nil {
				opts.OnEvent(ExecEvent{
					NodeID:     node.ID,
					Tool:       node.Tool,
					Kind:       "error",
					Chunk:      strings.TrimSpace(last.ErrMessage),
					Attempt:    attempt,
					WorkerID:   fmt.Sprintf("w%d", workerID),
					DurationMS: last.DurationMS,
					At:         ended,
				})
			}
			if !opts.RetryEnabled || attempt >= maxAttempts || !isRetryableError(err) {
				break
			}
			wait := opts.RetryBackoff*time.Duration(attempt) + time.Duration(rng.Int63n(int64(opts.RetryJitter)+1))
			select {
			case <-time.After(wait):
			case <-ctx.Done():
				last.Err = ctx.Err()
				last.ErrMessage = strings.TrimSpace(ctx.Err().Error())
				return last
			}
			continue
		}
		res.StartedAt = started
		res.EndedAt = ended
		res.DurationMS = ended.Sub(started).Milliseconds()
		res.Citations = extractCitations(res.Output)
		res.Attempts = attempt
		res.WorkerID = fmt.Sprintf("w%d", workerID)
		if opts.OnEvent != nil {
			for _, chunk := range splitOutputChunks(res.Output, 220, 10) {
				opts.OnEvent(ExecEvent{
					NodeID:     node.ID,
					Tool:       node.Tool,
					Kind:       "output",
					Chunk:      chunk,
					Attempt:    attempt,
					WorkerID:   fmt.Sprintf("w%d", workerID),
					DurationMS: res.DurationMS,
					At:         time.Now().UTC(),
				})
			}
			opts.OnEvent(ExecEvent{
				NodeID:     node.ID,
				Tool:       node.Tool,
				Kind:       "complete",
				Attempt:    attempt,
				WorkerID:   fmt.Sprintf("w%d", workerID),
				DurationMS: res.DurationMS,
				At:         ended,
			})
		}
		return res
	}
	last.Attempts = attempts
	if opts.OnEvent != nil {
		opts.OnEvent(ExecEvent{
			NodeID:     node.ID,
			Tool:       node.Tool,
			Kind:       "complete",
			Chunk:      strings.TrimSpace(last.ErrMessage),
			Attempt:    attempts,
			WorkerID:   fmt.Sprintf("w%d", workerID),
			DurationMS: last.DurationMS,
			At:         time.Now().UTC(),
		})
	}
	return last
}

func isRetryableError(err error) bool {
	if err == nil {
		return false
	}
	e := strings.ToLower(strings.TrimSpace(err.Error()))
	return strings.Contains(e, "timeout") || strings.Contains(e, "tempor") || strings.Contains(e, "429") || strings.Contains(e, "502") || strings.Contains(e, "503") || strings.Contains(e, "504")
}

func statusFromResult(r ToolResult) string {
	if r.Err != nil {
		if r.Attempts > 1 {
			return "error_retry_exhausted"
		}
		return "error"
	}
	if r.Attempts > 1 {
		return "ok_retry"
	}
	return "ok"
}

var citationURLRE = regexp.MustCompile(`https?://[^\s"')]+`)

func extractCitations(s string) []string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	matches := citationURLRE.FindAllString(s, -1)
	return uniqueSorted(matches)
}

func uniqueSorted(in []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(in))
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func orderedResults(order []string, byID map[string]ToolResult) []ToolResult {
	ordered := make([]ToolResult, 0, len(byID))
	for _, id := range order {
		if res, ok := byID[id]; ok {
			ordered = append(ordered, res)
		}
	}
	return ordered
}

func splitOutputChunks(out string, maxChunkLen int, maxChunks int) []string {
	out = strings.TrimSpace(out)
	if out == "" || maxChunks <= 0 {
		return nil
	}
	if maxChunkLen <= 0 {
		maxChunkLen = 220
	}
	lines := strings.Split(out, "\n")
	chunks := make([]string, 0, maxChunks)
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		for len(line) > maxChunkLen {
			chunks = append(chunks, line[:maxChunkLen])
			line = strings.TrimSpace(line[maxChunkLen:])
			if len(chunks) >= maxChunks {
				return chunks
			}
		}
		if line != "" {
			chunks = append(chunks, line)
			if len(chunks) >= maxChunks {
				return chunks
			}
		}
	}
	return chunks
}
