package cognition

import (
	"strings"
	"testing"

	"github.com/Thynaptic/P-LMv1/pkg/state"
)

func TestBuildStyleProfile_AdjustsForFatigue(t *testing.T) {
	sm, err := state.NewManagerWithPath(t.TempDir() + "/session_state.json")
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}
	sm.UpdateDelta(map[string]float64{"Frustration": 0.35})
	sm.IngestSubtext([]string{"fatigue"}, -0.4)

	p := BuildStyleProfile(sm, nil, "please summarize this architecture", "balanced")
	if p.Tone != "calm_clarifying" {
		t.Fatalf("expected calm_clarifying tone, got %q", p.Tone)
	}
	if p.VerbosityTarget > 2 {
		t.Fatalf("expected concise verbosity target under fatigue, got %d", p.VerbosityTarget)
	}
}

func TestBuildStylePromptContract_ContainsCoreFields(t *testing.T) {
	p := StyleProfile{
		Mode:                 "deep",
		Tone:                 "direct_technical",
		Structure:            "table_first",
		Density:              1.2,
		VerbosityTarget:      4,
		EvidenceBias:         0.9,
		RiskBias:             0.8,
		SourceCitationStrict: true,
	}
	contract := BuildStylePromptContract(p)
	for _, token := range []string{"mode=deep", "tone=direct_technical", "structure=table_first", "source_citation_strict=true"} {
		if !strings.Contains(contract, token) {
			t.Fatalf("expected contract token %q in %q", token, contract)
		}
	}
}
