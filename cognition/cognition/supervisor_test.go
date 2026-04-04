package cognition

import "testing"

func TestRunSymbolicSupervision_HardVetoSecretLeak(t *testing.T) {
	decision, err := RunSymbolicSupervision(SupervisionInput{
		Stage:     StageSynthesis,
		Query:     "summarize",
		Candidate: "GLM_API_KEY=abcdefghijklmnopqrstuvwxyz123456",
	}, DefaultSupervisionPolicy("balanced"))
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if decision.Outcome != SupervisionHardVeto {
		t.Fatalf("expected hard veto, got %s", decision.Outcome)
	}
}

func TestRunSymbolicSupervision_SoftWarnOnMissingSources(t *testing.T) {
	decision, err := RunSymbolicSupervision(SupervisionInput{
		Stage:      StageResearchFinding,
		Query:      "policy changes",
		Candidate:  "Kubernetes policy changed in production and all clusters are updated.",
		SourceRefs: nil,
	}, DefaultSupervisionPolicy("balanced"))
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if decision.Outcome != SupervisionSoftWarn {
		t.Fatalf("expected soft warn, got %s", decision.Outcome)
	}
}

func TestRunSymbolicSupervision_CacheHit(t *testing.T) {
	in := SupervisionInput{
		Stage:     StageSynthesis,
		Query:     "test",
		Candidate: "This is a concise technical answer.",
	}
	p := DefaultSupervisionPolicy("balanced")
	first, err := RunSymbolicSupervision(in, p)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	second, err := RunSymbolicSupervision(in, p)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if first.Outcome != second.Outcome {
		t.Fatalf("expected consistent outcomes, got %s vs %s", first.Outcome, second.Outcome)
	}
	if !second.Cached {
		t.Fatalf("expected cache hit on second call")
	}
}
