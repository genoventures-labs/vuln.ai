package skills

import (
	"context"
	"fmt"
	"strings"
)

type ExecutionRequest struct {
	Skill   SkillRecord
	Input   map[string]any
	Query   string
	Tools   []string
	Domains []string
	Chain   []SkillRecord
}

type ExecutionResult struct {
	Output       map[string]any
	Status       string
	PolicyNotes  []string
	DeniedReason string
}

func ExecuteWithPolicy(_ context.Context, req ExecutionRequest) (ExecutionResult, error) {
	return executeWithPolicyDepth(req, 0)
}

func executeWithPolicyDepth(req ExecutionRequest, depth int) (ExecutionResult, error) {
	if depth > 4 {
		return ExecutionResult{}, fmt.Errorf("maximum skill chain depth exceeded")
	}
	if strings.TrimSpace(req.Skill.SkillID) == "" {
		return ExecutionResult{}, fmt.Errorf("skill is required")
	}
	if len(req.Chain) >= 3 {
		chain := pickDistinctChainCandidates(req.Chain, 3)
		if len(chain) < 3 {
			return ExecutionResult{
				Status:       "denied",
				DeniedReason: "invalid super-skill chain candidates",
				PolicyNotes:  []string{"super-skill requires 3 distinct executable skills"},
			}, nil
		}
		results := make([]map[string]any, 0, len(chain))
		for i, sub := range chain {
			subReq := ExecutionRequest{
				Skill:   sub,
				Input:   req.Input,
				Query:   req.Query,
				Tools:   req.Tools,
				Domains: req.Domains,
			}
			subRes, err := executeWithPolicyDepth(subReq, depth+1)
			if err != nil {
				return ExecutionResult{}, err
			}
			entry := map[string]any{
				"index":     i + 1,
				"skill_id":  strings.TrimSpace(sub.SkillID),
				"revision":  strings.TrimSpace(sub.RevisionID),
				"name":      strings.TrimSpace(sub.Name),
				"status":    strings.TrimSpace(subRes.Status),
				"policy":    append([]string(nil), subRes.PolicyNotes...),
				"denied":    strings.TrimSpace(subRes.DeniedReason),
				"skill_out": subRes.Output,
			}
			results = append(results, entry)
			if strings.EqualFold(strings.TrimSpace(subRes.Status), "denied") {
				return ExecutionResult{
					Status:       "denied",
					DeniedReason: "super-skill chain blocked by child policy",
					PolicyNotes:  []string{fmt.Sprintf("child skill %d denied", i+1)},
					Output: map[string]any{
						"super_skill":   true,
						"chain_count":   len(chain),
						"chain_results": results,
					},
				}, nil
			}
		}
		query := strings.TrimSpace(req.Query)
		if query == "" {
			query = strings.TrimSpace(stringVal(req.Input["query"]))
		}
		return ExecutionResult{
			Status: "ok",
			Output: map[string]any{
				"skill_id":      req.Skill.SkillID,
				"revision":      req.Skill.RevisionID,
				"status":        "executed",
				"query":         query,
				"intent":        req.Skill.Intent,
				"super_skill":   true,
				"chain_count":   len(chain),
				"chain_results": results,
			},
		}, nil
	}
	if strings.EqualFold(strings.TrimSpace(req.Skill.Status), SkillStatusRevoked) || strings.EqualFold(strings.TrimSpace(req.Skill.Status), SkillStatusDeprecated) {
		return ExecutionResult{Status: "denied", DeniedReason: "skill status not executable", PolicyNotes: []string{"skill status must be active/validated"}}, nil
	}
	violations := policyViolations(req.Skill, req.Tools, req.Domains)
	if len(violations) > 0 {
		return ExecutionResult{
			Status:       "denied",
			DeniedReason: "capability policy violation",
			PolicyNotes:  violations,
			Output: map[string]any{
				"fallback": "Skill execution denied by policy; continue with standard TALOS reasoning path.",
			},
		}, nil
	}
	query := strings.TrimSpace(req.Query)
	if query == "" {
		query = strings.TrimSpace(stringVal(req.Input["query"]))
	}
	return ExecutionResult{
		Status: "ok",
		Output: map[string]any{
			"skill_id": req.Skill.SkillID,
			"revision": req.Skill.RevisionID,
			"status":   "executed",
			"query":    query,
			"intent":   req.Skill.Intent,
		},
	}, nil
}

func policyViolations(skill SkillRecord, tools []string, domains []string) []string {
	violations := []string{}
	if len(skill.AllowedTools) > 0 {
		allowed := map[string]bool{}
		for _, t := range skill.AllowedTools {
			allowed[strings.ToLower(strings.TrimSpace(t))] = true
		}
		for _, t := range tools {
			if !allowed[strings.ToLower(strings.TrimSpace(t))] {
				violations = append(violations, "tool denied: "+strings.TrimSpace(t))
			}
		}
	}
	if len(skill.AllowedDomains) > 0 {
		allowed := map[string]bool{}
		for _, d := range skill.AllowedDomains {
			allowed[strings.ToLower(strings.TrimSpace(d))] = true
		}
		for _, d := range domains {
			if !allowed[strings.ToLower(strings.TrimSpace(d))] {
				violations = append(violations, "domain denied: "+strings.TrimSpace(d))
			}
		}
	}
	return violations
}
