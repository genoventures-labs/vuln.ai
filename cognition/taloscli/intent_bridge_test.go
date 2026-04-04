package taloscli

import "testing"

func TestNormalizeModuleIntentResearch(t *testing.T) {
	got := normalizeModuleIntent("research", "look into this")
	want := "Investigate the target topic with cited sources, then return key findings, risks, and next actions."
	if got != want {
		t.Fatalf("expected %q, got %q", want, got)
	}

	got = normalizeModuleIntent("research", "research memory replay weighting")
	want = "Research objective: memory replay weighting"
	if got != want {
		t.Fatalf("expected %q, got %q", want, got)
	}
}

func TestModuleCommandHints(t *testing.T) {
	hints := moduleCommandHints("research", "compare cross-section evidence and verify citation quality")
	if len(hints) == 0 {
		t.Fatal("expected research hints")
	}
	foundMulti := false
	foundCitation := false
	for _, h := range hints {
		if h == "multi_document_analysis" {
			foundMulti = true
		}
		if h == "citation_priority" {
			foundCitation = true
		}
	}
	if !foundMulti || !foundCitation {
		t.Fatalf("expected multi_document_analysis and citation_priority hints, got %v", hints)
	}
}

func TestPreprocessUserIntentTrivialChatBypass(t *testing.T) {
	env, proceed, clarification := preprocessUserIntent("hi", nil, nil, "chat")
	if !proceed {
		t.Fatalf("expected proceed for trivial chat input, clarification=%q", clarification)
	}
	if env.Normalized != "hi" {
		t.Fatalf("expected unchanged normalized intent, got %q", env.Normalized)
	}
}
