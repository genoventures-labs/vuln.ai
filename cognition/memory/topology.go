package memory

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	defaultTopologyPath = ".memory/topology_graph.json"
)

type TopologyEdgeType string

const (
	TopologyEdgeRef      TopologyEdgeType = "ref"
	TopologyEdgeURLHost  TopologyEdgeType = "url_host"
	TopologyEdgePathProx TopologyEdgeType = "path_proximity"
	TopologyEdgeLexical  TopologyEdgeType = "lexical_overlap"
	TopologyEdgeCrossSec TopologyEdgeType = "cross_section"
)

type TopologyConfig struct {
	Enabled         bool
	EdgeThreshold   float64
	MaxNeighbors    int
	ExpansionLimit  int
	RefWeight       float64
	URLHostWeight   float64
	PathWeight      float64
	LexicalWeight   float64
	LexicalMinScore float64
}

type SourceFingerprint struct {
	SourceType string
	SourceRef  string
	SourcePath string
	SourceURL  string
	SourceHost string
	Content    string
}

type TopologyNode struct {
	SourceType string   `json:"source_type"`
	SourceRef  string   `json:"source_ref"`
	SourceHost string   `json:"source_host,omitempty"`
	SourceDir  string   `json:"source_dir,omitempty"`
	Tokens     []string `json:"tokens,omitempty"`
	UpdatedAt  string   `json:"updated_at"`
}

type TopologyEdge struct {
	From      string           `json:"from"`
	To        string           `json:"to"`
	EdgeType  TopologyEdgeType `json:"edge_type"`
	Weight    float64          `json:"weight"`
	Evidence  string           `json:"evidence,omitempty"`
	UpdatedAt string           `json:"updated_at"`
}

type TopologyGraph struct {
	Version   int                       `json:"version"`
	Nodes     map[string]TopologyNode   `json:"nodes"`
	Adjacency map[string][]TopologyEdge `json:"adjacency"`

	mu sync.RWMutex `json:"-"`
}

// CrossSectionalLink captures a section-to-section semantic bridge.
type CrossSectionalLink struct {
	Entity   string
	SourceA  string
	SectionA string
	SourceB  string
	SectionB string
	Strength int
	LinkType string
}

func defaultTopologyConfig() TopologyConfig {
	weights := parseWeightListEnv("TALOS_TOPOLOGY_WEIGHTS", []float64{0.45, 0.20, 0.15, 0.20})
	return TopologyConfig{
		Enabled:         boolFromEnvMemory("TALOS_TOPOLOGY_ENABLED", true),
		EdgeThreshold:   clamp01(floatFromEnvMemory("TALOS_TOPOLOGY_EDGE_THRESHOLD", 0.45)),
		MaxNeighbors:    maxInt(1, intFromEnvMemory("TALOS_TOPOLOGY_MAX_NEIGHBORS", 8)),
		ExpansionLimit:  maxInt(0, intFromEnvMemory("TALOS_TOPOLOGY_EXPANSION_LIMIT", 6)),
		RefWeight:       weights[0],
		URLHostWeight:   weights[1],
		PathWeight:      weights[2],
		LexicalWeight:   weights[3],
		LexicalMinScore: clamp01(floatFromEnvMemory("TALOS_TOPOLOGY_LEXICAL_MIN_SCORE", 0.12)),
	}
}

func newTopologyGraph() *TopologyGraph {
	return &TopologyGraph{
		Version:   1,
		Nodes:     map[string]TopologyNode{},
		Adjacency: map[string][]TopologyEdge{},
	}
}

func LoadTopologyGraph(path string) (*TopologyGraph, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		path = defaultTopologyPath
	}
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return newTopologyGraph(), nil
		}
		return newTopologyGraph(), err
	}
	if len(strings.TrimSpace(string(b))) == 0 {
		return newTopologyGraph(), nil
	}
	var g TopologyGraph
	if err := json.Unmarshal(b, &g); err != nil {
		return newTopologyGraph(), err
	}
	if g.Version <= 0 {
		g.Version = 1
	}
	if g.Nodes == nil {
		g.Nodes = map[string]TopologyNode{}
	}
	if g.Adjacency == nil {
		g.Adjacency = map[string][]TopologyEdge{}
	}
	return &g, nil
}

func SaveTopologyGraph(path string, g *TopologyGraph) error {
	if g == nil {
		return fmt.Errorf("topology graph is nil")
	}
	path = strings.TrimSpace(path)
	if path == "" {
		path = defaultTopologyPath
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(g, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func (g *TopologyGraph) UpsertSource(fp SourceFingerprint, cfg TopologyConfig) (int, error) {
	if g == nil {
		return 0, fmt.Errorf("topology graph is nil")
	}
	ref := canonicalSourceRef(fp)
	if ref == "" {
		return 0, fmt.Errorf("source ref is empty")
	}
	node := buildTopologyNode(ref, fp)
	now := time.Now().UTC().Format(time.RFC3339)
	node.UpdatedAt = now

	g.mu.Lock()
	defer g.mu.Unlock()
	if g.Nodes == nil {
		g.Nodes = map[string]TopologyNode{}
	}
	if g.Adjacency == nil {
		g.Adjacency = map[string][]TopologyEdge{}
	}
	g.Nodes[ref] = node

	edgesAdded := 0
	for otherRef, other := range g.Nodes {
		if otherRef == ref {
			continue
		}
		weight, edgeType, evidence := scoreTopologyEdge(node, other, cfg)
		if weight < cfg.EdgeThreshold {
			continue
		}
		if setEdge(g.Adjacency, ref, otherRef, edgeType, weight, evidence, now) {
			edgesAdded++
		}
		if setEdge(g.Adjacency, otherRef, ref, edgeType, weight, evidence, now) {
			edgesAdded++
		}
	}
	pruneNeighbors(g.Adjacency, ref, cfg.MaxNeighbors)
	for otherRef := range g.Nodes {
		pruneNeighbors(g.Adjacency, otherRef, cfg.MaxNeighbors)
	}
	return edgesAdded, nil
}

func (g *TopologyGraph) Related(sourceRef string, maxN int) []TopologyEdge {
	if g == nil || maxN <= 0 {
		return nil
	}
	ref := strings.TrimSpace(sourceRef)
	g.mu.RLock()
	defer g.mu.RUnlock()
	edges := append([]TopologyEdge(nil), g.Adjacency[ref]...)
	sort.SliceStable(edges, func(i, j int) bool {
		if edges[i].Weight == edges[j].Weight {
			return edges[i].To < edges[j].To
		}
		return edges[i].Weight > edges[j].Weight
	})
	if len(edges) > maxN {
		edges = edges[:maxN]
	}
	return edges
}

// ReinforceCrossSectionLinks boosts topology edges from cross-sectional link evidence.
func (g *TopologyGraph) ReinforceCrossSectionLinks(links []CrossSectionalLink, maxNeighbors int, baseBoost float64) int {
	if g == nil || len(links) == 0 {
		return 0
	}
	boost := clamp01(baseBoost)
	if boost <= 0 {
		boost = 0.18
	}
	now := time.Now().UTC().Format(time.RFC3339)

	g.mu.Lock()
	defer g.mu.Unlock()
	if g.Nodes == nil {
		g.Nodes = map[string]TopologyNode{}
	}
	if g.Adjacency == nil {
		g.Adjacency = map[string][]TopologyEdge{}
	}

	changed := 0
	for _, l := range links {
		a := strings.TrimSpace(l.SourceA)
		b := strings.TrimSpace(l.SourceB)
		if a == "" || b == "" || a == b {
			continue
		}
		if _, ok := g.Nodes[a]; !ok {
			g.Nodes[a] = TopologyNode{
				SourceType: "cross_section",
				SourceRef:  a,
				UpdatedAt:  now,
			}
		}
		if _, ok := g.Nodes[b]; !ok {
			g.Nodes[b] = TopologyNode{
				SourceType: "cross_section",
				SourceRef:  b,
				UpdatedAt:  now,
			}
		}

		strength := maxInt(1, l.Strength)
		strengthBoost := clamp01(float64(strength) * 0.06)
		w := clamp01(boost + (strengthBoost * 0.5))
		evidence := fmt.Sprintf("cross_section entity=%s type=%s strength=%d sections=%s|%s", strings.TrimSpace(l.Entity), strings.TrimSpace(l.LinkType), strength, strings.TrimSpace(l.SectionA), strings.TrimSpace(l.SectionB))

		if setEdge(g.Adjacency, a, b, TopologyEdgeCrossSec, w, evidence, now) {
			changed++
		}
		if setEdge(g.Adjacency, b, a, TopologyEdgeCrossSec, w, evidence, now) {
			changed++
		}
	}

	if maxNeighbors <= 0 {
		maxNeighbors = 8
	}
	for ref := range g.Nodes {
		pruneNeighbors(g.Adjacency, ref, maxNeighbors)
	}
	return changed
}

func scoreTopologyEdge(a TopologyNode, b TopologyNode, cfg TopologyConfig) (float64, TopologyEdgeType, string) {
	refSignal := explicitReferenceSignal(a, b)
	hostSignal := urlHostSignal(a, b)
	pathSignal := pathProximitySignal(a, b)
	lexSignal := lexicalOverlapSignal(a, b)
	if lexSignal < cfg.LexicalMinScore {
		lexSignal = 0
	}
	components := []struct {
		Type  TopologyEdgeType
		Score float64
	}{
		{Type: TopologyEdgeRef, Score: refSignal},
		{Type: TopologyEdgeURLHost, Score: hostSignal},
		{Type: TopologyEdgePathProx, Score: pathSignal},
		{Type: TopologyEdgeLexical, Score: lexSignal},
	}
	best := components[0]
	for _, c := range components[1:] {
		if c.Score > best.Score {
			best = c
		}
	}
	score := (refSignal * cfg.RefWeight) + (hostSignal * cfg.URLHostWeight) + (pathSignal * cfg.PathWeight) + (lexSignal * cfg.LexicalWeight)
	evidence := fmt.Sprintf("ref=%.2f host=%.2f path=%.2f lexical=%.2f", refSignal, hostSignal, pathSignal, lexSignal)
	return clamp01(score), best.Type, evidence
}

func setEdge(adj map[string][]TopologyEdge, from string, to string, t TopologyEdgeType, w float64, evidence string, now string) bool {
	edges := adj[from]
	for i := range edges {
		if edges[i].To != to {
			continue
		}
		changed := edges[i].Weight != w || edges[i].EdgeType != t || edges[i].Evidence != evidence
		edges[i].Weight = w
		edges[i].EdgeType = t
		edges[i].Evidence = evidence
		edges[i].UpdatedAt = now
		adj[from] = edges
		return changed
	}
	adj[from] = append(edges, TopologyEdge{
		From:      from,
		To:        to,
		EdgeType:  t,
		Weight:    w,
		Evidence:  evidence,
		UpdatedAt: now,
	})
	return true
}

func pruneNeighbors(adj map[string][]TopologyEdge, ref string, maxN int) {
	if maxN <= 0 {
		return
	}
	edges := append([]TopologyEdge(nil), adj[ref]...)
	sort.SliceStable(edges, func(i, j int) bool {
		if edges[i].Weight == edges[j].Weight {
			return edges[i].To < edges[j].To
		}
		return edges[i].Weight > edges[j].Weight
	})
	if len(edges) > maxN {
		edges = edges[:maxN]
	}
	adj[ref] = edges
}

func canonicalSourceRef(fp SourceFingerprint) string {
	if v := strings.TrimSpace(fp.SourceRef); v != "" {
		return v
	}
	switch strings.TrimSpace(strings.ToLower(fp.SourceType)) {
	case "file":
		return strings.TrimSpace(fp.SourcePath)
	case "url":
		if u := strings.TrimSpace(fp.SourceURL); u != "" {
			return u
		}
	case "hf_dataset":
		if v := strings.TrimSpace(fp.SourcePath); v != "" {
			return v
		}
	}
	if v := strings.TrimSpace(fp.SourceURL); v != "" {
		return v
	}
	return strings.TrimSpace(fp.SourcePath)
}

func buildTopologyNode(ref string, fp SourceFingerprint) TopologyNode {
	host := strings.TrimSpace(fp.SourceHost)
	if host == "" {
		if u, err := url.Parse(strings.TrimSpace(fp.SourceURL)); err == nil {
			host = strings.ToLower(strings.TrimSpace(u.Hostname()))
		}
	}
	dir := ""
	if strings.EqualFold(strings.TrimSpace(fp.SourceType), "file") {
		dir = strings.TrimSpace(filepath.Dir(strings.TrimSpace(fp.SourcePath)))
		if dir == "." {
			dir = ""
		}
	}
	tokens := tokenizeTopology(strings.TrimSpace(fp.Content))
	if len(tokens) > 200 {
		tokens = tokens[:200]
	}
	return TopologyNode{
		SourceType: strings.TrimSpace(fp.SourceType),
		SourceRef:  strings.TrimSpace(ref),
		SourceHost: host,
		SourceDir:  dir,
		Tokens:     tokens,
	}
}

func explicitReferenceSignal(a TopologyNode, b TopologyNode) float64 {
	if a.SourceRef == "" || b.SourceRef == "" {
		return 0
	}
	la := strings.ToLower(strings.Join(a.Tokens, " "))
	lb := strings.ToLower(strings.Join(b.Tokens, " "))
	ar := strings.ToLower(strings.TrimSpace(a.SourceRef))
	br := strings.ToLower(strings.TrimSpace(b.SourceRef))
	if strings.Contains(la, br) || strings.Contains(lb, ar) {
		return 1.0
	}
	return 0
}

func urlHostSignal(a TopologyNode, b TopologyNode) float64 {
	ha := strings.TrimSpace(strings.ToLower(a.SourceHost))
	hb := strings.TrimSpace(strings.ToLower(b.SourceHost))
	if ha == "" || hb == "" {
		return 0
	}
	if ha == hb {
		return 1.0
	}
	return 0
}

func pathProximitySignal(a TopologyNode, b TopologyNode) float64 {
	if a.SourceDir == "" || b.SourceDir == "" {
		return 0
	}
	pa := strings.Split(strings.Trim(a.SourceDir, "/"), "/")
	pb := strings.Split(strings.Trim(b.SourceDir, "/"), "/")
	limit := minInt(len(pa), len(pb))
	if limit == 0 {
		return 0
	}
	common := 0
	for i := 0; i < limit; i++ {
		if pa[i] != pb[i] {
			break
		}
		common++
	}
	return clamp01(float64(common) / float64(limit))
}

func lexicalOverlapSignal(a TopologyNode, b TopologyNode) float64 {
	if len(a.Tokens) == 0 || len(b.Tokens) == 0 {
		return 0
	}
	am := map[string]bool{}
	for _, t := range a.Tokens {
		am[t] = true
	}
	bm := map[string]bool{}
	for _, t := range b.Tokens {
		bm[t] = true
	}
	intersection := 0
	for t := range am {
		if bm[t] {
			intersection++
		}
	}
	denom := minInt(len(am), len(bm))
	if denom <= 0 {
		return 0
	}
	return clamp01(float64(intersection) / float64(denom))
}

func tokenizeTopology(content string) []string {
	if strings.TrimSpace(content) == "" {
		return nil
	}
	content = strings.ToLower(content)
	repl := strings.NewReplacer("\n", " ", "\t", " ", ",", " ", ".", " ", ";", " ", ":", " ", "(", " ", ")", " ", "[", " ", "]", " ", "{", " ", "}", " ", "\"", " ", "'", " ", "`", " ", "|", " ", "\\", " ", "/", " ", "!", " ", "?", " ", "#", " ", "*", " ", "&", " ", "=", " ", "+", " ", "<", " ", ">", " ")
	content = repl.Replace(content)
	parts := strings.Fields(content)
	out := make([]string, 0, len(parts))
	seen := map[string]bool{}
	for _, p := range parts {
		if len(p) < 3 {
			continue
		}
		if seen[p] {
			continue
		}
		seen[p] = true
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

func intFromEnvMemory(key string, fallback int) int {
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

func floatFromEnvMemory(key string, fallback float64) float64 {
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

func boolFromEnvMemory(key string, fallback bool) bool {
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
