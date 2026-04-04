package memory

import (
	"path/filepath"
	"testing"
)

func TestTopologyUpsertAndRelated(t *testing.T) {
	g := newTopologyGraph()
	cfg := defaultTopologyConfig()
	cfg.EdgeThreshold = 0.20
	cfg.MaxNeighbors = 8

	_, err := g.UpsertSource(SourceFingerprint{
		SourceType: "file",
		SourceRef:  "docs/runbook.md",
		SourcePath: "docs/runbook.md",
		Content:    "See docs/policy.md for security controls and deployment rules.",
	}, cfg)
	if err != nil {
		t.Fatalf("upsert first source failed: %v", err)
	}
	edges, err := g.UpsertSource(SourceFingerprint{
		SourceType: "file",
		SourceRef:  "docs/policy.md",
		SourcePath: "docs/policy.md",
		Content:    "security controls deployment policy reference",
	}, cfg)
	if err != nil {
		t.Fatalf("upsert second source failed: %v", err)
	}
	if edges <= 0 {
		t.Fatalf("expected edges to be added, got %d", edges)
	}

	related := g.Related("docs/runbook.md", 4)
	if len(related) == 0 {
		t.Fatal("expected at least one related edge")
	}
	if related[0].To != "docs/policy.md" {
		t.Fatalf("expected docs/policy.md as top related edge, got %q", related[0].To)
	}
}

func TestTopologySaveLoadRoundTrip(t *testing.T) {
	g := newTopologyGraph()
	g.Nodes["docs/a.md"] = TopologyNode{SourceType: "file", SourceRef: "docs/a.md"}
	g.Adjacency["docs/a.md"] = []TopologyEdge{{
		From: "docs/a.md", To: "docs/b.md", EdgeType: TopologyEdgePathProx, Weight: 0.5,
	}}
	p := filepath.Join(t.TempDir(), "topology_graph.json")
	if err := SaveTopologyGraph(p, g); err != nil {
		t.Fatalf("save topology failed: %v", err)
	}
	loaded, err := LoadTopologyGraph(p)
	if err != nil {
		t.Fatalf("load topology failed: %v", err)
	}
	if len(loaded.Nodes) != 1 {
		t.Fatalf("expected 1 node, got %d", len(loaded.Nodes))
	}
	if len(loaded.Adjacency["docs/a.md"]) != 1 {
		t.Fatalf("expected 1 adjacency edge, got %d", len(loaded.Adjacency["docs/a.md"]))
	}
}

func TestTopologyRefFromMetadata(t *testing.T) {
	ref := topologyRefFromMetadata(map[string]string{
		"hf_dataset": "my/ds",
		"hf_split":   "train",
	})
	if ref != "hf:my/ds:train" {
		t.Fatalf("unexpected ref: %q", ref)
	}
}
