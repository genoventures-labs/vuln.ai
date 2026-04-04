package memory

import (
	"os"
	"strconv"
	"strings"
)

const (
	marEnabledEnv             = "TALOS_MAR_ENABLED"
	marCandidateLimitEnv      = "TALOS_MAR_CANDIDATE_LIMIT"
	marMaxAnchorsEnv          = "TALOS_MAR_MAX_ANCHORS"
	marWeightsEnv             = "TALOS_MAR_WEIGHTS"
	marMinAnchorScoreEnv      = "TALOS_MAR_MIN_SCORE"
	marTopologyBoostEnv       = "TALOS_MAR_TOPOLOGY_BOOST"
	marCacheTTLEnv            = "TALOS_MAR_CACHE_TTL_SEC"
	marCacheMaxEnv            = "TALOS_MAR_CACHE_MAX"
	marStatusReportEnabledEnv = "TALOS_MAR_STATUS_ENABLED"
)

// MARPolicy configures Memory-Anchored Reasoning retrieval and ranking.
type MARPolicy struct {
	Enabled          bool
	CandidateLimit   int
	MaxAnchors       int
	MinAnchorScore   float64
	TopologyBoost    float64
	StatusReport     bool
	WeightSemantic   float64
	WeightLexical    float64
	WeightImportance float64
	WeightFreshness  float64
	WeightTopology   float64
}

func defaultMARPolicy() MARPolicy {
	weights := parseWeightListEnv(marWeightsEnv, []float64{0.40, 0.20, 0.20, 0.15, 0.05})
	candidateLimit := envIntWithFloor(marCandidateLimitEnv, 24, 8)
	maxAnchors := envIntWithFloor(marMaxAnchorsEnv, 12, 4)
	if maxAnchors > candidateLimit {
		maxAnchors = candidateLimit
	}
	minScore := envFloatWithBounds(marMinAnchorScoreEnv, 0.20, 0, 1)
	topologyBoost := envFloatWithBounds(marTopologyBoostEnv, 0.08, 0, 0.50)
	return MARPolicy{
		Enabled:          envBoolDefaultTrue(marEnabledEnv),
		CandidateLimit:   candidateLimit,
		MaxAnchors:       maxAnchors,
		MinAnchorScore:   minScore,
		TopologyBoost:    topologyBoost,
		StatusReport:     envBoolDefaultTrue(marStatusReportEnabledEnv),
		WeightSemantic:   weights[0],
		WeightLexical:    weights[1],
		WeightImportance: weights[2],
		WeightFreshness:  weights[3],
		WeightTopology:   weights[4],
	}
}

// EffectiveMARPolicy resolves the current memory-anchored reasoning policy from environment.
func EffectiveMARPolicy() MARPolicy {
	return defaultMARPolicy()
}

func envBoolDefaultTrue(key string) bool {
	raw := strings.ToLower(strings.TrimSpace(os.Getenv(key)))
	switch raw {
	case "", "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func envBoolDefaultFalse(key string) bool {
	raw := strings.ToLower(strings.TrimSpace(os.Getenv(key)))
	switch raw {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func envIntWithFloor(key string, fallback int, floor int) int {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback
	}
	v, err := strconv.Atoi(raw)
	if err != nil {
		return fallback
	}
	if v < floor {
		return floor
	}
	return v
}

func envFloatWithBounds(key string, fallback float64, lo float64, hi float64) float64 {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback
	}
	v, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return fallback
	}
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
