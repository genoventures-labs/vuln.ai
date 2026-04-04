package taloscli

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Thynaptic/P-LMv1/pkg/tools"
)

const (
	multimodalToolsEnabledEnv = "TALOS_MULTIMODAL_TOOLS_ENABLED"
)

type multimodalStep struct {
	Shard string
	Call  toolInvocation
}

func multimodalToolsEnabled() bool {
	return envBoolDefault(multimodalToolsEnabledEnv, true)
}

func executeMultimodalTool(tc *tools.GLMToolClient, call toolInvocation) (string, error) {
	if !multimodalToolsEnabled() {
		return "", fmt.Errorf("multimodal tools disabled")
	}
	args := cloneArgs(call.Args)
	bundle, notes, warnings := resolveArtifactBundle(args)
	query := resolveMultimodalQuery(args, bundle)
	steps, shards := buildMultimodalSteps(args, query)
	if len(steps) == 0 {
		return "", fmt.Errorf("multimodal_tool requires at least one modality (query/artifact/visual target)")
	}

	type shardReport struct {
		Shard  string `json:"shard"`
		Tool   string `json:"tool"`
		Status string `json:"status"`
		Output string `json:"output,omitempty"`
		Error  string `json:"error,omitempty"`
	}
	report := struct {
		Mode          string        `json:"mode"`
		Query         string        `json:"query,omitempty"`
		CognitiveLoad int           `json:"cognitive_load"`
		ActiveShards  []string      `json:"active_shards"`
		Notes         []string      `json:"notes,omitempty"`
		Warnings      []string      `json:"warnings,omitempty"`
		Citations     []string      `json:"citations,omitempty"`
		Steps         []shardReport `json:"steps"`
	}{
		Mode:          "multimodal_tool_integration",
		Query:         strings.TrimSpace(query),
		CognitiveLoad: len(steps),
		ActiveShards:  shards,
		Notes:         trimNotes(notes, 10),
		Warnings:      trimNotes(warnings, 8),
		Citations:     multimodalCitations(args, bundle),
		Steps:         make([]shardReport, 0, len(steps)),
	}

	for _, step := range steps {
		out, err := executeToolCall(tc, step.Call)
		r := shardReport{
			Shard: step.Shard,
			Tool:  step.Call.Tool,
		}
		if err != nil {
			r.Status = "error"
			r.Error = strings.TrimSpace(err.Error())
			report.Steps = append(report.Steps, r)
			payload, _ := json.Marshal(report)
			return string(payload), err
		}
		r.Status = "ok"
		r.Output = truncateText(strings.TrimSpace(out), 1200)
		report.Steps = append(report.Steps, r)
	}
	payload, _ := json.Marshal(report)
	return string(payload), nil
}

func buildMultimodalSteps(args map[string]interface{}, query string) ([]multimodalStep, []string) {
	target := resolveMultimodalTarget(args)
	consent := getArgBool(args, "consent", false)
	intent := getArgString(args, "intent", "analyze_ui_element")
	shellContext := getArgString(args, "shell_context", "")
	useVector := getArgBool(args, "use_vector", true)
	useWeb := getArgBool(args, "use_web", true)
	useFetch := getArgBool(args, "use_fetch", false)
	steps := make([]multimodalStep, 0, 4)
	shards := make([]string, 0, 4)

	if target != nil {
		steps = append(steps, multimodalStep{
			Shard: "vision",
			Call: toolInvocation{
				Tool: "analyze_visual_target",
				Args: map[string]interface{}{
					"consent":       consent,
					"intent":        intent,
					"shell_context": shellContext,
					"target":        target,
				},
			},
		})
		shards = append(shards, "vision")
	}
	if strings.TrimSpace(query) != "" && useVector {
		steps = append(steps, multimodalStep{
			Shard: "memory",
			Call: toolInvocation{
				Tool: "vector_retrieve",
				Args: map[string]interface{}{
					"query":     query,
					"namespace": getArgString(args, "namespace", "knowledge_base"),
					"top_k":     getArgInt(args, "top_k", 5),
					"filters":   getArgMap(args, "filters"),
				},
			},
		})
		shards = append(shards, "memory")
	}
	if strings.TrimSpace(query) != "" && useWeb {
		steps = append(steps, multimodalStep{
			Shard: "web",
			Call: toolInvocation{
				Tool: "web_search",
				Args: map[string]interface{}{
					"query":        query,
					"top_k":        getArgInt(args, "top_k", 5),
					"recency_days": getArgInt(args, "recency_days", 0),
					"site_filter":  getArgStringSlice(args, "site_filter"),
				},
			},
		})
		shards = append(shards, "web")
	}
	if useFetch {
		url := strings.TrimSpace(getArgString(args, "url", ""))
		if url != "" {
			steps = append(steps, multimodalStep{
				Shard: "fetch",
				Call: toolInvocation{
					Tool: "fetch_url",
					Args: map[string]interface{}{
						"url":       url,
						"max_chars": getArgInt(args, "max_chars", 0),
					},
				},
			})
			shards = append(shards, "fetch")
		}
	}
	return steps, shards
}

func resolveMultimodalQuery(args map[string]interface{}, bundle artifactBundle) string {
	for _, key := range []string{"query", "goal", "task", "objective", "intent"} {
		if q := strings.TrimSpace(getArgString(args, key, "")); q != "" {
			return q
		}
	}
	if q := strings.TrimSpace(buildArtifactQuery(bundle)); q != "" {
		return q
	}
	target := resolveMultimodalTarget(args)
	if target == nil {
		return ""
	}
	label := strings.TrimSpace(getArgString(target, "label", ""))
	snippet := strings.TrimSpace(getArgString(target, "snippet", ""))
	switch {
	case label != "" && snippet != "":
		return truncateText(label+" "+snippet, 240)
	case label != "":
		return label
	case snippet != "":
		return truncateText(snippet, 240)
	default:
		return ""
	}
}

func resolveMultimodalTarget(args map[string]interface{}) map[string]interface{} {
	target := getArgMap(args, "target")
	if target == nil {
		target = getArgMap(args, "visual_target")
	}
	if target == nil {
		target = map[string]interface{}{}
	}
	if strings.TrimSpace(getArgString(target, "capture_path", "")) == "" {
		if p := strings.TrimSpace(getArgString(args, "image_path", "")); p != "" {
			target["capture_path"] = p
		}
	}
	if strings.TrimSpace(getArgString(target, "label", "")) == "" {
		if s := strings.TrimSpace(getArgString(args, "label", "")); s != "" {
			target["label"] = s
		}
	}
	if strings.TrimSpace(getArgString(target, "snippet", "")) == "" {
		if s := strings.TrimSpace(getArgString(args, "snippet", "")); s != "" {
			target["snippet"] = s
		}
	}
	hasSignal := strings.TrimSpace(getArgString(target, "capture_path", "")) != "" ||
		strings.TrimSpace(getArgString(target, "label", "")) != "" ||
		strings.TrimSpace(getArgString(target, "snippet", "")) != ""
	if !hasSignal {
		return nil
	}
	if getArgInt(target, "width", getArgInt(target, "w", 0)) <= 0 {
		target["width"] = 160
	}
	if getArgInt(target, "height", getArgInt(target, "h", 0)) <= 0 {
		target["height"] = 90
	}
	return target
}

func multimodalCitations(args map[string]interface{}, bundle artifactBundle) []string {
	out := append([]string(nil), bundle.Sources...)
	if p := strings.TrimSpace(getArgString(args, "image_path", "")); p != "" {
		out = append(out, p)
	}
	if t := resolveMultimodalTarget(args); t != nil {
		if p := strings.TrimSpace(getArgString(t, "capture_path", "")); p != "" {
			out = append(out, p)
		}
	}
	return uniqueTrimmed(out, 16)
}
