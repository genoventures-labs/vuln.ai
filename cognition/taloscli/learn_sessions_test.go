package taloscli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLearnSessionAppendReadFilter(t *testing.T) {
	orig := learnSessionsPath
	origGrounding := nativeGroundingPath
	learnSessionsPath = filepath.Join(t.TempDir(), "learn_sessions.jsonl")
	nativeGroundingPath = filepath.Join(t.TempDir(), "native_text_grounding.jsonl")
	t.Cleanup(func() {
		learnSessionsPath = orig
		nativeGroundingPath = origGrounding
	})

	r1 := newLearnSession("DIRECTORY", "./docs", []string{"./docs"}, nil)
	r1.finish("SUCCESS", "ok", "", map[string]int64{"files_indexed": 10})
	if err := appendLearnSessionRecord(r1); err != nil {
		t.Fatalf("append r1: %v", err)
	}
	r2 := newLearnSession("REMOTE", "remote", []string{"https://example.com"}, nil)
	r2.finish("FAILED", "bad", "safety blocked", map[string]int64{"items_indexed": 0})
	if err := appendLearnSessionRecord(r2); err != nil {
		t.Fatalf("append r2: %v", err)
	}
	if err := os.WriteFile(learnSessionsPath, append(readAll(t, learnSessionsPath), []byte("{bad-json}\n")...), 0o644); err != nil {
		t.Fatalf("append corrupt line: %v", err)
	}

	records, corrupt, err := readLearnSessionRecords()
	if err != nil {
		t.Fatalf("read sessions: %v", err)
	}
	if len(records) != 2 {
		t.Fatalf("expected 2 records, got %d", len(records))
	}
	if corrupt != 1 {
		t.Fatalf("expected 1 corrupt record, got %d", corrupt)
	}

	filtered := filterLearnSessionRecords(records, "failed", nil, 5)
	if len(filtered) != 1 || strings.ToUpper(filtered[0].Status) != "FAILED" {
		t.Fatalf("unexpected failed filter result: %#v", filtered)
	}
	grounding, groundingCorrupt, err := readNativeGroundingRecords()
	if err != nil {
		t.Fatalf("read native grounding: %v", err)
	}
	if groundingCorrupt != 0 {
		t.Fatalf("expected no corrupt grounding records, got %d", groundingCorrupt)
	}
	if len(grounding) != 2 {
		t.Fatalf("expected 2 grounding records, got %d", len(grounding))
	}
	report := renderLearnedReport(records, 5, corrupt)
	if !strings.Contains(report, "TALOS LEARNED REPORT") || !strings.Contains(report, "AGGREGATE METRICS") {
		t.Fatalf("unexpected report: %s", report)
	}
}

func readAll(t *testing.T, p string) []byte {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("read file %s: %v", p, err)
	}
	return b
}
