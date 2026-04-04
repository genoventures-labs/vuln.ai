package taloscli

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Thynaptic/P-LMv1/pkg/toolflow"
)

const defaultReflectionAuditPath = ".memory/reflection_audit.jsonl"

type artifactBundle struct {
	Summary         string
	Findings        []string
	EvidenceNotes   []string
	Sources         []string
	ReflectionNotes []string
}

func applyArtifactDrivenInputs(inv []toolflow.Invocation) ([]toolflow.Invocation, []string, []string) {
	out := make([]toolflow.Invocation, 0, len(inv))
	notes := make([]string, 0, len(inv))
	warnings := make([]string, 0, len(inv))
	for _, in := range inv {
		args := cloneArgs(in.Args)
		switch in.Tool {
		case "web_search", "fetch_url", "http_request", "vector_retrieve":
			bundle, n, warn := resolveArtifactBundle(args)
			if len(n) > 0 {
				notes = append(notes, prefixNotes(in.Tool, n)...)
			}
			if len(warn) > 0 {
				warnings = append(warnings, prefixNotes(in.Tool, warn)...)
			}
			artifactQuery := buildArtifactQuery(bundle)
			if in.Tool == "web_search" || in.Tool == "vector_retrieve" {
				q := strings.TrimSpace(getArgString(args, "query", ""))
				if q == "" && artifactQuery != "" {
					args["query"] = artifactQuery
					notes = append(notes, fmt.Sprintf("%s.query=artifact", in.Tool))
				} else if q != "" && artifactQuery != "" {
					merged := mergeArtifactContext(q, artifactQuery, 420)
					if merged != q {
						args["query"] = merged
						notes = append(notes, fmt.Sprintf("%s.query+=artifact", in.Tool))
					}
				}
			}
			if in.Tool == "fetch_url" || in.Tool == "http_request" {
				url := strings.TrimSpace(getArgString(args, "url", ""))
				if url == "" && len(bundle.Sources) > 0 {
					args["url"] = strings.TrimSpace(bundle.Sources[0])
					notes = append(notes, fmt.Sprintf("%s.url=artifact_source", in.Tool))
				}
			}
		}
		out = append(out, toolflow.Invocation{
			Tool:   in.Tool,
			Args:   args,
			Source: in.Source,
			Raw:    in.Raw,
		})
	}
	return out, trimNotes(notes, 12), trimNotes(warnings, 8)
}

func resolveArtifactBundle(args map[string]interface{}) (artifactBundle, []string, []string) {
	var out artifactBundle
	notes := []string{}
	warnings := []string{}
	artifactID := strings.TrimSpace(getArgString(args, "artifact_id", ""))
	artifactPath := strings.TrimSpace(getArgString(args, "artifact_path", ""))
	includeEvidence := getArgBool(args, "include_evidence_notes", true)
	includeReflection := getArgBool(args, "include_reflection_notes", true)

	if artifactID != "" {
		id := artifactID
		if strings.EqualFold(id, "latest") {
			resolved, err := resolveResearchArtifactID("latest")
			if err != nil {
				warnings = append(warnings, "artifact_id latest unresolved")
			} else {
				id = resolved
			}
		}
		if strings.TrimSpace(id) != "" {
			if art, err := loadResearchArtifactByID(id); err != nil {
				warnings = append(warnings, "artifact_id load failed")
			} else {
				out = mergeArtifactBundle(out, bundleFromResearchArtifact(art, includeEvidence))
				notes = append(notes, "artifact_id="+id)
			}
		}
	}

	if artifactPath != "" {
		b, err := os.ReadFile(artifactPath)
		if err != nil {
			warnings = append(warnings, "artifact_path read failed")
		} else {
			if art, ok := parseResearchArtifactBytes(b); ok {
				out = mergeArtifactBundle(out, bundleFromResearchArtifact(art, includeEvidence))
				notes = append(notes, "artifact_path=research")
			} else {
				refl, err := loadReflectionEvidenceNotesFromPath(artifactPath, 8)
				if err != nil {
					warnings = append(warnings, "artifact_path unsupported")
				} else if len(refl) > 0 {
					out.ReflectionNotes = append(out.ReflectionNotes, refl...)
					notes = append(notes, "artifact_path=reflection")
				}
			}
		}
	}

	if includeReflection {
		refPath := strings.TrimSpace(getArgString(args, "reflection_audit_path", ""))
		if refPath == "" {
			refPath = defaultReflectionAuditPath
		}
		refl, err := loadReflectionEvidenceNotesFromPath(refPath, 8)
		if err != nil {
			if strings.TrimSpace(getArgString(args, "reflection_audit_path", "")) != "" {
				warnings = append(warnings, "reflection_audit_path read failed")
			}
		} else if len(refl) > 0 {
			out.ReflectionNotes = append(out.ReflectionNotes, refl...)
			notes = append(notes, "reflection_notes="+fmt.Sprintf("%d", len(refl)))
		}
	}

	out = normalizeArtifactBundle(out)
	return out, notes, warnings
}

func parseResearchArtifactBytes(b []byte) (ResearchArtifact, bool) {
	var art ResearchArtifact
	if err := json.Unmarshal(b, &art); err != nil {
		return ResearchArtifact{}, false
	}
	if strings.TrimSpace(art.SessionID) == "" && strings.TrimSpace(art.Query) == "" && len(art.Findings) == 0 {
		return ResearchArtifact{}, false
	}
	return art, true
}

func bundleFromResearchArtifact(art ResearchArtifact, includeEvidence bool) artifactBundle {
	out := artifactBundle{
		Summary: strings.TrimSpace(art.ExecutiveSummary),
		Sources: append([]string(nil), art.Sources...),
	}
	for _, f := range art.Findings {
		text := strings.TrimSpace(f.Text)
		if text != "" {
			out.Findings = append(out.Findings, text)
		}
	}
	if includeEvidence {
		for _, n := range art.EvidenceNotes {
			n = strings.TrimSpace(n)
			if n != "" {
				out.EvidenceNotes = append(out.EvidenceNotes, n)
			}
		}
	}
	return out
}

func loadReflectionEvidenceNotesFromPath(path string, maxNotes int) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	buf := make([]byte, 0, 64*1024)
	sc.Buffer(buf, 1024*1024)
	notes := make([]string, 0, maxNotes)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var m map[string]interface{}
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			// Non-jsonl artifact path.
			return nil, fmt.Errorf("invalid reflection audit jsonl")
		}
		outcome := strings.TrimSpace(strings.ToLower(anyString(m["outcome"])))
		risk := anyFloat(m["risk_score"])
		stage := strings.TrimSpace(anyString(m["stage"]))
		violations := anyStrings(m["violations"])
		if outcome == "" || outcome == "pass" || len(violations) == 0 {
			continue
		}
		note := fmt.Sprintf("reflection stage=%s outcome=%s risk=%.2f: %s", stage, outcome, risk, strings.Join(violations, "; "))
		notes = append(notes, note)
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	if len(notes) > maxNotes {
		notes = notes[len(notes)-maxNotes:]
	}
	return notes, nil
}

func buildArtifactQuery(b artifactBundle) string {
	parts := make([]string, 0, 4)
	if strings.TrimSpace(b.Summary) != "" {
		parts = append(parts, b.Summary)
	}
	if len(b.Findings) > 0 {
		parts = append(parts, b.Findings[0])
	}
	if len(b.EvidenceNotes) > 0 {
		parts = append(parts, b.EvidenceNotes[0])
	}
	if len(b.ReflectionNotes) > 0 {
		parts = append(parts, b.ReflectionNotes[0])
	}
	return truncateText(strings.Join(parts, " | "), 320)
}

func mergeArtifactContext(query, ctx string, maxLen int) string {
	query = strings.TrimSpace(query)
	ctx = strings.TrimSpace(ctx)
	if query == "" {
		return truncateText(ctx, maxLen)
	}
	if ctx == "" {
		return truncateText(query, maxLen)
	}
	out := query + " | context: " + ctx
	return truncateText(out, maxLen)
}

func mergeArtifactBundle(a, b artifactBundle) artifactBundle {
	if strings.TrimSpace(a.Summary) == "" {
		a.Summary = strings.TrimSpace(b.Summary)
	}
	a.Findings = append(a.Findings, b.Findings...)
	a.EvidenceNotes = append(a.EvidenceNotes, b.EvidenceNotes...)
	a.ReflectionNotes = append(a.ReflectionNotes, b.ReflectionNotes...)
	a.Sources = append(a.Sources, b.Sources...)
	return a
}

func normalizeArtifactBundle(in artifactBundle) artifactBundle {
	in.Summary = strings.TrimSpace(in.Summary)
	in.Findings = uniqueTrimmed(in.Findings, 8)
	in.EvidenceNotes = uniqueTrimmed(in.EvidenceNotes, 8)
	in.ReflectionNotes = uniqueTrimmed(in.ReflectionNotes, 8)
	in.Sources = uniqueURLs(in.Sources, 12)
	return in
}

func uniqueTrimmed(in []string, maxN int) []string {
	seen := map[string]bool{}
	out := make([]string, 0, minIntAuto(len(in), maxN))
	for _, v := range in {
		v = strings.TrimSpace(v)
		if v == "" || seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
		if len(out) >= maxN {
			break
		}
	}
	return out
}

func uniqueURLs(in []string, maxN int) []string {
	cleaned := make([]string, 0, len(in))
	for _, v := range in {
		v = strings.TrimSpace(v)
		if v == "" {
			continue
		}
		if strings.HasPrefix(strings.ToLower(v), "http://") || strings.HasPrefix(strings.ToLower(v), "https://") {
			cleaned = append(cleaned, v)
		}
	}
	cleaned = uniqueTrimmed(cleaned, maxN)
	sort.Strings(cleaned)
	return cleaned
}

func anyString(v interface{}) string {
	switch x := v.(type) {
	case string:
		return x
	default:
		return fmt.Sprintf("%v", v)
	}
}

func anyFloat(v interface{}) float64 {
	switch x := v.(type) {
	case float64:
		return x
	case float32:
		return float64(x)
	case int:
		return float64(x)
	case int64:
		return float64(x)
	default:
		return 0
	}
}

func anyStrings(v interface{}) []string {
	arr, ok := v.([]interface{})
	if !ok {
		return nil
	}
	out := make([]string, 0, len(arr))
	for _, e := range arr {
		s := strings.TrimSpace(anyString(e))
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}

func truncateText(s string, n int) string {
	s = strings.TrimSpace(strings.ReplaceAll(s, "\n", " "))
	if n <= 0 || len(s) <= n {
		return s
	}
	if n <= 3 {
		return s[:n]
	}
	return s[:n-3] + "..."
}

func defaultResearchArtifactPath(id string) string {
	return filepath.Join(researchArtifactsDir, strings.TrimSpace(id)+".json")
}
