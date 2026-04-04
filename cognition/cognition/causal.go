package cognition

import (
	"math"
	"sort"
	"strings"
)

// CausalNode links an action/effect to downstream outcomes and local risk.
type CausalNode struct {
	Name     string
	Outcomes []string
	Risk     float64 // [0,1]
}

// Counterfactual captures one "what-if" simulation branch.
type Counterfactual struct {
	Scenario      string
	RiskSignals   []string
	FragilityPart float64 // [0,1]
}

// CausalAssessment is the aggregate safety output for strategy gating.
type CausalAssessment struct {
	Action         string
	Simulations    []Counterfactual
	FragilityScore float64 // [0,1]
	Safe           bool
}

// UpgradeSimulation captures counterfactual outcomes for self-upgrade proposals.
type UpgradeSimulation struct {
	ChangeDescription      string           `json:"change_description"`
	EfficiencyDelta        float64          `json:"efficiency_delta"`          // positive is better
	RegressiveRisk         float64          `json:"regressive_risk"`           // [0,1]
	CorrectionLoopDelta    float64          `json:"correction_loop_delta"`     // positive means more loops
	ResponseLatencyDeltaMS float64          `json:"response_latency_delta_ms"` // positive means slower
	Notes                  []string         `json:"notes"`
	Causal                 CausalAssessment `json:"causal"`
}

// CausalModel is a lightweight graph used for strategic risk simulation.
type CausalModel struct {
	Nodes map[string]CausalNode
}

const defaultCausalSafetyThreshold = 0.62

func NewDefaultCausalModel() *CausalModel {
	nodes := map[string]CausalNode{
		"updatepackage": {
			Name:     "UpdatePackage",
			Outcomes: []string{"ConfigDrift", "ServiceRestart"},
			Risk:     0.48,
		},
		"configdrift": {
			Name:     "ConfigDrift",
			Outcomes: []string{"DowntimeRisk"},
			Risk:     0.72,
		},
		"servicerestart": {
			Name:     "ServiceRestart",
			Outcomes: []string{"DowntimeRisk"},
			Risk:     0.55,
		},
		"manualfirewallchanges": {
			Name:     "ManualFirewallChanges",
			Outcomes: []string{"LockoutRisk", "DowntimeRisk"},
			Risk:     0.80,
		},
		"zerotrustsasswallmigration": {
			Name:     "ZeroTrustSasswallMigration",
			Outcomes: []string{"PolicyComplexity", "RolloutRisk", "DowntimeRisk"},
			Risk:     0.58,
		},
		"rolloutrisk": {
			Name:     "RolloutRisk",
			Outcomes: []string{"DowntimeRisk"},
			Risk:     0.62,
		},
		"policyascodehardenedrollout": {
			Name:     "PolicyAsCodeHardenedRollout",
			Outcomes: []string{"RolloutRisk"},
			Risk:     0.34,
		},
		"canaryandverificationfirst": {
			Name:     "CanaryAndVerificationFirst",
			Outcomes: []string{"RolloutRisk"},
			Risk:     0.28,
		},
		"downtimerisk": {
			Name:     "DowntimeRisk",
			Outcomes: []string{"IncidentEscalation"},
			Risk:     0.88,
		},
		"lockoutrisk": {
			Name:     "LockoutRisk",
			Outcomes: []string{"IncidentEscalation"},
			Risk:     0.86,
		},
		"incidentescalation": {
			Name:     "IncidentEscalation",
			Outcomes: nil,
			Risk:     0.92,
		},
	}
	return &CausalModel{Nodes: nodes}
}

// Simulate runs 3-5 what-if branches for the proposed action.
func (cm *CausalModel) Simulate(action string) CausalAssessment {
	if cm == nil || len(cm.Nodes) == 0 {
		cm = NewDefaultCausalModel()
	}
	actionKey := normalizeCausal(action)
	baseNode, ok := cm.Nodes[actionKey]
	if !ok {
		baseNode = CausalNode{Name: action, Outcomes: []string{"RolloutRisk"}, Risk: 0.46}
	}

	scenarios := []string{
		baseNode.Name + " without safeguards",
		baseNode.Name + " with canary rollout",
		baseNode.Name + " with pre-deployment verification",
		baseNode.Name + " under peak load",
		baseNode.Name + " with rollback automation",
	}

	// Use 5 when action appears high-impact, otherwise 3.
	n := 3
	if strings.Contains(actionKey, "migration") || strings.Contains(actionKey, "firewall") || strings.Contains(actionKey, "update") {
		n = 5
	}
	if n > len(scenarios) {
		n = len(scenarios)
	}
	sims := make([]Counterfactual, 0, n)
	for i := 0; i < n; i++ {
		mult := scenarioMultiplier(i)
		if strings.Contains(strings.ToLower(scenarios[i]), "canary") || strings.Contains(strings.ToLower(scenarios[i]), "verification") || strings.Contains(strings.ToLower(scenarios[i]), "rollback") {
			mult -= 0.18
		}
		part, signals := cm.traceRisk(baseNode, clamp01Causal(mult))
		sims = append(sims, Counterfactual{
			Scenario:      scenarios[i],
			RiskSignals:   signals,
			FragilityPart: part,
		})
	}

	score := 0.0
	for _, s := range sims {
		score += s.FragilityPart
	}
	score = clamp01Causal(score / float64(maxIntCausal(len(sims), 1)))
	return CausalAssessment{
		Action:         action,
		Simulations:    sims,
		FragilityScore: score,
		Safe:           score <= defaultCausalSafetyThreshold,
	}
}

func (cm *CausalModel) traceRisk(root CausalNode, mult float64) (float64, []string) {
	type item struct {
		name  string
		depth int
	}
	queue := []item{{name: normalizeCausal(root.Name), depth: 0}}
	seen := map[string]bool{}
	total := clamp01Causal(root.Risk) * mult
	signals := []string{root.Name}

	for len(queue) > 0 {
		it := queue[0]
		queue = queue[1:]
		if it.depth > 3 || seen[it.name] {
			continue
		}
		seen[it.name] = true

		node, ok := cm.Nodes[it.name]
		if !ok {
			continue
		}
		decay := math.Pow(0.72, float64(it.depth))
		total += clamp01Causal(node.Risk) * decay * mult
		for _, out := range node.Outcomes {
			outKey := normalizeCausal(out)
			signals = append(signals, out)
			queue = append(queue, item{name: outKey, depth: it.depth + 1})
		}
	}
	signals = dedupeStringsCausal(signals)
	sort.Strings(signals)
	return clamp01Causal(total / 2.8), signals
}

func scenarioMultiplier(i int) float64 {
	switch i {
	case 0:
		return 1.0
	case 1:
		return 0.82
	case 2:
		return 0.80
	case 3:
		return 1.12
	case 4:
		return 0.78
	default:
		return 1.0
	}
}

func normalizeCausal(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	repl := strings.NewReplacer(" ", "", "-", "", "_", "", "/", "", ":", "")
	return repl.Replace(s)
}

func dedupeStringsCausal(in []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(in))
	for _, v := range in {
		key := strings.ToLower(strings.TrimSpace(v))
		if key == "" || seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, strings.TrimSpace(v))
	}
	return out
}

func clamp01Causal(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

func maxIntCausal(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// SimulateSystemUpgrade runs a counterfactual for internal system upgrades.
// It is primarily used for self-modifications to cognition/orchestration paths.
func SimulateSystemUpgrade(change string) UpgradeSimulation {
	change = strings.TrimSpace(change)
	lc := strings.ToLower(change)
	cm := NewDefaultCausalModel()

	action := "SystemUpgrade"
	notes := []string{}
	loopDelta := 0.0
	latencyDelta := 0.0
	effDelta := 0.0
	regressiveRisk := 0.0

	switch {
	case strings.Contains(lc, "symbolic") && (strings.Contains(lc, "strict") || strings.Contains(lc, "stricter")):
		action = "StrictSymbolicAuditorUpgrade"
		// Expected tradeoff: more vetoes -> more correction loops and latency.
		loopDelta = 0.24
		latencyDelta = 1300
		effDelta = -0.18
		regressiveRisk = 0.28
		notes = append(notes,
			"Stricter symbolic auditing is predicted to increase CorrectionNode loop count.",
			"Expected user-visible latency increase from additional refinement cycles.")
	case strings.Contains(lc, "symbolic") && strings.Contains(lc, "cache"):
		action = "SymbolicAuditCacheUpgrade"
		loopDelta = -0.05
		latencyDelta = -450
		effDelta = 0.16
		regressiveRisk = 0.00
		notes = append(notes,
			"Caching deterministic symbolic checks reduces repeat audit cost.",
			"No additional correction-loop pressure predicted.")
	case strings.Contains(lc, "reindexer") || strings.Contains(lc, "memory"):
		action = "MemoryReindexerUpgrade"
		loopDelta = -0.03
		latencyDelta = -320
		effDelta = 0.12
		regressiveRisk = 0.00
		notes = append(notes,
			"Batch scoring reduces memory maintenance overhead.",
			"No regression risk detected under current causal model.")
	default:
		action = "GenericInternalUpgrade"
		loopDelta = 0.02
		latencyDelta = 120
		effDelta = 0.01
		regressiveRisk = 0.05
		notes = append(notes, "Generic upgrade path carries mild uncertainty.")
	}

	causal := cm.Simulate(action)
	// Fold fragility into regressive risk.
	regressiveRisk = clamp01Causal(regressiveRisk + (causal.FragilityScore * 0.2))
	return UpgradeSimulation{
		ChangeDescription:      change,
		EfficiencyDelta:        effDelta,
		RegressiveRisk:         regressiveRisk,
		CorrectionLoopDelta:    loopDelta,
		ResponseLatencyDeltaMS: latencyDelta,
		Notes:                  notes,
		Causal:                 causal,
	}
}

// PassesStabilityGate accepts only positive efficiency and zero regression risk.
func PassesStabilityGate(sim UpgradeSimulation) bool {
	return sim.EfficiencyDelta > 0 && sim.RegressiveRisk == 0
}
