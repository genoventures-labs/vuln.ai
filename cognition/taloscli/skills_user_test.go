package taloscli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Thynaptic/P-LMv1/pkg/skills"
	"github.com/Thynaptic/P-LMv1/pkg/state"
)

func TestSkillsMigrateCommandDryRunAndApply(t *testing.T) {
	tmp := t.TempDir()
	t.Chdir(tmp)
	if err := os.MkdirAll(filepath.Join(".skills", "permanent"), 0o755); err != nil {
		t.Fatal(err)
	}
	legacyIndex := `{"skills":[{"skill_id":"legacy_skill","name":"legacy","intent":"legacy intent","root_dir":".skills/permanent/legacy_skill","source_path":".skills/permanent/legacy_skill/skill.go","manifest_path":".skills/permanent/legacy_skill/skill_manifest.json","enabled":true}]}`
	if err := os.WriteFile(filepath.Join(".skills", "permanent", "index.json"), []byte(legacyIndex), 0o644); err != nil {
		t.Fatal(err)
	}

	out := &bytes.Buffer{}
	errOut := &bytes.Buffer{}
	rootCmd.SetOut(out)
	rootCmd.SetErr(errOut)
	rootCmd.SetArgs([]string{"skills", "migrate"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("skills migrate dry-run failed: %v", err)
	}
	if !strings.Contains(out.String(), "DRY-RUN") {
		t.Fatalf("expected dry-run output, got: %s", out.String())
	}

	out.Reset()
	errOut.Reset()
	rootCmd.SetOut(out)
	rootCmd.SetErr(errOut)
	rootCmd.SetArgs([]string{"skills", "migrate", "--apply"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("skills migrate apply failed: %v", err)
	}
	if !strings.Contains(out.String(), "APPLIED") {
		t.Fatalf("expected applied output, got: %s", out.String())
	}
}

func TestSkillsListFiltersByActiveNamespaceBindings(t *testing.T) {
	prevStoreFactory := namespaceCapabilityStoreFactory
	prevRequestedNamespace := requestedNamespace
	defer func() {
		namespaceCapabilityStoreFactory = prevStoreFactory
		requestedNamespace = prevRequestedNamespace
	}()

	tmp := t.TempDir()
	t.Chdir(tmp)

	reg := skills.NewSkillRegistry(skills.PermanentSkillsRoot())
	if err := reg.Upsert(skills.SkillRecord{
		SkillID:      "skill_allowed",
		RevisionID:   "rev_allowed",
		Version:      "0.1.0",
		Status:       skills.SkillStatusActive,
		Name:         "allowed",
		Intent:       "allowed intent",
		RootDir:      ".skills/permanent/skill_allowed",
		SourcePath:   ".skills/permanent/skill_allowed/skill.go",
		ManifestPath: ".skills/permanent/skill_allowed/skill_manifest.json",
		CompileOK:    true,
		Enabled:      true,
	}); err != nil {
		t.Fatalf("upsert allowed skill: %v", err)
	}
	if err := reg.Upsert(skills.SkillRecord{
		SkillID:      "skill_blocked",
		RevisionID:   "rev_blocked",
		Version:      "0.1.0",
		Status:       skills.SkillStatusActive,
		Name:         "blocked",
		Intent:       "blocked intent",
		RootDir:      ".skills/permanent/skill_blocked",
		SourcePath:   ".skills/permanent/skill_blocked/skill.go",
		ManifestPath: ".skills/permanent/skill_blocked/skill_manifest.json",
		CompileOK:    true,
		Enabled:      true,
	}); err != nil {
		t.Fatalf("upsert blocked skill: %v", err)
	}

	storePath := filepath.Join(tmp, ".memory", "namespace_capabilities.json")
	store, err := state.NewNamespaceCapabilityStoreWithPath(storePath)
	if err != nil {
		t.Fatalf("namespace capability store: %v", err)
	}
	store.AllowSkill("training", "skill_allowed")
	if err := store.Save(); err != nil {
		t.Fatalf("save namespace capability store: %v", err)
	}
	namespaceCapabilityStoreFactory = func() (*state.NamespaceCapabilityStore, error) {
		return state.NewNamespaceCapabilityStoreWithPath(storePath)
	}
	requestedNamespace = "training"

	out := &bytes.Buffer{}
	errOut := &bytes.Buffer{}
	rootCmd.SetOut(out)
	rootCmd.SetErr(errOut)
	rootCmd.SetArgs([]string{"skills", "list"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("skills list failed: %v", err)
	}
	rendered := out.String()
	if !strings.Contains(rendered, "skill_allowed") {
		t.Fatalf("expected allowed skill in output, got: %s", rendered)
	}
	if strings.Contains(rendered, "skill_blocked") {
		t.Fatalf("expected blocked skill to be filtered out, got: %s", rendered)
	}
}
