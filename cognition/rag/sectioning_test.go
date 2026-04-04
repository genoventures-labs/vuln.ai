package rag

import "testing"

func TestInferChunkSectionsWithHeadings(t *testing.T) {
	chunks := []string{
		"## Intro\nThis section introduces the system.",
		"## API\nThis section describes interfaces.",
	}
	got := inferChunkSections(chunks, "docs/guide.md")
	if len(got) != 2 {
		t.Fatalf("expected 2 section metas, got %d", len(got))
	}
	if got[0].Title != "Intro" {
		t.Fatalf("expected Intro, got %q", got[0].Title)
	}
	if got[1].Title != "API" {
		t.Fatalf("expected API, got %q", got[1].Title)
	}
	if got[1].ID == got[0].ID {
		t.Fatalf("expected different section ids for heading transitions")
	}
}

func TestInferChunkSectionsFallbackGeneral(t *testing.T) {
	chunks := []string{
		"plain text one",
		"plain text two",
	}
	got := inferChunkSections(chunks, "docs/notes.txt")
	if len(got) != 2 {
		t.Fatalf("expected 2 section metas, got %d", len(got))
	}
	if got[0].Title != "General" || got[1].Title != "General" {
		t.Fatalf("expected General fallback titles, got %+v", got)
	}
	if got[0].ID != got[1].ID {
		t.Fatalf("expected one shared section for non-heading chunks")
	}
}
