package cognition

import (
	"fmt"
	"strings"
	"time"

	"github.com/Thynaptic/P-LMv1/pkg/state"
)

// GraphContext carries mutable execution context across graph nodes.
type GraphContext struct {
	Input    string
	Output   string
	Values   map[string]float64
	Metadata map[string]string
	Step     int
}

// ActionRunner executes an action node.
type ActionRunner func(input string, ctx *GraphContext, sm *state.Manager) (output string, stateDelta map[string]float64, err error)

// EvaluationRunner scores current graph output.
type EvaluationRunner func(input string, ctx *GraphContext, sm *state.Manager) (score float64, stateDelta map[string]float64, err error)

// BranchDecider chooses a branch key based on context/state.
type BranchDecider func(input string, ctx *GraphContext, sm *state.Manager) string

// AdversarialRunner performs internal critic self-play before final synthesis.
type AdversarialRunner func(input string, ctx *GraphContext, sm *state.Manager) (output string, stateDelta map[string]float64, err error)

// FusionRunner performs worldview conflict-resolution and synthesis.
type FusionRunner func(input string, ctx *GraphContext, sm *state.Manager) (output string, stateDelta map[string]float64, err error)

// CapabilityRunner performs JIT skill compilation/validation for missing capabilities.
type CapabilityRunner func(input string, ctx *GraphContext, sm *state.Manager) (output string, stateDelta map[string]float64, err error)

// AlignmentRunner performs policy audit/correction post-synthesis.
type AlignmentRunner func(input string, ctx *GraphContext, sm *state.Manager) (output string, stateDelta map[string]float64, err error)

// VisualRunner performs local VLM-based visual reasoning and verification loops.
type VisualRunner func(input string, ctx *GraphContext, sm *state.Manager) (output string, stateDelta map[string]float64, err error)

// DelegationRunner dispatches background work to a sub-agent daemon.
type DelegationRunner func(input string, ctx *GraphContext, sm *state.Manager) (output string, stateDelta map[string]float64, err error)

// ActionNode performs a concrete action (tool/model/pipeline invocation).
type ActionNode struct {
	Name string
	Run  ActionRunner
}

// EvaluationNode grades the current state/output and writes a score to context.
type EvaluationNode struct {
	Name string
	Run  EvaluationRunner
}

// BranchNode chooses the next node dynamically.
type BranchNode struct {
	Name        string
	Decide      BranchDecider
	Routes      map[string]string
	DefaultNext string
}

// AdversarialNode runs trial-by-fire critique/refinement loops.
type AdversarialNode struct {
	Name string
	Run  AdversarialRunner
}

// FusionNode merges technical worldviews into a unified synthesis.
type FusionNode struct {
	Name string
	Run  FusionRunner
}

// CapabilityNode generates/validates temporary skill primitives.
type CapabilityNode struct {
	Name string
	Run  CapabilityRunner
}

// AlignmentCorrectionNode enforces policy via audit + correction loop.
type AlignmentCorrectionNode struct {
	Name string
	Run  AlignmentRunner
}

// VisualNode executes screenshot + VLM reasoning + visual verification.
type VisualNode struct {
	Name string
	Run  VisualRunner
}

// DelegationNode sends a work order to a specific daemon and continues local reasoning.
type DelegationNode struct {
	Name string
	Run  DelegationRunner
}

// Node is a discriminated graph node that supports action/evaluation/branch behavior.
type Node struct {
	ID          string
	Next        string
	Action      *ActionNode
	Evaluation  *EvaluationNode
	Branch      *BranchNode
	Adversarial *AdversarialNode
	Fusion      *FusionNode
	Capability  *CapabilityNode
	Alignment   *AlignmentCorrectionNode
	Visual      *VisualNode
	Delegation  *DelegationNode
}

// CompiledTopology is the JIT-compiled graph plan for a topology.
type CompiledTopology struct {
	Nodes          []Node
	Start          string
	MaxSteps       int
	RecoveryNodeID string
}

// TopologyCompiler compiles nodes for a selected topology.
type TopologyCompiler func(topology Topology) (CompiledTopology, error)

// ThoughtGraph describes a deterministic, branching reasoning flow.
type ThoughtGraph struct {
	Nodes           []Node
	Start           string
	MaxSteps        int
	RecoveryNodeID  string
	Reflection      *ReflectionLayer
	DefaultTopology Topology
	Compiler        TopologyCompiler
}

// ExecuteGraph executes a topology-compiled graph and returns final context output.
func (tg *ThoughtGraph) ExecuteGraph(input string, sm *state.Manager, topology Topology) (string, error) {
	if strings.TrimSpace(input) == "" {
		return "", fmt.Errorf("graph input is empty")
	}

	compiled, err := tg.compileTopology(topology)
	if err != nil {
		return "", err
	}
	if len(compiled.Nodes) == 0 {
		return "", fmt.Errorf("thought graph has no nodes for topology %q", topology)
	}

	byID := make(map[string]Node, len(compiled.Nodes))
	for _, n := range compiled.Nodes {
		if strings.TrimSpace(n.ID) == "" {
			return "", fmt.Errorf("found node with empty ID")
		}
		byID[n.ID] = n
	}

	currentID := compiled.Start
	if strings.TrimSpace(currentID) == "" {
		currentID = compiled.Nodes[0].ID
	}
	if _, ok := byID[currentID]; !ok {
		return "", fmt.Errorf("start node %q not found", currentID)
	}

	maxSteps := compiled.MaxSteps
	if maxSteps <= 0 {
		maxSteps = 24
	}

	ctx := &GraphContext{
		Input:    input,
		Output:   input,
		Values:   make(map[string]float64),
		Metadata: make(map[string]string),
	}
	if tg.Reflection != nil {
		tg.Reflection.Start()
		defer tg.Reflection.Stop()
	}

	for step := 0; step < maxSteps; step++ {
		ctx.Step = step + 1

		node, ok := byID[currentID]
		if !ok {
			return "", fmt.Errorf("node %q not found", currentID)
		}

		nextID := node.Next
		switch {
		case node.Action != nil:
			if node.Action.Run == nil {
				return "", fmt.Errorf("action node %q missing runner", node.ID)
			}
			out, delta, err := node.Action.Run(input, ctx, sm)
			if err != nil {
				return "", fmt.Errorf("action node %q failed: %w", node.ID, err)
			}
			if strings.TrimSpace(out) != "" {
				ctx.Output = out
			}
			applyStateDelta(sm, delta)

		case node.Evaluation != nil:
			if node.Evaluation.Run == nil {
				return "", fmt.Errorf("evaluation node %q missing runner", node.ID)
			}
			score, delta, err := node.Evaluation.Run(input, ctx, sm)
			if err != nil {
				return "", fmt.Errorf("evaluation node %q failed: %w", node.ID, err)
			}
			ctx.Values[node.Evaluation.Name] = clamp01(score)
			applyStateDelta(sm, delta)

		case node.Branch != nil:
			if node.Branch.Decide == nil {
				return "", fmt.Errorf("branch node %q missing decider", node.ID)
			}
			route := strings.TrimSpace(node.Branch.Decide(input, ctx, sm))
			chosen, ok := node.Branch.Routes[route]
			if !ok || strings.TrimSpace(chosen) == "" {
				chosen = node.Branch.DefaultNext
			}
			nextID = chosen

		case node.Adversarial != nil:
			if node.Adversarial.Run == nil {
				return "", fmt.Errorf("adversarial node %q missing runner", node.ID)
			}
			out, delta, err := node.Adversarial.Run(input, ctx, sm)
			if err != nil {
				return "", fmt.Errorf("adversarial node %q failed: %w", node.ID, err)
			}
			if strings.TrimSpace(out) != "" {
				ctx.Output = out
			}
			applyStateDelta(sm, delta)

		case node.Fusion != nil:
			if node.Fusion.Run == nil {
				return "", fmt.Errorf("fusion node %q missing runner", node.ID)
			}
			out, delta, err := node.Fusion.Run(input, ctx, sm)
			if err != nil {
				return "", fmt.Errorf("fusion node %q failed: %w", node.ID, err)
			}
			if strings.TrimSpace(out) != "" {
				ctx.Output = out
			}
			applyStateDelta(sm, delta)

		case node.Capability != nil:
			if node.Capability.Run == nil {
				return "", fmt.Errorf("capability node %q missing runner", node.ID)
			}
			out, delta, err := node.Capability.Run(input, ctx, sm)
			if err != nil {
				return "", fmt.Errorf("capability node %q failed: %w", node.ID, err)
			}
			if strings.TrimSpace(out) != "" {
				ctx.Output = out
			}
			applyStateDelta(sm, delta)

		case node.Alignment != nil:
			if node.Alignment.Run == nil {
				return "", fmt.Errorf("alignment node %q missing runner", node.ID)
			}
			out, delta, err := node.Alignment.Run(input, ctx, sm)
			if err != nil {
				return "", fmt.Errorf("alignment node %q failed: %w", node.ID, err)
			}
			if strings.TrimSpace(out) != "" {
				ctx.Output = out
			}
			applyStateDelta(sm, delta)

		case node.Visual != nil:
			if node.Visual.Run == nil {
				return "", fmt.Errorf("visual node %q missing runner", node.ID)
			}
			out, delta, err := node.Visual.Run(input, ctx, sm)
			if err != nil {
				return "", fmt.Errorf("visual node %q failed: %w", node.ID, err)
			}
			if strings.TrimSpace(out) != "" {
				ctx.Output = out
			}
			applyStateDelta(sm, delta)

		case node.Delegation != nil:
			if node.Delegation.Run == nil {
				return "", fmt.Errorf("delegation node %q missing runner", node.ID)
			}
			out, delta, err := node.Delegation.Run(input, ctx, sm)
			if err != nil {
				return "", fmt.Errorf("delegation node %q failed: %w", node.ID, err)
			}
			if strings.TrimSpace(out) != "" {
				ctx.Output = out
			}
			applyStateDelta(sm, delta)

		default:
			return "", fmt.Errorf("node %q has no behavior", node.ID)
		}

		if sm != nil {
			sm.Decay()
			_ = sm.Save()
		}
		// Track how crowded the active reasoning context is for adaptive delegation.
		pressure := estimateContextPressure(ctx)
		UpdateContextWindowPressure(pressure)
		if tg.Reflection != nil {
			tg.Reflection.Publish(NodeOutputEvent{
				NodeID:    node.ID,
				Step:      ctx.Step,
				Input:     input,
				Output:    ctx.Output,
				NextNode:  nextID,
				Timestamp: time.Now().UTC(),
			})
			if node.ID != strings.TrimSpace(compiled.RecoveryNodeID) {
				if steer, ok := tg.Reflection.WaitForSteer(8 * time.Millisecond); ok && strings.TrimSpace(compiled.RecoveryNodeID) != "" {
					ctx.Metadata["steer_reason"] = steer.Reason
					nextID = compiled.RecoveryNodeID
				}
			}
		}
		if strings.TrimSpace(nextID) == "" {
			return strings.TrimSpace(ctx.Output), nil
		}
		currentID = nextID
	}

	return strings.TrimSpace(ctx.Output), fmt.Errorf("graph exceeded max steps (%d)", maxSteps)
}

func (tg *ThoughtGraph) compileTopology(topology Topology) (CompiledTopology, error) {
	t := topology
	if strings.TrimSpace(string(t)) == "" {
		t = tg.DefaultTopology
	}
	if strings.TrimSpace(string(t)) == "" {
		t = TopologySpike
	}

	if tg.Compiler != nil {
		compiled, err := tg.Compiler(t)
		if err != nil {
			return CompiledTopology{}, err
		}
		if len(compiled.Nodes) == 0 {
			return CompiledTopology{}, fmt.Errorf("compiler produced empty nodes for topology %q", t)
		}
		if compiled.MaxSteps <= 0 {
			compiled.MaxSteps = tg.MaxSteps
		}
		return compiled, nil
	}

	// Legacy static mode fallback.
	return CompiledTopology{
		Nodes:          tg.Nodes,
		Start:          tg.Start,
		MaxSteps:       tg.MaxSteps,
		RecoveryNodeID: tg.RecoveryNodeID,
	}, nil
}

func applyStateDelta(sm *state.Manager, delta map[string]float64) {
	if sm == nil || len(delta) == 0 {
		return
	}
	sm.UpdateDelta(delta)
}

func clamp01(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

func estimateContextPressure(ctx *GraphContext) float64 {
	if ctx == nil {
		return 0.2
	}
	outputLoad := float64(len(strings.TrimSpace(ctx.Output))) / 9000.0
	metaLoad := float64(len(ctx.Metadata)) / 20.0
	valueLoad := float64(len(ctx.Values)) / 16.0
	stepLoad := float64(ctx.Step) / 16.0
	return clamp01((outputLoad * 0.50) + (metaLoad * 0.20) + (valueLoad * 0.10) + (stepLoad * 0.20))
}
