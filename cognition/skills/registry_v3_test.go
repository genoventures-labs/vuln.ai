package skills

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestRegistryRevisionLifecycle(t *testing.T) {
	reg := NewSkillRegistry(t.TempDir())
	rec1 := SkillRecord{SkillID: "skillA", RevisionID: "r1", Version: "0.1.0", Status: SkillStatusDraft, Name: "a", Intent: "i", RootDir: "x", SourcePath: "x/a.go", ManifestPath: "x/m1.json", Enabled: true}
	rec2 := SkillRecord{SkillID: "skillA", RevisionID: "r2", Version: "0.2.0", Status: SkillStatusValidated, Name: "a", Intent: "i", RootDir: "x", SourcePath: "x/b.go", ManifestPath: "x/m2.json", Enabled: true}
	if err := reg.Upsert(rec1); err != nil {
		t.Fatalf("upsert r1: %v", err)
	}
	if err := reg.Upsert(rec2); err != nil {
		t.Fatalf("upsert r2: %v", err)
	}
	if err := reg.ActivateRevision("skillA", "r2"); err != nil {
		t.Fatalf("activate r2: %v", err)
	}
	active, ok, err := reg.ResolveActive("skillA")
	if err != nil || !ok || active == nil {
		t.Fatalf("resolve active failed: ok=%t err=%v", ok, err)
	}
	if active.RevisionID != "r2" || active.Status != SkillStatusActive {
		t.Fatalf("unexpected active revision: %+v", active)
	}
	if err := reg.DeprecateRevision("skillA", "r2"); err != nil {
		t.Fatalf("deprecate r2: %v", err)
	}
	revs, err := reg.ListRevisions("skillA")
	if err != nil {
		t.Fatalf("list revisions: %v", err)
	}
	foundDep := false
	for _, r := range revs {
		if r.RevisionID == "r2" && r.Status == SkillStatusDeprecated {
			foundDep = true
		}
	}
	if !foundDep {
		t.Fatalf("expected deprecated r2 in revisions: %+v", revs)
	}
}

func TestMigrateLegacyDryRunAndApply(t *testing.T) {
	root := t.TempDir()
	reg := NewSkillRegistry(root)
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	legacy := map[string]any{
		"skills": []map[string]any{
			{
				"skill_id":      "legacy_skill",
				"name":          "legacy",
				"intent":        "legacy intent",
				"root_dir":      filepath.Join(root, "legacy"),
				"source_path":   filepath.Join(root, "legacy", "skill.go"),
				"manifest_path": filepath.Join(root, "legacy", "skill_manifest.json"),
				"enabled":       true,
			},
		},
	}
	b, _ := json.Marshal(legacy)
	if err := os.WriteFile(reg.IndexPath, b, 0o644); err != nil {
		t.Fatal(err)
	}
	report, err := reg.MigrateLegacy(false)
	if err != nil {
		t.Fatalf("dry-run migrate: %v", err)
	}
	if report.LegacyRecords == 0 || report.UpdatedRecords == 0 {
		t.Fatalf("expected legacy and updated records, got %+v", report)
	}
	report2, err := reg.MigrateLegacy(true)
	if err != nil {
		t.Fatalf("apply migrate: %v", err)
	}
	if !report2.Applied {
		t.Fatalf("expected applied=true")
	}
	recs, err := reg.ListEnabled()
	if err != nil {
		t.Fatalf("list enabled after migrate: %v", err)
	}
	if len(recs) != 1 || recs[0].RevisionID == "" || recs[0].Version == "" || recs[0].Status == "" {
		t.Fatalf("expected migrated V3 record, got %+v", recs)
	}
}
