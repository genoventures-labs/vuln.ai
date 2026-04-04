package rag

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestManifestPathForRoot(t *testing.T) {
	p := ManifestPathForRoot("/tmp/example")
	if !strings.Contains(p, ".memory/incremental/") {
		t.Fatalf("unexpected manifest path: %s", p)
	}
	if !strings.HasSuffix(p, ".json") {
		t.Fatalf("expected json manifest path, got: %s", p)
	}
}

func TestComputeFastFileFingerprint(t *testing.T) {
	f := filepath.Join(t.TempDir(), "doc.txt")
	if err := os.WriteFile(f, []byte("hello incremental index"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	size, mod, hash, err := ComputeFastFileFingerprint(f, 64)
	if err != nil {
		t.Fatalf("fingerprint failed: %v", err)
	}
	if size <= 0 || mod <= 0 || strings.TrimSpace(hash) == "" {
		t.Fatalf("invalid fingerprint values: size=%d mod=%d hash=%q", size, mod, hash)
	}
}

func TestManifestRoundTrip(t *testing.T) {
	m := IncrementalManifest{
		Version: 1,
		Root:    "/tmp/root",
		Files: map[string]IncrementalFileRecord{
			"a.txt": {
				RelPath:       "a.txt",
				Size:          12,
				ModTimeUnix:   1234,
				FastHash:      "abc",
				LastIndexedAt: "2026-02-18T00:00:00Z",
			},
		},
	}
	p := filepath.Join(t.TempDir(), "manifest.json")
	if err := SaveIncrementalManifest(p, m); err != nil {
		t.Fatalf("save manifest: %v", err)
	}
	loaded, err := LoadIncrementalManifest(p)
	if err != nil {
		t.Fatalf("load manifest: %v", err)
	}
	if loaded.Root != m.Root {
		t.Fatalf("root mismatch: got %q want %q", loaded.Root, m.Root)
	}
	if len(loaded.Files) != 1 {
		t.Fatalf("expected 1 file record, got %d", len(loaded.Files))
	}
}
