package taloscli

import (
	"path/filepath"
	"testing"

	"github.com/Thynaptic/P-LMv1/pkg/state"
)

func TestResolveRuntimeNamespacePrecedence(t *testing.T) {
	prevRoot := requestedNamespace
	defer func() { requestedNamespace = prevRoot }()

	sm, err := state.NewManagerWithPath(filepath.Join(t.TempDir(), "session_state.json"))
	if err != nil {
		t.Fatalf("state manager: %v", err)
	}
	sm.SetActiveNamespace("persisted")
	if err := sm.Save(); err != nil {
		t.Fatalf("save state: %v", err)
	}

	requestedNamespace = ""
	if got := resolveRuntimeNamespace(sm, ""); got != "persisted" {
		t.Fatalf("expected persisted namespace fallback, got %q", got)
	}
	if got := resolveRuntimeNamespace(sm, "learn-local"); got != "learn-local" {
		t.Fatalf("expected explicit learn namespace, got %q", got)
	}
	requestedNamespace = "root-global"
	if got := resolveRuntimeNamespace(sm, "learn-local"); got != "root-global" {
		t.Fatalf("expected root namespace precedence, got %q", got)
	}
}
