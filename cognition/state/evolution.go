package state

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const defaultBaselineFile = ".memory/personality_baseline.json"

// PersonalityBaseline persists mined long-term communication preferences.
type PersonalityBaseline struct {
	UpdatedAt         time.Time      `json:"updated_at"`
	Samples           int            `json:"samples"`
	PreferenceCounts  map[string]int `json:"preference_counts"`
	StablePreferences []string       `json:"stable_preferences"`
	LastEntropyMode   string         `json:"last_entropy_mode,omitempty"`
}

// EntropySettings maps cognitive state to generation entropy controls.
type EntropySettings struct {
	Temperature float64 `json:"temperature"`
	TopP        float64 `json:"top_p"`
	Mode        string  `json:"mode"`
}

// TraitMiner mines stable preferences from session state snapshots over time.
type TraitMiner struct {
	StatePath    string
	BaselinePath string
}

// NewTraitMiner creates a miner with default memory paths.
func NewTraitMiner() *TraitMiner {
	return &TraitMiner{
		StatePath:    defaultStateFile,
		BaselinePath: defaultBaselineFile,
	}
}

// MineTraits scans session_state and updates personality_baseline.
func (tm *TraitMiner) MineTraits() (PersonalityBaseline, error) {
	statePath := strings.TrimSpace(tm.StatePath)
	if statePath == "" {
		statePath = defaultStateFile
	}
	basePath := strings.TrimSpace(tm.BaselinePath)
	if basePath == "" {
		basePath = defaultBaselineFile
	}

	s, err := loadSessionSnapshot(statePath)
	if err != nil {
		return PersonalityBaseline{}, err
	}
	b, _ := loadBaseline(basePath)
	if b.PreferenceCounts == nil {
		b.PreferenceCounts = make(map[string]int)
	}
	b.Samples++

	for _, pref := range inferPreferences(s) {
		b.PreferenceCounts[pref]++
	}
	b.StablePreferences = deriveStablePreferences(b.PreferenceCounts, 3, 6)
	b.UpdatedAt = time.Now().UTC()

	if err := saveBaseline(basePath, b); err != nil {
		return PersonalityBaseline{}, err
	}
	return b, nil
}

// DetermineEntropy maps state + prompt context to generation entropy settings.
func DetermineEntropy(s SessionState, prompt string) EntropySettings {
	promptLower := strings.ToLower(strings.TrimSpace(prompt))
	excited := hasMarker(s.Subtext, "excitement") ||
		strings.Contains(promptLower, "brainstorm") ||
		strings.Contains(promptLower, "ideas") ||
		strings.Contains(promptLower, "creative")

	if s.AnalyticalMode >= 0.70 && !excited {
		return EntropySettings{Temperature: 0.10, TopP: 0.20, Mode: "precision"}
	}
	if excited {
		return EntropySettings{Temperature: 0.80, TopP: 0.90, Mode: "creative"}
	}
	if s.AnalyticalMode >= 0.58 {
		return EntropySettings{Temperature: 0.22, TopP: 0.45, Mode: "technical"}
	}
	return EntropySettings{Temperature: 0.45, TopP: 0.72, Mode: "balanced"}
}

// DetermineEntropyWithBaseline applies optional baseline preference modulation.
func DetermineEntropyWithBaseline(s SessionState, prompt string, baseline PersonalityBaseline) EntropySettings {
	e := DetermineEntropy(s, prompt)
	for _, p := range baseline.StablePreferences {
		lp := strings.ToLower(p)
		if strings.Contains(lp, "dry wit") && s.Frustration >= 0.6 {
			e.TopP = clampRange(e.TopP+0.05, 0.10, 1.0)
		}
		if strings.Contains(lp, "high detail when excited") && (hasMarker(s.Subtext, "excitement") || strings.Contains(strings.ToLower(prompt), "brainstorm")) {
			e.TopP = clampRange(e.TopP+0.08, 0.10, 1.0)
		}
	}
	return e
}

func inferPreferences(s SessionState) []string {
	var prefs []string
	if s.Frustration >= 0.65 && hasMarker(s.Subtext, "sarcasm") {
		prefs = append(prefs, "prefers dry wit when frustrated")
	}
	if hasMarker(s.Subtext, "excitement") && s.Confidence >= 0.55 {
		prefs = append(prefs, "prefers high detail when excited")
	}
	if hasMarker(s.Subtext, "fatigue") {
		prefs = append(prefs, "prefers concise responses when fatigued")
	}
	if hasMarker(s.Subtext, "vulnerability") {
		prefs = append(prefs, "prefers supportive framing when uncertain")
	}
	if s.AnalyticalMode >= 0.72 {
		prefs = append(prefs, "prefers precision-forward technical output")
	}
	if len(prefs) == 0 {
		prefs = append(prefs, "prefers balanced technical communication")
	}
	return prefs
}

func deriveStablePreferences(counts map[string]int, minCount int, maxN int) []string {
	type kv struct {
		Key string
		Val int
	}
	arr := make([]kv, 0, len(counts))
	for k, v := range counts {
		if v >= minCount {
			arr = append(arr, kv{Key: k, Val: v})
		}
	}
	sort.Slice(arr, func(i, j int) bool {
		if arr[i].Val == arr[j].Val {
			return arr[i].Key < arr[j].Key
		}
		return arr[i].Val > arr[j].Val
	})
	if len(arr) > maxN {
		arr = arr[:maxN]
	}
	out := make([]string, 0, len(arr))
	for _, e := range arr {
		out = append(out, e.Key)
	}
	return out
}

func hasMarker(markers []string, target string) bool {
	t := strings.ToLower(strings.TrimSpace(target))
	for _, m := range markers {
		if strings.ToLower(strings.TrimSpace(m)) == t {
			return true
		}
	}
	return false
}

func clampRange(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func loadSessionSnapshot(path string) (SessionState, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return SessionState{}, err
	}
	var s SessionState
	if err := json.Unmarshal(b, &s); err != nil {
		return SessionState{}, err
	}
	return s, nil
}

func loadBaseline(path string) (PersonalityBaseline, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return PersonalityBaseline{
				UpdatedAt:         time.Now().UTC(),
				PreferenceCounts:  map[string]int{},
				StablePreferences: []string{},
			}, nil
		}
		return PersonalityBaseline{}, err
	}
	var out PersonalityBaseline
	if err := json.Unmarshal(b, &out); err != nil {
		return PersonalityBaseline{}, err
	}
	if out.PreferenceCounts == nil {
		out.PreferenceCounts = map[string]int{}
	}
	return out, nil
}

func saveBaseline(path string, b PersonalityBaseline) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(b, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

// ResolveEntropyOptions returns Ollama chat options derived from current session state
// and personality baseline without requiring a live Manager reference.
func ResolveEntropyOptions(prompt string) (map[string]any, EntropySettings) {
	snapshot := defaultSessionState()
	if sm, err := NewManager(); err == nil && sm != nil {
		snapshot = sm.GetSnapshot()
	}

	entropy := DetermineEntropy(snapshot, prompt)
	if b, err := loadBaseline(defaultBaselineFile); err == nil {
		entropy = DetermineEntropyWithBaseline(snapshot, prompt, b)
	}
	return map[string]any{
		"temperature": entropy.Temperature,
		"top_p":       entropy.TopP,
	}, entropy
}
