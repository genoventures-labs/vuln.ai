package memory

import "testing"

func TestBM25PrefersExactLexicalMatch(t *testing.T) {
	idx := NewBM25Index()
	idx.Rebuild([]bm25Document{
		{ID: "a", Content: "database migration rollback playbook", Metadata: map[string]string{"namespace": "ops"}},
		{ID: "b", Content: "general deployment checklist", Metadata: map[string]string{"namespace": "ops"}},
		{ID: "c", Content: "incident report timeline", Metadata: map[string]string{"namespace": "ops"}},
	})

	results := idx.Search("rollback migration", 3, "ops")
	if len(results) == 0 {
		t.Fatalf("expected bm25 results")
	}
	if results[0].ID != "a" {
		t.Fatalf("expected top result 'a', got %q", results[0].ID)
	}
}

func TestBM25RespectsNamespaceFilter(t *testing.T) {
	idx := NewBM25Index()
	idx.Rebuild([]bm25Document{
		{ID: "a", Content: "security hardening checklist", Metadata: map[string]string{"namespace": "alpha"}},
		{ID: "b", Content: "security hardening checklist", Metadata: map[string]string{"namespace": "beta"}},
	})

	results := idx.Search("security checklist", 5, "beta")
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].ID != "b" {
		t.Fatalf("expected result 'b', got %q", results[0].ID)
	}
}
