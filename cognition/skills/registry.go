package skills

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/Thynaptic/P-LMv1/pkg/tools"
)

const defaultRegistryFile = "index.json"

type SkillRecord struct {
	SkillID        string    `json:"skill_id"`
	RevisionID     string    `json:"revision_id,omitempty"`
	Version        string    `json:"version,omitempty"`
	Status         string    `json:"status,omitempty"`
	Name           string    `json:"name"`
	Intent         string    `json:"intent"`
	Description    string    `json:"description,omitempty"`
	Namespace      string    `json:"namespace,omitempty"`
	ReasoningTier  string    `json:"reasoning_tier,omitempty"`
	TaskType       string    `json:"task_type,omitempty"`
	RootDir        string    `json:"root_dir"`
	SourcePath     string    `json:"source_path"`
	ManifestPath   string    `json:"manifest_path"`
	PackageName    string    `json:"package_name,omitempty"`
	CompileOK      bool      `json:"compile_ok"`
	Enabled        bool      `json:"enabled"`
	Active         bool      `json:"active,omitempty"`
	Provenance     string    `json:"provenance,omitempty"`
	AllowedTools   []string  `json:"allowed_tools,omitempty"`
	AllowedDomains []string  `json:"allowed_domains,omitempty"`
	SandboxProfile string    `json:"sandbox_profile,omitempty"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

type SkillRegistry struct {
	Root      string
	IndexPath string
}

type MigrationReport struct {
	TotalRecords   int  `json:"total_records"`
	LegacyRecords  int  `json:"legacy_records"`
	UpdatedRecords int  `json:"updated_records"`
	Applied        bool `json:"applied"`
}

func NewSkillRegistry(root string) *SkillRegistry {
	root = strings.TrimSpace(root)
	if root == "" {
		root = PermanentSkillsRoot()
	}
	return &SkillRegistry{
		Root:      root,
		IndexPath: filepath.Join(root, defaultRegistryFile),
	}
}

// MigrateLegacy upgrades legacy records to V3 fields. When apply=false, this is a dry-run report.
func (r *SkillRegistry) MigrateLegacy(apply bool) (MigrationReport, error) {
	if r == nil {
		r = NewSkillRegistry("")
	}
	raw, err := os.ReadFile(r.IndexPath)
	if err != nil {
		if os.IsNotExist(err) {
			return MigrationReport{Applied: apply}, nil
		}
		return MigrationReport{}, err
	}
	type wrappedRaw struct {
		Skills []map[string]any `json:"skills"`
	}
	var wr wrappedRaw
	var list []map[string]any
	if err := json.Unmarshal(raw, &wr); err == nil && len(wr.Skills) > 0 {
		list = wr.Skills
	} else {
		var flat []map[string]any
		if err := json.Unmarshal(raw, &flat); err != nil {
			return MigrationReport{}, os.ErrInvalid
		}
		list = flat
	}
	report := MigrationReport{TotalRecords: len(list), Applied: apply}
	recs := make([]SkillRecord, 0, len(list))
	for _, m := range list {
		b, _ := json.Marshal(m)
		var rec SkillRecord
		if err := json.Unmarshal(b, &rec); err != nil {
			continue
		}
		if isLegacySkillRecordMap(m, rec) {
			report.LegacyRecords++
		}
		recs = append(recs, rec)
	}
	migrated := migrateLegacyRecords(recs)
	for i := range recs {
		if i >= len(migrated) {
			break
		}
		if recs[i].RevisionID != migrated[i].RevisionID ||
			recs[i].Version != migrated[i].Version ||
			recs[i].Status != migrated[i].Status ||
			recs[i].Active != migrated[i].Active ||
			recs[i].SandboxProfile != migrated[i].SandboxProfile {
			report.UpdatedRecords++
		}
	}
	if apply {
		if err := r.writeAll(migrated); err != nil {
			return report, err
		}
	}
	return report, nil
}

func (r *SkillRegistry) Upsert(record SkillRecord) error {
	if r == nil {
		r = NewSkillRegistry("")
	}
	record.SkillID = strings.TrimSpace(record.SkillID)
	record.RevisionID = strings.TrimSpace(record.RevisionID)
	record.Version = strings.TrimSpace(record.Version)
	record.Status = strings.TrimSpace(record.Status)
	record.Name = strings.TrimSpace(record.Name)
	record.Intent = strings.TrimSpace(record.Intent)
	record.TaskType = strings.TrimSpace(record.TaskType)
	record.RootDir = strings.TrimSpace(record.RootDir)
	record.SourcePath = strings.TrimSpace(record.SourcePath)
	record.ManifestPath = strings.TrimSpace(record.ManifestPath)
	record.PackageName = strings.TrimSpace(record.PackageName)
	record.SandboxProfile = strings.TrimSpace(record.SandboxProfile)
	if record.SkillID == "" || record.RootDir == "" || record.SourcePath == "" || record.ManifestPath == "" {
		return os.ErrInvalid
	}
	if record.Version == "" {
		record.Version = "0.1.0"
	}
	if record.RevisionID == "" {
		record.RevisionID = revisionIDFrom(record.SkillID, record.Version, record.SourcePath)
	}
	if record.Status == "" {
		record.Status = SkillStatusDraft
	}
	if record.SandboxProfile == "" {
		record.SandboxProfile = "skill_default"
	}
	now := time.Now().UTC()
	if record.CreatedAt.IsZero() {
		record.CreatedAt = now
	}
	record.UpdatedAt = now

	all, err := r.loadAll()
	if err != nil {
		return err
	}
	found := false
	for i := range all {
		if strings.TrimSpace(all[i].SkillID) == record.SkillID && strings.TrimSpace(all[i].RevisionID) == record.RevisionID {
			record.CreatedAt = firstNonZeroTime(all[i].CreatedAt, record.CreatedAt)
			all[i] = record
			found = true
			break
		}
	}
	if !found {
		all = append(all, record)
	}
	return r.writeAll(all)
}

func (r *SkillRegistry) ListEnabled() ([]SkillRecord, error) {
	all, err := r.loadAll()
	if err != nil {
		return nil, err
	}
	out := make([]SkillRecord, 0, len(all))
	for _, rec := range all {
		if rec.Enabled && !strings.EqualFold(strings.TrimSpace(rec.Status), SkillStatusRevoked) {
			out = append(out, rec)
		}
	}
	return out, nil
}

// ListRevisions returns all revisions for a logical skill ID ordered by UpdatedAt desc.
func (r *SkillRegistry) ListRevisions(skillID string) ([]SkillRecord, error) {
	all, err := r.loadAll()
	if err != nil {
		return nil, err
	}
	skillID = strings.TrimSpace(skillID)
	if skillID == "" {
		return nil, nil
	}
	out := make([]SkillRecord, 0, 4)
	for _, rec := range all {
		if strings.EqualFold(strings.TrimSpace(rec.SkillID), skillID) {
			out = append(out, rec)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].UpdatedAt.After(out[j].UpdatedAt)
	})
	return out, nil
}

// ResolveActive returns the active revision for a logical skill ID.
func (r *SkillRegistry) ResolveActive(skillID string) (*SkillRecord, bool, error) {
	revs, err := r.ListRevisions(skillID)
	if err != nil {
		return nil, false, err
	}
	for i := range revs {
		if revs[i].Active && revs[i].Enabled && strings.EqualFold(strings.TrimSpace(revs[i].Status), SkillStatusActive) {
			out := revs[i]
			return &out, true, nil
		}
	}
	if len(revs) == 0 {
		return nil, false, nil
	}
	out := revs[0]
	return &out, true, nil
}

// ActivateRevision sets one revision active for a skill and marks it active.
func (r *SkillRegistry) ActivateRevision(skillID, revisionID string) error {
	all, err := r.loadAll()
	if err != nil {
		return err
	}
	skillID = strings.TrimSpace(skillID)
	revisionID = strings.TrimSpace(revisionID)
	if skillID == "" {
		return os.ErrInvalid
	}
	found := false
	for i := range all {
		if !strings.EqualFold(strings.TrimSpace(all[i].SkillID), skillID) {
			continue
		}
		if revisionID != "" && strings.EqualFold(strings.TrimSpace(all[i].RevisionID), revisionID) {
			all[i].Active = true
			all[i].Enabled = true
			all[i].Status = SkillStatusActive
			all[i].UpdatedAt = time.Now().UTC()
			found = true
			continue
		}
		if revisionID == "" && !found {
			all[i].Active = true
			all[i].Enabled = true
			all[i].Status = SkillStatusActive
			all[i].UpdatedAt = time.Now().UTC()
			found = true
			continue
		}
		all[i].Active = false
	}
	if !found {
		return os.ErrNotExist
	}
	return r.writeAll(all)
}

func (r *SkillRegistry) DeprecateRevision(skillID, revisionID string) error {
	all, err := r.loadAll()
	if err != nil {
		return err
	}
	skillID = strings.TrimSpace(skillID)
	revisionID = strings.TrimSpace(revisionID)
	if skillID == "" {
		return os.ErrInvalid
	}
	found := false
	for i := range all {
		if !strings.EqualFold(strings.TrimSpace(all[i].SkillID), skillID) {
			continue
		}
		if revisionID == "" || strings.EqualFold(strings.TrimSpace(all[i].RevisionID), revisionID) {
			all[i].Status = SkillStatusDeprecated
			all[i].Active = false
			all[i].UpdatedAt = time.Now().UTC()
			found = true
		}
	}
	if !found {
		return os.ErrNotExist
	}
	return r.writeAll(all)
}

func (r *SkillRegistry) FindMatch(name, intent, taskType string) (*SkillRecord, bool, error) {
	all, err := r.loadAll()
	if err != nil {
		return nil, false, err
	}
	name = strings.ToLower(strings.TrimSpace(name))
	intent = strings.ToLower(strings.TrimSpace(intent))
	taskType = strings.ToLower(strings.TrimSpace(taskType))
	bestIdx := -1
	bestScore := -1

	for i, rec := range all {
		if !rec.Enabled || strings.EqualFold(strings.TrimSpace(rec.Status), SkillStatusRevoked) {
			continue
		}
		score := 0
		recName := strings.ToLower(strings.TrimSpace(rec.Name))
		recIntent := strings.ToLower(strings.TrimSpace(rec.Intent))
		recTask := strings.ToLower(strings.TrimSpace(rec.TaskType))
		if name != "" && recName == name {
			score += 8
		}
		if taskType != "" && recTask != "" && taskType == recTask {
			score += 2
		}
		score += tokenOverlapScore(intent, recIntent)
		if score > bestScore {
			bestScore = score
			bestIdx = i
		}
	}
	if bestIdx < 0 || bestScore < 3 {
		return nil, false, nil
	}
	matched := all[bestIdx]
	return &matched, true, nil
}

func (r *SkillRegistry) loadAll() ([]SkillRecord, error) {
	if r == nil {
		r = NewSkillRegistry("")
	}
	raw, err := os.ReadFile(r.IndexPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var wrapped struct {
		Skills []SkillRecord `json:"skills"`
	}
	if err := json.Unmarshal(raw, &wrapped); err == nil {
		return migrateLegacyRecords(wrapped.Skills), nil
	}
	var out []SkillRecord
	if err := json.Unmarshal(raw, &out); err == nil {
		return migrateLegacyRecords(out), nil
	}
	return nil, os.ErrInvalid
}

func (r *SkillRegistry) writeAll(records []SkillRecord) error {
	if r == nil {
		r = NewSkillRegistry("")
	}
	if err := os.MkdirAll(filepath.Dir(r.IndexPath), 0o755); err != nil {
		return err
	}
	payload := struct {
		Skills []SkillRecord `json:"skills"`
	}{
		Skills: records,
	}
	b, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(r.IndexPath, b, 0o644)
}

func SkillRecordFromArtifact(req UserSkillCreateRequest, art JITSkillArtifact, enabled bool) SkillRecord {
	return SkillRecord{
		SkillID:        strings.TrimSpace(art.SkillID),
		RevisionID:     strings.TrimSpace(art.RevisionID),
		Version:        firstNonZeroString(strings.TrimSpace(art.Version), "0.1.0"),
		Status:         firstNonZeroString(strings.TrimSpace(art.Status), SkillStatusDraft),
		Name:           strings.TrimSpace(req.Name),
		Intent:         strings.TrimSpace(req.Intent),
		Description:    strings.TrimSpace(req.Description),
		Namespace:      strings.ToLower(strings.TrimSpace(req.Namespace)),
		ReasoningTier:  strings.TrimSpace(req.ReasoningTier),
		TaskType:       strings.TrimSpace(req.TaskType),
		RootDir:        strings.TrimSpace(art.RootDir),
		SourcePath:     strings.TrimSpace(art.SourcePath),
		ManifestPath:   strings.TrimSpace(art.ManifestPath),
		PackageName:    strings.TrimSpace(art.PackageName),
		CompileOK:      art.CompileOK,
		Enabled:        enabled,
		Active:         false,
		Provenance:     "user_cli",
		AllowedTools:   requestToolNames(req.RequestedTools),
		AllowedDomains: append([]string(nil), req.RequestedDomains...),
		SandboxProfile: "skill_default",
	}
}

func ArtifactFromSkillRecord(rec SkillRecord) JITSkillArtifact {
	return JITSkillArtifact{
		SkillID:      strings.TrimSpace(rec.SkillID),
		RevisionID:   strings.TrimSpace(rec.RevisionID),
		Version:      strings.TrimSpace(rec.Version),
		Status:       strings.TrimSpace(rec.Status),
		RootDir:      strings.TrimSpace(rec.RootDir),
		SourcePath:   strings.TrimSpace(rec.SourcePath),
		ManifestPath: strings.TrimSpace(rec.ManifestPath),
		PackageName:  strings.TrimSpace(rec.PackageName),
		CompileOK:    rec.CompileOK,
	}
}

func migrateLegacyRecords(in []SkillRecord) []SkillRecord {
	out := make([]SkillRecord, 0, len(in))
	for _, rec := range in {
		if strings.TrimSpace(rec.Version) == "" {
			rec.Version = "0.1.0"
		}
		if strings.TrimSpace(rec.RevisionID) == "" {
			rec.RevisionID = revisionIDFrom(rec.SkillID, rec.Version, rec.SourcePath)
		}
		if strings.TrimSpace(rec.Status) == "" {
			if rec.Enabled {
				rec.Status = SkillStatusActive
				rec.Active = true
			} else {
				rec.Status = SkillStatusDraft
			}
		}
		if strings.TrimSpace(rec.SandboxProfile) == "" {
			rec.SandboxProfile = "skill_default"
		}
		out = append(out, rec)
	}
	return out
}

func isLegacySkillRecordMap(raw map[string]any, rec SkillRecord) bool {
	if raw == nil {
		return true
	}
	if _, ok := raw["revision_id"]; !ok {
		return true
	}
	if _, ok := raw["version"]; !ok {
		return true
	}
	if _, ok := raw["status"]; !ok {
		return true
	}
	if strings.TrimSpace(rec.RevisionID) == "" || strings.TrimSpace(rec.Version) == "" || strings.TrimSpace(rec.Status) == "" {
		return true
	}
	return false
}

func firstNonZeroString(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func requestToolNames(req []tools.SkillPreflightToolRequest) []string {
	out := make([]string, 0, len(req))
	seen := map[string]bool{}
	for _, t := range req {
		name := strings.TrimSpace(t.Name)
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		out = append(out, name)
	}
	return out
}

func tokenOverlapScore(a, b string) int {
	at := registryTokenSet(a)
	bt := registryTokenSet(b)
	if len(at) == 0 || len(bt) == 0 {
		return 0
	}
	score := 0
	for k := range at {
		if bt[k] {
			score++
		}
	}
	return score
}

func registryTokenSet(s string) map[string]bool {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.NewReplacer(",", " ", ".", " ", ";", " ", ":", " ", "/", " ", "_", " ", "-", " ").Replace(s)
	parts := strings.Fields(s)
	out := map[string]bool{}
	for _, p := range parts {
		if len(p) < 3 {
			continue
		}
		out[p] = true
	}
	return out
}

func firstNonZeroTime(values ...time.Time) time.Time {
	for _, v := range values {
		if !v.IsZero() {
			return v
		}
	}
	return time.Time{}
}
