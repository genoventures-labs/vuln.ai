package taloscli

import (
	"path/filepath"
	"testing"

	"github.com/Thynaptic/P-LMv1/pkg/skills"
	"github.com/Thynaptic/P-LMv1/pkg/state"
)

func TestFilterNamespaceAllowedSkills(t *testing.T) {
	prevStoreFactory := namespaceCapabilityStoreFactory
	prevRequestedNamespace := requestedNamespace
	defer func() {
		namespaceCapabilityStoreFactory = prevStoreFactory
		requestedNamespace = prevRequestedNamespace
	}()

	storePath := filepath.Join(t.TempDir(), "namespace_capabilities.json")
	store, err := state.NewNamespaceCapabilityStoreWithPath(storePath)
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	store.AllowSkill("talos-runtime", "skill_allowed")
	if err := store.Save(); err != nil {
		t.Fatalf("save store: %v", err)
	}
	namespaceCapabilityStoreFactory = func() (*state.NamespaceCapabilityStore, error) {
		return state.NewNamespaceCapabilityStoreWithPath(storePath)
	}
	requestedNamespace = "talos-runtime"

	in := []skills.SkillRecord{
		{SkillID: "skill_allowed"},
		{SkillID: "skill_blocked"},
	}
	filtered, err := filterNamespaceAllowedSkills(in)
	if err != nil {
		t.Fatalf("filter skills: %v", err)
	}
	if len(filtered) != 1 || filtered[0].SkillID != "skill_allowed" {
		t.Fatalf("unexpected filtered skills: %#v", filtered)
	}
}
