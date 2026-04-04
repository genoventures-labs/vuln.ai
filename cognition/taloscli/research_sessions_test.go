package taloscli

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPersistResearchSessionAndResolveLatest(t *testing.T) {
	origSessions := researchSessionsPath
	origArtifacts := researchArtifactsDir
	researchSessionsPath = filepath.Join(t.TempDir(), "research_sessions.jsonl")
	researchArtifactsDir = filepath.Join(t.TempDir(), "artifacts")
	t.Cleanup(func() {
		researchSessionsPath = origSessions
		researchArtifactsDir = origArtifacts
	})

	report := researchReport{
		Mode:      "run",
		Query:     "q",
		Executive: "summary",
		Findings:  []researchFinding{{Text: "f1", Verified: true, Refs: []int{1}}},
		Sources:   []string{"https://example.com"},
	}
	rec, err := persistResearchSession("q", "run", report, "SUCCESS", "", 2, researchExecutionContext{
		ProfileName:       "market-scan",
		ProfileCategories: []string{"crypto", "daily"},
	})
	if err != nil {
		t.Fatalf("persist session: %v", err)
	}
	if rec.SessionID == "" {
		t.Fatal("expected session id")
	}
	if _, err := os.Stat(filepath.Join(researchArtifactsDir, rec.SessionID+".json")); err != nil {
		t.Fatalf("expected artifact file, got: %v", err)
	}

	id, err := resolveResearchArtifactID("latest")
	if err != nil {
		t.Fatalf("resolve latest: %v", err)
	}
	if id != rec.SessionID {
		t.Fatalf("expected latest id %s, got %s", rec.SessionID, id)
	}

	artifact, err := loadResearchArtifactByID(id)
	if err != nil {
		t.Fatalf("load artifact: %v", err)
	}
	if artifact.Query != "q" || len(artifact.Sources) != 1 {
		t.Fatalf("unexpected artifact data: %+v", artifact)
	}
	if artifact.ProfileName != "market-scan" {
		t.Fatalf("expected profile metadata in artifact, got %+v", artifact)
	}
}
