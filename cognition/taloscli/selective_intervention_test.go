package taloscli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSelectiveInterventionHighRiskRequiresApproval(t *testing.T) {
	t.Setenv(selectiveInterventionEnabledEnv, "true")
	t.Setenv(selectiveInterventionAutoEnv, "false")
	t.Setenv(selectiveApprovedIDsEnv, "")

	tmp := t.TempDir()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	defer func() { _ = os.Chdir(wd) }()
	if err := os.Chdir(tmp); err != nil {
		t.Fatalf("chdir: %v", err)
	}

	call := toolInvocation{
		Tool: "sys_exec",
		Args: map[string]interface{}{"command": "rm -rf /tmp/demo"},
	}
	err = maybeRequireSelectiveIntervention(call)
	if err == nil {
		t.Fatal("expected selective intervention error")
	}
	if !isSelectiveInterventionRequiredError(err) {
		t.Fatalf("expected selective intervention marker, got: %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(tmp, selectiveInterventionPath)); statErr != nil {
		t.Fatalf("expected intervention store file, got: %v", statErr)
	}
}

func TestSelectiveInterventionApproveFlow(t *testing.T) {
	t.Setenv(selectiveInterventionEnabledEnv, "true")
	t.Setenv(selectiveInterventionAutoEnv, "false")
	t.Setenv(selectiveApprovedIDsEnv, "")

	tmp := t.TempDir()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	defer func() { _ = os.Chdir(wd) }()
	if err := os.Chdir(tmp); err != nil {
		t.Fatalf("chdir: %v", err)
	}

	call := toolInvocation{
		Tool: "execute_code",
		Args: map[string]interface{}{"language": "bash", "code": "echo hi"},
	}
	firstErr := maybeRequireSelectiveIntervention(call)
	if firstErr == nil {
		t.Fatal("expected initial intervention error")
	}
	msg := selectiveInterventionErrorMessage(firstErr)
	idx := strings.Index(msg, "si-")
	if idx < 0 || len(msg[idx:]) < 15 {
		t.Fatalf("expected ticket id in message, got: %s", msg)
	}
	ticket := msg[idx : idx+15]

	approvalMsg, handled := TryHandleSelectiveInterventionApproval("approve " + ticket)
	if !handled {
		t.Fatal("expected approval command to be handled")
	}
	if !strings.Contains(strings.ToLower(approvalMsg), "approved") {
		t.Fatalf("unexpected approval response: %s", approvalMsg)
	}

	if err := maybeRequireSelectiveIntervention(call); err != nil {
		t.Fatalf("expected approved call to pass, got: %v", err)
	}
}

func TestSelectiveInterventionLowRiskBypass(t *testing.T) {
	t.Setenv(selectiveInterventionEnabledEnv, "true")
	t.Setenv(selectiveInterventionAutoEnv, "false")

	call := toolInvocation{
		Tool: "web_search",
		Args: map[string]interface{}{"query": "golang channels"},
	}
	if err := maybeRequireSelectiveIntervention(call); err != nil {
		t.Fatalf("expected low-risk call to bypass intervention, got: %v", err)
	}
}

func TestSelectiveInterventionHITLApproveImmediate(t *testing.T) {
	t.Setenv(selectiveInterventionEnabledEnv, "true")
	t.Setenv(selectiveInterventionAutoEnv, "false")
	t.Setenv(selectiveApprovedIDsEnv, "")
	t.Setenv(hitlTriggersEnabledEnv, "true")
	t.Setenv(hitlInteractiveEnv, "true")

	tmp := t.TempDir()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	defer func() { _ = os.Chdir(wd) }()
	if err := os.Chdir(tmp); err != nil {
		t.Fatalf("chdir: %v", err)
	}

	oldIn := hitlInputReader
	oldOut := hitlOutput
	oldTTY := hitlIsTTYFn
	defer func() {
		hitlInputReader = oldIn
		hitlOutput = oldOut
		hitlIsTTYFn = oldTTY
	}()
	hitlInputReader = strings.NewReader("approve\n")
	hitlOutput = &bytes.Buffer{}
	hitlIsTTYFn = func() bool { return true }

	call := toolInvocation{
		Tool: "sys_exec",
		Args: map[string]interface{}{"command": "rm -rf /tmp/demo"},
	}
	if err := maybeRequireSelectiveIntervention(call); err != nil {
		t.Fatalf("expected HITL approval to pass without deferred error, got: %v", err)
	}
}
