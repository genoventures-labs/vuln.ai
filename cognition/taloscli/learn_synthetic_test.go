package taloscli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildSyntheticTextJSONLLines(t *testing.T) {
	lines := buildSyntheticTextJSONLLines("alpha beta gamma delta", "inline", 10)
	if len(lines) < 2 {
		t.Fatalf("expected chunking into multiple lines, got %d", len(lines))
	}
	for _, line := range lines {
		for _, token := range []string{`"type":"synthetic_text"`, `"source":"inline"`, `"target_text"`} {
			if !strings.Contains(line, token) {
				t.Fatalf("expected token %q in line: %s", token, line)
			}
		}
	}
}

func TestExecuteLearnSyntheticTextWritesOutput(t *testing.T) {
	prevOut := learnSyntheticTextOut
	prevFile := learnFile
	prevSessions := learnSessionsPath
	learnFile = ""
	learnSessionsPath = filepath.Join(t.TempDir(), "learn_sessions.jsonl")
	learnSyntheticTextOut = filepath.Join(t.TempDir(), "synthetic.jsonl")
	t.Cleanup(func() {
		learnSyntheticTextOut = prevOut
		learnFile = prevFile
		learnSessionsPath = prevSessions
	})
	if err := executeLearnSyntheticText([]string{"synthetic sample text for generator"}); err != nil {
		t.Fatalf("executeLearnSyntheticText returned error: %v", err)
	}
	data, err := os.ReadFile(learnSyntheticTextOut)
	if err != nil {
		t.Fatalf("read synthetic output: %v", err)
	}
	out := string(data)
	if !strings.Contains(out, `"type":"synthetic_text"`) {
		t.Fatalf("expected synthetic artifact records, got: %s", out)
	}
}

func TestAppendSyntheticTextArtifactsAppends(t *testing.T) {
	out := filepath.Join(t.TempDir(), "append.jsonl")
	first, err := appendSyntheticTextArtifacts("alpha beta gamma", "inline", out, 8)
	if err != nil {
		t.Fatalf("first append failed: %v", err)
	}
	second, err := appendSyntheticTextArtifacts("delta epsilon zeta", "inline", out, 8)
	if err != nil {
		t.Fatalf("second append failed: %v", err)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("read output failed: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if want := first + second; len(lines) != want {
		t.Fatalf("expected %d lines after append, got %d", want, len(lines))
	}
}
