package taloscli

import (
	"bytes"
	"strings"
	"testing"
)

func TestExplainKnownCapability(t *testing.T) {
	out := &bytes.Buffer{}
	errOut := &bytes.Buffer{}
	rootCmd.SetOut(out)
	rootCmd.SetErr(errOut)
	rootCmd.SetArgs([]string{"explain", "doctor"})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("expected explain doctor to succeed, got error: %v", err)
	}
	rendered := out.String()
	if !strings.Contains(rendered, "CAPABILITY") || !strings.Contains(rendered, "doctor") {
		t.Fatalf("expected explain output to include capability section, got: %s", rendered)
	}
	if !strings.Contains(rendered, "USAGE") || !strings.Contains(rendered, "FUNCTIONALITY") {
		t.Fatalf("expected explain output sections in response, got: %s", rendered)
	}
}

func TestExplainAliasResolution(t *testing.T) {
	doc, ok := lookupCapabilityDoc("toolserver")
	if !ok {
		t.Fatal("expected alias 'toolserver' to resolve")
	}
	if doc.Name != "tools admin" {
		t.Fatalf("expected alias to resolve tools admin, got %q", doc.Name)
	}
}

func TestExplainRebootAliasResolution(t *testing.T) {
	doc, ok := lookupCapabilityDoc("reboot")
	if !ok {
		t.Fatal("expected alias 'reboot' to resolve")
	}
	if doc.Name != "monitor" {
		t.Fatalf("expected alias to resolve monitor, got %q", doc.Name)
	}
}

func TestExplainUnknownCapability(t *testing.T) {
	out := &bytes.Buffer{}
	writeExplainNotFound(out, "does-not-exist")
	rendered := out.String()
	if !strings.Contains(rendered, "Unknown capability") {
		t.Fatalf("expected unknown capability error, got: %s", rendered)
	}
	if !strings.Contains(rendered, "AVAILABLE CAPABILITIES") {
		t.Fatalf("expected available capabilities list, got: %s", rendered)
	}
}

func TestExplainMemoryCapabilityIncludesTuningVars(t *testing.T) {
	out := &bytes.Buffer{}
	errOut := &bytes.Buffer{}
	rootCmd.SetOut(out)
	rootCmd.SetErr(errOut)
	rootCmd.SetArgs([]string{"explain", "memory"})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("expected explain memory to succeed, got error: %v", err)
	}
	rendered := out.String()
	required := []string{
		"CAPABILITY",
		"memory",
		"TALOS_MEMORY_RETRIEVAL_MODE",
		"... (",
	}
	for _, token := range required {
		if !strings.Contains(rendered, token) {
			t.Fatalf("expected explain memory output to contain %q, got: %s", token, rendered)
		}
	}
}

func TestExplainReflectionCapabilityIncludesTuningPlaybook(t *testing.T) {
	out := &bytes.Buffer{}
	errOut := &bytes.Buffer{}
	rootCmd.SetOut(out)
	rootCmd.SetErr(errOut)
	rootCmd.SetArgs([]string{"explain", "reflection"})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("expected explain reflection to succeed, got error: %v", err)
	}
	rendered := out.String()
	required := []string{
		"CAPABILITY",
		"reflection",
		"TALOS_REFLECTION_V2_ENABLED",
		"... (",
	}
	for _, token := range required {
		if !strings.Contains(strings.ToLower(rendered), strings.ToLower(token)) {
			t.Fatalf("expected explain reflection output to contain %q, got: %s", token, rendered)
		}
	}
}

func TestExplainMCTSCapabilityIncludesTuningVars(t *testing.T) {
	out := &bytes.Buffer{}
	errOut := &bytes.Buffer{}
	rootCmd.SetOut(out)
	rootCmd.SetErr(errOut)
	rootCmd.SetArgs([]string{"explain", "mcts"})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("expected explain mcts to succeed, got error: %v", err)
	}
	rendered := out.String()
	required := []string{
		"CAPABILITY",
		"mcts",
		"TALOS_MCTS_STRATEGY",
		"TALOS_MCTS_MAX_CONCURRENCY",
		"TALOS_MCTS_WIDENING_ALPHA",
		"... (",
	}
	for _, token := range required {
		if !strings.Contains(strings.ToLower(rendered), strings.ToLower(token)) {
			t.Fatalf("expected explain mcts output to contain %q, got: %s", token, rendered)
		}
	}
}

func TestExplainLearnCapabilityIsCompacted(t *testing.T) {
	out := &bytes.Buffer{}
	errOut := &bytes.Buffer{}
	rootCmd.SetOut(out)
	rootCmd.SetErr(errOut)
	rootCmd.SetArgs([]string{"explain", "learn"})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("expected explain learn to succeed, got error: %v", err)
	}
	rendered := out.String()
	if !strings.Contains(rendered, "... (") {
		t.Fatalf("expected compact overflow indicator, got: %s", rendered)
	}
	if strings.Contains(rendered, "talos learn --file <path>") {
		t.Fatalf("expected learn usage list to be capped, got: %s", rendered)
	}
}

func TestExplainChainingCapability(t *testing.T) {
	out := &bytes.Buffer{}
	errOut := &bytes.Buffer{}
	rootCmd.SetOut(out)
	rootCmd.SetErr(errOut)
	rootCmd.SetArgs([]string{"explain", "chaining"})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("expected explain chaining to succeed, got error: %v", err)
	}
	rendered := strings.ToLower(out.String())
	required := []string{
		"capability",
		"chaining",
		"--chain",
		"talos pipeline",
		"research",
		"learn",
		"kaggle",
		"github",
	}
	for _, token := range required {
		if !strings.Contains(rendered, token) {
			t.Fatalf("expected explain chaining output to contain %q, got: %s", token, rendered)
		}
	}
}
