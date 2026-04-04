package taloscli

import (
	"strings"
	"testing"
)

func TestRenderDocSearchReport(t *testing.T) {
	out := renderDocSearchReport(DocSearchReport{
		Query:        "Sasswall",
		Root:         ".",
		ScannedFiles: 3,
		MatchedFiles: 1,
		TotalMatches: 2,
		Results: []DocSearchFileResult{
			{
				Path:       "docs/a.md",
				MatchCount: 2,
				Matches: []DocSearchMatch{
					{Line: 3, Column: 7, Snippet: "Sasswall active"},
				},
			},
		},
	})
	required := []string{"TALOS DOC SEARCH", "QUERY", "RESULTS", "docs/a.md", "L3:C7"}
	for _, token := range required {
		if !strings.Contains(out, token) {
			t.Fatalf("expected output to include %q, got:\n%s", token, out)
		}
	}
}

func TestParseDocSearchCSV(t *testing.T) {
	out := parseDocSearchCSV(".md, .txt,,.go")
	if len(out) != 3 {
		t.Fatalf("expected 3 tokens, got %d (%v)", len(out), out)
	}
}
