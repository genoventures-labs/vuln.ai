package taloscli

import "testing"

func TestLearnProgressTracksFileEvents(t *testing.T) {
	p := newLearnProgress()
	p.enabled = false
	p.onFileEvent("indexed", 3, 1, 1)
	p.onFileEvent("skipped-binary", 0, 2, 1)
	p.onFileEvent("index-error", 0, 3, 1)

	if p.scanned != 3 {
		t.Fatalf("expected scanned=3, got %d", p.scanned)
	}
	if p.indexed != 1 || p.chunks != 3 {
		t.Fatalf("unexpected indexed/chunks: %d/%d", p.indexed, p.chunks)
	}
	if p.skipped != 1 || p.errors != 1 {
		t.Fatalf("unexpected skipped/errors: %d/%d", p.skipped, p.errors)
	}
}

func TestLearnProgressTracksRemoteEvents(t *testing.T) {
	p := newLearnProgress()
	p.enabled = false
	p.onRemoteEvent("indexed")
	p.onRemoteEvent("blocked")
	p.onRemoteEvent("skipped")

	if p.itemsFetched != 3 {
		t.Fatalf("expected fetched=3, got %d", p.itemsFetched)
	}
	if p.indexed != 1 {
		t.Fatalf("expected indexed=1, got %d", p.indexed)
	}
	if p.errors != 1 || p.skipped != 1 {
		t.Fatalf("unexpected errors/skipped: %d/%d", p.errors, p.skipped)
	}
}
