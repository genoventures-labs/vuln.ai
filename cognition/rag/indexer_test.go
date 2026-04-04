package rag

import "testing"

func TestDefaultIndexOptionsChunkTitlesEnabled(t *testing.T) {
	opts := DefaultIndexOptions()
	if !opts.GenerateChunkTitles {
		t.Fatal("expected GenerateChunkTitles enabled by default")
	}
	if opts.ChunkTitleMaxChars <= 0 {
		t.Fatalf("expected positive ChunkTitleMaxChars, got %d", opts.ChunkTitleMaxChars)
	}
}

func TestDeterministicChunkFallbackTitle(t *testing.T) {
	out := deterministicChunkFallbackTitle("docs/guide.md", 2, 4)
	if out != "guide.md [2/4]" {
		t.Fatalf("unexpected fallback title: %q", out)
	}
}
