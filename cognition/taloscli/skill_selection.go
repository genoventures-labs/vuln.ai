package taloscli

import (
	"fmt"
	"os"
	"strings"

	"github.com/Thynaptic/P-LMv1/pkg/skills"
)

func resolveRequestedSkillSelection(query string) (*skills.SkillRecord, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, nil
	}
	reg := skills.NewSkillRegistry(skills.PermanentSkillsRoot())
	if strings.EqualFold(strings.TrimSpace(os.Getenv("TALOS_SKILLS_LEGACY_FALLBACK")), "true") {
		return resolveRequestedSkillSelectionLegacy(reg, query)
	}
	recs, err := reg.ListEnabled()
	if err != nil {
		return nil, fmt.Errorf("load skills registry: %w", err)
	}
	if recs, err = filterNamespaceAllowedSkills(recs); err != nil {
		return nil, err
	}
	if len(recs) == 0 {
		if ns := activeRuntimeNamespace(); ns != "" {
			return nil, fmt.Errorf("no skills are bound in active namespace %q", ns)
		}
		return nil, fmt.Errorf("no enabled skills are available")
	}

	qLower := strings.ToLower(strings.TrimSpace(query))
	for _, rec := range recs {
		if strings.EqualFold(strings.TrimSpace(rec.SkillID), query) || strings.EqualFold(strings.TrimSpace(rec.Name), query) {
			if active, ok, err := reg.ResolveActive(rec.SkillID); err == nil && ok && active != nil {
				return active, nil
			}
			out := rec
			return &out, nil
		}
		if strings.Contains(strings.ToLower(strings.TrimSpace(rec.ManifestPath)), qLower) ||
			strings.Contains(strings.ToLower(strings.TrimSpace(rec.RootDir)), qLower) {
			if active, ok, err := reg.ResolveActive(rec.SkillID); err == nil && ok && active != nil {
				return active, nil
			}
			out := rec
			return &out, nil
		}
	}
	decision, err := skills.RouteSkill(reg, skills.RouteRequest{
		Query:     query,
		HintSkill: query,
	})
	if err == nil && decision.Chosen != nil {
		return decision.Chosen, nil
	}
	if match, ok, err := reg.FindMatch(query, query, ""); err == nil && ok && match != nil {
		if active, aok, aerr := reg.ResolveActive(match.SkillID); aerr == nil && aok && active != nil {
			return active, nil
		}
		return match, nil
	}
	if err != nil {
		return nil, fmt.Errorf("match skill: %w", err)
	}
	return nil, fmt.Errorf("skill not found: %s", query)
}

func resolveRequestedSkillSelectionLegacy(reg *skills.SkillRegistry, query string) (*skills.SkillRecord, error) {
	recs, err := reg.ListEnabled()
	if err != nil {
		return nil, fmt.Errorf("load skills registry: %w", err)
	}
	if recs, err = filterNamespaceAllowedSkills(recs); err != nil {
		return nil, err
	}
	if len(recs) == 0 {
		if ns := activeRuntimeNamespace(); ns != "" {
			return nil, fmt.Errorf("no skills are bound in active namespace %q", ns)
		}
		return nil, fmt.Errorf("no enabled skills are available")
	}
	qLower := strings.ToLower(strings.TrimSpace(query))
	for i := range recs {
		rec := recs[i]
		if strings.EqualFold(strings.TrimSpace(rec.SkillID), query) || strings.EqualFold(strings.TrimSpace(rec.Name), query) {
			out := rec
			return &out, nil
		}
		if strings.Contains(strings.ToLower(strings.TrimSpace(rec.ManifestPath)), qLower) ||
			strings.Contains(strings.ToLower(strings.TrimSpace(rec.RootDir)), qLower) {
			out := rec
			return &out, nil
		}
	}
	if match, ok, err := reg.FindMatch(query, query, ""); err == nil && ok && match != nil {
		return match, nil
	}
	if err != nil {
		return nil, fmt.Errorf("match skill: %w", err)
	}
	return nil, fmt.Errorf("skill not found: %s", query)
}

func filterNamespaceAllowedSkills(recs []skills.SkillRecord) ([]skills.SkillRecord, error) {
	ns := activeRuntimeNamespace()
	if ns == "" {
		return recs, nil
	}
	store, err := namespaceCapabilityStoreFactory()
	if err != nil {
		return nil, fmt.Errorf("load namespace capability policy: %w", err)
	}
	out := make([]skills.SkillRecord, 0, len(recs))
	for _, rec := range recs {
		id := strings.TrimSpace(rec.SkillID)
		if id == "" {
			continue
		}
		if store.IsSkillAllowed(ns, id) {
			out = append(out, rec)
		}
	}
	return out, nil
}

func skillContextBlock(rec *skills.SkillRecord) string {
	if rec == nil {
		return ""
	}
	var b strings.Builder
	b.WriteString("Active skill profile:\n")
	if strings.TrimSpace(rec.SkillID) != "" {
		b.WriteString("- skill_id: " + strings.TrimSpace(rec.SkillID) + "\n")
	}
	if strings.TrimSpace(rec.RevisionID) != "" {
		b.WriteString("- revision_id: " + strings.TrimSpace(rec.RevisionID) + "\n")
	}
	if strings.TrimSpace(rec.Version) != "" {
		b.WriteString("- version: " + strings.TrimSpace(rec.Version) + "\n")
	}
	if strings.TrimSpace(rec.Status) != "" {
		b.WriteString("- status: " + strings.TrimSpace(rec.Status) + "\n")
	}
	if strings.TrimSpace(rec.Name) != "" {
		b.WriteString("- name: " + strings.TrimSpace(rec.Name) + "\n")
	}
	if strings.TrimSpace(rec.Intent) != "" {
		b.WriteString("- intent: " + strings.TrimSpace(rec.Intent) + "\n")
	}
	if strings.TrimSpace(rec.Description) != "" {
		b.WriteString("- description: " + strings.TrimSpace(rec.Description) + "\n")
	}
	if strings.TrimSpace(rec.ReasoningTier) != "" {
		b.WriteString("- reasoning_tier: " + strings.TrimSpace(rec.ReasoningTier) + "\n")
	}
	if strings.TrimSpace(rec.TaskType) != "" {
		b.WriteString("- task_type: " + strings.TrimSpace(rec.TaskType) + "\n")
	}
	b.WriteString("- directive: Prioritize this skill's intent and approach while answering.\n")
	return strings.TrimSpace(b.String())
}
