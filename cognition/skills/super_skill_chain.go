package skills

import (
	"fmt"
	"strings"
	"time"
)

const (
	defaultSuperSkillTaskType = "general"
)

// DraftTemporarySuperSkill drafts an ad-hoc planner super-skill that chains exactly 3 V3 skills.
func DraftTemporarySuperSkill(query string, candidates []SkillRecord) (SkillRecord, []SkillRecord, error) {
	query = strings.TrimSpace(query)
	picked := pickDistinctChainCandidates(candidates, 3)
	if len(picked) < 3 {
		return SkillRecord{}, nil, fmt.Errorf("at least 3 distinct candidates are required for super-skill chain")
	}
	now := time.Now().UTC()
	skillID := fmt.Sprintf("super_skill_%d", now.UnixNano())
	rev := revisionIDFrom(skillID, "0.1.0", "adhoc://super_skill")
	nameA := firstNonEmptySkillName(picked[0])
	nameB := firstNonEmptySkillName(picked[1])
	nameC := firstNonEmptySkillName(picked[2])
	taskType := strings.TrimSpace(picked[0].TaskType)
	if taskType == "" {
		taskType = defaultSuperSkillTaskType
	}
	rec := SkillRecord{
		SkillID:       skillID,
		RevisionID:    rev,
		Version:       "0.1.0",
		Status:        SkillStatusValidated,
		Name:          "planner_jit_super_skill",
		Intent:        firstNonEmptyStr(query, "complex multi-stage task"),
		Description:   fmt.Sprintf("Ad-hoc wrapper chaining V3 skills: %s -> %s -> %s", nameA, nameB, nameC),
		ReasoningTier: "deep",
		TaskType:      taskType,
		RootDir:       ".skills/jit/super",
		SourcePath:    "adhoc://super_skill",
		ManifestPath:  "adhoc://super_skill_manifest",
		PackageName:   "jit_super_skill",
		CompileOK:     true,
		Enabled:       true,
		Active:        true,
		Provenance:    "planner_jit_chain",
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	return rec, picked, nil
}

func pickDistinctChainCandidates(in []SkillRecord, maxN int) []SkillRecord {
	if maxN <= 0 {
		maxN = 3
	}
	seen := map[string]bool{}
	out := make([]SkillRecord, 0, maxN)
	for _, rec := range in {
		id := strings.TrimSpace(rec.SkillID)
		if id == "" || seen[id] {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(rec.Status), SkillStatusRevoked) || strings.EqualFold(strings.TrimSpace(rec.Status), SkillStatusDeprecated) {
			continue
		}
		seen[id] = true
		out = append(out, rec)
		if len(out) >= maxN {
			break
		}
	}
	return out
}

func firstNonEmptySkillName(rec SkillRecord) string {
	if s := strings.TrimSpace(rec.Name); s != "" {
		return s
	}
	if s := strings.TrimSpace(rec.SkillID); s != "" {
		return s
	}
	return "skill"
}
