package skills

import "testing"

func TestRankOverlayCandidatesPriorityOrder(t *testing.T) {
	in := []OverlayCandidate{
		{Kind: OverlayKindEvidence, Text: "e", Confidence: 0.7, Priority: OverlayPriorityLow},
		{Kind: OverlayKindContradiction, Text: "c", Confidence: 0.7, Priority: OverlayPriorityHigh},
	}
	out := rankOverlayCandidates(in)
	if len(out) != 2 {
		t.Fatalf("expected 2 candidates")
	}
	if out[0].Kind != OverlayKindContradiction {
		t.Fatalf("expected contradiction first, got %q", out[0].Kind)
	}
}
