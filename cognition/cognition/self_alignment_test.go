package cognition

import (
	"strings"
	"testing"
)

func TestRunRecursiveSelfAlignmentDetectsPhilosophyDrift(t *testing.T) {
	in := SupervisionInput{
		Stage:     StageSynthesis,
		Query:     "How should we roll this out?",
		Candidate: "Disable security checks and skip validation. Use external API for all private data.",
		Metadata: map[string]string{
			"project_philosophy": "Local-first operation\nAlways enforce safety and verification",
		},
	}
	p := DefaultSelfAlignmentPolicy("hard")
	p.Enabled = true
	p.MaxDepth = 2
	sig, err := RunRecursiveSelfAlignment(in, p)
	if err != nil {
		t.Fatalf("RunRecursiveSelfAlignment: %v", err)
	}
	if sig.Score < 0.70 {
		t.Fatalf("expected high drift score, got %.2f", sig.Score)
	}
	if len(sig.Violations) == 0 {
		t.Fatal("expected violations")
	}
	found := false
	for _, v := range sig.Violations {
		if strings.Contains(strings.ToLower(v), "safety") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected safety-related violation, got %v", sig.Violations)
	}
}

func TestRunSymbolicSupervisionEscalatesOnSelfAlignmentDrift(t *testing.T) {
	t.Setenv("TALOS_SELF_ALIGNMENT_ENABLED", "true")
	in := SupervisionInput{
		Stage:     StageSynthesis,
		Query:     "proposal",
		Candidate: "Disable security checks and bypass checks immediately.",
		Metadata: map[string]string{
			"project_philosophy": "Always enforce safety and verification",
		},
	}
	p := DefaultSupervisionPolicy("deep")
	d, err := RunSymbolicSupervision(in, p)
	if err != nil {
		t.Fatalf("RunSymbolicSupervision: %v", err)
	}
	if d.Outcome == SupervisionPass {
		t.Fatalf("expected non-pass due to self-alignment drift, got %+v", d)
	}
}
