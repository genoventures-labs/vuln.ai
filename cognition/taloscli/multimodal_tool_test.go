package taloscli

import "testing"

func TestBuildMultimodalStepsIncludesVisionMemoryWeb(t *testing.T) {
	args := map[string]interface{}{
		"query":   "find sasswall regression source",
		"consent": true,
		"target": map[string]interface{}{
			"label":  "status badge",
			"x":      10,
			"y":      20,
			"width":  120,
			"height": 70,
		},
	}
	steps, shards := buildMultimodalSteps(args, "find sasswall regression source")
	if len(steps) < 3 {
		t.Fatalf("expected >=3 multimodal steps, got %d", len(steps))
	}
	if len(shards) < 3 {
		t.Fatalf("expected >=3 shards, got %d", len(shards))
	}
	if steps[0].Call.Tool != "analyze_visual_target" {
		t.Fatalf("expected first step to be analyze_visual_target, got %s", steps[0].Call.Tool)
	}
}

func TestExecuteMultimodalToolDisabled(t *testing.T) {
	t.Setenv(multimodalToolsEnabledEnv, "false")
	_, err := executeMultimodalTool(nil, toolInvocation{
		Tool: "multimodal_tool",
		Args: map[string]interface{}{"query": "x"},
	})
	if err == nil {
		t.Fatal("expected disabled multimodal tool error")
	}
}

func TestResolveMultimodalQueryFallbackFromTarget(t *testing.T) {
	q := resolveMultimodalQuery(map[string]interface{}{
		"target": map[string]interface{}{
			"label":   "checkout button",
			"snippet": "Payment failed",
		},
	}, artifactBundle{})
	if q == "" {
		t.Fatal("expected query from visual target fallback")
	}
}
