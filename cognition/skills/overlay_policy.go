package skills

import (
	"os"
	"strconv"
	"strings"
	"time"
)

type OverlayContext struct {
	Stage         string
	Explicit      bool
	HighRiskScore float64
	SessionID     string
}

type OverlayPolicy struct {
	Enabled               bool
	DefaultMode           string
	MaxBoxes              int
	MinConfidence         float64
	HighRiskAutoThreshold float64
	SessionConsentTTL     time.Duration
	TaxonomyStrict        bool
}

type OverlayDecision struct {
	Render        bool
	Reason        string
	Selected      []OverlayCandidate
	Dropped       []OverlayCandidate
	Cached        bool
	LatencyMicros int64
}

func DefaultOverlayPolicy() OverlayPolicy {
	maxBoxes := envIntOverlay("TALOS_OVERLAY_MAX_BOXES", 5)
	if maxBoxes < 1 {
		maxBoxes = 1
	}
	if maxBoxes > 12 {
		maxBoxes = 12
	}
	mode := strings.ToLower(strings.TrimSpace(os.Getenv("TALOS_OVERLAY_DEFAULT_MODE")))
	if mode == "" {
		mode = "quiet"
	}
	if mode != "quiet" && mode != "on_demand" && mode != "auto_high_risk" {
		mode = "quiet"
	}
	return OverlayPolicy{
		Enabled:               envBoolOverlay("TALOS_OVERLAY_POLICY_ENABLED", true),
		DefaultMode:           mode,
		MaxBoxes:              maxBoxes,
		MinConfidence:         clampOverlayFloat(envFloatOverlay("TALOS_OVERLAY_MIN_CONFIDENCE", 0.62), 0.10, 1.0),
		HighRiskAutoThreshold: clampOverlayFloat(envFloatOverlay("TALOS_OVERLAY_HIGH_RISK_AUTO_THRESHOLD", 0.82), 0.20, 1.0),
		SessionConsentTTL:     time.Duration(envIntOverlay("TALOS_OVERLAY_SESSION_CONSENT_TTL_SEC", 900)) * time.Second,
		TaxonomyStrict:        envBoolOverlay("TALOS_OVERLAY_TAXONOMY_STRICT", true),
	}
}

func EvaluateOverlayCandidates(cands []OverlayCandidate, ctx OverlayContext, policy OverlayPolicy) OverlayDecision {
	start := time.Now()
	decision := OverlayDecision{
		Render:   false,
		Reason:   "overlay disabled",
		Selected: nil,
		Dropped:  nil,
	}
	if !policy.Enabled {
		decision.LatencyMicros = time.Since(start).Microseconds()
		appendOverlayAudit(decision, ctx, policy, len(cands))
		return decision
	}
	if len(cands) == 0 {
		decision.Reason = "no overlay candidates"
		decision.LatencyMicros = time.Since(start).Microseconds()
		appendOverlayAudit(decision, ctx, policy, 0)
		return decision
	}

	ranked := rankOverlayCandidates(cands)
	selected := make([]OverlayCandidate, 0, policy.MaxBoxes)
	dropped := make([]OverlayCandidate, 0, len(ranked))
	for i, c := range ranked {
		if c.Confidence < policy.MinConfidence {
			dropped = append(dropped, c)
			continue
		}
		if len(selected) >= policy.MaxBoxes {
			dropped = append(dropped, c)
			continue
		}
		// Suppress heavy overlap with already-selected regions.
		if overlapsAny(c, selected) {
			dropped = append(dropped, c)
			continue
		}
		selected = append(selected, c)
		if i >= policy.MaxBoxes*2 && len(selected) > 0 {
			break
		}
	}

	decision.Selected = selected
	decision.Dropped = dropped
	render := false
	reason := "quiet mode suppressed auto overlay"
	switch policy.DefaultMode {
	case "quiet", "on_demand":
		if ctx.Explicit {
			render = len(selected) > 0
			reason = "explicit request"
		}
	case "auto_high_risk":
		if ctx.Explicit || ctx.HighRiskScore >= policy.HighRiskAutoThreshold {
			render = len(selected) > 0
			reason = "high-risk auto overlay"
		}
	}
	if !render && ctx.Explicit && len(selected) == 0 {
		reason = "explicit request but candidates below threshold"
	}
	decision.Render = render
	decision.Reason = reason
	decision.LatencyMicros = time.Since(start).Microseconds()
	appendOverlayAudit(decision, ctx, policy, len(cands))
	return decision
}

func ToWarRoomBoxes(selected []OverlayCandidate) []WarRoomBox {
	out := make([]WarRoomBox, 0, len(selected))
	for _, c := range selected {
		c = normalizeOverlayCandidate(c)
		out = append(out, WarRoomBox{
			X:     c.X,
			Y:     c.Y,
			W:     c.W,
			H:     c.H,
			Text:  c.Text,
			Kind:  string(c.Kind),
			Color: overlayColorForKind(c.Kind),
		})
	}
	return out
}

func overlapsAny(c OverlayCandidate, others []OverlayCandidate) bool {
	for _, o := range others {
		if rectOverlap(c.X, c.Y, c.W, c.H, o.X, o.Y, o.W, o.H) > 0.45 {
			return true
		}
	}
	return false
}

func rectOverlap(ax, ay, aw, ah, bx, by, bw, bh int) float64 {
	ax2, ay2 := ax+aw, ay+ah
	bx2, by2 := bx+bw, by+bh
	ix1 := maxIntOverlay(ax, bx)
	iy1 := maxIntOverlay(ay, by)
	ix2 := minIntOverlay(ax2, bx2)
	iy2 := minIntOverlay(ay2, by2)
	if ix2 <= ix1 || iy2 <= iy1 {
		return 0
	}
	inter := float64((ix2 - ix1) * (iy2 - iy1))
	a := float64(aw * ah)
	b := float64(bw * bh)
	den := a
	if b > den {
		den = b
	}
	if den <= 0 {
		return 0
	}
	return inter / den
}

func envBoolOverlay(key string, fallback bool) bool {
	raw := strings.ToLower(strings.TrimSpace(os.Getenv(key)))
	switch raw {
	case "":
		return fallback
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	default:
		return fallback
	}
}

func envIntOverlay(key string, fallback int) int {
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

func envFloatOverlay(key string, fallback float64) float64 {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback
	}
	v, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return fallback
	}
	return v
}

func maxIntOverlay(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func minIntOverlay(a, b int) int {
	if a < b {
		return a
	}
	return b
}
