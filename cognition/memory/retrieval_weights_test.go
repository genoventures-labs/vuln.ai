package memory

import "testing"

func TestParseWeightListEnvNormalizes(t *testing.T) {
	t.Setenv(candidateWeightsEnv, "3,1")
	got := parseWeightListEnv(candidateWeightsEnv, []float64{0.6, 0.4})
	if len(got) != 2 {
		t.Fatalf("expected 2 weights, got %d", len(got))
	}
	if got[0] < 0.74 || got[0] > 0.76 {
		t.Fatalf("expected normalized first weight ~0.75, got %.4f", got[0])
	}
	if got[1] < 0.24 || got[1] > 0.26 {
		t.Fatalf("expected normalized second weight ~0.25, got %.4f", got[1])
	}
}

func TestParseWeightListEnvFallsBackOnInvalid(t *testing.T) {
	fallback := []float64{0.4, 0.6}
	t.Setenv(candidateWeightsEnv, "bad,input")
	got := parseWeightListEnv(candidateWeightsEnv, fallback)
	if len(got) != len(fallback) {
		t.Fatalf("expected fallback length %d, got %d", len(fallback), len(got))
	}
	for i := range fallback {
		if got[i] != fallback[i] {
			t.Fatalf("expected fallback[%d]=%.2f, got %.2f", i, fallback[i], got[i])
		}
	}
}

func TestResolveRetrievalWeightsUsesEnv(t *testing.T) {
	t.Setenv(candidateWeightsEnv, "1,1")
	t.Setenv(dynamicWeightsEnv, "2,1,1,0")
	t.Setenv(knowledgeWeightsEnv, "1,3")
	t.Setenv(segmentWeightsEnv, "4,1")

	w := resolveRetrievalWeights()
	if w.CandidateSemantic != 0.5 || w.CandidateLexical != 0.5 {
		t.Fatalf("unexpected candidate weights: %+v", w)
	}
	if w.DynamicSemantic <= w.DynamicLexical {
		t.Fatalf("expected dynamic semantic > lexical, got %+v", w)
	}
	if w.KnowledgeLexical <= w.KnowledgeSemantic {
		t.Fatalf("expected knowledge lexical > semantic, got %+v", w)
	}
	if w.SegmentSemantic <= w.SegmentLexical {
		t.Fatalf("expected segment semantic > lexical, got %+v", w)
	}
}
