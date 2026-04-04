package skills

import "testing"

func TestNormalizeOverlayKindAndLabel(t *testing.T) {
	k := normalizeOverlayKind("anomaly")
	if k != OverlayKindContradiction {
		t.Fatalf("expected contradiction kind, got %q", k)
	}
	label := normalizeOverlayLabel(k, "signature mismatch")
	if label == "" {
		t.Fatal("expected non-empty label")
	}
	if label[:14] != "CONTRADICTION:" {
		t.Fatalf("expected contradiction prefix, got %q", label)
	}
}

func TestOverlayColorForKind(t *testing.T) {
	if c := overlayColorForKind(OverlayKindEvidence); c == "" {
		t.Fatal("expected non-empty evidence color")
	}
}
