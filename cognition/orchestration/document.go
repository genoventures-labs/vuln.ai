package orchestration

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Thynaptic/P-LMv1/pkg/memory"
	"github.com/Thynaptic/P-LMv1/pkg/state"
	"github.com/ollama/ollama/api"
)

const (
	mapTimeout    = 20 * time.Second
	reduceTimeout = 30 * time.Second
	maxDocGroups  = 6
	maxChunksPer  = 4
)

var (
	ipPattern     = regexp.MustCompile(`\b(?:\d{1,3}\.){3}\d{1,3}\b`)
	entityPattern = regexp.MustCompile(`\b[A-Z][A-Za-z0-9_.:/-]{2,}\b`)
)

// DocumentOrchestrator coordinates hierarchical reading across indexed documents.
type DocumentOrchestrator struct {
	Memory *memory.MemoryManager
	Client *api.Client
}

// DocGroup contains routed chunks from one source document.
type DocGroup struct {
	SourcePath string
	Segments   []memory.KnowledgeSegment
}

// SectionGroup contains routed chunks for one logical section in a source.
type SectionGroup struct {
	SourcePath   string
	SectionID    string
	SectionTitle string
	Segments     []memory.KnowledgeSegment
	Score        float64
	Inferred     bool
}

// MapSummary is the stage-1 summary for a routed document group.
type MapSummary struct {
	SourcePath string
	Summary    string
	Entities   []string
}

// SectionMapSummary is the stage-1 summary for a routed section group.
type SectionMapSummary struct {
	SourcePath   string
	SectionID    string
	SectionTitle string
	Summary      string
	Entities     []string
	Score        float64
	Inferred     bool
}

type LongFormReasoningConfig struct {
	Enabled              bool
	MaxPasses            int
	MaxSectionsPerPass   int
	MaxDuration          time.Duration
	TriggerMinComplexity int
	TriggerMinSections   int
}

type SectionClaim struct {
	Claim      string
	SectionID  string
	SourcePath string
	Confidence float64
}

type LongFormReasoningReport struct {
	Triggered         bool
	PassesRun         int
	DurationMS        int64
	FallbackUsed      bool
	CitationCoverage  float64
	UnsupportedClaims int
	Notes             []string
}

// HierarchyReport captures section-level routing and fallback details.
type HierarchyReport struct {
	CandidateSections int
	SelectedSections  int
	DroppedSections   int
	InferenceUsed     bool
	Notes             []string
}

// CrossLink captures a shared entity between two sources.
type CrossLink struct {
	Entity   string
	SourceA  string
	SourceB  string
	Strength int
}

// SectionCrossLink captures explicit section-to-section linkage signals.
type SectionCrossLink struct {
	Entity   string
	SourceA  string
	SectionA string
	SourceB  string
	SectionB string
	Strength int
	LinkType string
}

// DocumentSynthesis is the full orchestration output.
type DocumentSynthesis struct {
	StructuredAnswer  string
	MapSummaries      []MapSummary
	SectionMaps       []SectionMapSummary
	Claims            []SectionClaim
	CrossLinks        []CrossLink
	SectionCrossLinks []SectionCrossLink
	RouteReport       RouteReport
	HierarchyReport   HierarchyReport
	ReasoningReport   LongFormReasoningReport
}

// Orchestrate runs multi-document routing, map-reduce synthesis, and cross-linking.
func (d *DocumentOrchestrator) Orchestrate(query string, modelCandidates []string, topK int) (DocumentSynthesis, error) {
	if d == nil || d.Memory == nil {
		return DocumentSynthesis{}, fmt.Errorf("document orchestrator requires memory manager")
	}
	if strings.TrimSpace(query) == "" {
		return DocumentSynthesis{}, fmt.Errorf("query is empty")
	}
	if topK <= 0 {
		topK = 24
	}

	groups, routeReport, err := d.routeRelevantSegments(query, topK)
	if err != nil {
		return DocumentSynthesis{}, err
	}
	if len(groups) == 0 {
		return DocumentSynthesis{}, fmt.Errorf("no relevant document segments found")
	}

	sections, hierarchyReport := buildSectionGroups(groups)
	selectedSections, hierarchyReport := RouteSections(query, sections, resolveHierarchyConfig(), hierarchyReport)
	sectionMaps := d.mapSectionSummaries(query, selectedSections, modelCandidates)
	maps := collapseSectionMaps(sectionMaps)
	links := crossLinkSectionSummaries(sectionMaps)
	sectionLinks := crossLinkSections(sectionMaps)
	answer := d.reduceSectionSummaries(query, sectionMaps, links, modelCandidates)
	longCfg := resolveLongFormReasoningConfig()
	claims := make([]SectionClaim, 0, 8)
	reasoningReport := LongFormReasoningReport{}
	if shouldRunLongFormReasoning(query, sectionMaps, longCfg) {
		longAnswer, longClaims, rep := d.runLongFormSectionReasoning(query, sectionMaps, links, sectionLinks, modelCandidates, longCfg)
		reasoningReport = rep
		claims = append(claims, longClaims...)
		if strings.TrimSpace(longAnswer) != "" {
			answer = strings.TrimSpace(longAnswer)
		}
	} else {
		reasoningReport.Notes = append(reasoningReport.Notes, "long-form reasoning not triggered")
	}

	return DocumentSynthesis{
		StructuredAnswer:  strings.TrimSpace(answer),
		MapSummaries:      maps,
		SectionMaps:       sectionMaps,
		Claims:            claims,
		CrossLinks:        links,
		SectionCrossLinks: sectionLinks,
		RouteReport:       routeReport,
		HierarchyReport:   hierarchyReport,
		ReasoningReport:   reasoningReport,
	}, nil
}

func shouldRunLongFormReasoning(query string, sections []SectionMapSummary, cfg LongFormReasoningConfig) bool {
	if !cfg.Enabled {
		return false
	}
	if len(sections) < cfg.TriggerMinSections {
		return false
	}
	return estimateReasoningComplexity(query) >= cfg.TriggerMinComplexity
}

func (d *DocumentOrchestrator) runLongFormSectionReasoning(query string, sections []SectionMapSummary, links []CrossLink, sectionLinks []SectionCrossLink, modelCandidates []string, cfg LongFormReasoningConfig) (string, []SectionClaim, LongFormReasoningReport) {
	report := LongFormReasoningReport{Triggered: true}
	start := time.Now()
	if len(sections) == 0 {
		report.FallbackUsed = true
		report.Notes = append(report.Notes, "no section summaries available")
		return "", nil, report
	}
	if cfg.MaxPasses <= 0 || cfg.MaxSectionsPerPass <= 0 || cfg.MaxDuration <= 0 {
		cfg = resolveLongFormReasoningConfig()
	}
	ordered := append([]SectionMapSummary(nil), sections...)
	sort.SliceStable(ordered, func(i, j int) bool {
		if ordered[i].Score == ordered[j].Score {
			if ordered[i].SourcePath == ordered[j].SourcePath {
				return ordered[i].SectionTitle < ordered[j].SectionTitle
			}
			return ordered[i].SourcePath < ordered[j].SourcePath
		}
		return ordered[i].Score > ordered[j].Score
	})

	claims := make([]SectionClaim, 0, cfg.MaxPasses*cfg.MaxSectionsPerPass)
	working := ""
	for pass := 1; pass <= cfg.MaxPasses; pass++ {
		if time.Since(start) >= cfg.MaxDuration {
			report.FallbackUsed = true
			report.Notes = append(report.Notes, "long-form timeout budget exhausted")
			break
		}
		window := sliceSectionsForPass(ordered, pass, cfg.MaxSectionsPerPass)
		if len(window) == 0 {
			break
		}
		report.PassesRun++
		answer, passClaims, err := d.runLongFormPass(query, working, window, links, sectionLinks, modelCandidates, cfg.MaxDuration-time.Since(start))
		if err != nil {
			report.FallbackUsed = true
			report.Notes = append(report.Notes, "pass "+strconv.Itoa(pass)+" error: "+err.Error())
			continue
		}
		if strings.TrimSpace(answer) != "" {
			working = strings.TrimSpace(answer)
		}
		if len(passClaims) > 0 {
			claims = append(claims, passClaims...)
		}
	}

	verified, unsupported, coverage := enforceSectionClaimCitations(claims, sections)
	report.UnsupportedClaims = unsupported
	report.CitationCoverage = coverage
	if unsupported > 0 {
		report.Notes = append(report.Notes, fmt.Sprintf("dropped %d uncited claim(s)", unsupported))
	}
	if strings.TrimSpace(working) == "" {
		report.FallbackUsed = true
	}
	report.DurationMS = time.Since(start).Milliseconds()
	if strings.TrimSpace(working) == "" {
		return "", verified, report
	}
	if len(verified) == 0 {
		report.FallbackUsed = true
		report.Notes = append(report.Notes, "no cited claims produced")
		return "", verified, report
	}
	grounded := composeGroundedClaimAnswer(query, working, verified, links, sectionLinks)
	return grounded, verified, report
}

type longFormPassPayload struct {
	Synthesis string         `json:"synthesis"`
	Claims    []SectionClaim `json:"claims"`
}

func (d *DocumentOrchestrator) runLongFormPass(query, prior string, sections []SectionMapSummary, links []CrossLink, sectionLinks []SectionCrossLink, modelCandidates []string, budget time.Duration) (string, []SectionClaim, error) {
	if d == nil {
		return "", nil, fmt.Errorf("document orchestrator is nil")
	}
	if d.Client == nil || len(modelCandidates) == 0 {
		return "", nil, fmt.Errorf("llm client unavailable")
	}
	if budget <= 0 {
		return "", nil, fmt.Errorf("no pass budget remaining")
	}
	system := `You perform long-form cross-section reasoning.
Use only provided section summaries.
Return JSON only: {"synthesis":"...","claims":[{"claim":"...","section_id":"...","source_path":"...","confidence":0.0}]}
Do not include chain-of-thought.`
	user := "Query:\n" + strings.TrimSpace(query) +
		"\n\nPrior synthesis:\n" + strings.TrimSpace(prior) +
		"\n\nSections:\n" + formatSectionWindow(sections) +
		"\n\nCross-links:\n" + formatCrossLinks(links) +
		"\n\nCross-sectional links:\n" + formatSectionCrossLinks(sectionLinks)
	timeout := budget
	if timeout > reduceTimeout {
		timeout = reduceTimeout
	}
	resp, err := callLLMWithCandidates(d.Client, modelCandidates, []api.Message{
		{Role: "system", Content: system},
		{Role: "user", Content: user},
	}, timeout)
	if err != nil {
		return "", nil, err
	}
	resp = strings.TrimSpace(stripCodeFence(resp))
	if resp == "" {
		return "", nil, fmt.Errorf("empty long-form pass response")
	}
	var payload longFormPassPayload
	if unmarshalErr := json.Unmarshal([]byte(resp), &payload); unmarshalErr != nil {
		return "", nil, fmt.Errorf("invalid long-form pass JSON: %w", unmarshalErr)
	}
	for i := range payload.Claims {
		payload.Claims[i].Claim = strings.TrimSpace(payload.Claims[i].Claim)
		payload.Claims[i].SectionID = strings.TrimSpace(payload.Claims[i].SectionID)
		payload.Claims[i].SourcePath = strings.TrimSpace(payload.Claims[i].SourcePath)
		payload.Claims[i].Confidence = clampRoute(payload.Claims[i].Confidence)
	}
	return strings.TrimSpace(payload.Synthesis), payload.Claims, nil
}

func sliceSectionsForPass(in []SectionMapSummary, pass int, maxPerPass int) []SectionMapSummary {
	if pass <= 0 || maxPerPass <= 0 || len(in) == 0 {
		return nil
	}
	start := (pass - 1) * maxPerPass
	if start >= len(in) {
		return nil
	}
	end := start + maxPerPass
	if end > len(in) {
		end = len(in)
	}
	return append([]SectionMapSummary(nil), in[start:end]...)
}

func formatSectionWindow(sections []SectionMapSummary) string {
	if len(sections) == 0 {
		return "[none]"
	}
	var b strings.Builder
	for i, s := range sections {
		title := strings.TrimSpace(s.SectionTitle)
		if title == "" {
			title = "General"
		}
		fmt.Fprintf(&b, "%d) source=%s section_id=%s title=%s score=%.2f\n%s\n\n", i+1, s.SourcePath, s.SectionID, title, s.Score, strings.TrimSpace(s.Summary))
	}
	return strings.TrimSpace(b.String())
}

func formatCrossLinks(links []CrossLink) string {
	if len(links) == 0 {
		return "[none]"
	}
	var b strings.Builder
	for _, l := range links {
		fmt.Fprintf(&b, "- %s: %s <-> %s\n", l.Entity, l.SourceA, l.SourceB)
	}
	return strings.TrimSpace(b.String())
}

func formatSectionCrossLinks(links []SectionCrossLink) string {
	if len(links) == 0 {
		return "[none]"
	}
	var b strings.Builder
	for _, l := range links {
		fmt.Fprintf(&b, "- %s | %s::%s <-> %s::%s | strength=%d | type=%s\n", l.Entity, l.SourceA, l.SectionA, l.SourceB, l.SectionB, l.Strength, l.LinkType)
	}
	return strings.TrimSpace(b.String())
}

func enforceSectionClaimCitations(claims []SectionClaim, sections []SectionMapSummary) ([]SectionClaim, int, float64) {
	if len(claims) == 0 {
		return nil, 0, 0
	}
	valid := make(map[string]bool, len(sections))
	sourceOnly := make(map[string]bool, len(sections))
	for _, s := range sections {
		src := strings.TrimSpace(s.SourcePath)
		sec := strings.TrimSpace(s.SectionID)
		if src == "" {
			continue
		}
		sourceOnly[src] = true
		if sec != "" {
			valid[src+"||"+sec] = true
		}
	}
	out := make([]SectionClaim, 0, len(claims))
	unsupported := 0
	for _, c := range claims {
		claim := strings.TrimSpace(c.Claim)
		src := strings.TrimSpace(c.SourcePath)
		sec := strings.TrimSpace(c.SectionID)
		if claim == "" || src == "" {
			unsupported++
			continue
		}
		if sec != "" && valid[src+"||"+sec] {
			out = append(out, c)
			continue
		}
		if sourceOnly[src] {
			c.SectionID = ""
			out = append(out, c)
			continue
		}
		unsupported++
	}
	total := len(claims)
	coverage := 0.0
	if total > 0 {
		coverage = float64(len(out)) / float64(total)
	}
	return dedupeSectionClaims(out), unsupported, clampRoute(coverage)
}

func dedupeSectionClaims(in []SectionClaim) []SectionClaim {
	if len(in) == 0 {
		return nil
	}
	out := make([]SectionClaim, 0, len(in))
	seen := make(map[string]bool, len(in))
	for _, c := range in {
		key := strings.ToLower(strings.TrimSpace(c.Claim)) + "||" + strings.TrimSpace(c.SourcePath) + "||" + strings.TrimSpace(c.SectionID)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, c)
	}
	return out
}

func composeGroundedClaimAnswer(query, synthesis string, claims []SectionClaim, links []CrossLink, sectionLinks []SectionCrossLink) string {
	if len(claims) == 0 {
		return strings.TrimSpace(synthesis)
	}
	var b strings.Builder
	if strings.TrimSpace(synthesis) != "" {
		b.WriteString(strings.TrimSpace(synthesis))
		b.WriteString("\n\n")
	}
	b.WriteString("Grounded Claims\n")
	for _, c := range claims {
		ref := c.SourcePath
		if strings.TrimSpace(c.SectionID) != "" {
			ref += " :: " + c.SectionID
		}
		b.WriteString("- " + strings.TrimSpace(c.Claim) + " [" + ref + "]\n")
	}
	if len(links) > 0 {
		b.WriteString("\nLinked Evidence\n")
		for _, l := range links {
			b.WriteString("- " + l.Entity + ": " + l.SourceA + " <-> " + l.SourceB + "\n")
		}
	}
	if len(sectionLinks) > 0 {
		b.WriteString("\nCross-Sectional Links\n")
		for _, l := range sectionLinks {
			b.WriteString("- " + l.Entity + ": " + l.SourceA + "::" + l.SectionA + " <-> " + l.SourceB + "::" + l.SectionB + " (" + l.LinkType + ")\n")
		}
	}
	return strings.TrimSpace(b.String())
}

func estimateReasoningComplexity(query string) int {
	q := strings.ToLower(strings.TrimSpace(query))
	if q == "" {
		return 0
	}
	score := 0
	if len(q) > 80 {
		score++
	}
	if len(q) > 160 {
		score++
	}
	if strings.Count(q, " and ") >= 1 {
		score++
	}
	if strings.Count(q, ",") >= 2 {
		score++
	}
	for _, marker := range []string{
		"architecture", "compare", "tradeoff", "cross-section", "cross section", "synthesize", "reason", "evidence", "multi-step", "multi step",
	} {
		if strings.Contains(q, marker) {
			score += 2
		}
	}
	return score
}

func buildSectionGroups(groups []DocGroup) ([]SectionGroup, HierarchyReport) {
	report := HierarchyReport{}
	if len(groups) == 0 {
		report.Notes = append(report.Notes, "no document groups available for sectioning")
		return nil, report
	}
	out := make([]SectionGroup, 0, len(groups)*2)
	for _, g := range groups {
		if len(g.Segments) == 0 {
			continue
		}
		segs := append([]memory.KnowledgeSegment(nil), g.Segments...)
		sort.SliceStable(segs, func(i, j int) bool {
			ii := parseMetaInt(segs[i].Metadata, "chunk_index", 0)
			jj := parseMetaInt(segs[j].Metadata, "chunk_index", 0)
			if ii == jj {
				return segs[i].Similarity > segs[j].Similarity
			}
			if ii == 0 {
				return false
			}
			if jj == 0 {
				return true
			}
			return ii < jj
		})

		byID := make(map[string]*SectionGroup)
		order := make([]string, 0, 4)
		sectionSeq := 0
		currentID := ""
		currentTitle := "General"
		for _, s := range segs {
			id := strings.TrimSpace(s.Metadata["section_id"])
			title := strings.TrimSpace(s.Metadata["section_title"])
			level := strings.TrimSpace(s.Metadata["section_inferred"])
			inferred := strings.EqualFold(level, "true")
			if id == "" {
				title = inferSectionTitleFromChunk(s.Content, currentTitle)
				if strings.TrimSpace(title) != "" && strings.TrimSpace(title) != strings.TrimSpace(currentTitle) {
					sectionSeq++
				}
				if sectionSeq == 0 {
					sectionSeq = 1
				}
				if strings.TrimSpace(title) == "" {
					title = "General"
				}
				id = fmt.Sprintf("%s::section-%d", strings.TrimSpace(g.SourcePath), sectionSeq)
				currentID = id
				currentTitle = title
				inferred = true
				report.InferenceUsed = true
			}
			if strings.TrimSpace(id) == "" {
				if currentID == "" {
					currentID = fmt.Sprintf("%s::section-1", strings.TrimSpace(g.SourcePath))
				}
				id = currentID
			}
			if strings.TrimSpace(title) == "" {
				if strings.TrimSpace(currentTitle) == "" {
					title = "General"
				} else {
					title = currentTitle
				}
			}

			slot, ok := byID[id]
			if !ok {
				slot = &SectionGroup{
					SourcePath:   g.SourcePath,
					SectionID:    id,
					SectionTitle: title,
					Inferred:     inferred,
				}
				byID[id] = slot
				order = append(order, id)
			}
			slot.Segments = append(slot.Segments, s)
			slot.Inferred = slot.Inferred || inferred
		}
		for _, id := range order {
			out = append(out, *byID[id])
		}
	}
	report.CandidateSections = len(out)
	if len(out) == 0 {
		report.Notes = append(report.Notes, "sectioning produced no candidate sections")
	}
	return out, report
}

func (d *DocumentOrchestrator) routeRelevantSegments(query string, topK int) ([]DocGroup, RouteReport, error) {
	cfg := resolveRoutingConfig()
	if topK > cfg.CandidateK {
		cfg.CandidateK = topK
	}
	routed, report, err := RouteDocuments(d.Memory, query, cfg)
	if err == nil && len(routed) > 0 {
		groups := make([]DocGroup, 0, len(routed))
		for _, rd := range routed {
			groups = append(groups, DocGroup{
				SourcePath: rd.SourceRef,
				Segments:   rd.Segments,
			})
		}
		return groups, report, nil
	}
	report.FallbackUsed = true
	if strings.TrimSpace(report.FallbackReason) == "" {
		if err != nil {
			report.FallbackReason = err.Error()
		} else {
			report.FallbackReason = "advanced routing returned no groups"
		}
	}

	groups, baseErr := d.routeRelevantSegmentsBasic(query, topK)
	if baseErr != nil {
		return nil, report, baseErr
	}
	if len(groups) == 0 {
		return nil, report, nil
	}
	if len(report.SelectedDocs) == 0 {
		report.SelectedDocs = make([]RoutedDoc, 0, len(groups))
		for _, g := range groups {
			score := 0.0
			for _, s := range g.Segments {
				score += s.Similarity
			}
			report.SelectedDocs = append(report.SelectedDocs, RoutedDoc{
				SourceRef:    g.SourcePath,
				Score:        clampRoute(score),
				SegmentCount: len(g.Segments),
				Reasons:      []string{"fallback_simple_route"},
				Segments:     g.Segments,
			})
		}
	}
	return groups, report, nil
}

func (d *DocumentOrchestrator) routeRelevantSegmentsBasic(query string, topK int) ([]DocGroup, error) {
	segs, err := d.Memory.RetrieveKnowledgeSegments(query, topK)
	if err != nil {
		return nil, err
	}
	if len(segs) == 0 {
		return nil, nil
	}

	bySource := make(map[string][]memory.KnowledgeSegment)
	for _, s := range segs {
		source := strings.TrimSpace(s.Metadata["source_path"])
		if source == "" {
			source = "knowledge_base:unscoped"
		}
		bySource[source] = append(bySource[source], s)
	}

	type sourceScore struct {
		Source string
		Score  float64
	}
	var ranking []sourceScore
	for source, chunks := range bySource {
		score := 0.0
		for _, c := range chunks {
			score += c.Similarity
		}
		ranking = append(ranking, sourceScore{Source: source, Score: score})
	}
	sort.Slice(ranking, func(i, j int) bool { return ranking[i].Score > ranking[j].Score })
	if len(ranking) > maxDocGroups {
		ranking = ranking[:maxDocGroups]
	}

	var groups []DocGroup
	for _, r := range ranking {
		chunks := bySource[r.Source]
		sort.Slice(chunks, func(i, j int) bool { return chunks[i].Similarity > chunks[j].Similarity })
		if len(chunks) > maxChunksPer {
			chunks = chunks[:maxChunksPer]
		}
		groups = append(groups, DocGroup{
			SourcePath: r.Source,
			Segments:   chunks,
		})
	}
	return groups, nil
}

func (d *DocumentOrchestrator) mapSummaries(query string, groups []DocGroup, modelCandidates []string) []MapSummary {
	out := make([]MapSummary, 0, len(groups))
	for _, g := range groups {
		summary := d.mapSingleSource(query, g, modelCandidates)
		entities := extractEntities(summary)
		if len(entities) == 0 {
			entities = extractEntities(joinSegments(g.Segments))
		}
		out = append(out, MapSummary{
			SourcePath: g.SourcePath,
			Summary:    summary,
			Entities:   entities,
		})
	}
	return out
}

func (d *DocumentOrchestrator) mapSectionSummaries(query string, sections []SectionGroup, modelCandidates []string) []SectionMapSummary {
	out := make([]SectionMapSummary, 0, len(sections))
	for _, s := range sections {
		summary := d.mapSingleSection(query, s, modelCandidates)
		entities := extractEntities(summary)
		if len(entities) == 0 {
			entities = extractEntities(joinSegments(s.Segments))
		}
		out = append(out, SectionMapSummary{
			SourcePath:   s.SourcePath,
			SectionID:    s.SectionID,
			SectionTitle: s.SectionTitle,
			Summary:      summary,
			Entities:     entities,
			Score:        s.Score,
			Inferred:     s.Inferred,
		})
	}
	return out
}

func (d *DocumentOrchestrator) mapSingleSection(query string, s SectionGroup, modelCandidates []string) string {
	contextText := joinSegments(s.Segments)
	if strings.TrimSpace(contextText) == "" {
		return "No extractable content for section."
	}
	title := strings.TrimSpace(s.SectionTitle)
	if title == "" {
		title = "General"
	}
	fallback := "Section " + title + " (" + s.SourcePath + "):\n" + truncate(contextText, 420)
	if d.Client == nil || len(modelCandidates) == 0 {
		return fallback
	}

	system := `You are a section mapper.
Summarize the provided section chunk set into 2-4 concise bullets.
Focus on claims, interfaces, constraints, and explicit evidence.
Do not include chain-of-thought.`
	user := "Query:\n" + query + "\n\nSource: " + s.SourcePath + "\nSection: " + title + "\n\nChunks:\n" + contextText

	resp, err := callLLMWithCandidates(d.Client, modelCandidates, []api.Message{
		{Role: "system", Content: system},
		{Role: "user", Content: user},
	}, mapTimeout)
	if err != nil {
		return fallback
	}
	resp = strings.TrimSpace(resp)
	if resp == "" {
		return fallback
	}
	return resp
}

func (d *DocumentOrchestrator) mapSingleSource(query string, g DocGroup, modelCandidates []string) string {
	contextText := joinSegments(g.Segments)
	if strings.TrimSpace(contextText) == "" {
		return "No extractable content for source."
	}

	fallback := "Source " + g.SourcePath + ":\n" + truncate(contextText, 420)
	if d.Client == nil || len(modelCandidates) == 0 {
		return fallback
	}

	system := `You are a document mapper.
Summarize the provided source chunk set into 4-6 concise bullets.
Focus on architecture, responsibilities, interfaces, and constraints.
Do not include chain-of-thought.`
	user := "Query:\n" + query + "\n\nSource: " + g.SourcePath + "\n\nChunks:\n" + contextText

	resp, err := callLLMWithCandidates(d.Client, modelCandidates, []api.Message{
		{Role: "system", Content: system},
		{Role: "user", Content: user},
	}, mapTimeout)
	if err != nil {
		return fallback
	}
	resp = strings.TrimSpace(resp)
	if resp == "" {
		return fallback
	}
	return resp
}

func collapseSectionMaps(sections []SectionMapSummary) []MapSummary {
	if len(sections) == 0 {
		return nil
	}
	bySource := make(map[string][]SectionMapSummary)
	order := make([]string, 0, len(sections))
	seen := make(map[string]bool)
	for _, s := range sections {
		src := strings.TrimSpace(s.SourcePath)
		if src == "" {
			src = "knowledge_base:unscoped"
		}
		if !seen[src] {
			seen[src] = true
			order = append(order, src)
		}
		bySource[src] = append(bySource[src], s)
	}

	out := make([]MapSummary, 0, len(bySource))
	for _, src := range order {
		arr := bySource[src]
		sort.SliceStable(arr, func(i, j int) bool { return arr[i].Score > arr[j].Score })
		var b strings.Builder
		entitySet := make(map[string]bool)
		for _, s := range arr {
			title := strings.TrimSpace(s.SectionTitle)
			if title == "" {
				title = "General"
			}
			fmt.Fprintf(&b, "[%s] %s\n", title, strings.TrimSpace(s.Summary))
			for _, e := range s.Entities {
				entitySet[e] = true
			}
		}
		entities := make([]string, 0, len(entitySet))
		for e := range entitySet {
			entities = append(entities, e)
		}
		sort.Strings(entities)
		out = append(out, MapSummary{
			SourcePath: src,
			Summary:    strings.TrimSpace(b.String()),
			Entities:   entities,
		})
	}
	return out
}

func crossLinkSectionSummaries(sections []SectionMapSummary) []CrossLink {
	if len(sections) == 0 {
		return nil
	}
	type sourceEntity struct {
		Source string
	}
	entitySources := make(map[string][]sourceEntity)
	for _, s := range sections {
		for _, e := range s.Entities {
			entitySources[e] = append(entitySources[e], sourceEntity{Source: s.SourcePath})
		}
	}
	var out []CrossLink
	seen := make(map[string]bool)
	for entity, refs := range entitySources {
		if len(refs) < 2 {
			continue
		}
		for i := 0; i < len(refs); i++ {
			for j := i + 1; j < len(refs); j++ {
				a := strings.TrimSpace(refs[i].Source)
				b := strings.TrimSpace(refs[j].Source)
				if a == "" || b == "" || a == b {
					continue
				}
				key := entity + "||" + minStr(a, b) + "||" + maxStr(a, b)
				if seen[key] {
					continue
				}
				seen[key] = true
				out = append(out, CrossLink{
					Entity:   entity,
					SourceA:  a,
					SourceB:  b,
					Strength: 1,
				})
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Entity == out[j].Entity {
			if out[i].SourceA == out[j].SourceA {
				return out[i].SourceB < out[j].SourceB
			}
			return out[i].SourceA < out[j].SourceA
		}
		return out[i].Entity < out[j].Entity
	})
	if len(out) > 12 {
		out = out[:12]
	}
	return out
}

func crossLinkSections(sections []SectionMapSummary) []SectionCrossLink {
	if len(sections) < 2 {
		return nil
	}
	out := make([]SectionCrossLink, 0, 16)
	seen := make(map[string]bool)
	for i := 0; i < len(sections); i++ {
		for j := i + 1; j < len(sections); j++ {
			a := sections[i]
			b := sections[j]
			srcA := strings.TrimSpace(a.SourcePath)
			srcB := strings.TrimSpace(b.SourcePath)
			if srcA == "" || srcB == "" {
				continue
			}
			secA := strings.TrimSpace(a.SectionID)
			secB := strings.TrimSpace(b.SectionID)
			if secA == "" {
				secA = strings.TrimSpace(a.SectionTitle)
			}
			if secB == "" {
				secB = strings.TrimSpace(b.SectionTitle)
			}
			if secA == "" {
				secA = "general"
			}
			if secB == "" {
				secB = "general"
			}
			if srcA == srcB && secA == secB {
				continue
			}

			shared, score := sharedSectionSignals(a, b)
			if len(shared) == 0 && score < 0.34 {
				continue
			}
			entity := "lexical_overlap"
			strength := maxIntRoute(scoreToStrength(score), 1)
			if len(shared) > 0 {
				sort.Strings(shared)
				entity = shared[0]
				strength = len(shared)
			}
			linkType := "inter_source"
			if srcA == srcB {
				linkType = "intra_source"
			}
			key := minStr(srcA+"::"+secA, srcB+"::"+secB) + "||" + maxStr(srcA+"::"+secA, srcB+"::"+secB) + "||" + entity
			if seen[key] {
				continue
			}
			seen[key] = true
			out = append(out, SectionCrossLink{
				Entity:   entity,
				SourceA:  srcA,
				SectionA: secA,
				SourceB:  srcB,
				SectionB: secB,
				Strength: strength,
				LinkType: linkType,
			})
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Strength == out[j].Strength {
			if out[i].SourceA == out[j].SourceA {
				if out[i].SectionA == out[j].SectionA {
					if out[i].SourceB == out[j].SourceB {
						return out[i].SectionB < out[j].SectionB
					}
					return out[i].SourceB < out[j].SourceB
				}
				return out[i].SectionA < out[j].SectionA
			}
			return out[i].SourceA < out[j].SourceA
		}
		return out[i].Strength > out[j].Strength
	})
	if len(out) > 24 {
		out = out[:24]
	}
	return out
}

// BuildSectionCrossLinksFromSegments derives cross-sectional links without LLM calls.
func BuildSectionCrossLinksFromSegments(segments []memory.KnowledgeSegment, limit int) []SectionCrossLink {
	if len(segments) < 2 {
		return nil
	}
	type acc struct {
		SourcePath   string
		SectionID    string
		SectionTitle string
		ScoreSum     float64
		Count        int
		EntitySet    map[string]bool
	}
	bySection := make(map[string]*acc, len(segments))
	for _, seg := range segments {
		src := strings.TrimSpace(seg.Metadata["source_path"])
		if src == "" {
			src = strings.TrimSpace(seg.Metadata["source_url"])
		}
		if src == "" {
			src = strings.TrimSpace(seg.Metadata["topology_node"])
		}
		if src == "" {
			continue
		}
		secID := strings.TrimSpace(seg.Metadata["section_id"])
		title := strings.TrimSpace(seg.Metadata["section_title"])
		if secID == "" {
			if title == "" {
				title = inferSectionTitleFromChunk(seg.Content, "General")
			}
			if title == "" {
				title = "General"
			}
			secID = src + "::" + title
		}
		key := src + "||" + secID
		slot, ok := bySection[key]
		if !ok {
			slot = &acc{
				SourcePath:   src,
				SectionID:    secID,
				SectionTitle: title,
				EntitySet:    map[string]bool{},
			}
			bySection[key] = slot
		}
		slot.ScoreSum += seg.Similarity
		slot.Count++
		for _, e := range extractEntities(seg.Content) {
			slot.EntitySet[e] = true
		}
	}
	if len(bySection) < 2 {
		return nil
	}

	summaries := make([]SectionMapSummary, 0, len(bySection))
	for _, v := range bySection {
		entities := make([]string, 0, len(v.EntitySet))
		for e := range v.EntitySet {
			entities = append(entities, e)
		}
		sort.Strings(entities)
		score := 0.5
		if v.Count > 0 {
			score = clampRoute(v.ScoreSum / float64(v.Count))
		}
		summaries = append(summaries, SectionMapSummary{
			SourcePath:   v.SourcePath,
			SectionID:    v.SectionID,
			SectionTitle: v.SectionTitle,
			Entities:     entities,
			Score:        score,
		})
	}
	sort.SliceStable(summaries, func(i, j int) bool { return summaries[i].Score > summaries[j].Score })
	links := crossLinkSections(summaries)
	if limit > 0 && len(links) > limit {
		links = links[:limit]
	}
	return links
}

func sharedSectionSignals(a, b SectionMapSummary) ([]string, float64) {
	aset := make(map[string]bool, len(a.Entities))
	for _, e := range a.Entities {
		e = strings.TrimSpace(e)
		if e == "" {
			continue
		}
		aset[e] = true
	}
	shared := make([]string, 0, 4)
	for _, e := range b.Entities {
		e = strings.TrimSpace(e)
		if e == "" {
			continue
		}
		if aset[e] {
			shared = append(shared, e)
		}
	}
	at := tokenizeRouteText(a.SectionTitle + " " + a.Summary)
	bt := tokenizeRouteText(b.SectionTitle + " " + b.Summary)
	if len(at) == 0 || len(bt) == 0 {
		return shared, 0
	}
	inter := 0
	for t := range at {
		if bt[t] {
			inter++
		}
	}
	denom := len(at)
	if len(bt) < denom {
		denom = len(bt)
	}
	if denom <= 0 {
		return shared, 0
	}
	return shared, clampRoute(float64(inter) / float64(denom))
}

func scoreToStrength(score float64) int {
	if score <= 0 {
		return 0
	}
	v := int(score * 10)
	if v < 1 {
		v = 1
	}
	return v
}

func (d *DocumentOrchestrator) reduceSummaries(query string, summaries []MapSummary, links []CrossLink, modelCandidates []string) string {
	if len(summaries) == 0 {
		return "No relevant documents found."
	}

	var b strings.Builder
	b.WriteString("Mapped Sources:\n")
	for i, s := range summaries {
		fmt.Fprintf(&b, "%d) %s\n%s\n\n", i+1, s.SourcePath, strings.TrimSpace(s.Summary))
	}
	if len(links) > 0 {
		b.WriteString("Cross-links:\n")
		for _, l := range links {
			fmt.Fprintf(&b, "- %s: %s <-> %s\n", l.Entity, l.SourceA, l.SourceB)
		}
	}
	payload := b.String()

	fallback := fallbackReduce(query, summaries, links)
	if d.Client == nil || len(modelCandidates) == 0 {
		return fallback
	}

	system := `You are a reduce-stage synthesizer for hierarchical document reasoning.
Produce a structured response with sections:
1) Architecture Overview
2) Key Components
3) Data/Control Flow
4) Risks or Open Questions
5) Linked Evidence
Be concise and high-signal.`
	user := "User Query:\n" + query + "\n\nInputs:\n" + payload

	resp, err := callLLMWithCandidates(d.Client, modelCandidates, []api.Message{
		{Role: "system", Content: system},
		{Role: "user", Content: user},
	}, reduceTimeout)
	if err != nil {
		return fallback
	}
	resp = strings.TrimSpace(resp)
	if resp == "" {
		return fallback
	}
	return resp
}

func (d *DocumentOrchestrator) reduceSectionSummaries(query string, summaries []SectionMapSummary, links []CrossLink, modelCandidates []string) string {
	if len(summaries) == 0 {
		return "No relevant sections found."
	}
	sort.SliceStable(summaries, func(i, j int) bool {
		if summaries[i].Score == summaries[j].Score {
			if summaries[i].SourcePath == summaries[j].SourcePath {
				return summaries[i].SectionTitle < summaries[j].SectionTitle
			}
			return summaries[i].SourcePath < summaries[j].SourcePath
		}
		return summaries[i].Score > summaries[j].Score
	})

	var b strings.Builder
	b.WriteString("Mapped Sections:\n")
	for i, s := range summaries {
		fmt.Fprintf(&b, "%d) %s :: %s (score=%.2f)\n%s\n\n", i+1, s.SourcePath, strings.TrimSpace(s.SectionTitle), s.Score, strings.TrimSpace(s.Summary))
	}
	if len(links) > 0 {
		b.WriteString("Cross-links:\n")
		for _, l := range links {
			fmt.Fprintf(&b, "- %s: %s <-> %s\n", l.Entity, l.SourceA, l.SourceB)
		}
	}
	payload := b.String()

	fallback := fallbackReduce(query, collapseSectionMaps(summaries), links)
	if d.Client == nil || len(modelCandidates) == 0 {
		return fallback
	}

	system := `You are a reduce-stage synthesizer for hierarchical document reasoning.
Produce a structured response with sections:
1) Architecture Overview
2) Key Components
3) Data/Control Flow
4) Risks or Open Questions
5) Linked Evidence
Be concise and high-signal.`
	user := "User Query:\n" + query + "\n\nInputs:\n" + payload

	resp, err := callLLMWithCandidates(d.Client, modelCandidates, []api.Message{
		{Role: "system", Content: system},
		{Role: "user", Content: user},
	}, reduceTimeout)
	if err != nil {
		return fallback
	}
	resp = strings.TrimSpace(resp)
	if resp == "" {
		return fallback
	}
	return resp
}

func crossLinkSummaries(summaries []MapSummary) []CrossLink {
	type sourceEntity struct {
		Source string
	}

	entitySources := make(map[string][]sourceEntity)
	for _, s := range summaries {
		for _, e := range s.Entities {
			entitySources[e] = append(entitySources[e], sourceEntity{Source: s.SourcePath})
		}
	}

	var out []CrossLink
	seen := make(map[string]bool)
	for entity, refs := range entitySources {
		if len(refs) < 2 {
			continue
		}
		for i := 0; i < len(refs); i++ {
			for j := i + 1; j < len(refs); j++ {
				a, b := refs[i].Source, refs[j].Source
				if a == b {
					continue
				}
				key := entity + "||" + minStr(a, b) + "||" + maxStr(a, b)
				if seen[key] {
					continue
				}
				seen[key] = true
				out = append(out, CrossLink{
					Entity:   entity,
					SourceA:  a,
					SourceB:  b,
					Strength: 1,
				})
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Entity == out[j].Entity {
			if out[i].SourceA == out[j].SourceA {
				return out[i].SourceB < out[j].SourceB
			}
			return out[i].SourceA < out[j].SourceA
		}
		return out[i].Entity < out[j].Entity
	})
	if len(out) > 12 {
		out = out[:12]
	}
	return out
}

func extractEntities(text string) []string {
	if strings.TrimSpace(text) == "" {
		return nil
	}
	entities := make(map[string]bool)

	for _, ip := range ipPattern.FindAllString(text, -1) {
		entities[ip] = true
	}
	for _, ent := range entityPattern.FindAllString(text, -1) {
		n := strings.TrimSpace(ent)
		l := strings.ToLower(n)
		if len(n) < 3 {
			continue
		}
		if l == "http" || l == "https" || l == "json" {
			continue
		}
		entities[n] = true
	}

	var out []string
	for e := range entities {
		out = append(out, e)
	}
	sort.Strings(out)
	if len(out) > 20 {
		out = out[:20]
	}
	return out
}

func joinSegments(segs []memory.KnowledgeSegment) string {
	var b strings.Builder
	for i, s := range segs {
		fmt.Fprintf(&b, "[chunk %d | sim %.2f | source %s", i+1, s.Similarity, s.Metadata["source_path"])
		if idx := strings.TrimSpace(s.Metadata["chunk_index"]); idx != "" {
			if total := strings.TrimSpace(s.Metadata["chunk_total"]); total != "" {
				fmt.Fprintf(&b, " | %s/%s", idx, total)
			}
		}
		b.WriteString("]\n")
		b.WriteString(strings.TrimSpace(s.Content))
		b.WriteString("\n\n")
	}
	return strings.TrimSpace(b.String())
}

func inferSectionTitleFromChunk(chunk string, fallback string) string {
	lines := strings.Split(strings.TrimSpace(chunk), "\n")
	for _, line := range lines {
		clean := strings.TrimSpace(line)
		if clean == "" {
			continue
		}
		if strings.HasPrefix(clean, "#") {
			clean = strings.TrimSpace(strings.TrimLeft(clean, "#"))
			if clean != "" {
				return truncate(clean, 96)
			}
		}
		lower := strings.ToLower(clean)
		if strings.HasPrefix(lower, "section:") {
			title := strings.TrimSpace(clean[len("section:"):])
			if title != "" {
				return truncate(title, 96)
			}
		}
		break
	}
	return strings.TrimSpace(fallback)
}

func CompactHierarchySummaryLines(report HierarchyReport, sections []SectionMapSummary, limit int) []string {
	if limit <= 0 {
		limit = 5
	}
	out := []string{
		fmt.Sprintf("sections=%d selected=%d dropped=%d inferred=%t", report.CandidateSections, report.SelectedSections, report.DroppedSections, report.InferenceUsed),
	}
	if len(sections) == 0 {
		return out
	}
	sorted := append([]SectionMapSummary(nil), sections...)
	sort.SliceStable(sorted, func(i, j int) bool {
		if sorted[i].Score == sorted[j].Score {
			if sorted[i].SourcePath == sorted[j].SourcePath {
				return sorted[i].SectionTitle < sorted[j].SectionTitle
			}
			return sorted[i].SourcePath < sorted[j].SourcePath
		}
		return sorted[i].Score > sorted[j].Score
	})
	for i, s := range sorted {
		if i >= limit {
			break
		}
		title := strings.TrimSpace(s.SectionTitle)
		if title == "" {
			title = "General"
		}
		out = append(out, fmt.Sprintf("- %s :: %s | score=%.2f", s.SourcePath, title, s.Score))
	}
	return out
}

func CompactSectionCrossLinkLines(links []SectionCrossLink, limit int) []string {
	if limit <= 0 {
		limit = 6
	}
	if len(links) == 0 {
		return nil
	}
	out := make([]string, 0, minIntRoute(limit, len(links)))
	for i, l := range links {
		if i >= limit {
			break
		}
		out = append(out, fmt.Sprintf("- %s | %s::%s <-> %s::%s | strength=%d | type=%s", l.Entity, l.SourceA, l.SectionA, l.SourceB, l.SectionB, l.Strength, l.LinkType))
	}
	return out
}

func parseMetaInt(meta map[string]string, key string, fallback int) int {
	if meta == nil {
		return fallback
	}
	raw := strings.TrimSpace(meta[key])
	if raw == "" {
		return fallback
	}
	v, err := strconv.Atoi(raw)
	if err != nil {
		return fallback
	}
	return v
}

func fallbackReduce(query string, summaries []MapSummary, links []CrossLink) string {
	var b strings.Builder
	b.WriteString("Architecture Overview\n")
	b.WriteString("- Query: " + strings.TrimSpace(query) + "\n")
	b.WriteString("- Sources analyzed: " + strconv.Itoa(len(summaries)) + "\n\n")

	b.WriteString("Key Components\n")
	for _, s := range summaries {
		b.WriteString("- " + s.SourcePath + ": " + truncate(strings.ReplaceAll(strings.TrimSpace(s.Summary), "\n", " "), 180) + "\n")
	}

	if len(links) > 0 {
		b.WriteString("\nLinked Evidence\n")
		for _, l := range links {
			b.WriteString("- " + l.Entity + " appears in " + l.SourceA + " and " + l.SourceB + "\n")
		}
	}
	return strings.TrimSpace(b.String())
}

func callLLMWithCandidates(client *api.Client, models []string, messages []api.Message, timeout time.Duration) (string, error) {
	if client == nil || len(models) == 0 {
		return "", fmt.Errorf("missing llm client or model candidates")
	}
	var lastErr error
	for _, model := range models {
		prompt := ""
		if len(messages) > 0 {
			prompt = messages[len(messages)-1].Content
		}
		opts, _ := state.ResolveEntropyOptions(prompt)
		req := &api.ChatRequest{
			Model:    model,
			Options:  opts,
			Messages: messages,
		}
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		var out strings.Builder
		err := client.Chat(ctx, req, func(resp api.ChatResponse) error {
			out.WriteString(resp.Message.Content)
			return nil
		})
		cancel()
		if err == nil {
			return strings.TrimSpace(stripCodeFence(out.String())), nil
		}
		lastErr = err
	}
	return "", lastErr
}

func stripCodeFence(s string) string {
	t := strings.TrimSpace(s)
	if !strings.HasPrefix(t, "```") || !strings.HasSuffix(t, "```") {
		return t
	}
	lines := strings.Split(t, "\n")
	if len(lines) < 3 {
		return t
	}
	raw := strings.TrimSpace(strings.Join(lines[1:len(lines)-1], "\n"))
	if json.Valid([]byte(raw)) {
		return raw
	}
	return raw
}

func truncate(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	if n < 4 {
		return s[:n]
	}
	return s[:n-3] + "..."
}

func minStr(a, b string) string {
	if a <= b {
		return a
	}
	return b
}

func maxStr(a, b string) string {
	if a >= b {
		return a
	}
	return b
}
