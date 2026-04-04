package taloscli

import "testing"

func TestDetectSmokingGunStreamSignal(t *testing.T) {
	reason, ok := detectSmokingGunStreamSignal("Found SMOKING GUN: credential exposed in logs")
	if !ok {
		t.Fatal("expected smoking-gun detection")
	}
	if reason == "" {
		t.Fatal("expected non-empty reason")
	}
	if _, ok := detectSmokingGunStreamSignal("normal progress update, no issues"); ok {
		t.Fatal("expected non-detection for benign stream chunk")
	}
}
