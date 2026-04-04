package taloscli

import (
	"bytes"
	"strings"
	"testing"
)

func TestNextCommandShowsFullHelpWithDeprecationNotice(t *testing.T) {
	out := &bytes.Buffer{}
	errOut := &bytes.Buffer{}
	rootCmd.SetOut(out)
	rootCmd.SetErr(errOut)

	rootCmd.SetArgs([]string{"next"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("next execute failed: %v", err)
	}
	rendered := out.String()
	if !strings.Contains(rendered, "Help pagination retired") {
		t.Fatalf("expected pagination retirement notice, got: %s", rendered)
	}
	if !strings.Contains(rendered, "TALOS CLI HELP") {
		t.Fatalf("expected help header, got: %s", rendered)
	}
	if !strings.Contains(rendered, "ALL COMMANDS") {
		t.Fatalf("expected full help sections, got: %s", rendered)
	}
}
