package cognition

import (
	"strings"

	"github.com/Thynaptic/P-LMv1/pkg/state"
)

// Topology defines the reasoning geometry selected for a query.
type Topology string

const (
	TopologySpike     Topology = "spike"     // Linear: Research -> Synthesis
	TopologyBloom     Topology = "bloom"     // Branching: Research -> N Drafts -> Eval -> Synthesis
	TopologyOuroboros Topology = "ouroboros" // Recursive: Research -> Draft -> Adversarial/Refine loops -> Synthesis
	TopologyArchitect Topology = "architect" // Plan-Act-Reflect
)

// DetermineTopology selects a base topology from query-only heuristics.
func DetermineTopology(query string) Topology {
	q := strings.ToLower(strings.TrimSpace(query))
	if q == "" {
		return TopologySpike
	}

	ambiguousMarkers := []string{
		"compare", "tradeoff", "which", "maybe", "unclear", "depends", "options",
		"pros and cons", "alternatives", "multiple", "several", "branch",
	}
	deepMarkers := []string{
		"prove", "formal", "consistency", "contradiction", "audit", "deep",
		"recursive", "adversarial", "root cause", "architecture review",
		"threat model", "multi-step", "complex",
	}
	technicalMarkers := []string{
		"api", "server", "vps", "docker", "golang", "pipeline", "vector",
		"orchestration", "benchmark", "state manager", "mcts", "tot", "rag",
	}
	projectMarkers := []string{
		"set up", "setup", "build", "implement", "deploy", "migrate", "harden", "secure",
		"roadmap", "project", "phases", "milestones", "architecture plan",
	}

	ambiguousScore := markerScore(q, ambiguousMarkers)
	deepScore := markerScore(q, deepMarkers)
	techScore := markerScore(q, technicalMarkers)
	projectScore := markerScore(q, projectMarkers)

	if projectScore >= 0.15 || (strings.Contains(q, "for ") && len(q) > 60 && strings.Contains(q, "set up")) {
		return TopologyArchitect
	}

	if deepScore >= 0.18 || (techScore >= 0.25 && len(q) > 120) {
		return TopologyOuroboros
	}
	if ambiguousScore >= 0.15 || strings.Count(q, " and ") >= 2 || strings.Count(q, ",") >= 3 {
		return TopologyBloom
	}
	if len(q) > 180 {
		return TopologyBloom
	}
	return TopologySpike
}

// DetermineTopologyWithState adds "state gravity" using analytical mode/confidence.
func DetermineTopologyWithState(query string, s state.SessionState) Topology {
	base := DetermineTopology(query)

	if base == TopologyArchitect {
		return TopologyArchitect
	}

	// Low confidence or high analytical drive forces deeper recursive checking.
	if s.Confidence < 0.45 || s.AnalyticalMode >= 0.72 {
		return TopologyOuroboros
	}
	// High goal persistence resists branch inflation on otherwise moderate prompts.
	if s.GoalPersistence >= 0.78 && base == TopologyBloom && len(strings.TrimSpace(query)) < 180 {
		return TopologySpike
	}
	// Low goal persistence allows broader branching when ambiguity exists.
	if s.GoalPersistence <= 0.30 && base == TopologySpike && (strings.Count(query, " and ") >= 2 || strings.Contains(strings.ToLower(query), "compare")) {
		return TopologyBloom
	}
	// High confidence + low ambiguity can collapse to efficient linear path.
	if s.Confidence >= 0.8 && s.AnalyticalMode < 0.55 && base == TopologyBloom && len(strings.TrimSpace(query)) < 90 {
		return TopologySpike
	}
	return base
}

// DetermineTopologyWithTruthShift adjusts cognitive shape based on worldview truth-shift severity.
// High-severity truth shifts force recursive verification geometry.
func DetermineTopologyWithTruthShift(query string, s state.SessionState, shiftDetected bool, shiftSeverity float64, conflictIndex float64) Topology {
	base := DetermineTopologyWithState(query, s)
	sev := clamp01Geometry(maxFloatGeometry(shiftSeverity, conflictIndex))

	if !shiftDetected && sev < 0.35 {
		return base
	}
	if sev >= 0.78 {
		return TopologyOuroboros
	}
	if sev >= 0.52 {
		switch base {
		case TopologySpike:
			return TopologyBloom
		case TopologyBloom:
			return TopologyOuroboros
		default:
			return base
		}
	}
	if shiftDetected && sev >= 0.35 && base == TopologySpike {
		return TopologyBloom
	}
	return base
}

func markerScore(q string, markers []string) float64 {
	if len(markers) == 0 {
		return 0
	}
	hits := 0
	for _, m := range markers {
		if strings.Contains(q, m) {
			hits++
		}
	}
	return float64(hits) / float64(len(markers))
}

func clamp01Geometry(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

func maxFloatGeometry(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}
