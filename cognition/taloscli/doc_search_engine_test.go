package taloscli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunDocSearchLiteralRecursive(t *testing.T) {
	root := t.TempDir()
	mustWriteTestFile(t, filepath.Join(root, "docs", "a.md"), "Sasswall mission line\nsecond line")
	mustWriteTestFile(t, filepath.Join(root, "docs", "b.txt"), "no match here")

	withWorkingDir(t, root, func() {
		report, err := runDocSearch(DocSearchOptions{
			Query: "sasswall",
			Dir:   ".",
		})
		if err != nil {
			t.Fatalf("runDocSearch failed: %v", err)
		}
		if report.MatchedFiles != 1 || report.TotalMatches == 0 {
			t.Fatalf("unexpected report: %+v", report)
		}
		if len(report.Citations) == 0 || !strings.Contains(report.Citations[0], "docs/a.md") {
			t.Fatalf("expected citation for docs/a.md, got: %+v", report.Citations)
		}
	})
}

func TestRunDocSearchRegex(t *testing.T) {
	root := t.TempDir()
	mustWriteTestFile(t, filepath.Join(root, "notes.md"), "SCAC alpha\nSasswall beta")
	withWorkingDir(t, root, func() {
		report, err := runDocSearch(DocSearchOptions{
			Query: "SCAC|Sasswall",
			Dir:   ".",
			Regex: true,
		})
		if err != nil {
			t.Fatalf("runDocSearch regex failed: %v", err)
		}
		if report.TotalMatches < 2 {
			t.Fatalf("expected regex matches, got %+v", report)
		}
	})
}

func TestRunDocSearchSkipsBinaryAndHidden(t *testing.T) {
	root := t.TempDir()
	mustWriteTestFile(t, filepath.Join(root, ".hidden", "secret.md"), "Sasswall")
	mustWriteTestFile(t, filepath.Join(root, "notes.md"), "Sasswall visible")
	binPath := filepath.Join(root, "bin.md")
	if err := os.MkdirAll(filepath.Dir(binPath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(binPath, []byte{0x00, 0x01, 0x02, 0x03}, 0o644); err != nil {
		t.Fatalf("write bin: %v", err)
	}
	withWorkingDir(t, root, func() {
		report, err := runDocSearch(DocSearchOptions{
			Query: "Sasswall",
			Dir:   ".",
		})
		if err != nil {
			t.Fatalf("runDocSearch failed: %v", err)
		}
		if report.SkippedHidden == 0 {
			t.Fatalf("expected hidden skip, got %+v", report)
		}
		if report.SkippedBinary == 0 {
			t.Fatalf("expected binary skip, got %+v", report)
		}
	})
}

func TestRunDocSearchTruncationCaps(t *testing.T) {
	root := t.TempDir()
	mustWriteTestFile(t, filepath.Join(root, "big.md"), "x\na\na\na\na\na\n")
	withWorkingDir(t, root, func() {
		report, err := runDocSearch(DocSearchOptions{
			Query:      "a",
			Dir:        ".",
			MaxMatches: 2,
			MaxPerFile: 2,
		})
		if err != nil {
			t.Fatalf("runDocSearch failed: %v", err)
		}
		if !report.Truncated {
			t.Fatalf("expected truncated report, got %+v", report)
		}
		if report.TotalMatches != 2 {
			t.Fatalf("expected 2 matches, got %d", report.TotalMatches)
		}
	})
}

func TestRunDocSearchBlocksOutsideWorkspace(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	mustWriteTestFile(t, filepath.Join(outside, "x.md"), "Sasswall")
	withWorkingDir(t, root, func() {
		_, err := runDocSearch(DocSearchOptions{
			Query: "Sasswall",
			Dir:   outside,
		})
		if err == nil {
			t.Fatal("expected outside workspace validation error")
		}
		if !strings.Contains(strings.ToLower(err.Error()), "outside workspace") {
			t.Fatalf("unexpected error: %v", err)
		}
	})
}

func mustWriteTestFile(t *testing.T, path string, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
}

func withWorkingDir(t *testing.T, dir string, fn func()) {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	defer func() { _ = os.Chdir(wd) }()
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	fn()
}
