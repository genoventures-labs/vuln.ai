package skills

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"
)

const (
	SkillStatusDraft      = "draft"
	SkillStatusValidated  = "validated"
	SkillStatusActive     = "active"
	SkillStatusDeprecated = "deprecated"
	SkillStatusRevoked    = "revoked"
)

type SkillIOContract struct {
	InputSchema  string `json:"input_schema,omitempty"`
	OutputSchema string `json:"output_schema,omitempty"`
}

type SkillCapabilityPolicy struct {
	AllowedTools   []string `json:"allowed_tools,omitempty"`
	AllowedPlugins []string `json:"allowed_plugins,omitempty"`
	AllowedDomains []string `json:"allowed_domains,omitempty"`
	SandboxProfile string   `json:"sandbox_profile,omitempty"`
}

type SkillProvenance struct {
	CreatedBy      string    `json:"created_by,omitempty"`
	CreatedAt      time.Time `json:"created_at,omitempty"`
	ParentRevision string    `json:"parent_revision,omitempty"`
}

type SkillSignature struct {
	Algorithm string `json:"algorithm,omitempty"`
	Digest    string `json:"digest,omitempty"`
	Signer    string `json:"signer,omitempty"`
}

type SkillManifestV3 struct {
	SchemaVersion    string                `json:"schema_version"`
	SkillID          string                `json:"skill_id"`
	RevisionID       string                `json:"revision_id"`
	Version          string                `json:"version,omitempty"`
	Name             string                `json:"name,omitempty"`
	Intent           string                `json:"intent,omitempty"`
	Description      string                `json:"description,omitempty"`
	ReasoningTier    string                `json:"reasoning_tier,omitempty"`
	TaskType         string                `json:"task_type,omitempty"`
	Status           string                `json:"status,omitempty"`
	IOContract       SkillIOContract       `json:"io_contract,omitempty"`
	CapabilityPolicy SkillCapabilityPolicy `json:"capability_policy,omitempty"`
	Provenance       SkillProvenance       `json:"provenance,omitempty"`
	Signature        SkillSignature        `json:"signature,omitempty"`
	SourcePath       string                `json:"source_path,omitempty"`
	PackageName      string                `json:"package,omitempty"`
	CompileOK        bool                  `json:"compile_ok,omitempty"`
}

func LoadManifest(path string) (SkillManifestV3, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return SkillManifestV3{}, fmt.Errorf("manifest path is empty")
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return SkillManifestV3{}, err
	}
	var raw map[string]any
	if err := json.Unmarshal(b, &raw); err != nil {
		return SkillManifestV3{}, err
	}
	if strings.EqualFold(strings.TrimSpace(stringVal(raw["schema_version"])), "v3") {
		var v3 SkillManifestV3
		if err := json.Unmarshal(b, &v3); err != nil {
			return SkillManifestV3{}, err
		}
		if strings.TrimSpace(v3.Status) == "" {
			v3.Status = SkillStatusDraft
		}
		if strings.TrimSpace(v3.Version) == "" {
			v3.Version = "0.1.0"
		}
		return v3, nil
	}
	v3 := SkillManifestV3{
		SchemaVersion: "v3",
		SkillID:       strings.TrimSpace(stringVal(raw["skill_id"])),
		RevisionID:    strings.TrimSpace(stringVal(raw["revision_id"])),
		Version:       strings.TrimSpace(stringVal(raw["version"])),
		Name:          strings.TrimSpace(stringVal(raw["name"])),
		Intent:        strings.TrimSpace(firstNonEmptyStr(stringVal(raw["intent"]), stringVal(raw["requirement"]))),
		Description:   strings.TrimSpace(stringVal(raw["description"])),
		ReasoningTier: strings.TrimSpace(stringVal(raw["reasoning_tier"])),
		TaskType:      strings.TrimSpace(stringVal(raw["task_type"])),
		Status:        strings.TrimSpace(stringVal(raw["status"])),
		SourcePath:    strings.TrimSpace(stringVal(raw["source_path"])),
		PackageName:   strings.TrimSpace(stringVal(raw["package"])),
		CompileOK:     boolVal(raw["compile_ok"]),
		Provenance: SkillProvenance{
			CreatedBy: "legacy_migration",
			CreatedAt: time.Now().UTC(),
		},
	}
	if v3.RevisionID == "" {
		v3.RevisionID = revisionIDFrom(v3.SkillID, v3.Version, v3.SourcePath)
	}
	if v3.Version == "" {
		v3.Version = "0.1.0"
	}
	if v3.Status == "" {
		v3.Status = SkillStatusDraft
	}
	if v3.CapabilityPolicy.SandboxProfile == "" {
		v3.CapabilityPolicy.SandboxProfile = "skill_default"
	}
	v3.Signature = signSkillManifest(v3)
	return v3, nil
}

func SaveManifestV3(path string, m SkillManifestV3) error {
	if strings.TrimSpace(path) == "" {
		return fmt.Errorf("manifest path is empty")
	}
	m.SchemaVersion = "v3"
	if strings.TrimSpace(m.Version) == "" {
		m.Version = "0.1.0"
	}
	if strings.TrimSpace(m.Status) == "" {
		m.Status = SkillStatusDraft
	}
	if strings.TrimSpace(m.RevisionID) == "" {
		m.RevisionID = revisionIDFrom(m.SkillID, m.Version, m.SourcePath)
	}
	m.Signature = signSkillManifest(m)
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o644)
}

func VerifyManifestSignature(m SkillManifestV3) bool {
	if strings.TrimSpace(m.Signature.Algorithm) == "" || strings.TrimSpace(m.Signature.Digest) == "" {
		return false
	}
	expected := signSkillManifest(m)
	return strings.EqualFold(strings.TrimSpace(expected.Digest), strings.TrimSpace(m.Signature.Digest))
}

func signSkillManifest(m SkillManifestV3) SkillSignature {
	h := sha256.New()
	payload := strings.Join([]string{
		strings.TrimSpace(m.SkillID),
		strings.TrimSpace(m.RevisionID),
		strings.TrimSpace(m.Version),
		strings.TrimSpace(m.Name),
		strings.TrimSpace(m.Intent),
		strings.TrimSpace(m.TaskType),
		strings.TrimSpace(m.SourcePath),
		strings.TrimSpace(m.PackageName),
		fmt.Sprintf("%t", m.CompileOK),
		strings.Join(m.CapabilityPolicy.AllowedTools, ","),
		strings.Join(m.CapabilityPolicy.AllowedPlugins, ","),
		strings.Join(m.CapabilityPolicy.AllowedDomains, ","),
		strings.TrimSpace(m.CapabilityPolicy.SandboxProfile),
	}, "|")
	_, _ = h.Write([]byte(payload))
	return SkillSignature{Algorithm: "sha256", Digest: hex.EncodeToString(h.Sum(nil)), Signer: "talos_local"}
}

func revisionIDFrom(skillID, version, sourcePath string) string {
	h := sha256.New()
	_, _ = h.Write([]byte(strings.TrimSpace(skillID) + "|" + strings.TrimSpace(version) + "|" + strings.TrimSpace(sourcePath) + "|" + fmt.Sprintf("%d", time.Now().UTC().UnixNano())))
	return "rev_" + hex.EncodeToString(h.Sum(nil))[:16]
}

func firstNonEmptyStr(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func stringVal(v any) string {
	s, _ := v.(string)
	return s
}

func boolVal(v any) bool {
	b, ok := v.(bool)
	if ok {
		return b
	}
	return false
}
