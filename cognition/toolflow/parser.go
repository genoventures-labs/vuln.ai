package toolflow

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

// ParseLegacyAndV3ToolCalls supports existing and canonical tool-call formats.
// normalizeName should map aliases to canonical tool names.
// isSupported should return true for allowed canonical tools.
func ParseLegacyAndV3ToolCalls(fullResponse string, normalizeName func(string) string, isSupported func(string) bool) ([]Invocation, bool, []string, error) {
	if normalizeName == nil || isSupported == nil {
		return nil, false, nil, fmt.Errorf("normalizeName and isSupported are required")
	}
	trimmed := strings.TrimSpace(stripMarkdownCodeFences(stripReasoningSections(fullResponse)))
	if trimmed == "" {
		return nil, false, nil, nil
	}
	lowerTrimmed := strings.ToLower(trimmed)

	if calls, deps, ok := parseInvocationsPayload(trimmed, normalizeName, isSupported); ok {
		return calls, len(calls) > 0, deps, nil
	}

	// Handle markdown-like wrappers: "Tool Call: ..." followed by JSON block.
	if strings.Contains(lowerTrimmed, "tool call") {
		jsonBlockRE := regexp.MustCompile(`(?s)\{.*\}|\[.*\]`)
		if block := strings.TrimSpace(jsonBlockRE.FindString(trimmed)); block != "" {
			if calls, deps, ok := parseInvocationsPayload(block, normalizeName, isSupported); ok {
				return calls, len(calls) > 0, deps, nil
			}
		}
	}

	prefixPattern := regexp.MustCompile(`(?is)^\s*([A-Z_]+)\s*:\s*(\{.*\}|\[.*\])\s*$`)
	matches := prefixPattern.FindStringSubmatch(trimmed)
	if len(matches) == 3 {
		prefix := normalizeName(matches[1])
		payloadJSON := strings.TrimSpace(matches[2])
		if prefix == "tool_call" || prefix == "tool_chain" {
			if calls, deps, ok := parseInvocationsPayload(payloadJSON, normalizeName, isSupported); ok {
				return calls, len(calls) > 0, deps, nil
			}
		}
		var payload map[string]interface{}
		if err := json.Unmarshal([]byte(payloadJSON), &payload); err == nil {
			if isSupported(prefix) {
				args := payload
				if av, found := payload["args"]; found {
					if m, ok := av.(map[string]interface{}); ok {
						args = m
					}
				}
				return []Invocation{{Tool: prefix, Args: ensureArgsMap(args)}}, true, []string{"deprecated: prefixed tool call wrapper"}, nil
			}
		}
	}

	return nil, false, nil, nil
}

func parseInvocationsPayload(payload string, normalizeName func(string) string, isSupported func(string) bool) ([]Invocation, []string, bool) {
	deprecations := []string{}

	var direct Invocation
	if err := json.Unmarshal([]byte(payload), &direct); err == nil {
		if out := sanitizeInvocations([]Invocation{direct}, normalizeName, isSupported); len(out) > 0 {
			return out, deprecations, true
		}
	}

	var directChain []Invocation
	if err := json.Unmarshal([]byte(payload), &directChain); err == nil {
		if out := sanitizeInvocations(directChain, normalizeName, isSupported); len(out) > 0 {
			return out, deprecations, true
		}
	}

	var chainContainer struct {
		ToolChain []Invocation `json:"tool_chain"`
		Tools     []Invocation `json:"tools"`
		Calls     []Invocation `json:"calls"`
		Steps     []Invocation `json:"steps"`
	}
	if err := json.Unmarshal([]byte(payload), &chainContainer); err == nil {
		for name, chain := range map[string][]Invocation{
			"tool_chain": chainContainer.ToolChain,
			"tools":      chainContainer.Tools,
			"calls":      chainContainer.Calls,
			"steps":      chainContainer.Steps,
		} {
			if out := sanitizeInvocations(chain, normalizeName, isSupported); len(out) > 0 {
				if name != "tool_chain" {
					deprecations = append(deprecations, "deprecated: legacy chain container \""+name+"\"")
				}
				return out, deprecations, true
			}
		}
	}

	var alt map[string]interface{}
	if err := json.Unmarshal([]byte(payload), &alt); err == nil {
		for _, candidate := range []string{"web_search", "fetch_url", "http_request", "vector_retrieve", "execute_code", "sys_exec", "capture_screen", "watch_terminal", "draw_box", "draw_war_room", "analyze_visual_target", "doc_search", "multimodal_tool", "auto_tool", "provision_client", "rotate_client_key", "revoke_client", "admin_list_clients", "admin_create_client", "admin_rotate_client", "admin_delete_client"} {
			if args, found := alt[candidate]; found {
				if m, ok := args.(map[string]interface{}); ok {
					out := sanitizeInvocations([]Invocation{{Tool: candidate, Args: m}}, normalizeName, isSupported)
					if len(out) > 0 {
						deprecations = append(deprecations, "deprecated: object-style tool field payload")
						return out, deprecations, true
					}
				}
			}
		}
	}
	return nil, nil, false
}

func sanitizeInvocations(calls []Invocation, normalizeName func(string) string, isSupported func(string) bool) []Invocation {
	out := make([]Invocation, 0, len(calls))
	for _, c := range calls {
		tool := normalizeName(c.Tool)
		if !isSupported(tool) {
			continue
		}
		out = append(out, Invocation{Tool: tool, Args: ensureArgsMap(c.Args), Source: c.Source, Raw: c.Raw})
	}
	return out
}

func ensureArgsMap(args map[string]interface{}) map[string]interface{} {
	if args == nil {
		return map[string]interface{}{}
	}
	return args
}

func stripReasoningSections(s string) string {
	if strings.TrimSpace(s) == "" {
		return ""
	}
	lines := strings.Split(s, "\n")
	out := make([]string, 0, len(lines))
	inReason := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(strings.ToLower(line))
		switch {
		case strings.HasPrefix(trimmed, "<think>") || strings.HasPrefix(trimmed, "<reasoning>"):
			inReason = true
			continue
		case strings.HasPrefix(trimmed, "</think>") || strings.HasPrefix(trimmed, "</reasoning>"):
			inReason = false
			continue
		}
		if inReason {
			continue
		}
		out = append(out, line)
	}
	return strings.TrimSpace(strings.Join(out, "\n"))
}

func stripMarkdownCodeFences(s string) string {
	trimmed := strings.TrimSpace(s)
	if !strings.HasPrefix(trimmed, "```") || !strings.HasSuffix(trimmed, "```") {
		return trimmed
	}
	lines := strings.Split(trimmed, "\n")
	if len(lines) < 3 {
		return trimmed
	}
	return strings.TrimSpace(strings.Join(lines[1:len(lines)-1], "\n"))
}
