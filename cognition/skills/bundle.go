package skills

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type SkillBundle struct {
	SchemaVersion string          `json:"schema_version"`
	ExportedAt    time.Time       `json:"exported_at"`
	Manifest      SkillManifestV3 `json:"manifest"`
	Source        string          `json:"source"`
	Digest        string          `json:"digest"`
	Signer        string          `json:"signer"`
}

func ExportBundle(rec SkillRecord, outPath string) error {
	if strings.TrimSpace(rec.ManifestPath) == "" || strings.TrimSpace(rec.SourcePath) == "" {
		return fmt.Errorf("record missing source or manifest path")
	}
	m, err := LoadManifest(rec.ManifestPath)
	if err != nil {
		return err
	}
	src, err := os.ReadFile(rec.SourcePath)
	if err != nil {
		return err
	}
	bundle := SkillBundle{
		SchemaVersion: "v1",
		ExportedAt:    time.Now().UTC(),
		Manifest:      m,
		Source:        string(src),
		Signer:        "talos_local",
	}
	bundle.Digest = digestBundle(bundle)
	b, err := json.MarshalIndent(bundle, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
		return err
	}
	return os.WriteFile(outPath, b, 0o644)
}

func ImportBundle(root string, inPath string) (SkillRecord, error) {
	if strings.TrimSpace(root) == "" {
		root = PermanentSkillsRoot()
	}
	b, err := os.ReadFile(strings.TrimSpace(inPath))
	if err != nil {
		return SkillRecord{}, err
	}
	var bundle SkillBundle
	if err := json.Unmarshal(b, &bundle); err != nil {
		return SkillRecord{}, err
	}
	if strings.TrimSpace(bundle.Digest) == "" || !strings.EqualFold(bundle.Digest, digestBundle(bundle)) {
		return SkillRecord{}, fmt.Errorf("bundle signature digest verification failed")
	}
	if !VerifyManifestSignature(bundle.Manifest) {
		return SkillRecord{}, fmt.Errorf("bundle manifest signature verification failed")
	}
	dir := filepath.Join(root, strings.TrimSpace(bundle.Manifest.SkillID)+"_"+strings.TrimSpace(bundle.Manifest.RevisionID))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return SkillRecord{}, err
	}
	sourcePath := filepath.Join(dir, "skill.go")
	manifestPath := filepath.Join(dir, "skill_manifest.json")
	if err := os.WriteFile(sourcePath, []byte(bundle.Source), 0o644); err != nil {
		return SkillRecord{}, err
	}
	m := bundle.Manifest
	m.SourcePath = sourcePath
	if err := SaveManifestV3(manifestPath, m); err != nil {
		return SkillRecord{}, err
	}
	rec := SkillRecord{
		SkillID:        strings.TrimSpace(m.SkillID),
		RevisionID:     strings.TrimSpace(m.RevisionID),
		Version:        strings.TrimSpace(m.Version),
		Status:         SkillStatusDraft,
		Name:           strings.TrimSpace(m.Name),
		Intent:         strings.TrimSpace(m.Intent),
		Description:    strings.TrimSpace(m.Description),
		ReasoningTier:  strings.TrimSpace(m.ReasoningTier),
		TaskType:       strings.TrimSpace(m.TaskType),
		RootDir:        dir,
		SourcePath:     sourcePath,
		ManifestPath:   manifestPath,
		PackageName:    strings.TrimSpace(m.PackageName),
		CompileOK:      m.CompileOK,
		Enabled:        true,
		Active:         false,
		Provenance:     "bundle_import",
		AllowedTools:   append([]string(nil), m.CapabilityPolicy.AllowedTools...),
		AllowedDomains: append([]string(nil), m.CapabilityPolicy.AllowedDomains...),
		SandboxProfile: firstNonEmptyStr(strings.TrimSpace(m.CapabilityPolicy.SandboxProfile), "skill_default"),
	}
	return rec, nil
}

func digestBundle(b SkillBundle) string {
	h := sha256.New()
	_, _ = h.Write([]byte(strings.TrimSpace(b.SchemaVersion)))
	_, _ = h.Write([]byte(strings.TrimSpace(b.Manifest.SkillID)))
	_, _ = h.Write([]byte(strings.TrimSpace(b.Manifest.RevisionID)))
	_, _ = h.Write([]byte(strings.TrimSpace(b.Manifest.Version)))
	_, _ = h.Write([]byte(strings.TrimSpace(b.Source)))
	return hex.EncodeToString(h.Sum(nil))
}
