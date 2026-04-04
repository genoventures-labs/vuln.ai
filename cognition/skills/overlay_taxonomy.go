package skills

import "strings"

type OverlayKind string

const (
	OverlayKindDocument      OverlayKind = "document"
	OverlayKindContradiction OverlayKind = "contradiction"
	OverlayKindEvidence      OverlayKind = "evidence"
	OverlayKindTimeline      OverlayKind = "timeline"
	OverlayKindRisk          OverlayKind = "risk"
	OverlayKindFix           OverlayKind = "fix"
	OverlayKindTarget        OverlayKind = "target"
)

var overlayKindColor = map[OverlayKind]string{
	OverlayKindContradiction: "#ff4d4f",
	OverlayKindEvidence:      "#3b82f6",
	OverlayKindDocument:      "#06b6d4",
	OverlayKindTimeline:      "#f59e0b",
	OverlayKindRisk:          "#f97316",
	OverlayKindFix:           "#22c55e",
	OverlayKindTarget:        "#ffffff",
}

func normalizeOverlayKind(raw string) OverlayKind {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "document", "doc", "signature", "entity":
		return OverlayKindDocument
	case "contradiction", "conflict", "anomaly", "smoking_gun":
		return OverlayKindContradiction
	case "evidence", "proof", "support":
		return OverlayKindEvidence
	case "timeline", "date", "timestamp":
		return OverlayKindTimeline
	case "risk":
		return OverlayKindRisk
	case "fix", "verified", "confirmed":
		return OverlayKindFix
	case "target":
		return OverlayKindTarget
	default:
		return OverlayKindEvidence
	}
}

func overlayKindPrefix(kind OverlayKind) string {
	switch kind {
	case OverlayKindDocument:
		return "DOCUMENT:"
	case OverlayKindContradiction:
		return "CONTRADICTION:"
	case OverlayKindEvidence:
		return "EVIDENCE:"
	case OverlayKindTimeline:
		return "TIMELINE:"
	case OverlayKindRisk:
		return "RISK:"
	case OverlayKindFix:
		return "FIX:"
	case OverlayKindTarget:
		return "TARGET:"
	default:
		return "EVIDENCE:"
	}
}

func overlayColorForKind(kind OverlayKind) string {
	if c, ok := overlayKindColor[kind]; ok {
		return c
	}
	return "#3b82f6"
}

func normalizeOverlayLabel(kind OverlayKind, text string) string {
	text = strings.Join(strings.Fields(strings.TrimSpace(text)), " ")
	if text == "" {
		text = "Visual marker"
	}
	prefix := overlayKindPrefix(kind)
	if strings.HasPrefix(strings.ToUpper(text), prefix) {
		return truncateOverlayText(text, 92)
	}
	return truncateOverlayText(prefix+" "+text, 92)
}

func truncateOverlayText(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	if n < 4 {
		return s[:n]
	}
	return s[:n-3] + "..."
}
