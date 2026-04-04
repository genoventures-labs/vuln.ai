package skills

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// VisualManifest defines the Thynaptic brand contract.
type VisualManifest struct {
	Name                 string   `json:"name"`
	Theme                string   `json:"theme"`
	ColorProfile         string   `json:"color_profile"`
	Tone                 string   `json:"tone"`
	RequiredSignals      []string `json:"required_signals"`
	AntiPatterns         []string `json:"anti_patterns"`
	PreferredFonts       []string `json:"preferred_fonts"`
	MinContrastIndicator float64  `json:"min_contrast_indicator"`
}

// BrandSignal captures one style feature hit.
type BrandSignal struct {
	Signal  string  `json:"signal"`
	Weight  float64 `json:"weight"`
	Present bool    `json:"present"`
	Source  string  `json:"source,omitempty"`
}

// BrandAnalysis is the style mirror output for workspace UI assets.
type BrandAnalysis struct {
	Timestamp      time.Time     `json:"timestamp"`
	WorkspaceRoot  string        `json:"workspace_root"`
	ActiveFile     string        `json:"active_file,omitempty"`
	FilesScanned   int           `json:"files_scanned"`
	BrandScore     float64       `json:"brand_score"`
	Alignment      string        `json:"alignment"` // aligned | drifting | generic_web2
	Signals        []BrandSignal `json:"signals,omitempty"`
	VetoTriggered  bool          `json:"veto_triggered"`
	VetoReason     string        `json:"veto_reason,omitempty"`
	CSSSuggestion  string        `json:"css_suggestion,omitempty"`
	BrandNarrative string        `json:"brand_narrative,omitempty"`
	ProductIDLine  string        `json:"product_id_line,omitempty"`
}

// AestheticVetoResult returns a suggested tweak and optional HUD overlay result.
type AestheticVetoResult struct {
	Analysis       BrandAnalysis `json:"analysis"`
	SuggestedPatch string        `json:"suggested_patch,omitempty"`
	HUD            DrawBoxResult `json:"hud"`
}

var (
	neutralBgRE     = regexp.MustCompile(`(?i)#(?:fff|ffffff|f8f8f8|f5f5f5|fafafa)\b|rgb\(\s*255\s*,\s*255\s*,\s*255`)
	defaultFontRE   = regexp.MustCompile(`(?i)\bfont-family\s*:\s*(?:inter|roboto|arial|sans-serif|system-ui)`)
	darkModeHintRE  = regexp.MustCompile(`(?i)(prefers-color-scheme:\s*dark|--bg|--surface|background(?:-color)?\s*:\s*#0|#111|#121212)`)
	contrastHintRE  = regexp.MustCompile(`(?i)(--text|contrast|font-weight\s*:\s*(?:600|700|800)|letter-spacing)`)
	cyberpunkHintRE = regexp.MustCompile(`(?i)(neon|cyan|magenta|glow|grid|scanline|backdrop-filter|mix-blend-mode|glass)`)
)

// DefaultThynapticVisualManifest returns brand constraints for style mirror.
func DefaultThynapticVisualManifest() VisualManifest {
	return VisualManifest{
		Name:         "Thynaptic Visual Manifest",
		Theme:        "Dark-mode",
		ColorProfile: "High-contrast",
		Tone:         "Professional-cyberpunk",
		RequiredSignals: []string{
			"dark_surface", "high_contrast_text", "explicit_design_tokens", "distinctive_typography",
		},
		AntiPatterns: []string{
			"white_default_background", "generic_system_font_stack", "flat_web2_layout",
		},
		PreferredFonts:       []string{"Space Grotesk", "Sora", "IBM Plex Sans", "JetBrains Mono"},
		MinContrastIndicator: 0.65,
	}
}

// AnalyzeBrandConsistency is the Style Mirror engine.
func AnalyzeBrandConsistency(workspaceRoot, activeFile string) BrandAnalysis {
	return AnalyzeBrandConsistencyWithManifest(workspaceRoot, activeFile, DefaultThynapticVisualManifest())
}

// AnalyzeBrandConsistencyWithManifest analyzes CSS/Svelte/HTML against a visual manifest.
func AnalyzeBrandConsistencyWithManifest(workspaceRoot, activeFile string, manifest VisualManifest) BrandAnalysis {
	root := strings.TrimSpace(workspaceRoot)
	if root == "" {
		root = "."
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		absRoot = root
	}
	active := strings.TrimSpace(activeFile)

	paths := collectDesignFiles(absRoot)
	if active != "" {
		if rel, ok := makeActiveFirst(absRoot, active, paths); ok {
			paths = rel
		}
	}
	if len(paths) > 120 {
		paths = paths[:120]
	}

	score := 0.50
	var signals []BrandSignal
	hit := func(name string, weight float64, present bool, src string) {
		signals = append(signals, BrandSignal{Signal: name, Weight: weight, Present: present, Source: src})
		if present {
			score += weight
		} else {
			score -= weight * 0.7
		}
	}

	darkPresent := false
	contrastPresent := false
	cyberPresent := false
	tokenPresent := false
	whitePenalty := false
	genericPenalty := false
	flatPenalty := false
	sourceHit := map[string]string{}

	for _, p := range paths {
		raw, err := os.ReadFile(p)
		if err != nil || len(raw) == 0 {
			continue
		}
		txt := string(raw)
		if !darkPresent && darkModeHintRE.MatchString(txt) {
			darkPresent = true
			sourceHit["dark_surface"] = shortDesignPath(absRoot, p)
		}
		if !contrastPresent && contrastHintRE.MatchString(txt) {
			contrastPresent = true
			sourceHit["high_contrast_text"] = shortDesignPath(absRoot, p)
		}
		if !cyberPresent && cyberpunkHintRE.MatchString(txt) {
			cyberPresent = true
			sourceHit["professional_cyberpunk"] = shortDesignPath(absRoot, p)
		}
		if !tokenPresent && strings.Contains(strings.ToLower(txt), ":root") && strings.Contains(strings.ToLower(txt), "--") {
			tokenPresent = true
			sourceHit["design_tokens"] = shortDesignPath(absRoot, p)
		}
		if !whitePenalty && neutralBgRE.MatchString(txt) {
			whitePenalty = true
			sourceHit["white_default_background"] = shortDesignPath(absRoot, p)
		}
		if !genericPenalty && defaultFontRE.MatchString(txt) {
			genericPenalty = true
			sourceHit["generic_system_font_stack"] = shortDesignPath(absRoot, p)
		}
		if !flatPenalty && strings.Contains(strings.ToLower(txt), "display:flex") &&
			!strings.Contains(strings.ToLower(txt), "box-shadow") &&
			!strings.Contains(strings.ToLower(txt), "background: linear-gradient") {
			flatPenalty = true
			sourceHit["flat_web2_layout"] = shortDesignPath(absRoot, p)
		}
	}

	hit("dark_surface", 0.12, darkPresent, sourceHit["dark_surface"])
	hit("high_contrast_text", 0.10, contrastPresent, sourceHit["high_contrast_text"])
	hit("professional_cyberpunk", 0.08, cyberPresent, sourceHit["professional_cyberpunk"])
	hit("design_tokens", 0.08, tokenPresent, sourceHit["design_tokens"])
	hit("white_default_background", 0.10, !whitePenalty, sourceHit["white_default_background"])
	hit("generic_system_font_stack", 0.10, !genericPenalty, sourceHit["generic_system_font_stack"])
	hit("flat_web2_layout", 0.08, !flatPenalty, sourceHit["flat_web2_layout"])

	score = clampDesign(score)
	alignment := "aligned"
	veto := false
	vetoReason := ""
	if score < 0.55 || whitePenalty || genericPenalty {
		alignment = "generic_web2"
		veto = true
		vetoReason = "Aesthetic Veto: UI leans generic Web 2.0 and drifts from Thynaptic visual identity."
	} else if score < 0.72 {
		alignment = "drifting"
	}

	suggestion := suggestBrandCSSTweak(whitePenalty, genericPenalty, flatPenalty, darkPresent, tokenPresent)
	productID := fmt.Sprintf("Brand Alignment: %d%% | Manifest: %s", int(score*100), manifest.Name)
	narrative := "Style Mirror: visual profile aligns with Thynaptic identity."
	if veto {
		narrative = "Style Mirror: Generic Web 2.0 drift detected. Applying Aesthetic Veto guidance."
	}

	return BrandAnalysis{
		Timestamp:      time.Now().UTC(),
		WorkspaceRoot:  absRoot,
		ActiveFile:     active,
		FilesScanned:   len(paths),
		BrandScore:     score,
		Alignment:      alignment,
		Signals:        signals,
		VetoTriggered:  veto,
		VetoReason:     vetoReason,
		CSSSuggestion:  suggestion,
		BrandNarrative: narrative,
		ProductIDLine:  productID,
	}
}

// RunAestheticVeto runs style mirror and emits a HUD overlay suggestion if needed.
func RunAestheticVeto(workspaceRoot, activeFile string, consent bool) AestheticVetoResult {
	analysis := AnalyzeBrandConsistency(workspaceRoot, activeFile)
	out := AestheticVetoResult{
		Analysis:       analysis,
		SuggestedPatch: analysis.CSSSuggestion,
		HUD:            DrawBoxResult{ExitCode: 0},
	}
	if !analysis.VetoTriggered {
		// Show compact product-id badge even when aligned.
		out.HUD = DrawBox(DrawBoxRequest{
			Consent:    consent,
			X:          28,
			Y:          24,
			W:          300,
			H:          66,
			Text:       analysis.ProductIDLine,
			DurationMS: 2600,
		})
		return out
	}
	overlayText := strings.TrimSpace(analysis.VetoReason + " | Suggested tweak: " + oneLineDesign(analysis.CSSSuggestion))
	out.HUD = DrawBox(DrawBoxRequest{
		Consent:    consent,
		X:          34,
		Y:          28,
		W:          560,
		H:          112,
		Text:       overlayText,
		DurationMS: 4800,
	})
	return out
}

func collectDesignFiles(root string) []string {
	var out []string
	_ = filepath.WalkDir(root, func(path string, de os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if de.IsDir() {
			p := strings.ToLower(filepath.ToSlash(path))
			for _, skip := range []string{"/.git", "/node_modules", "/dist", "/build", "/.memory", "/vendor"} {
				if strings.Contains(p, skip) {
					return filepath.SkipDir
				}
			}
			return nil
		}
		ext := strings.ToLower(filepath.Ext(path))
		switch ext {
		case ".css", ".scss", ".sass", ".svelte", ".html":
			out = append(out, path)
		}
		return nil
	})
	sort.Strings(out)
	return out
}

func makeActiveFirst(root, active string, in []string) ([]string, bool) {
	act := strings.TrimSpace(active)
	if act == "" {
		return in, false
	}
	if !filepath.IsAbs(act) {
		act = filepath.Join(root, act)
	}
	act = filepath.Clean(act)
	var out []string
	found := false
	for _, p := range in {
		if filepath.Clean(p) == act {
			out = append(out, p)
			found = true
			break
		}
	}
	for _, p := range in {
		if filepath.Clean(p) == act {
			continue
		}
		out = append(out, p)
	}
	return out, found
}

func suggestBrandCSSTweak(whitePenalty, genericPenalty, flatPenalty, darkPresent, tokenPresent bool) string {
	var b strings.Builder
	b.WriteString(":root {\n")
	if !tokenPresent {
		b.WriteString("  --bg: #090f18;\n  --surface: #111a28;\n  --text: #e7f3ff;\n  --accent: #22d3ee;\n")
	} else {
		b.WriteString("  --accent: #22d3ee;\n")
	}
	b.WriteString("}\n")
	if whitePenalty || !darkPresent {
		b.WriteString("body { background: radial-gradient(1200px 700px at 80% -10%, #123048 0%, #090f18 45%, #05070c 100%); color: var(--text); }\n")
	}
	if genericPenalty {
		b.WriteString("body { font-family: 'Space Grotesk', 'IBM Plex Sans', sans-serif; }\n")
	}
	if flatPenalty {
		b.WriteString(".panel { background: linear-gradient(180deg, #0f1725 0%, #0b1220 100%); border: 1px solid #1e2f47; box-shadow: 0 10px 30px rgba(0,0,0,.35); }\n")
	}
	b.WriteString(".brand-accent { color: var(--accent); text-shadow: 0 0 18px rgba(34,211,238,.20); }\n")
	return b.String()
}

func shortDesignPath(root, p string) string {
	rel, err := filepath.Rel(root, p)
	if err != nil {
		return p
	}
	return filepath.ToSlash(rel)
}

func oneLineDesign(s string) string {
	s = strings.Join(strings.Fields(strings.TrimSpace(s)), " ")
	if len(s) > 240 {
		return s[:240] + "...(trimmed)"
	}
	return s
}

func clampDesign(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}
