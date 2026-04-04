package memory

import "testing"

func TestReinforceCrossSectionLinksAddsEdges(t *testing.T) {
	g := newTopologyGraph()
	now := "2026-02-18T00:00:00Z"
	g.Nodes["docs/a.md"] = TopologyNode{SourceType: "file", SourceRef: "docs/a.md", UpdatedAt: now}
	g.Nodes["docs/b.md"] = TopologyNode{SourceType: "file", SourceRef: "docs/b.md", UpdatedAt: now}

	changed := g.ReinforceCrossSectionLinks([]CrossSectionalLink{
		{
			Entity:   "JWT",
			SourceA:  "docs/a.md",
			SectionA: "auth",
			SourceB:  "docs/b.md",
			SectionB: "gateway",
			Strength: 3,
			LinkType: "inter_source",
		},
	}, 8, 0.2)

	if changed == 0 {
		t.Fatal("expected cross-sectional reinforcement to change edges")
	}
	edges := g.Related("docs/a.md", 8)
	if len(edges) == 0 {
		t.Fatal("expected reinforced adjacency for docs/a.md")
	}
	found := false
	for _, e := range edges {
		if e.To == "docs/b.md" && e.EdgeType == TopologyEdgeCrossSec {
			found = true
			if e.Weight <= 0 {
				t.Fatalf("expected positive weight, got %.2f", e.Weight)
			}
		}
	}
	if !found {
		t.Fatalf("expected cross_section edge to docs/b.md, got %+v", edges)
	}
}
