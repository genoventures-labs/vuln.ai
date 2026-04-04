package taloscli

import "testing"

func TestBuildModelCandidatesPrefersLatestForBareNames(t *testing.T) {
	got := buildModelCandidates([]string{"llama3.1", "deepseek-r1", "llama3.2:1b"}, "llama3.1")
	if len(got) < 4 {
		t.Fatalf("expected multiple candidates, got %v", got)
	}
	if got[0] != "llama3.1:latest" {
		t.Fatalf("expected first candidate llama3.1:latest, got %q", got[0])
	}
	if got[1] != "llama3.1" {
		t.Fatalf("expected second candidate llama3.1, got %q", got[1])
	}
}

func TestBuildModelCandidatesSkipsEmbeddingModels(t *testing.T) {
	got := buildModelCandidates([]string{"nomic-embed-text:latest", "llama3.2:latest"}, "nomic-embed-text:latest")
	if len(got) != 1 || got[0] != "llama3.2:latest" {
		t.Fatalf("expected only chat-capable models, got %v", got)
	}
}

func TestIsTransientLLMErrorTreatsModelNotFoundAsRetryable(t *testing.T) {
	err := isTransientLLMError(assertErr("404 Not Found: model 'llama3.1' not found"))
	if !err {
		t.Fatal("expected model-not-found error to be retryable")
	}
}

type staticErr string

func (e staticErr) Error() string { return string(e) }

func assertErr(s string) error { return staticErr(s) }
