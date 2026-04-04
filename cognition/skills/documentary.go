package skills

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/Thynaptic/P-LMv1/pkg/cognition"
	"github.com/Thynaptic/P-LMv1/pkg/memory"
)

const (
	defaultDocumentaryRoot = ".memory/documentary"
	defaultPDFDPI          = 220
)

var (
	dateISORe         = regexp.MustCompile(`\b(20\d{2}|19\d{2})-(0[1-9]|1[0-2])-[0-3]\d\b`)
	dateSlashRe       = regexp.MustCompile(`\b(0?[1-9]|1[0-2])[/-]([0-2]?[0-9]|3[0-1])[/-]((?:19|20)\d{2})\b`)
	personNameRe      = regexp.MustCompile(`\b[A-Z][a-z]{2,}\s+[A-Z][a-z]{2,}\b`)
	locationHintRe    = regexp.MustCompile(`\b(?:airport|terminal|office|building|room|datacenter|vps|server|london|new york|berlin|paris|tokyo)\b`)
	eventHintRe       = regexp.MustCompile(`\b(?:meeting|flight|incident|outage|review|audit|deployment|handoff|briefing)\b`)
	stampHintRe       = regexp.MustCompile(`(?i)\b(stamp|seal|official seal|rubber stamp|notary)\b`)
	handwrittenHintRe = regexp.MustCompile(`(?i)\b(handwritten|hand writing|scribble|ink note|margin note)\b`)
	signatureHintRe   = regexp.MustCompile(`(?i)\b(signature|signed by|signatory|approved by)\b`)
)

// DocumentaryProcessor handles OCR/layout extraction and temporal narrative synthesis for non-text evidence.
type DocumentaryProcessor struct {
	Memory      *memory.MemoryManager
	WorkingRoot string
}

// DocumentaryArtifact identifies one source file to process.
type DocumentaryArtifact struct {
	Path       string            `json:"path"`
	SourceType string            `json:"source_type"` // pdf_scan, photo_ledger, screenshot, etc.
	Metadata   map[string]string `json:"metadata,omitempty"`
}

// HighValueEntity marks sensitive/high-signal visual entities.
type HighValueEntity struct {
	Kind       string    `json:"kind"` // stamp, handwritten_note, signature_block
	Value      string    `json:"value"`
	Confidence float64   `json:"confidence"`
	Page       int       `json:"page"`
	Timestamp  time.Time `json:"timestamp"`
	SourcePath string    `json:"source_path"`
}

// NarrativeNode represents one temporal graph node.
type NarrativeNode struct {
	ID          string            `json:"id"`
	Type        string            `json:"type"` // person, location, event, document
	Label       string            `json:"label"`
	Timestamp   time.Time         `json:"timestamp,omitempty"`
	Confidence  float64           `json:"confidence"`
	Attributes  map[string]string `json:"attributes,omitempty"`
	EvidenceRef []string          `json:"evidence_ref,omitempty"`
}

// NarrativeEdge links two nodes with temporal relation metadata.
type NarrativeEdge struct {
	FromID      string    `json:"from_id"`
	ToID        string    `json:"to_id"`
	Relation    string    `json:"relation"` // was_present_at, signed_by, mentioned_in
	Timestamp   time.Time `json:"timestamp,omitempty"`
	EvidenceRef string    `json:"evidence_ref,omitempty"`
	Weight      float64   `json:"weight"`
}

// TemporalNarrativeGraph is a structured timeline-aware graph.
type TemporalNarrativeGraph struct {
	GeneratedAt time.Time       `json:"generated_at"`
	Nodes       []NarrativeNode `json:"nodes"`
	Edges       []NarrativeEdge `json:"edges"`
}

// RecursiveReindexFinding captures re-evaluation of older evidence using causal checks.
type RecursiveReindexFinding struct {
	OlderEvidenceID   string                     `json:"older_evidence_id"`
	OlderSourcePath   string                     `json:"older_source_path,omitempty"`
	NewEvidencePath   string                     `json:"new_evidence_path"`
	Question          string                     `json:"question"`
	SignificanceDelta float64                    `json:"significance_delta"`
	Causal            cognition.CausalAssessment `json:"causal"`
	Decision          string                     `json:"decision"` // elevate, keep, investigate
	Reason            string                     `json:"reason"`
}

// DocumentaryResult is the aggregate processing output.
type DocumentaryResult struct {
	Artifact          DocumentaryArtifact       `json:"artifact"`
	RenderedPages     []string                  `json:"rendered_pages"`
	VisualReasoning   []VisualReasoningResult   `json:"visual_reasoning"`
	HighValueEntities []HighValueEntity         `json:"high_value_entities"`
	Graph             TemporalNarrativeGraph    `json:"graph"`
	ReindexFindings   []RecursiveReindexFinding `json:"reindex_findings"`
}

// ProcessArtifact runs visual OCR/layout analysis, builds temporal narrative graph,
// and performs recursive re-indexing against older evidence.
func (p *DocumentaryProcessor) ProcessArtifact(artifact DocumentaryArtifact) (DocumentaryResult, error) {
	artifact.Path = strings.TrimSpace(artifact.Path)
	if artifact.Path == "" {
		return DocumentaryResult{}, fmt.Errorf("artifact path is required")
	}
	if p == nil {
		p = &DocumentaryProcessor{}
	}
	root := strings.TrimSpace(p.WorkingRoot)
	if root == "" {
		root = defaultDocumentaryRoot
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return DocumentaryResult{}, err
	}

	pages, err := renderArtifactPages(artifact.Path, root)
	if err != nil {
		return DocumentaryResult{}, err
	}
	if len(pages) == 0 {
		return DocumentaryResult{}, fmt.Errorf("no renderable pages for artifact")
	}

	var visual []VisualReasoningResult
	var entities []HighValueEntity
	for i, page := range pages {
		prompt := "Extract OCR, layout, stamps, handwritten notes, and signature blocks. Mark evidentiary details."
		vr, err := AnalyzeImageWithVLM(page, prompt, "")
		if err != nil {
			// Continue page-wise so one bad page doesn't discard whole artifact.
			continue
		}
		visual = append(visual, vr)
		entities = append(entities, detectHighValueEntities(vr, page, i+1)...)

		if p.Memory != nil {
			meta := copyStringMap(artifact.Metadata)
			meta["source_type"] = nonEmpty(artifact.SourceType, "non_text_document")
			meta["source_path"] = artifact.Path
			meta["page_path"] = page
			meta["doc_page"] = fmt.Sprintf("%d", i+1)
			meta["doc_mode"] = "documentary"
			meta["base_importance"] = "0.78"
			_, _ = p.Memory.IngestImageKnowledge(page, "Documentary evidence: "+artifact.Path, meta)
		}
	}

	graph := buildTemporalNarrativeGraph(artifact.Path, visual, entities)
	reindex := p.recursiveReindex(graph, artifact.Path)

	res := DocumentaryResult{
		Artifact:          artifact,
		RenderedPages:     pages,
		VisualReasoning:   visual,
		HighValueEntities: entities,
		Graph:             graph,
		ReindexFindings:   reindex,
	}
	_ = p.persistResult(res, root)
	_ = p.persistToMemory(res)
	return res, nil
}

func (p *DocumentaryProcessor) persistToMemory(res DocumentaryResult) error {
	if p == nil || p.Memory == nil {
		return nil
	}
	b, _ := json.Marshal(res.Graph)
	summary := "Temporal Narrative Graph built for " + res.Artifact.Path +
		fmt.Sprintf(" with %d nodes, %d edges, and %d high-value entities.",
			len(res.Graph.Nodes), len(res.Graph.Edges), len(res.HighValueEntities))
	meta := map[string]string{
		"type":            "knowledge",
		"source_type":     "documentary_graph",
		"source_path":     res.Artifact.Path,
		"base_importance": "0.82",
	}
	_ = p.Memory.AddKnowledge(summary+"\nGraph JSON:\n"+string(b), meta)

	for _, f := range res.ReindexFindings {
		_ = p.Memory.AddKnowledge(
			"Recursive re-index finding: "+f.Question+"\nDecision: "+f.Decision+"\nReason: "+f.Reason,
			map[string]string{
				"type":            "knowledge",
				"source_type":     "documentary_reindex",
				"source_path":     res.Artifact.Path,
				"base_importance": "0.76",
			},
		)
	}
	return nil
}

func (p *DocumentaryProcessor) persistResult(res DocumentaryResult, root string) error {
	dir := filepath.Join(root, "results")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	name := sanitizeDocName(filepath.Base(res.Artifact.Path))
	outPath := filepath.Join(dir, name+"_"+fmt.Sprintf("%d", time.Now().Unix())+".json")
	b, err := json.MarshalIndent(res, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(outPath, b, 0o644)
}

func renderArtifactPages(path string, workingRoot string) ([]string, error) {
	path = strings.TrimSpace(path)
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".png", ".jpg", ".jpeg", ".webp", ".bmp", ".tiff":
		return []string{path}, nil
	case ".pdf":
		return renderPDFToPNGs(path, workingRoot)
	default:
		return nil, fmt.Errorf("unsupported documentary artifact format: %s", ext)
	}
}

func renderPDFToPNGs(pdfPath string, workingRoot string) ([]string, error) {
	if _, err := exec.LookPath("pdftoppm"); err != nil {
		return nil, fmt.Errorf("pdftoppm not found; required for PDF visual processing")
	}
	runID := fmt.Sprintf("%s_%d", sanitizeDocName(filepath.Base(pdfPath)), time.Now().UnixNano())
	outDir := filepath.Join(workingRoot, "render", runID)
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return nil, err
	}
	prefix := filepath.Join(outDir, "page")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "pdftoppm", "-png", "-r", fmt.Sprintf("%d", defaultPDFDPI), pdfPath, prefix)
	if out, err := cmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("pdftoppm failed: %v (%s)", err, strings.TrimSpace(string(out)))
	}
	files, err := filepath.Glob(filepath.Join(outDir, "page-*.png"))
	if err != nil {
		return nil, err
	}
	sort.Strings(files)
	return files, nil
}

func detectHighValueEntities(vr VisualReasoningResult, sourcePath string, page int) []HighValueEntity {
	text := strings.TrimSpace(strings.Join([]string{
		vr.Summary,
		vr.OCRText,
		vr.Layout,
		vr.SpatialCorrelation,
		strings.Join(vr.TechnicalFindings, " "),
	}, "\n"))
	if text == "" {
		return nil
	}
	now := time.Now().UTC()
	var out []HighValueEntity
	add := func(kind, value string, conf float64) {
		out = append(out, HighValueEntity{
			Kind:       kind,
			Value:      strings.TrimSpace(value),
			Confidence: conf,
			Page:       page,
			Timestamp:  now,
			SourcePath: sourcePath,
		})
	}
	if stampHintRe.MatchString(text) {
		add("stamp", "Stamp/Seal indicator found in layout or OCR", 0.84)
	}
	if handwrittenHintRe.MatchString(text) {
		add("handwritten_note", "Handwritten annotation pattern detected", 0.79)
	}
	if signatureHintRe.MatchString(text) {
		add("signature_block", "Signature/approval block detected", 0.88)
	}
	return out
}

func buildTemporalNarrativeGraph(sourcePath string, visual []VisualReasoningResult, entities []HighValueEntity) TemporalNarrativeGraph {
	g := TemporalNarrativeGraph{
		GeneratedAt: time.Now().UTC(),
	}

	nodeByKey := map[string]int{}
	ensureNode := func(ntype, label string, ts time.Time, conf float64, attrs map[string]string, ref string) string {
		key := strings.ToLower(strings.TrimSpace(ntype + "::" + label))
		if idx, ok := nodeByKey[key]; ok {
			if ref != "" {
				g.Nodes[idx].EvidenceRef = appendUnique(g.Nodes[idx].EvidenceRef, ref)
			}
			if conf > g.Nodes[idx].Confidence {
				g.Nodes[idx].Confidence = conf
			}
			return g.Nodes[idx].ID
		}
		id := fmt.Sprintf("n_%s_%d", sanitizeDocName(key), len(g.Nodes)+1)
		n := NarrativeNode{
			ID:          id,
			Type:        ntype,
			Label:       label,
			Timestamp:   ts,
			Confidence:  conf,
			Attributes:  copyStringMap(attrs),
			EvidenceRef: nil,
		}
		if ref != "" {
			n.EvidenceRef = []string{ref}
		}
		nodeByKey[key] = len(g.Nodes)
		g.Nodes = append(g.Nodes, n)
		return id
	}

	// Base document node.
	docID := ensureNode("document", filepath.Base(sourcePath), time.Now().UTC(), 0.95, map[string]string{
		"source_path": sourcePath,
	}, sourcePath)

	// High-value entity nodes and edges.
	for _, e := range entities {
		label := strings.ReplaceAll(e.Kind, "_", " ")
		nid := ensureNode("event", label, e.Timestamp, e.Confidence, map[string]string{
			"kind": e.Kind,
		}, e.SourcePath)
		g.Edges = append(g.Edges, NarrativeEdge{
			FromID:      nid,
			ToID:        docID,
			Relation:    "mentioned_in",
			Timestamp:   e.Timestamp,
			EvidenceRef: e.SourcePath,
			Weight:      e.Confidence,
		})
	}

	// Extract people, location, events from OCR to build temporal narrative.
	for _, vr := range visual {
		text := strings.TrimSpace(vr.OCRText + "\n" + vr.Summary + "\n" + vr.SpatialCorrelation)
		ts := parseTemporalHint(text)
		ref := nonEmpty(vr.Summary, sourcePath)

		for _, p := range dedupeStrings(personNameRe.FindAllString(text, -1)) {
			pid := ensureNode("person", p, ts, 0.74, nil, ref)
			g.Edges = append(g.Edges, NarrativeEdge{
				FromID:      pid,
				ToID:        docID,
				Relation:    "mentioned_in",
				Timestamp:   ts,
				EvidenceRef: ref,
				Weight:      0.70,
			})
			if signatureHintRe.MatchString(text) {
				g.Edges = append(g.Edges, NarrativeEdge{
					FromID:      docID,
					ToID:        pid,
					Relation:    "signed_by",
					Timestamp:   ts,
					EvidenceRef: ref,
					Weight:      0.83,
				})
			}
		}

		if locationHintRe.MatchString(strings.ToLower(text)) {
			locLabel := guessLocationLabel(text)
			lid := ensureNode("location", locLabel, ts, 0.67, nil, ref)
			g.Edges = append(g.Edges, NarrativeEdge{
				FromID:      lid,
				ToID:        docID,
				Relation:    "mentioned_in",
				Timestamp:   ts,
				EvidenceRef: ref,
				Weight:      0.63,
			})
			for _, n := range g.Nodes {
				if n.Type == "person" {
					g.Edges = append(g.Edges, NarrativeEdge{
						FromID:      n.ID,
						ToID:        lid,
						Relation:    "was_present_at",
						Timestamp:   ts,
						EvidenceRef: ref,
						Weight:      0.55,
					})
				}
			}
		}
		if eventHintRe.MatchString(strings.ToLower(text)) {
			ev := firstMatch(eventHintRe, strings.ToLower(text))
			eid := ensureNode("event", strings.Title(ev), ts, 0.64, nil, ref) //nolint:staticcheck
			g.Edges = append(g.Edges, NarrativeEdge{
				FromID:      eid,
				ToID:        docID,
				Relation:    "mentioned_in",
				Timestamp:   ts,
				EvidenceRef: ref,
				Weight:      0.62,
			})
		}
	}
	return g
}

func (p *DocumentaryProcessor) recursiveReindex(g TemporalNarrativeGraph, newSourcePath string) []RecursiveReindexFinding {
	if p == nil || p.Memory == nil || len(g.Nodes) == 0 {
		return nil
	}
	cm := cognition.NewDefaultCausalModel()
	var findings []RecursiveReindexFinding

	var queryParts []string
	for _, n := range g.Nodes {
		queryParts = append(queryParts, n.Label)
		if len(queryParts) >= 8 {
			break
		}
	}
	query := strings.TrimSpace(strings.Join(queryParts, " "))
	if query == "" {
		query = "documentary evidence timeline signature stamp meeting"
	}
	segs, err := p.Memory.RetrieveKnowledgeSegments(query, 20)
	if err != nil || len(segs) == 0 {
		return nil
	}

	for _, s := range segs {
		oldSource := strings.TrimSpace(s.Metadata["source_path"])
		if oldSource == "" || oldSource == newSourcePath {
			continue
		}
		overlap := overlapScore(query, s.Content+" "+strings.Join(mapNodeLabels(g.Nodes), " "))
		if overlap < 0.18 {
			continue
		}
		causal := cm.Simulate("DocumentaryEvidenceReindex")
		delta := clamp01Doc((overlap * 0.7) + ((1.0 - causal.FragilityScore) * 0.3))
		decision := "keep"
		reason := "new evidence does not materially shift prior significance"
		if delta >= 0.62 {
			decision = "elevate"
			reason = "new documentary evidence plausibly increases importance of older related record"
		} else if delta >= 0.40 {
			decision = "investigate"
			reason = "moderate overlap; requires targeted verification before mutation"
		}
		findings = append(findings, RecursiveReindexFinding{
			OlderEvidenceID:   s.ID,
			OlderSourcePath:   oldSource,
			NewEvidencePath:   newSourcePath,
			Question:          "Does this new document change the significance of older evidence?",
			SignificanceDelta: delta,
			Causal:            causal,
			Decision:          decision,
			Reason:            reason,
		})
	}
	sort.Slice(findings, func(i, j int) bool {
		return findings[i].SignificanceDelta > findings[j].SignificanceDelta
	})
	if len(findings) > 12 {
		findings = findings[:12]
	}
	return findings
}

func parseTemporalHint(text string) time.Time {
	text = strings.TrimSpace(text)
	if text == "" {
		return time.Time{}
	}
	if m := dateISORe.FindString(text); m != "" {
		if t, err := time.Parse("2006-01-02", m); err == nil {
			return t.UTC()
		}
	}
	if m := dateSlashRe.FindStringSubmatch(text); len(m) == 4 {
		parsed := fmt.Sprintf("%s/%s/%s", m[1], m[2], m[3])
		if t, err := time.Parse("1/2/2006", parsed); err == nil {
			return t.UTC()
		}
	}
	return time.Time{}
}

func guessLocationLabel(text string) string {
	text = strings.ToLower(strings.TrimSpace(text))
	switch {
	case strings.Contains(text, "airport"):
		return "Airport"
	case strings.Contains(text, "datacenter"):
		return "Datacenter"
	case strings.Contains(text, "office"):
		return "Office"
	case strings.Contains(text, "server"):
		return "Server Room"
	default:
		return "Location"
	}
}

func firstMatch(re *regexp.Regexp, text string) string {
	if re == nil {
		return ""
	}
	m := re.FindString(text)
	return strings.TrimSpace(m)
}

func overlapScore(a, b string) float64 {
	ta := tokenSet(a)
	tb := tokenSet(b)
	if len(ta) == 0 || len(tb) == 0 {
		return 0
	}
	match := 0.0
	for t := range ta {
		if tb[t] {
			match++
		}
	}
	return match / float64(maxIntDoc(len(ta), len(tb)))
}

func tokenSet(s string) map[string]bool {
	out := map[string]bool{}
	parts := regexp.MustCompile(`[^a-z0-9._:/-]+`).Split(strings.ToLower(strings.TrimSpace(s)), -1)
	for _, p := range parts {
		if len(p) < 4 {
			continue
		}
		out[p] = true
	}
	return out
}

func mapNodeLabels(nodes []NarrativeNode) []string {
	out := make([]string, 0, len(nodes))
	for _, n := range nodes {
		if s := strings.TrimSpace(n.Label); s != "" {
			out = append(out, s)
		}
	}
	return out
}

func appendUnique(list []string, val string) []string {
	val = strings.TrimSpace(val)
	if val == "" {
		return list
	}
	for _, v := range list {
		if strings.EqualFold(strings.TrimSpace(v), val) {
			return list
		}
	}
	return append(list, val)
}

func dedupeStrings(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, v := range in {
		k := strings.ToLower(strings.TrimSpace(v))
		if k == "" || seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, strings.TrimSpace(v))
	}
	return out
}

func sanitizeDocName(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	re := regexp.MustCompile(`[^a-z0-9._-]+`)
	s = re.ReplaceAllString(s, "_")
	s = strings.Trim(s, "._-")
	if s == "" {
		return "doc"
	}
	return s
}

func copyStringMap(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func clamp01Doc(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

func maxIntDoc(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func nonEmpty(v, fallback string) string {
	v = strings.TrimSpace(v)
	if v != "" {
		return v
	}
	return strings.TrimSpace(fallback)
}
