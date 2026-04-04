package orchestration

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Thynaptic/P-LMv1/pkg/cognition"
	"github.com/Thynaptic/P-LMv1/pkg/memory"
	"github.com/Thynaptic/P-LMv1/pkg/state"
)

// AgentProfile defines sovereign agent configuration for multi-daemon deployment.
type AgentProfile struct {
	Name            string   `json:"name"`
	Tools           []string `json:"tools"`
	PolicyProfile   string   `json:"policy_profile"`
	MemoryNamespace string   `json:"memory_namespace"`
	ReasoningMode   string   `json:"reasoning_mode"`
}

// SpawnedAgent is an isolated runtime context for one profile.
type SpawnedAgent struct {
	Profile       AgentProfile
	Memory        *memory.MemoryManager
	State         *state.Manager
	SkillCompiler *cognition.SkillCompiler
}

// SovereignFramework manages profiles and per-agent isolation.
type SovereignFramework struct {
	profiles map[string]AgentProfile
}

// Profile returns a registered profile by ID.
func (sf *SovereignFramework) Profile(id string) (AgentProfile, bool) {
	if sf == nil {
		sf = NewSovereignFramework()
	}
	p, ok := sf.profiles[strings.TrimSpace(id)]
	return p, ok
}

// NewSovereignFramework creates a framework with default profiles.
func NewSovereignFramework() *SovereignFramework {
	return &SovereignFramework{
		profiles: map[string]AgentProfile{
			"default": {
				Name:            "default",
				Tools:           []string{"web_search", "http_request", "vector_retrieve", "execute_code"},
				PolicyProfile:   "balanced",
				MemoryNamespace: "agent_default",
				ReasoningMode:   "adaptive",
			},
			"ops": {
				Name:            "ops",
				Tools:           []string{"http_request", "vector_retrieve", "execute_code"},
				PolicyProfile:   "strict",
				MemoryNamespace: "agent_ops",
				ReasoningMode:   "architect",
			},
		},
	}
}

// RegisterProfile adds/updates an agent profile.
func (sf *SovereignFramework) RegisterProfile(id string, p AgentProfile) {
	if sf == nil {
		return
	}
	if sf.profiles == nil {
		sf.profiles = make(map[string]AgentProfile)
	}
	sf.profiles[strings.TrimSpace(id)] = p
}

// SpawnAgent initializes a profile-isolated context.
func (sf *SovereignFramework) SpawnAgent(profileID string) (*SpawnedAgent, error) {
	if sf == nil {
		sf = NewSovereignFramework()
	}
	p, ok := sf.profiles[strings.TrimSpace(profileID)]
	if !ok {
		return nil, fmt.Errorf("unknown profile: %s", profileID)
	}
	if strings.TrimSpace(p.MemoryNamespace) == "" {
		return nil, fmt.Errorf("profile %s has empty MemoryNamespace", profileID)
	}

	mm, err := memory.NewMemoryManager()
	if err != nil {
		return nil, err
	}
	mm.SetActiveNamespace(p.MemoryNamespace)

	sm, err := state.NewManager()
	if err != nil {
		// Non-fatal for spawn; create nil state context.
		sm = nil
	}

	return &SpawnedAgent{
		Profile:       p,
		Memory:        mm,
		State:         sm,
		SkillCompiler: cognition.NewSkillCompiler(),
	}, nil
}

// DiscoverSkills returns skills visible to the agent under policy bounds.
func (sa *SpawnedAgent) DiscoverSkills() ([]cognition.SkillPrimitive, error) {
	if sa == nil || sa.SkillCompiler == nil {
		return nil, fmt.Errorf("agent has no skill compiler")
	}

	roots := []string{sa.SkillCompiler.TempDir, sa.SkillCompiler.PermanentDir}
	var out []cognition.SkillPrimitive
	for _, root := range roots {
		entries, err := os.ReadDir(root)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, err
		}
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			metaPath := filepath.Join(root, e.Name(), "metadata.json")
			b, err := os.ReadFile(metaPath)
			if err != nil {
				continue
			}
			var skill cognition.SkillPrimitive
			if err := json.Unmarshal(b, &skill); err != nil {
				continue
			}
			if sa.allowsSkill(skill) {
				out = append(out, skill)
			}
		}
	}
	return out, nil
}

func (sa *SpawnedAgent) allowsSkill(skill cognition.SkillPrimitive) bool {
	policy := strings.ToLower(strings.TrimSpace(sa.Profile.PolicyProfile))
	switch policy {
	case "strict":
		// Strict profile only allows local parsing/helper primitives.
		desc := strings.ToLower(skill.Description + " " + skill.Name)
		if strings.Contains(desc, "network") || strings.Contains(desc, "remote") {
			return false
		}
		if skill.Language != "bash" && skill.Language != "go" {
			return false
		}
		return true
	default:
		return true
	}
}
