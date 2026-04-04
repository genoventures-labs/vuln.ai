package output

import (
	"context"
	"encoding/json"
	"regexp"
	"strings"
	"time"

	"github.com/Thynaptic/P-LMv1/pkg/state"
	"github.com/ollama/ollama/api"
)

type ToneMicroBias string

const (
	ToneTechnical  ToneMicroBias = "Technical"
	ToneEmpathetic ToneMicroBias = "Empathetic"
	ToneSarcastic  ToneMicroBias = "Sarcastic"
	ToneConcise    ToneMicroBias = "Concise"
	ToneNeutral    ToneMicroBias = "Neutral"
)

type TonalSegment struct {
	Start int           `json:"start"`
	End   int           `json:"end"`
	Text  string        `json:"text"`
	Tone  ToneMicroBias `json:"tone_micro_bias"`
}

var (
	segmentSplitRE = regexp.MustCompile(`(?s)([^.!?\n;:]+[.!?\n;:]?)`)
	jsonRE         = regexp.MustCompile(`(?s)\{.*\}`)
)

// AnalyzeSubtext breaks generated text into tonal segments.
func AnalyzeSubtext(text string) []TonalSegment {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}
	matches := segmentSplitRE.FindAllStringIndex(text, -1)
	segments := make([]TonalSegment, 0, len(matches))
	for _, idx := range matches {
		if len(idx) != 2 || idx[0] >= idx[1] {
			continue
		}
		chunk := strings.TrimSpace(text[idx[0]:idx[1]])
		if chunk == "" {
			continue
		}
		segments = append(segments, TonalSegment{
			Start: idx[0],
			End:   idx[1],
			Text:  chunk,
			Tone:  inferSegmentTone(chunk),
		})
	}
	if len(segments) == 0 {
		return []TonalSegment{{Start: 0, End: len(text), Text: text, Tone: inferSegmentTone(text)}}
	}
	return segments
}

// ColorSegmentsWithSubtext colors segment tones based on user subtext markers.
func ColorSegmentsWithSubtext(segments []TonalSegment, markers []string) []TonalSegment {
	if len(segments) == 0 || len(markers) == 0 {
		return segments
	}
	has := map[string]bool{}
	for _, m := range markers {
		v := strings.ToLower(strings.TrimSpace(m))
		if v != "" {
			has[v] = true
		}
	}
	out := make([]TonalSegment, len(segments))
	copy(out, segments)
	for i := range out {
		switch {
		case has["fatigue"]:
			if out[i].Tone == ToneTechnical {
				out[i].Tone = ToneConcise
			}
		case has["vulnerability"]:
			if out[i].Tone == ToneTechnical || out[i].Tone == ToneNeutral {
				out[i].Tone = ToneEmpathetic
			}
		case has["sarcasm"]:
			if out[i].Tone == ToneNeutral {
				out[i].Tone = ToneSarcastic
			}
		}
	}
	return out
}

type promptSubtextResponse struct {
	Markers   []string `json:"markers"`
	MoodScore float64  `json:"mood_score"`
}

var promptSubtextModels = []string{"llama3.2:1b", "qwen2.5:3b-instruct"}

// DetectPromptSubtext performs a fast pass over the user prompt for sarcasm/fatigue/vulnerability.
func DetectPromptSubtext(prompt string) ([]string, float64) {
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		return nil, 0
	}
	if markers, score, ok := detectPromptSubtextWithModel(prompt); ok {
		return markers, score
	}
	return detectPromptSubtextHeuristic(prompt)
}

func detectPromptSubtextWithModel(prompt string) ([]string, float64, bool) {
	client, err := api.ClientFromEnvironment()
	if err != nil {
		return nil, 0, false
	}
	system := `Classify user prompt subtext.
Return JSON only:
{"markers":["sarcasm","vulnerability","fatigue"],"mood_score":-0.4}
Rules:
- markers allowed: sarcasm, vulnerability, fatigue.
- include only detected markers.
- mood_score in [-1,1], negative means strained tone.`
	msgs := []api.Message{
		{Role: "system", Content: system},
		{Role: "user", Content: prompt},
	}
	for _, model := range promptSubtextModels {
		opts, _ := state.ResolveEntropyOptions(prompt)
		req := &api.ChatRequest{Model: model, Messages: msgs, Options: opts}
		ctx, cancel := context.WithTimeout(context.Background(), 7*time.Second)
		var out strings.Builder
		err := client.Chat(ctx, req, func(resp api.ChatResponse) error {
			out.WriteString(resp.Message.Content)
			return nil
		})
		cancel()
		if err != nil {
			continue
		}
		parsed, ok := parsePromptSubtext(out.String())
		if !ok {
			continue
		}
		return sanitizePromptMarkers(parsed.Markers), clampSigned(parsed.MoodScore), true
	}
	return nil, 0, false
}

func parsePromptSubtext(raw string) (promptSubtextResponse, bool) {
	raw = strings.TrimSpace(stripFence(raw))
	var out promptSubtextResponse
	if err := json.Unmarshal([]byte(raw), &out); err == nil {
		return out, true
	}
	m := jsonRE.FindString(raw)
	if m == "" {
		return promptSubtextResponse{}, false
	}
	if err := json.Unmarshal([]byte(m), &out); err != nil {
		return promptSubtextResponse{}, false
	}
	return out, true
}

func detectPromptSubtextHeuristic(prompt string) ([]string, float64) {
	l := strings.ToLower(prompt)
	markers := []string{}
	score := 0.0
	if strings.Contains(l, "yeah right") || strings.Contains(l, "sure, because") || strings.Contains(l, "obviously") {
		markers = append(markers, "sarcasm")
		score -= 0.2
	}
	if strings.Contains(l, "i'm tired") || strings.Contains(l, "exhausted") || strings.Contains(l, "burned out") {
		markers = append(markers, "fatigue")
		score -= 0.35
	}
	if strings.Contains(l, "i'm worried") || strings.Contains(l, "i'm stuck") || strings.Contains(l, "not sure") {
		markers = append(markers, "vulnerability")
		score -= 0.2
	}
	return sanitizePromptMarkers(markers), clampSigned(score)
}

func sanitizePromptMarkers(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	allowed := map[string]bool{
		"sarcasm":       true,
		"vulnerability": true,
		"fatigue":       true,
	}
	seen := map[string]bool{}
	out := make([]string, 0, len(in))
	for _, v := range in {
		s := strings.ToLower(strings.TrimSpace(v))
		if !allowed[s] || seen[s] {
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

func inferSegmentTone(segment string) ToneMicroBias {
	s := strings.ToLower(strings.TrimSpace(segment))
	if s == "" {
		return ToneNeutral
	}
	switch {
	case strings.Contains(s, "however") || strings.Contains(s, "therefore") || strings.Contains(s, "because") || strings.Contains(s, "latency") || strings.Contains(s, "timeout") || strings.Contains(s, "config"):
		return ToneTechnical
	case strings.Contains(s, "i understand") || strings.Contains(s, "it makes sense") || strings.Contains(s, "thanks for") || strings.Contains(s, "we can"):
		return ToneEmpathetic
	case strings.Contains(s, "obviously") || strings.Contains(s, "sure") && strings.Contains(s, "?"):
		return ToneSarcastic
	case len(s) < 72:
		return ToneConcise
	default:
		return ToneNeutral
	}
}

func stripFence(s string) string {
	t := strings.TrimSpace(s)
	if !strings.HasPrefix(t, "```") || !strings.HasSuffix(t, "```") {
		return t
	}
	lines := strings.Split(t, "\n")
	if len(lines) < 3 {
		return t
	}
	return strings.TrimSpace(strings.Join(lines[1:len(lines)-1], "\n"))
}

func clampSigned(v float64) float64 {
	if v > 1 {
		return 1
	}
	if v < -1 {
		return -1
	}
	return v
}
