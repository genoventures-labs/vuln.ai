package cognition

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Thynaptic/P-LMv1/pkg/memory"
)

const defaultBeliefShiftLogPath = ".memory/belief_shifts.json"

type EpistemicConflict struct {
	NewSegment     memory.KnowledgeSegment `json:"new_segment"`
	OldSegment     memory.KnowledgeSegment `json:"old_segment"`
	Contradiction  float64                 `json:"contradiction"`
	ConflictReason string                  `json:"conflict_reason"`
}

type DebateOutcome struct {
	Conflict          EpistemicConflict `json:"conflict"`
	WinnerID          string            `json:"winner_id"`
	LoserID           string            `json:"loser_id"`
	WinnerDescription string            `json:"winner_description"`
	Rationale         string            `json:"rationale"`
	VerificationNote  string            `json:"verification_note,omitempty"`
}

type beliefShiftLogEntry struct {
	Timestamp         time.Time `json:"timestamp"`
	Query             string    `json:"query"`
	WinnerID          string    `json:"winner_id"`
	LoserID           string    `json:"loser_id"`
	WinnerDescription string    `json:"winner_description"`
	Rationale         string    `json:"rationale"`
	VerificationNote  string    `json:"verification_note,omitempty"`
}

// RunEpistemicSelfAlignment detects conflicts, debates truth updates, adjusts memory weights, and logs belief shifts.
func RunEpistemicSelfAlignment(query, research string, segs []memory.KnowledgeSegment, mm *memory.MemoryManager) ([]DebateOutcome, error) {
	conflicts := detectEpistemicConflicts(segs)
	if len(conflicts) == 0 {
		return nil, nil
	}

	outcomes := make([]DebateOutcome, 0, len(conflicts))
	for _, c := range conflicts {
		outcome := debateConflict(c, research)
		outcomes = append(outcomes, outcome)
		if mm != nil && outcome.WinnerID != "" && outcome.LoserID != "" {
			_ = mm.ApplyBeliefShift(outcome.LoserID, outcome.WinnerID, outcome.Rationale)
		}
		_ = appendBeliefShiftLog(defaultBeliefShiftLogPath, query, outcome)
	}
	return outcomes, nil
}

// TriggerEpistemicAudit is an explicit arbiter entrypoint for nodes (Fusion/Deliberator).
func TriggerEpistemicAudit(query, unifiedTheory string, segs []memory.KnowledgeSegment, mm *memory.MemoryManager) ([]DebateOutcome, error) {
	return RunEpistemicSelfAlignment(query, unifiedTheory, segs, mm)
}

func detectEpistemicConflicts(segs []memory.KnowledgeSegment) []EpistemicConflict {
	if len(segs) < 2 {
		return nil
	}
	var recent []memory.KnowledgeSegment
	var strongOld []memory.KnowledgeSegment
	now := time.Now().UTC()

	for _, s := range segs {
		ts := parseTS(s.Metadata["timestamp"])
		ageH := 99999.0
		if !ts.IsZero() {
			ageH = now.Sub(ts).Hours()
		}
		imp := parseFloatSafe(s.Metadata["base_importance"], 0.5)
		source := strings.ToLower(s.Metadata["source_path"] + " " + s.Metadata["source_type"])
		isRuntime := containsAny(source, "log", "runtime", "journal", "vps", "health", "metrics", "status")
		if isRuntime || ageH <= 72 {
			recent = append(recent, s)
		}
		if imp >= 0.72 && ageH > 24*7 {
			strongOld = append(strongOld, s)
		}
	}
	if len(recent) == 0 || len(strongOld) == 0 {
		return nil
	}

	var out []EpistemicConflict
	for _, n := range recent {
		for _, o := range strongOld {
			if n.ID == o.ID {
				continue
			}
			if lexicalOverlapEpistemic(n.Content, o.Content) < 0.22 {
				continue
			}
			ctr := DetectContradiction(n.Content, o.Content)
			if ctr < 0.62 {
				continue
			}
			out = append(out, EpistemicConflict{
				NewSegment:     n,
				OldSegment:     o,
				Contradiction:  ctr,
				ConflictReason: "Fresh evidence contradicts strongly-held older memory.",
			})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Contradiction > out[j].Contradiction })
	if len(out) > 4 {
		out = out[:4]
	}
	return out
}

func debateConflict(c EpistemicConflict, research string) DebateOutcome {
	newScore := 0.0
	oldScore := 0.0

	newFresh := freshnessWeight(parseTS(c.NewSegment.Metadata["timestamp"]))
	oldFresh := freshnessWeight(parseTS(c.OldSegment.Metadata["timestamp"]))
	newScore += newFresh * 0.45
	oldScore += oldFresh * 0.45

	newAuth := sourceAuthority(c.NewSegment.Metadata)
	oldAuth := sourceAuthority(c.OldSegment.Metadata)
	newScore += newAuth * 0.35
	oldScore += oldAuth * 0.35

	unified := extractUnifiedTheory(research)
	if strings.TrimSpace(unified) != "" {
		newCons := unifiedTheoryConsistency(c.NewSegment.Content, unified)
		oldCons := unifiedTheoryConsistency(c.OldSegment.Content, unified)
		newScore += newCons * 0.20
		oldScore += oldCons * 0.20
	}

	verification := ""
	if note, boostNew := runVerificationPrimitive(c, research); note != "" {
		verification = note
		newScore += boostNew
	}

	// Contradiction severity increases decision confidence, favoring fresher+authoritative side.
	confBoost := clamp01Epistemic(c.Contradiction) * 0.20
	if newFresh+newAuth >= oldFresh+oldAuth {
		newScore += confBoost
	} else {
		oldScore += confBoost
	}

	winnerID := c.NewSegment.ID
	loserID := c.OldSegment.ID
	winnerDesc := "new evidence"
	rationale := fmt.Sprintf("Freshness %.2f>%.2f, authority %.2f>%.2f, contradiction %.2f",
		newFresh, oldFresh, newAuth, oldAuth, c.Contradiction)
	if oldScore > newScore {
		winnerID = c.OldSegment.ID
		loserID = c.NewSegment.ID
		winnerDesc = "existing memory"
		rationale = fmt.Sprintf("Existing memory retained: freshness %.2f>=%.2f or authority %.2f>=%.2f with contradiction %.2f",
			oldFresh, newFresh, oldAuth, newAuth, c.Contradiction)
	}

	return DebateOutcome{
		Conflict:          c,
		WinnerID:          winnerID,
		LoserID:           loserID,
		WinnerDescription: winnerDesc,
		Rationale:         rationale,
		VerificationNote:  verification,
	}
}

func runVerificationPrimitive(c EpistemicConflict, research string) (string, float64) {
	text := strings.ToLower(c.NewSegment.Content + " " + c.OldSegment.Content + " " + research)
	if !containsAny(text, "parser", "binary", "protobuf", "yaml", "json schema", "regex") {
		return "", 0
	}
	sc := NewSkillCompiler()
	gap := CapabilityGap{
		Detected:    true,
		Description: "verify conflicting dependency claim",
	}
	skill, err := sc.CompileSkillPrimitive(gap, research)
	if err != nil {
		return "verification primitive compile failed", 0
	}
	out, runErr := sc.DispatchSandbox(skill, "--self-test")
	if runErr == nil && strings.Contains(strings.ToLower(out), "skill-ok") {
		return "verification primitive self-test passed", 0.15
	}
	return "verification primitive inconclusive", 0.03
}

func appendBeliefShiftLog(path string, query string, outcome DebateOutcome) error {
	if strings.TrimSpace(path) == "" {
		path = defaultBeliefShiftLogPath
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	var logs []beliefShiftLogEntry
	if b, err := os.ReadFile(path); err == nil && len(b) > 0 {
		_ = json.Unmarshal(b, &logs)
	}
	logs = append(logs, beliefShiftLogEntry{
		Timestamp:         time.Now().UTC(),
		Query:             strings.TrimSpace(query),
		WinnerID:          outcome.WinnerID,
		LoserID:           outcome.LoserID,
		WinnerDescription: outcome.WinnerDescription,
		Rationale:         outcome.Rationale,
		VerificationNote:  outcome.VerificationNote,
	})
	if len(logs) > 2000 {
		logs = logs[len(logs)-2000:]
	}
	data, err := json.MarshalIndent(logs, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

func sourceAuthority(meta map[string]string) float64 {
	src := strings.ToLower(strings.TrimSpace(meta["source_path"] + " " + meta["source_type"]))
	switch {
	case containsAny(src, "live shell", "shell", "terminal", "stdout", "stderr", "cli"):
		return 1.0
	case containsAny(src, "log", "runtime", "journal", "health", "metrics", "status"):
		return 0.92
	case containsAny(src, "config", "yaml", "json", "toml"):
		return 0.72
	case containsAny(src, "readme", "docs", "guide", "md"):
		return 0.45
	default:
		return 0.55
	}
}

func extractUnifiedTheory(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	l := strings.ToLower(s)
	if idx := strings.Index(l, "unified theory"); idx >= 0 {
		return strings.TrimSpace(s[idx:])
	}
	return s
}

func unifiedTheoryConsistency(claim, unified string) float64 {
	claim = strings.TrimSpace(claim)
	unified = strings.TrimSpace(unified)
	if claim == "" || unified == "" {
		return 0.5
	}
	overlap := lexicalOverlapEpistemic(claim, unified)
	contra := DetectContradiction(claim, unified)
	score := (overlap * 0.7) + ((1.0 - contra) * 0.3)
	return clamp01Epistemic(score)
}

func freshnessWeight(ts time.Time) float64 {
	if ts.IsZero() {
		return 0.4
	}
	ageH := time.Since(ts).Hours()
	if ageH <= 0 {
		return 1.0
	}
	return clamp01Epistemic(math.Pow(2, -ageH/(24*3))) // 3-day half-life
}

func lexicalOverlapEpistemic(a, b string) float64 {
	ta := tokensEpistemic(a)
	tb := tokensEpistemic(b)
	if len(ta) == 0 || len(tb) == 0 {
		return 0
	}
	setA := map[string]bool{}
	for _, t := range ta {
		setA[t] = true
	}
	shared := 0
	for _, t := range tb {
		if setA[t] {
			shared++
		}
	}
	den := len(ta)
	if len(tb) > den {
		den = len(tb)
	}
	return float64(shared) / float64(den)
}

func tokensEpistemic(s string) []string {
	s = strings.ToLower(strings.TrimSpace(s))
	rep := strings.NewReplacer(",", " ", ".", " ", ";", " ", ":", " ", "(", " ", ")", " ", "[", " ", "]", " ", "{", " ", "}", " ", "\"", " ")
	s = rep.Replace(s)
	var out []string
	for _, t := range strings.Fields(s) {
		if len(t) < 3 {
			continue
		}
		out = append(out, t)
	}
	return out
}

func parseTS(v string) time.Time {
	v = strings.TrimSpace(v)
	if v == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339, v)
	if err != nil {
		return time.Time{}
	}
	return t
}

func parseFloatSafe(v string, fallback float64) float64 {
	v = strings.TrimSpace(v)
	if v == "" {
		return fallback
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil {
		return fallback
	}
	return clamp01Epistemic(f)
}

func containsAny(s string, markers ...string) bool {
	for _, m := range markers {
		if strings.Contains(s, m) {
			return true
		}
	}
	return false
}

func clamp01Epistemic(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}
