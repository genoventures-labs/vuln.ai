package taloscli

import (
	"strings"
	"testing"
	"time"

	"github.com/Thynaptic/P-LMv1/pkg/orchestration"
)

func TestParseAuditDateFlag(t *testing.T) {
	cases := []string{"2025", "2025-01-01", "2025-01-01T00:00:00Z"}
	for _, c := range cases {
		if _, err := parseAuditDateFlag(c); err != nil {
			t.Fatalf("expected parse success for %q: %v", c, err)
		}
	}
	if _, err := parseAuditDateFlag("2025/01/01"); err == nil {
		t.Fatal("expected parse failure for invalid date format")
	}
}

func TestRenderBeliefAuditReportShowsWinnerFlip(t *testing.T) {
	base := orchestration.WorldviewTruthState{
		UpdatedAt:     time.Date(2025, 1, 2, 0, 0, 0, 0, time.UTC),
		Query:         "STRATA architecture baseline",
		TruthHash:     "aaaa1111",
		ConflictIndex: 0.22,
		ResolutionAnchors: []orchestration.WorldviewTruthAnchor{
			{Issue: "Tool dispatch layer", Winner: "Legacy System Reality"},
		},
	}
	curr := orchestration.WorldviewTruthState{
		UpdatedAt:     time.Date(2026, 2, 18, 0, 0, 0, 0, time.UTC),
		Query:         "STRATA architecture updated",
		TruthHash:     "bbbb2222",
		ConflictIndex: 0.66,
		ResolutionAnchors: []orchestration.WorldviewTruthAnchor{
			{Issue: "Tool dispatch layer", Winner: "Desired State Spec"},
		},
	}
	report := renderBeliefAuditReport("STRATA architecture", time.Time{}, time.Time{}, 30, []orchestration.WorldviewTruthState{base, curr})
	for _, token := range []string{
		"EPISTEMIC ALIGNMENT AUDIT",
		"worldview_shift: changed",
		"flip: Tool dispatch layer | Legacy System Reality -> Desired State Spec",
		"2025-01-02T00:00:00Z",
		"2026-02-18T00:00:00Z",
	} {
		if !strings.Contains(report, token) {
			t.Fatalf("expected token %q in report, got:\n%s", token, report)
		}
	}
}

func TestFilterWorldviewHistoryByTopicAndWindow(t *testing.T) {
	in := []orchestration.WorldviewTruthState{
		{UpdatedAt: time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC), Query: "alpha"},
		{UpdatedAt: time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC), Query: "strata architecture"},
		{UpdatedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), Query: "strata roadmap"},
	}
	since := time.Date(2025, 5, 1, 0, 0, 0, 0, time.UTC)
	until := time.Date(2025, 12, 31, 0, 0, 0, 0, time.UTC)
	out := filterWorldviewHistory("strata", since, until, in)
	if len(out) != 1 {
		t.Fatalf("expected 1 filtered item, got %d", len(out))
	}
	if !strings.Contains(strings.ToLower(out[0].Query), "strata") {
		t.Fatalf("expected strata item, got %+v", out[0])
	}
}
