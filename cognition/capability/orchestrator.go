package capability

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/Thynaptic/P-LMv1/pkg/skills"
	"github.com/Thynaptic/P-LMv1/pkg/tools"
)

type CapabilityKind string

const (
	CapabilityKindTool  CapabilityKind = "tool"
	CapabilityKindSkill CapabilityKind = "skill"
)

type DecisionSource string

const (
	DecisionSourcePolicy DecisionSource = "policy"
	DecisionSourceLLM    DecisionSource = "llm"
)

// IntentSignal is the normalized input for capability routing.
type IntentSignal struct {
	Query          string
	GapDescription string
	TaskType       string
	ReasoningTier  string
}

// Decision captures tool-vs-skill routing output.
type Decision struct {
	Kind       CapabilityKind
	Confidence float64
	Source     DecisionSource
	Reasons    []string
}

// PluginOutcome summarizes tool generation/install state.
type PluginOutcome struct {
	PluginID        string
	Endpoint        string
	AutoEnabled     bool
	PendingApproval bool
	Status          string
	Publisher       string
}

// Outcome captures either tool or skill realization output.
type Outcome struct {
	Decision Decision
	Plugin   *PluginOutcome
	Skill    *skills.JITSkillArtifact
}

type LLMClassifier interface {
	Classify(ctx context.Context, signal IntentSignal) (CapabilityKind, float64, string, error)
}

type Orchestrator struct {
	Admin          *tools.GLMAdminClient
	ToolClient     *tools.GLMToolClient
	SkillGenerator *skills.JITGenerator
	Classifier     LLMClassifier
}

func NewOrchestrator(admin *tools.GLMAdminClient, generator *skills.JITGenerator, classifier LLMClassifier) *Orchestrator {
	if generator == nil {
		generator = skills.NewJITGenerator(skills.PermanentSkillsRoot())
	}
	return &Orchestrator{
		Admin:          admin,
		SkillGenerator: generator,
		Classifier:     classifier,
	}
}

// Decide routes missing capability between environment tool and self skill.
func (o *Orchestrator) Decide(ctx context.Context, signal IntentSignal) Decision {
	kind, conf, reasons := policyRoute(signal)
	if conf >= 0.70 {
		return Decision{Kind: kind, Confidence: conf, Source: DecisionSourcePolicy, Reasons: reasons}
	}
	if o != nil && o.Classifier != nil {
		lk, lc, why, err := o.Classifier.Classify(ctx, signal)
		if err == nil && lk != "" {
			reasons = append(reasons, why)
			return Decision{Kind: lk, Confidence: lc, Source: DecisionSourceLLM, Reasons: reasons}
		}
	}
	return Decision{Kind: kind, Confidence: conf, Source: DecisionSourcePolicy, Reasons: reasons}
}

// HandleGap executes the selected path end-to-end.
func (o *Orchestrator) HandleGap(ctx context.Context, signal IntentSignal) (Outcome, error) {
	decision := o.Decide(ctx, signal)
	out := Outcome{Decision: decision}
	switch decision.Kind {
	case CapabilityKindSkill:
		skillArtifact, err := o.handleSkill(ctx, signal)
		if err != nil {
			return out, err
		}
		out.Skill = &skillArtifact
		return out, nil
	default:
		plugin, err := o.handleTool(ctx, signal)
		if err != nil {
			// Safe fallback: when tool path fails, still provide self path.
			skillArtifact, sErr := o.handleSkill(ctx, signal)
			if sErr != nil {
				return out, fmt.Errorf("tool path failed: %w; skill fallback failed: %v", err, sErr)
			}
			out.Decision.Reasons = append(out.Decision.Reasons, "tool path failed; used skill fallback")
			out.Skill = &skillArtifact
			out.Decision.Kind = CapabilityKindSkill
			return out, nil
		}
		out.Plugin = plugin
		return out, nil
	}
}

func (o *Orchestrator) handleSkill(ctx context.Context, signal IntentSignal) (skills.JITSkillArtifact, error) {
	if o == nil || o.SkillGenerator == nil {
		return skills.JITSkillArtifact{}, fmt.Errorf("skill generator unavailable")
	}
	if strings.TrimSpace(o.SkillGenerator.Root) == "" {
		o.SkillGenerator.Root = skills.PermanentSkillsRoot()
	}
	name := deriveSkillName(signal)
	intent := strings.TrimSpace(signal.Query)
	reg := skills.NewSkillRegistry(o.SkillGenerator.Root)
	match, ok, err := reg.FindMatch(name, intent, strings.TrimSpace(signal.TaskType))
	if err != nil {
		return skills.JITSkillArtifact{}, err
	}
	if ok && match != nil {
		return skills.ArtifactFromSkillRecord(*match), nil
	}
	tc, err := o.skillToolClient()
	if err != nil {
		return skills.JITSkillArtifact{}, err
	}
	if _, err := skills.EnsurePreflightAllowed(ctx, tc, tools.SkillPreflightRequest{
		SkillName: name,
		Intent:    intent,
	}, true); err != nil {
		return skills.JITSkillArtifact{}, err
	}
	artifact, err := o.SkillGenerator.Generate(skills.JITSkillRequest{
		Name:          name,
		Description:   "Self capability generated from TALOS intent routing.",
		Requirement:   intent,
		ReasoningTier: strings.TrimSpace(signal.ReasoningTier),
		TaskType:      strings.TrimSpace(signal.TaskType),
	})
	if err != nil {
		return skills.JITSkillArtifact{}, err
	}
	if err := reg.Upsert(skills.SkillRecord{
		SkillID:        strings.TrimSpace(artifact.SkillID),
		RevisionID:     strings.TrimSpace(artifact.RevisionID),
		Version:        firstNonEmpty(strings.TrimSpace(artifact.Version), "0.1.0"),
		Status:         skills.SkillStatusDraft,
		Name:           name,
		Intent:         intent,
		Description:    "Self capability generated from TALOS intent routing.",
		ReasoningTier:  strings.TrimSpace(signal.ReasoningTier),
		TaskType:       strings.TrimSpace(signal.TaskType),
		RootDir:        strings.TrimSpace(artifact.RootDir),
		SourcePath:     strings.TrimSpace(artifact.SourcePath),
		ManifestPath:   strings.TrimSpace(artifact.ManifestPath),
		PackageName:    strings.TrimSpace(artifact.PackageName),
		CompileOK:      artifact.CompileOK,
		Enabled:        true,
		Active:         false,
		Provenance:     "capability_orchestrator",
		SandboxProfile: "skill_default",
	}); err != nil {
		return skills.JITSkillArtifact{}, err
	}
	return artifact, nil
}

func (o *Orchestrator) handleTool(ctx context.Context, signal IntentSignal) (*PluginOutcome, error) {
	if o == nil || o.Admin == nil {
		return nil, fmt.Errorf("admin toolserver client unavailable")
	}
	req := tools.AdminToolgenRequest{
		Name:        derivePluginName(signal),
		Description: "Generated plugin for environment capability gap.",
		Publisher:   "talos",
		Goal:        strings.TrimSpace(signal.GapDescription),
		Requirement: strings.TrimSpace(signal.Query),
		Metadata: map[string]interface{}{
			"task_type":      strings.TrimSpace(signal.TaskType),
			"reasoning_tier": strings.TrimSpace(signal.ReasoningTier),
		},
	}
	gen, err := o.Admin.GenerateToolPlugin(req)
	if err != nil {
		return nil, err
	}

	jobID := strings.TrimSpace(gen.JobID)
	job := gen
	if jobID != "" {
		deadline := time.Now().Add(90 * time.Second)
		for {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			state := strings.ToLower(strings.TrimSpace(firstNonEmpty(job.Status, job.State)))
			if state == "ready" || state == "completed" || state == "succeeded" || state == "enabled" {
				break
			}
			if state == "failed" || state == "error" {
				return nil, fmt.Errorf("plugin generation failed: %s", firstNonEmpty(job.Status, job.State))
			}
			if time.Now().After(deadline) {
				return nil, fmt.Errorf("plugin generation timed out waiting for job %s", jobID)
			}
			time.Sleep(2 * time.Second)
			next, err := o.Admin.GetToolgenJob(jobID)
			if err != nil {
				return nil, err
			}
			job = next
		}
	}

	pluginName := strings.TrimSpace(firstNonEmpty(job.Name, gen.Name))
	pluginID := strings.TrimSpace(firstNonEmpty(job.PluginID, gen.PluginID, pluginName))
	if pluginName == "" {
		pluginName = pluginID
	}
	if pluginID == "" {
		return nil, fmt.Errorf("plugin generation response missing plugin identifier")
	}

	pluginMeta := tools.TrustedPluginMetadata{
		ID:        pluginID,
		Name:      firstNonEmpty(pluginName, pluginID),
		Publisher: firstNonEmpty(job.Publisher, gen.Publisher),
	}
	autoEnabled := false
	pending := true
	if tools.IsTrustedPlugin(pluginMeta) {
		if err := o.Admin.EnablePlugin(pluginName); err != nil {
			return nil, err
		}
		autoEnabled = true
		pending = false
	}
	endpoint := strings.TrimSpace(firstNonEmpty(job.Endpoint, gen.Endpoint))
	if endpoint == "" && pluginName != "" {
		endpoint = "/tools/plugins/" + pluginName
	}
	status := strings.TrimSpace(firstNonEmpty(job.Status, job.State, gen.Status, "queued"))
	return &PluginOutcome{
		PluginID:        pluginID,
		Endpoint:        endpoint,
		AutoEnabled:     autoEnabled,
		PendingApproval: pending,
		Status:          status,
		Publisher:       strings.TrimSpace(firstNonEmpty(job.Publisher, gen.Publisher)),
	}, nil
}

func policyRoute(signal IntentSignal) (CapabilityKind, float64, []string) {
	text := strings.ToLower(strings.TrimSpace(signal.Query + " " + signal.GapDescription + " " + signal.TaskType))
	toolMarkers := []string{
		"http", "api", "endpoint", "plugin", "deploy", "network", "socket", "filesystem", "database", "service",
	}
	skillMarkers := []string{
		"reason", "summar", "analy", "classif", "plan", "forensic", "debug", "strategy", "self",
	}
	toolScore := 0
	skillScore := 0
	for _, m := range toolMarkers {
		if strings.Contains(text, m) {
			toolScore++
		}
	}
	for _, m := range skillMarkers {
		if strings.Contains(text, m) {
			skillScore++
		}
	}
	if toolScore > skillScore {
		return CapabilityKindTool, 0.75, []string{"environment markers dominate capability signal"}
	}
	if skillScore > toolScore {
		return CapabilityKindSkill, 0.75, []string{"self-reasoning markers dominate capability signal"}
	}
	// Tie-break default: environment tool first.
	return CapabilityKindTool, 0.55, []string{"ambiguous signal; defaulting to environment tool path"}
}

type KeywordLLMClassifier struct{}

func (KeywordLLMClassifier) Classify(_ context.Context, signal IntentSignal) (CapabilityKind, float64, string, error) {
	text := strings.ToLower(strings.TrimSpace(signal.Query + " " + signal.GapDescription + " " + signal.TaskType))
	if strings.Contains(text, "self skill") || strings.Contains(text, "inner skill") {
		return CapabilityKindSkill, 0.72, "llm fallback favored self skill", nil
	}
	if strings.Contains(text, "plugin") || strings.Contains(text, "admin") {
		return CapabilityKindTool, 0.72, "llm fallback favored plugin/tool path", nil
	}
	return CapabilityKindTool, 0.60, "llm fallback inconclusive; leaning tool", nil
}

func deriveSkillName(signal IntentSignal) string {
	base := strings.TrimSpace(signal.GapDescription)
	if base == "" {
		base = strings.TrimSpace(signal.Query)
	}
	if base == "" {
		base = "self_capability"
	}
	base = strings.ToLower(base)
	base = strings.ReplaceAll(base, " ", "_")
	base = strings.ReplaceAll(base, "-", "_")
	base = strings.ReplaceAll(base, "/", "_")
	if len(base) > 42 {
		base = base[:42]
	}
	return "talos_" + strings.Trim(base, "_")
}

func derivePluginName(signal IntentSignal) string {
	base := strings.TrimSpace(signal.GapDescription)
	if base == "" {
		base = strings.TrimSpace(signal.Query)
	}
	if base == "" {
		base = "environment_tool"
	}
	base = strings.ToLower(base)
	base = strings.ReplaceAll(base, " ", "-")
	base = strings.ReplaceAll(base, "_", "-")
	base = strings.ReplaceAll(base, "/", "-")
	if len(base) > 42 {
		base = base[:42]
	}
	return "talos-" + strings.Trim(base, "-")
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func (o *Orchestrator) skillToolClient() (*tools.GLMToolClient, error) {
	if o == nil {
		return nil, fmt.Errorf("orchestrator is nil")
	}
	if o.ToolClient != nil {
		return o.ToolClient, nil
	}
	tc, err := tools.NewGLMToolClient()
	if err != nil {
		return nil, err
	}
	o.ToolClient = tc
	return tc, nil
}
