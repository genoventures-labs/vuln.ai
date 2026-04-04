package memory

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/Thynaptic/P-LMv1/pkg/state"
	"github.com/ollama/ollama/api"
)

var (
	visionModels = []string{
		"llava:latest",
		"moondream:latest",
		"llava",
		"moondream",
	}
	jsonObjectRE = regexp.MustCompile(`(?s)\{.*\}`)
)

// VisualDescriptor stores rich vision analysis for memory indexing.
type VisualDescriptor struct {
	Summary               string   `json:"summary"`
	OCRText               string   `json:"ocr_text"`
	Layout                string   `json:"layout"`
	Objects               []string `json:"objects"`
	TechnicalObservations []string `json:"technical_observations"`
}

// VisualIngestResult reports multimodal ingest outputs.
type VisualIngestResult struct {
	ImagePath       string           `json:"image_path"`
	Model           string           `json:"model"`
	LinkID          string           `json:"cross_modal_link_id"`
	Descriptor      VisualDescriptor `json:"descriptor"`
	DescriptorText  string           `json:"descriptor_text"`
	LinkedTextDocID string           `json:"linked_text_doc_id,omitempty"`
}

// IngestImageKnowledge processes an image with a local vision model, stores rich descriptors
// in the knowledge vector store, and links it to related text memory via cross-modal ID.
func (mm *MemoryManager) IngestImageKnowledge(imagePath string, anchorText string, metadata map[string]string) (*VisualIngestResult, error) {
	imagePath = strings.TrimSpace(imagePath)
	if imagePath == "" {
		return nil, fmt.Errorf("image path is required")
	}
	if mm == nil || mm.client == nil {
		return nil, fmt.Errorf("memory manager not initialized with ollama client")
	}

	absPath := imagePath
	if resolved, err := filepath.Abs(imagePath); err == nil {
		absPath = resolved
	}
	imageBytes, err := os.ReadFile(absPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read image: %w", err)
	}

	desc, usedModel, err := mm.describeImage(imageBytes, filepath.Base(absPath), anchorText)
	if err != nil {
		return nil, err
	}
	linkID := crossModalLinkID(anchorText, absPath)
	descText := visualDescriptorToText(desc, absPath, anchorText)

	meta := map[string]string{
		"type":             "knowledge",
		metaModality:       "image",
		metaImagePath:      absPath,
		metaVisualModel:    usedModel,
		metaCrossModalLink: linkID,
		metaClusterLabel:   "xmodal:" + linkID,
		metaClusterID:      clusterIDForLabel("xmodal:" + linkID),
		metaBaseImportance: formatFloat(0.72),
		metaTimestamp:      time.Now().UTC().Format(time.RFC3339),
	}
	for k, v := range metadata {
		meta[k] = v
	}

	if err := mm.AddKnowledge(descText, meta); err != nil {
		return nil, fmt.Errorf("failed to store visual descriptor: %w", err)
	}

	linkedTextDocID, _ := mm.linkAnchorToCrossModal(anchorText, linkID, metadata)
	return &VisualIngestResult{
		ImagePath:       absPath,
		Model:           usedModel,
		LinkID:          linkID,
		Descriptor:      desc,
		DescriptorText:  descText,
		LinkedTextDocID: linkedTextDocID,
	}, nil
}

func (mm *MemoryManager) describeImage(imageBytes []byte, imageName string, anchorText string) (VisualDescriptor, string, error) {
	system := `You are a technical vision parser for long-term memory indexing.
Analyze the image and return JSON only:
{
  "summary":"...",
  "ocr_text":"...",
  "layout":"...",
  "objects":["..."],
  "technical_observations":["..."]
}
Requirements:
- OCR should include exact key labels, IDs, port numbers, and settings when visible.
- Layout should describe spatial structure (top/bottom/left panels, table regions, form sections).
- technical_observations should include operational clues relevant to infrastructure/software diagnostics.`

	user := "Image name: " + imageName
	if strings.TrimSpace(anchorText) != "" {
		user += "\nAnchor text/context: " + strings.TrimSpace(anchorText)
	}

	for _, model := range visionModels {
		opts, _ := state.ResolveEntropyOptions(user)
		req := &api.ChatRequest{
			Model:   model,
			Options: opts,
			Messages: []api.Message{
				{Role: "system", Content: system},
				{Role: "user", Content: user, Images: []api.ImageData{imageBytes}},
			},
		}
		ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
		var out strings.Builder
		err := mm.client.Chat(ctx, req, func(resp api.ChatResponse) error {
			out.WriteString(resp.Message.Content)
			return nil
		})
		cancel()
		if err != nil {
			continue
		}

		desc, ok := parseVisualDescriptor(out.String())
		if !ok {
			continue
		}
		return desc, model, nil
	}
	return VisualDescriptor{}, "", fmt.Errorf("no vision model succeeded for image analysis")
}

func parseVisualDescriptor(raw string) (VisualDescriptor, bool) {
	raw = strings.TrimSpace(stripCodeFence(raw))
	var d VisualDescriptor
	if err := json.Unmarshal([]byte(raw), &d); err == nil {
		return normalizeVisualDescriptor(d), true
	}
	match := jsonObjectRE.FindString(raw)
	if match == "" {
		return VisualDescriptor{}, false
	}
	if err := json.Unmarshal([]byte(match), &d); err != nil {
		return VisualDescriptor{}, false
	}
	return normalizeVisualDescriptor(d), true
}

func normalizeVisualDescriptor(d VisualDescriptor) VisualDescriptor {
	d.Summary = strings.TrimSpace(d.Summary)
	d.OCRText = strings.TrimSpace(d.OCRText)
	d.Layout = strings.TrimSpace(d.Layout)
	d.Objects = dedupeNonEmpty(d.Objects, 16)
	d.TechnicalObservations = dedupeNonEmpty(d.TechnicalObservations, 16)
	return d
}

func dedupeNonEmpty(in []string, maxN int) []string {
	if len(in) == 0 {
		return nil
	}
	seen := map[string]bool{}
	out := make([]string, 0, len(in))
	for _, v := range in {
		s := strings.TrimSpace(v)
		key := strings.ToLower(s)
		if s == "" || seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, s)
		if len(out) >= maxN {
			break
		}
	}
	return out
}

func visualDescriptorToText(d VisualDescriptor, imagePath string, anchorText string) string {
	var b strings.Builder
	b.WriteString("Visual Descriptor")
	if imagePath != "" {
		b.WriteString(" [image:")
		b.WriteString(imagePath)
		b.WriteString("]")
	}
	b.WriteString("\n")
	if strings.TrimSpace(anchorText) != "" {
		b.WriteString("Anchor Context: ")
		b.WriteString(strings.TrimSpace(anchorText))
		b.WriteString("\n")
	}
	if d.Summary != "" {
		b.WriteString("Summary: ")
		b.WriteString(d.Summary)
		b.WriteString("\n")
	}
	if d.OCRText != "" {
		b.WriteString("OCR: ")
		b.WriteString(d.OCRText)
		b.WriteString("\n")
	}
	if d.Layout != "" {
		b.WriteString("Layout: ")
		b.WriteString(d.Layout)
		b.WriteString("\n")
	}
	if len(d.Objects) > 0 {
		b.WriteString("Objects: ")
		b.WriteString(strings.Join(d.Objects, "; "))
		b.WriteString("\n")
	}
	if len(d.TechnicalObservations) > 0 {
		b.WriteString("Technical Observations: ")
		b.WriteString(strings.Join(d.TechnicalObservations, "; "))
	}
	return strings.TrimSpace(b.String())
}

func crossModalLinkID(anchorText string, imagePath string) string {
	base := strings.ToLower(strings.TrimSpace(anchorText)) + "|" + strings.ToLower(strings.TrimSpace(imagePath))
	h := sha1.Sum([]byte(base))
	return "xmod_" + hex.EncodeToString(h[:8])
}

func (mm *MemoryManager) linkAnchorToCrossModal(anchorText string, linkID string, metadata map[string]string) (string, error) {
	anchorText = strings.TrimSpace(anchorText)
	linkID = strings.TrimSpace(linkID)
	if anchorText == "" || linkID == "" {
		return "", nil
	}

	// Try to bind the closest existing knowledge fragment to this link.
	if mm.knowledgeCollection.Count() > 0 {
		results, err := mm.knowledgeCollection.Query(context.Background(), anchorText, 1, mm.namespaceWhereFilter(), nil)
		if err == nil && len(results) > 0 {
			doc, getErr := mm.knowledgeCollection.GetByID(context.Background(), results[0].ID)
			if getErr == nil {
				if doc.Metadata == nil {
					doc.Metadata = make(map[string]string)
				}
				doc.Metadata[metaCrossModalLink] = linkID
				if _, ok := doc.Metadata[metaModality]; !ok {
					doc.Metadata[metaModality] = "text"
				}
				if _, ok := doc.Metadata[metaClusterLabel]; !ok {
					doc.Metadata[metaClusterLabel] = "xmodal:" + linkID
					doc.Metadata[metaClusterID] = clusterIDForLabel("xmodal:" + linkID)
				}
				if err := mm.knowledgeCollection.AddDocument(context.Background(), doc); err == nil {
					return doc.ID, nil
				}
			}
		}
	}

	// If no suitable text fragment exists, create an explicit anchor doc.
	meta := map[string]string{
		"type":             "knowledge",
		metaModality:       "text_anchor",
		metaCrossModalLink: linkID,
		metaClusterLabel:   "xmodal:" + linkID,
		metaClusterID:      clusterIDForLabel("xmodal:" + linkID),
		metaBaseImportance: formatFloat(0.64),
		metaTimestamp:      time.Now().UTC().Format(time.RFC3339),
	}
	for k, v := range metadata {
		meta[k] = v
	}
	content := "Cross-modal text anchor: " + anchorText
	if err := mm.AddKnowledge(content, meta); err != nil {
		return "", err
	}
	return "", nil
}
