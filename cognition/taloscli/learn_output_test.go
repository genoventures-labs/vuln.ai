package taloscli

import (
	"strings"
	"testing"

	"github.com/Thynaptic/P-LMv1/pkg/rag"
)

func TestRenderRemoteLearnSummaryFriendlyIncludesSourceAndProgress(t *testing.T) {
	out := renderRemoteLearnSummaryFriendly(
		[]string{"https://example.com/a", "https://example.com/b"},
		rag.RemoteIndexStats{ItemsFetched: 12, ItemsIndexed: 7, ChunksIndexed: 21},
	)
	for _, token := range []string{
		"LEARN REPORT",
		"STATUS",
		"SUCCESS",
		"https://example.com/a, https://example.com/b",
		"fetched: 12 | indexed: 7",
		"chunks_indexed: 21",
	} {
		if !strings.Contains(out, token) {
			t.Fatalf("expected token %q in output: %s", token, out)
		}
	}
}

func TestRenderDirectoryLearnSummaryFriendlyIncludesSourceAndProgress(t *testing.T) {
	out := renderDirectoryLearnSummaryFriendly("/tmp/docs", rag.IndexStats{
		FilesScanned:  8,
		FilesIndexed:  5,
		ChunksIndexed: 14,
	})
	for _, token := range []string{
		"LEARN REPORT",
		"STATUS",
		"SUCCESS",
		"/tmp/docs",
		"scanned_files: 8 | indexed_files: 5",
		"chunks_indexed: 14",
	} {
		if !strings.Contains(out, token) {
			t.Fatalf("expected token %q in output: %s", token, out)
		}
	}
}

func TestRenderInlineLearnSummaryFriendlyIncludesSource(t *testing.T) {
	out := renderInlineLearnSummaryFriendly(true, "/tmp/notes.md")
	for _, token := range []string{
		"LEARN REPORT",
		"STATUS",
		"SUCCESS",
		"file: /tmp/notes.md",
		"indexed: 1/1",
	} {
		if !strings.Contains(out, token) {
			t.Fatalf("expected token %q in output: %s", token, out)
		}
	}
}

func TestLearnCommandHasVerboseFlag(t *testing.T) {
	f := learnCmd.Flags().Lookup("verbose")
	if f == nil {
		t.Fatal("expected --verbose flag to be registered on learn command")
	}
}

func TestLearnCommandHasSelfTrainSpeechFlag(t *testing.T) {
	f := learnCmd.Flags().Lookup("self-train-speech")
	if f == nil {
		t.Fatal("expected --self-train-speech flag to be registered on learn command")
	}
}

func TestFormatRemoteLearnEventLineFriendlySuppressesSafetyNoise(t *testing.T) {
	line, ok := formatRemoteLearnEventLine(rag.RemoteEvent{
		Outcome:      "safety-cache-hit",
		Source:       "https://example.com",
		ItemsFetched: 1,
		ItemsIndexed: 1,
	}, false)
	if ok || line != "" {
		t.Fatalf("expected safety cache event suppressed in friendly mode, got ok=%t line=%q", ok, line)
	}
}

func TestFormatRemoteLearnEventLineFriendlyKeepsIndexedPage(t *testing.T) {
	line, ok := formatRemoteLearnEventLine(rag.RemoteEvent{
		Outcome:      "indexed",
		Source:       "https://example.com/news",
		ItemsFetched: 3,
		ItemsIndexed: 2,
	}, false)
	if !ok {
		t.Fatal("expected indexed event to render in friendly mode")
	}
	for _, token := range []string{"Indexed page:", "https://example.com/news", "3 fetched", "2 indexed"} {
		if !strings.Contains(line, token) {
			t.Fatalf("expected token %q in line: %s", token, line)
		}
	}
}
