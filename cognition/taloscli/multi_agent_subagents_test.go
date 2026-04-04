package taloscli

import "testing"

func TestParseSelectedSubAgentsDocSearcherAlias(t *testing.T) {
	got, err := parseSelectedSubAgents("planner,doc-search,verifier")
	if err != nil {
		t.Fatalf("parseSelectedSubAgents error: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("expected 3 agents, got %d (%v)", len(got), got)
	}
	if got[1] != "docsearcher" {
		t.Fatalf("expected docsearcher alias resolution, got %v", got)
	}
}
