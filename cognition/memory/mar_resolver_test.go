package memory

import (
	"strings"
	"testing"
	"time"
)

func TestDefaultMARPolicyParsesWeightsAndBounds(t *testing.T) {
	t.Setenv(marWeightsEnv, "2,1,1,0,0")
	t.Setenv(marCandidateLimitEnv, "4")
	t.Setenv(marMaxAnchorsEnv, "2")
	t.Setenv(marMinAnchorScoreEnv, "0.15")
	t.Setenv(marTopologyBoostEnv, "0.2")

	p := defaultMARPolicy()
	if !p.Enabled {
		t.Fatalf("expected MAR enabled by default")
	}
	if p.CandidateLimit != 8 {
		t.Fatalf("expected candidate floor=8, got %d", p.CandidateLimit)
	}
	if p.MaxAnchors != 4 {
		t.Fatalf("expected max anchors floor=4, got %d", p.MaxAnchors)
	}
	if p.WeightSemantic <= p.WeightLexical {
		t.Fatalf("expected semantic weight > lexical weight, got %+v", p)
	}
	if p.MinAnchorScore < 0.15 || p.TopologyBoost < 0.19 {
		t.Fatalf("expected custom score/boost values, got %+v", p)
	}
}

func TestScoreAnchoredCandidatesFiltersWeakAnchors(t *testing.T) {
	policy := defaultMARPolicy()
	policy.MinAnchorScore = 0.5
	policy.TopologyBoost = 0

	now := time.Now().UTC()
	cands := []retrievalCandidate{
		{
			ID:       "strong",
			Content:  "strong anchor",
			Semantic: 0.95,
			Lexical:  0.75,
			Metadata: map[string]string{metaBaseImportance: "0.9", metaTimestamp: now.Format(time.RFC3339)},
		},
		{
			ID:       "weak",
			Content:  "weak anchor",
			Semantic: 0.1,
			Lexical:  0.1,
			Metadata: map[string]string{metaBaseImportance: "0.1"},
		},
	}

	anchors := scoreAnchoredCandidates(cands, anchorKindKnowledge, now, policy)
	if len(anchors) != 1 {
		t.Fatalf("expected one anchor above threshold, got %d", len(anchors))
	}
	if anchors[0].ID != "strong" {
		t.Fatalf("expected strong anchor first, got %q", anchors[0].ID)
	}
}

func TestResolveAnchoredContextCacheHit(t *testing.T) {
	mm := &MemoryManager{
		marPolicy: defaultMARPolicy(),
		marCache:  newMARContextCache(),
	}

	first, err := mm.ResolveAnchoredContext("cache me", 0, 0)
	if err != nil {
		t.Fatalf("resolve first call: %v", err)
	}
	if first.CacheHit {
		t.Fatalf("first call should not be cache hit")
	}

	second, err := mm.ResolveAnchoredContext("cache me", 0, 0)
	if err != nil {
		t.Fatalf("resolve second call: %v", err)
	}
	if !second.CacheHit {
		t.Fatalf("second call should be cache hit")
	}
}

func TestAnchoredStatusReportIncludesShards(t *testing.T) {
	ctx := AnchoredContext{
		Query:          "test query",
		CandidateCount: 3,
		History:        []string{"h1"},
		Knowledge:      []string{"k1"},
		Anchors: []AnchoredTrace{{
			ID:      "a1",
			Kind:    anchorKindKnowledge,
			Content: "segment",
			Score:   0.82,
			Metadata: map[string]string{
				"source": "docs/spec.md",
			},
		}},
	}
	report := ctx.AnchoredStatusReport()
	for _, token := range []string{"MAR STATUS", "active_shards", "docs/spec.md"} {
		if !strings.Contains(report, token) {
			t.Fatalf("expected report to contain %q, got: %s", token, report)
		}
	}
}
