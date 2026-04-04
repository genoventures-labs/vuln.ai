package envload

import (
	"os"
	"testing"
)

func TestEnsureRuntimeDefaultsSetsOllamaHost(t *testing.T) {
	t.Setenv("OLLAMA_HOST", "")
	if err := ensureRuntimeDefaults(); err != nil {
		t.Fatalf("ensure defaults: %v", err)
	}
	if got := getenv("OLLAMA_HOST"); got != defaultOllamaHost {
		t.Fatalf("expected default OLLAMA_HOST %q, got %q", defaultOllamaHost, got)
	}
}

func TestEnsureRuntimeDefaultsPreservesExistingHost(t *testing.T) {
	t.Setenv("OLLAMA_HOST", "http://example:11434")
	if err := ensureRuntimeDefaults(); err != nil {
		t.Fatalf("ensure defaults: %v", err)
	}
	if got := getenv("OLLAMA_HOST"); got != "http://example:11434" {
		t.Fatalf("expected existing OLLAMA_HOST to be preserved, got %q", got)
	}
}

func getenv(k string) string {
	return os.Getenv(k)
}
