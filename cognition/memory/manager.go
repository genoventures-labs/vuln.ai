package memory

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Thynaptic/P-LMv1/pkg/state"
	"github.com/ollama/ollama/api"
	chromem "github.com/philippgille/chromem-go"
)

const (
	defaultEmbeddingModel    = "nomic-embed-text:latest"
	collectionNameBase       = "conversation_history"
	knowledgeCollectionBase  = "knowledge_base"
	ollamaAPIHostEnv         = "OLLAMA_HOST"
	retrievalModeEnv         = "TALOS_MEMORY_RETRIEVAL_MODE"
	candidateWeightsEnv      = "TALOS_RETRIEVAL_CANDIDATE_WEIGHTS"
	dynamicWeightsEnv        = "TALOS_RETRIEVAL_DYNAMIC_WEIGHTS"
	knowledgeWeightsEnv      = "TALOS_RETRIEVAL_KNOWLEDGE_WEIGHTS"
	segmentWeightsEnv        = "TALOS_RETRIEVAL_SEGMENT_WEIGHTS"
	defaultBaseImportance    = 0.5
	defaultFreshnessHalfLife = 24 * time.Hour
	importanceEvalTimeout    = 8 * time.Second
)

var importanceEvalModels = []string{"llama3.2:1b", "qwen2.5:3b-instruct"}

const (
	retrievalModeSemantic = "semantic"
	retrievalModeHybrid   = "hybrid"
	retrievalModeLexical  = "lexical"
)

const (
	metaTimestamp       = "timestamp"
	metaBaseImportance  = "base_importance"
	metaRetrievalCount  = "retrieval_count"
	metaLastRetrievedAt = "last_retrieved_at"
	metaClusterID       = "cluster_id"
	metaClusterLabel    = "cluster_label"
	metaClusterSize     = "cluster_size"
	metaArchived        = "archived"
	metaArchiveReason   = "archive_reason"
	metaLastReindexedAt = "last_reindexed_at"
	metaModality        = "modality"
	metaImagePath       = "image_path"
	metaVisualModel     = "visual_model"
	metaCrossModalLink  = "cross_modal_link_id"
	metaTopologyNode    = "topology_node"
)

// MemoryManager handles interaction with chromem-go for conversation memory.
type MemoryManager struct {
	db                  *chromem.DB
	historyCollection   *chromem.Collection
	knowledgeCollection *chromem.Collection
	historyBM25         *BM25Index
	knowledgeBM25       *BM25Index
	retrievalMode       string
	retrievalWeights    RetrievalWeights
	marPolicy           MARPolicy
	marCache            *marContextCache
	client              *api.Client
	activeNamespace     string
	topology            *TopologyGraph
	topologyCfg         TopologyConfig
	topologyPath        string
	topologyLastErr     string
	topologyEdgesAdded  int64
	topologyLinksUsed   int64
	mu                  sync.Mutex
}

// KnowledgeSegment is a query result with source metadata for document orchestration.
type KnowledgeSegment struct {
	ID         string
	Content    string
	Similarity float64
	Metadata   map[string]string
}

type retrievalCandidate struct {
	ID       string
	Content  string
	Metadata map[string]string
	Semantic float64
	Lexical  float64
}

type RetrievalWeights struct {
	CandidateSemantic float64
	CandidateLexical  float64

	DynamicSemantic   float64
	DynamicLexical    float64
	DynamicImportance float64
	DynamicFreshness  float64

	KnowledgeSemantic float64
	KnowledgeLexical  float64

	SegmentSemantic float64
	SegmentLexical  float64
}

// NewMemoryManager initializes a new MemoryManager with persistent storage.
func NewMemoryManager() (*MemoryManager, error) {
	// Initialize Ollama client for embeddings
	client, err := api.ClientFromEnvironment()
	if err != nil {
		return nil, fmt.Errorf("failed to create Ollama client for embeddings: %w", err)
	}
	embeddingModel, err := resolveEmbeddingModel(client)
	if err != nil {
		return nil, err
	}
	var embedMu sync.Mutex

	// Create custom embedding function
	ef := func(ctx context.Context, text string) ([]float32, error) {
		embedMu.Lock()
		model := embeddingModel
		embedMu.Unlock()
		req := &api.EmbeddingRequest{
			Model:  model,
			Prompt: text,
		}
		resp, err := client.Embeddings(ctx, req)
		if err != nil {
			// Model can disappear when VPS rotates tags; re-resolve once and retry.
			if strings.Contains(strings.ToLower(err.Error()), "not found") {
				if replacement, resolveErr := resolveEmbeddingModel(client); resolveErr == nil && replacement != "" && replacement != model {
					embedMu.Lock()
					embeddingModel = replacement
					embedMu.Unlock()
					req.Model = replacement
					resp, err = client.Embeddings(ctx, req)
				}
			}
		}
		if err != nil {
			return nil, fmt.Errorf("ollama embedding error: %w", err)
		}
		if len(resp.Embedding) == 0 {
			return nil, fmt.Errorf("no embedding returned from ollama")
		}

		// Convert []float64 to []float32
		res := make([]float32, len(resp.Embedding))
		for i, f := range resp.Embedding {
			res[i] = float32(f)
		}
		return res, nil
	}

	historyCollectionName := collectionNameForModel(collectionNameBase, embeddingModel)
	knowledgeCollectionName := collectionNameForModel(knowledgeCollectionBase, embeddingModel)

	// Initialize chromem-go database with persistence
	db, err := chromem.NewPersistentDB(".memory", true)
	if err != nil {
		return nil, fmt.Errorf("failed to create persistent chromem-go database: %w", err)
	}

	// Create or get collection for conversation history
	hCol, err := db.GetOrCreateCollection(historyCollectionName, nil, ef)
	if err != nil {
		return nil, fmt.Errorf("failed to get or create history collection: %w", err)
	}

	// Create or get collection for general knowledge
	kCol, err := db.GetOrCreateCollection(knowledgeCollectionName, nil, ef)
	if err != nil {
		return nil, fmt.Errorf("failed to get or create knowledge collection: %w", err)
	}

	retrievalMode := resolveRetrievalMode()
	retrievalWeights := resolveRetrievalWeights()
	mm := &MemoryManager{
		db:                  db,
		historyCollection:   hCol,
		knowledgeCollection: kCol,
		historyBM25:         NewBM25Index(),
		knowledgeBM25:       NewBM25Index(),
		retrievalMode:       retrievalMode,
		retrievalWeights:    retrievalWeights,
		marPolicy:           defaultMARPolicy(),
		marCache:            newMARContextCache(),
		client:              client,
		activeNamespace:     "",
		topologyCfg:         defaultTopologyConfig(),
		topologyPath:        defaultTopologyPath,
	}
	if mm.topologyCfg.Enabled {
		if graph, loadErr := LoadTopologyGraph(mm.topologyPath); loadErr != nil {
			mm.topology = newTopologyGraph()
			mm.topologyLastErr = loadErr.Error()
		} else {
			mm.topology = graph
		}
	}

	_ = mm.rebuildBM25IndexForCollection(mm.historyCollection, mm.historyBM25)
	_ = mm.rebuildBM25IndexForCollection(mm.knowledgeCollection, mm.knowledgeBM25)

	return mm, nil
}

func resolveEmbeddingModel(client *api.Client) (string, error) {
	if v := strings.TrimSpace(firstNonEmptyEnv("TALOS_EMBED_MODEL", "EMBEDDING_MODEL")); v != "" {
		return v, nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	listResp, err := client.List(ctx)
	if err != nil {
		return "", fmt.Errorf("failed listing ollama models for embedding resolution: %w", err)
	}
	if len(listResp.Models) == 0 {
		return "", fmt.Errorf("no Ollama models available for embedding resolution")
	}

	candidates := make([]string, 0, len(listResp.Models)+1)
	seen := make(map[string]bool)
	add := func(m string) {
		m = strings.TrimSpace(m)
		if m == "" || seen[m] {
			return
		}
		seen[m] = true
		candidates = append(candidates, m)
	}
	add(defaultEmbeddingModel)
	for _, m := range listResp.Models {
		add(m.Name)
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		return embeddingCandidateScore(candidates[i]) > embeddingCandidateScore(candidates[j])
	})

	for _, model := range candidates {
		probeCtx, probeCancel := context.WithTimeout(context.Background(), 6*time.Second)
		resp, probeErr := client.Embeddings(probeCtx, &api.EmbeddingRequest{
			Model:  model,
			Prompt: "embedding-healthcheck",
		})
		probeCancel()
		if probeErr == nil && len(resp.Embedding) > 0 {
			return model, nil
		}
	}

	// If VPS rotation removed embedding models, auto-pull a known embed model and retry once.
	if shouldAutoPullEmbeddingModel() {
		if err := pullEmbeddingModel(defaultEmbeddingModel); err == nil {
			retryCtx, retryCancel := context.WithTimeout(context.Background(), 8*time.Second)
			defer retryCancel()
			retryResp, retryErr := client.Embeddings(retryCtx, &api.EmbeddingRequest{
				Model:  defaultEmbeddingModel,
				Prompt: "embedding-healthcheck",
			})
			if retryErr == nil && len(retryResp.Embedding) > 0 {
				return defaultEmbeddingModel, nil
			}
		}
	}
	return "", fmt.Errorf("no embedding-capable model resolved from current Ollama tags; set TALOS_EMBED_MODEL explicitly")
}

func embeddingCandidateScore(model string) int {
	m := strings.ToLower(strings.TrimSpace(model))
	switch {
	case m == "nomic-embed-text:latest":
		return 100
	case strings.Contains(m, "embed"):
		return 90
	case strings.Contains(m, "embedding"):
		return 80
	case strings.Contains(m, "bge"), strings.Contains(m, "e5"), strings.Contains(m, "minilm"):
		return 70
	default:
		return 10
	}
}

func firstNonEmptyEnv(keys ...string) string {
	for _, k := range keys {
		v := strings.TrimSpace(os.Getenv(k))
		if v != "" {
			return v
		}
	}
	return ""
}

func shouldAutoPullEmbeddingModel() bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv("TALOS_AUTO_PULL_EMBED_MODEL")))
	switch v {
	case "", "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	default:
		return true
	}
}

func pullEmbeddingModel(model string) error {
	model = strings.TrimSpace(model)
	if model == "" {
		return fmt.Errorf("embedding model name is empty")
	}
	host := strings.TrimSpace(os.Getenv(ollamaAPIHostEnv))
	if host == "" {
		host = "http://85.31.233.157:11434"
	}
	if !strings.HasPrefix(host, "http://") && !strings.HasPrefix(host, "https://") {
		host = "http://" + host
	}
	host = strings.TrimRight(host, "/")

	payload, _ := json.Marshal(map[string]interface{}{
		"name":   model,
		"stream": false,
	})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, host+"/api/pull", bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := (&http.Client{}).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return fmt.Errorf("ollama pull failed (%d): %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return nil
}

func collectionNameForModel(base, model string) string {
	model = strings.ToLower(strings.TrimSpace(model))
	model = strings.ReplaceAll(model, ":", "_")
	model = strings.ReplaceAll(model, "/", "_")
	model = strings.ReplaceAll(model, "-", "_")
	if model == "" {
		model = "default"
	}
	return base + "__" + model
}

func resolveRetrievalMode() string {
	mode := strings.ToLower(strings.TrimSpace(os.Getenv(retrievalModeEnv)))
	switch mode {
	case retrievalModeSemantic, retrievalModeHybrid, retrievalModeLexical:
		return mode
	default:
		return retrievalModeHybrid
	}
}

func resolveRetrievalWeights() RetrievalWeights {
	candidate := parseWeightListEnv(candidateWeightsEnv, []float64{0.60, 0.40})
	dynamic := parseWeightListEnv(dynamicWeightsEnv, []float64{0.40, 0.20, 0.25, 0.15})
	knowledge := parseWeightListEnv(knowledgeWeightsEnv, []float64{0.70, 0.30})
	segment := parseWeightListEnv(segmentWeightsEnv, []float64{0.70, 0.30})
	return RetrievalWeights{
		CandidateSemantic: candidate[0],
		CandidateLexical:  candidate[1],

		DynamicSemantic:   dynamic[0],
		DynamicLexical:    dynamic[1],
		DynamicImportance: dynamic[2],
		DynamicFreshness:  dynamic[3],

		KnowledgeSemantic: knowledge[0],
		KnowledgeLexical:  knowledge[1],

		SegmentSemantic: segment[0],
		SegmentLexical:  segment[1],
	}
}

func parseWeightListEnv(key string, fallback []float64) []float64 {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return append([]float64(nil), fallback...)
	}
	parts := strings.Split(raw, ",")
	if len(parts) != len(fallback) {
		return append([]float64(nil), fallback...)
	}
	values := make([]float64, 0, len(parts))
	for _, p := range parts {
		v, err := strconv.ParseFloat(strings.TrimSpace(p), 64)
		if err != nil || v < 0 {
			return append([]float64(nil), fallback...)
		}
		values = append(values, v)
	}
	return normalizeWeights(values, fallback)
}

func normalizeWeights(values []float64, fallback []float64) []float64 {
	if len(values) == 0 {
		return append([]float64(nil), fallback...)
	}
	sum := 0.0
	for _, v := range values {
		sum += v
	}
	if sum <= 0 {
		return append([]float64(nil), fallback...)
	}
	out := make([]float64, len(values))
	for i, v := range values {
		out[i] = v / sum
	}
	return out
}

func (mm *MemoryManager) rebuildBM25IndexForCollection(col *chromem.Collection, idx *BM25Index) error {
	if mm == nil || col == nil || idx == nil {
		return nil
	}
	count := col.Count()
	if count <= 0 {
		idx.Rebuild(nil)
		return nil
	}
	results, err := col.Query(context.Background(), " ", count, nil, nil)
	if err != nil {
		return err
	}
	docs := make([]bm25Document, 0, len(results))
	for _, r := range results {
		meta := make(map[string]string, len(r.Metadata))
		for k, v := range r.Metadata {
			meta[k] = v
		}
		docs = append(docs, bm25Document{
			ID:       r.ID,
			Content:  r.Content,
			Metadata: meta,
		})
	}
	idx.Rebuild(docs)
	return nil
}

func (mm *MemoryManager) bm25IndexForCollection(col *chromem.Collection) *BM25Index {
	switch col {
	case mm.historyCollection:
		return mm.historyBM25
	case mm.knowledgeCollection:
		return mm.knowledgeBM25
	default:
		return nil
	}
}

func (mm *MemoryManager) upsertBM25Doc(col *chromem.Collection, doc chromem.Document) {
	idx := mm.bm25IndexForCollection(col)
	if idx == nil {
		return
	}
	meta := make(map[string]string, len(doc.Metadata))
	for k, v := range doc.Metadata {
		meta[k] = v
	}
	idx.Upsert(bm25Document{
		ID:       doc.ID,
		Content:  doc.Content,
		Metadata: meta,
	})
}

// SetActiveNamespace scopes all Add/Retrieve operations to a namespace.
// Empty means global/unscoped behavior (no metadata filter).
func (mm *MemoryManager) SetActiveNamespace(namespace string) {
	mm.mu.Lock()
	defer mm.mu.Unlock()
	mm.activeNamespace = strings.TrimSpace(namespace)
}

// ActiveNamespace returns the currently scoped namespace.
func (mm *MemoryManager) ActiveNamespace() string {
	mm.mu.Lock()
	defer mm.mu.Unlock()
	return mm.activeNamespace
}

func (mm *MemoryManager) retrieveCandidates(col *chromem.Collection, idx *BM25Index, query string, candidateLimit int) ([]retrievalCandidate, error) {
	if col == nil || candidateLimit <= 0 {
		return nil, nil
	}
	mode := mm.retrievalMode
	if mode == "" {
		mode = retrievalModeHybrid
	}

	semanticCandidates := make(map[string]retrievalCandidate)
	var semanticErr error
	if mode != retrievalModeLexical {
		results, err := col.Query(context.Background(), query, candidateLimit, mm.namespaceWhereFilter(), nil)
		if err != nil {
			semanticErr = err
			if mode == retrievalModeSemantic {
				return nil, err
			}
		} else {
			for _, r := range results {
				meta := make(map[string]string, len(r.Metadata))
				for k, v := range r.Metadata {
					meta[k] = v
				}
				semanticCandidates[r.ID] = retrievalCandidate{
					ID:       r.ID,
					Content:  r.Content,
					Metadata: meta,
					Semantic: similarityToUnit(r.Similarity),
				}
			}
		}
	}

	lexicalCandidates := make(map[string]retrievalCandidate)
	if mode != retrievalModeSemantic && idx != nil {
		ns := strings.TrimSpace(mm.ActiveNamespace())
		results := idx.Search(query, candidateLimit, ns)
		for _, r := range results {
			meta := make(map[string]string, len(r.Metadata))
			for k, v := range r.Metadata {
				meta[k] = v
			}
			lexicalCandidates[r.ID] = retrievalCandidate{
				ID:       r.ID,
				Content:  r.Content,
				Metadata: meta,
				Lexical:  clamp01(r.Score),
			}
		}
	}

	if mode == retrievalModeSemantic {
		return mapCandidates(semanticCandidates, candidateLimit, func(c retrievalCandidate) float64 {
			return c.Semantic
		}), nil
	}
	if mode == retrievalModeLexical {
		return mapCandidates(lexicalCandidates, candidateLimit, func(c retrievalCandidate) float64 {
			return c.Lexical
		}), nil
	}

	merged := make(map[string]retrievalCandidate, len(semanticCandidates)+len(lexicalCandidates))
	for id, c := range semanticCandidates {
		merged[id] = c
	}
	for id, c := range lexicalCandidates {
		existing := merged[id]
		if existing.ID == "" {
			merged[id] = c
			continue
		}
		existing.Lexical = c.Lexical
		if strings.TrimSpace(existing.Content) == "" {
			existing.Content = c.Content
		}
		if len(existing.Metadata) == 0 {
			existing.Metadata = c.Metadata
		}
		merged[id] = existing
	}
	if len(merged) == 0 {
		if semanticErr != nil {
			return nil, semanticErr
		}
		return nil, nil
	}
	return mapCandidates(merged, candidateLimit, func(c retrievalCandidate) float64 {
		return (c.Semantic * mm.retrievalWeights.CandidateSemantic) + (c.Lexical * mm.retrievalWeights.CandidateLexical)
	}), nil
}

func mapCandidates(in map[string]retrievalCandidate, limit int, scoreFn func(retrievalCandidate) float64) []retrievalCandidate {
	if len(in) == 0 || limit <= 0 {
		return nil
	}
	out := make([]retrievalCandidate, 0, len(in))
	for _, c := range in {
		out = append(out, c)
	}
	sort.SliceStable(out, func(i, j int) bool {
		return scoreFn(out[i]) > scoreFn(out[j])
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out
}

func (mm *MemoryManager) sortCandidatesByKnowledgeWeights(candidates []retrievalCandidate) {
	sort.SliceStable(candidates, func(i, j int) bool {
		si := (candidates[i].Semantic * mm.retrievalWeights.KnowledgeSemantic) + (candidates[i].Lexical * mm.retrievalWeights.KnowledgeLexical)
		sj := (candidates[j].Semantic * mm.retrievalWeights.KnowledgeSemantic) + (candidates[j].Lexical * mm.retrievalWeights.KnowledgeLexical)
		return si > sj
	})
}

func (mm *MemoryManager) segmentHybridScore(c retrievalCandidate) float64 {
	return clamp01((c.Semantic * mm.retrievalWeights.SegmentSemantic) + (c.Lexical * mm.retrievalWeights.SegmentLexical))
}

// AddMessage adds a message (user or assistant) to the conversation memory.
func (mm *MemoryManager) AddMessage(role, content string) error {
	importance := mm.evaluateMessageImportance(role, content)
	now := time.Now().UTC()
	doc := chromem.Document{
		ID:      fmt.Sprintf("msg_%d", time.Now().UnixNano()),
		Content: fmt.Sprintf("%s: %s", role, content),
		Metadata: map[string]string{
			"role":              role,
			"type":              "chat",
			metaTimestamp:       now.Format(time.RFC3339),
			metaBaseImportance:  formatFloat(importance),
			metaRetrievalCount:  "0",
			metaLastRetrievedAt: "",
			metaArchived:        "false",
		},
	}
	if ns := strings.TrimSpace(mm.ActiveNamespace()); ns != "" {
		doc.Metadata["namespace"] = ns
	}

	err := mm.historyCollection.AddDocument(context.Background(), doc)
	if err != nil {
		return fmt.Errorf("failed to add message to history: %w", err)
	}
	mm.upsertBM25Doc(mm.historyCollection, doc)

	return nil
}

// RetrieveDynamicContext retrieves context using semantic similarity, importance, and freshness weighting.
//
// Score = (SemanticSimilarity * 0.5) + (Importance * 0.3) + (Freshness * 0.2)
func (mm *MemoryManager) RetrieveDynamicContext(query string, k int) ([]string, error) {
	count := mm.historyCollection.Count()
	if count == 0 {
		return nil, nil
	}
	if k > count {
		k = count
	}
	if k <= 0 {
		return nil, nil
	}

	// Query a broader candidate set before weighted reranking.
	candidatesN := minInt(maxInt(k*8, 16), count)
	candidates, err := mm.retrieveCandidates(mm.historyCollection, mm.historyBM25, query, candidatesN)
	if err != nil {
		return nil, err
	}

	now := time.Now().UTC()
	type scored struct {
		id      string
		content string
		score   float64
	}
	scoredResults := make([]scored, 0, len(candidates))
	for _, c := range candidates {
		if isArchived(c.Metadata) {
			continue
		}
		importance := parseMetaFloat(c.Metadata, metaBaseImportance, defaultBaseImportance)
		freshness := freshnessScore(parseMetaTime(c.Metadata, metaTimestamp), now)
		finalScore := (c.Semantic * mm.retrievalWeights.DynamicSemantic) +
			(c.Lexical * mm.retrievalWeights.DynamicLexical) +
			(importance * mm.retrievalWeights.DynamicImportance) +
			(freshness * mm.retrievalWeights.DynamicFreshness)
		scoredResults = append(scoredResults, scored{
			id:      c.ID,
			content: c.Content,
			score:   finalScore,
		})
	}

	sort.SliceStable(scoredResults, func(i, j int) bool {
		return scoredResults[i].score > scoredResults[j].score
	})
	if len(scoredResults) > k {
		scoredResults = scoredResults[:k]
	}

	contextMessages := make([]string, 0, k)
	selectedIDs := make(map[string]bool)
	clusterIDs := make(map[string]bool)
	for _, s := range scoredResults {
		if len(contextMessages) >= k {
			break
		}
		contextMessages = append(contextMessages, s.content)
		selectedIDs[s.id] = true
		mm.bumpRetrievalCount(mm.historyCollection, s.id, now)
		if doc, err := mm.historyCollection.GetByID(context.Background(), s.id); err == nil {
			if cid := strings.TrimSpace(doc.Metadata[metaClusterID]); cid != "" {
				clusterIDs[cid] = true
			}
		}
	}
	if len(contextMessages) < k && len(clusterIDs) > 0 {
		extra := mm.clusterCompanions(mm.historyCollection, clusterIDs, selectedIDs, k-len(contextMessages), now)
		contextMessages = append(contextMessages, extra...)
	}

	return contextMessages, nil
}

// RetrieveContext retrieves relevant past conversation context based on the current query.
func (mm *MemoryManager) RetrieveContext(query string, k int) ([]string, error) {
	count := mm.historyCollection.Count()
	if count == 0 {
		return nil, nil
	}
	if k > count {
		k = count
	}

	candidates, err := mm.retrieveCandidates(mm.historyCollection, mm.historyBM25, query, k)
	if err != nil {
		return nil, err
	}

	var contextMessages []string
	for _, c := range candidates {
		contextMessages = append(contextMessages, c.Content)
	}

	return contextMessages, nil
}

// AddKnowledge adds information to the persistent knowledge base.
func (mm *MemoryManager) AddKnowledge(content string, metadata map[string]string) error {
	if metadata == nil {
		metadata = make(map[string]string)
	}
	metadata["type"] = "knowledge"
	if _, ok := metadata[metaTimestamp]; !ok {
		metadata[metaTimestamp] = time.Now().UTC().Format(time.RFC3339)
	}
	if _, ok := metadata[metaBaseImportance]; !ok {
		metadata[metaBaseImportance] = formatFloat(defaultBaseImportance)
	}
	if _, ok := metadata[metaRetrievalCount]; !ok {
		metadata[metaRetrievalCount] = "0"
	}
	if _, ok := metadata[metaLastRetrievedAt]; !ok {
		metadata[metaLastRetrievedAt] = ""
	}
	if _, ok := metadata[metaArchived]; !ok {
		metadata[metaArchived] = "false"
	}
	if ns := strings.TrimSpace(mm.ActiveNamespace()); ns != "" {
		if _, ok := metadata["namespace"]; !ok {
			metadata["namespace"] = ns
		}
	}

	doc := chromem.Document{
		ID:       fmt.Sprintf("kb_%d", time.Now().UnixNano()),
		Content:  content,
		Metadata: metadata,
	}

	err := mm.knowledgeCollection.AddDocument(context.Background(), doc)
	if err != nil {
		return fmt.Errorf("failed to add knowledge: %w", err)
	}
	mm.upsertBM25Doc(mm.knowledgeCollection, doc)

	return nil
}

func (mm *MemoryManager) UpsertTopologySource(fp SourceFingerprint) error {
	if mm == nil {
		return fmt.Errorf("memory manager is nil")
	}
	if !mm.topologyCfg.Enabled {
		return nil
	}
	if mm.topology == nil {
		mm.topology = newTopologyGraph()
	}
	edgesAdded, err := mm.topology.UpsertSource(fp, mm.topologyCfg)
	if err != nil {
		mm.mu.Lock()
		mm.topologyLastErr = err.Error()
		mm.mu.Unlock()
		return err
	}
	if err := SaveTopologyGraph(mm.topologyPath, mm.topology); err != nil {
		mm.mu.Lock()
		mm.topologyLastErr = err.Error()
		mm.mu.Unlock()
		return err
	}
	mm.mu.Lock()
	mm.topologyEdgesAdded += int64(edgesAdded)
	mm.topologyLastErr = ""
	mm.mu.Unlock()
	return nil
}

type TopologyStats struct {
	Enabled    bool
	Nodes      int
	Edges      int
	EdgesAdded int64
	LinksUsed  int64
	LastError  string
}

func (mm *MemoryManager) TopologyStats() TopologyStats {
	if mm == nil {
		return TopologyStats{}
	}
	mm.mu.Lock()
	defer mm.mu.Unlock()
	out := TopologyStats{
		Enabled:    mm.topologyCfg.Enabled,
		EdgesAdded: mm.topologyEdgesAdded,
		LinksUsed:  mm.topologyLinksUsed,
		LastError:  mm.topologyLastErr,
	}
	if mm.topology != nil {
		mm.topology.mu.RLock()
		out.Nodes = len(mm.topology.Nodes)
		for _, edges := range mm.topology.Adjacency {
			out.Edges += len(edges)
		}
		mm.topology.mu.RUnlock()
	}
	return out
}

// SnapshotKnowledgeSegments returns a broad deterministic sample of knowledge segments for offline consolidation.
func (mm *MemoryManager) SnapshotKnowledgeSegments(limit int) ([]KnowledgeSegment, error) {
	if mm == nil || mm.knowledgeCollection == nil {
		return nil, nil
	}
	count := mm.knowledgeCollection.Count()
	if count <= 0 {
		return nil, nil
	}
	if limit <= 0 || limit > count {
		limit = count
	}

	results, err := mm.knowledgeCollection.Query(context.Background(), " ", count, mm.namespaceWhereFilter(), nil)
	if err != nil {
		return nil, err
	}

	out := make([]KnowledgeSegment, 0, minInt(limit, len(results)))
	for _, r := range results {
		if isArchived(r.Metadata) {
			continue
		}
		meta := make(map[string]string, len(r.Metadata))
		for k, v := range r.Metadata {
			meta[k] = v
		}
		score := parseMetaFloat(meta, metaBaseImportance, similarityToUnit(r.Similarity))
		out = append(out, KnowledgeSegment{
			ID:         r.ID,
			Content:    r.Content,
			Similarity: score,
			Metadata:   meta,
		})
		if len(out) >= limit {
			break
		}
	}
	return out, nil
}

// ReinforceTopologyFromCrossSectionLinks applies cross-sectional evidence to the topology graph and persists updates.
func (mm *MemoryManager) ReinforceTopologyFromCrossSectionLinks(links []CrossSectionalLink, baseBoost float64) (int, error) {
	if mm == nil {
		return 0, fmt.Errorf("memory manager is nil")
	}
	if !mm.topologyCfg.Enabled {
		return 0, nil
	}
	if mm.topology == nil {
		mm.topology = newTopologyGraph()
	}
	updated := mm.topology.ReinforceCrossSectionLinks(links, mm.topologyCfg.MaxNeighbors, baseBoost)
	if updated == 0 {
		return 0, nil
	}
	if err := SaveTopologyGraph(mm.topologyPath, mm.topology); err != nil {
		mm.mu.Lock()
		mm.topologyLastErr = err.Error()
		mm.mu.Unlock()
		return 0, err
	}
	mm.mu.Lock()
	mm.topologyEdgesAdded += int64(updated)
	mm.topologyLastErr = ""
	mm.mu.Unlock()
	return updated, nil
}

// RefineImportance re-evaluates importance from retrieval frequency and updates metadata.
// Intended to be called periodically as a background maintenance task.
func (mm *MemoryManager) RefineImportance() error {
	now := time.Now().UTC()
	if err := mm.refineCollectionImportance(mm.historyCollection, now); err != nil {
		return err
	}
	if err := mm.refineCollectionImportance(mm.knowledgeCollection, now); err != nil {
		return err
	}
	return nil
}

// RetrieveKnowledge retrieves relevant info from the knowledge base.
func (mm *MemoryManager) RetrieveKnowledge(query string, k int) ([]string, error) {
	count := mm.knowledgeCollection.Count()
	if count == 0 {
		return nil, nil
	}
	if k > count {
		k = count
	}

	candidatesN := minInt(maxInt(k*6, 12), count)
	candidates, err := mm.retrieveCandidates(mm.knowledgeCollection, mm.knowledgeBM25, query, candidatesN)
	if err != nil {
		return nil, err
	}
	mm.sortCandidatesByKnowledgeWeights(candidates)

	var knowledge []string
	selectedIDs := make(map[string]bool)
	clusterIDs := make(map[string]bool)
	now := time.Now().UTC()
	for _, c := range candidates {
		if isArchived(c.Metadata) {
			continue
		}
		knowledge = append(knowledge, c.Content)
		selectedIDs[c.ID] = true
		mm.bumpRetrievalCount(mm.knowledgeCollection, c.ID, now)
		if cid := strings.TrimSpace(c.Metadata[metaClusterID]); cid != "" {
			clusterIDs[cid] = true
		}
		if len(knowledge) >= k {
			break
		}
	}
	if len(knowledge) < k && len(clusterIDs) > 0 {
		extra := mm.clusterCompanions(mm.knowledgeCollection, clusterIDs, selectedIDs, k-len(knowledge), now)
		knowledge = append(knowledge, extra...)
	}
	if len(knowledge) < k {
		linkIDs := mm.collectCrossModalLinkIDs(mm.knowledgeCollection, selectedIDs)
		if len(linkIDs) > 0 {
			extra := mm.crossModalCompanions(mm.knowledgeCollection, linkIDs, selectedIDs, k-len(knowledge), now)
			knowledge = append(knowledge, extra...)
		}
	}

	return knowledge, nil
}

// RetrieveKnowledgeSegments returns chunk-level knowledge matches with metadata.
func (mm *MemoryManager) RetrieveKnowledgeSegments(query string, k int) ([]KnowledgeSegment, error) {
	count := mm.knowledgeCollection.Count()
	if count == 0 {
		return nil, nil
	}
	if k > count {
		k = count
	}
	if k <= 0 {
		return nil, nil
	}

	candidatesN := minInt(maxInt(k*6, 12), count)
	candidates, err := mm.retrieveCandidates(mm.knowledgeCollection, mm.knowledgeBM25, query, candidatesN)
	if err != nil {
		return nil, err
	}
	mm.sortCandidatesByKnowledgeWeights(candidates)

	segments := make([]KnowledgeSegment, 0, len(candidates))
	now := time.Now().UTC()
	for _, c := range candidates {
		if isArchived(c.Metadata) {
			continue
		}
		meta := make(map[string]string, len(c.Metadata))
		for mk, mv := range c.Metadata {
			meta[mk] = mv
		}
		hybridScore := mm.segmentHybridScore(c)
		segments = append(segments, KnowledgeSegment{
			ID:         c.ID,
			Content:    c.Content,
			Similarity: hybridScore,
			Metadata:   meta,
		})
		mm.bumpRetrievalCount(mm.knowledgeCollection, c.ID, now)
		if len(segments) >= k {
			break
		}
	}
	if len(segments) < k {
		selected := make(map[string]bool, len(segments))
		for _, s := range segments {
			selected[s.ID] = true
		}
		linkIDs := mm.collectCrossModalLinkIDs(mm.knowledgeCollection, selected)
		if len(linkIDs) > 0 {
			extra := mm.crossModalCompanionSegments(mm.knowledgeCollection, linkIDs, selected, k-len(segments), time.Now().UTC())
			segments = append(segments, extra...)
		}
	}
	if len(segments) > 0 && mm.topologyCfg.Enabled && mm.topology != nil && mm.topologyCfg.ExpansionLimit > 0 {
		expanded, linksUsed := mm.topologyExpandSegments(segments, minInt(mm.topologyCfg.ExpansionLimit, maxInt(k, 1)))
		if len(expanded) > 0 {
			segments = append(segments, expanded...)
			sort.SliceStable(segments, func(i, j int) bool {
				if segments[i].Similarity == segments[j].Similarity {
					ri := topologyRefFromMetadata(segments[i].Metadata)
					rj := topologyRefFromMetadata(segments[j].Metadata)
					if ri == rj {
						ii := parseMetaInt(segments[i].Metadata, "chunk_index", 0)
						ij := parseMetaInt(segments[j].Metadata, "chunk_index", 0)
						return ii < ij
					}
					return ri < rj
				}
				return segments[i].Similarity > segments[j].Similarity
			})
			if len(segments) > k {
				segments = segments[:k]
			}
		}
		mm.mu.Lock()
		mm.topologyLinksUsed += int64(linksUsed)
		mm.mu.Unlock()
	}
	return segments, nil
}

func (mm *MemoryManager) topologyExpandSegments(seed []KnowledgeSegment, limit int) ([]KnowledgeSegment, int) {
	if mm == nil || mm.topology == nil || len(seed) == 0 || limit <= 0 {
		return nil, 0
	}
	selected := map[string]bool{}
	seedRefs := map[string]bool{}
	for _, s := range seed {
		selected[s.ID] = true
		if ref := topologyRefFromMetadata(s.Metadata); ref != "" {
			seedRefs[ref] = true
		}
	}
	if len(seedRefs) == 0 {
		return nil, 0
	}
	related := map[string]float64{}
	linksUsed := 0
	for ref := range seedRefs {
		for _, edge := range mm.topology.Related(ref, mm.topologyCfg.MaxNeighbors) {
			if edge.Weight <= 0 {
				continue
			}
			linksUsed++
			if edge.Weight > related[edge.To] {
				related[edge.To] = edge.Weight
			}
		}
	}
	if len(related) == 0 {
		return nil, 0
	}

	count := mm.knowledgeCollection.Count()
	if count == 0 {
		return nil, 0
	}
	results, err := mm.knowledgeCollection.Query(context.Background(), " ", count, mm.namespaceWhereFilter(), nil)
	if err != nil {
		return nil, 0
	}
	out := make([]KnowledgeSegment, 0, limit)
	now := time.Now().UTC()
	for _, r := range results {
		if len(out) >= limit {
			break
		}
		if selected[r.ID] || isArchived(r.Metadata) {
			continue
		}
		ref := topologyRefFromMetadata(r.Metadata)
		weight, ok := related[ref]
		if !ok {
			continue
		}
		meta := make(map[string]string, len(r.Metadata))
		for k, v := range r.Metadata {
			meta[k] = v
		}
		boosted := clamp01(similarityToUnit(r.Similarity)*0.75 + weight*0.25)
		out = append(out, KnowledgeSegment{
			ID:         r.ID,
			Content:    r.Content,
			Similarity: boosted,
			Metadata:   meta,
		})
		selected[r.ID] = true
		mm.bumpRetrievalCount(mm.knowledgeCollection, r.ID, now)
	}
	return out, linksUsed
}

// HistoryCount returns the number of items in the history collection.
func (mm *MemoryManager) HistoryCount() int {
	return mm.historyCollection.Count()
}

// GetHistoryCollection returns the underlying history collection.
func (mm *MemoryManager) GetHistoryCollection() *chromem.Collection {
	return mm.historyCollection
}

// KnowledgeCount returns the number of items in the knowledge collection.
func (mm *MemoryManager) KnowledgeCount() int {
	return mm.knowledgeCollection.Count()
}

func (mm *MemoryManager) evaluateMessageImportance(role, content string) float64 {
	base := heuristicImportance(role, content)
	if mm.client == nil || strings.TrimSpace(content) == "" {
		return base
	}

	system := `You score memory importance for a personal AI system.
Return JSON only: {"importance": 0.0}
importance must be in [0.0,1.0], where:
- 0.0 means trivial/chit-chat
- 1.0 means mission-critical for future reasoning.`

	user := fmt.Sprintf(`{"role":%q,"content":%q}`, role, content)

	for _, model := range importanceEvalModels {
		opts, _ := state.ResolveEntropyOptions(user)
		req := &api.ChatRequest{
			Model:   model,
			Options: opts,
			Messages: []api.Message{
				{Role: "system", Content: system},
				{Role: "user", Content: user},
			},
		}

		ctx, cancel := context.WithTimeout(context.Background(), importanceEvalTimeout)
		var out strings.Builder
		err := mm.client.Chat(ctx, req, func(resp api.ChatResponse) error {
			out.WriteString(resp.Message.Content)
			return nil
		})
		cancel()
		if err != nil {
			continue
		}

		if imp, ok := parseImportanceJSON(out.String()); ok {
			return imp
		}
	}
	return base
}

func heuristicImportance(role, content string) float64 {
	c := strings.ToLower(content)
	score := 0.45
	if role == "user" {
		score += 0.05
	}
	keywords := []string{
		"goal", "deadline", "must", "critical", "important", "remember",
		"production", "incident", "bug", "regression", "security", "password",
		"architecture", "design", "constraint", "requirement",
	}
	for _, kw := range keywords {
		if strings.Contains(c, kw) {
			score += 0.04
		}
	}
	if len(content) > 300 {
		score += 0.05
	}
	return clamp01(score)
}

func parseImportanceJSON(raw string) (float64, bool) {
	raw = strings.TrimSpace(stripCodeFence(raw))
	type payload struct {
		Importance float64 `json:"importance"`
	}
	var p payload
	if err := json.Unmarshal([]byte(raw), &p); err == nil {
		return clamp01(p.Importance), true
	}
	// best effort: extract JSON object if model wrapped text around it
	start := strings.Index(raw, "{")
	end := strings.LastIndex(raw, "}")
	if start >= 0 && end > start {
		if err := json.Unmarshal([]byte(raw[start:end+1]), &p); err == nil {
			return clamp01(p.Importance), true
		}
	}
	return 0, false
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
	return strings.TrimSpace(strings.Join(lines[1:len(lines)-1], "\n"))
}

func (mm *MemoryManager) bumpRetrievalCount(col *chromem.Collection, docID string, now time.Time) {
	mm.mu.Lock()
	defer mm.mu.Unlock()

	doc, err := col.GetByID(context.Background(), docID)
	if err != nil {
		return
	}
	if doc.Metadata == nil {
		doc.Metadata = make(map[string]string)
	}
	count := parseMetaInt(doc.Metadata, metaRetrievalCount, 0) + 1
	doc.Metadata[metaRetrievalCount] = strconv.Itoa(count)
	doc.Metadata[metaLastRetrievedAt] = now.Format(time.RFC3339)
	// Keep timestamps for older docs that were created before metadata enhancement.
	if _, ok := doc.Metadata[metaTimestamp]; !ok {
		doc.Metadata[metaTimestamp] = now.Format(time.RFC3339)
	}
	_ = col.AddDocument(context.Background(), doc)
	mm.upsertBM25Doc(col, doc)
}

func (mm *MemoryManager) refineCollectionImportance(col *chromem.Collection, now time.Time) error {
	// Use a broad self-query as a pragmatic way to enumerate persisted documents.
	count := col.Count()
	if count == 0 {
		return nil
	}
	results, err := col.Query(context.Background(), " ", count, nil, nil)
	if err != nil {
		return err
	}

	for _, r := range results {
		doc, getErr := col.GetByID(context.Background(), r.ID)
		if getErr != nil {
			continue
		}
		if doc.Metadata == nil {
			doc.Metadata = make(map[string]string)
		}

		current := parseMetaFloat(doc.Metadata, metaBaseImportance, defaultBaseImportance)
		retrievals := parseMetaInt(doc.Metadata, metaRetrievalCount, 0)

		// Replay rule: frequently retrieved memories gain importance.
		boost := math.Min(0.30, math.Log1p(float64(retrievals))*0.08)
		target := clamp01(defaultBaseImportance + boost)
		updated := clamp01((current * 0.7) + (target * 0.3))

		doc.Metadata[metaBaseImportance] = formatFloat(updated)
		if _, ok := doc.Metadata[metaTimestamp]; !ok {
			doc.Metadata[metaTimestamp] = now.Format(time.RFC3339)
		}
		if _, ok := doc.Metadata[metaLastRetrievedAt]; !ok {
			doc.Metadata[metaLastRetrievedAt] = ""
		}
		if _, ok := doc.Metadata[metaRetrievalCount]; !ok {
			doc.Metadata[metaRetrievalCount] = "0"
		}

		if addErr := col.AddDocument(context.Background(), doc); addErr != nil {
			continue
		}
		mm.upsertBM25Doc(col, doc)
	}
	return nil
}

func freshnessScore(createdAt *time.Time, now time.Time) float64 {
	if createdAt == nil || createdAt.IsZero() || !now.After(*createdAt) {
		return 1.0
	}
	age := now.Sub(*createdAt)
	return clamp01(math.Pow(2, -age.Seconds()/defaultFreshnessHalfLife.Seconds()))
}

func similarityToUnit(sim float32) float64 {
	// cosine similarity from chromem is in [-1,1]; project to [0,1]
	return clamp01((float64(sim) + 1.0) / 2.0)
}

func parseMetaFloat(meta map[string]string, key string, fallback float64) float64 {
	if meta == nil {
		return fallback
	}
	raw, ok := meta[key]
	if !ok || strings.TrimSpace(raw) == "" {
		return fallback
	}
	v, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return fallback
	}
	return clamp01(v)
}

func parseMetaInt(meta map[string]string, key string, fallback int) int {
	if meta == nil {
		return fallback
	}
	raw, ok := meta[key]
	if !ok || strings.TrimSpace(raw) == "" {
		return fallback
	}
	v, err := strconv.Atoi(raw)
	if err != nil {
		return fallback
	}
	if v < 0 {
		return 0
	}
	return v
}

func parseMetaTime(meta map[string]string, key string) *time.Time {
	if meta == nil {
		return nil
	}
	raw, ok := meta[key]
	if !ok || strings.TrimSpace(raw) == "" {
		return nil
	}
	t, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return nil
	}
	return &t
}

func topologyRefFromMetadata(meta map[string]string) string {
	if meta == nil {
		return ""
	}
	if v := strings.TrimSpace(meta[metaTopologyNode]); v != "" {
		return v
	}
	if v := strings.TrimSpace(meta["source_path"]); v != "" {
		return v
	}
	if v := strings.TrimSpace(meta["source_url"]); v != "" {
		return v
	}
	if ds := strings.TrimSpace(meta["hf_dataset"]); ds != "" {
		split := strings.TrimSpace(meta["hf_split"])
		if split == "" {
			return "hf:" + ds
		}
		return "hf:" + ds + ":" + split
	}
	return ""
}

func isArchived(meta map[string]string) bool {
	if meta == nil {
		return false
	}
	v := strings.ToLower(strings.TrimSpace(meta[metaArchived]))
	return v == "true" || v == "1" || v == "yes"
}

func (mm *MemoryManager) clusterCompanions(col *chromem.Collection, clusterIDs map[string]bool, exclude map[string]bool, limit int, now time.Time) []string {
	if limit <= 0 || len(clusterIDs) == 0 {
		return nil
	}
	count := col.Count()
	if count == 0 {
		return nil
	}

	results, err := col.Query(context.Background(), " ", count, mm.namespaceWhereFilter(), nil)
	if err != nil {
		return nil
	}

	var out []string
	for _, r := range results {
		if len(out) >= limit {
			break
		}
		if exclude[r.ID] || isArchived(r.Metadata) {
			continue
		}
		cid := strings.TrimSpace(r.Metadata[metaClusterID])
		if cid == "" || !clusterIDs[cid] {
			continue
		}
		exclude[r.ID] = true
		out = append(out, r.Content)
		mm.bumpRetrievalCount(col, r.ID, now)
	}
	return out
}

func (mm *MemoryManager) collectCrossModalLinkIDs(col *chromem.Collection, selected map[string]bool) map[string]bool {
	if len(selected) == 0 {
		return nil
	}
	out := map[string]bool{}
	for id := range selected {
		doc, err := col.GetByID(context.Background(), id)
		if err != nil || doc.Metadata == nil {
			continue
		}
		if link := strings.TrimSpace(doc.Metadata[metaCrossModalLink]); link != "" {
			out[link] = true
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func (mm *MemoryManager) crossModalCompanions(col *chromem.Collection, linkIDs map[string]bool, exclude map[string]bool, limit int, now time.Time) []string {
	if limit <= 0 || len(linkIDs) == 0 {
		return nil
	}
	count := col.Count()
	if count == 0 {
		return nil
	}

	results, err := col.Query(context.Background(), " ", count, mm.namespaceWhereFilter(), nil)
	if err != nil {
		return nil
	}

	var out []string
	for _, r := range results {
		if len(out) >= limit {
			break
		}
		if exclude[r.ID] || isArchived(r.Metadata) {
			continue
		}
		link := strings.TrimSpace(r.Metadata[metaCrossModalLink])
		if link == "" || !linkIDs[link] {
			continue
		}
		exclude[r.ID] = true
		out = append(out, r.Content)
		mm.bumpRetrievalCount(col, r.ID, now)
	}
	return out
}

func (mm *MemoryManager) crossModalCompanionSegments(col *chromem.Collection, linkIDs map[string]bool, exclude map[string]bool, limit int, now time.Time) []KnowledgeSegment {
	if limit <= 0 || len(linkIDs) == 0 {
		return nil
	}
	count := col.Count()
	if count == 0 {
		return nil
	}
	results, err := col.Query(context.Background(), " ", count, mm.namespaceWhereFilter(), nil)
	if err != nil {
		return nil
	}

	out := make([]KnowledgeSegment, 0, limit)
	for _, r := range results {
		if len(out) >= limit {
			break
		}
		if exclude[r.ID] || isArchived(r.Metadata) {
			continue
		}
		link := strings.TrimSpace(r.Metadata[metaCrossModalLink])
		if link == "" || !linkIDs[link] {
			continue
		}
		exclude[r.ID] = true
		meta := make(map[string]string, len(r.Metadata))
		for k, v := range r.Metadata {
			meta[k] = v
		}
		out = append(out, KnowledgeSegment{
			ID:         r.ID,
			Content:    r.Content,
			Similarity: similarityToUnit(r.Similarity),
			Metadata:   meta,
		})
		mm.bumpRetrievalCount(col, r.ID, now)
	}
	return out
}

// PruneLowSignalContext archives low-signal history items to reduce active context pressure.
// Returns number of newly archived history records.
func (mm *MemoryManager) PruneLowSignalContext(maxArchive int) (int, error) {
	if mm == nil || mm.historyCollection == nil {
		return 0, fmt.Errorf("memory manager not initialized")
	}
	if maxArchive <= 0 {
		maxArchive = 8
	}
	count := mm.historyCollection.Count()
	if count == 0 {
		return 0, nil
	}
	results, err := mm.historyCollection.Query(context.Background(), " ", count, mm.namespaceWhereFilter(), nil)
	if err != nil {
		return 0, err
	}

	type candidate struct {
		doc     chromem.Document
		score   float64
		staleHr float64
	}
	var cands []candidate
	now := time.Now().UTC()
	for _, r := range results {
		doc, err := mm.historyCollection.GetByID(context.Background(), r.ID)
		if err != nil || doc.Metadata == nil || isArchived(doc.Metadata) {
			continue
		}
		imp := parseMetaFloat(doc.Metadata, metaBaseImportance, defaultBaseImportance)
		retr := parseMetaInt(doc.Metadata, metaRetrievalCount, 0)
		created := parseMetaTime(doc.Metadata, metaTimestamp)
		stale := 0.0
		if created != nil && !created.IsZero() && now.After(*created) {
			stale = now.Sub(*created).Hours()
		}
		// Lower score = lower signal.
		score := (imp * 0.7) + math.Min(float64(retr)/20.0, 0.3)
		cands = append(cands, candidate{doc: doc, score: score, staleHr: stale})
	}
	if len(cands) == 0 {
		return 0, nil
	}
	sort.SliceStable(cands, func(i, j int) bool {
		if cands[i].score == cands[j].score {
			return cands[i].staleHr > cands[j].staleHr
		}
		return cands[i].score < cands[j].score
	})

	archived := 0
	for _, c := range cands {
		if archived >= maxArchive {
			break
		}
		if c.doc.Metadata == nil {
			c.doc.Metadata = map[string]string{}
		}
		c.doc.Metadata[metaArchived] = "true"
		c.doc.Metadata[metaArchiveReason] = "attention_pressure_prune"
		c.doc.Metadata[metaLastReindexedAt] = now.Format(time.RFC3339)
		if err := mm.historyCollection.AddDocument(context.Background(), c.doc); err != nil {
			continue
		}
		mm.upsertBM25Doc(mm.historyCollection, c.doc)
		archived++
	}
	return archived, nil
}

// AdjustKnowledgeImportanceByID applies additive importance delta to a knowledge document.
func (mm *MemoryManager) AdjustKnowledgeImportanceByID(docID string, delta float64, reason string) error {
	docID = strings.TrimSpace(docID)
	if docID == "" {
		return fmt.Errorf("docID is empty")
	}

	mm.mu.Lock()
	defer mm.mu.Unlock()

	doc, err := mm.knowledgeCollection.GetByID(context.Background(), docID)
	if err != nil {
		return err
	}
	if doc.Metadata == nil {
		doc.Metadata = make(map[string]string)
	}
	cur := parseMetaFloat(doc.Metadata, metaBaseImportance, defaultBaseImportance)
	doc.Metadata[metaBaseImportance] = formatFloat(cur + delta)
	doc.Metadata["belief_shift_reason"] = strings.TrimSpace(reason)
	doc.Metadata["belief_shift_at"] = time.Now().UTC().Format(time.RFC3339)
	if err := mm.knowledgeCollection.AddDocument(context.Background(), doc); err != nil {
		return err
	}
	mm.upsertBM25Doc(mm.knowledgeCollection, doc)
	return nil
}

// ArchiveKnowledgeBySourcePath archives active knowledge documents for a given source_path.
func (mm *MemoryManager) ArchiveKnowledgeBySourcePath(sourcePath, reason string) (int, error) {
	if mm == nil || mm.knowledgeCollection == nil {
		return 0, fmt.Errorf("memory manager not initialized")
	}
	sourcePath = strings.TrimSpace(sourcePath)
	if sourcePath == "" {
		return 0, fmt.Errorf("sourcePath is required")
	}
	count := mm.knowledgeCollection.Count()
	if count == 0 {
		return 0, nil
	}
	results, err := mm.knowledgeCollection.Query(context.Background(), " ", count, mm.namespaceWhereFilter(), nil)
	if err != nil {
		return 0, err
	}
	archived := 0
	now := time.Now().UTC().Format(time.RFC3339)
	for _, r := range results {
		if strings.TrimSpace(r.Metadata["source_path"]) != sourcePath {
			continue
		}
		doc, getErr := mm.knowledgeCollection.GetByID(context.Background(), r.ID)
		if getErr != nil {
			continue
		}
		if doc.Metadata == nil {
			doc.Metadata = map[string]string{}
		}
		if isArchived(doc.Metadata) {
			continue
		}
		doc.Metadata[metaArchived] = "true"
		doc.Metadata[metaArchiveReason] = strings.TrimSpace(reason)
		doc.Metadata[metaLastReindexedAt] = now
		if addErr := mm.knowledgeCollection.AddDocument(context.Background(), doc); addErr != nil {
			continue
		}
		mm.upsertBM25Doc(mm.knowledgeCollection, doc)
		archived++
	}
	return archived, nil
}

// ApplyBeliefShift downgrades an old belief and elevates the winning new belief.
func (mm *MemoryManager) ApplyBeliefShift(loserID string, winnerID string, reason string) error {
	if strings.TrimSpace(loserID) == "" || strings.TrimSpace(winnerID) == "" {
		return fmt.Errorf("loserID and winnerID are required")
	}
	if err := mm.AdjustKnowledgeImportanceByID(loserID, -0.25, "downgraded: "+reason); err != nil {
		return err
	}
	if err := mm.AdjustKnowledgeImportanceByID(winnerID, +0.25, "elevated: "+reason); err != nil {
		return err
	}
	return nil
}

func (mm *MemoryManager) namespaceWhereFilter() map[string]string {
	ns := strings.TrimSpace(mm.ActiveNamespace())
	if ns == "" {
		return nil
	}
	return map[string]string{"namespace": ns}
}

func formatFloat(v float64) string {
	return strconv.FormatFloat(clamp01(v), 'f', 4, 64)
}

func clamp01(v float64) float64 {
	switch {
	case v < 0:
		return 0
	case v > 1:
		return 1
	default:
		return v
	}
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
