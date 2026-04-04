package cognition

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	defaultSelfAlignmentLogPath     = ".memory/self_alignment_audit.jsonl"
	defaultWorldviewHistoryAuditLog = ".memory/worldview_history.jsonl"
)

type SelfAlignmentPolicy struct {
	Enabled       bool
	MaxDepth      int
	WarnAt        float64
	VetoAt        float64
	MaxSignals    int
	HistoryWindow int
	LogPath       string
}

type SelfAlignmentSignal struct {
	Score          float64
	RecursiveDepth int
	Violations     []string
	PhilosophyRefs []string
	Rationale      string
}

type worldviewHistoryEntry struct {
	UpdatedAt      time.Time `json:"updated_at"`
	Query          string    `json:"query"`
	FusedWorldview string    `json:"fused_worldview"`
	TruthHash      string    `json:"truth_hash"`
}

func DefaultSelfAlignmentPolicy(mode string) SelfAlignmentPolicy {
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode == "" {
		mode = "tiered"
	}
	warn := 0.54
	veto := 0.78
	if mode == "deep" || mode == "hard" {
		warn = 0.48
		veto = 0.72
	}
	return SelfAlignmentPolicy{
		Enabled:       envBoolSup("TALOS_SELF_ALIGNMENT_ENABLED", true),
		MaxDepth:      clampIntSup(envIntSup("TALOS_SELF_ALIGNMENT_MAX_DEPTH", 2), 1, 4),
		WarnAt:        warn,
		VetoAt:        veto,
		MaxSignals:    clampIntSup(envIntSup("TALOS_SELF_ALIGNMENT_MAX_SIGNALS", 8), 2, 20),
		HistoryWindow: clampIntSup(envIntSup("TALOS_SELF_ALIGNMENT_HISTORY_WINDOW", 10), 2, 40),
		LogPath:       firstNonEmptySelfAlign(strings.TrimSpace(os.Getenv("TALOS_SELF_ALIGNMENT_LOG_PATH")), defaultSelfAlignmentLogPath),
	}
}

func RunRecursiveSelfAlignment(in SupervisionInput, policy SelfAlignmentPolicy) (SelfAlignmentSignal, error) {
	out := SelfAlignmentSignal{}
	if !policy.Enabled {
		return out, nil
	}
	candidate := strings.TrimSpace(in.Candidate)
	if candidate == "" {
		return out, nil
	}
	refs := collectProjectPhilosophyRefs(in, policy)
	if len(refs) == 0 {
		return out, nil
	}
	score, violations, depth := recursiveSelfAlignScore(candidate, refs, policy.MaxDepth, 1)
	out.Score = score
	out.RecursiveDepth = depth
	out.Violations = trimUniqueSelfAlign(violations, policy.MaxSignals)
	out.PhilosophyRefs = trimUniqueSelfAlign(refs, policy.MaxSignals)
	out.Rationale = fmt.Sprintf("recursive self-alignment score %.2f at depth %d", out.Score, out.RecursiveDepth)
	appendSelfAlignmentAudit(policy.LogPath, in, out)
	return out, nil
}

func recursiveSelfAlignScore(candidate string, refs []string, maxDepth int, depth int) (float64, []string, int) {
	base := clampScore(EvaluateLogic(candidate, refs))
	heuristic, issues := philosophyDriftHeuristic(candidate, refs)
	score := clampScore(maxFloatSup(base, heuristic))
	if depth >= maxDepth || score < 0.35 {
		return score, issues, depth
	}
	nextRefs := append([]string{}, refs...)
	for _, issue := range issues {
		nextRefs = append(nextRefs, "Project philosophy guardrail: "+issue)
	}
	rScore, rIssues, rDepth := recursiveSelfAlignScore(candidate, nextRefs, maxDepth, depth+1)
	merged := append(issues, rIssues...)
	return clampScore(maxFloatSup(score, rScore)), merged, rDepth
}

func collectProjectPhilosophyRefs(in SupervisionInput, policy SelfAlignmentPolicy) []string {
	refs := make([]string, 0, 16)
	if v := strings.TrimSpace(in.Metadata["project_philosophy"]); v != "" {
		for _, part := range splitPhilosophyLines(v) {
			refs = append(refs, part)
		}
	}
	if v := strings.TrimSpace(in.Session.PrimaryGoal); v != "" {
		refs = append(refs, "Primary goal: "+v)
	}
	if model, err := MinePreferenceModel(defaultAlignmentAuditPath, defaultSessionStatePath); err == nil && model != nil {
		for _, p := range model.Patterns {
			if p.Weight < 0.20 {
				continue
			}
			refs = append(refs, p.Description)
		}
	}
	if hist, err := loadWorldviewHistoryForAlignment(policy.HistoryWindow); err == nil && len(hist) > 0 {
		for _, h := range hist {
			if strings.TrimSpace(h.Query) != "" {
				refs = append(refs, "Worldview query: "+h.Query)
			}
			if strings.TrimSpace(h.FusedWorldview) != "" {
				refs = append(refs, summarizeSelfAlign(h.FusedWorldview, 180))
			}
		}
	}
	return trimUniqueSelfAlign(refs, 18)
}

func loadWorldviewHistoryForAlignment(last int) ([]worldviewHistoryEntry, error) {
	f, err := os.Open(defaultWorldviewHistoryAuditLog)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	buf := make([]byte, 0, 64*1024)
	sc.Buffer(buf, 1024*1024)
	out := make([]worldviewHistoryEntry, 0, last)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var item worldviewHistoryEntry
		if err := json.Unmarshal([]byte(line), &item); err != nil {
			continue
		}
		out = append(out, item)
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	if last > 0 && len(out) > last {
		out = out[len(out)-last:]
	}
	return out, nil
}

func philosophyDriftHeuristic(candidate string, refs []string) (float64, []string) {
	c := strings.ToLower(strings.TrimSpace(candidate))
	if c == "" {
		return 0, nil
	}
	score := 0.0
	issues := make([]string, 0, 4)
	for _, ref := range refs {
		r := strings.ToLower(strings.TrimSpace(ref))
		if r == "" {
			continue
		}
		if strings.Contains(r, "local-first") && containsAnySelfAlign(c, "use external api", "send all data to cloud", "remote-only") {
			score = maxFloatSup(score, 0.82)
			issues = append(issues, "candidate diverges from local-first philosophy")
		}
		if strings.Contains(r, "safety") && containsAnySelfAlign(c, "disable security", "skip validation", "bypass checks") {
			score = maxFloatSup(score, 0.92)
			issues = append(issues, "candidate bypasses safety-oriented project philosophy")
		}
		if strings.Contains(r, "citation") && containsAnySelfAlign(c, "no sources needed", "without citations") {
			score = maxFloatSup(score, 0.74)
			issues = append(issues, "candidate rejects citation discipline")
		}
		if strings.Contains(r, "deterministic") && containsAnySelfAlign(c, "randomly", "best effort only") {
			score = maxFloatSup(score, 0.62)
			issues = append(issues, "candidate conflicts with deterministic execution philosophy")
		}
	}
	return clampScore(score), trimUniqueSelfAlign(issues, 6)
}

func appendSelfAlignmentAudit(path string, in SupervisionInput, sig SelfAlignmentSignal) {
	path = strings.TrimSpace(path)
	if path == "" {
		path = defaultSelfAlignmentLogPath
	}
	entry := map[string]interface{}{
		"timestamp":       time.Now().UTC(),
		"stage":           in.Stage,
		"query":           summarizeSelfAlign(in.Query, 220),
		"score":           sig.Score,
		"recursive_depth": sig.RecursiveDepth,
		"violations":      sig.Violations,
		"rationale":       sig.Rationale,
	}
	payload, err := json.Marshal(entry)
	if err != nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = f.Write(append(payload, '\n'))
}

func splitPhilosophyLines(s string) []string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	raw := strings.Split(s, "\n")
	out := make([]string, 0, len(raw))
	for _, line := range raw {
		line = strings.TrimSpace(strings.TrimLeft(line, "-*0123456789. "))
		if line != "" {
			out = append(out, line)
		}
	}
	return out
}

func trimUniqueSelfAlign(in []string, maxN int) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(in))
	for _, v := range in {
		v = strings.TrimSpace(v)
		if v == "" || seen[strings.ToLower(v)] {
			continue
		}
		seen[strings.ToLower(v)] = true
		out = append(out, v)
		if maxN > 0 && len(out) >= maxN {
			break
		}
	}
	sort.Strings(out)
	return out
}

func summarizeSelfAlign(s string, n int) string {
	s = strings.Join(strings.Fields(strings.TrimSpace(s)), " ")
	if s == "" || n <= 0 || len(s) <= n {
		return s
	}
	if n <= 3 {
		return s[:n]
	}
	return s[:n-3] + "..."
}

func firstNonEmptySelfAlign(vals ...string) string {
	for _, v := range vals {
		v = strings.TrimSpace(v)
		if v != "" {
			return v
		}
	}
	return ""
}

func containsAnySelfAlign(s string, tokens ...string) bool {
	for _, t := range tokens {
		if strings.Contains(s, strings.ToLower(strings.TrimSpace(t))) {
			return true
		}
	}
	return false
}
