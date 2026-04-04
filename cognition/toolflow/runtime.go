package toolflow

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// Runtime combines policy, planner, and executor into one deterministic engine.
type Runtime struct {
	Policy       ToolPolicy
	Registry     *Registry
	ExecOpts     ExecOptions
	PlanOpts     PlanOptions
	TraceEnabled bool
}

func (r *Runtime) Run(ctx context.Context, turnID string, invocations []Invocation) (Plan, []ToolResult, ExecutionTrace, error) {
	return r.RunWithHints(ctx, turnID, invocations, PlannerHints{})
}

// RunWithHints executes toolflow with planner/runtime hints.
func (r *Runtime) RunWithHints(ctx context.Context, turnID string, invocations []Invocation, hints PlannerHints) (Plan, []ToolResult, ExecutionTrace, error) {
	if r == nil {
		return Plan{}, nil, ExecutionTrace{}, fmt.Errorf("runtime is nil")
	}
	if r.Policy == nil {
		return Plan{}, nil, ExecutionTrace{}, fmt.Errorf("policy is nil")
	}
	if r.Registry == nil {
		return Plan{}, nil, ExecutionTrace{}, fmt.Errorf("registry is nil")
	}
	if len(invocations) == 0 {
		return Plan{}, nil, ExecutionTrace{}, fmt.Errorf("invocations are empty")
	}
	if turnID == "" {
		turnID = fmt.Sprintf("turn-%d", time.Now().UnixNano())
	}
	trace := ExecutionTrace{TurnID: turnID}
	normalized := make([]NormalizedInvocation, 0, len(invocations))

	for i, inv := range invocations {
		if !r.Policy.Allow(inv.Tool) {
			dec := PolicyDecision{Tool: inv.Tool, Allowed: false, Reason: "tool not allowlisted", ErrorCode: "TOOL_NOT_ALLOWLISTED"}
			trace.PolicyDecisions = append(trace.PolicyDecisions, dec)
			if r.ExecOpts.FailClosed {
				return Plan{}, nil, trace, fmt.Errorf("toolflow blocked tool=%s code=%s", inv.Tool, dec.ErrorCode)
			}
			continue
		}
		normArgs, dep, err := r.Policy.Normalize(inv.Tool, inv.Args)
		if err != nil {
			dec := PolicyDecision{Tool: inv.Tool, Allowed: false, Reason: err.Error(), ErrorCode: "VALIDATION_FAILED"}
			trace.PolicyDecisions = append(trace.PolicyDecisions, dec)
			if r.ExecOpts.FailClosed {
				return Plan{}, nil, trace, err
			}
			continue
		}
		normalized = append(normalized, NormalizedInvocation{Tool: inv.Tool, Args: normArgs, Canonical: true, Deprecations: dep, OriginalIdx: i})
		trace.PolicyDecisions = append(trace.PolicyDecisions, PolicyDecision{Tool: inv.Tool, Allowed: true})
		trace.DeprecationNotes = append(trace.DeprecationNotes, dep...)
	}

	if len(normalized) == 0 {
		return Plan{}, nil, trace, fmt.Errorf("no valid invocations after policy checks")
	}
	plan, err := BuildDeterministicPlanV2(normalized, r.PlanOpts, hints)
	if err != nil {
		return Plan{}, nil, trace, err
	}
	for i := range plan.Nodes {
		plan.Nodes[i].RetryPolicy = defaultRetryPolicyForTool(plan.Nodes[i].Tool, r.ExecOpts)
		if plan.Nodes[i].TimeoutMS <= 0 && r.ExecOpts.NodeTimeout > 0 {
			plan.Nodes[i].TimeoutMS = int(r.ExecOpts.NodeTimeout.Milliseconds())
		}
	}

	trace.PlanHash = plan.DeterministicHash
	results, execTrace, err := ExecutePlanV2(ctx, plan, r.Registry, r.ExecOpts)
	if err != nil {
		return plan, results, mergeTrace(trace, execTrace), err
	}
	return plan, results, mergeTrace(trace, execTrace), nil
}

func defaultRetryPolicyForTool(tool string, opts ExecOptions) RetryPolicySpec {
	p := RetryPolicySpec{MaxAttempts: maxInt(1, opts.RetryMaxAttempts), BackoffMS: int(opts.RetryBackoff.Milliseconds()), JitterMS: int(opts.RetryJitter.Milliseconds())}
	switch strings.TrimSpace(tool) {
	case "sys_exec", "execute_code", "analyze_visual_target", "doc_search", "multimodal_tool", "auto_tool", "admin_list_clients", "admin_create_client", "admin_rotate_client", "admin_delete_client", "provision_client", "rotate_client_key", "revoke_client":
		p.MaxAttempts = 1
	default:
		p.RetryableCodes = []string{"timeout", "transient", "429", "502", "503", "504"}
	}
	return p
}

func mergeTrace(base, exec ExecutionTrace) ExecutionTrace {
	merged := base
	if merged.TurnID == "" {
		merged.TurnID = exec.TurnID
	}
	if merged.PlanHash == "" {
		merged.PlanHash = exec.PlanHash
	}
	merged.Steps = append(merged.Steps, exec.Steps...)
	merged.PolicyDecisions = append(merged.PolicyDecisions, exec.PolicyDecisions...)
	merged.DeprecationNotes = append(merged.DeprecationNotes, exec.DeprecationNotes...)
	merged.Citations = uniqueSorted(append(merged.Citations, exec.Citations...))
	return merged
}
