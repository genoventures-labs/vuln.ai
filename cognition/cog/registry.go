package cog

import (
	"fmt"
	"strings"
)

const (
	RouteAetherCouncil = "aether_council"
	RouteSTRATAForge   = "strata_forge"
	RouteOllamaVPS     = "ollama_vps"
)

// RouteDecision is the deterministic handoff output from the decision registry.
type RouteDecision struct {
	Target string `json:"target"`
	Reason string `json:"reason"`
}

// DecisionRegistry routes thoughts to reasoning or execution endpoints.
type DecisionRegistry struct {
	OffloadThreshold float64
}

func NewDecisionRegistry(offloadThreshold float64) (*DecisionRegistry, error) {
	if offloadThreshold <= 0 || offloadThreshold > 1 {
		return nil, fmt.Errorf("decision registry: offload threshold must be in (0,1]")
	}
	return &DecisionRegistry{OffloadThreshold: offloadThreshold}, nil
}

// Route deterministically resolves the next cognitive destination.
func (r *DecisionRegistry) Route(thought ThoughtObject, currentLoad float64) (RouteDecision, error) {
	if r == nil {
		return RouteDecision{}, fmt.Errorf("decision registry: nil receiver")
	}
	if currentLoad < 0 || currentLoad > 1 {
		return RouteDecision{}, fmt.Errorf("decision registry: current load must be in [0,1]")
	}
	if strings.TrimSpace(thought.ID) == "" {
		return RouteDecision{}, fmt.Errorf("decision registry: thought id is empty")
	}

	if thought.RequiresTool {
		return RouteDecision{Target: RouteSTRATAForge, Reason: "requires tool synthesis/execution"}, nil
	}
	if thought.AllowOffload && currentLoad >= r.OffloadThreshold {
		return RouteDecision{Target: RouteOllamaVPS, Reason: "mesh offload due to high local cognitive load"}, nil
	}
	return RouteDecision{Target: RouteAetherCouncil, Reason: "default reasoning path"}, nil
}
