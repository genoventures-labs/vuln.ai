package skills

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestLoadManifestMigratesLegacy(t *testing.T) {
	d := t.TempDir()
	p := filepath.Join(d, "skill_manifest.json")
	legacy := map[string]any{
		"skill_id":    "skill_1",
		"name":        "jira_helper",
		"requirement": "summarize jira",
		"source_path": "./skill.go",
		"compile_ok":  true,
	}
	b, _ := json.Marshal(legacy)
	if err := os.WriteFile(p, b, 0o644); err != nil {
		t.Fatal(err)
	}
	m, err := LoadManifest(p)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if m.SchemaVersion != "v3" || m.RevisionID == "" || m.Version == "" {
		t.Fatalf("expected migrated v3 manifest, got %+v", m)
	}
	if !VerifyManifestSignature(m) {
		t.Fatalf("expected valid signature")
	}
}
