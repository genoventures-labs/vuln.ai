package state

import (
	"path/filepath"
	"testing"
)

func TestActiveNamespacePersistsAcrossManagerReload(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "session_state.json")
	m, err := NewManagerWithPath(statePath)
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}
	m.SetActiveNamespace("Talos-Runtime")
	if err := m.Save(); err != nil {
		t.Fatalf("save state: %v", err)
	}

	reloaded, err := NewManagerWithPath(statePath)
	if err != nil {
		t.Fatalf("reload manager: %v", err)
	}
	if got := reloaded.ActiveNamespace(); got != "talos-runtime" {
		t.Fatalf("expected persisted active namespace talos-runtime, got %q", got)
	}
}
