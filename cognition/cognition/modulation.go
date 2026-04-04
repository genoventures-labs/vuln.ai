package cognition

import (
	"math"
	"os"
	"strconv"
	"strings"

	"github.com/Thynaptic/P-LMv1/pkg/state"
)

type SubThoughtWeights struct {
	Base                 float64
	Adversarial          float64
	Evidence             float64
	ContradictionPenalty float64
}

type ReasoningModulationProfile struct {
	Entropy                state.EntropySettings
	EmotionPressure        float64
	EmotionTrend           float64
	EmotionVolatility      float64
	GoalPersistence        float64
	DensityScale           float64
	PredictiveIntervention string
	BranchBudget           int
	SectionBudget          int
	PruneThreshold         float64
	Weights                SubThoughtWeights
}

func BuildReasoningModulation(sm *state.Manager, query string, mode string) ReasoningModulationProfile {
	snapshot := state.SessionState{
		Confidence:      0.5,
		Urgency:         0.5,
		AnalyticalMode:  0.5,
		Frustration:     0.5,
		GoalPersistence: 0.5,
	}
	carry := 0.0
	if sm != nil {
		snapshot = sm.GetSnapshot()
		carry = sm.SentimentCarryover()
	}
	entropy := state.DetermineEntropy(snapshot, query)
	goalPersistence := clamp01Mod(snapshot.GoalPersistence)
	basePressure := clamp01Mod((snapshot.Frustration * 0.55) + ((1.0 - snapshot.Confidence) * 0.25) + (maxFloatMod(0, -carry) * 0.20))
	emotionTrend, emotionVolatility := sentimentTrendAndVolatility(snapshot.MoodHistory)
	predictiveEnabled := envBoolMod("TALOS_PREDICTIVE_INTERVENTION_ENABLED", true)
	predictiveGain := clampRangeMod(floatFromEnvMod("TALOS_PREDICTIVE_DENSITY_GAIN", 0.22), 0.0, 0.8)
	trendRisk := clamp01Mod(maxFloatMod(0, -emotionTrend))
	volRisk := clamp01Mod(emotionVolatility)
	predictivePressure := 0.0
	if predictiveEnabled {
		predictivePressure = clamp01Mod((trendRisk * 0.65) + (volRisk * 0.35))
	}
	emotionPressure := clamp01Mod(basePressure + (predictivePressure * predictiveGain))

	minDensity := floatFromEnvMod("TALOS_EMOTION_DENSITY_MIN", 0.75)
	maxDensity := floatFromEnvMod("TALOS_EMOTION_DENSITY_MAX", 1.50)
	if minDensity <= 0 {
		minDensity = 0.75
	}
	if maxDensity < minDensity {
		maxDensity = minDensity
	}
	rawDensity := 1.0 - (emotionPressure * 0.40) + (maxFloatMod(0, carry) * 0.20) - (predictivePressure * predictiveGain)
	density := clampRangeMod(rawDensity, minDensity, maxDensity)
	predictiveIntervention := ""
	alertAt := clampRangeMod(floatFromEnvMod("TALOS_PREDICTIVE_DENSITY_ALERT_AT", 0.45), 0.1, 0.95)
	if predictiveEnabled && predictivePressure >= alertAt {
		predictiveIntervention = "emotion-aware-density-scaling"
	}

	mode = strings.ToLower(strings.TrimSpace(mode))
	branchBase := 3
	sectionBase := 8
	switch mode {
	case "minimal":
		branchBase = 2
		sectionBase = 6
	case "deep":
		branchBase = 4
		sectionBase = 10
	}
	complexity := estimateModulationComplexity(query)
	branchBudget := clampIntMod(int(float64(branchBase)+float64(complexity)/3.0), 2, 5)
	sectionBudget := clampIntMod(int(float64(sectionBase)*density), 4, 16)

	gain := clampRangeMod(floatFromEnvMod("TALOS_GOAL_PERSISTENCE_GAIN", 0.35), 0.0, 1.0)
	if goalPersistence >= 0.7 {
		branchBudget = clampIntMod(int(float64(branchBudget)-(goalPersistence-0.7)*2.0*gain), 2, 5)
	} else if goalPersistence <= 0.35 {
		branchBudget = clampIntMod(int(float64(branchBudget)+(0.35-goalPersistence)*2.0*gain), 2, 5)
	}

	pruneMin := clampRangeMod(floatFromEnvMod("TALOS_DYNAMIC_PRUNE_MIN", 0.22), 0.05, 0.95)
	pruneMax := clampRangeMod(floatFromEnvMod("TALOS_DYNAMIC_PRUNE_MAX", 0.40), pruneMin, 0.99)
	pruneThreshold := pruneMin + (((emotionPressure + (1.0 - goalPersistence)) / 2.0) * (pruneMax - pruneMin))

	return ReasoningModulationProfile{
		Entropy:                entropy,
		EmotionPressure:        emotionPressure,
		EmotionTrend:           emotionTrend,
		EmotionVolatility:      emotionVolatility,
		GoalPersistence:        goalPersistence,
		DensityScale:           density,
		PredictiveIntervention: predictiveIntervention,
		BranchBudget:           branchBudget,
		SectionBudget:          sectionBudget,
		PruneThreshold:         clampRangeMod(pruneThreshold, pruneMin, pruneMax),
		Weights:                parseSubThoughtWeights(),
	}
}

func parseSubThoughtWeights() SubThoughtWeights {
	raw := strings.TrimSpace(os.Getenv("TALOS_SUBTHOUGHT_WEIGHTS"))
	out := []float64{0.45, 0.30, 0.15, 0.10}
	if raw != "" {
		parts := strings.Split(raw, ",")
		if len(parts) == 4 {
			next := make([]float64, 0, 4)
			ok := true
			for _, p := range parts {
				v, err := strconv.ParseFloat(strings.TrimSpace(p), 64)
				if err != nil || v < 0 {
					ok = false
					break
				}
				next = append(next, v)
			}
			if ok {
				sum := 0.0
				for _, v := range next {
					sum += v
				}
				if sum > 0 {
					for i := range next {
						next[i] = next[i] / sum
					}
					out = next
				}
			}
		}
	}
	return SubThoughtWeights{
		Base:                 out[0],
		Adversarial:          out[1],
		Evidence:             out[2],
		ContradictionPenalty: out[3],
	}
}

func estimateModulationComplexity(query string) int {
	q := strings.ToLower(strings.TrimSpace(query))
	if q == "" {
		return 0
	}
	score := 0
	if len(q) > 80 {
		score++
	}
	if len(q) > 180 {
		score++
	}
	for _, marker := range []string{
		"compare", "tradeoff", "multi", "branch", "deep", "contradiction", "architecture", "reason", "evidence",
	} {
		if strings.Contains(q, marker) {
			score++
		}
	}
	return score
}

func floatFromEnvMod(key string, fallback float64) float64 {
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

func envBoolMod(key string, fallback bool) bool {
	raw := strings.ToLower(strings.TrimSpace(os.Getenv(key)))
	if raw == "" {
		return fallback
	}
	switch raw {
	case "1", "true", "yes", "y", "on":
		return true
	case "0", "false", "no", "n", "off":
		return false
	default:
		return fallback
	}
}

func sentimentTrendAndVolatility(history []float64) (float64, float64) {
	if len(history) < 2 {
		return 0, 0
	}
	// Focus on recent affective drift for predictive interventions.
	maxN := 8
	if len(history) < maxN {
		maxN = len(history)
	}
	h := history[len(history)-maxN:]
	split := len(h) / 2
	if split < 1 {
		split = 1
	}
	prevAvg := avgFloatMod(h[:split])
	currAvg := avgFloatMod(h[split:])
	trend := clampRangeMod(currAvg-prevAvg, -1, 1)
	vol := clamp01Mod(stddevFloatMod(h) / 0.75)
	return trend, vol
}

func avgFloatMod(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	sum := 0.0
	for _, v := range values {
		sum += v
	}
	return sum / float64(len(values))
}

func stddevFloatMod(values []float64) float64 {
	if len(values) <= 1 {
		return 0
	}
	mean := avgFloatMod(values)
	acc := 0.0
	for _, v := range values {
		d := v - mean
		acc += d * d
	}
	return math.Sqrt(acc / float64(len(values)))
}

func clampRangeMod(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func clamp01Mod(v float64) float64 {
	return clampRangeMod(v, 0, 1)
}

func clampIntMod(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func maxFloatMod(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}
