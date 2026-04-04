package taloscli

import "testing"

func TestParseToolCallsAutoTool(t *testing.T) {
	in := `{"tool":"auto_tool","args":{"goal":"compare memory paths and then fetch docs"}}`
	calls, ok := parseToolCalls(in)
	if !ok || len(calls) != 1 {
		t.Fatalf("expected one parsed call, got ok=%v calls=%d", ok, len(calls))
	}
	if calls[0].Tool != "auto_tool" {
		t.Fatalf("expected auto_tool, got %s", calls[0].Tool)
	}
}

func TestSynthesizeAutoToolStepsRecursiveSplit(t *testing.T) {
	steps := synthesizeAutoToolSteps("compare APIs across services and then fetch https://example.com/docs", 0, 3)
	if len(steps) < 2 {
		t.Fatalf("expected at least 2 steps, got %d", len(steps))
	}
	foundRecursive := false
	for _, s := range steps {
		if s.Tool == "auto_tool" {
			foundRecursive = true
			break
		}
	}
	if !foundRecursive {
		t.Fatalf("expected recursive auto_tool step in synthesized chain: %+v", steps)
	}
}

func TestExecuteRecursiveAutoToolDisabled(t *testing.T) {
	t.Setenv(autoToolsEnabledEnv, "false")
	_, err := executeRecursiveAutoTool(nil, toolInvocation{Tool: "auto_tool", Args: map[string]interface{}{"goal": "fetch docs"}}, 0)
	if err == nil {
		t.Fatal("expected disabled auto tools error")
	}
}
