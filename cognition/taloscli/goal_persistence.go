package taloscli

import (
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"

	"github.com/Thynaptic/P-LMv1/pkg/state"
)

const (
	goalPersistenceCoefficientEnv = "TALOS_GOAL_PERSISTENCE_COEFFICIENT"
	goalPersistenceMinWordsEnv    = "TALOS_GOAL_PERSISTENCE_MIN_WORDS"
)

var goalStopwordRE = regexp.MustCompile(`[^a-z0-9\s]+`)

func applyPersistentGoalLock(sm *state.Manager, normalized string, module string) (string, string) {
	normalized = strings.TrimSpace(normalized)
	if sm == nil || normalized == "" {
		return normalized, ""
	}
	if shouldBypassGoalLock(normalized, module) {
		return normalized, "goal lock bypassed for standalone user query"
	}
	snap := sm.GetSnapshot()
	currentGoal := strings.TrimSpace(snap.PrimaryGoal)
	if currentGoal == "" {
		sm.SetPrimaryGoal(normalized)
		sm.UpdateDelta(map[string]float64{"GoalPersistence": 0.06})
		_ = sm.Save()
		return normalized, "goal persistence initialized from first objective"
	}

	coeff := clamp01Budget(floatEnvGoal(goalPersistenceCoefficientEnv, 0.82))
	minWords := int(floatEnvGoal(goalPersistenceMinWordsEnv, 5))
	if minWords < 2 {
		minWords = 2
	}
	candidateWords := wordCountGoal(normalized)
	persistence := snap.GoalPersistence
	shift := goalShiftScore(currentGoal, normalized)
	requiredShift := 0.32 + (persistence * coeff * 0.42)

	if isGoalOverrideIntent(normalized) {
		sm.SetPrimaryGoal(normalized)
		sm.UpdateDelta(map[string]float64{"GoalPersistence": -0.24})
		_ = sm.Save()
		return normalized, fmt.Sprintf("goal shift accepted by explicit override (shift=%.2f)", shift)
	}

	if candidateWords < minWords || shift < requiredShift {
		locked := enforceGoalAnchor(currentGoal, normalized, module)
		sm.SetPrimaryGoal(currentGoal)
		sm.UpdateDelta(map[string]float64{"GoalPersistence": 0.04})
		_ = sm.Save()
		return locked, fmt.Sprintf("goal lock active (shift=%.2f required=%.2f coeff=%.2f)", shift, requiredShift, coeff)
	}

	sm.SetPrimaryGoal(normalized)
	sm.UpdateDelta(map[string]float64{"GoalPersistence": -0.12})
	_ = sm.Save()
	return normalized, fmt.Sprintf("goal shift accepted (shift=%.2f required=%.2f)", shift, requiredShift)
}

func enforceGoalAnchor(goal string, request string, module string) string {
	goal = strings.TrimSpace(goal)
	request = strings.TrimSpace(request)
	if goal == "" {
		return request
	}
	if request == "" {
		return "Primary objective: " + goal
	}
	if strings.Contains(strings.ToLower(request), strings.ToLower(goal)) {
		return request
	}
	module = strings.ToLower(strings.TrimSpace(module))
	switch module {
	case "research":
		return "Within active objective [" + goal + "], process this request: " + request
	case "multi-agent":
		return "Stay aligned to active objective [" + goal + "] while handling: " + request
	default:
		return "Keep focus on active objective [" + goal + "]. Request: " + request
	}
}

func isGoalOverrideIntent(s string) bool {
	l := strings.ToLower(strings.TrimSpace(s))
	if l == "" {
		return false
	}
	for _, marker := range []string{
		"new goal:", "switch goal", "override goal", "change objective",
		"different objective", "focus now on", "pivot to", "ignore previous goal",
	} {
		if strings.Contains(l, marker) {
			return true
		}
	}
	return false
}

func goalShiftScore(a, b string) float64 {
	at := tokenizeGoal(a)
	bt := tokenizeGoal(b)
	if len(at) == 0 || len(bt) == 0 {
		return 1.0
	}
	shared := 0
	for token := range at {
		if bt[token] {
			shared++
		}
	}
	union := len(at) + len(bt) - shared
	if union <= 0 {
		return 0
	}
	jacc := float64(shared) / float64(union)
	return clamp01Budget(1.0 - jacc)
}

func tokenizeGoal(s string) map[string]bool {
	clean := strings.ToLower(strings.TrimSpace(s))
	clean = goalStopwordRE.ReplaceAllString(clean, " ")
	out := map[string]bool{}
	for _, w := range strings.Fields(clean) {
		if len(w) <= 2 {
			continue
		}
		switch w {
		case "the", "and", "for", "with", "from", "this", "that", "into", "then", "now", "goal", "objective":
			continue
		}
		out[w] = true
	}
	return out
}

func wordCountGoal(s string) int {
	return len(strings.Fields(strings.TrimSpace(s)))
}

func shouldBypassGoalLock(normalized string, module string) bool {
	if !strings.EqualFold(strings.TrimSpace(module), "chat") {
		return false
	}
	lower := strings.ToLower(strings.TrimSpace(normalized))
	if lower == "" {
		return false
	}
	if strings.HasPrefix(lower, "new goal:") ||
		strings.Contains(lower, "switch goal") ||
		strings.Contains(lower, "override goal") ||
		strings.Contains(lower, "change objective") {
		return false
	}
	if strings.HasSuffix(lower, "?") {
		return true
	}
	for _, prefix := range []string{
		"what ", "what's ", "who ", "who's ", "when ", "where ", "why ", "how ",
		"can you ", "could you ", "would you ",
	} {
		if strings.HasPrefix(lower, prefix) {
			return true
		}
	}
	return false
}

func floatEnvGoal(key string, fallback float64) float64 {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback
	}
	v, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return fallback
	}
	return v
}

func clamp01Budget(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}
