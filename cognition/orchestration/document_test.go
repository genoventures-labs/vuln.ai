package orchestration

import (
	"strings"
	"testing"

	"github.com/Thynaptic/P-LMv1/pkg/memory"
)

func TestBuildSectionGroupsUsesMetadata(t *testing.T) {
	groups := []DocGroup{
		{
			SourcePath: "docs/guide.md",
			Segments: []memory.KnowledgeSegment{
				{
					Content:    "content a",
					Similarity: 0.8,
					Metadata: map[string]string{
						"chunk_index":   "1",
						"section_id":    "docs/guide.md::section-1",
						"section_title": "Overview",
					},
				},
				{
					Content:    "content b",
					Similarity: 0.7,
					Metadata: map[string]string{
						"chunk_index":   "2",
						"section_id":    "docs/guide.md::section-1",
						"section_title": "Overview",
					},
				},
			},
		},
	}
	sections, report := buildSectionGroups(groups)
	if len(sections) != 1 {
		t.Fatalf("expected one section group, got %d", len(sections))
	}
	if sections[0].SectionTitle != "Overview" {
		t.Fatalf("expected section title Overview, got %q", sections[0].SectionTitle)
	}
	if report.InferenceUsed {
		t.Fatalf("did not expect inference when metadata is present")
	}
}

func TestBuildSectionGroupsInfersFromHeadings(t *testing.T) {
	groups := []DocGroup{
		{
			SourcePath: "docs/arch.md",
			Segments: []memory.KnowledgeSegment{
				{
					Content:    "## Architecture\nservice layout",
					Similarity: 0.9,
					Metadata: map[string]string{
						"chunk_index": "1",
					},
				},
				{
					Content:    "## API\npublic interfaces",
					Similarity: 0.85,
					Metadata: map[string]string{
						"chunk_index": "2",
					},
				},
			},
		},
	}
	sections, report := buildSectionGroups(groups)
	if len(sections) < 2 {
		t.Fatalf("expected at least 2 inferred sections, got %d", len(sections))
	}
	if !report.InferenceUsed {
		t.Fatalf("expected inference flag true")
	}
	if !strings.Contains(strings.ToLower(sections[0].SectionTitle+sections[1].SectionTitle), "api") {
		t.Fatalf("expected inferred heading-derived section titles, got %+v", sections)
	}
}

func TestCompactHierarchySummaryLines(t *testing.T) {
	lines := CompactHierarchySummaryLines(
		HierarchyReport{CandidateSections: 5, SelectedSections: 3, DroppedSections: 2, InferenceUsed: true},
		[]SectionMapSummary{
			{SourcePath: "docs/a.md", SectionTitle: "Overview", Score: 0.82},
			{SourcePath: "docs/b.md", SectionTitle: "API", Score: 0.75},
		},
		5,
	)
	if len(lines) < 3 {
		t.Fatalf("expected summary + section lines, got %d", len(lines))
	}
	if !strings.Contains(lines[0], "sections=5") {
		t.Fatalf("expected header line with counts, got %q", lines[0])
	}
}

func TestShouldRunLongFormReasoning(t *testing.T) {
	sections := []SectionMapSummary{
		{SourcePath: "docs/a.md", SectionID: "a1", Score: 0.9},
		{SourcePath: "docs/a.md", SectionID: "a2", Score: 0.8},
		{SourcePath: "docs/b.md", SectionID: "b1", Score: 0.7},
		{SourcePath: "docs/c.md", SectionID: "c1", Score: 0.6},
	}
	cfg := LongFormReasoningConfig{
		Enabled:              true,
		TriggerMinComplexity: 4,
		TriggerMinSections:   4,
	}
	if !shouldRunLongFormReasoning("compare architecture tradeoff across sections and evidence", sections, cfg) {
		t.Fatalf("expected long-form reasoning trigger")
	}
	if shouldRunLongFormReasoning("quick status", sections[:2], cfg) {
		t.Fatalf("expected long-form reasoning not to trigger for low complexity/sections")
	}
}

func TestEnforceSectionClaimCitations(t *testing.T) {
	claims := []SectionClaim{
		{Claim: "A", SourcePath: "docs/a.md", SectionID: "docs/a.md::section-1", Confidence: 0.9},
		{Claim: "B", SourcePath: "docs/b.md", SectionID: "", Confidence: 0.7},
		{Claim: "C", SourcePath: "docs/missing.md", SectionID: "x", Confidence: 0.8},
	}
	sections := []SectionMapSummary{
		{SourcePath: "docs/a.md", SectionID: "docs/a.md::section-1"},
		{SourcePath: "docs/b.md", SectionID: "docs/b.md::section-3"},
	}
	out, unsupported, coverage := enforceSectionClaimCitations(claims, sections)
	if len(out) != 2 {
		t.Fatalf("expected 2 supported claims, got %d", len(out))
	}
	if unsupported != 1 {
		t.Fatalf("expected unsupported=1, got %d", unsupported)
	}
	if coverage < 0.66 || coverage > 0.67 {
		t.Fatalf("unexpected coverage: %.4f", coverage)
	}
}

func TestCrossLinkSectionsBuildsSectionLevelLinks(t *testing.T) {
	sections := []SectionMapSummary{
		{
			SourcePath:   "docs/a.md",
			SectionID:    "docs/a.md::section-1",
			SectionTitle: "Auth",
			Summary:      "Token validation and key rotation",
			Entities:     []string{"JWT", "KeyStore"},
		},
		{
			SourcePath:   "docs/b.md",
			SectionID:    "docs/b.md::section-2",
			SectionTitle: "Gateway",
			Summary:      "JWT verification for upstream auth",
			Entities:     []string{"JWT", "Ingress"},
		},
		{
			SourcePath:   "docs/a.md",
			SectionID:    "docs/a.md::section-3",
			SectionTitle: "Policies",
			Summary:      "Retention and audit controls",
			Entities:     []string{"Audit", "Controls"},
		},
	}
	links := crossLinkSections(sections)
	if len(links) == 0 {
		t.Fatalf("expected section links")
	}
	foundInter := false
	for _, l := range links {
		if l.LinkType == "inter_source" && l.Entity == "JWT" {
			foundInter = true
			break
		}
	}
	if !foundInter {
		t.Fatalf("expected inter_source JWT section link, got %+v", links)
	}
	lines := CompactSectionCrossLinkLines(links, 3)
	if len(lines) == 0 || !strings.Contains(lines[0], "strength=") {
		t.Fatalf("expected compact section link lines, got %+v", lines)
	}
}
