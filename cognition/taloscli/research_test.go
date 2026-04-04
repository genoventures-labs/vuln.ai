package taloscli

import (
	"strings"
	"testing"
)

func TestRenderResearchReportSections(t *testing.T) {
	report := researchReport{
		Mode:      "run",
		Query:     "test query",
		Executive: "summary",
		Findings: []researchFinding{
			{Text: "Finding one", Refs: []int{1}, Verified: true},
			{Text: "Finding two", Verified: false},
		},
		EvidenceNotes: []string{"tool evidence captured"},
		Risks:         []string{"risk one"},
		NextActions:   []string{"action one"},
		Sources:       []string{"https://example.com"},
	}
	out := renderResearchReport(report)

	required := []string{
		"TALOS RESEARCH REPORT",
		"COMMAND",
		"STATUS",
		"RESULTS",
		"MODE",
		"QUERY",
		"EXECUTIVE SUMMARY",
		"KEY FINDINGS",
		"EVIDENCE AND CITATIONS",
		"RISKS / UNCERTAINTIES",
		"RECOMMENDED NEXT ACTIONS",
		"SOURCES",
		"[1]",
		"[UNVERIFIED]",
	}
	for _, token := range required {
		if !strings.Contains(out, token) {
			t.Fatalf("expected report to contain %q\noutput:\n%s", token, out)
		}
	}
}

func TestBuildFindingsWithSources(t *testing.T) {
	findings := buildFindings("A long finding sentence that should be included.\nAnother long finding sentence that should also be included.", []string{"https://a", "https://b"})
	if len(findings) == 0 {
		t.Fatal("expected findings")
	}
	for _, f := range findings {
		if !f.Verified || len(f.Refs) == 0 {
			t.Fatalf("expected verified finding with refs: %#v", f)
		}
	}
}
