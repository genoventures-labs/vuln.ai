package taloscli

import (
	"fmt"
	"strings"
	"testing"

	"github.com/Thynaptic/P-LMv1/pkg/skills"
)

func TestParsePipelineChainQuoted(t *testing.T) {
	steps, err := parsePipelineChain(`research run "k8s release" ; learn --from-research latest`)
	if err != nil {
		t.Fatalf("parse chain: %v", err)
	}
	if len(steps) != 2 {
		t.Fatalf("expected 2 steps, got %d", len(steps))
	}
	if got := strings.Join(steps[0].Args, "|"); got != "research|run|k8s release" {
		t.Fatalf("unexpected step[0] args: %s", got)
	}
	if got := strings.Join(steps[1].Args, "|"); got != "learn|--from-research|latest" {
		t.Fatalf("unexpected step[1] args: %s", got)
	}
}

func TestParsePipelineChainWithSkillFlag(t *testing.T) {
	steps, err := parsePipelineChain(`research run "k8s release" --skill analyst ; learn --from-research latest --skill=memory`)
	if err != nil {
		t.Fatalf("parse chain: %v", err)
	}
	if len(steps) != 2 {
		t.Fatalf("expected 2 steps, got %d", len(steps))
	}
	if steps[0].SkillRef != "analyst" {
		t.Fatalf("expected step[0] skill analyst, got %q", steps[0].SkillRef)
	}
	if steps[1].SkillRef != "memory" {
		t.Fatalf("expected step[1] skill memory, got %q", steps[1].SkillRef)
	}
	if got := strings.Join(steps[0].Args, "|"); got != "research|run|k8s release" {
		t.Fatalf("unexpected step[0] args after skill strip: %s", got)
	}
	if got := strings.Join(steps[1].Args, "|"); got != "learn|--from-research|latest" {
		t.Fatalf("unexpected step[1] args after skill strip: %s", got)
	}
}

func TestParsePipelineChainSkillFlagMissingValue(t *testing.T) {
	_, err := parsePipelineChain(`research run x --skill ; learn --from-research latest`)
	if err == nil {
		t.Fatal("expected missing skill value parse error")
	}
}

func TestValidatePipelineAllowlist(t *testing.T) {
	origResolver := pipelineSkillResolver
	pipelineSkillResolver = func(query string) (*skills.SkillRecord, error) { return nil, nil }
	defer func() { pipelineSkillResolver = origResolver }()

	ok := []pipelineStep{
		{Raw: "research run x", Args: []string{"research", "run", "x"}},
		{Raw: "learn --from-research latest", Args: []string{"learn", "--from-research", "latest"}},
	}
	if _, err := validatePipelineSteps(ok); err != nil {
		t.Fatalf("expected allowlisted chain, got %v", err)
	}

	bad := []pipelineStep{
		{Raw: "doctor", Args: []string{"doctor"}},
		{Raw: "learn --from-research latest", Args: []string{"learn", "--from-research", "latest"}},
	}
	if _, err := validatePipelineSteps(bad); err == nil {
		t.Fatal("expected invalid chain error")
	}
}

func TestValidatePipelineSkillResolutionFailFast(t *testing.T) {
	origResolver := pipelineSkillResolver
	pipelineSkillResolver = func(query string) (*skills.SkillRecord, error) {
		return nil, fmt.Errorf("not found")
	}
	defer func() { pipelineSkillResolver = origResolver }()

	steps := []pipelineStep{
		{Raw: "research run x --skill missing", Args: []string{"research", "run", "x"}, SkillRef: "missing"},
		{Raw: "learn --from-research latest", Args: []string{"learn", "--from-research", "latest"}},
	}
	if _, err := validatePipelineSteps(steps); err == nil {
		t.Fatal("expected skill resolution failure")
	}
}

func TestSplitPipelineSegmentsUnterminatedQuote(t *testing.T) {
	_, err := splitPipelineSegments(`research run "bad ; learn --from-research latest`)
	if err == nil {
		t.Fatal("expected quote parse error")
	}
}
