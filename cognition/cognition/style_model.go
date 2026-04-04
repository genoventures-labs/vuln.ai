package cognition

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/ollama/ollama/api"
)

type StyleModelScorer interface {
	Score(ctx context.Context, query string, baseline StyleProfile) (StyleProfile, error)
}

type defaultStyleScorer struct {
	models []string
}

func newDefaultStyleScorer() StyleModelScorer {
	return &defaultStyleScorer{
		models: []string{"llama3.2:1b", "qwen2.5:3b-instruct"},
	}
}

type styleModelResponse struct {
	Tone      string  `json:"tone"`
	Structure string  `json:"structure"`
	Density   float64 `json:"density"`
}

func (s *defaultStyleScorer) Score(ctx context.Context, query string, baseline StyleProfile) (StyleProfile, error) {
	client, err := api.ClientFromEnvironment()
	if err != nil {
		return StyleProfile{}, err
	}
	system := `Return JSON only.
Adjust style profile with minimal drift from baseline:
{"tone":"direct_technical|calm_clarifying|supportive|energetic_solution","structure":"table_first|bullet_first|narrative_first","density":1.0}
Rules:
- Keep density in [0.60,1.40].
- Do not optimize for verbosity inflation.
- No prose, JSON only.`
	user := fmt.Sprintf("Query:\n%s\n\nBaseline:\nmode=%s tone=%s structure=%s density=%.2f verbosity=%d",
		strings.TrimSpace(query),
		baseline.Mode,
		baseline.Tone,
		baseline.Structure,
		baseline.Density,
		baseline.VerbosityTarget,
	)
	msgs := []api.Message{
		{Role: "system", Content: system},
		{Role: "user", Content: user},
	}
	var lastErr error
	for _, model := range s.models {
		req := &api.ChatRequest{
			Model:    model,
			Messages: msgs,
			Options: map[string]any{
				"temperature": 0.1,
				"top_p":       0.8,
			},
		}
		callCtx := ctx
		if callCtx == nil {
			t := clampIntStyle(envIntStyle("TALOS_STYLE_MODEL_TIMEOUT_MS", 120), 60, 600)
			var cancel context.CancelFunc
			callCtx, cancel = context.WithTimeout(context.Background(), time.Duration(t)*time.Millisecond)
			defer cancel()
		}
		var out strings.Builder
		err := client.Chat(callCtx, req, func(resp api.ChatResponse) error {
			out.WriteString(resp.Message.Content)
			return nil
		})
		if err != nil {
			lastErr = err
			continue
		}
		parsed, ok := parseStyleModelResponse(out.String())
		if !ok {
			lastErr = fmt.Errorf("invalid style model output")
			continue
		}
		refined := baseline
		if parsed.Tone != "" {
			refined.Tone = parsed.Tone
		}
		if parsed.Structure != "" {
			refined.Structure = parsed.Structure
		}
		if parsed.Density > 0 {
			refined.Density = parsed.Density
		}
		refined.Density = clampRangeStyle(refined.Density, 0.60, 1.40)
		refined.FromModel = true
		return refined, nil
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("no style models available")
	}
	return StyleProfile{}, lastErr
}

var styleJSONRE = regexp.MustCompile(`(?s)\{.*\}`)

func parseStyleModelResponse(raw string) (styleModelResponse, bool) {
	trimmed := strings.TrimSpace(raw)
	if strings.HasPrefix(trimmed, "```") {
		lines := strings.Split(trimmed, "\n")
		if len(lines) > 2 {
			trimmed = strings.TrimSpace(strings.Join(lines[1:len(lines)-1], "\n"))
		}
	}
	var out styleModelResponse
	if err := json.Unmarshal([]byte(trimmed), &out); err != nil {
		match := styleJSONRE.FindString(trimmed)
		if match == "" {
			return styleModelResponse{}, false
		}
		if err := json.Unmarshal([]byte(match), &out); err != nil {
			return styleModelResponse{}, false
		}
	}
	out.Tone = sanitizeTone(out.Tone)
	out.Structure = sanitizeStructure(out.Structure)
	out.Density = clampRangeStyle(out.Density, 0.60, 1.40)
	return out, true
}

func sanitizeTone(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "direct_technical":
		return "direct_technical"
	case "calm_clarifying":
		return "calm_clarifying"
	case "supportive":
		return "supportive"
	case "energetic_solution":
		return "energetic_solution"
	default:
		return ""
	}
}

func sanitizeStructure(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "table_first":
		return "table_first"
	case "bullet_first":
		return "bullet_first"
	case "narrative_first":
		return "narrative_first"
	default:
		return ""
	}
}
