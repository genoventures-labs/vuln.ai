package orchestration

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ollama/ollama/api"
	chromem "github.com/philippgille/chromem-go"
)

const (
	defaultArchiveFeedPath  = ".memory/decision_feed.jsonl"
	defaultArchiveBriefPath = ".memory/archive_mirror_briefs.jsonl"
	archiveCollectionName   = "reasoning_archive"
	archiveEmbeddingModel   = "nomic-embed-text:latest"
)

// DecisionRecord captures why a path/topology decision was chosen.
type DecisionRecord struct {
	Timestamp    time.Time `json:"timestamp"`
	Query        string    `json:"query"`
	Source       string    `json:"source,omitempty"`
	Topology     string    `json:"topology,omitempty"`
	ChosenPath   string    `json:"chosen_path"`
	Alternatives []string  `json:"alternatives,omitempty"`
	Reasoning    string    `json:"reasoning"`
	Confidence   float64   `json:"confidence,omitempty"`
}

// ReasoningMatch is an archive retrieval item.
type ReasoningMatch struct {
	ID         string         `json:"id"`
	Similarity float64        `json:"similarity"`
	Record     DecisionRecord `json:"record"`
}

// MirrorBrief is a compact mirror status report from daemons.
type MirrorBrief struct {
	Timestamp time.Time `json:"timestamp"`
	Source    string    `json:"source,omitempty"`
	Message   string    `json:"message"`
}

// AppendDecisionFeed appends one decision record to the daemon feed.
func AppendDecisionFeed(path string, rec DecisionRecord) error {
	path = strings.TrimSpace(path)
	if path == "" {
		path = defaultArchiveFeedPath
	}
	if rec.Timestamp.IsZero() {
		rec.Timestamp = time.Now().UTC()
	}
	b, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	return appendJSONL(path, string(b))
}

// ArchiveDecisionRecord writes the record into the vectorized reasoning archive collection.
func ArchiveDecisionRecord(rec DecisionRecord) error {
	db, col, err := openArchiveCollection()
	if err != nil {
		return err
	}
	_ = db
	if rec.Timestamp.IsZero() {
		rec.Timestamp = time.Now().UTC()
	}
	if strings.TrimSpace(rec.ChosenPath) == "" {
		rec.ChosenPath = strings.TrimSpace(rec.Topology)
	}
	if strings.TrimSpace(rec.Source) == "" {
		rec.Source = "thoughtgraph"
	}
	content := buildReasoningArchiveContent(rec)
	raw, _ := json.Marshal(rec)
	doc := chromem.Document{
		ID:      fmt.Sprintf("decision_%d", time.Now().UnixNano()),
		Content: content,
		Metadata: map[string]string{
			"type":       "decision_record",
			"source":     strings.TrimSpace(rec.Source),
			"topology":   strings.TrimSpace(rec.Topology),
			"chosen":     strings.TrimSpace(rec.ChosenPath),
			"timestamp":  rec.Timestamp.UTC().Format(time.RFC3339),
			"confidence": fmt.Sprintf("%.4f", rec.Confidence),
			"record":     string(raw),
		},
	}
	return col.AddDocument(context.Background(), doc)
}

// QueryReasoningArchive returns top-k matching decision records.
func QueryReasoningArchive(query string, k int) ([]ReasoningMatch, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, nil
	}
	_, col, err := openArchiveCollection()
	if err != nil {
		return nil, err
	}
	if k <= 0 {
		k = 4
	}
	if count := col.Count(); count == 0 {
		return nil, nil
	} else if k > count {
		k = count
	}

	results, err := col.Query(context.Background(), query, k, nil, nil)
	if err != nil {
		return nil, err
	}
	out := make([]ReasoningMatch, 0, len(results))
	for _, r := range results {
		match := ReasoningMatch{
			ID:         r.ID,
			Similarity: clamp01Archive((float64(r.Similarity) + 1) / 2),
		}
		if raw := strings.TrimSpace(r.Metadata["record"]); raw != "" {
			_ = json.Unmarshal([]byte(raw), &match.Record)
		}
		if strings.TrimSpace(match.Record.Query) == "" {
			match.Record.Query = r.Content
		}
		if match.Record.Timestamp.IsZero() {
			if ts := strings.TrimSpace(r.Metadata["timestamp"]); ts != "" {
				if parsed, err := time.Parse(time.RFC3339, ts); err == nil {
					match.Record.Timestamp = parsed
				}
			}
		}
		out = append(out, match)
	}
	return out, nil
}

// AppendArchiveMirrorBrief appends a short status brief.
func AppendArchiveMirrorBrief(path string, brief MirrorBrief) error {
	path = strings.TrimSpace(path)
	if path == "" {
		path = defaultArchiveBriefPath
	}
	if brief.Timestamp.IsZero() {
		brief.Timestamp = time.Now().UTC()
	}
	if strings.TrimSpace(brief.Source) == "" {
		brief.Source = "glm-archived"
	}
	if strings.TrimSpace(brief.Message) == "" {
		return nil
	}
	b, err := json.Marshal(brief)
	if err != nil {
		return err
	}
	return appendJSONL(path, string(b))
}

// ConsumeArchiveMirrorBriefs returns and clears pending archive briefs.
func ConsumeArchiveMirrorBriefs(path string, maxN int) ([]MirrorBrief, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		path = defaultArchiveBriefPath
	}
	if maxN <= 0 {
		maxN = 2
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var lines []string
	for _, ln := range strings.Split(string(raw), "\n") {
		ln = strings.TrimSpace(ln)
		if ln == "" {
			continue
		}
		lines = append(lines, ln)
	}
	if len(lines) == 0 {
		return nil, nil
	}
	var out []MirrorBrief
	for _, ln := range lines {
		var b MirrorBrief
		if err := json.Unmarshal([]byte(ln), &b); err != nil {
			continue
		}
		out = append(out, b)
	}
	if len(out) > maxN {
		out = out[len(out)-maxN:]
	}
	_ = os.WriteFile(path, []byte(""), 0o644)
	return out, nil
}

func openArchiveCollection() (*chromem.DB, *chromem.Collection, error) {
	client, err := api.ClientFromEnvironment()
	if err != nil {
		return nil, nil, fmt.Errorf("archive ollama client: %w", err)
	}
	ef := func(ctx context.Context, text string) ([]float32, error) {
		resp, err := client.Embeddings(ctx, &api.EmbeddingRequest{
			Model:  archiveEmbeddingModel,
			Prompt: text,
		})
		if err != nil {
			return nil, err
		}
		if len(resp.Embedding) == 0 {
			return nil, fmt.Errorf("empty embedding")
		}
		out := make([]float32, len(resp.Embedding))
		for i := range resp.Embedding {
			out[i] = float32(resp.Embedding[i])
		}
		return out, nil
	}
	db, err := chromem.NewPersistentDB(".memory", true)
	if err != nil {
		return nil, nil, err
	}
	col, err := db.GetOrCreateCollection(archiveCollectionName, nil, ef)
	if err != nil {
		return nil, nil, err
	}
	return db, col, nil
}

func buildReasoningArchiveContent(rec DecisionRecord) string {
	parts := []string{
		"Query: " + strings.TrimSpace(rec.Query),
		"Chosen: " + strings.TrimSpace(rec.ChosenPath),
		"Reasoning: " + strings.TrimSpace(rec.Reasoning),
	}
	if strings.TrimSpace(rec.Topology) != "" {
		parts = append(parts, "Topology: "+strings.TrimSpace(rec.Topology))
	}
	if len(rec.Alternatives) > 0 {
		parts = append(parts, "Alternatives: "+strings.Join(rec.Alternatives, ", "))
	}
	return strings.Join(parts, "\n")
}

func appendJSONL(path, line string) error {
	path = strings.TrimSpace(path)
	if path == "" {
		return fmt.Errorf("path required")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.WriteString(strings.TrimSpace(line) + "\n")
	return err
}

func clamp01Archive(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}
