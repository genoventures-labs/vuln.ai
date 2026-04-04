package cognition

import "testing"

func TestRunReflectionV2CitationGate(t *testing.T) {
	p := DefaultReflectionPolicy("")
	p.CitationGate = true
	p.WarnThreshold = 0.1
	p.SteerThreshold = 0.3
	p.VetoThreshold = 0.9

	d, err := RunReflectionV2(ReflectionInput{
		Stage:      StageResearchSynthesis,
		Goal:       "summarize security change",
		Query:      "summarize security change",
		Candidate:  "Security posture improved with new controls.",
		SourceRefs: []string{"https://example.com/a"},
	}, p)
	if err != nil {
		t.Fatalf("run reflection: %v", err)
	}
	if d.Outcome == ReflectionPass {
		t.Fatalf("expected non-pass due to missing citation markers, got %+v", d)
	}
}

func TestRunReflectionV2PassesWithCitationMarkers(t *testing.T) {
	p := DefaultReflectionPolicy("")
	p.CitationGate = true
	p.WarnThreshold = 0.3
	p.SteerThreshold = 0.6
	p.VetoThreshold = 0.9

	d, err := RunReflectionV2(ReflectionInput{
		Stage:      StageResearchSynthesis,
		Goal:       "summarize security change",
		Query:      "summarize security change",
		Candidate:  "Security posture improved [1].",
		SourceRefs: []string{"https://example.com/a"},
	}, p)
	if err != nil {
		t.Fatalf("run reflection: %v", err)
	}
	if d.Outcome != ReflectionPass && d.Outcome != ReflectionWarn {
		t.Fatalf("expected pass/warn, got %+v", d)
	}
}
