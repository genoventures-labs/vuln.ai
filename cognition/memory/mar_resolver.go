package memory

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

const (
	anchorKindHistory   = "history"
	anchorKindKnowledge = "knowledge"
)

// AnchoredTrace is one scored memory anchor used during reasoning context build.
type AnchoredTrace struct {
	ID       string
	Kind     string
	Content  string
	Score    float64
	Metadata map[string]string
}

// AnchoredContext represents memory-anchored reasoning state for a query.
type AnchoredContext struct {
	Query          string
	History        []string
	Knowledge      []string
	Anchors        []AnchoredTrace
	CacheHit       bool
	CandidateCount int
	Policy         MARPolicy
}

// ResolveAnchoredContext builds a deterministic memory-anchored context for reasoning.
func (mm *MemoryManager) ResolveAnchoredContext(query string, historyK int, knowledgeK int) (AnchoredContext, error) {
	if mm == nil {
		return AnchoredContext{}, fmt.Errorf("memory manager is nil")
	}
	query = strings.TrimSpace(query)
	if query == "" {
		return AnchoredContext{}, fmt.Errorf("query is empty")
	}
	if historyK < 0 || knowledgeK < 0 {
		return AnchoredContext{}, fmt.Errorf("historyK and knowledgeK must be >= 0")
	}
	policy := mm.marPolicy
	if !policy.Enabled {
		history, hErr := mm.RetrieveDynamicContext(query, historyK)
		knowledge, kErr := mm.RetrieveKnowledge(query, knowledgeK)
		if hErr != nil {
			return AnchoredContext{}, hErr
		}
		if kErr != nil {
			return AnchoredContext{}, kErr
		}
		return AnchoredContext{Query: query, History: history, Knowledge: knowledge, Policy: policy}, nil
	}

	cacheKey := marCacheKey(mm.ActiveNamespace(), query, historyK, knowledgeK, policy)
	if cached, ok := mm.marCache.get(cacheKey); ok {
		cached.CacheHit = true
		return cached, nil
	}

	historyCount := 0
	if mm.historyCollection != nil {
		historyCount = mm.historyCollection.Count()
	}
	knowledgeCount := 0
	if mm.knowledgeCollection != nil {
		knowledgeCount = mm.knowledgeCollection.Count()
	}

	historyLimit := minInt(policy.CandidateLimit, maxInt(historyK*6, historyK))
	knowledgeLimit := minInt(policy.CandidateLimit, maxInt(knowledgeK*6, knowledgeK))
	if historyLimit <= 0 {
		historyLimit = minInt(policy.CandidateLimit, maxInt(historyK, 1))
	}
	if knowledgeLimit <= 0 {
		knowledgeLimit = minInt(policy.CandidateLimit, maxInt(knowledgeK, 1))
	}
	if historyCount > 0 {
		historyLimit = minInt(historyLimit, historyCount)
	}
	if knowledgeCount > 0 {
		knowledgeLimit = minInt(knowledgeLimit, knowledgeCount)
	}

	historyCandidates := []retrievalCandidate{}
	if historyK > 0 && historyCount > 0 {
		cands, err := mm.retrieveCandidates(mm.historyCollection, mm.historyBM25, query, historyLimit)
		if err != nil {
			return AnchoredContext{}, fmt.Errorf("history retrieval failed: %w", err)
		}
		historyCandidates = cands
	}
	knowledgeCandidates := []retrievalCandidate{}
	if knowledgeK > 0 && knowledgeCount > 0 {
		cands, err := mm.retrieveCandidates(mm.knowledgeCollection, mm.knowledgeBM25, query, knowledgeLimit)
		if err != nil {
			return AnchoredContext{}, fmt.Errorf("knowledge retrieval failed: %w", err)
		}
		knowledgeCandidates = cands
	}

	now := time.Now().UTC()
	historyAnchors := scoreAnchoredCandidates(historyCandidates, anchorKindHistory, now, policy)
	knowledgeAnchors := scoreAnchoredCandidates(knowledgeCandidates, anchorKindKnowledge, now, policy)

	history := selectAnchoredContent(historyAnchors, historyK, policy)
	knowledge := selectAnchoredContent(knowledgeAnchors, knowledgeK, policy)

	allAnchors := make([]AnchoredTrace, 0, len(historyAnchors)+len(knowledgeAnchors))
	allAnchors = append(allAnchors, historyAnchors...)
	allAnchors = append(allAnchors, knowledgeAnchors...)
	sort.SliceStable(allAnchors, func(i, j int) bool {
		if allAnchors[i].Score == allAnchors[j].Score {
			if allAnchors[i].Kind == allAnchors[j].Kind {
				return allAnchors[i].ID < allAnchors[j].ID
			}
			return allAnchors[i].Kind < allAnchors[j].Kind
		}
		return allAnchors[i].Score > allAnchors[j].Score
	})
	if len(allAnchors) > policy.MaxAnchors {
		allAnchors = allAnchors[:policy.MaxAnchors]
	}

	out := AnchoredContext{
		Query:          query,
		History:        history,
		Knowledge:      knowledge,
		Anchors:        allAnchors,
		CandidateCount: len(historyCandidates) + len(knowledgeCandidates),
		Policy:         policy,
	}
	mm.marCache.set(cacheKey, out)
	return out, nil
}

func scoreAnchoredCandidates(candidates []retrievalCandidate, kind string, now time.Time, policy MARPolicy) []AnchoredTrace {
	out := make([]AnchoredTrace, 0, len(candidates))
	for _, c := range candidates {
		if isArchived(c.Metadata) {
			continue
		}
		importance := parseMetaFloat(c.Metadata, metaBaseImportance, defaultBaseImportance)
		freshness := freshnessScore(parseMetaTime(c.Metadata, metaTimestamp), now)
		topologySignal := 0.0
		if strings.TrimSpace(c.Metadata[metaTopologyNode]) != "" {
			topologySignal = 1.0
		}
		score := (c.Semantic * policy.WeightSemantic) +
			(c.Lexical * policy.WeightLexical) +
			(importance * policy.WeightImportance) +
			(freshness * policy.WeightFreshness) +
			(topologySignal * policy.WeightTopology)
		if topologySignal > 0 {
			score += policy.TopologyBoost
		}
		score = clamp01(score)
		if score < policy.MinAnchorScore {
			continue
		}
		meta := make(map[string]string, len(c.Metadata))
		for k, v := range c.Metadata {
			meta[k] = v
		}
		out = append(out, AnchoredTrace{
			ID:       c.ID,
			Kind:     kind,
			Content:  c.Content,
			Score:    score,
			Metadata: meta,
		})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Score == out[j].Score {
			return out[i].ID < out[j].ID
		}
		return out[i].Score > out[j].Score
	})
	return out
}

func selectAnchoredContent(anchors []AnchoredTrace, limit int, policy MARPolicy) []string {
	if limit <= 0 || len(anchors) == 0 {
		return nil
	}
	capLimit := minInt(limit, policy.MaxAnchors)
	if capLimit <= 0 {
		return nil
	}
	out := make([]string, 0, capLimit)
	for _, a := range anchors {
		if strings.TrimSpace(a.Content) == "" {
			continue
		}
		out = append(out, a.Content)
		if len(out) >= capLimit {
			break
		}
	}
	return out
}

// AnchoredStatusReport returns an ASCII status snapshot for memory-anchored reasoning.
func (c AnchoredContext) AnchoredStatusReport() string {
	if strings.TrimSpace(c.Query) == "" {
		return "MAR STATUS | EMPTY\n"
	}
	var b strings.Builder
	b.WriteString("MAR STATUS\n")
	b.WriteString("query: ")
	b.WriteString(c.Query)
	b.WriteString("\n")
	b.WriteString(fmt.Sprintf("candidate_pool: %d | anchors: %d | cache_hit: %t\n", c.CandidateCount, len(c.Anchors), c.CacheHit))
	b.WriteString(fmt.Sprintf("history: %d | knowledge: %d\n", len(c.History), len(c.Knowledge)))
	b.WriteString("active_shards:\n")
	if len(c.Anchors) == 0 {
		b.WriteString("  - none\n")
		return b.String()
	}
	for i, a := range c.Anchors {
		if i >= 8 {
			break
		}
		b.WriteString(fmt.Sprintf("  - [%s] %.2f %s\n", a.Kind, a.Score, compactAnchorLabel(a)))
	}
	return b.String()
}

func compactAnchorLabel(a AnchoredTrace) string {
	if ref := strings.TrimSpace(a.Metadata["source"]); ref != "" {
		return ref
	}
	if ref := strings.TrimSpace(a.Metadata["path"]); ref != "" {
		return ref
	}
	if ref := strings.TrimSpace(a.Metadata["url"]); ref != "" {
		return ref
	}
	if ref := strings.TrimSpace(a.Metadata["title"]); ref != "" {
		return ref
	}
	content := strings.TrimSpace(strings.ReplaceAll(a.Content, "\n", " "))
	if len(content) <= 42 {
		return content
	}
	return content[:42] + "..."
}
