package taloscli

import (
	"bytes"
	"strings"
	"testing"
)

func TestAboutOutput(t *testing.T) {
	out := &bytes.Buffer{}
	errOut := &bytes.Buffer{}

	rootCmd.SetOut(out)
	rootCmd.SetErr(errOut)
	rootCmd.SetArgs([]string{"about"})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("expected about command to succeed, got error: %v", err)
	}

	rendered := out.String()
	if rendered == "" {
		t.Fatal("expected about output to be non-empty")
	}

	requiredSections := []string{
		"TALOS ABOUT",
		"IDENTITY",
		"MISSION",
		"ARCHITECTURE",
		"AUTHORITATIVE DOCUMENTS",
		"SIGNATURE",
	}
	for _, section := range requiredSections {
		if !strings.Contains(rendered, section) {
			t.Fatalf("expected about output to contain section %q", section)
		}
	}
}
