package cognition

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/Thynaptic/P-LMv1/pkg/memory"
	"github.com/Thynaptic/P-LMv1/pkg/state"
)

// StyleProfile controls output structure/tone density for a turn.
type StyleProfile struct {
	Mode                 string  `json:"mode"`
	Tone                 string  `json:"tone"`
	Structure            string  `json:"structure"`
	Density              float64 `json:"density"`
	VerbosityTarget      int     `json:"verbosity_target"`
	EvidenceBias         float64 `json:"evidence_bias"`
	RiskBias             float64 `json:"risk_bias"`
	SourceCitationStrict bool    `json:"source_citation_strict"`
	FromModel            bool    `json:"from_model"`
	Version              string  `json:"version"`
}

func StyleV2Enabled() bool {
	return envBoolStyle("TALOS_STYLE_V2_ENABLED", true)
}

func StyleModelEnabled() bool {
	return envBoolStyle("TALOS_STYLE_MODEL_ENABLED", true)
}

// BuildStyleProfile builds a deterministic style baseline for the current turn.
func BuildStyleProfile(sm *state.Manager, mm *memory.MemoryManager, query string, mode string) StyleProfile {
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode == "" || mode == "auto" {
		mode = "balanced"
	}
	if mode != "minimal" && mode != "balanced" && mode != "deep" {
		mode = "balanced"
	}

	profile := StyleProfile{
		Mode:                 mode,
		Tone:                 "direct_technical",
		Structure:            "bullet_first",
		Density:              1.0,
		VerbosityTarget:      3,
		EvidenceBias:         0.70,
		RiskBias:             0.60,
		SourceCitationStrict: true,
		FromModel:            false,
		Version:              "v2",
	}

	switch mode {
	case "minimal":
		profile.Density = 0.82
		profile.VerbosityTarget = 2
	case "deep":
		profile.Density = 1.18
		profile.VerbosityTarget = 4
		profile.Structure = "table_first"
	}

	if sm != nil {
		s := sm.GetSnapshot()
		if s.Frustration >= 0.70 || hasSubtextMarker(s.Subtext, "fatigue") {
			profile.Tone = "calm_clarifying"
			profile.Density = maxFloatStyle(0.72, profile.Density-0.14)
			profile.VerbosityTarget = minIntStyle(profile.VerbosityTarget, 2)
			profile.Structure = "bullet_first"
		}
		if s.Confidence >= 0.82 && s.AnalyticalMode >= 0.64 {
			profile.Tone = "direct_technical"
			profile.Density = minFloatStyle(1.28, profile.Density+0.08)
		}
		if s.ToneBias <= -0.35 || hasSubtextMarker(s.Subtext, "vulnerability") {
			profile.Tone = "supportive"
			profile.Structure = "narrative_first"
			profile.Density = maxFloatStyle(0.74, profile.Density-0.10)
		}
		if s.ToneBias >= 0.35 || hasSubtextMarker(s.Subtext, "excitement") {
			profile.Tone = "energetic_solution"
			profile.Structure = "bullet_first"
		}
	}

	q := strings.ToLower(strings.TrimSpace(query))
	if strings.Count(q, " and ") >= 2 || strings.Contains(q, "compare") || strings.Contains(q, "tradeoff") {
		profile.Structure = "table_first"
		profile.EvidenceBias = minFloatStyle(1.0, profile.EvidenceBias+0.10)
		profile.RiskBias = minFloatStyle(1.0, profile.RiskBias+0.08)
	}
	if len(q) > 180 {
		profile.VerbosityTarget = minIntStyle(5, profile.VerbosityTarget+1)
		profile.Density = minFloatStyle(1.35, profile.Density+0.08)
	}

	// Preference vectors can tilt structure without overriding safety defaults.
	if mm != nil {
		delta := strings.ToLower(BuildStyleLogicDelta(mm, query))
		switch {
		case strings.Contains(delta, "concise"):
			profile.Density = maxFloatStyle(0.70, profile.Density-0.08)
			profile.VerbosityTarget = minIntStyle(profile.VerbosityTarget, 2)
		case strings.Contains(delta, "high-detail") || strings.Contains(delta, "high detail"):
			profile.Density = minFloatStyle(1.35, profile.Density+0.08)
			profile.VerbosityTarget = maxIntStyle(profile.VerbosityTarget, 4)
		}
		if strings.Contains(delta, "stepwise") || strings.Contains(delta, "stepwise technical") {
			profile.Structure = "bullet_first"
		}
	}

	profile.Density = clampRangeStyle(profile.Density, 0.60, 1.40)
	profile.VerbosityTarget = clampIntStyle(profile.VerbosityTarget, 1, 5)
	profile.EvidenceBias = clampRangeStyle(profile.EvidenceBias, 0, 1)
	profile.RiskBias = clampRangeStyle(profile.RiskBias, 0, 1)
	return profile
}

// ResolveStyleProfile returns an immediate profile and refreshes model refinement async.
func ResolveStyleProfile(sm *state.Manager, mm *memory.MemoryManager, query string, mode string) StyleProfile {
	base := BuildStyleProfile(sm, mm, query, mode)
	if !StyleV2Enabled() {
		return base
	}
	key := styleCacheKey(query, mode, sm)
	if cached, ok := globalStyleProfileCache.get(key); ok {
		return mergeStyleProfile(base, cached)
	}
	if !StyleModelEnabled() {
		return base
	}
	go refreshStyleProfileAsync(key, query, base)
	return base
}

// BuildStylePromptContract compiles an explicit style contract for prompt conditioning.
func BuildStylePromptContract(p StyleProfile) string {
	structure := p.Structure
	if structure == "" {
		structure = "bullet_first"
	}
	tone := p.Tone
	if tone == "" {
		tone = "direct_technical"
	}
	return strings.TrimSpace(fmt.Sprintf(`Thynaptic Style Contract v2:
- mode=%s
- tone=%s
- structure=%s
- density=%.2f
- verbosity_target=%d/5
- evidence_bias=%.2f
- risk_bias=%.2f
- source_citation_strict=%t
Rules:
- Keep wording precise, professional, and non-fluffy.
- Prefer deterministic structure (headings + flat bullets) over loose prose.
- Cite available sources/paths/URLs explicitly when present.`,
		p.Mode,
		tone,
		structure,
		p.Density,
		p.VerbosityTarget,
		p.EvidenceBias,
		p.RiskBias,
		p.SourceCitationStrict,
	))
}

func mergeStyleProfile(base, refined StyleProfile) StyleProfile {
	out := base
	if strings.TrimSpace(refined.Tone) != "" {
		out.Tone = refined.Tone
	}
	if strings.TrimSpace(refined.Structure) != "" {
		out.Structure = refined.Structure
	}
	out.Density = clampRangeStyle(refined.Density, maxFloatStyle(0.60, base.Density-0.20), minFloatStyle(1.40, base.Density+0.20))
	out.FromModel = refined.FromModel
	return out
}

func styleCacheKey(query, mode string, sm *state.Manager) string {
	raw := strings.ToLower(strings.TrimSpace(query)) + "|" + strings.ToLower(strings.TrimSpace(mode))
	if sm != nil {
		s := sm.GetSnapshot()
		raw += fmt.Sprintf("|%.2f|%.2f|%.2f|%.2f|%.2f|%.2f|%s",
			s.Confidence,
			s.Urgency,
			s.AnalyticalMode,
			s.Frustration,
			s.GoalPersistence,
			s.ToneBias,
			strings.Join(s.Subtext, ","),
		)
	}
	sum := sha1.Sum([]byte(raw))
	return hex.EncodeToString(sum[:])
}

func refreshStyleProfileAsync(key, query string, base StyleProfile) {
	scorer := newDefaultStyleScorer()
	ctxTimeout := clampIntStyle(envIntStyle("TALOS_STYLE_MODEL_TIMEOUT_MS", 120), 60, 600)
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(ctxTimeout)*time.Millisecond)
	defer cancel()
	refined, err := scorer.Score(ctx, query, base)
	if err != nil {
		return
	}
	refined.FromModel = true
	refined.Version = base.Version
	globalStyleProfileCache.set(key, refined)
}

func hasSubtextMarker(markers []string, marker string) bool {
	marker = strings.ToLower(strings.TrimSpace(marker))
	for _, m := range markers {
		if strings.ToLower(strings.TrimSpace(m)) == marker {
			return true
		}
	}
	return false
}

func envBoolStyle(key string, fallback bool) bool {
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

func envIntStyle(key string, fallback int) int {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback
	}
	var out int
	_, err := fmt.Sscanf(raw, "%d", &out)
	if err != nil {
		return fallback
	}
	return out
}

func clampRangeStyle(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func clampIntStyle(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func maxFloatStyle(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}

func minFloatStyle(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}

func maxIntStyle(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func minIntStyle(a, b int) int {
	if a < b {
		return a
	}
	return b
}
