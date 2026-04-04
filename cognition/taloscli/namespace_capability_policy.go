package taloscli

import (
	"fmt"
	"strings"

	"github.com/Thynaptic/P-LMv1/pkg/state"
)

var namespaceCapabilityStoreFactory = state.NewNamespaceCapabilityStore

func activeRuntimeNamespace() string {
	if ns := strings.ToLower(strings.TrimSpace(requestedNamespace)); ns != "" {
		return ns
	}
	sm, err := state.NewManager()
	if err != nil {
		return ""
	}
	return strings.ToLower(strings.TrimSpace(sm.ActiveNamespace()))
}

func ensureSkillAllowedInNamespace(namespace, skillID string) error {
	ns := strings.ToLower(strings.TrimSpace(namespace))
	if ns == "" {
		return nil
	}
	id := strings.ToLower(strings.TrimSpace(skillID))
	if id == "" {
		return fmt.Errorf("skill unavailable in active namespace %q", ns)
	}
	store, err := namespaceCapabilityStoreFactory()
	if err != nil {
		return fmt.Errorf("load namespace capability policy: %w", err)
	}
	if store.IsSkillAllowed(ns, id) {
		return nil
	}
	return fmt.Errorf("skill %q unavailable in active namespace %q (bind it with `talos namespace bind-skill --id %s`)", id, ns, id)
}

func ensureToolAllowedInNamespace(namespace, toolID string) error {
	ns := strings.ToLower(strings.TrimSpace(namespace))
	if ns == "" {
		return nil
	}
	id := strings.ToLower(strings.TrimSpace(toolID))
	if id == "" {
		return fmt.Errorf("tool unavailable in active namespace %q", ns)
	}
	store, err := namespaceCapabilityStoreFactory()
	if err != nil {
		return fmt.Errorf("load namespace capability policy: %w", err)
	}
	if store.IsToolAllowed(ns, id) {
		return nil
	}
	return fmt.Errorf("tool %q unavailable in active namespace %q (bind it with `talos namespace bind-tool --id %s`)", id, ns, id)
}
