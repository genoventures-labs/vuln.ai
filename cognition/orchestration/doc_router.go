package orchestration

import (
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Thynaptic/P-LMv1/pkg/memory"
)

const (
	defaultRouteTimeout = 1200 * time.Millisecond
)

type RoutingConfig struct {
	MaxDocGroups    int
	MaxChunksPerDoc int
	CandidateK      int
	RouteTimeout    time.Duration

	WeightSemantic float64
	WeightLexical  float64
	WeightTopology float64
	WeightMetadata float64
}

type RoutedDoc struct {
	SourceRef    string
	Score        float64
	SegmentCount int
	Reasons      []string
	Segments     []memory.KnowledgeSegment
}

type RouteReport struct {
	Query             string
	CandidateSegments int
	SelectedDocs      []RoutedDoc
	DroppedDocs       int
	DurationMS        int64
	FallbackUsed      bool
	FallbackReason    string
}

type HierarchyConfig struct {
	MaxSectionsPerDoc int
	MaxTotalSections  int
	MinSectionScore   float64
}

func resolveRoutingConfig() RoutingConfig {
	weights := parseRouteWeightsEnv("TALOS_DOC_ROUTING_WEIGHTS", []float64{0.45, 0.25, 0.20, 0.10})
	return RoutingConfig{
		MaxDocGroups:    maxIntRoute(1, intFromEnvRoute("TALOS_DOC_ROUTING_MAX_DOC_GROUPS", 8)),
		MaxChunksPerDoc: maxIntRoute(1, intFromEnvRoute("TALOS_DOC_ROUTING_MAX_CHUNKS_PER_DOC", 5)),
		CandidateK:      maxIntRoute(8, intFromEnvRoute("TALOS_DOC_ROUTING_CANDIDATE_K", 48)),
		RouteTimeout:    maxDurationRoute(200*time.Millisecond, time.Duration(maxIntRoute(200, intFromEnvRoute("TALOS_DOC_ROUTING_TIMEOUT_MS", 1200)))*time.Millisecond),
		WeightSemantic:  weights[0],
		WeightLexical:   weights[1],
		WeightTopology:  weights[2],
		WeightMetadata:  weights[3],
	}
}

func resolveHierarchyConfig() HierarchyConfig {
	return HierarchyConfig{
		MaxSectionsPerDoc: maxIntRoute(1, intFromEnvRoute("TALOS_HIER_READ_MAX_SECTIONS_PER_DOC", 3)),
		MaxTotalSections:  maxIntRoute(1, intFromEnvRoute("TALOS_HIER_READ_MAX_TOTAL_SECTIONS", 12)),
		MinSectionScore:   clampRoute(floatFromEnvRoute("TALOS_HIER_READ_MIN_SECTION_SCORE", 0.20)),
	}
}

func resolveLongFormReasoningConfig() LongFormReasoningConfig {
	return LongFormReasoningConfig{
		Enabled:              boolFromEnvRoute("TALOS_LONGFORM_ENABLED", true),
		MaxPasses:            maxIntRoute(1, intFromEnvRoute("TALOS_LONGFORM_MAX_PASSES", 3)),
		MaxSectionsPerPass:   maxIntRoute(1, intFromEnvRoute("TALOS_LONGFORM_MAX_SECTIONS_PER_PASS", 8)),
		MaxDuration:          maxDurationRoute(1200*time.Millisecond, time.Duration(maxIntRoute(1200, intFromEnvRoute("TALOS_LONGFORM_TIMEOUT_MS", 6000)))*time.Millisecond),
		TriggerMinComplexity: maxIntRoute(1, intFromEnvRoute("TALOS_LONGFORM_TRIGGER_MIN_COMPLEXITY", 4)),
		TriggerMinSections:   maxIntRoute(1, intFromEnvRoute("TALOS_LONGFORM_TRIGGER_MIN_SECTIONS", 4)),
	}
}

func RouteDocuments(mm *memory.MemoryManager, query string, cfg RoutingConfig) ([]RoutedDoc, RouteReport, error) {
	report := RouteReport{Query: strings.TrimSpace(query)}
	start := time.Now()
	if mm == nil {
		report.FallbackUsed = true
		report.FallbackReason = "memory manager unavailable"
		return nil, report, fmt.Errorf("memory manager is required")
	}
	query = strings.TrimSpace(query)
	if query == "" {
		report.FallbackUsed = true
		report.FallbackReason = "empty query"
		return nil, report, fmt.Errorf("query is empty")
	}
	if cfg.MaxDocGroups <= 0 || cfg.MaxChunksPerDoc <= 0 || cfg.CandidateK <= 0 || cfg.RouteTimeout <= 0 {
		cfg = resolveRoutingConfig()
	}

	segs, err := mm.RetrieveKnowledgeSegments(query, cfg.CandidateK)
	if err != nil {
		report.FallbackUsed = true
		report.FallbackReason = err.Error()
		report.DurationMS = time.Since(start).Milliseconds()
		return nil, report, err
	}
	report.CandidateSegments = len(segs)
	if len(segs) == 0 {
		report.FallbackUsed = true
		report.FallbackReason = "no candidate segments"
		report.DurationMS = time.Since(start).Milliseconds()
		return nil, report, nil
	}

	bySource := map[string][]memory.KnowledgeSegment{}
	for _, s := range segs {
		ref := routeSourceRef(s.Metadata)
		if ref == "" {
			ref = "knowledge_base:unscoped"
		}
		bySource[ref] = append(bySource[ref], s)
	}

	queryTokens := tokenizeRouteText(query)
	routed := make([]RoutedDoc, 0, len(bySource))
	for source, chunks := range bySource {
		if time.Since(start) > cfg.RouteTimeout {
			report.FallbackUsed = true
			report.FallbackReason = "route timeout exceeded"
			report.DurationMS = time.Since(start).Milliseconds()
			return nil, report, fmt.Errorf("route timeout exceeded")
		}
		sort.SliceStable(chunks, func(i, j int) bool { return chunks[i].Similarity > chunks[j].Similarity })
		if len(chunks) > cfg.MaxChunksPerDoc {
			chunks = chunks[:cfg.MaxChunksPerDoc]
		}

		sem := semanticGroupScore(chunks)
		lex := lexicalGroupScore(queryTokens, chunks)
		topo := topologyGroupScore(chunks)
		meta := metadataGroupScore(chunks)
		score := clampRoute((sem * cfg.WeightSemantic) + (lex * cfg.WeightLexical) + (topo * cfg.WeightTopology) + (meta * cfg.WeightMetadata))
		reasons := routeReasons(sem, lex, topo, meta)

		routed = append(routed, RoutedDoc{
			SourceRef:    source,
			Score:        score,
			SegmentCount: len(chunks),
			Reasons:      reasons,
			Segments:     chunks,
		})
	}

	sort.SliceStable(routed, func(i, j int) bool {
		if routed[i].Score == routed[j].Score {
			return routed[i].SourceRef < routed[j].SourceRef
		}
		return routed[i].Score > routed[j].Score
	})
	if len(routed) > cfg.MaxDocGroups {
		report.DroppedDocs = len(routed) - cfg.MaxDocGroups
		routed = routed[:cfg.MaxDocGroups]
	}
	report.SelectedDocs = routed
	report.DurationMS = time.Since(start).Milliseconds()
	return routed, report, nil
}

func CompactRouteReportLines(report RouteReport, limit int) []string {
	if limit <= 0 {
		limit = 6
	}
	out := make([]string, 0, limit+1)
	for i, d := range report.SelectedDocs {
		if i >= limit {
			break
		}
		reasons := strings.Join(d.Reasons, ",")
		if reasons == "" {
			reasons = "score"
		}
		out = append(out, fmt.Sprintf("- %s | score=%.2f | chunks=%d | reasons=%s", d.SourceRef, d.Score, d.SegmentCount, reasons))
	}
	if report.FallbackUsed {
		out = append(out, "fallback="+strings.TrimSpace(report.FallbackReason))
	}
	return out
}

func RouteSections(query string, sections []SectionGroup, cfg HierarchyConfig, base HierarchyReport) ([]SectionGroup, HierarchyReport) {
	report := base
	report.CandidateSections = len(sections)
	if len(sections) == 0 {
		report.Notes = append(report.Notes, "no section candidates")
		return nil, report
	}
	if cfg.MaxSectionsPerDoc <= 0 || cfg.MaxTotalSections <= 0 {
		cfg = resolveHierarchyConfig()
	}

	qtoks := tokenizeRouteText(query)
	scored := make([]SectionGroup, 0, len(sections))
	for _, s := range sections {
		sem := semanticGroupScore(s.Segments)
		lex := lexicalSectionScore(qtoks, s)
		topo := topologyGroupScore(s.Segments)
		meta := metadataGroupScore(s.Segments)
		score := clampRoute((sem * 0.50) + (lex * 0.25) + (topo * 0.15) + (meta * 0.10))
		if score < cfg.MinSectionScore {
			continue
		}
		s.Score = score
		if s.Inferred {
			report.InferenceUsed = true
		}
		scored = append(scored, s)
	}
	if len(scored) == 0 {
		report.Notes = append(report.Notes, "all sections filtered by min score")
		return nil, report
	}

	sort.SliceStable(scored, func(i, j int) bool {
		if scored[i].Score == scored[j].Score {
			if scored[i].SourcePath == scored[j].SourcePath {
				return scored[i].SectionTitle < scored[j].SectionTitle
			}
			return scored[i].SourcePath < scored[j].SourcePath
		}
		return scored[i].Score > scored[j].Score
	})

	usedPerSource := map[string]int{}
	selected := make([]SectionGroup, 0, minIntRoute(len(scored), cfg.MaxTotalSections))
	for _, s := range scored {
		src := strings.TrimSpace(s.SourcePath)
		if usedPerSource[src] >= cfg.MaxSectionsPerDoc {
			continue
		}
		if len(selected) >= cfg.MaxTotalSections {
			break
		}
		usedPerSource[src]++
		selected = append(selected, s)
	}
	report.SelectedSections = len(selected)
	report.DroppedSections = len(sections) - len(selected)
	if len(selected) == 0 {
		report.Notes = append(report.Notes, "section routing selected no sections")
	}
	return selected, report
}

func routeSourceRef(meta map[string]string) string {
	if meta == nil {
		return ""
	}
	for _, key := range []string{"source_path", "source_url", "topology_node", "summary_of_ref"} {
		if v := strings.TrimSpace(meta[key]); v != "" {
			return v
		}
	}
	if ds := strings.TrimSpace(meta["hf_dataset"]); ds != "" {
		split := strings.TrimSpace(meta["hf_split"])
		if split == "" {
			return "hf:" + ds
		}
		return "hf:" + ds + ":" + split
	}
	return strings.TrimSpace(meta["source_type"])
}

func semanticGroupScore(chunks []memory.KnowledgeSegment) float64 {
	if len(chunks) == 0 {
		return 0
	}
	top := chunks[0].Similarity
	sum := 0.0
	for _, c := range chunks {
		sum += c.Similarity
	}
	avg := sum / float64(len(chunks))
	return clampRoute((top * 0.6) + (avg * 0.4))
}

func lexicalGroupScore(queryTokens map[string]bool, chunks []memory.KnowledgeSegment) float64 {
	if len(queryTokens) == 0 || len(chunks) == 0 {
		return 0
	}
	best := 0.0
	for _, c := range chunks {
		toks := tokenizeRouteText(c.Content + " " + c.Metadata["chunk_title"])
		if len(toks) == 0 {
			continue
		}
		match := 0
		for t := range queryTokens {
			if toks[t] {
				match++
			}
		}
		score := float64(match) / float64(len(queryTokens))
		if score > best {
			best = score
		}
	}
	return clampRoute(best)
}

func lexicalSectionScore(queryTokens map[string]bool, s SectionGroup) float64 {
	if len(queryTokens) == 0 || len(s.Segments) == 0 {
		return 0
	}
	best := 0.0
	titleToks := tokenizeRouteText(s.SectionTitle)
	matchTitle := 0
	for t := range queryTokens {
		if titleToks[t] {
			matchTitle++
		}
	}
	if len(queryTokens) > 0 {
		best = float64(matchTitle) / float64(len(queryTokens))
	}
	for _, c := range s.Segments {
		toks := tokenizeRouteText(c.Content + " " + c.Metadata["chunk_title"])
		if len(toks) == 0 {
			continue
		}
		match := 0
		for t := range queryTokens {
			if toks[t] {
				match++
			}
		}
		score := float64(match) / float64(len(queryTokens))
		if score > best {
			best = score
		}
	}
	return clampRoute(best)
}

func topologyGroupScore(chunks []memory.KnowledgeSegment) float64 {
	if len(chunks) == 0 {
		return 0
	}
	withTopo := 0
	for _, c := range chunks {
		if strings.TrimSpace(c.Metadata["topology_node"]) != "" {
			withTopo++
		}
	}
	return clampRoute(float64(withTopo) / float64(len(chunks)))
}

func metadataGroupScore(chunks []memory.KnowledgeSegment) float64 {
	if len(chunks) == 0 {
		return 0
	}
	total := 0.0
	now := time.Now().UTC()
	for _, c := range chunks {
		importance := 0.5
		if raw := strings.TrimSpace(c.Metadata["base_importance"]); raw != "" {
			if v, err := strconv.ParseFloat(raw, 64); err == nil {
				importance = clampRoute(v)
			}
		}
		fresh := 0.5
		if ts := strings.TrimSpace(c.Metadata["timestamp"]); ts != "" {
			if t, err := time.Parse(time.RFC3339, ts); err == nil {
				ageH := now.Sub(t).Hours()
				if ageH <= 0 {
					fresh = 1.0
				} else if ageH >= 24*14 {
					fresh = 0.1
				} else {
					fresh = clampRoute(1.0 - (ageH / (24 * 14)))
				}
			}
		}
		total += (importance * 0.6) + (fresh * 0.4)
	}
	return clampRoute(total / float64(len(chunks)))
}

func routeReasons(sem, lex, topo, meta float64) []string {
	out := make([]string, 0, 4)
	if sem >= 0.6 {
		out = append(out, "semantic_high")
	}
	if lex >= 0.4 {
		out = append(out, "lexical_match")
	}
	if topo >= 0.5 {
		out = append(out, "topology_boost")
	}
	if meta >= 0.6 {
		out = append(out, "metadata_fresh")
	}
	if len(out) == 0 {
		out = append(out, "balanced")
	}
	return out
}

func tokenizeRouteText(s string) map[string]bool {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" {
		return nil
	}
	repl := strings.NewReplacer(
		"\n", " ", "\t", " ", ".", " ", ",", " ", ";", " ", ":", " ", "(", " ", ")", " ",
		"[", " ", "]", " ", "{", " ", "}", " ", "\"", " ", "'", " ", "`", " ", "/", " ",
		"\\", " ", "|", " ", "!", " ", "?", " ", "#", " ", "=", " ", "+", " ", "-", " ",
		"_", " ", "<", " ", ">", " ",
	)
	s = repl.Replace(s)
	stop := map[string]bool{
		"the": true, "and": true, "for": true, "with": true, "that": true, "this": true, "from": true,
		"into": true, "onto": true, "http": true, "https": true, "www": true,
	}
	out := map[string]bool{}
	for _, p := range strings.Fields(s) {
		if len(p) < 3 || stop[p] {
			continue
		}
		out[p] = true
	}
	return out
}

func parseRouteWeightsEnv(key string, fallback []float64) []float64 {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return append([]float64(nil), fallback...)
	}
	parts := strings.Split(raw, ",")
	if len(parts) != len(fallback) {
		return append([]float64(nil), fallback...)
	}
	out := make([]float64, 0, len(parts))
	sum := 0.0
	for _, p := range parts {
		v, err := strconv.ParseFloat(strings.TrimSpace(p), 64)
		if err != nil || v < 0 {
			return append([]float64(nil), fallback...)
		}
		out = append(out, v)
		sum += v
	}
	if sum <= 0 {
		return append([]float64(nil), fallback...)
	}
	for i := range out {
		out[i] = out[i] / sum
	}
	return out
}

func intFromEnvRoute(key string, fallback int) int {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback
	}
	v, err := strconv.Atoi(raw)
	if err != nil {
		return fallback
	}
	return v
}

func floatFromEnvRoute(key string, fallback float64) float64 {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback
	}
	v, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return fallback
	}
	return v
}

func boolFromEnvRoute(key string, fallback bool) bool {
	raw := strings.ToLower(strings.TrimSpace(os.Getenv(key)))
	if raw == "" {
		return fallback
	}
	switch raw {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	default:
		return fallback
	}
}

func clampRoute(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

func maxIntRoute(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func minIntRoute(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func maxDurationRoute(a, b time.Duration) time.Duration {
	if a > b {
		return a
	}
	return b
}
