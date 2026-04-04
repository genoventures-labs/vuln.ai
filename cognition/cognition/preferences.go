package cognition

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Thynaptic/P-LMv1/pkg/memory"
)

const (
	defaultAlignmentAuditPath = ".memory/alignment_audit.json"
	defaultSessionStatePath   = ".memory/session_state.json"
	preferenceMemoryKind      = "preference_vector"
)

type PreferencePattern struct {
	Key         string  `json:"key"`
	Description string  `json:"description"`
	Weight      float64 `json:"weight"`
}

type PreferenceModel struct {
	UpdatedAt time.Time           `json:"updated_at"`
	Patterns  []PreferencePattern `json:"patterns"`
}

type auditViolation struct {
	Rule string `json:"rule"`
}

type auditEntry struct {
	Violations       []auditViolation `json:"violations"`
	OutputPreview    string           `json:"output_preview"`
	CorrectedPreview string           `json:"corrected_preview"`
}

type sessionSnapshot struct {
	AnalyticalMode float64  `json:"analytical_mode"`
	Frustration    float64  `json:"frustration"`
	Confidence     float64  `json:"confidence"`
	Subtext        []string `json:"subtext_markers"`
}

// MineAndPersistPreferences analyzes correction/state traces and stores preference vectors.
func MineAndPersistPreferences(mm *memory.MemoryManager) (*PreferenceModel, error) {
	model, err := MinePreferenceModel(defaultAlignmentAuditPath, defaultSessionStatePath)
	if err != nil {
		return nil, err
	}
	if mm != nil {
		_ = PersistPreferenceVectors(mm, model)
	}
	return model, nil
}

// MinePreferenceModel derives stable style/logic preferences from logs + state.
func MinePreferenceModel(auditPath, sessionPath string) (*PreferenceModel, error) {
	audits, _ := loadAuditEntries(auditPath)
	stateSnap, _ := loadSessionSnapshot(sessionPath)

	score := map[string]float64{}
	desc := map[string]string{}
	add := func(key, description string, delta float64) {
		if strings.TrimSpace(key) == "" || strings.TrimSpace(description) == "" || delta == 0 {
			return
		}
		score[key] += delta
		if _, ok := desc[key]; !ok {
			desc[key] = description
		}
	}

	for _, a := range audits {
		for _, v := range a.Violations {
			rule := strings.ToLower(strings.TrimSpace(v.Rule))
			switch rule {
			case "strict_masking", "redact_internal_network_details", "internal_ip_masking":
				add("security_redaction", "Always mask internal IPs and sensitive infra details.", 0.5)
			case "safe_operational_guidance":
				add("safety_guardrails", "Include explicit safety/validation guardrails for operational actions.", 0.4)
			}
		}
		orig := strings.ToLower(a.OutputPreview)
		corr := strings.ToLower(a.CorrectedPreview)
		if strings.Contains(corr, "import (") && !strings.Contains(orig, "import (") {
			add("go_grouped_imports", "Group Go imports into canonical import blocks.", 0.75)
		}
		if strings.Contains(corr, "gofmt") || strings.Contains(corr, "go fmt") {
			add("go_formatting", "Prefer gofmt-consistent formatting and idiomatic Go layout.", 0.4)
		}
	}

	if stateSnap.AnalyticalMode >= 0.70 {
		add("high_precision", "Prefer explicit, stepwise technical precision.", 0.45)
	}
	if stateSnap.Frustration >= 0.60 {
		add("direct_brief", "When tension rises, prefer direct concise delivery with minimal fluff.", 0.35)
	}
	if hasSubtext(stateSnap.Subtext, "excitement") && stateSnap.Confidence >= 0.55 {
		add("high_detail_exploration", "When momentum is high, provide high-detail options and tradeoffs.", 0.35)
	}
	if hasSubtext(stateSnap.Subtext, "fatigue") {
		add("concise_bullets", "Use concise bullets and reduce cognitive overhead.", 0.30)
	}

	patterns := make([]PreferencePattern, 0, len(score))
	for k, w := range score {
		patterns = append(patterns, PreferencePattern{
			Key:         k,
			Description: desc[k],
			Weight:      clampPreferenceWeight(w),
		})
	}
	sort.Slice(patterns, func(i, j int) bool {
		if patterns[i].Weight == patterns[j].Weight {
			return patterns[i].Key < patterns[j].Key
		}
		return patterns[i].Weight > patterns[j].Weight
	})
	if len(patterns) > 12 {
		patterns = patterns[:12]
	}

	return &PreferenceModel{
		UpdatedAt: time.Now().UTC(),
		Patterns:  patterns,
	}, nil
}

// PersistPreferenceVectors writes mined preferences into vector memory as preference vectors.
func PersistPreferenceVectors(mm *memory.MemoryManager, model *PreferenceModel) error {
	if mm == nil || model == nil || len(model.Patterns) == 0 {
		return nil
	}
	for _, p := range model.Patterns {
		content := "Preference Vector [" + p.Key + "]: " + p.Description
		meta := map[string]string{
			"memory_kind":       preferenceMemoryKind,
			"preference_key":    p.Key,
			"preference_weight": strconv.FormatFloat(clampPreferenceWeight(p.Weight), 'f', 4, 64),
			"source":            "alignment_audit+session_state",
		}
		_ = mm.AddKnowledge(content, meta)
	}
	return nil
}

// BuildStyleLogicDelta retrieves preference vectors and builds an instruction delta.
func BuildStyleLogicDelta(mm *memory.MemoryManager, query string) string {
	if mm == nil {
		return ""
	}
	segs, err := mm.RetrieveKnowledgeSegments("engineering style preference vector "+query, 10)
	if err != nil || len(segs) == 0 {
		if model, mineErr := MineAndPersistPreferences(mm); mineErr == nil && model != nil {
			return styleDeltaFromPatterns(model.Patterns)
		}
		return ""
	}

	patterns := []PreferencePattern{}
	seen := map[string]bool{}
	for _, s := range segs {
		if strings.ToLower(strings.TrimSpace(s.Metadata["memory_kind"])) != preferenceMemoryKind {
			continue
		}
		key := strings.TrimSpace(s.Metadata["preference_key"])
		if key == "" || seen[key] {
			continue
		}
		seen[key] = true
		w, _ := strconv.ParseFloat(strings.TrimSpace(s.Metadata["preference_weight"]), 64)
		patterns = append(patterns, PreferencePattern{
			Key:         key,
			Description: strings.TrimSpace(s.Content),
			Weight:      clampPreferenceWeight(w),
		})
	}
	if len(patterns) == 0 {
		return ""
	}
	sort.Slice(patterns, func(i, j int) bool {
		return patterns[i].Weight > patterns[j].Weight
	})
	if len(patterns) > 6 {
		patterns = patterns[:6]
	}
	return styleDeltaFromPatterns(patterns)
}

func styleDeltaFromPatterns(patterns []PreferencePattern) string {
	if len(patterns) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("Style/Logic Delta:\n")
	for _, p := range patterns {
		line := strings.TrimSpace(p.Description)
		if strings.HasPrefix(strings.ToLower(line), "preference vector") {
			if idx := strings.Index(line, ":"); idx >= 0 && idx+1 < len(line) {
				line = strings.TrimSpace(line[idx+1:])
			}
		}
		if line == "" {
			line = p.Key
		}
		b.WriteString("- ")
		b.WriteString(line)
		b.WriteString("\n")
	}
	b.WriteString("Match these preferences in structure, rigor, and wording.")
	return strings.TrimSpace(b.String())
}

func loadAuditEntries(path string) ([]auditEntry, error) {
	if strings.TrimSpace(path) == "" {
		path = defaultAlignmentAuditPath
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var out []auditEntry
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func loadSessionSnapshot(path string) (sessionSnapshot, error) {
	if strings.TrimSpace(path) == "" {
		path = defaultSessionStatePath
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return sessionSnapshot{}, err
	}
	var out sessionSnapshot
	if err := json.Unmarshal(b, &out); err != nil {
		return sessionSnapshot{}, err
	}
	return out, nil
}

func hasSubtext(in []string, marker string) bool {
	marker = strings.ToLower(strings.TrimSpace(marker))
	for _, v := range in {
		if strings.ToLower(strings.TrimSpace(v)) == marker {
			return true
		}
	}
	return false
}

func clampPreferenceWeight(v float64) float64 {
	if v < 0.05 {
		return 0.05
	}
	if v > 1.0 {
		return 1.0
	}
	return v
}

func (pm PreferenceModel) String() string {
	return fmt.Sprintf("PreferenceModel{patterns:%d}", len(pm.Patterns))
}
