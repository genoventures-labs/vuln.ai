package taloscli

import (
	"bytes"
	"strings"
	"testing"
)

func TestFindCommandsByKeyword(t *testing.T) {
	matches := findCommandsByKeyword(rootCmd, "research")
	if len(matches) == 0 {
		t.Fatal("expected matches for keyword research")
	}
	found := false
	for _, m := range matches {
		if m.Path == "talos research" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected talos research in matches: %#v", matches)
	}
}

func TestFindCommandExecution(t *testing.T) {
	out := &bytes.Buffer{}
	errOut := &bytes.Buffer{}
	rootCmd.SetOut(out)
	rootCmd.SetErr(errOut)
	rootCmd.SetArgs([]string{"find", "doctor"})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("expected find command to succeed, got: %v", err)
	}
	rendered := out.String()
	if !strings.Contains(rendered, "TALOS COMMAND FIND") || !strings.Contains(rendered, "talos doctor") {
		t.Fatalf("unexpected find output: %s", rendered)
	}
}
