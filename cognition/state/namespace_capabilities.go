package state

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

const defaultNamespaceCapabilitiesFile = ".memory/namespace_capabilities.json"

type NamespaceCapabilities struct {
	Skills []string `json:"skills,omitempty"`
	Tools  []string `json:"tools,omitempty"`
}

type NamespaceCapabilityStore struct {
	mu         sync.RWMutex
	filePath   string
	namespaces map[string]NamespaceCapabilities
}

func NewNamespaceCapabilityStore() (*NamespaceCapabilityStore, error) {
	return NewNamespaceCapabilityStoreWithPath(defaultNamespaceCapabilitiesFile)
}

func NewNamespaceCapabilityStoreWithPath(path string) (*NamespaceCapabilityStore, error) {
	s := &NamespaceCapabilityStore{
		filePath:   strings.TrimSpace(path),
		namespaces: map[string]NamespaceCapabilities{},
	}
	if err := s.Load(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *NamespaceCapabilityStore) Load() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if strings.TrimSpace(s.filePath) == "" {
		return fmt.Errorf("namespace capability store path is required")
	}
	raw, err := os.ReadFile(s.filePath)
	if err != nil {
		if os.IsNotExist(err) {
			s.namespaces = map[string]NamespaceCapabilities{}
			return nil
		}
		return err
	}
	var payload struct {
		Namespaces map[string]NamespaceCapabilities `json:"namespaces"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return err
	}
	out := make(map[string]NamespaceCapabilities, len(payload.Namespaces))
	for rawNS, caps := range payload.Namespaces {
		ns := normalizeNamespaceKey(rawNS)
		if ns == "" {
			continue
		}
		out[ns] = NamespaceCapabilities{
			Skills: normalizeStringSet(caps.Skills),
			Tools:  normalizeStringSet(caps.Tools),
		}
	}
	s.namespaces = out
	return nil
}

func (s *NamespaceCapabilityStore) Save() error {
	s.mu.RLock()
	payload := struct {
		Namespaces map[string]NamespaceCapabilities `json:"namespaces"`
	}{
		Namespaces: make(map[string]NamespaceCapabilities, len(s.namespaces)),
	}
	for ns, caps := range s.namespaces {
		payload.Namespaces[ns] = NamespaceCapabilities{
			Skills: append([]string(nil), caps.Skills...),
			Tools:  append([]string(nil), caps.Tools...),
		}
	}
	path := s.filePath
	s.mu.RUnlock()

	if strings.TrimSpace(path) == "" {
		return fmt.Errorf("namespace capability store path is required")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil && filepath.Dir(path) != "." {
		return err
	}
	b, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o644)
}

func (s *NamespaceCapabilityStore) Capabilities(namespace string) NamespaceCapabilities {
	s.mu.RLock()
	defer s.mu.RUnlock()
	ns := normalizeNamespaceKey(namespace)
	if ns == "" {
		return NamespaceCapabilities{}
	}
	caps := s.namespaces[ns]
	return NamespaceCapabilities{
		Skills: append([]string(nil), caps.Skills...),
		Tools:  append([]string(nil), caps.Tools...),
	}
}

func (s *NamespaceCapabilityStore) Namespaces() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]string, 0, len(s.namespaces))
	for ns := range s.namespaces {
		ns = normalizeNamespaceKey(ns)
		if ns == "" {
			continue
		}
		out = append(out, ns)
	}
	sort.Strings(out)
	return out
}

func (s *NamespaceCapabilityStore) AllowSkill(namespace, skillID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ensureNamespace(namespace)
	ns := normalizeNamespaceKey(namespace)
	id := normalizeCapabilityID(skillID)
	if ns == "" || id == "" {
		return
	}
	c := s.namespaces[ns]
	c.Skills = addUnique(c.Skills, id)
	s.namespaces[ns] = c
}

func (s *NamespaceCapabilityStore) DenySkill(namespace, skillID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ns := normalizeNamespaceKey(namespace)
	id := normalizeCapabilityID(skillID)
	if ns == "" || id == "" {
		return
	}
	c := s.namespaces[ns]
	c.Skills = removeValue(c.Skills, id)
	s.namespaces[ns] = c
}

func (s *NamespaceCapabilityStore) AllowTool(namespace, toolID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ensureNamespace(namespace)
	ns := normalizeNamespaceKey(namespace)
	id := normalizeCapabilityID(toolID)
	if ns == "" || id == "" {
		return
	}
	c := s.namespaces[ns]
	c.Tools = addUnique(c.Tools, id)
	s.namespaces[ns] = c
}

func (s *NamespaceCapabilityStore) DenyTool(namespace, toolID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ns := normalizeNamespaceKey(namespace)
	id := normalizeCapabilityID(toolID)
	if ns == "" || id == "" {
		return
	}
	c := s.namespaces[ns]
	c.Tools = removeValue(c.Tools, id)
	s.namespaces[ns] = c
}

func (s *NamespaceCapabilityStore) IsSkillAllowed(namespace, skillID string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	ns := normalizeNamespaceKey(namespace)
	id := normalizeCapabilityID(skillID)
	if ns == "" || id == "" {
		return false
	}
	c := s.namespaces[ns]
	for _, allowed := range c.Skills {
		if strings.EqualFold(strings.TrimSpace(allowed), id) {
			return true
		}
	}
	return false
}

func (s *NamespaceCapabilityStore) IsToolAllowed(namespace, toolID string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	ns := normalizeNamespaceKey(namespace)
	id := normalizeCapabilityID(toolID)
	if ns == "" || id == "" {
		return false
	}
	c := s.namespaces[ns]
	for _, allowed := range c.Tools {
		if strings.EqualFold(strings.TrimSpace(allowed), id) {
			return true
		}
	}
	return false
}

func (s *NamespaceCapabilityStore) ensureNamespace(namespace string) {
	ns := normalizeNamespaceKey(namespace)
	if ns == "" {
		return
	}
	if s.namespaces == nil {
		s.namespaces = map[string]NamespaceCapabilities{}
	}
	if _, ok := s.namespaces[ns]; !ok {
		s.namespaces[ns] = NamespaceCapabilities{}
	}
}

func normalizeNamespaceKey(namespace string) string {
	return strings.ToLower(strings.TrimSpace(namespace))
}

func normalizeCapabilityID(id string) string {
	return strings.ToLower(strings.TrimSpace(id))
}

func addUnique(values []string, v string) []string {
	for _, cur := range values {
		if strings.EqualFold(strings.TrimSpace(cur), v) {
			return values
		}
	}
	values = append(values, v)
	sort.Strings(values)
	return values
}

func removeValue(values []string, v string) []string {
	out := make([]string, 0, len(values))
	for _, cur := range values {
		if strings.EqualFold(strings.TrimSpace(cur), v) {
			continue
		}
		out = append(out, cur)
	}
	sort.Strings(out)
	return out
}

func normalizeStringSet(values []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		v := normalizeCapabilityID(value)
		if v == "" || seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	sort.Strings(out)
	return out
}
