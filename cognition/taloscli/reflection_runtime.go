package taloscli

import (
	"fmt"
	"strings"

	"github.com/Thynaptic/P-LMv1/pkg/cognition"
	"github.com/Thynaptic/P-LMv1/pkg/state"
	"github.com/ollama/ollama/api"
)

func applyReflectionGate(
	client *api.Client,
	modelCandidates []string,
	stage cognition.ReflectionStage,
	query string,
	candidate string,
	sourceRefs []string,
	contextFacts []string,
	sm *state.Manager,
) (string, []string) {
	candidate = strings.TrimSpace(candidate)
	if candidate == "" {
		return candidate, nil
	}
	policy := cognition.DefaultReflectionPolicy("balanced")
	snapshot := state.SessionState{}
	goal := strings.TrimSpace(query)
	if sm != nil {
		snapshot = sm.GetSnapshot()
		if g := strings.TrimSpace(snapshot.PrimaryGoal); g != "" {
			goal = g
		}
	}
	decision, err := cognition.RunReflectionV2(cognition.ReflectionInput{
		Stage:        stage,
		Goal:         goal,
		Query:        strings.TrimSpace(query),
		Candidate:    candidate,
		SourceRefs:   append([]string(nil), sourceRefs...),
		ContextFacts: append([]string(nil), contextFacts...),
		Session:      snapshot,
	}, policy)
	if err != nil {
		return candidate, []string{"Reflection warning: " + err.Error()}
	}
	notes := []string{}
	if decision.Outcome == cognition.ReflectionPass {
		return candidate, notes
	}
	notes = append(notes, formatReflectionStatusLine(stage, decision))
	if !decision.NeedsCorrection || client == nil || len(modelCandidates) == 0 || policy.CorrectionMaxPass <= 0 {
		return candidate, notes
	}
	corrected, corrErr := runReflectionCorrectionPass(client, modelCandidates, stage, query, candidate, decision, sourceRefs, policy)
	if corrErr != nil || strings.TrimSpace(corrected) == "" {
		if strings.ToLower(policy.EnforcementMode) == "hard" && (decision.Outcome == cognition.ReflectionSteer || decision.Outcome == cognition.ReflectionVeto) {
			return reflectionFallbackText(stage), append(notes, "Reflection hard enforcement blocked unsafe output.")
		}
		return candidate, append(notes, "Reflection correction skipped: "+errorText(corrErr))
	}
	reeval, reErr := cognition.RunReflectionV2(cognition.ReflectionInput{
		Stage:        stage,
		Goal:         goal,
		Query:        strings.TrimSpace(query),
		Candidate:    corrected,
		SourceRefs:   append([]string(nil), sourceRefs...),
		ContextFacts: append([]string(nil), contextFacts...),
		Session:      snapshot,
		Metadata:     map[string]string{"reflection_pass": "post_correction"},
	}, policy)
	if reErr != nil {
		return corrected, append(notes, "Reflection re-eval warning: "+reErr.Error())
	}
	notes = append(notes, "Reflection correction pass completed: outcome="+string(reeval.Outcome))
	if reeval.Outcome == cognition.ReflectionVeto && strings.ToLower(policy.EnforcementMode) == "hard" {
		return reflectionFallbackText(stage), append(notes, "Reflection hard enforcement replaced output with conservative fallback.")
	}
	if reeval.Outcome == cognition.ReflectionSteer && strings.ToLower(policy.EnforcementMode) == "hard" {
		return reflectionFallbackText(stage), append(notes, "Reflection hard enforcement escalated steer to fallback.")
	}
	return corrected, notes
}

func runReflectionCorrectionPass(
	client *api.Client,
	modelCandidates []string,
	stage cognition.ReflectionStage,
	query string,
	candidate string,
	decision cognition.ReflectionDecision,
	sourceRefs []string,
	policy cognition.ReflectionPolicy,
) (string, error) {
	system := `You are a strict reflection correction node.
Repair the candidate output while preserving factual content.
Rules:
- Fix only flagged issues.
- Keep the answer concise and directly aligned to goal/query.
- Remove contradictory claims.
- If numbered sources exist, include [n] citations where relevant.
Return only corrected output.`
	var b strings.Builder
	b.WriteString("Stage:\n")
	b.WriteString(string(stage))
	b.WriteString("\n\nQuery:\n")
	b.WriteString(strings.TrimSpace(query))
	b.WriteString("\n\nCurrent candidate:\n")
	b.WriteString(strings.TrimSpace(candidate))
	if len(decision.Violations) > 0 {
		b.WriteString("\n\nViolations:\n- ")
		b.WriteString(strings.Join(decision.Violations, "\n- "))
	}
	if len(sourceRefs) > 0 {
		b.WriteString("\n\nAvailable sources:\n")
		for i, s := range sourceRefs {
			b.WriteString(fmt.Sprintf("[%d] %s\n", i+1, s))
		}
	}
	resp, _, err := callLLMWithFallbackWithTimeout(client, modelCandidates, []api.Message{
		{Role: "system", Content: system},
		{Role: "user", Content: b.String()},
	}, policy.Timeout)
	if err != nil {
		return "", err
	}
	out := sanitizeModelOutput(resp)
	if strings.TrimSpace(out) == "" {
		return "", fmt.Errorf("empty correction output")
	}
	return out, nil
}

func reflectionFallbackText(stage cognition.ReflectionStage) string {
	return "Reflection guardrail blocked this stage output due to elevated risk. Re-run with stronger evidence and citations. (stage=" + string(stage) + ")"
}

func formatReflectionStatusLine(stage cognition.ReflectionStage, d cognition.ReflectionDecision) string {
	return fmt.Sprintf("Reflection: stage=%s outcome=%s risk=%.2f", stage, d.Outcome, d.RiskScore)
}

func errorText(err error) string {
	if err == nil {
		return "unknown"
	}
	return err.Error()
}
