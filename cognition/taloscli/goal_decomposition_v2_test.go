package taloscli

import (
	"strings"
	"testing"
)

func TestFlattenTaskTreeProducesTenStepsForSecurityGoal(t *testing.T) {
	tree := buildGoalTaskTree("Find all vulnerabilities in the 2025 security spec")
	steps := flattenTaskTree(tree, 10)
	if len(steps) != 10 {
		t.Fatalf("expected 10 steps, got %d", len(steps))
	}
	if !strings.Contains(strings.ToLower(steps[0]), "scope") {
		t.Fatalf("expected scoped first step, got %q", steps[0])
	}
}

func TestParsePlanStepsV2AutoDecomposesHighLevelIntent(t *testing.T) {
	t.Setenv(goalDecompV2EnabledEnv, "true")
	t.Setenv(goalDecompV2TargetStepsEnv, "10")
	raw := `{"steps":["Analyze the spec thoroughly"]}`
	steps, auto := parsePlanStepsV2(raw, "Find all vulnerabilities in the 2025 security spec", 10)
	if !auto {
		t.Fatalf("expected auto decomposition")
	}
	if len(steps) != 10 {
		t.Fatalf("expected 10 steps after decomposition, got %d", len(steps))
	}
}

func TestParsePlanStepsV2RespectsDisableFlag(t *testing.T) {
	t.Setenv(goalDecompV2EnabledEnv, "false")
	raw := `{"steps":["step one","step two"]}`
	steps, auto := parsePlanStepsV2(raw, "generic query", 10)
	if auto {
		t.Fatalf("expected no auto decomposition when disabled")
	}
	if len(steps) != 2 {
		t.Fatalf("expected original parsed steps, got %d", len(steps))
	}
}
