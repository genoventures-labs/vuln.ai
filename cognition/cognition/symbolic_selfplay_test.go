package cognition

import "testing"

func TestBuildSelfPlayVectors(t *testing.T) {
	vectors := buildSelfPlayVectors()
	if len(vectors) < 4 {
		t.Fatalf("expected at least 4 self-play vectors, got %d", len(vectors))
	}
	seen := map[string]bool{}
	for _, v := range vectors {
		if v.Name == "" {
			t.Fatal("expected vector name")
		}
		seen[v.Name] = true
	}
	for _, required := range []string{
		"logic_contradiction",
		"evidence_coverage",
		"security_exploitability",
		"implementation_feasibility",
	} {
		if !seen[required] {
			t.Fatalf("missing required vector %q", required)
		}
	}
}

func TestSelectStrongestSelfPlayBranch(t *testing.T) {
	branches := []selfPlayBranch{
		{vectorName: "logic_contradiction", finalCandidate: "A", maxFlawScore: 0.52, contradictions: 1, cycles: 1, branchScore: scoreSelfPlayBranch(0.52, 1, 1)},
		{vectorName: "evidence_coverage", finalCandidate: "B", maxFlawScore: 0.21, contradictions: 0, cycles: 1, branchScore: scoreSelfPlayBranch(0.21, 0, 1)},
		{vectorName: "security_exploitability", finalCandidate: "C", maxFlawScore: 0.34, contradictions: 2, cycles: 0, branchScore: scoreSelfPlayBranch(0.34, 2, 0)},
	}
	best, ok := selectStrongestSelfPlayBranch(branches)
	if !ok {
		t.Fatal("expected strongest branch")
	}
	if best.vectorName != "evidence_coverage" {
		t.Fatalf("expected evidence_coverage winner, got %q", best.vectorName)
	}
	if best.finalCandidate != "B" {
		t.Fatalf("expected candidate B, got %q", best.finalCandidate)
	}
}
