package skills

import (
	"os"
	"sort"
	"strconv"
	"strings"
)

type RouteRequest struct {
	Query         string
	TaskType      string
	ReasoningTier string
	HintSkill     string
}

type RouteCandidate struct {
	Record     SkillRecord
	Score      float64
	Confidence float64
	Reasons    []string
}

type RouteDecision struct {
	Chosen         *SkillRecord
	Score          float64
	Confidence     float64
	Candidates     []RouteCandidate
	FallbackReason string
}

func RouteSkill(reg *SkillRegistry, req RouteRequest) (RouteDecision, error) {
	if reg == nil {
		reg = NewSkillRegistry("")
	}
	recs, err := reg.ListEnabled()
	if err != nil {
		return RouteDecision{}, err
	}
	if len(recs) == 0 {
		return RouteDecision{FallbackReason: "no enabled skills"}, nil
	}
	minConf := envFloatSkill("TALOS_SKILLS_ROUTER_MIN_CONFIDENCE", 0.45)
	cand := make([]RouteCandidate, 0, len(recs))
	for _, rec := range recs {
		score, reasons := scoreSkill(rec, req)
		cand = append(cand, RouteCandidate{Record: rec, Score: score, Confidence: score, Reasons: reasons})
	}
	sort.SliceStable(cand, func(i, j int) bool {
		if cand[i].Score == cand[j].Score {
			return cand[i].Record.UpdatedAt.After(cand[j].Record.UpdatedAt)
		}
		return cand[i].Score > cand[j].Score
	})
	d := RouteDecision{Candidates: cand}
	if len(cand) == 0 {
		d.FallbackReason = "no route candidates"
		return d, nil
	}
	top := cand[0]
	d.Score = top.Score
	d.Confidence = top.Confidence
	if top.Confidence < minConf {
		d.FallbackReason = "low routing confidence"
		return d, nil
	}
	r := top.Record
	d.Chosen = &r
	return d, nil
}

func scoreSkill(rec SkillRecord, req RouteRequest) (float64, []string) {
	score := 0.0
	reasons := make([]string, 0, 4)
	q := strings.ToLower(strings.TrimSpace(req.Query))
	intent := strings.ToLower(strings.TrimSpace(rec.Intent))
	name := strings.ToLower(strings.TrimSpace(rec.Name))
	task := strings.ToLower(strings.TrimSpace(req.TaskType))
	recTask := strings.ToLower(strings.TrimSpace(rec.TaskType))
	tier := strings.ToLower(strings.TrimSpace(req.ReasoningTier))
	recTier := strings.ToLower(strings.TrimSpace(rec.ReasoningTier))
	hint := strings.ToLower(strings.TrimSpace(req.HintSkill))
	if hint != "" && (strings.EqualFold(hint, rec.SkillID) || strings.EqualFold(hint, rec.Name)) {
		score += 0.35
		reasons = append(reasons, "explicit skill hint matched")
	}
	if task != "" && recTask != "" && task == recTask {
		score += 0.20
		reasons = append(reasons, "task type matched")
	}
	if tier != "" && recTier != "" && tier == recTier {
		score += 0.10
		reasons = append(reasons, "reasoning tier matched")
	}
	if name != "" && strings.Contains(q, name) {
		score += 0.15
		reasons = append(reasons, "query references skill name")
	}
	ov := tokenOverlapScore(q, intent)
	if ov > 0 {
		bonus := 0.03 * float64(ov)
		if bonus > 0.25 {
			bonus = 0.25
		}
		score += bonus
		reasons = append(reasons, "intent token overlap")
	}
	if rec.Active {
		score += 0.05
		reasons = append(reasons, "active revision")
	}
	if strings.EqualFold(strings.TrimSpace(rec.Status), SkillStatusValidated) || strings.EqualFold(strings.TrimSpace(rec.Status), SkillStatusActive) {
		score += 0.05
		reasons = append(reasons, "validated/active status")
	}
	if score < 0 {
		score = 0
	}
	if score > 1 {
		score = 1
	}
	return score, reasons
}

func envFloatSkill(key string, fallback float64) float64 {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback
	}
	v, err := strconv.ParseFloat(raw, 64)
	if err != nil || v < 0 || v > 1 {
		return fallback
	}
	return v
}
