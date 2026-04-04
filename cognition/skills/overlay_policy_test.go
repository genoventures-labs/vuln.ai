package skills

import "testing"

func TestEvaluateOverlayCandidatesQuietSuppressed(t *testing.T) {
	p := DefaultOverlayPolicy()
	p.DefaultMode = "quiet"
	dec := EvaluateOverlayCandidates([]OverlayCandidate{
		{
			Kind:       OverlayKindEvidence,
			Text:       "proof link",
			X:          100,
			Y:          100,
			W:          200,
			H:          120,
			Confidence: 0.95,
			Priority:   OverlayPriorityHigh,
		},
	}, OverlayContext{Explicit: false, HighRiskScore: 0.2}, p)
	if dec.Render {
		t.Fatalf("expected quiet mode suppression")
	}
}

func TestEvaluateOverlayCandidatesExplicitRender(t *testing.T) {
	p := DefaultOverlayPolicy()
	p.DefaultMode = "on_demand"
	dec := EvaluateOverlayCandidates([]OverlayCandidate{
		{
			Kind:       OverlayKindTarget,
			Text:       "focus node",
			X:          10,
			Y:          10,
			W:          100,
			H:          80,
			Confidence: 0.90,
			Priority:   OverlayPriorityHigh,
		},
	}, OverlayContext{Explicit: true, HighRiskScore: 0.1}, p)
	if !dec.Render {
		t.Fatalf("expected explicit render")
	}
	if len(dec.Selected) == 0 {
		t.Fatalf("expected selected candidates")
	}
}
