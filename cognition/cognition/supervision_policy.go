package cognition

import (
	"os"
	"strconv"
	"strings"
	"time"
)

type SupervisionOutcome string

const (
	SupervisionPass     SupervisionOutcome = "pass"
	SupervisionSoftWarn SupervisionOutcome = "soft_warn"
	SupervisionHardVeto SupervisionOutcome = "hard_veto"
	SupervisionDefer    SupervisionOutcome = "defer"
)

type SupervisionStage string

const (
	StageDraft           SupervisionStage = "stage_draft"
	StageToolResult      SupervisionStage = "stage_tool_result"
	StageSynthesis       SupervisionStage = "stage_synthesis"
	StageFinalCode       SupervisionStage = "stage_final_code"
	StageResearchFinding SupervisionStage = "stage_research_finding"
	StageMultiAgentMerge SupervisionStage = "stage_multi_agent_merge"
)

type RiskTier string

const (
	RiskLow      RiskTier = "low"
	RiskMedium   RiskTier = "medium"
	RiskHigh     RiskTier = "high"
	RiskCritical RiskTier = "critical"
)

type SupervisionPolicy struct {
	Enabled               bool
	EnforcementMode       string
	MaxCorrections        int
	Timeout               time.Duration
	ContradictionWarnAt   float64
	ContradictionVetoAt   float64
	RequireSourcesByStage map[SupervisionStage]bool
}

func DefaultSupervisionPolicy(mode string) SupervisionPolicy {
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode == "" {
		mode = "balanced"
	}
	p := SupervisionPolicy{
		Enabled:             envBoolSup("TALOS_SYMBOLIC_SUPERVISION_ENABLED", true),
		EnforcementMode:     strings.ToLower(strings.TrimSpace(os.Getenv("TALOS_SYMBOLIC_ENFORCEMENT_MODE"))),
		MaxCorrections:      envIntSup("TALOS_SYMBOLIC_MAX_CORRECTIONS", 2),
		Timeout:             time.Duration(clampIntSup(envIntSup("TALOS_SYMBOLIC_TIMEOUT_MS", 120), 60, 2000)) * time.Millisecond,
		ContradictionWarnAt: 0.55,
		ContradictionVetoAt: 0.78,
		RequireSourcesByStage: map[SupervisionStage]bool{
			StageResearchFinding: true,
		},
	}
	if p.EnforcementMode == "" {
		p.EnforcementMode = "tiered"
	}
	if p.EnforcementMode != "tiered" && p.EnforcementMode != "hard" && p.EnforcementMode != "advisory" {
		p.EnforcementMode = "tiered"
	}
	if mode == "deep" {
		p.ContradictionWarnAt = 0.50
		p.ContradictionVetoAt = 0.72
	}
	return p
}

func envBoolSup(key string, fallback bool) bool {
	raw := strings.ToLower(strings.TrimSpace(os.Getenv(key)))
	switch raw {
	case "":
		return fallback
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	default:
		return fallback
	}
}

func envIntSup(key string, fallback int) int {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback
	}
	v, err := strconv.Atoi(raw)
	if err != nil {
		return fallback
	}
	return v
}

func clampIntSup(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
