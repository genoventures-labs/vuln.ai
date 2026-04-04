package taloscli

import (
	"bytes"
	"strings"
	"testing"
)

func TestVersionCommandUnifiedOutput(t *testing.T) {
	out := &bytes.Buffer{}
	errOut := &bytes.Buffer{}
	rootCmd.SetOut(out)
	rootCmd.SetErr(errOut)
	rootCmd.SetArgs([]string{"version"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("version command failed: %v", err)
	}
	rendered := out.String()
	for _, token := range []string{"TALOS VERSION", "COMMAND", "STATUS", "RESULTS", "version:", "commit:", "build_date:", "runtime:"} {
		if !strings.Contains(rendered, token) {
			t.Fatalf("expected %q in output, got: %s", token, rendered)
		}
	}
}
