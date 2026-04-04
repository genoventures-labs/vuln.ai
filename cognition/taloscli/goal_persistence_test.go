package taloscli

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/Thynaptic/P-LMv1/pkg/state"
)

func TestApplyPersistentGoalLockInitializesGoal(t *testing.T) {
	sm, err := state.NewManagerWithPath(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatalf("state manager: %v", err)
	}
	out, note := applyPersistentGoalLock(sm, "Harden STRATA tool-calling safety model", "chat")
	if strings.TrimSpace(out) == "" {
		t.Fatal("expected non-empty locked objective")
	}
	if !strings.Contains(strings.ToLower(note), "initialized") {
		t.Fatalf("expected initialization note, got %q", note)
	}
	if got := strings.TrimSpace(sm.GetSnapshot().PrimaryGoal); got == "" {
		t.Fatal("expected primary goal set")
	}
}

func TestApplyPersistentGoalLockResistsWeakShift(t *testing.T) {
	sm, err := state.NewManagerWithPath(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatalf("state manager: %v", err)
	}
	sm.SetPrimaryGoal("Audit STRATA execution sandbox policy and controls")
	sm.UpdateDelta(map[string]float64{"GoalPersistence": 0.35})
	_ = sm.Save()

	out, note := applyPersistentGoalLock(sm, "do it", "chat")
	if !strings.Contains(strings.ToLower(note), "goal lock active") {
		t.Fatalf("expected lock note, got %q", note)
	}
	if !strings.Contains(strings.ToLower(out), "active objective") {
		t.Fatalf("expected anchored output, got %q", out)
	}
	if !strings.Contains(strings.ToLower(out), "audit strata execution sandbox policy and controls") {
		t.Fatalf("expected active goal in anchored output, got %q", out)
	}
}

func TestApplyPersistentGoalLockAcceptsExplicitOverride(t *testing.T) {
	sm, err := state.NewManagerWithPath(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatalf("state manager: %v", err)
	}
	sm.SetPrimaryGoal("Audit STRATA execution sandbox policy and controls")
	_ = sm.Save()

	override := "switch goal to optimize memory retrieval precision for TALOS"
	out, note := applyPersistentGoalLock(sm, override, "chat")
	if out != override {
		t.Fatalf("expected override to pass through, got %q", out)
	}
	if !strings.Contains(strings.ToLower(note), "explicit override") {
		t.Fatalf("expected explicit override note, got %q", note)
	}
	if got := strings.ToLower(strings.TrimSpace(sm.GetSnapshot().PrimaryGoal)); !strings.Contains(got, "optimize memory retrieval precision") {
		t.Fatalf("expected new primary goal, got %q", got)
	}
}

func TestApplyPersistentGoalLockBypassesStandaloneQuestion(t *testing.T) {
	sm, err := state.NewManagerWithPath(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatalf("state manager: %v", err)
	}
	sm.SetPrimaryGoal("Summarize last learning run")
	_ = sm.Save()

	out, note := applyPersistentGoalLock(sm, "What's Thynaptic?", "chat")
	if out != "What's Thynaptic?" {
		t.Fatalf("expected standalone question passthrough, got %q", out)
	}
	if !strings.Contains(strings.ToLower(note), "bypassed") {
		t.Fatalf("expected bypass note, got %q", note)
	}
}
