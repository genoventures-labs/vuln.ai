package toolflow

import (
	"strings"
	"testing"
)

func TestDefaultToolSpecsIncludeAutoTool(t *testing.T) {
	p := NewStrictPolicy(DefaultToolSpecs())
	if !p.Allow("auto_tool") {
		t.Fatal("expected auto_tool allowlisted")
	}
	if err := p.Validate("auto_tool", map[string]interface{}{"goal": "compare options"}); err != nil {
		t.Fatalf("expected valid auto_tool payload: %v", err)
	}
}

func TestDefaultToolSpecsIncludeMultimodalTool(t *testing.T) {
	p := NewStrictPolicy(DefaultToolSpecs())
	if !p.Allow("multimodal_tool") {
		t.Fatal("expected multimodal_tool allowlisted")
	}
	if err := p.Validate("multimodal_tool", map[string]interface{}{"query": "debug ui"}); err != nil {
		t.Fatalf("expected valid multimodal payload: %v", err)
	}
}

func TestDefaultToolSpecsIncludeDocSearchTool(t *testing.T) {
	p := NewStrictPolicy(DefaultToolSpecs())
	if !p.Allow("doc_search") {
		t.Fatal("expected doc_search allowlisted")
	}
	if err := p.Validate("doc_search", map[string]interface{}{"query": "Sasswall"}); err != nil {
		t.Fatalf("expected valid doc_search payload: %v", err)
	}
}

func TestParseLegacyAndV3ToolCallsObjectStyleAutoTool(t *testing.T) {
	payload := `{"auto_tool":{"goal":"compare memory lanes"}}`
	norm := func(name string) string { return strings.TrimSpace(strings.ToLower(name)) }
	supported := func(name string) bool { return name == "auto_tool" }
	calls, ok, dep, err := ParseLegacyAndV3ToolCalls(payload, norm, supported)
	if err != nil {
		t.Fatalf("unexpected parse err: %v", err)
	}
	if !ok || len(calls) != 1 {
		t.Fatalf("expected one call, got ok=%v len=%d", ok, len(calls))
	}
	if calls[0].Tool != "auto_tool" {
		t.Fatalf("expected auto_tool, got %s", calls[0].Tool)
	}
	if len(dep) == 0 {
		t.Fatalf("expected deprecation note for object-style payload")
	}
}

func TestParseLegacyAndV3ToolCallsObjectStyleMultimodalTool(t *testing.T) {
	payload := `{"multimodal_tool":{"query":"analyze ui error and memory traces"}}`
	norm := func(name string) string { return strings.TrimSpace(strings.ToLower(name)) }
	supported := func(name string) bool { return name == "multimodal_tool" }
	calls, ok, dep, err := ParseLegacyAndV3ToolCalls(payload, norm, supported)
	if err != nil {
		t.Fatalf("unexpected parse err: %v", err)
	}
	if !ok || len(calls) != 1 {
		t.Fatalf("expected one call, got ok=%v len=%d", ok, len(calls))
	}
	if calls[0].Tool != "multimodal_tool" {
		t.Fatalf("expected multimodal_tool, got %s", calls[0].Tool)
	}
	if len(dep) == 0 {
		t.Fatalf("expected deprecation note for object-style payload")
	}
}

func TestParseLegacyAndV3ToolCallsObjectStyleDocSearchTool(t *testing.T) {
	payload := `{"doc_search":{"query":"goal persistence","dir":"./docs"}}`
	norm := func(name string) string { return strings.TrimSpace(strings.ToLower(name)) }
	supported := func(name string) bool { return name == "doc_search" }
	calls, ok, dep, err := ParseLegacyAndV3ToolCalls(payload, norm, supported)
	if err != nil {
		t.Fatalf("unexpected parse err: %v", err)
	}
	if !ok || len(calls) != 1 {
		t.Fatalf("expected one call, got ok=%v len=%d", ok, len(calls))
	}
	if calls[0].Tool != "doc_search" {
		t.Fatalf("expected doc_search, got %s", calls[0].Tool)
	}
	if len(dep) == 0 {
		t.Fatalf("expected deprecation note for object-style payload")
	}
}
