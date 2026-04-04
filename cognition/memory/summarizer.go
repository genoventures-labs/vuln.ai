package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/ollama/ollama/api"
)

const (
	defaultSummaryMaxChars  = 900
	defaultSummaryMaxPoints = 5
)

type SourceSummaryConfig struct {
	MaxChars  int
	MaxPoints int
	Model     string
}

type SourceSummaryRequest struct {
	SourceType string
	SourceRef  string
	Content    string
	SessionID  string
	Metadata   map[string]string
	ChunkCount int
}

type SourceSummaryResult struct {
	SummaryText string
	ModelUsed   string
	Fallback    bool
}

// SourceSummarizer produces compact source summaries for ingest-time enrichment.
type SourceSummarizer struct {
	mm  *MemoryManager
	cfg SourceSummaryConfig
}

func NewSourceSummarizer(mm *MemoryManager, cfg SourceSummaryConfig) *SourceSummarizer {
	if cfg.MaxChars <= 0 {
		cfg.MaxChars = defaultSummaryMaxChars
	}
	if cfg.MaxPoints <= 0 {
		cfg.MaxPoints = defaultSummaryMaxPoints
	}
	if cfg.MaxPoints > 8 {
		cfg.MaxPoints = 8
	}
	return &SourceSummarizer{mm: mm, cfg: cfg}
}

func (s *SourceSummarizer) Summarize(ctx context.Context, req SourceSummaryRequest) (SourceSummaryResult, error) {
	if s == nil {
		return SourceSummaryResult{}, fmt.Errorf("source summarizer is nil")
	}
	if strings.TrimSpace(req.Content) == "" {
		return SourceSummaryResult{}, fmt.Errorf("source summary content is empty")
	}
	if model := strings.TrimSpace(s.cfg.Model); model != "" && s.mm != nil && s.mm.client != nil {
		if out, err := s.modelSummary(ctx, model, req.Content); err == nil && strings.TrimSpace(out) != "" {
			return SourceSummaryResult{
				SummaryText: truncateSummary(out, s.cfg.MaxChars),
				ModelUsed:   model,
				Fallback:    false,
			}, nil
		}
	}
	return SourceSummaryResult{
		SummaryText: truncateSummary(s.fallbackSummary(req), s.cfg.MaxChars),
		Fallback:    true,
	}, nil
}

func (s *SourceSummarizer) modelSummary(ctx context.Context, model string, content string) (string, error) {
	if s.mm == nil || s.mm.client == nil {
		return "", fmt.Errorf("memory manager client unavailable")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	system := `You summarize ingested sources for retrieval memory.
Return JSON only with this schema:
{"gist":"...","key_points":["..."],"risk_uncertainty":"..."}
Rules:
- Keep concise and factual.
- key_points must contain 3 to 6 bullets.
- risk_uncertainty can be "none" when no risk.`

	user := "Source content:\n" + content
	req := &api.ChatRequest{
		Model: model,
		Messages: []api.Message{
			{Role: "system", Content: system},
			{Role: "user", Content: user},
		},
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	var out strings.Builder
	if err := s.mm.client.Chat(ctx, req, func(resp api.ChatResponse) error {
		out.WriteString(resp.Message.Content)
		return nil
	}); err != nil {
		return "", err
	}
	payload := strings.TrimSpace(stripCodeFence(out.String()))
	if payload == "" {
		return "", fmt.Errorf("empty summary response")
	}
	type summaryPayload struct {
		Gist            string   `json:"gist"`
		KeyPoints       []string `json:"key_points"`
		RiskUncertainty string   `json:"risk_uncertainty"`
	}
	var parsed summaryPayload
	if err := json.Unmarshal([]byte(payload), &parsed); err != nil {
		start := strings.Index(payload, "{")
		end := strings.LastIndex(payload, "}")
		if start >= 0 && end > start {
			if err2 := json.Unmarshal([]byte(payload[start:end+1]), &parsed); err2 != nil {
				return "", err
			}
		} else {
			return "", err
		}
	}
	return renderStructuredBrief(parsed.Gist, parsed.KeyPoints, parsed.RiskUncertainty, s.cfg.MaxPoints), nil
}

func (s *SourceSummarizer) fallbackSummary(req SourceSummaryRequest) string {
	gist := firstSentence(req.Content)
	points := extractKeyPoints(req.Content, s.cfg.MaxPoints)
	risk := inferRisk(req.Content)

	fingerprint := strings.TrimSpace(req.SourceRef)
	if fingerprint == "" {
		fingerprint = "n/a"
	}
	if req.ChunkCount > 0 {
		fingerprint += fmt.Sprintf(" | chunks=%d", req.ChunkCount)
	}
	if st := strings.TrimSpace(req.SourceType); st != "" {
		fingerprint = st + " | " + fingerprint
	}

	brief := renderStructuredBrief(gist, points, risk, s.cfg.MaxPoints)
	return brief + "\nSource Fingerprint: " + fingerprint
}

func renderStructuredBrief(gist string, points []string, risk string, maxPoints int) string {
	gist = strings.TrimSpace(gist)
	if gist == "" {
		gist = "No clear gist extracted."
	}
	if maxPoints <= 0 {
		maxPoints = defaultSummaryMaxPoints
	}
	if len(points) > maxPoints {
		points = points[:maxPoints]
	}
	if len(points) == 0 {
		points = []string{"No explicit key points extracted."}
	}
	risk = strings.TrimSpace(risk)
	if risk == "" {
		risk = "none"
	}

	var b strings.Builder
	b.WriteString("Gist: ")
	b.WriteString(gist)
	b.WriteString("\nKey Points:\n")
	for _, p := range points {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		b.WriteString("- ")
		b.WriteString(p)
		b.WriteString("\n")
	}
	b.WriteString("Risks/Uncertainty: ")
	b.WriteString(risk)
	return strings.TrimSpace(b.String())
}

func firstSentence(content string) string {
	content = strings.TrimSpace(strings.Join(strings.Fields(content), " "))
	if content == "" {
		return ""
	}
	for _, sep := range []string{". ", "! ", "? ", "\n"} {
		if idx := strings.Index(content, sep); idx > 20 {
			return strings.TrimSpace(content[:idx+1])
		}
	}
	if len(content) > 180 {
		return strings.TrimSpace(content[:180]) + "..."
	}
	return content
}

func extractKeyPoints(content string, max int) []string {
	lines := strings.Split(content, "\n")
	out := make([]string, 0, max)
	seen := map[string]bool{}
	for _, line := range lines {
		candidate := strings.TrimSpace(strings.TrimLeft(line, "-*0123456789. "))
		if candidate == "" || len(candidate) < 30 {
			continue
		}
		key := strings.ToLower(candidate)
		if seen[key] {
			continue
		}
		seen[key] = true
		if len(candidate) > 180 {
			candidate = candidate[:180] + "..."
		}
		out = append(out, candidate)
		if len(out) >= max {
			break
		}
	}
	if len(out) == 0 {
		fallback := firstSentence(content)
		if fallback != "" {
			out = append(out, fallback)
		}
	}
	return out
}

func inferRisk(content string) string {
	l := strings.ToLower(content)
	riskTerms := []string{"might", "may", "unknown", "unverified", "risk", "warning", "deprecated", "todo"}
	for _, term := range riskTerms {
		if strings.Contains(l, term) {
			return "Contains uncertainty/risk language; verify against canonical source."
		}
	}
	return "none"
}

func truncateSummary(s string, maxChars int) string {
	s = strings.TrimSpace(s)
	if maxChars <= 0 || len(s) <= maxChars {
		return s
	}
	if maxChars <= 3 {
		return s[:maxChars]
	}
	return strings.TrimSpace(s[:maxChars-3]) + "..."
}
