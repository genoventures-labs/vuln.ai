package taloscli

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

const (
	goalDecompV2EnabledEnv         = "TALOS_GOAL_DECOMP_V2_ENABLED"
	goalDecompV2TargetStepsEnv     = "TALOS_GOAL_DECOMP_V2_TARGET_STEPS"
	goalDecompV2TargetStepsDefault = 10
)

type taskTreeNode struct {
	Title    string
	Children []taskTreeNode
}

func goalDecompositionV2Enabled() bool {
	return envBoolDefault(goalDecompV2EnabledEnv, true)
}

func goalDecompositionV2TargetSteps() int {
	raw := strings.TrimSpace(os.Getenv(goalDecompV2TargetStepsEnv))
	if raw == "" {
		return goalDecompV2TargetStepsDefault
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 {
		return goalDecompV2TargetStepsDefault
	}
	if n > 16 {
		return 16
	}
	return n
}

func maybeApplyGoalDecompositionV2(query string, parsed []string, maxSteps int) ([]string, bool) {
	parsed = clampSteps(parsed, maxSteps)
	if !goalDecompositionV2Enabled() {
		return parsed, false
	}
	target := goalDecompositionV2TargetSteps()
	if maxSteps > 0 && target > maxSteps {
		target = maxSteps
	}
	if target <= 0 {
		target = goalDecompV2TargetStepsDefault
	}
	if len(parsed) >= target {
		return parsed, false
	}
	if len(parsed) >= 6 && !isHighLevelIntent(query) {
		return parsed, false
	}
	tree := buildGoalTaskTree(query)
	expanded := flattenTaskTree(tree, target)
	if len(expanded) == 0 {
		return parsed, false
	}
	return expanded, true
}

func isHighLevelIntent(query string) bool {
	l := strings.ToLower(strings.TrimSpace(query))
	if l == "" {
		return false
	}
	for _, token := range []string{
		"find all", "audit", "analyze", "assess", "review", "security spec",
		"architecture", "vulnerabilit", "compliance", "risk", "investigate",
	} {
		if strings.Contains(l, token) {
			return true
		}
	}
	return len(strings.Fields(l)) >= 6
}

func buildGoalTaskTree(query string) taskTreeNode {
	q := strings.TrimSpace(query)
	if q == "" {
		q = "Investigate target objective"
	}
	l := strings.ToLower(q)
	if strings.Contains(l, "vulnerab") || strings.Contains(l, "security") || strings.Contains(l, "threat") {
		return taskTreeNode{
			Title: fmt.Sprintf("Goal: %s", q),
			Children: []taskTreeNode{
				{Title: "Frame scope and baseline assumptions", Children: []taskTreeNode{
					{Title: "Extract scope boundaries, trust zones, and critical assets from the request."},
					{Title: "Collect baseline docs/specs and normalize terminology for consistent analysis."},
				}},
				{Title: "Map attack surface", Children: []taskTreeNode{
					{Title: "Enumerate architecture components, data flows, and privileged execution paths."},
					{Title: "Identify exposed interfaces and cross-module trust boundaries."},
				}},
				{Title: "Run vulnerability sweeps", Children: []taskTreeNode{
					{Title: "Check authn/authz, secrets handling, and identity boundary weaknesses."},
					{Title: "Check input validation, injection, deserialization, and unsafe execution paths."},
				}},
				{Title: "Validate findings", Children: []taskTreeNode{
					{Title: "Cross-verify candidate vulnerabilities against logs, configs, and implementation evidence."},
					{Title: "Rate exploitability/impact and remove weak or contradictory claims."},
				}},
				{Title: "Produce action plan", Children: []taskTreeNode{
					{Title: "Prioritize remediations with owner, effort, and risk-reduction rationale."},
					{Title: "Define follow-up verification checks and residual-risk tracking."},
				}},
			},
		}
	}
	return taskTreeNode{
		Title: fmt.Sprintf("Goal: %s", q),
		Children: []taskTreeNode{
			{Title: "Define objective and constraints", Children: []taskTreeNode{
				{Title: "Clarify the concrete success criteria and non-goals for the objective."},
				{Title: "Gather baseline context, sources, and known constraints relevant to execution."},
			}},
			{Title: "Build evidence map", Children: []taskTreeNode{
				{Title: "Identify key entities/modules and map relationships required for the task."},
				{Title: "Collect and tag high-signal source material by reliability and freshness."},
			}},
			{Title: "Run focused analysis branches", Children: []taskTreeNode{
				{Title: "Analyze branch A assumptions and expected outcomes with supporting evidence."},
				{Title: "Analyze branch B alternatives and contradictions against known context."},
			}},
			{Title: "Reconcile conflicts", Children: []taskTreeNode{
				{Title: "Resolve conflicting claims with weighted evidence and explicit rationale."},
				{Title: "Update the working worldview and isolate unresolved risks."},
			}},
			{Title: "Deliver execution plan", Children: []taskTreeNode{
				{Title: "Produce prioritized actions, dependencies, and verification checkpoints."},
				{Title: "Define monitoring signals and a follow-up loop for continuous refinement."},
			}},
		},
	}
}

func flattenTaskTree(root taskTreeNode, limit int) []string {
	if limit <= 0 {
		return nil
	}
	out := make([]string, 0, limit)
	var walk func(n taskTreeNode)
	walk = func(n taskTreeNode) {
		if len(out) >= limit {
			return
		}
		if len(n.Children) == 0 {
			title := strings.TrimSpace(n.Title)
			if title != "" {
				out = append(out, title)
			}
			return
		}
		for _, c := range n.Children {
			walk(c)
			if len(out) >= limit {
				return
			}
		}
	}
	walk(root)
	return out
}
