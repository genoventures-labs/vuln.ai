package toolflow

import "testing"

func TestBuildDeterministicPlanV2InfersFetchDependency(t *testing.T) {
	plan, err := BuildDeterministicPlanV2([]NormalizedInvocation{
		{Tool: "web_search", Args: map[string]interface{}{"query": "foo"}},
		{Tool: "fetch_url", Args: map[string]interface{}{}},
	}, PlanOptions{SequentialDefault: false}, PlannerHints{})
	if err != nil {
		t.Fatalf("plan failed: %v", err)
	}
	if len(plan.Nodes) != 2 {
		t.Fatalf("unexpected node count: %d", len(plan.Nodes))
	}
	if len(plan.Nodes[1].DependsOn) == 0 {
		t.Fatalf("expected inferred dependency on prior search node")
	}
	if plan.InferredEdgeCount == 0 {
		t.Fatalf("expected inferred edge count > 0")
	}
}

func TestBuildDeterministicPlanV2SerializesSharedTarget(t *testing.T) {
	plan, err := BuildDeterministicPlanV2([]NormalizedInvocation{
		{Tool: "execute_code", Args: map[string]interface{}{"output_path": "/tmp/a.txt"}},
		{Tool: "execute_code", Args: map[string]interface{}{"output_path": "/tmp/a.txt"}},
	}, PlanOptions{SequentialDefault: false}, PlannerHints{})
	if err != nil {
		t.Fatalf("plan failed: %v", err)
	}
	if len(plan.Nodes[1].DependsOn) == 0 {
		t.Fatalf("expected shared-target dependency")
	}
}
