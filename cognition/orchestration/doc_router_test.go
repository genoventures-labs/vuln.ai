package orchestration

import (
	"os"
	"strings"
	"testing"

	"github.com/Thynaptic/P-LMv1/pkg/memory"
)

func TestParseRouteWeightsEnv(t *testing.T) {
	const key = "TALOS_DOC_ROUTING_WEIGHTS_TEST"
	_ = os.Setenv(key, "0.5,0.2,0.2,0.1")
	defer os.Unsetenv(key)

	got := parseRouteWeightsEnv(key, []float64{0.45, 0.25, 0.20, 0.10})
	if len(got) != 4 {
		t.Fatalf("expected 4 weights, got %d", len(got))
	}
	sum := got[0] + got[1] + got[2] + got[3]
	if sum < 0.99 || sum > 1.01 {
		t.Fatalf("weights should normalize to 1.0, got %.4f", sum)
	}
}

func TestCompactRouteReportLines(t *testing.T) {
	report := RouteReport{
		SelectedDocs: []RoutedDoc{
			{SourceRef: "docs/a.md", Score: 0.82, SegmentCount: 4, Reasons: []string{"semantic_high", "lexical_match"}},
			{SourceRef: "docs/b.md", Score: 0.75, SegmentCount: 3, Reasons: []string{"topology_boost"}},
		},
	}
	lines := CompactRouteReportLines(report, 5)
	if len(lines) < 2 {
		t.Fatalf("expected at least 2 lines, got %d", len(lines))
	}
	if !strings.Contains(lines[0], "docs/a.md") {
		t.Fatalf("expected first line to include source ref, got %q", lines[0])
	}
}

func TestTokenizeRouteText(t *testing.T) {
	toks := tokenizeRouteText("Architecture and README mapping across docs.")
	if toks["architecture"] != true {
		t.Fatalf("expected architecture token")
	}
	if toks["readme"] != true {
		t.Fatalf("expected readme token")
	}
}

func TestRouteSectionsHonorsCaps(t *testing.T) {
	sections := []SectionGroup{
		{
			SourcePath:   "docs/a.md",
			SectionID:    "a-1",
			SectionTitle: "Intro",
			Segments: []memory.KnowledgeSegment{
				{Content: "architecture overview", Similarity: 0.9, Metadata: map[string]string{"chunk_title": "architecture"}},
			},
			Inferred: true,
		},
		{
			SourcePath:   "docs/a.md",
			SectionID:    "a-2",
			SectionTitle: "API",
			Segments: []memory.KnowledgeSegment{
				{Content: "api endpoints", Similarity: 0.8, Metadata: map[string]string{"chunk_title": "api"}},
			},
			Inferred: true,
		},
		{
			SourcePath:   "docs/b.md",
			SectionID:    "b-1",
			SectionTitle: "Storage",
			Segments: []memory.KnowledgeSegment{
				{Content: "storage model", Similarity: 0.7, Metadata: map[string]string{"chunk_title": "storage"}},
			},
			Inferred: false,
		},
	}
	selected, report := RouteSections("architecture api storage", sections, HierarchyConfig{
		MaxSectionsPerDoc: 1,
		MaxTotalSections:  2,
		MinSectionScore:   0.0,
	}, HierarchyReport{})
	if len(selected) != 2 {
		t.Fatalf("expected 2 selected sections, got %d", len(selected))
	}
	if report.SelectedSections != 2 {
		t.Fatalf("expected report selected=2, got %d", report.SelectedSections)
	}
	if selected[0].SourcePath == selected[1].SourcePath {
		t.Fatalf("expected per-doc cap to avoid duplicate source selections")
	}
}
