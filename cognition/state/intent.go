package state

import (
	"context"
	"encoding/json"
	"regexp"
	"strings"
	"time"

	"github.com/ollama/ollama/api"
)

const (
	intentModelTimeout = 15 * time.Second
)

var (
	intentFastModels = []string{
		"llama3.2:1b",
		"qwen2.5:3b-instruct",
	}
	vaguePattern = regexp.MustCompile(`(?i)\b(that thing|do it|fix (the )?server|handle it|sort it|make it work|this issue|that issue|same as before|as usual)\b`)
	jsonBlockRE  = regexp.MustCompile(`(?s)\{.*\}`)
)

// MissionTask is a lightweight mission task view for intent normalization.
type MissionTask struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Status      string `json:"status"`
}

// MissionPlan is a lightweight mission context for intent normalization.
type MissionPlan struct {
	Goal               string        `json:"goal"`
	Tasks              []MissionTask `json:"tasks,omitempty"`
	ContextualClusters []string      `json:"contextual_clusters,omitempty"`
}

// IntentNormalizationResult is the output of mission-aware intent normalization.
type IntentNormalizationResult struct {
	CorrectedInput       string   `json:"corrected_input"`
	AmbiguityScore       float64  `json:"ambiguity_score"`
	ClarificationRequest string   `json:"clarification_request,omitempty"`
	VagueTokens          []string `json:"vague_tokens,omitempty"`
	MappedTaskIDs        []string `json:"mapped_task_ids,omitempty"`
	MappedClusters       []string `json:"mapped_clusters,omitempty"`
}

type intentModelResponse struct {
	CorrectedInput       string   `json:"corrected_input"`
	AmbiguityScore       float64  `json:"ambiguity_score"`
	ClarificationRequest string   `json:"clarification_request"`
	VagueTokens          []string `json:"vague_tokens"`
	MappedTaskIDs        []string `json:"mapped_task_ids"`
	MappedClusters       []string `json:"mapped_clusters"`
}

// NormalizeIntent rewrites raw input into high-signal instructions aligned to mission context.
// If ambiguity is high and unresolved (score < 0.3), ClarificationRequest is returned.
func NormalizeIntent(rawInput string, currentMission MissionPlan) IntentNormalizationResult {
	raw := strings.TrimSpace(rawInput)
	if raw == "" {
		return IntentNormalizationResult{CorrectedInput: raw, AmbiguityScore: 1.0}
	}

	if res, ok := normalizeWithFastModel(raw, currentMission); ok {
		return finalizeNormalization(raw, currentMission, res)
	}
	return finalizeNormalization(raw, currentMission, heuristicNormalize(raw, currentMission))
}

func normalizeWithFastModel(raw string, mission MissionPlan) (intentModelResponse, bool) {
	client, err := api.ClientFromEnvironment()
	if err != nil {
		return intentModelResponse{}, false
	}

	system := `You normalize user intent for a technical mission executor.
Return JSON only:
{
  "corrected_input":"...",
  "ambiguity_score":0.0,
  "clarification_request":"...",
  "vague_tokens":["..."],
  "mapped_task_ids":["..."],
  "mapped_clusters":["..."]
}
Rules:
1) ambiguity_score in [0,1], where 1 is very clear.
2) Map vague words ("that thing", "do it", "fix server") to mission tasks or contextual clusters.
3) If unresolved ambiguity remains and score < 0.3, provide a direct clarification_request question.
4) Keep corrected_input concise and technically actionable.`

	payload, _ := json.Marshal(map[string]interface{}{
		"raw_input": raw,
		"mission":   mission,
	})
	msgs := []api.Message{
		{Role: "system", Content: system},
		{Role: "user", Content: string(payload)},
	}

	for _, model := range intentFastModels {
		opts, _ := ResolveEntropyOptions(raw)
		req := &api.ChatRequest{Model: model, Messages: msgs, Options: opts}
		ctx, cancel := context.WithTimeout(context.Background(), intentModelTimeout)
		var out strings.Builder
		err := client.Chat(ctx, req, func(resp api.ChatResponse) error {
			out.WriteString(resp.Message.Content)
			return nil
		})
		cancel()
		if err != nil {
			continue
		}
		if parsed, ok := parseIntentModelResponse(out.String()); ok {
			return parsed, true
		}
	}
	return intentModelResponse{}, false
}

func parseIntentModelResponse(raw string) (intentModelResponse, bool) {
	trim := strings.TrimSpace(stripFences(raw))
	var out intentModelResponse
	if err := json.Unmarshal([]byte(trim), &out); err == nil {
		return out, true
	}
	block := jsonBlockRE.FindString(trim)
	if block == "" {
		return intentModelResponse{}, false
	}
	if err := json.Unmarshal([]byte(block), &out); err != nil {
		return intentModelResponse{}, false
	}
	return out, true
}

func heuristicNormalize(raw string, mission MissionPlan) intentModelResponse {
	lower := strings.ToLower(raw)
	vague := detectVagueTokens(lower)
	resp := intentModelResponse{
		CorrectedInput: raw,
		AmbiguityScore: 0.82,
		VagueTokens:    vague,
	}

	active := activeMissionTask(mission.Tasks)
	if len(vague) == 0 {
		return resp
	}

	if active != nil {
		resp.MappedTaskIDs = []string{active.ID}
		resp.CorrectedInput = "For mission '" + strings.TrimSpace(mission.Goal) + "', " + strings.TrimSpace(active.Description) + ". User request: " + raw
		resp.AmbiguityScore = 0.56
		return resp
	}
	if len(mission.ContextualClusters) > 0 {
		resp.MappedClusters = []string{mission.ContextualClusters[0]}
		resp.CorrectedInput = "Focus on cluster '" + mission.ContextualClusters[0] + "'. Resolve: " + raw
		resp.AmbiguityScore = 0.45
		return resp
	}

	resp.AmbiguityScore = 0.2
	resp.ClarificationRequest = "I can do that, but which specific task should I target right now?"
	return resp
}

func finalizeNormalization(raw string, mission MissionPlan, in intentModelResponse) IntentNormalizationResult {
	corrected := strings.TrimSpace(in.CorrectedInput)
	if corrected == "" {
		corrected = raw
	}
	score := clamp01(in.AmbiguityScore)
	clarify := strings.TrimSpace(in.ClarificationRequest)

	if score < 0.3 && clarify == "" {
		clarify = buildClarificationQuestion(mission)
	}
	if score >= 0.3 {
		clarify = ""
	}

	return IntentNormalizationResult{
		CorrectedInput:       corrected,
		AmbiguityScore:       score,
		ClarificationRequest: clarify,
		VagueTokens:          dedupeLower(in.VagueTokens),
		MappedTaskIDs:        dedupeLower(in.MappedTaskIDs),
		MappedClusters:       dedupePreserve(in.MappedClusters),
	}
}

func buildClarificationQuestion(mission MissionPlan) string {
	if t := activeMissionTask(mission.Tasks); t != nil {
		return "Do you mean task '" + t.Title + "' (" + t.ID + ")?"
	}
	if len(mission.Tasks) > 0 {
		return "Which mission task should I apply this to?"
	}
	if len(mission.ContextualClusters) > 0 {
		return "Which area should I target: " + strings.Join(mission.ContextualClusters[:minInt(3, len(mission.ContextualClusters))], ", ") + "?"
	}
	return "Can you clarify the specific target and expected outcome?"
}

func activeMissionTask(tasks []MissionTask) *MissionTask {
	for i := range tasks {
		if strings.EqualFold(strings.TrimSpace(tasks[i].Status), "active") {
			return &tasks[i]
		}
	}
	return nil
}

func detectVagueTokens(lower string) []string {
	matches := vaguePattern.FindAllString(lower, -1)
	return dedupeLower(matches)
}

func dedupeLower(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	out := make([]string, 0, len(in))
	seen := map[string]bool{}
	for _, v := range in {
		s := strings.ToLower(strings.TrimSpace(v))
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func dedupePreserve(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	out := make([]string, 0, len(in))
	seen := map[string]bool{}
	for _, v := range in {
		s := strings.TrimSpace(v)
		key := strings.ToLower(s)
		if s == "" || seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, s)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func stripFences(s string) string {
	trim := strings.TrimSpace(s)
	if !strings.HasPrefix(trim, "```") || !strings.HasSuffix(trim, "```") {
		return trim
	}
	lines := strings.Split(trim, "\n")
	if len(lines) < 3 {
		return trim
	}
	return strings.TrimSpace(strings.Join(lines[1:len(lines)-1], "\n"))
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
