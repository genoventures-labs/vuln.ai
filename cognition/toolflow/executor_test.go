package toolflow

import (
	"context"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestExecutePlanV2RetriesTransientFailure(t *testing.T) {
	reg := NewRegistry()
	var calls int32
	err := reg.Register("web_search", func(ctx context.Context, args map[string]interface{}) (string, error) {
		n := atomic.AddInt32(&calls, 1)
		if n == 1 {
			return "", fmt.Errorf("temporary timeout")
		}
		return "https://example.com", nil
	})
	if err != nil {
		t.Fatalf("register failed: %v", err)
	}
	plan := Plan{Nodes: []PlanNode{{
		ID: "n01_web_search", Tool: "web_search", Args: map[string]interface{}{"query": "q"}, RetryPolicy: RetryPolicySpec{MaxAttempts: 2},
	}}, OrderedIDs: []string{"n01_web_search"}, DeterministicHash: "x"}
	results, trace, err := ExecutePlanV2(context.Background(), plan, reg, ExecOptions{
		MaxWorkers:       1,
		NodeTimeout:      time.Second,
		GlobalBudget:     3 * time.Second,
		RetryEnabled:     true,
		RetryMaxAttempts: 2,
		RetryBackoff:     1 * time.Millisecond,
		RetryJitter:      0,
		FailClosed:       true,
	})
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected one result, got %d", len(results))
	}
	if results[0].Attempts != 2 {
		t.Fatalf("expected two attempts, got %d", results[0].Attempts)
	}
	if len(trace.Steps) == 0 {
		t.Fatalf("expected trace steps")
	}
}

func TestExecutePlanV2StreamEventsAndPartialCancel(t *testing.T) {
	reg := NewRegistry()
	if err := reg.Register("web_search", func(ctx context.Context, args map[string]interface{}) (string, error) {
		return "critical contradiction found\nsmoking gun: credential exposed", nil
	}); err != nil {
		t.Fatalf("register web_search: %v", err)
	}
	if err := reg.Register("fetch_url", func(ctx context.Context, args map[string]interface{}) (string, error) {
		return "should not run", nil
	}); err != nil {
		t.Fatalf("register fetch_url: %v", err)
	}
	plan := Plan{
		Nodes: []PlanNode{
			{ID: "n1", Tool: "web_search", Args: map[string]interface{}{"query": "q"}, RetryPolicy: RetryPolicySpec{MaxAttempts: 1}},
			{ID: "n2", Tool: "fetch_url", Args: map[string]interface{}{"url": "https://example.com"}, DependsOn: []string{"n1"}, RetryPolicy: RetryPolicySpec{MaxAttempts: 1}},
		},
		OrderedIDs:        []string{"n1", "n2"},
		DeterministicHash: "x2",
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var sawOutput atomic.Bool
	results, _, err := ExecutePlanV2(ctx, plan, reg, ExecOptions{
		MaxWorkers:   1,
		NodeTimeout:  time.Second,
		GlobalBudget: 3 * time.Second,
		FailClosed:   true,
		OnEvent: func(ev ExecEvent) {
			if ev.Kind == "output" && strings.Contains(strings.ToLower(ev.Chunk), "smoking gun") {
				sawOutput.Store(true)
				cancel()
			}
		},
	})
	if err == nil {
		t.Fatalf("expected cancellation error")
	}
	if !sawOutput.Load() {
		t.Fatalf("expected output stream event before cancellation")
	}
	if len(results) != 1 || results[0].NodeID != "n1" {
		t.Fatalf("expected partial results with first node only, got %+v", results)
	}
}
