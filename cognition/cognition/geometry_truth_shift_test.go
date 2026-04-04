package cognition

import (
	"testing"

	"github.com/Thynaptic/P-LMv1/pkg/state"
)

func neutralSessionState() state.SessionState {
	return state.SessionState{
		Confidence:      0.5,
		Urgency:         0.5,
		AnalyticalMode:  0.5,
		Frustration:     0.5,
		GoalPersistence: 0.5,
	}
}

func TestDetermineTopologyWithTruthShiftSevereForcesOuroboros(t *testing.T) {
	top := DetermineTopologyWithTruthShift("quick status summary", neutralSessionState(), true, 0.86, 0.20)
	if top != TopologyOuroboros {
		t.Fatalf("expected ouroboros, got %s", top)
	}
}

func TestDetermineTopologyWithTruthShiftModerateUpgradesSpikeToBloom(t *testing.T) {
	top := DetermineTopologyWithTruthShift("quick status summary", neutralSessionState(), true, 0.56, 0.30)
	if top != TopologyBloom {
		t.Fatalf("expected bloom, got %s", top)
	}
}

func TestDetermineTopologyWithTruthShiftLowKeepsBase(t *testing.T) {
	s := neutralSessionState()
	base := DetermineTopologyWithState("compare options and risks", s)
	top := DetermineTopologyWithTruthShift("compare options and risks", s, false, 0.12, 0.10)
	if top != base {
		t.Fatalf("expected base topology %s, got %s", base, top)
	}
}

func TestDetermineTopologyWithTruthShiftConflictIndexEscalates(t *testing.T) {
	top := DetermineTopologyWithTruthShift("quick status summary", neutralSessionState(), false, 0.0, 0.80)
	if top != TopologyOuroboros {
		t.Fatalf("expected ouroboros from conflict index escalation, got %s", top)
	}
}
