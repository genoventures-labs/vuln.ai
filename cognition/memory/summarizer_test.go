package memory

import (
	"context"
	"strings"
	"testing"
)

func TestSourceSummarizerFallbackStructuredBrief(t *testing.T) {
	s := NewSourceSummarizer(nil, SourceSummaryConfig{MaxChars: 500, MaxPoints: 4})
	res, err := s.Summarize(context.Background(), SourceSummaryRequest{
		SourceType: "url",
		SourceRef:  "https://example.com/doc",
		Content:    "This system updates security policies weekly. It may contain deprecated controls. Key actions include rotating keys and validating logs.",
		ChunkCount: 3,
	})
	if err != nil {
		t.Fatalf("summarize failed: %v", err)
	}
	if !res.Fallback {
		t.Fatal("expected fallback summary when no model configured")
	}
	for _, token := range []string{"Gist:", "Key Points:", "Risks/Uncertainty:", "Source Fingerprint:"} {
		if !strings.Contains(res.SummaryText, token) {
			t.Fatalf("expected summary to contain %q, got: %s", token, res.SummaryText)
		}
	}
}

func TestTruncateSummary(t *testing.T) {
	in := strings.Repeat("a", 100)
	out := truncateSummary(in, 20)
	if len(out) > 20 {
		t.Fatalf("expected truncated summary <= 20 chars, got %d", len(out))
	}
	if !strings.HasSuffix(out, "...") {
		t.Fatalf("expected ellipsis suffix, got %q", out)
	}
}
