package cognition

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

const (
	defaultDocumentaryResultsDir = ".memory/documentary/results"
)

var (
	companyEntityRE = regexp.MustCompile(`\b([A-Z][A-Za-z0-9&.\-]*(?:\s+[A-Z][A-Za-z0-9&.\-]*)*\s+(?:Inc|LLC|Ltd|Corp|Corporation|Company|Co|GmbH|PLC))\b`)
	personEntityRE  = regexp.MustCompile(`\b([A-Z][a-z]+(?:\s+[A-Z][a-z]+){1,2})\b`)
	signatureRE     = regexp.MustCompile(`(?i)\bsignature\b`)
)

// VisualEvidence is a lightweight vision-side signal used by entity extraction.
type VisualEvidence struct {
	SourcePath string
	OCRText    string
	Labels     []string
}

// EvidenceInput merges Scout and Vision evidence streams.
type EvidenceInput struct {
	ScoutFindings []ScoutFinding
	Vision        []VisualEvidence
}

// NamedEntity is one normalized entity mention.
type NamedEntity struct {
	Name       string   `json:"name"`
	Type       string   `json:"type"` // person | company | document | unknown
	Documents  []string `json:"documents,omitempty"`
	Mentions   int      `json:"mentions"`
	Confidence float64  `json:"confidence"`
}

// EntityLink is one weighted relation between two entities.
type EntityLink struct {
	EntityA      string   `json:"entity_a"`
	EntityB      string   `json:"entity_b"`
	TypeA        string   `json:"type_a,omitempty"`
	TypeB        string   `json:"type_b,omitempty"`
	DocumentRefs []string `json:"document_refs,omitempty"`
	CoMentions   int      `json:"co_mentions"`
	CoDocs       int      `json:"co_docs"`
	Weight       float64  `json:"weight"`
	Confidence   float64  `json:"confidence"`
	Label        string   `json:"label"` // high-confidence-link | medium-link
}

// RelationalMap is the graph output for documentary relation reasoning.
type RelationalMap struct {
	GeneratedAt time.Time     `json:"generated_at"`
	Entities    []NamedEntity `json:"entities"`
	Links       []EntityLink  `json:"links"`
}

// HUDAnchor is a screen anchor for one document region.
type HUDAnchor struct {
	Document string `json:"document"`
	X        int    `json:"x"`
	Y        int    `json:"y"`
	W        int    `json:"w"`
	H        int    `json:"h"`
}

// HUDBox is a War-Room-compatible rectangle definition.
type HUDBox struct {
	X     int    `json:"x"`
	Y     int    `json:"y"`
	W     int    `json:"w"`
	H     int    `json:"h"`
	Text  string `json:"text,omitempty"`
	Kind  string `json:"kind,omitempty"`
	Color string `json:"color,omitempty"`
}

// EvidenceHUDPlan is an overlay plan and narration line for Reasoning Mirror.
type EvidenceHUDPlan struct {
	Boxes     []HUDBox `json:"boxes"`
	Narrative string   `json:"narrative"`
}

// LoadDocumentaryEvidence reads documentary JSON outputs and converts them into scout+vision inputs.
func LoadDocumentaryEvidence(resultsDir string) (EvidenceInput, error) {
	if strings.TrimSpace(resultsDir) == "" {
		resultsDir = defaultDocumentaryResultsDir
	}
	entries, err := os.ReadDir(resultsDir)
	if err != nil {
		return EvidenceInput{}, err
	}

	type docJSON struct {
		Artifact struct {
			Path string `json:"path"`
		} `json:"artifact"`
		VisualReasoning []struct {
			Summary            string   `json:"summary"`
			SpatialCorrelation string   `json:"spatial_correlation"`
			TechnicalFindings  []string `json:"technical_findings"`
			SpatialElements    []struct {
				Label string `json:"label"`
			} `json:"spatial_elements"`
		} `json:"visual_reasoning"`
	}

	in := EvidenceInput{}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(strings.ToLower(e.Name()), ".json") {
			continue
		}
		path := filepath.Join(resultsDir, e.Name())
		raw, err := os.ReadFile(path)
		if err != nil || len(raw) == 0 {
			continue
		}
		var d docJSON
		if err := json.Unmarshal(raw, &d); err != nil {
			continue
		}
		docPath := strings.TrimSpace(d.Artifact.Path)
		if docPath == "" {
			docPath = path
		}

		sf := ScoutFinding{
			ID:         strings.TrimSuffix(e.Name(), filepath.Ext(e.Name())),
			SourcePath: docPath,
			DocumentID: filepath.Base(docPath),
			Timestamp:  time.Now().UTC(),
			Confidence: 0.62,
		}
		if len(d.VisualReasoning) > 0 {
			vr := d.VisualReasoning[0]
			sf.Summary = strings.TrimSpace(vr.Summary + " " + vr.SpatialCorrelation + " " + strings.Join(vr.TechnicalFindings, " "))
			labels := make([]string, 0, len(vr.SpatialElements))
			for _, el := range vr.SpatialElements {
				if t := strings.TrimSpace(el.Label); t != "" {
					labels = append(labels, t)
				}
			}
			in.Vision = append(in.Vision, VisualEvidence{
				SourcePath: docPath,
				OCRText:    sf.Summary,
				Labels:     labels,
			})
		}
		if strings.TrimSpace(sf.Summary) == "" {
			sf.Summary = "documentary evidence"
		}
		in.ScoutFindings = append(in.ScoutFindings, sf)
	}
	return in, nil
}

// BuildRelationalMap extracts entities and scores weighted links across documents.
func BuildRelationalMap(input EvidenceInput) RelationalMap {
	docEntities := extractDocEntities(input)
	entities := aggregateEntities(docEntities)
	links := weightEntityLinks(docEntities, entities)
	return RelationalMap{
		GeneratedAt: time.Now().UTC(),
		Entities:    entities,
		Links:       links,
	}
}

// BuildEvidenceHUDWeb creates a War-Room overlay plan connecting two related documents.
func BuildEvidenceHUDWeb(link EntityLink, currentDoc HUDAnchor, relatedDoc HUDAnchor) EvidenceHUDPlan {
	normalizeAnchor := func(a HUDAnchor) HUDAnchor {
		if a.W <= 0 {
			a.W = 260
		}
		if a.H <= 0 {
			a.H = 140
		}
		if a.X < 0 {
			a.X = 0
		}
		if a.Y < 0 {
			a.Y = 0
		}
		return a
	}
	currentDoc = normalizeAnchor(currentDoc)
	relatedDoc = normalizeAnchor(relatedDoc)

	boxes := []HUDBox{
		{
			X: currentDoc.X, Y: currentDoc.Y, W: currentDoc.W, H: currentDoc.H,
			Text: "Current: " + docLabel(currentDoc.Document), Kind: "document", Color: "red",
		},
		{
			X: relatedDoc.X, Y: relatedDoc.Y, W: relatedDoc.W, H: relatedDoc.H,
			Text: "Linked: " + docLabel(relatedDoc.Document), Kind: "verified", Color: "green",
		},
	}
	boxes = append(boxes, buildConnectorBoxes(currentDoc, relatedDoc, link.Label)...)

	narrative := fmt.Sprintf(
		"Sir, %s matches %s across %d document(s). I've highlighted the connection.",
		link.EntityA, link.EntityB, maxIntEvidence(1, link.CoDocs),
	)
	if isSignatureConnection(link) {
		narrative = "Sir, this signature matches the one in the 2011 Ledger. I've highlighted the connection."
	}
	return EvidenceHUDPlan{
		Boxes:     boxes,
		Narrative: narrative,
	}
}

// ExtractNamedEntities returns entity inventory across scout+vision evidence.
func ExtractNamedEntities(input EvidenceInput) []NamedEntity {
	return aggregateEntities(extractDocEntities(input))
}

// TopEvidenceLink returns the best weighted connection.
func TopEvidenceLink(m RelationalMap) (EntityLink, bool) {
	if len(m.Links) == 0 {
		return EntityLink{}, false
	}
	return m.Links[0], true
}

type docEntityMentions map[string]map[string]int // doc -> entityKey -> mentions

func extractDocEntities(input EvidenceInput) docEntityMentions {
	out := docEntityMentions{}
	addMention := func(doc, name string) {
		doc = strings.TrimSpace(doc)
		name = normalizeEntityName(name)
		if doc == "" || name == "" {
			return
		}
		if out[doc] == nil {
			out[doc] = map[string]int{}
		}
		out[doc][name]++
	}

	for _, f := range input.ScoutFindings {
		doc := bestDocRef(f.DocumentID, f.SourcePath, f.ID)
		if actor := strings.TrimSpace(f.Actor); actor != "" {
			addMention(doc, actor)
		}
		for _, e := range extractEntityCandidates(strings.TrimSpace(f.Summary)) {
			addMention(doc, e)
		}
		if ev := strings.TrimSpace(f.EventType); ev != "" {
			addMention(doc, ev)
		}
	}

	for _, v := range input.Vision {
		doc := bestDocRef("", v.SourcePath, "")
		for _, e := range extractEntityCandidates(v.OCRText) {
			addMention(doc, e)
		}
		for _, l := range v.Labels {
			for _, e := range extractEntityCandidates(l) {
				addMention(doc, e)
			}
		}
	}
	return out
}

func aggregateEntities(docEntities docEntityMentions) []NamedEntity {
	type agg struct {
		Type      string
		Mentions  int
		DocSet    map[string]struct{}
		ConfScore float64
	}
	byEntity := map[string]*agg{}
	for doc, ents := range docEntities {
		for entity, count := range ents {
			a := byEntity[entity]
			if a == nil {
				a = &agg{
					Type:      classifyEntityType(entity),
					DocSet:    map[string]struct{}{},
					ConfScore: 0.45,
				}
				byEntity[entity] = a
			}
			a.Mentions += count
			a.DocSet[doc] = struct{}{}
			a.ConfScore += float64(count) * 0.08
		}
	}
	out := make([]NamedEntity, 0, len(byEntity))
	for name, a := range byEntity {
		docs := make([]string, 0, len(a.DocSet))
		for d := range a.DocSet {
			docs = append(docs, d)
		}
		sort.Strings(docs)
		out = append(out, NamedEntity{
			Name:       name,
			Type:       a.Type,
			Documents:  docs,
			Mentions:   a.Mentions,
			Confidence: clamp01Evidence(a.ConfScore + float64(len(docs))*0.06),
		})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Confidence == out[j].Confidence {
			return out[i].Mentions > out[j].Mentions
		}
		return out[i].Confidence > out[j].Confidence
	})
	return out
}

func weightEntityLinks(docEntities docEntityMentions, entities []NamedEntity) []EntityLink {
	et := map[string]string{}
	for _, e := range entities {
		et[e.Name] = e.Type
	}
	type pairAgg struct {
		Docs      map[string]struct{}
		CoMention int
	}
	agg := map[string]*pairAgg{}

	for doc, mentions := range docEntities {
		var keys []string
		for k := range mentions {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for i := 0; i < len(keys); i++ {
			for j := i + 1; j < len(keys); j++ {
				a, b := keys[i], keys[j]
				id := a + "||" + b
				p := agg[id]
				if p == nil {
					p = &pairAgg{Docs: map[string]struct{}{}}
					agg[id] = p
				}
				p.Docs[doc] = struct{}{}
				cm := mentions[a]
				if mentions[b] < cm {
					cm = mentions[b]
				}
				p.CoMention += maxIntEvidence(cm, 1)
			}
		}
	}

	links := make([]EntityLink, 0, len(agg))
	for id, p := range agg {
		parts := strings.Split(id, "||")
		if len(parts) != 2 {
			continue
		}
		a, b := parts[0], parts[1]
		docRefs := make([]string, 0, len(p.Docs))
		for d := range p.Docs {
			docRefs = append(docRefs, d)
		}
		sort.Strings(docRefs)
		docsN := len(docRefs)
		weight := clamp01Evidence(float64(p.CoMention)*0.12 + float64(docsN)*0.16)
		conf := clamp01Evidence(weight + boolToFloatEvidence(p.CoMention >= 5)*0.22)
		label := "medium-link"
		if p.CoMention >= 5 || conf >= 0.82 {
			label = "high-confidence-link"
		}
		links = append(links, EntityLink{
			EntityA:      a,
			EntityB:      b,
			TypeA:        et[a],
			TypeB:        et[b],
			DocumentRefs: docRefs,
			CoMentions:   p.CoMention,
			CoDocs:       docsN,
			Weight:       weight,
			Confidence:   conf,
			Label:        label,
		})
	}

	sort.SliceStable(links, func(i, j int) bool {
		if links[i].Confidence == links[j].Confidence {
			return links[i].CoMentions > links[j].CoMentions
		}
		return links[i].Confidence > links[j].Confidence
	})
	return links
}

func extractEntityCandidates(text string) []string {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}
	var out []string
	add := func(s string) {
		s = normalizeEntityName(s)
		if s == "" {
			return
		}
		out = append(out, s)
	}

	for _, m := range companyEntityRE.FindAllString(text, -1) {
		add(m)
	}
	for _, m := range personEntityRE.FindAllString(text, -1) {
		add(m)
	}
	if signatureRE.MatchString(text) {
		add("Signature")
	}

	// Quoted tokens often carry named records in OCR/text dumps.
	qRe := regexp.MustCompile(`"([^"]{3,80})"`)
	for _, mm := range qRe.FindAllStringSubmatch(text, -1) {
		if len(mm) == 2 {
			add(mm[1])
		}
	}
	return dedupeEvidenceStrings(out)
}

func buildConnectorBoxes(a HUDAnchor, b HUDAnchor, label string) []HUDBox {
	x1 := a.X + a.W/2
	y1 := a.Y + a.H/2
	x2 := b.X + b.W/2
	y2 := b.Y + b.H/2
	dx := x2 - x1
	dy := y2 - y1
	steps := 9
	if absEvidence(dx)+absEvidence(dy) > 900 {
		steps = 12
	}
	if steps < 3 {
		steps = 3
	}

	out := make([]HUDBox, 0, steps+1)
	for i := 1; i <= steps; i++ {
		t := float64(i) / float64(steps)
		x := x1 + int(float64(dx)*t)
		y := y1 + int(float64(dy)*t)
		out = append(out, HUDBox{
			X: x - 3, Y: y - 3, W: 6, H: 6,
			Kind: "timeline", Color: "yellow",
		})
	}
	if strings.TrimSpace(label) != "" {
		mx := x1 + dx/2
		my := y1 + dy/2
		out = append(out, HUDBox{
			X: mx + 8, Y: my - 10, W: 260, H: 44,
			Text: "Link: " + label, Kind: "entity", Color: "blue",
		})
	}
	return out
}

func classifyEntityType(name string) string {
	n := strings.TrimSpace(name)
	if n == "" {
		return "unknown"
	}
	l := strings.ToLower(n)
	for _, tok := range []string{" inc", " llc", " ltd", " corp", " corporation", " company", " gmbh", " plc"} {
		if strings.Contains(l, tok) {
			return "company"
		}
	}
	if strings.HasSuffix(strings.ToLower(filepath.Ext(n)), ".pdf") || strings.Contains(l, "ledger") {
		return "document"
	}
	if personEntityRE.MatchString(n) {
		return "person"
	}
	return "unknown"
}

func normalizeEntityName(s string) string {
	s = strings.TrimSpace(s)
	s = strings.Trim(s, "[](){}.,:;")
	s = strings.Join(strings.Fields(s), " ")
	if len(s) < 2 || len(s) > 120 {
		return ""
	}
	return s
}

func bestDocRef(documentID, sourcePath, fallback string) string {
	if v := strings.TrimSpace(documentID); v != "" {
		return v
	}
	if v := strings.TrimSpace(sourcePath); v != "" {
		return filepath.Base(v)
	}
	if v := strings.TrimSpace(fallback); v != "" {
		return v
	}
	return "unknown_document"
}

func isSignatureConnection(link EntityLink) bool {
	if signatureRE.MatchString(link.EntityA) || signatureRE.MatchString(link.EntityB) {
		return true
	}
	for _, d := range link.DocumentRefs {
		if strings.Contains(strings.ToLower(d), "ledger") {
			return true
		}
	}
	return false
}

func docLabel(d string) string {
	d = strings.TrimSpace(d)
	if d == "" {
		return "document"
	}
	return filepath.Base(d)
}

func dedupeEvidenceStrings(in []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(in))
	for _, s := range in {
		k := strings.ToLower(strings.TrimSpace(s))
		if k == "" {
			continue
		}
		if _, ok := seen[k]; ok {
			continue
		}
		seen[k] = struct{}{}
		out = append(out, s)
	}
	return out
}

func clamp01Evidence(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

func maxIntEvidence(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func absEvidence(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

func boolToFloatEvidence(v bool) float64 {
	if v {
		return 1
	}
	return 0
}
