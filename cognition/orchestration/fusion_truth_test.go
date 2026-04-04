package orchestration

import (
	"path/filepath"
	"testing"
	"time"
)

func TestDetectWorldviewTruthShiftDetectedOnWinnerFlip(t *testing.T) {
	prev := WorldviewTruthState{
		Version:       1,
		UpdatedAt:     time.Now().UTC().Add(-1 * time.Hour),
		TruthHash:     "aaaa1111",
		ConflictIndex: 0.40,
		ResolutionAnchors: []WorldviewTruthAnchor{
			{Issue: "Parser path", Winner: "Legacy System Reality"},
			{Issue: "Timeout defaults", Winner: "Legacy System Reality"},
		},
	}
	curr := WorldviewTruthState{
		Version:       1,
		UpdatedAt:     time.Now().UTC(),
		TruthHash:     "bbbb2222",
		ConflictIndex: 0.76,
		ResolutionAnchors: []WorldviewTruthAnchor{
			{Issue: "Parser path", Winner: "Desired State Spec"},
			{Issue: "Timeout defaults", Winner: "Desired State Spec"},
		},
	}
	shift := detectWorldviewTruthShift(true, prev, curr)
	if !shift.Detected {
		t.Fatalf("expected shift detected, got %+v", shift)
	}
	if shift.Severity <= 0 {
		t.Fatalf("expected positive severity, got %.2f", shift.Severity)
	}
	if len(shift.ChangedIssues) == 0 {
		t.Fatal("expected changed issues to be populated")
	}
}

func TestDetectWorldviewTruthShiftStableWhenHashUnchanged(t *testing.T) {
	prev := WorldviewTruthState{
		Version:       1,
		UpdatedAt:     time.Now().UTC().Add(-1 * time.Hour),
		TruthHash:     "samehash",
		ConflictIndex: 0.20,
		ResolutionAnchors: []WorldviewTruthAnchor{
			{Issue: "Planner edge", Winner: "Legacy"},
		},
	}
	curr := WorldviewTruthState{
		Version:       1,
		UpdatedAt:     time.Now().UTC(),
		TruthHash:     "samehash",
		ConflictIndex: 0.70,
		ResolutionAnchors: []WorldviewTruthAnchor{
			{Issue: "Planner edge", Winner: "Desired"},
		},
	}
	shift := detectWorldviewTruthShift(true, prev, curr)
	if shift.Detected {
		t.Fatalf("expected stable shift=false, got %+v", shift)
	}
	if shift.Reason != "truth hash unchanged" {
		t.Fatalf("expected hash unchanged reason, got %q", shift.Reason)
	}
}

func TestWorldviewTruthStateRoundTrip(t *testing.T) {
	p := filepath.Join(t.TempDir(), "worldview_truth.json")
	in := WorldviewTruthState{
		Version:        1,
		UpdatedAt:      time.Now().UTC(),
		Query:          "What changed?",
		TruthHash:      "cafefeed",
		ConflictIndex:  0.55,
		FusedWorldview: "Fused Worldview",
		ResolutionAnchors: []WorldviewTruthAnchor{
			{Issue: "Runtime claim", Winner: "Desired State Spec"},
		},
	}
	if err := saveWorldviewTruthState(p, in); err != nil {
		t.Fatalf("saveWorldviewTruthState: %v", err)
	}
	out, found, err := loadWorldviewTruthState(p)
	if err != nil {
		t.Fatalf("loadWorldviewTruthState: %v", err)
	}
	if !found {
		t.Fatal("expected worldview state file to be found")
	}
	if out.TruthHash != in.TruthHash {
		t.Fatalf("expected truth hash %q, got %q", in.TruthHash, out.TruthHash)
	}
	if len(out.ResolutionAnchors) != 1 {
		t.Fatalf("expected 1 resolution anchor, got %d", len(out.ResolutionAnchors))
	}
}
