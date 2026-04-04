package skills

import (
	"os"
	"path/filepath"
	"testing"
)

func TestBundleExportImportRoundTrip(t *testing.T) {
	root := t.TempDir()
	skillDir := filepath.Join(root, "s1")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(skillDir, "skill.go")
	manifest := filepath.Join(skillDir, "skill_manifest.json")
	if err := os.WriteFile(source, []byte("package x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	m := SkillManifestV3{SchemaVersion: "v3", SkillID: "s1", RevisionID: "r1", Version: "0.1.0", Name: "skill", Intent: "intent", Status: SkillStatusActive, SourcePath: source, PackageName: "x", CompileOK: true}
	if err := SaveManifestV3(manifest, m); err != nil {
		t.Fatal(err)
	}
	rec := SkillRecord{SkillID: "s1", RevisionID: "r1", Version: "0.1.0", Status: SkillStatusActive, Name: "skill", Intent: "intent", RootDir: skillDir, SourcePath: source, ManifestPath: manifest, Enabled: true, Active: true}
	bundle := filepath.Join(root, "bundle.json")
	if err := ExportBundle(rec, bundle); err != nil {
		t.Fatalf("export bundle: %v", err)
	}
	imported, err := ImportBundle(filepath.Join(root, "imported"), bundle)
	if err != nil {
		t.Fatalf("import bundle: %v", err)
	}
	if imported.SkillID != "s1" || imported.RevisionID == "" {
		t.Fatalf("unexpected imported record: %+v", imported)
	}
}
