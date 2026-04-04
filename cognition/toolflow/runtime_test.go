package toolflow

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestRuntimeFailClosedValidation(t *testing.T) {
	r := &Runtime{
		Policy:   NewStrictPolicy(DefaultToolSpecs()),
		Registry: NewRegistry(),
		ExecOpts: ExecOptions{FailClosed: true, MaxWorkers: 2, NodeTimeout: time.Second},
		PlanOpts: PlanOptions{SequentialDefault: true},
	}
	_ = r.Registry.Register("web_search", func(ctx context.Context, args map[string]interface{}) (string, error) {
		return "ok", nil
	})
	_, _, _, err := r.Run(context.Background(), "t1", []Invocation{{Tool: "web_search", Args: map[string]interface{}{}}})
	if err == nil {
		t.Fatal("expected validation error")
	}
	if !strings.Contains(err.Error(), "MISSING_REQUIRED_ARG") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRuntimeExecuteDeterministic(t *testing.T) {
	r := &Runtime{
		Policy:   NewStrictPolicy(DefaultToolSpecs()),
		Registry: NewRegistry(),
		ExecOpts: ExecOptions{FailClosed: true, MaxWorkers: 2, NodeTimeout: time.Second},
		PlanOpts: PlanOptions{SequentialDefault: true},
	}
	_ = r.Registry.Register("web_search", func(ctx context.Context, args map[string]interface{}) (string, error) {
		return "https://example.com/a", nil
	})
	_ = r.Registry.Register("fetch_url", func(ctx context.Context, args map[string]interface{}) (string, error) {
		return "https://example.com/b", nil
	})
	plan, results, trace, err := r.Run(context.Background(), "t2", []Invocation{
		{Tool: "web_search", Args: map[string]interface{}{"query": "a"}},
		{Tool: "fetch_url", Args: map[string]interface{}{"url": "https://example.com"}},
	})
	if err != nil {
		t.Fatalf("run failed: %v", err)
	}
	if len(plan.Nodes) != 2 || len(results) != 2 {
		t.Fatalf("unexpected plan/results sizes: %d %d", len(plan.Nodes), len(results))
	}
	if len(trace.Citations) == 0 {
		t.Fatalf("expected citations from outputs")
	}
	status := FormatASCIIStatus(plan, trace, results, 2)
	if !strings.Contains(status, "TOOLFLOW STATUS") {
		t.Fatalf("unexpected status: %s", status)
	}
}
