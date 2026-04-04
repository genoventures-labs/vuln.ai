package state

import (
	"path/filepath"
	"testing"
)

func TestNamespaceCapabilityStorePersistsAllowlists(t *testing.T) {
	path := filepath.Join(t.TempDir(), "namespace_capabilities.json")
	store, err := NewNamespaceCapabilityStoreWithPath(path)
	if err != nil {
		t.Fatalf("new store: %v", err)
	}

	store.AllowSkill("talos-runtime", "skill_report")
	store.AllowTool("talos-runtime", "web_search")
	if err := store.Save(); err != nil {
		t.Fatalf("save store: %v", err)
	}

	reloaded, err := NewNamespaceCapabilityStoreWithPath(path)
	if err != nil {
		t.Fatalf("reload store: %v", err)
	}
	if !reloaded.IsSkillAllowed("talos-runtime", "skill_report") {
		t.Fatal("expected persisted skill allowlist")
	}
	if !reloaded.IsToolAllowed("talos-runtime", "web_search") {
		t.Fatal("expected persisted tool allowlist")
	}
}

func TestNamespaceCapabilityStoreDefaultsToDeny(t *testing.T) {
	store, err := NewNamespaceCapabilityStoreWithPath(filepath.Join(t.TempDir(), "namespace_capabilities.json"))
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	if store.IsSkillAllowed("talos-runtime", "unknown_skill") {
		t.Fatal("expected unknown skill to be denied by default")
	}
	if store.IsToolAllowed("talos-runtime", "web_search") {
		t.Fatal("expected unknown tool to be denied by default")
	}
}
