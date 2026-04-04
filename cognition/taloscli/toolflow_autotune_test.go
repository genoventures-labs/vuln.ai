package taloscli

import (
	"path/filepath"
	"testing"

	"github.com/Thynaptic/P-LMv1/pkg/toolflow"
)

func TestToolflowAutotuneApplyCapsAndDefaults(t *testing.T) {
	path := filepath.Join(t.TempDir(), "autotune.json")
	opt, err := newToolflowParamOptimizer(path)
	if err != nil {
		t.Fatalf("newToolflowParamOptimizer: %v", err)
	}
	inv := []toolflow.Invocation{
		{Tool: "web_search", Args: map[string]interface{}{"query": "incident", "top_k": 12}},
		{Tool: "fetch_url", Args: map[string]interface{}{"url": "https://example.com", "max_chars": 99999}},
		{Tool: "execute_code", Args: map[string]interface{}{"language": "bash", "code": "echo ok", "timeout_seconds": 50}},
	}
	out, notes, err := opt.Apply(inv, 0.93)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if got := getIntArg(out[0].Args, "top_k"); got != 4 {
		t.Fatalf("expected web_search top_k=4, got %d", got)
	}
	if got := getIntArg(out[1].Args, "max_chars"); got != 8000 {
		t.Fatalf("expected fetch_url max_chars=8000, got %d", got)
	}
	if got := getIntArg(out[2].Args, "timeout_seconds"); got != 12 {
		t.Fatalf("expected execute_code timeout_seconds=12, got %d", got)
	}
	if len(notes) == 0 {
		t.Fatal("expected non-empty autotune apply notes")
	}
}

func TestToolflowAutotuneLearnsAndPersists(t *testing.T) {
	path := filepath.Join(t.TempDir(), "autotune.json")
	opt, err := newToolflowParamOptimizer(path)
	if err != nil {
		t.Fatalf("newToolflowParamOptimizer: %v", err)
	}
	plan := toolflow.Plan{
		Nodes: []toolflow.PlanNode{
			{ID: "n1", Tool: "execute_code", Args: map[string]interface{}{"language": "bash", "code": "sleep 1", "timeout_seconds": 8}},
		},
	}
	results := []toolflow.ToolResult{
		{NodeID: "n1", Tool: "execute_code", ErrMessage: "temporary timeout", DurationMS: 8200},
	}
	learned, err := opt.Learn(plan, results, 0.60)
	if err != nil {
		t.Fatalf("Learn: %v", err)
	}
	if len(learned) == 0 {
		t.Fatal("expected learning notes")
	}

	// Re-open to ensure persisted recommendations are loaded.
	opt2, err := newToolflowParamOptimizer(path)
	if err != nil {
		t.Fatalf("reload optimizer: %v", err)
	}
	out, _, err := opt2.Apply([]toolflow.Invocation{
		{Tool: "execute_code", Args: map[string]interface{}{"language": "bash", "code": "echo ok"}},
	}, 0.60)
	if err != nil {
		t.Fatalf("Apply after reload: %v", err)
	}
	got := getIntArg(out[0].Args, "timeout_seconds")
	if got < 10 {
		t.Fatalf("expected learned timeout_seconds >= 10, got %d", got)
	}
}

func TestToolflowAutotuneDepthCapsAndLearning(t *testing.T) {
	path := filepath.Join(t.TempDir(), "autotune.json")
	opt, err := newToolflowParamOptimizer(path)
	if err != nil {
		t.Fatalf("newToolflowParamOptimizer: %v", err)
	}

	inv := []toolflow.Invocation{
		{Tool: "sys_exec", Args: map[string]interface{}{"command": "scan", "crawl_depth": 9, "max_pages": 999}},
	}
	out, notes, err := opt.Apply(inv, 0.91)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if got := getIntArg(out[0].Args, "crawl_depth"); got != 2 {
		t.Fatalf("expected crawl_depth=2 under high load, got %d", got)
	}
	if got := getIntArg(out[0].Args, "max_pages"); got != 30 {
		t.Fatalf("expected max_pages=30 under high load, got %d", got)
	}
	if len(notes) == 0 {
		t.Fatal("expected depth cap notes")
	}

	plan := toolflow.Plan{
		Nodes: []toolflow.PlanNode{
			{ID: "n2", Tool: "sys_exec", Args: map[string]interface{}{"command": "scan", "crawl_depth": 2, "max_pages": 30}},
		},
	}
	results := []toolflow.ToolResult{
		{NodeID: "n2", Tool: "sys_exec", DurationMS: 1200, ErrMessage: "temporary timeout"},
	}
	learned, err := opt.Learn(plan, results, 0.91)
	if err != nil {
		t.Fatalf("Learn: %v", err)
	}
	if len(learned) == 0 {
		t.Fatal("expected depth learning notes")
	}

	opt2, err := newToolflowParamOptimizer(path)
	if err != nil {
		t.Fatalf("reload optimizer: %v", err)
	}
	out2, _, err := opt2.Apply([]toolflow.Invocation{
		{Tool: "sys_exec", Args: map[string]interface{}{"command": "scan", "crawl_depth": 5, "max_pages": 100}},
	}, 0.91)
	if err != nil {
		t.Fatalf("Apply after reload: %v", err)
	}
	if got := getIntArg(out2[0].Args, "crawl_depth"); got > 2 {
		t.Fatalf("expected tuned crawl_depth <= 2 under high load, got %d", got)
	}
	if got := getIntArg(out2[0].Args, "max_pages"); got > 30 {
		t.Fatalf("expected tuned max_pages <= 30 under high load, got %d", got)
	}
}
