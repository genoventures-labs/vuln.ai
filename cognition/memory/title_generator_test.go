package memory

import (
	"context"
	"strings"
	"testing"
)

func TestDeterministicChunkTitleFileSource(t *testing.T) {
	g := NewChunkTitleGenerator(nil, ChunkTitleConfig{MaxChars: 120})
	title, err := g.Generate(context.Background(), "file", "docs/guide.md", "Incident response guide for rotating API keys and validating audit logs.", 1, 3)
	if err != nil {
		t.Fatalf("generate title failed: %v", err)
	}
	if !strings.Contains(title, "guide.md") {
		t.Fatalf("expected file basename in title, got %q", title)
	}
	if !strings.Contains(title, "[1/3]") {
		t.Fatalf("expected chunk position in title, got %q", title)
	}
}

func TestDeterministicChunkTitleURLSource(t *testing.T) {
	g := NewChunkTitleGenerator(nil, ChunkTitleConfig{MaxChars: 120})
	title, err := g.Generate(context.Background(), "url", "https://example.com/security/posture", "Security posture notes include encryption requirements and backup cadence.", 1, 1)
	if err != nil {
		t.Fatalf("generate title failed: %v", err)
	}
	if !strings.Contains(title, "example.com") {
		t.Fatalf("expected URL host in title, got %q", title)
	}
}

func TestClampChunkTitleASCII(t *testing.T) {
	in := "Titel ✓ with unicode and extra words for truncation control"
	out := clampChunkTitleASCII(in, 20)
	if len(out) > 20 {
		t.Fatalf("expected max 20 chars, got %d", len(out))
	}
	if strings.Contains(out, "✓") {
		t.Fatalf("expected non-ascii removed, got %q", out)
	}
}

func TestGenerateChunkTitleEmptyContent(t *testing.T) {
	g := NewChunkTitleGenerator(nil, ChunkTitleConfig{})
	if _, err := g.Generate(context.Background(), "file", "a.txt", "   ", 1, 1); err == nil {
		t.Fatal("expected error for empty chunk content")
	}
}
