package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/ollama/ollama/api"
)

const (
	defaultChunkTitleMaxChars = 96
)

type ChunkTitleConfig struct {
	MaxChars int
	Model    string
}

// ChunkTitleGenerator generates concise per-chunk titles for ingest metadata.
type ChunkTitleGenerator struct {
	mm  *MemoryManager
	cfg ChunkTitleConfig
}

func NewChunkTitleGenerator(mm *MemoryManager, cfg ChunkTitleConfig) *ChunkTitleGenerator {
	if cfg.MaxChars <= 0 {
		cfg.MaxChars = defaultChunkTitleMaxChars
	}
	if cfg.MaxChars > 200 {
		cfg.MaxChars = 200
	}
	return &ChunkTitleGenerator{mm: mm, cfg: cfg}
}

func (g *ChunkTitleGenerator) Generate(ctx context.Context, sourceType string, sourceRef string, chunk string, index int, total int) (string, error) {
	if g == nil {
		return "", fmt.Errorf("chunk title generator is nil")
	}
	chunk = strings.TrimSpace(chunk)
	if chunk == "" {
		return "", fmt.Errorf("chunk content is empty")
	}

	title := ""
	model := strings.TrimSpace(g.cfg.Model)
	if model != "" && g.mm != nil && g.mm.client != nil {
		modelTitle, err := g.modelTitle(ctx, model, sourceType, sourceRef, chunk, index, total)
		if err == nil {
			title = modelTitle
		}
	}
	if strings.TrimSpace(title) == "" {
		title = deterministicChunkTitle(sourceType, sourceRef, chunk, index, total)
	}
	title = clampChunkTitleASCII(title, g.cfg.MaxChars)
	if strings.TrimSpace(title) == "" {
		return "", fmt.Errorf("generated chunk title is empty")
	}
	return title, nil
}

func (g *ChunkTitleGenerator) modelTitle(ctx context.Context, model string, sourceType string, sourceRef string, chunk string, index int, total int) (string, error) {
	if g.mm == nil || g.mm.client == nil {
		return "", fmt.Errorf("memory manager client unavailable")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	system := `Generate a concise factual title for one text chunk.
Return JSON only: {"title":"..."}
Rules:
- One line only.
- No quotes, markdown, or trailing punctuation clutter.
- Keep under 12 words.`
	user := fmt.Sprintf("source_type=%s\nsource_ref=%s\nchunk_index=%d\nchunk_total=%d\nchunk:\n%s", strings.TrimSpace(sourceType), strings.TrimSpace(sourceRef), index, total, chunk)
	req := &api.ChatRequest{
		Model: model,
		Messages: []api.Message{
			{Role: "system", Content: system},
			{Role: "user", Content: user},
		},
	}
	ctx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	var out strings.Builder
	if err := g.mm.client.Chat(ctx, req, func(resp api.ChatResponse) error {
		out.WriteString(resp.Message.Content)
		return nil
	}); err != nil {
		return "", err
	}
	payload := strings.TrimSpace(stripCodeFence(out.String()))
	if payload == "" {
		return "", fmt.Errorf("empty title response")
	}
	var parsed struct {
		Title string `json:"title"`
	}
	if err := json.Unmarshal([]byte(payload), &parsed); err != nil {
		start := strings.Index(payload, "{")
		end := strings.LastIndex(payload, "}")
		if start < 0 || end <= start {
			return "", err
		}
		if err2 := json.Unmarshal([]byte(payload[start:end+1]), &parsed); err2 != nil {
			return "", err
		}
	}
	return strings.TrimSpace(parsed.Title), nil
}

func deterministicChunkTitle(sourceType string, sourceRef string, chunk string, index int, total int) string {
	prefix := chunkTitlePrefix(sourceType, sourceRef)
	body := firstSentence(chunk)
	body = strings.Trim(body, " \t\r\n-:;,.!?'\"`")
	if body == "" {
		body = "Knowledge segment"
	}
	title := body
	if prefix != "" {
		title = prefix + " :: " + body
	}
	if total > 1 && index > 0 {
		title += " [" + strconv.Itoa(index) + "/" + strconv.Itoa(total) + "]"
	}
	return title
}

func chunkTitlePrefix(sourceType string, sourceRef string) string {
	st := strings.ToLower(strings.TrimSpace(sourceType))
	ref := strings.TrimSpace(sourceRef)
	switch st {
	case "file":
		if ref == "" {
			return "file"
		}
		base := filepath.Base(ref)
		if strings.TrimSpace(base) == "" || base == "." || base == string(filepath.Separator) {
			return "file"
		}
		return base
	case "url":
		if u, err := url.Parse(ref); err == nil && strings.TrimSpace(u.Hostname()) != "" {
			return strings.ToLower(strings.TrimSpace(u.Hostname()))
		}
		return "url"
	case "hf_dataset":
		if ref != "" {
			return ref
		}
		return "hf_dataset"
	default:
		if ref != "" {
			return ref
		}
		if st != "" {
			return st
		}
		return ""
	}
}

func clampChunkTitleASCII(in string, maxChars int) string {
	s := strings.TrimSpace(strings.Join(strings.Fields(in), " "))
	if s == "" {
		return ""
	}
	var b strings.Builder
	for _, r := range s {
		if r < 32 || r > 126 {
			continue
		}
		b.WriteRune(r)
	}
	clean := strings.TrimSpace(strings.Join(strings.Fields(b.String()), " "))
	if clean == "" {
		return ""
	}
	if maxChars <= 0 {
		maxChars = defaultChunkTitleMaxChars
	}
	if len(clean) <= maxChars {
		return clean
	}
	if maxChars <= 3 {
		return clean[:maxChars]
	}
	return strings.TrimSpace(clean[:maxChars-3]) + "..."
}
