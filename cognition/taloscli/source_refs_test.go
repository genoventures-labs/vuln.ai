package taloscli

import (
	"testing"

	"github.com/Thynaptic/P-LMv1/pkg/orchestration"
)

func TestExtractSourceRefsCapturesURLHFAndLocalPaths(t *testing.T) {
	in := `
Sources used:
- https://example.com/spec
- hf:org/security-dataset
- ./docs/guide.md
- /var/log/auth.log
- pkg/taloscli/research.go
- README.md
`
	refs := extractSourceRefs(in)
	want := []string{
		"https://example.com/spec",
		"hf:org/security-dataset",
		"./docs/guide.md",
		"/var/log/auth.log",
		"pkg/taloscli/research.go",
		"README.md",
	}
	for _, w := range want {
		found := false
		for _, got := range refs {
			if got == w {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("expected source ref %q in %v", w, refs)
		}
	}
}

func TestMapClaimsToSourceRefsFiltersAndFormats(t *testing.T) {
	claims := []orchestration.SectionClaim{
		{Claim: "a", SourcePath: "docs/a.md", SectionID: "docs/a.md::section-1"},
		{Claim: "b", SourcePath: "docs/b.md", SectionID: ""},
		{Claim: "c", SourcePath: "docs/c.md", SectionID: "docs/c.md::section-3"},
	}
	refs := mapClaimsToSourceRefs(claims, []string{"docs/a.md", "docs/b.md"})
	want := []string{"docs/a.md::section-1", "docs/b.md"}
	if len(refs) != len(want) {
		t.Fatalf("expected %d refs, got %d (%v)", len(want), len(refs), refs)
	}
	for _, w := range want {
		found := false
		for _, got := range refs {
			if got == w {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("expected ref %q in %v", w, refs)
		}
	}
}
