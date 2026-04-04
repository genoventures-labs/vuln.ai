package skills

import "testing"

func TestSkillRegistry_UpsertListFind(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	reg := NewSkillRegistry(root)
	rec := SkillRecord{
		SkillID:      "s1",
		Name:         "talos_reasoning",
		Intent:       "improve reasoning strategy for forensic analysis",
		TaskType:     "forensic",
		RootDir:      root + "/s1",
		SourcePath:   root + "/s1/skill.go",
		ManifestPath: root + "/s1/skill_manifest.json",
		CompileOK:    true,
		Enabled:      true,
	}
	if err := reg.Upsert(rec); err != nil {
		t.Fatalf("Upsert failed: %v", err)
	}
	enabled, err := reg.ListEnabled()
	if err != nil {
		t.Fatalf("ListEnabled failed: %v", err)
	}
	if len(enabled) != 1 {
		t.Fatalf("expected 1 enabled skill, got %d", len(enabled))
	}
	matched, ok, err := reg.FindMatch("talos_reasoning", "forensic reasoning strategy needed", "forensic")
	if err != nil {
		t.Fatalf("FindMatch failed: %v", err)
	}
	if !ok || matched == nil || matched.SkillID != "s1" {
		t.Fatalf("expected match s1, got ok=%v rec=%+v", ok, matched)
	}
}
