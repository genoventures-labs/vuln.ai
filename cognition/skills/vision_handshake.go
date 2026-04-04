package skills

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

const (
	visualHandshakeEnabledEnv            = "TALOS_VISUAL_HANDSHAKE_ENABLED"
	visualHandshakeMaxSnippetCharsEnv    = "TALOS_VISUAL_HANDSHAKE_MAX_SNIPPET_CHARS"
	visualHandshakeMaxSpatialElementsEnv = "TALOS_VISUAL_HANDSHAKE_MAX_SPATIAL_ELEMENTS"
	visualHandshakeRequireConsentEnv     = "TALOS_VISUAL_HANDSHAKE_REQUIRE_CONSENT"
	visualHandshakeRedactSnippetsEnv     = "TALOS_VISUAL_HANDSHAKE_REDACT_SNIPPETS"
)

// VisualTargetPayload is the bridge payload from overlays/vision into tool calls.
type VisualTargetPayload struct {
	CapturePath     string            `json:"capture_path,omitempty"`
	CaptureMode     string            `json:"capture_mode,omitempty"`
	WindowID        string            `json:"window_id,omitempty"`
	TargetID        string            `json:"target_id,omitempty"`
	X               int               `json:"x"`
	Y               int               `json:"y"`
	Width           int               `json:"width"`
	Height          int               `json:"height"`
	Label           string            `json:"label,omitempty"`
	Snippet         string            `json:"snippet,omitempty"`
	Confidence      float64           `json:"confidence,omitempty"`
	OverlayKind     string            `json:"overlay_kind,omitempty"`
	SourceModel     string            `json:"source_model,omitempty"`
	SpatialElements []SpatialElement  `json:"spatial_elements,omitempty"`
	Provenance      map[string]string `json:"provenance,omitempty"`
}

// AnalyzeVisualTargetRequest requests targeted visual analysis around one ROI.
type AnalyzeVisualTargetRequest struct {
	Consent      bool                `json:"consent"`
	Intent       string              `json:"intent,omitempty"`
	Target       VisualTargetPayload `json:"target"`
	ShellContext string              `json:"shell_context,omitempty"`
}

// AnalyzeVisualTargetResult returns grounded ROI-level findings.
type AnalyzeVisualTargetResult struct {
	Allowed          bool           `json:"allowed"`
	RequiresApproval bool           `json:"requires_approval"`
	VisualRequest    string         `json:"visual_request,omitempty"`
	Summary          string         `json:"summary,omitempty"`
	Findings         []string       `json:"findings,omitempty"`
	SuggestedActions []VisualAction `json:"suggested_actions,omitempty"`
	Confidence       float64        `json:"confidence,omitempty"`
	Citations        []string       `json:"citations,omitempty"`
	Redacted         bool           `json:"redacted,omitempty"`
}

// AnalyzeVisualTarget executes the vision-to-tool handshake target analysis.
func AnalyzeVisualTarget(req AnalyzeVisualTargetRequest) AnalyzeVisualTargetResult {
	if !envBoolVisionHandshake(visualHandshakeEnabledEnv, true) {
		return AnalyzeVisualTargetResult{Allowed: false, Summary: "visual handshake disabled"}
	}
	if envBoolVisionHandshake(visualHandshakeRequireConsentEnv, true) && !req.Consent {
		return AnalyzeVisualTargetResult{
			Allowed:          false,
			RequiresApproval: true,
			VisualRequest:    "Visual target analysis requires explicit consent. Re-run with consent=true.",
		}
	}
	maxSnippet := envIntVisionHandshake(visualHandshakeMaxSnippetCharsEnv, 280)
	maxSpatial := envIntVisionHandshake(visualHandshakeMaxSpatialElementsEnv, 12)
	target, redacted := sanitizeVisualTargetPayload(req.Target, maxSnippet, maxSpatial, envBoolVisionHandshake(visualHandshakeRedactSnippetsEnv, true))

	result := AnalyzeVisualTargetResult{Allowed: true, Confidence: clampVisual(target.Confidence), Redacted: redacted}
	if result.Confidence <= 0 {
		result.Confidence = 0.55
	}
	if strings.TrimSpace(target.CapturePath) != "" {
		if _, err := os.Stat(strings.TrimSpace(target.CapturePath)); err == nil {
			prompt := buildVisualTargetPrompt(strings.TrimSpace(req.Intent), target)
			vr, err := AnalyzeImageWithVLM(strings.TrimSpace(target.CapturePath), prompt, strings.TrimSpace(req.ShellContext))
			if err == nil {
				vr = MergeSpatialEvidence(vr, CaptureScreenResult{Path: target.CapturePath, OCRText: target.Snippet, SpatialElements: target.SpatialElements})
				result.Summary = strings.TrimSpace(vr.Summary)
				result.Findings = append(result.Findings, vr.TechnicalFindings...)
				actions := BuildGroundedActions(vr, req.ShellContext)
				if len(actions) > 0 {
					result.SuggestedActions = actions
					if primary, ok := SelectPrimaryAction(actions); ok && primary.Confidence > result.Confidence {
						result.Confidence = clampVisual(primary.Confidence)
					}
				}
			}
		}
	}
	if strings.TrimSpace(result.Summary) == "" {
		result.Summary = fallbackVisualSummary(strings.TrimSpace(req.Intent), target)
	}
	if len(result.Findings) == 0 {
		result.Findings = fallbackVisualFindings(strings.TrimSpace(req.Intent), target)
	}
	if len(result.SuggestedActions) == 0 {
		result.SuggestedActions = fallbackVisualActions(strings.TrimSpace(req.Intent), target)
	}
	result.Citations = buildVisualCitations(target)
	return result
}

func sanitizeVisualTargetPayload(in VisualTargetPayload, maxSnippet, maxSpatial int, redact bool) (VisualTargetPayload, bool) {
	out := in
	redacted := false
	if out.Width <= 0 {
		out.Width = 160
	}
	if out.Height <= 0 {
		out.Height = 90
	}
	if out.X < 0 {
		out.X = 0
	}
	if out.Y < 0 {
		out.Y = 0
	}
	out.Label = strings.TrimSpace(out.Label)
	out.Snippet = strings.TrimSpace(out.Snippet)
	if maxSnippet <= 0 {
		maxSnippet = 280
	}
	if len(out.Snippet) > maxSnippet {
		out.Snippet = strings.TrimSpace(out.Snippet[:maxSnippet])
	}
	if redact && out.Snippet != "" {
		if matches := detectVisualSensitivePatterns(out.Snippet); len(matches) > 0 {
			out.Snippet = "[REDACTED_VISUAL_SNIPPET]"
			redacted = true
		}
	}
	if maxSpatial <= 0 {
		maxSpatial = 12
	}
	if len(out.SpatialElements) > maxSpatial {
		out.SpatialElements = append([]SpatialElement(nil), out.SpatialElements[:maxSpatial]...)
	}
	out.SpatialElements = dedupeSpatialElements(out.SpatialElements, maxSpatial)
	out.Confidence = clampVisual(out.Confidence)
	if out.Provenance == nil {
		out.Provenance = map[string]string{}
	}
	return out, redacted
}

func fallbackVisualSummary(intent string, target VisualTargetPayload) string {
	intent = strings.TrimSpace(intent)
	label := strings.TrimSpace(target.Label)
	if label == "" {
		label = "selected UI element"
	}
	if intent == "" {
		return fmt.Sprintf("Focused visual analysis prepared for %s at (%d,%d).", label, target.X, target.Y)
	}
	return fmt.Sprintf("Focused visual analysis for intent=%s on %s at (%d,%d).", intent, label, target.X, target.Y)
}

func fallbackVisualFindings(intent string, target VisualTargetPayload) []string {
	out := []string{}
	if strings.TrimSpace(target.Snippet) != "" {
		out = append(out, "ROI snippet: "+strings.TrimSpace(target.Snippet))
	}
	if strings.TrimSpace(target.OverlayKind) != "" {
		out = append(out, "Overlay kind: "+strings.TrimSpace(target.OverlayKind))
	}
	if strings.TrimSpace(intent) != "" {
		out = append(out, "Requested intent: "+strings.TrimSpace(intent))
	}
	if len(out) == 0 {
		out = append(out, "No high-signal snippet was available; using coordinate-only target grounding.")
	}
	return out
}

func fallbackVisualActions(intent string, target VisualTargetPayload) []VisualAction {
	label := strings.TrimSpace(target.Label)
	if label == "" {
		label = "visual target"
	}
	action := "Inspect the selected ROI and validate UI state against expected behavior."
	if strings.Contains(strings.ToLower(intent), "error") || strings.Contains(strings.ToLower(label), "error") {
		action = "Inspect the ROI for error persistence and verify whether fault markers are cleared."
	}
	return []VisualAction{{
		Action:     action,
		Target:     label,
		X:          target.X + maxIntVision(target.Width/2, 0),
		Y:          target.Y + maxIntVision(target.Height/2, 0),
		Width:      target.Width,
		Height:     target.Height,
		Confidence: clampVisual(maxFloatVisual(0.55, target.Confidence)),
		Rationale:  "vision-to-tool handshake target",
	}}
}

func buildVisualTargetPrompt(intent string, target VisualTargetPayload) string {
	intent = strings.TrimSpace(intent)
	if intent == "" {
		intent = "analyze_ui_element"
	}
	return fmt.Sprintf("Intent: %s\nTarget label: %s\nROI: x=%d y=%d w=%d h=%d\nSnippet: %s\nProvide focused analysis for this specific visual element.",
		intent,
		strings.TrimSpace(target.Label),
		target.X,
		target.Y,
		target.Width,
		target.Height,
		strings.TrimSpace(target.Snippet),
	)
}

func buildVisualCitations(target VisualTargetPayload) []string {
	var out []string
	if strings.TrimSpace(target.CapturePath) != "" {
		out = append(out, strings.TrimSpace(target.CapturePath))
	}
	if s := strings.TrimSpace(target.SourceModel); s != "" {
		out = append(out, "visual_model:"+s)
	}
	if p := strings.TrimSpace(target.Provenance["surface"]); p != "" {
		out = append(out, "surface:"+p)
	}
	if p := strings.TrimSpace(target.Provenance["stage"]); p != "" {
		out = append(out, "stage:"+p)
	}
	if p := strings.TrimSpace(target.Provenance["turn_id"]); p != "" {
		out = append(out, "turn:"+p)
	}
	return dedupeVisionStrings(out)
}

func envBoolVisionHandshake(key string, fallback bool) bool {
	raw := strings.ToLower(strings.TrimSpace(os.Getenv(key)))
	switch raw {
	case "":
		return fallback
	case "1", "true", "yes", "y", "on":
		return true
	case "0", "false", "no", "n", "off":
		return false
	default:
		return fallback
	}
}

func envIntVisionHandshake(key string, fallback int) int {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback
	}
	v, err := strconv.Atoi(raw)
	if err != nil {
		return fallback
	}
	return v
}

func maxFloatVisual(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}
