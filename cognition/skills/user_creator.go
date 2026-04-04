package skills

import (
	"context"
	"fmt"
	"strings"

	"github.com/Thynaptic/P-LMv1/pkg/tools"
)

const defaultPermanentSkillsRoot = ".skills/permanent"

func PermanentSkillsRoot() string {
	return defaultPermanentSkillsRoot
}

// UserSkillCreateRequest captures user-directed TALOS self-skill creation input.
type UserSkillCreateRequest struct {
	Name             string
	Intent           string
	Description      string
	ReasoningTier    string
	TaskType         string
	Namespace        string
	RequestedTools   []tools.SkillPreflightToolRequest
	RequestedDomains []string
}

// UserSkillCreateResult contains creation output and preflight details.
type UserSkillCreateResult struct {
	Artifact  JITSkillArtifact
	Preflight tools.SkillPreflightResponse
}

// UserSkillCreator creates user-defined TALOS self-skills with required preflight gating.
type UserSkillCreator struct {
	Generator        *JITGenerator
	ToolClient       *tools.GLMToolClient
	RequirePreflight bool
}

func NewUserSkillCreator(tc *tools.GLMToolClient) *UserSkillCreator {
	return &UserSkillCreator{
		Generator:        NewJITGenerator(defaultPermanentSkillsRoot),
		ToolClient:       tc,
		RequirePreflight: true,
	}
}

func (c *UserSkillCreator) Create(ctx context.Context, req UserSkillCreateRequest) (UserSkillCreateResult, error) {
	name := strings.TrimSpace(req.Name)
	intent := strings.TrimSpace(req.Intent)
	if name == "" {
		return UserSkillCreateResult{}, fmt.Errorf("name is required")
	}
	if intent == "" {
		return UserSkillCreateResult{}, fmt.Errorf("intent is required")
	}

	if c == nil {
		return UserSkillCreateResult{}, fmt.Errorf("user skill creator is nil")
	}
	if c.Generator == nil {
		c.Generator = NewJITGenerator(defaultPermanentSkillsRoot)
	}
	if strings.TrimSpace(c.Generator.Root) == "" {
		c.Generator.Root = defaultPermanentSkillsRoot
	}

	preflight, err := EnsurePreflightAllowed(ctx, c.ToolClient, tools.SkillPreflightRequest{
		SkillName:        name,
		Intent:           intent,
		RequestedTools:   req.RequestedTools,
		RequestedDomains: req.RequestedDomains,
	}, c.RequirePreflight)
	if err != nil {
		return UserSkillCreateResult{}, err
	}

	artifact, err := c.Generator.Generate(JITSkillRequest{
		Name:          name,
		Description:   strings.TrimSpace(req.Description),
		Requirement:   intent,
		ReasoningTier: strings.TrimSpace(req.ReasoningTier),
		TaskType:      strings.TrimSpace(req.TaskType),
	})
	if err != nil {
		return UserSkillCreateResult{}, err
	}
	reg := NewSkillRegistry(c.Generator.Root)
	if err := reg.Upsert(SkillRecordFromArtifact(req, artifact, true)); err != nil {
		return UserSkillCreateResult{}, err
	}
	return UserSkillCreateResult{
		Artifact:  artifact,
		Preflight: preflight,
	}, nil
}
