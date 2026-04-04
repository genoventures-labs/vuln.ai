package taloscli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/Thynaptic/P-LMv1/pkg/toolflow"
	"github.com/Thynaptic/P-LMv1/pkg/tools"
)

const (
	toolflowV3EnabledEnv          = "TALOS_TOOLFLOW_V3_ENABLED"
	toolflowShadowEvalEnv         = "TALOS_TOOLFLOW_SHADOW_EVAL"
	toolflowMaxWorkersEnv         = "TALOS_TOOLFLOW_MAX_WORKERS"
	toolflowNodeTimeoutMSEnv      = "TALOS_TOOLFLOW_NODE_TIMEOUT_MS"
	toolflowTraceEnabledEnv       = "TALOS_TOOLFLOW_TRACE_ENABLED"
	toolflowTraceVerboseEnv       = "TALOS_TOOLFLOW_TRACE_VERBOSE"
	toolflowFailPolicyEnv         = "TALOS_TOOLFLOW_FAIL_POLICY"
	toolflowDeprecationsEnv       = "TALOS_TOOLFLOW_DEPRECATION_WARNINGS"
	toolflowAutoInferenceEnv      = "TALOS_TOOLFLOW_AUTO_DEP_INFERENCE"
	toolflowRetryEnabledEnv       = "TALOS_TOOLFLOW_RETRY_ENABLED"
	toolflowRetryMaxAttemptsEnv   = "TALOS_TOOLFLOW_RETRY_MAX_ATTEMPTS"
	toolflowRetryBackoffMSEnv     = "TALOS_TOOLFLOW_RETRY_BACKOFF_MS"
	toolflowRetryJitterMSEnv      = "TALOS_TOOLFLOW_RETRY_JITTER_MS"
	toolflowGlobalBudgetMSEnv     = "TALOS_TOOLFLOW_GLOBAL_BUDGET_MS"
	toolflowDefaultMaxWorkers     = 4
	toolflowDefaultNodeTimeout    = 12_000
	toolflowDefaultGlobalBudgetMS = 20_000
)

type toolflowExecItem struct {
	Tool   string
	Output string
	Err    error
}

type toolflowEventObserver func(toolflow.ExecEvent) bool

func toolflowV3Enabled() bool {
	return envBoolDefault(toolflowV3EnabledEnv, true)
}

func toolflowShadowEvalEnabled() bool {
	return envBoolDefault(toolflowShadowEvalEnv, true)
}

func toolflowTraceEnabled() bool {
	return envBoolDefault(toolflowTraceEnabledEnv, true)
}

func toolflowTraceVerbose() bool {
	return envBoolDefault(toolflowTraceVerboseEnv, false)
}

func toolflowDeprecationsEnabled() bool {
	return envBoolDefault(toolflowDeprecationsEnv, true)
}

func toolflowAutoInferenceEnabled() bool {
	return envBoolDefault(toolflowAutoInferenceEnv, true)
}

func toolflowRetryEnabled() bool {
	return envBoolDefault(toolflowRetryEnabledEnv, true)
}

func toolflowFailClosed() bool {
	mode := strings.ToLower(strings.TrimSpace(os.Getenv(toolflowFailPolicyEnv)))
	if mode == "" {
		mode = "closed"
	}
	return mode != "open"
}

func toolflowMaxWorkers() int {
	return envIntDefault(toolflowMaxWorkersEnv, toolflowDefaultMaxWorkers)
}

func toolflowNodeTimeout() time.Duration {
	ms := envIntDefault(toolflowNodeTimeoutMSEnv, toolflowDefaultNodeTimeout)
	if ms <= 0 {
		ms = toolflowDefaultNodeTimeout
	}
	return time.Duration(ms) * time.Millisecond
}

func toolflowGlobalBudget() time.Duration {
	ms := envIntDefault(toolflowGlobalBudgetMSEnv, toolflowDefaultGlobalBudgetMS)
	if ms <= 0 {
		ms = toolflowDefaultGlobalBudgetMS
	}
	return time.Duration(ms) * time.Millisecond
}

func toolflowRetryMaxAttempts() int {
	return envIntDefault(toolflowRetryMaxAttemptsEnv, 2)
}

func toolflowRetryBackoff() time.Duration {
	return time.Duration(envIntDefault(toolflowRetryBackoffMSEnv, 250)) * time.Millisecond
}

func toolflowRetryJitter() time.Duration {
	return time.Duration(envIntDefault(toolflowRetryJitterMSEnv, 100)) * time.Millisecond
}

func parseToolCallsV3Aware(fullResponse string) ([]toolInvocation, bool, []string, error) {
	calls, ok, dep, err := toolflow.ParseLegacyAndV3ToolCalls(fullResponse, normalizeToolName, isSupportedTool)
	if err != nil {
		return nil, false, nil, err
	}
	if !ok || len(calls) == 0 {
		return nil, false, dep, nil
	}
	out := make([]toolInvocation, 0, len(calls))
	for _, c := range calls {
		out = append(out, toolInvocation{Tool: c.Tool, Args: c.Args})
	}
	return out, true, dep, nil
}

func runToolflowV3(tc *tools.GLMToolClient, taskQuery string, calls []toolInvocation, turnID string, surface string, stage string) ([]toolflowExecItem, string, []string, error) {
	items, status, dep, _, err := runToolflowV3Observed(tc, taskQuery, calls, turnID, surface, stage, nil)
	return items, status, dep, err
}

func runToolflowV3Observed(tc *tools.GLMToolClient, taskQuery string, calls []toolInvocation, turnID string, surface string, stage string, observer toolflowEventObserver) ([]toolflowExecItem, string, []string, bool, error) {
	if tc == nil {
		return nil, "", nil, false, fmt.Errorf("tool client unavailable")
	}
	loadRatio, loadErr := detectToolflowCPULoadRatio()
	if loadErr != nil {
		loadRatio = 0.25
	}
	autotuneWarnings := make([]string, 0, 2)
	if loadErr != nil {
		autotuneWarnings = append(autotuneWarnings, loadErr.Error())
	}
	var applyNotes []string
	var learnNotes []string
	var artifactNotes []string

	inv := make([]toolflow.Invocation, 0, len(calls))
	for _, c := range calls {
		call, _ := arbitrateToolCall(c, taskQuery)
		inv = append(inv, toolflow.Invocation{Tool: call.Tool, Args: call.Args, Source: "talos"})
	}
	optimizer, optErr := globalToolflowParamOptimizer()
	if optErr != nil {
		autotuneWarnings = append(autotuneWarnings, optErr.Error())
	} else if optimizer != nil {
		tuned, notes, err := optimizer.Apply(inv, loadRatio)
		if err != nil {
			autotuneWarnings = append(autotuneWarnings, err.Error())
		} else {
			inv = tuned
			applyNotes = notes
		}
	}
	resolvedInv, aNotes, aWarnings := applyArtifactDrivenInputs(inv)
	inv = resolvedInv
	if len(aNotes) > 0 {
		artifactNotes = aNotes
	}
	if len(aWarnings) > 0 {
		autotuneWarnings = append(autotuneWarnings, aWarnings...)
	}
	policy := toolflow.NewStrictPolicy(toolflow.DefaultToolSpecs())
	reg := toolflow.NewRegistry()
	for _, toolName := range []string{
		"web_search", "fetch_url", "http_request", "vector_retrieve", "execute_code", "sys_exec", "capture_screen", "watch_terminal", "draw_box", "draw_war_room",
		"analyze_visual_target", "doc_search", "multimodal_tool", "auto_tool",
		"provision_client", "rotate_client_key", "revoke_client", "admin_list_clients", "admin_create_client", "admin_rotate_client", "admin_delete_client",
	} {
		name := toolName
		err := reg.Register(name, func(ctx context.Context, args map[string]interface{}) (string, error) {
			return executeToolCall(tc, toolInvocation{Tool: name, Args: args})
		})
		if err != nil {
			return nil, "", nil, false, err
		}
	}
	sequentialDefault := !toolflowAutoInferenceEnabled()
	execCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var pivotTriggered atomic.Bool
	rt := &toolflow.Runtime{
		Policy:   policy,
		Registry: reg,
		ExecOpts: toolflow.ExecOptions{
			MaxWorkers:        toolflowMaxWorkers(),
			NodeTimeout:       toolflowNodeTimeout(),
			FailClosed:        toolflowFailClosed(),
			EnableTrace:       toolflowTraceEnabled(),
			SequentialDefault: sequentialDefault,
			GlobalBudget:      toolflowGlobalBudget(),
			RetryEnabled:      toolflowRetryEnabled(),
			RetryMaxAttempts:  toolflowRetryMaxAttempts(),
			RetryBackoff:      toolflowRetryBackoff(),
			RetryJitter:       toolflowRetryJitter(),
			OnEvent: func(ev toolflow.ExecEvent) {
				if observer == nil {
					return
				}
				if observer(ev) {
					pivotTriggered.Store(true)
					cancel()
				}
			},
		},
		PlanOpts: toolflow.PlanOptions{SequentialDefault: sequentialDefault},
	}
	hints := toolflow.PlannerHints{Query: strings.TrimSpace(taskQuery), Surface: strings.TrimSpace(surface), Stage: strings.TrimSpace(stage), BudgetMS: int(toolflowGlobalBudget().Milliseconds())}
	plan, results, trace, err := rt.RunWithHints(execCtx, turnID, inv, hints)
	if err != nil && !(pivotTriggered.Load() && errors.Is(err, context.Canceled)) {
		return nil, "", trace.DeprecationNotes, false, err
	}
	if optimizer != nil {
		notes, learnErr := optimizer.Learn(plan, results, loadRatio)
		if learnErr != nil {
			autotuneWarnings = append(autotuneWarnings, learnErr.Error())
		} else {
			learnNotes = notes
		}
	}
	items := make([]toolflowExecItem, 0, len(results))
	for _, r := range results {
		items = append(items, toolflowExecItem{Tool: r.Tool, Output: r.Output, Err: r.Err})
	}
	status := toolflow.FormatASCIIStatus(plan, trace, results, toolflowMaxWorkers())
	if len(artifactNotes) > 0 {
		status += "ARTIFACT_INPUT: " + strings.Join(artifactNotes, ", ") + "\n"
	}
	if toolflowAutotuneEnabled() {
		status += formatAutotuneStatus(loadRatio, applyNotes, learnNotes, autotuneWarnings)
	}
	if pivotTriggered.Load() {
		status += "TOOLFLOW_STREAM_PIVOT: detected high-signal stream marker; execution canceled for replanning\n"
	}
	return items, status, trace.DeprecationNotes, pivotTriggered.Load(), nil
}

func runToolflowShadowEval(taskQuery string, calls []toolInvocation, turnID string) {
	if !toolflowShadowEvalEnabled() || len(calls) == 0 {
		return
	}
	// Shadow eval validates parser + policy planning only; no side-effecting tool execution.
	inv := make([]toolflow.Invocation, 0, len(calls))
	for _, c := range calls {
		call, _ := arbitrateToolCall(c, taskQuery)
		inv = append(inv, toolflow.Invocation{Tool: call.Tool, Args: call.Args, Source: "shadow"})
	}
	policy := toolflow.NewStrictPolicy(toolflow.DefaultToolSpecs())
	for _, c := range inv {
		if _, _, err := policy.Normalize(c.Tool, c.Args); err != nil {
			fmt.Printf("DEBUG: toolflow shadow policy warning: %v\n", err)
		}
	}
}

func envBoolDefault(key string, fallback bool) bool {
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

func envIntDefault(key string, fallback int) int {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return fallback
	}
	return n
}
