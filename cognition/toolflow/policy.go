package toolflow

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// ToolSpec defines policy-level schema constraints.
type ToolSpec struct {
	Name         string
	Required     []string
	Allowed      map[string]string
	Defaults     map[string]interface{}
	AllowUnknown bool
}

// ToolPolicy validates and normalizes a tool invocation.
type ToolPolicy interface {
	Validate(tool string, args map[string]interface{}) error
	Normalize(tool string, args map[string]interface{}) (map[string]interface{}, []string, error)
	Allow(tool string) bool
}

// StrictPolicy enforces allowlist + required keys + basic type checks.
type StrictPolicy struct {
	specs map[string]ToolSpec
}

func NewStrictPolicy(specs []ToolSpec) *StrictPolicy {
	m := make(map[string]ToolSpec, len(specs))
	for _, s := range specs {
		if strings.TrimSpace(s.Name) == "" {
			continue
		}
		m[s.Name] = s
	}
	return &StrictPolicy{specs: m}
}

func (p *StrictPolicy) Allow(tool string) bool {
	if p == nil {
		return false
	}
	_, ok := p.specs[tool]
	return ok
}

func (p *StrictPolicy) Normalize(tool string, args map[string]interface{}) (map[string]interface{}, []string, error) {
	if p == nil {
		return nil, nil, fmt.Errorf("policy is nil")
	}
	spec, ok := p.specs[tool]
	if !ok {
		return nil, nil, fmt.Errorf("policy deny tool=%s code=TOOL_NOT_ALLOWLISTED", tool)
	}
	out := map[string]interface{}{}
	for k, v := range args {
		out[k] = v
	}
	for k, v := range spec.Defaults {
		if _, exists := out[k]; !exists {
			out[k] = v
		}
	}
	if !spec.AllowUnknown {
		for k := range out {
			if _, ok := spec.Allowed[k]; !ok {
				return nil, nil, fmt.Errorf("policy deny tool=%s arg=%s code=UNKNOWN_ARG", tool, k)
			}
		}
	}
	if err := p.Validate(tool, out); err != nil {
		return nil, nil, err
	}
	return out, nil, nil
}

func (p *StrictPolicy) Validate(tool string, args map[string]interface{}) error {
	if p == nil {
		return fmt.Errorf("policy is nil")
	}
	spec, ok := p.specs[tool]
	if !ok {
		return fmt.Errorf("policy deny tool=%s code=TOOL_NOT_ALLOWLISTED", tool)
	}
	for _, key := range spec.Required {
		v, ok := args[key]
		if !ok || isEmptyValue(v) {
			return fmt.Errorf("policy deny tool=%s arg=%s code=MISSING_REQUIRED_ARG", tool, key)
		}
	}
	for key, typeHint := range spec.Allowed {
		v, exists := args[key]
		if !exists || v == nil {
			continue
		}
		if err := validateType(typeHint, v); err != nil {
			return fmt.Errorf("policy deny tool=%s arg=%s code=INVALID_TYPE detail=%v", tool, key, err)
		}
	}
	return nil
}

func validateType(typeHint string, v interface{}) error {
	typeHint = strings.TrimSpace(strings.ToLower(typeHint))
	switch typeHint {
	case "string":
		if _, ok := v.(string); !ok {
			return fmt.Errorf("expected string")
		}
	case "int":
		switch v.(type) {
		case int, int32, int64, float64, float32, string:
			return nil
		default:
			return fmt.Errorf("expected int-compatible value")
		}
	case "bool":
		switch v.(type) {
		case bool, string:
			return nil
		default:
			return fmt.Errorf("expected bool-compatible value")
		}
	case "map":
		if _, ok := v.(map[string]interface{}); !ok {
			if _, ok2 := v.(map[string]string); !ok2 {
				return fmt.Errorf("expected object")
			}
		}
	case "[]string":
		switch a := v.(type) {
		case []string:
			_ = a
		case []interface{}:
			for _, e := range a {
				if _, ok := e.(string); !ok {
					return fmt.Errorf("expected string array")
				}
			}
		default:
			return fmt.Errorf("expected string array")
		}
	}
	return nil
}

func isEmptyValue(v interface{}) bool {
	switch t := v.(type) {
	case nil:
		return true
	case string:
		return strings.TrimSpace(t) == ""
	default:
		return false
	}
}

// DefaultToolSpecs returns strict tool schemas for built-in TALOS tools.
func DefaultToolSpecs() []ToolSpec {
	base := []ToolSpec{
		{
			Name:     "web_search",
			Required: []string{"query"},
			Allowed: map[string]string{
				"query": "string", "top_k": "int", "recency_days": "int", "site_filter": "[]string",
				"artifact_id": "string", "artifact_path": "string", "reflection_audit_path": "string",
				"include_evidence_notes": "bool", "include_reflection_notes": "bool",
			},
		},
		{
			Name:     "fetch_url",
			Required: []string{"url"},
			Allowed: map[string]string{
				"url": "string", "max_chars": "int",
				"artifact_id": "string", "artifact_path": "string", "reflection_audit_path": "string",
				"include_evidence_notes": "bool", "include_reflection_notes": "bool",
			},
		},
		{
			Name:     "http_request",
			Required: []string{"url"},
			Allowed: map[string]string{
				"method": "string", "url": "string", "headers": "map", "body": "string", "allowlist_profile": "string",
				"artifact_id": "string", "artifact_path": "string", "reflection_audit_path": "string",
				"include_evidence_notes": "bool", "include_reflection_notes": "bool",
			},
			Defaults: map[string]interface{}{"method": "GET"},
		},
		{
			Name:     "vector_retrieve",
			Required: []string{"query"},
			Allowed: map[string]string{
				"query": "string", "namespace": "string", "top_k": "int", "filters": "map",
				"artifact_id": "string", "artifact_path": "string", "reflection_audit_path": "string",
				"include_evidence_notes": "bool", "include_reflection_notes": "bool",
			},
			Defaults: map[string]interface{}{"namespace": "knowledge_base", "top_k": 5},
		},
		{
			Name:     "execute_code",
			Required: []string{"language", "code"},
			Allowed:  map[string]string{"language": "string", "code": "string", "timeout_seconds": "int"},
			Defaults: map[string]interface{}{"timeout_seconds": 10},
		},
		{
			Name:     "sys_exec",
			Required: []string{"command"},
			Allowed:  map[string]string{"command": "string", "override": "bool", "timeout_seconds": "int"},
			Defaults: map[string]interface{}{"override": false, "timeout_seconds": 20},
		},
		{Name: "capture_screen", Allowed: map[string]string{"consent": "bool", "mode": "string", "window_id": "string", "output_path": "string", "timeout_seconds": "int", "max_base64_chars": "int"}},
		{Name: "watch_terminal", Allowed: map[string]string{"consent": "bool", "duration_seconds": "int", "interval_ms": "int", "output_dir": "string", "max_base64_chars": "int"}, Defaults: map[string]interface{}{"duration_seconds": 30, "interval_ms": 2500}},
		{Name: "draw_box", Allowed: map[string]string{"consent": "bool", "x": "int", "y": "int", "w": "int", "h": "int", "text": "string", "duration_ms": "int"}},
		{Name: "draw_war_room", Allowed: map[string]string{"consent": "bool", "duration_ms": "int", "boxes": "map"}},
		{Name: "analyze_visual_target", Required: []string{"consent", "intent", "target"}, Allowed: map[string]string{"consent": "bool", "intent": "string", "target": "map", "shell_context": "string"}},
		{Name: "doc_search", Required: []string{"query"}, Allowed: map[string]string{
			"query": "string", "dir": "string", "regex": "bool", "case_sensitive": "bool",
			"extensions": "[]string", "exclude_dirs": "[]string", "include_hidden": "bool", "follow_symlinks": "bool",
			"max_files": "int", "max_matches": "int", "max_per_file": "int", "max_file_bytes": "int", "context_lines": "int",
		}},
		{Name: "multimodal_tool", Allowed: map[string]string{
			"query": "string", "goal": "string", "task": "string", "objective": "string", "intent": "string",
			"consent": "bool", "target": "map", "visual_target": "map", "image_path": "string",
			"label": "string", "snippet": "string", "shell_context": "string",
			"namespace": "string", "top_k": "int", "filters": "map",
			"site_filter": "[]string", "recency_days": "int",
			"url": "string", "max_chars": "int",
			"artifact_id": "string", "artifact_path": "string", "reflection_audit_path": "string",
			"include_evidence_notes": "bool", "include_reflection_notes": "bool",
			"use_vector": "bool", "use_web": "bool", "use_fetch": "bool",
		}},
		{Name: "auto_tool", Required: []string{"goal"}, Allowed: map[string]string{"goal": "string", "query": "string", "objective": "string", "task": "string", "intent": "string", "requested_tool": "string", "max_depth": "int"}},
		{Name: "provision_client", Allowed: map[string]string{"client_id": "string", "activate": "bool"}},
		{Name: "rotate_client_key", Required: []string{"client_id"}, Allowed: map[string]string{"client_id": "string", "activate": "bool"}},
		{Name: "revoke_client", Required: []string{"client_id"}, Allowed: map[string]string{"client_id": "string"}},
		{Name: "admin_list_clients", Allowed: map[string]string{}},
		{Name: "admin_create_client", Allowed: map[string]string{"client_id": "string", "activate": "bool"}},
		{Name: "admin_rotate_client", Required: []string{"client_id"}, Allowed: map[string]string{"client_id": "string", "activate": "bool"}},
		{Name: "admin_delete_client", Required: []string{"client_id"}, Allowed: map[string]string{"client_id": "string"}},
	}
	for i := range base {
		if base[i].Defaults == nil {
			base[i].Defaults = map[string]interface{}{}
		}
		if base[i].Allowed == nil {
			base[i].Allowed = map[string]string{}
		}
		if !base[i].AllowUnknown {
			keys := make([]string, 0, len(base[i].Allowed))
			for k := range base[i].Allowed {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			base[i].Defaults["_allowed_keys"] = strings.Join(keys, ",")
			delete(base[i].Defaults, "_allowed_keys")
		}
	}
	return base
}

func normalizeIntValue(v interface{}, fallback int) int {
	switch n := v.(type) {
	case int:
		return n
	case int32:
		return int(n)
	case int64:
		return int(n)
	case float64:
		return int(n)
	case float32:
		return int(n)
	case string:
		if strings.TrimSpace(n) == "" {
			return fallback
		}
		parsed, err := strconv.Atoi(strings.TrimSpace(n))
		if err != nil {
			return fallback
		}
		return parsed
	default:
		return fallback
	}
}
