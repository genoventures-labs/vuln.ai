package taloscli

import (
	"bytes"
	"strings"
	"testing"
)

func TestPromptHITLApprove(t *testing.T) {
	t.Setenv(hitlTriggersEnabledEnv, "true")
	t.Setenv(hitlInteractiveEnv, "true")
	t.Setenv(hitlTimeoutSecEnv, "30")

	oldIn := hitlInputReader
	oldOut := hitlOutput
	oldTTY := hitlIsTTYFn
	defer func() {
		hitlInputReader = oldIn
		hitlOutput = oldOut
		hitlIsTTYFn = oldTTY
	}()

	out := &bytes.Buffer{}
	hitlInputReader = strings.NewReader("approve\n")
	hitlOutput = out
	hitlIsTTYFn = func() bool { return true }

	d := promptHITL("t-1", "review action", []hitlDecision{hitlApprove, hitlReject})
	if d != hitlApprove {
		t.Fatalf("expected approve decision, got %s", d)
	}
}
