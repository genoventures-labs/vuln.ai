package cognition

import (
	"fmt"
	"strings"
	"time"

	"github.com/Thynaptic/P-LMv1/pkg/state"
)

type SupervisionInput struct {
	Stage          SupervisionStage
	Query          string
	Candidate      string
	ContextFacts   []string
	Session        state.SessionState
	LiveShellFacts []string
	SourceRefs     []string
	CodeAudit      *CodeAuditOptions
	Metadata       map[string]string
}

type SupervisionDecision struct {
	Outcome           SupervisionOutcome
	RiskTier          RiskTier
	Violations        []string
	ConfidencePenalty float64
	NextAction        string
	AuditID           string
	Cached            bool
	LatencyMicros     int64
}

// RunSymbolicSupervision executes deterministic-first symbolic supervision.
func RunSymbolicSupervision(in SupervisionInput, policy SupervisionPolicy) (SupervisionDecision, error) {
	start := time.Now()
	if !policy.Enabled {
		return SupervisionDecision{
			Outcome:       SupervisionPass,
			RiskTier:      RiskLow,
			NextAction:    "continue",
			LatencyMicros: time.Since(start).Microseconds(),
		}, nil
	}
	in.Candidate = strings.TrimSpace(in.Candidate)
	if in.Stage == "" {
		in.Stage = StageSynthesis
	}
	key := supervisionCacheKey(in, policy)
	if cached, ok := globalSupervisionCache.get(key); ok {
		cached.Cached = true
		return cached, nil
	}

	decision := SupervisionDecision{
		Outcome:       SupervisionPass,
		RiskTier:      RiskLow,
		NextAction:    "continue",
		Violations:    []string{},
		LatencyMicros: 0,
	}

	if in.Candidate == "" {
		decision.Outcome = SupervisionSoftWarn
		decision.RiskTier = RiskMedium
		decision.Violations = append(decision.Violations, "candidate output is empty")
		decision.NextAction = "fallback"
		finalizeSupervision(&decision, in, policy, start, key)
		return decision, nil
	}

	// Deterministic high-severity leakage checks.
	if leaks := DetectSecretLeaks(in.Candidate); len(leaks) > 0 {
		decision.RiskTier = RiskCritical
		decision.Violations = append(decision.Violations, leaks...)
		decision.Outcome = SupervisionHardVeto
		decision.NextAction = "correct"
		finalizeSupervision(&decision, in, policy, start, key)
		return decision, nil
	}

	// Deterministic assertion checks against state + shell facts.
	assertions := CheckSymbolicAssertions(in.Candidate, in.Session, in.LiveShellFacts)
	if assertions.Veto {
		decision.RiskTier = RiskHigh
		decision.Outcome = SupervisionHardVeto
		decision.NextAction = "correct"
		decision.Violations = append(decision.Violations, assertions.Violations...)
	}

	// Context contradiction checks.
	if len(in.ContextFacts) > 0 {
		contradiction := EvaluateLogic(in.Candidate, in.ContextFacts)
		if contradiction >= policy.ContradictionVetoAt {
			decision.RiskTier = RiskHigh
			decision.Outcome = SupervisionHardVeto
			decision.NextAction = "correct"
			decision.Violations = append(decision.Violations,
				fmt.Sprintf("candidate contradicts known context (score=%.2f)", contradiction))
		} else if contradiction >= policy.ContradictionWarnAt && decision.Outcome != SupervisionHardVeto {
			decision.RiskTier = maxRiskTier(decision.RiskTier, RiskMedium)
			decision.Outcome = SupervisionSoftWarn
			decision.ConfidencePenalty = maxFloatSup(decision.ConfidencePenalty, 0.12)
			decision.Violations = append(decision.Violations,
				fmt.Sprintf("moderate contradiction risk against context (score=%.2f)", contradiction))
		}
	}

	// Stage-specific code audit.
	if in.CodeAudit != nil {
		violations := AuditGoCodeSymbolic(*in.CodeAudit)
		if len(violations) > 0 {
			decision.RiskTier = RiskHigh
			decision.Outcome = SupervisionHardVeto
			decision.NextAction = "correct"
			decision.Violations = append(decision.Violations, violations...)
		}
	}

	// Stage-specific source grounding.
	if policy.RequireSourcesByStage[in.Stage] {
		if len(in.SourceRefs) == 0 && looksAssertive(in.Candidate) {
			if decision.Outcome != SupervisionHardVeto {
				decision.Outcome = SupervisionSoftWarn
				decision.RiskTier = maxRiskTier(decision.RiskTier, RiskMedium)
				decision.ConfidencePenalty = maxFloatSup(decision.ConfidencePenalty, 0.15)
				decision.NextAction = "correct"
			}
			decision.Violations = append(decision.Violations, "assertive finding without cited sources")
		}
	}

	// Recursive self-alignment against evolving project philosophy and worldview history.
	if selfPolicy := DefaultSelfAlignmentPolicy(policy.EnforcementMode); selfPolicy.Enabled {
		if signal, err := RunRecursiveSelfAlignment(in, selfPolicy); err == nil {
			if signal.Score >= selfPolicy.VetoAt {
				decision.RiskTier = RiskHigh
				decision.Outcome = SupervisionHardVeto
				decision.NextAction = "correct"
				decision.Violations = append(decision.Violations,
					fmt.Sprintf("self-alignment drift %.2f exceeds veto threshold %.2f", signal.Score, selfPolicy.VetoAt))
				decision.Violations = append(decision.Violations, signal.Violations...)
			} else if signal.Score >= selfPolicy.WarnAt && decision.Outcome != SupervisionHardVeto {
				decision.RiskTier = maxRiskTier(decision.RiskTier, RiskMedium)
				decision.Outcome = SupervisionSoftWarn
				decision.ConfidencePenalty = maxFloatSup(decision.ConfidencePenalty, 0.14)
				if decision.NextAction == "continue" {
					decision.NextAction = "correct"
				}
				decision.Violations = append(decision.Violations,
					fmt.Sprintf("self-alignment drift %.2f exceeds warning threshold %.2f", signal.Score, selfPolicy.WarnAt))
				decision.Violations = append(decision.Violations, signal.Violations...)
			}
		} else {
			decision.Violations = append(decision.Violations, "self-alignment check skipped: "+err.Error())
		}
	}

	applyPolicyMode(&decision, policy)
	finalizeSupervision(&decision, in, policy, start, key)
	return decision, nil
}

func applyPolicyMode(d *SupervisionDecision, policy SupervisionPolicy) {
	if d == nil {
		return
	}
	switch policy.EnforcementMode {
	case "hard":
		if d.Outcome == SupervisionSoftWarn {
			d.Outcome = SupervisionHardVeto
			if d.NextAction == "continue" {
				d.NextAction = "correct"
			}
			d.RiskTier = maxRiskTier(d.RiskTier, RiskHigh)
		}
	case "advisory":
		if d.Outcome == SupervisionHardVeto {
			d.Outcome = SupervisionSoftWarn
			d.NextAction = "continue"
			if d.RiskTier == RiskCritical {
				d.RiskTier = RiskHigh
			}
		}
	default:
		// tiered default: no rewrite
	}
}

func finalizeSupervision(d *SupervisionDecision, in SupervisionInput, policy SupervisionPolicy, start time.Time, key string) {
	if d == nil {
		return
	}
	d.Violations = dedupeViolations(d.Violations)
	if d.Outcome == SupervisionPass && len(d.Violations) > 0 {
		d.Outcome = SupervisionSoftWarn
		d.RiskTier = maxRiskTier(d.RiskTier, RiskMedium)
	}
	if d.Outcome == SupervisionPass && d.RiskTier == "" {
		d.RiskTier = RiskLow
	}
	if strings.TrimSpace(d.NextAction) == "" {
		d.NextAction = "continue"
	}
	d.ConfidencePenalty = clampScore(d.ConfidencePenalty)
	d.LatencyMicros = time.Since(start).Microseconds()
	globalSupervisionCache.set(key, *d)
	appendSupervisionAudit(in, *d)
}

func maxRiskTier(a, b RiskTier) RiskTier {
	order := map[RiskTier]int{
		RiskLow:      0,
		RiskMedium:   1,
		RiskHigh:     2,
		RiskCritical: 3,
	}
	if order[a] >= order[b] {
		return a
	}
	return b
}

func maxFloatSup(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}
