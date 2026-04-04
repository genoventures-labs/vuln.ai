package cognition

import (
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	reflectionV2EnabledEnv      = "TALOS_REFLECTION_V2_ENABLED"
	reflectionModeEnv           = "TALOS_REFLECTION_ENFORCEMENT_MODE"
	reflectionWarnThresholdEnv  = "TALOS_REFLECTION_WARN_THRESHOLD"
	reflectionSteerThresholdEnv = "TALOS_REFLECTION_STEER_THRESHOLD"
	reflectionVetoThresholdEnv  = "TALOS_REFLECTION_VETO_THRESHOLD"
	reflectionTimeoutMSEnv      = "TALOS_REFLECTION_TIMEOUT_MS"
	reflectionCitationGateEnv   = "TALOS_REFLECTION_CITATION_GATE"
	reflectionStatusEnabledEnv  = "TALOS_REFLECTION_STATUS_ENABLED"
	reflectionLogPathEnv        = "TALOS_REFLECTION_LOG_PATH"
	reflectionCacheTTLEnv       = "TALOS_REFLECTION_CACHE_TTL_SEC"
	reflectionCacheMaxEnv       = "TALOS_REFLECTION_CACHE_MAX"
)

const defaultReflectionAuditPath = ".memory/reflection_audit.jsonl"

type ReflectionPolicy struct {
	Enabled            bool
	EnforcementMode    string
	WarnThreshold      float64
	SteerThreshold     float64
	VetoThreshold      float64
	Timeout            time.Duration
	CitationGate       bool
	StatusEnabled      bool
	AuditLogPath       string
	CacheTTL           time.Duration
	CacheMax           int
	CorrectionMaxPass  int
	CorrectionFallback string
}

func DefaultReflectionPolicy(mode string) ReflectionPolicy {
	warn := envFloatWithBounds(reflectionWarnThresholdEnv, 0.32, 0, 1)
	steer := envFloatWithBounds(reflectionSteerThresholdEnv, 0.52, 0, 1)
	veto := envFloatWithBounds(reflectionVetoThresholdEnv, 0.78, 0, 1)
	if steer < warn {
		steer = warn
	}
	if veto < steer {
		veto = steer
	}
	timeoutMS := envIntWithFloor(reflectionTimeoutMSEnv, 250, 50)
	cacheTTL := time.Duration(envIntWithFloor(reflectionCacheTTLEnv, 90, 10)) * time.Second
	cacheMax := envIntWithFloor(reflectionCacheMaxEnv, 512, 64)
	enf := normalizeReflectionEnforcement(firstNonEmptyEnv(reflectionModeEnv, strings.TrimSpace(mode)))
	return ReflectionPolicy{
		Enabled:            envBoolDefaultTrue(reflectionV2EnabledEnv),
		EnforcementMode:    enf,
		WarnThreshold:      warn,
		SteerThreshold:     steer,
		VetoThreshold:      veto,
		Timeout:            time.Duration(timeoutMS) * time.Millisecond,
		CitationGate:       envBoolDefaultTrue(reflectionCitationGateEnv),
		StatusEnabled:      envBoolDefaultTrue(reflectionStatusEnabledEnv),
		AuditLogPath:       firstNonEmptyEnv(reflectionLogPathEnv, defaultReflectionAuditPath),
		CacheTTL:           cacheTTL,
		CacheMax:           cacheMax,
		CorrectionMaxPass:  1,
		CorrectionFallback: "conservative",
	}
}

func firstNonEmptyEnv(keys ...string) string {
	for _, k := range keys {
		if strings.TrimSpace(k) == "" {
			continue
		}
		v := strings.TrimSpace(os.Getenv(k))
		if v != "" {
			return v
		}
	}
	return ""
}

func normalizeReflectionEnforcement(raw string) string {
	raw = strings.ToLower(strings.TrimSpace(raw))
	switch raw {
	case "hard", "strict":
		return "hard"
	case "advisory", "warn":
		return "advisory"
	default:
		return "tiered"
	}
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

func envIntWithFloor(key string, fallback, floor int) int {
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

func envFloatWithBounds(key string, fallback, lo, hi float64) float64 {
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
