package taloscli

import (
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"

	"github.com/Thynaptic/P-LMv1/pkg/tools"
)

const (
	autoToolsEnabledEnv  = "TALOS_AUTO_TOOLS_ENABLED"
	autoToolsMaxDepthEnv = "TALOS_AUTO_TOOLS_MAX_DEPTH"
	autoToolsMaxStepsEnv = "TALOS_AUTO_TOOLS_MAX_STEPS"
)

var urlExtractRE = regexp.MustCompile(`https?://[^\s)]+`)
var shellCmdRE = regexp.MustCompile("`([^`]+)`")

func autoToolsEnabled() bool {
	return envBoolDefault(autoToolsEnabledEnv, true)
}

func autoToolsMaxDepth() int {
	return clampInt(autoToolEnvInt(autoToolsMaxDepthEnv, 2), 1, 4)
}

func autoToolsMaxSteps() int {
	return clampInt(autoToolEnvInt(autoToolsMaxStepsEnv, 8), 1, 24)
}

func executeRecursiveAutoTool(tc *tools.GLMToolClient, call toolInvocation, depth int) (string, error) {
	if !autoToolsEnabled() {
		return "", fmt.Errorf("auto tools disabled")
	}
	maxDepth := autoToolsMaxDepth()
	if override := getArgInt(call.Args, "max_depth", 0); override > 0 {
		maxDepth = clampInt(override, 1, 4)
	}
	if depth >= maxDepth {
		return "", fmt.Errorf("auto-tool recursion limit reached at depth %d", depth)
	}
	goal := resolveAutoToolGoal(call)
	if goal == "" {
		return "", fmt.Errorf("auto_tool requires non-empty goal/query/objective")
	}

	steps := synthesizeAutoToolSteps(goal, depth, maxDepth)
	if len(steps) == 0 {
		return "", fmt.Errorf("auto-tool synthesis produced no executable steps")
	}
	if len(steps) > autoToolsMaxSteps() {
		steps = steps[:autoToolsMaxSteps()]
	}

	type stepReport struct {
		Index  int                    `json:"index"`
		Tool   string                 `json:"tool"`
		Args   map[string]interface{} `json:"args"`
		Status string                 `json:"status"`
		Output string                 `json:"output,omitempty"`
		Error  string                 `json:"error,omitempty"`
	}
	report := struct {
		Mode      string       `json:"mode"`
		Goal      string       `json:"goal"`
		Depth     int          `json:"depth"`
		MaxDepth  int          `json:"max_depth"`
		StepCount int          `json:"step_count"`
		Steps     []stepReport `json:"steps"`
	}{
		Mode:      "recursive_auto_tool_synthesis",
		Goal:      goal,
		Depth:     depth,
		MaxDepth:  maxDepth,
		StepCount: len(steps),
		Steps:     make([]stepReport, 0, len(steps)),
	}

	for i, step := range steps {
		sr := stepReport{
			Index: i + 1,
			Tool:  step.Tool,
			Args:  step.Args,
		}
		var (
			out string
			err error
		)
		if step.Tool == "auto_tool" {
			out, err = executeRecursiveAutoTool(tc, step, depth+1)
		} else {
			out, err = executeToolCall(tc, step)
		}
		if err != nil {
			sr.Status = "error"
			sr.Error = strings.TrimSpace(err.Error())
			report.Steps = append(report.Steps, sr)
			payload, _ := json.Marshal(report)
			return string(payload), err
		}
		sr.Status = "ok"
		sr.Output = truncateForModel(strings.TrimSpace(out))
		report.Steps = append(report.Steps, sr)
	}

	payload, _ := json.Marshal(report)
	return string(payload), nil
}

func synthesizeAutoToolSteps(goal string, depth int, maxDepth int) []toolInvocation {
	goal = strings.TrimSpace(goal)
	if goal == "" {
		return nil
	}
	clauses := splitAutoToolGoal(goal)
	if len(clauses) == 0 {
		clauses = []string{goal}
	}
	steps := make([]toolInvocation, 0, len(clauses))
	for _, clause := range clauses {
		clause = strings.TrimSpace(clause)
		if clause == "" {
			continue
		}
		if shouldRecurseAutoTool(clause) && depth+1 < maxDepth {
			steps = append(steps, toolInvocation{
				Tool: "auto_tool",
				Args: map[string]interface{}{"goal": clause},
			})
			continue
		}
		steps = append(steps, chooseAutoToolStep(clause))
	}
	return sanitizeToolCalls(steps)
}

func resolveAutoToolGoal(call toolInvocation) string {
	for _, key := range []string{"goal", "query", "objective", "task", "intent"} {
		if v := strings.TrimSpace(getArgString(call.Args, key, "")); v != "" {
			return v
		}
	}
	if v := strings.TrimSpace(getArgString(call.Args, "requested_tool", "")); v != "" {
		return v
	}
	tool := strings.TrimSpace(strings.ToLower(call.Tool))
	if tool == "" || tool == "auto_tool" {
		return ""
	}
	return strings.TrimSpace(call.Tool)
}

func splitAutoToolGoal(goal string) []string {
	raw := strings.TrimSpace(goal)
	if raw == "" {
		return nil
	}
	parts := []string{raw}
	separators := []string{" and then ", " then ", ";", "\n"}
	for _, sep := range separators {
		next := make([]string, 0, len(parts))
		for _, p := range parts {
			chunks := strings.Split(p, sep)
			for _, c := range chunks {
				if t := strings.TrimSpace(c); t != "" {
					next = append(next, t)
				}
			}
		}
		if len(next) > 0 {
			parts = next
		}
	}
	return parts
}

func shouldRecurseAutoTool(clause string) bool {
	l := strings.ToLower(strings.TrimSpace(clause))
	if l == "" {
		return false
	}
	if strings.Count(l, " and ") >= 2 {
		return true
	}
	return strings.Contains(l, "compare") && strings.Contains(l, "across")
}

func chooseAutoToolStep(clause string) toolInvocation {
	l := strings.ToLower(strings.TrimSpace(clause))
	if l == "" {
		return toolInvocation{Tool: "web_search", Args: map[string]interface{}{"query": clause}}
	}
	if u := strings.TrimSpace(urlExtractRE.FindString(clause)); u != "" {
		if strings.Contains(l, "api") || strings.Contains(l, "endpoint") || strings.Contains(l, "post ") || strings.Contains(l, "put ") {
			return toolInvocation{Tool: "http_request", Args: map[string]interface{}{"method": "GET", "url": u}}
		}
		return toolInvocation{Tool: "fetch_url", Args: map[string]interface{}{"url": u}}
	}
	if strings.Contains(l, "vector") || strings.Contains(l, "memory") || strings.Contains(l, "trace") || strings.Contains(l, "chunk") {
		return toolInvocation{Tool: "vector_retrieve", Args: map[string]interface{}{"query": clause, "namespace": "knowledge_base", "top_k": 5}}
	}
	if strings.Contains(l, "http") || strings.Contains(l, "request ") || strings.Contains(l, "endpoint") {
		return toolInvocation{Tool: "http_request", Args: map[string]interface{}{"method": "GET", "url": "https://example.com"}}
	}
	if strings.Contains(l, "run ") || strings.Contains(l, "execute ") || strings.Contains(l, "command ") {
		if m := shellCmdRE.FindStringSubmatch(clause); len(m) == 2 {
			return toolInvocation{Tool: "sys_exec", Args: map[string]interface{}{"command": strings.TrimSpace(m[1]), "timeout_seconds": 20}}
		}
		return toolInvocation{Tool: "execute_code", Args: map[string]interface{}{"language": "bash", "code": "echo \"auto-tool placeholder\""}}
	}
	if strings.Contains(l, "screen") || strings.Contains(l, "ui ") || strings.Contains(l, "visual") || strings.Contains(l, "element") {
		return toolInvocation{Tool: "analyze_visual_target", Args: map[string]interface{}{
			"consent": false,
			"intent":  "analyze_ui_element",
			"target":  map[string]interface{}{"label": "auto_target", "x": 0, "y": 0, "width": 160, "height": 90},
		}}
	}
	return toolInvocation{Tool: "web_search", Args: map[string]interface{}{"query": clause}}
}

func autoToolEnvInt(key string, fallback int) int {
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
