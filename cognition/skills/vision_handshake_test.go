package skills

import (
	"strings"
	"testing"
)

func TestAnalyzeVisualTargetRequiresConsent(t *testing.T) {
	res := AnalyzeVisualTarget(AnalyzeVisualTargetRequest{
		Consent: false,
		Intent:  "analyze_ui_element",
		Target: VisualTargetPayload{
			X: 10, Y: 20, Width: 100, Height: 60, Label: "Save",
		},
	})
	if res.Allowed {
		t.Fatalf("expected disallowed when consent missing")
	}
	if !res.RequiresApproval {
		t.Fatalf("expected requires_approval when consent missing")
	}
}

func TestAnalyzeVisualTargetRedactsSensitiveSnippet(t *testing.T) {
	t.Setenv("TALOS_VISUAL_HANDSHAKE_REDACT_SNIPPETS", "true")
	res := AnalyzeVisualTarget(AnalyzeVisualTargetRequest{
		Consent: true,
		Intent:  "inspect_error",
		Target: VisualTargetPayload{
			X:          10,
			Y:          20,
			Width:      100,
			Height:     60,
			Label:      "Login",
			Snippet:    "password: hunter2",
			Confidence: 0.8,
		},
	})
	if !res.Allowed {
		t.Fatalf("expected allowed response")
	}
	if !res.Redacted {
		t.Fatalf("expected redacted=true")
	}
	joined := strings.ToLower(strings.Join(res.Findings, " | "))
	if !strings.Contains(joined, "redacted") {
		t.Fatalf("expected redacted marker in findings, got: %s", joined)
	}
}
