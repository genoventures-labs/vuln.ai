package taloscli

import (
	"fmt"
	"strings"
	"time"

	"github.com/Thynaptic/P-LMv1/pkg/cognition"
	"github.com/Thynaptic/P-LMv1/pkg/memory"
	"github.com/Thynaptic/P-LMv1/pkg/state"
)

// IntentEnvelope is the normalized intent payload passed into module pipelines.
type IntentEnvelope struct {
	Raw           string
	Normalized    string
	Ambiguity     float64
	Clarification string
	GoalMappings  []string
	CommandHints  []string
}

// NormalizeForModule performs mission-aware normalization and returns a module-safe intent envelope.
func NormalizeForModule(raw string, sm *state.Manager, mm *memory.MemoryManager, module string) (IntentEnvelope, error) {
	raw = strings.TrimSpace(raw)
	module = strings.ToLower(strings.TrimSpace(module))
	env := IntentEnvelope{
		Raw:        raw,
		Normalized: raw,
		Ambiguity:  1.0,
	}
	if raw == "" {
		return env, nil
	}

	mission := buildIntentMissionContext(mm, raw)
	missionIntent := state.NormalizeIntent(raw, mission)
	env.Ambiguity = missionIntent.AmbiguityScore
	env.Clarification = strings.TrimSpace(missionIntent.ClarificationRequest)
	env.GoalMappings = append(env.GoalMappings, missionIntent.MappedTaskIDs...)
	env.GoalMappings = append(env.GoalMappings, missionIntent.MappedClusters...)
	seed := strings.TrimSpace(missionIntent.CorrectedInput)
	if seed == "" {
		seed = raw
	}
	env.Normalized = seed

	if env.Clarification != "" {
		if sm != nil {
			sm.UpdateDelta(map[string]float64{
				"Frustration": 0.04,
				"Confidence":  -0.03,
			})
			_ = sm.Save()
		}
		return env, nil
	}

	if sm != nil {
		current := sm.GetSnapshot()
		normalized, delta, subtext, moodScore := cognition.NormalizeIntent(seed, current)
		normalized = strings.TrimSpace(normalized)
		if normalized != "" {
			env.Normalized = normalized
		}
		if len(delta) > 0 {
			sm.UpdateDelta(delta)
		}
		if moodScore != 0 {
			sm.UpdateState(moodScore)
		}
		if len(subtext) > 0 || moodScore != 0 {
			sm.IngestSubtext(subtext, moodScore)
		}
		_ = sm.Save()
	}

	env.CommandHints = moduleCommandHints(module, env.Normalized)
	env.Normalized = normalizeModuleIntent(module, env.Normalized)
	if strings.TrimSpace(env.Normalized) == "" {
		return env, fmt.Errorf("normalized intent is empty")
	}
	return env, nil
}

// preprocessUserIntent is the shared user-intent middleware for chat/research entrypoints.
func preprocessUserIntent(raw string, sm *state.Manager, mm *memory.MemoryManager, module string) (IntentEnvelope, bool, string) {
	raw = strings.TrimSpace(raw)
	module = strings.ToLower(strings.TrimSpace(module))
	env := IntentEnvelope{
		Raw:        raw,
		Normalized: raw,
		Ambiguity:  1.0,
	}
	if raw == "" {
		return env, false, ""
	}
	if isTrivialPrompt(raw) && module == "chat" {
		return env, true, ""
	}

	timeout := durationFromEnv("PLM_INTENT_CORRECTION_TIMEOUT", 4*time.Second)
	type result struct {
		env IntentEnvelope
		err error
	}
	done := make(chan result, 1)
	go func() {
		out, err := NormalizeForModule(raw, sm, mm, module)
		done <- result{env: out, err: err}
	}()

	select {
	case r := <-done:
		if r.err != nil {
			return env, true, ""
		}
		if strings.TrimSpace(r.env.Clarification) != "" {
			return r.env, false, r.env.Clarification
		}
		if sm != nil && strings.TrimSpace(r.env.Normalized) != "" {
			locked, note := applyPersistentGoalLock(sm, r.env.Normalized, module)
			r.env.Normalized = strings.TrimSpace(locked)
			if strings.TrimSpace(note) != "" {
				fmt.Printf("DEBUG: %s\n", strings.TrimSpace(note))
			}
		}
		return r.env, true, ""
	case <-time.After(timeout):
		fmt.Printf("DEBUG: Intent correction timed out after %s; continuing with raw prompt.\n", timeout)
		return env, true, ""
	}
}

func moduleCommandHints(module, normalized string) []string {
	module = strings.ToLower(strings.TrimSpace(module))
	n := strings.ToLower(strings.TrimSpace(normalized))
	if n == "" {
		return nil
	}
	var out []string
	switch module {
	case "research":
		if strings.Contains(n, "compare") || strings.Contains(n, "tradeoff") || strings.Contains(n, "across docs") {
			out = append(out, "multi_document_analysis")
		}
		if strings.Contains(n, "verify") || strings.Contains(n, "citation") || strings.Contains(n, "source") {
			out = append(out, "citation_priority")
		}
	case "chat":
		if strings.Contains(n, "explain") || strings.Contains(n, "why") {
			out = append(out, "reasoning_response")
		}
		if strings.Contains(n, "search") || strings.Contains(n, "fetch") {
			out = append(out, "tool_candidate")
		}
	}
	return out
}

func normalizeModuleIntent(module, normalized string) string {
	module = strings.ToLower(strings.TrimSpace(module))
	normalized = strings.TrimSpace(normalized)
	if normalized == "" {
		return normalized
	}
	lower := strings.ToLower(normalized)
	switch module {
	case "research":
		switch {
		case lower == "research this" || lower == "look into this" || lower == "check this":
			return "Investigate the target topic with cited sources, then return key findings, risks, and next actions."
		case strings.HasPrefix(lower, "research "):
			return "Research objective: " + strings.TrimSpace(normalized[len("research "):])
		}
	case "chat":
		if lower == "fix it" || lower == "do it" {
			return "Provide a concrete implementation path for the active goal, including steps and risks."
		}
	}
	return normalized
}
