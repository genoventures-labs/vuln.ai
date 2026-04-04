package cognition

import "testing"

func TestParseStyleModelResponse_ParsesJSON(t *testing.T) {
	raw := `{"tone":"supportive","structure":"bullet_first","density":1.12}`
	out, ok := parseStyleModelResponse(raw)
	if !ok {
		t.Fatal("expected parse success")
	}
	if out.Tone != "supportive" {
		t.Fatalf("unexpected tone: %q", out.Tone)
	}
	if out.Structure != "bullet_first" {
		t.Fatalf("unexpected structure: %q", out.Structure)
	}
	if out.Density <= 0 {
		t.Fatalf("unexpected density: %f", out.Density)
	}
}

func TestMergeStyleProfile_BoundedDensityDelta(t *testing.T) {
	base := StyleProfile{Density: 0.80, Tone: "direct_technical", Structure: "bullet_first"}
	refined := StyleProfile{Density: 1.40, Tone: "supportive", Structure: "table_first", FromModel: true}
	out := mergeStyleProfile(base, refined)
	if out.Density > 1.00 {
		t.Fatalf("expected density bounded near base, got %f", out.Density)
	}
	if out.Tone != "supportive" {
		t.Fatalf("expected tone override, got %q", out.Tone)
	}
}
