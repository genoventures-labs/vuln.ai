package orchestration

import (
	"testing"

	"github.com/Thynaptic/P-LMv1/pkg/memory"
)

func TestBuildSectionCrossLinksFromSegments(t *testing.T) {
	segments := []memory.KnowledgeSegment{
		{
			ID:      "a1",
			Content: "JWT token flow with auth middleware and session verification.",
			Metadata: map[string]string{
				"source_path":   "docs/auth.md",
				"section_id":    "auth-flow",
				"section_title": "Auth Flow",
			},
			Similarity: 0.8,
		},
		{
			ID:      "b1",
			Content: "Gateway validates JWT token and forwards authenticated identity.",
			Metadata: map[string]string{
				"source_path":   "docs/gateway.md",
				"section_id":    "gateway-auth",
				"section_title": "Gateway Auth",
			},
			Similarity: 0.75,
		},
	}

	links := BuildSectionCrossLinksFromSegments(segments, 12)
	if len(links) == 0 {
		t.Fatal("expected cross-sectional links")
	}
	if links[0].SourceA == "" || links[0].SourceB == "" {
		t.Fatalf("expected populated link sources: %+v", links[0])
	}
}
