package taloscli

import (
	"bytes"
	"strings"
	"testing"
)

func TestRootHelpTemplate(t *testing.T) {
	out := &bytes.Buffer{}
	errOut := &bytes.Buffer{}

	rootCmd.SetOut(out)
	rootCmd.SetErr(errOut)
	rootCmd.SetArgs([]string{"--help"})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("expected help command to succeed, got error: %v", err)
	}

	rendered := out.String()
	if rendered == "" {
		t.Fatal("expected help output to be non-empty")
	}

	requiredSections := []string{
		"TALOS CLI HELP",
		"USAGE",
		"TOP COMMANDS",
		"ALL COMMANDS",
		"EXAMPLES",
		"DISCOVER MORE",
		"talos find <keyword>",
	}
	for _, section := range requiredSections {
		if !strings.Contains(rendered, section) {
			t.Fatalf("expected help output to contain section %q", section)
		}
	}
}

func TestRootRunEDoesNotTriggerHelpWhenNamespaceOnly(t *testing.T) {
	prevNamespace := requestedNamespace
	defer func() { requestedNamespace = prevNamespace }()

	requestedNamespace = "fresh-workspace"
	out := &bytes.Buffer{}
	rootCmd.SetOut(out)

	if err := rootCmd.RunE(rootCmd, []string{}); err != nil {
		t.Fatalf("expected root RunE with namespace only to succeed, got: %v", err)
	}
	if strings.Contains(out.String(), "TALOS CLI HELP") {
		t.Fatalf("expected no help output when only setting namespace, got: %s", out.String())
	}
}
