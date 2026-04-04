package taloscli

import (
	"regexp"
	"strings"

	"github.com/Thynaptic/P-LMv1/pkg/orchestration"
)

var (
	sourceURLRegex = regexp.MustCompile(`https?://[^\s"\\]+`)
	sourceHFRegex  = regexp.MustCompile(`\bhf:[A-Za-z0-9._/-]+\b`)
)

func extractSourceRefs(s string) []string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	refs := make([]string, 0, 8)

	for _, u := range sourceURLRegex.FindAllString(s, -1) {
		refs = append(refs, sanitizeSourceToken(u))
	}
	for _, h := range sourceHFRegex.FindAllString(s, -1) {
		refs = append(refs, sanitizeSourceToken(h))
	}

	for _, tok := range strings.Fields(s) {
		candidate := sanitizeSourceToken(tok)
		if candidate == "" {
			continue
		}
		if isLikelyLocalSourceRef(candidate) {
			refs = append(refs, candidate)
		}
	}

	return uniqueStrings(refs)
}

func sanitizeSourceToken(tok string) string {
	t := strings.TrimSpace(tok)
	t = strings.Trim(t, "`\"'()[]{}<>,;!")
	return strings.TrimSpace(t)
}

func isLikelyLocalSourceRef(tok string) bool {
	if tok == "" {
		return false
	}
	if strings.Contains(tok, "://") {
		return false
	}
	if strings.HasPrefix(tok, "hf:") {
		return true
	}
	if strings.HasPrefix(tok, "/") || strings.HasPrefix(tok, "./") || strings.HasPrefix(tok, "../") || strings.HasPrefix(tok, "~") {
		return true
	}
	if strings.Contains(tok, "/") {
		return true
	}
	if strings.Contains(tok, "\\") {
		return true
	}

	ext := strings.ToLower(tok)
	for _, suffix := range []string{
		".md", ".txt", ".pdf", ".json", ".yaml", ".yml", ".toml", ".csv", ".tsv", ".xml", ".html", ".htm", ".rst", ".adoc", ".log", ".go", ".py", ".js", ".ts", ".java", ".c", ".cc", ".cpp", ".h", ".hpp", ".rs", ".sh", ".sql", ".ipynb",
	} {
		if strings.HasSuffix(ext, suffix) {
			return true
		}
	}
	return false
}

func mapClaimsToSourceRefs(claims []orchestration.SectionClaim, knownSources []string) []string {
	if len(claims) == 0 {
		return nil
	}
	allow := make(map[string]bool, len(knownSources))
	for _, s := range knownSources {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		allow[s] = true
	}
	refs := make([]string, 0, len(claims))
	for _, c := range claims {
		src := strings.TrimSpace(c.SourcePath)
		if src == "" {
			continue
		}
		if len(allow) > 0 && !allow[src] {
			continue
		}
		if sec := strings.TrimSpace(c.SectionID); sec != "" {
			if strings.Contains(sec, "::") {
				refs = append(refs, sec)
			} else {
				refs = append(refs, src+"::"+sec)
			}
		} else {
			refs = append(refs, src)
		}
	}
	return uniqueStrings(refs)
}
