package forge

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/Thynaptic/P-LMv1/pkg/tools"
)

const (
	defaultLabResultsPath = ".memory/lab_assistant/results.jsonl"
	defaultBackupDir      = ".memory/backups"
)

// HotSwapResult captures the applied build and rollback location.
type HotSwapResult struct {
	BuildID      string    `json:"build_id"`
	ShadowPath   string    `json:"shadow_path"`
	LivePath     string    `json:"live_path"`
	Affected     []string  `json:"affected"`
	BackupPath   string    `json:"backup_path"`
	AppliedAt    time.Time `json:"applied_at"`
	AppliedBy    string    `json:"applied_by"`
	UsedPatch    bool      `json:"used_patch"`
	ResultsPath  string    `json:"results_path"`
	VerifiedLine string    `json:"verified_line,omitempty"`
}

type labResultEntry struct {
	Timestamp  time.Time `json:"timestamp"`
	TaskID     string    `json:"task_id"`
	BuildID    string    `json:"build_id,omitempty"`
	SourcePath string    `json:"source_path,omitempty"`
	ShadowPath string    `json:"shadow_path,omitempty"`
	Success    bool      `json:"success"`
	Status     string    `json:"status,omitempty"`
}

// ApplyVerifiedFix applies a verified shadow fix into live source.
// Build identity is inferred from the shadow folder name unless LAB_BUILD_ID is set.
func ApplyVerifiedFix(shadowPath, livePath string) error {
	buildID := strings.TrimSpace(os.Getenv("LAB_BUILD_ID"))
	_, err := ApplyVerifiedFixWithBuildID(shadowPath, livePath, buildID)
	return err
}

// ApplyVerifiedFixWithBuildID applies a verified shadow fix into live source using an explicit build ID.
func ApplyVerifiedFixWithBuildID(shadowPath, livePath, buildID string) (HotSwapResult, error) {
	absShadow, absLive, err := normalizeRoots(shadowPath, livePath)
	if err != nil {
		return HotSwapResult{}, err
	}
	if strings.TrimSpace(buildID) == "" {
		buildID = filepath.Base(absShadow)
	}

	verifiedLine, err := verifySuccessfulBuild(defaultLabResultsPath, buildID, absShadow)
	if err != nil {
		return HotSwapResult{}, err
	}

	affected, err := collectAffectedFiles(absShadow, absLive)
	if err != nil {
		return HotSwapResult{}, err
	}
	if len(affected) == 0 {
		return HotSwapResult{}, fmt.Errorf("no changed files detected between shadow and live")
	}
	sort.Strings(affected)

	backupPath, err := PreSwapBackup(absLive, affected)
	if err != nil {
		return HotSwapResult{}, err
	}

	usedPatch := false
	if ok := hasShadowPatch(absShadow); ok {
		if patchErr := applyPatchToLive(absShadow, absLive); patchErr == nil {
			usedPatch = true
		}
	}
	if !usedPatch {
		if err := atomicReplaceFromShadow(absShadow, absLive, affected); err != nil {
			return HotSwapResult{}, err
		}
	}

	res := HotSwapResult{
		BuildID:      buildID,
		ShadowPath:   absShadow,
		LivePath:     absLive,
		Affected:     affected,
		BackupPath:   backupPath,
		AppliedAt:    time.Now().UTC(),
		AppliedBy:    "hotswap",
		UsedPatch:    usedPatch,
		ResultsPath:  defaultLabResultsPath,
		VerifiedLine: verifiedLine,
	}
	_ = tools.AppendReasoningMirrorLine(
		fmt.Sprintf("Verified fix from build #%s applied to live source. Rollback point created at %s.", buildID, backupPath),
	)
	return res, nil
}

// PreSwapBackup snapshots affected live files into a zip archive in .memory/backups/.
func PreSwapBackup(livePath string, affectedFiles []string) (string, error) {
	livePath = strings.TrimSpace(livePath)
	if livePath == "" {
		return "", fmt.Errorf("live path is required")
	}
	absLive, err := filepath.Abs(livePath)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(defaultBackupDir, 0o755); err != nil {
		return "", err
	}
	stamp := time.Now().UTC().Format("20060102T150405Z")
	zipPath := filepath.Join(defaultBackupDir, "hotswap_"+stamp+".zip")
	f, err := os.OpenFile(zipPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return "", err
	}
	defer f.Close()
	zw := zip.NewWriter(f)
	defer zw.Close()

	seen := map[string]struct{}{}
	for _, rel := range affectedFiles {
		rel = normalizeRel(rel)
		if rel == "" {
			continue
		}
		if _, ok := seen[rel]; ok {
			continue
		}
		seen[rel] = struct{}{}
		src := filepath.Join(absLive, rel)
		info, err := os.Stat(src)
		if err != nil || info.IsDir() {
			continue
		}
		h, err := zip.FileInfoHeader(info)
		if err != nil {
			return "", err
		}
		h.Name = filepath.ToSlash(rel)
		h.Method = zip.Deflate
		w, err := zw.CreateHeader(h)
		if err != nil {
			return "", err
		}
		in, err := os.Open(src)
		if err != nil {
			return "", err
		}
		_, cpErr := io.Copy(w, in)
		_ = in.Close()
		if cpErr != nil {
			return "", cpErr
		}
	}

	manifest, _ := json.MarshalIndent(map[string]interface{}{
		"created_at": time.Now().UTC(),
		"root":       absLive,
		"files":      keysFromMap(seen),
	}, "", "  ")
	mw, err := zw.Create("manifest.json")
	if err != nil {
		return "", err
	}
	if _, err := mw.Write(manifest); err != nil {
		return "", err
	}
	return zipPath, nil
}

func normalizeRoots(shadowPath, livePath string) (string, string, error) {
	shadowPath = strings.TrimSpace(shadowPath)
	livePath = strings.TrimSpace(livePath)
	if shadowPath == "" || livePath == "" {
		return "", "", fmt.Errorf("shadow path and live path are required")
	}
	absShadow, err := filepath.Abs(shadowPath)
	if err != nil {
		return "", "", err
	}
	absLive, err := filepath.Abs(livePath)
	if err != nil {
		return "", "", err
	}
	shadowInfo, err := os.Stat(absShadow)
	if err != nil || !shadowInfo.IsDir() {
		return "", "", fmt.Errorf("invalid shadow path: %s", absShadow)
	}
	liveInfo, err := os.Stat(absLive)
	if err != nil || !liveInfo.IsDir() {
		return "", "", fmt.Errorf("invalid live path: %s", absLive)
	}
	return absShadow, absLive, nil
}

func verifySuccessfulBuild(resultsPath, buildID, shadowPath string) (string, error) {
	raw, err := os.ReadFile(resultsPath)
	if err != nil {
		return "", fmt.Errorf("cannot read lab results (%s): %w", resultsPath, err)
	}
	lines := strings.Split(string(raw), "\n")
	var latest *labResultEntry
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var rec labResultEntry
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			continue
		}
		statusOK := rec.Success || strings.EqualFold(strings.TrimSpace(rec.Status), "success")
		if !statusOK {
			continue
		}
		matchesBuildID := strings.TrimSpace(buildID) != "" &&
			(strings.EqualFold(strings.TrimSpace(rec.TaskID), buildID) || strings.EqualFold(strings.TrimSpace(rec.BuildID), buildID))
		matchesShadow := shadowPath != "" && samePath(rec.ShadowPath, shadowPath)
		if !matchesBuildID && !matchesShadow {
			continue
		}
		if latest == nil || rec.Timestamp.After(latest.Timestamp) {
			tmp := rec
			latest = &tmp
		}
	}
	if latest == nil {
		if strings.TrimSpace(buildID) == "" {
			return "", fmt.Errorf("hotswap blocked: no SUCCESS record found in %s for shadow=%s", resultsPath, shadowPath)
		}
		return "", fmt.Errorf("hotswap blocked: no SUCCESS record found in %s for build_id=%s", resultsPath, buildID)
	}
	line := fmt.Sprintf("task_id=%s shadow=%s timestamp=%s", latest.TaskID, latest.ShadowPath, latest.Timestamp.UTC().Format(time.RFC3339))
	return line, nil
}

func collectAffectedFiles(shadowRoot, liveRoot string) ([]string, error) {
	var out []string
	err := filepath.WalkDir(shadowRoot, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(shadowRoot, path)
		if err != nil {
			return err
		}
		rel = normalizeRel(rel)
		if rel == "" || rel == "." {
			return nil
		}
		if shouldSkipHotSwap(rel) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			return nil
		}
		shadowBytes, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		liveBytes, err := os.ReadFile(filepath.Join(liveRoot, rel))
		if err != nil {
			if os.IsNotExist(err) {
				out = append(out, rel)
				return nil
			}
			return err
		}
		if fileDigest(shadowBytes) != fileDigest(liveBytes) {
			out = append(out, rel)
		}
		return nil
	})
	return out, err
}

func atomicReplaceFromShadow(shadowRoot, liveRoot string, affected []string) error {
	for _, rel := range affected {
		rel = normalizeRel(rel)
		if rel == "" {
			continue
		}
		src := filepath.Join(shadowRoot, rel)
		info, err := os.Stat(src)
		if err != nil {
			return err
		}
		if info.IsDir() {
			continue
		}
		data, err := os.ReadFile(src)
		if err != nil {
			return err
		}
		dst := filepath.Join(liveRoot, rel)
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		tmp := dst + ".hotswap.tmp"
		if err := os.WriteFile(tmp, data, info.Mode().Perm()); err != nil {
			return err
		}
		if err := os.Rename(tmp, dst); err != nil {
			return err
		}
	}
	return nil
}

func hasShadowPatch(shadowRoot string) bool {
	_, err := os.Stat(filepath.Join(shadowRoot, ".lab_assistant_patch.diff"))
	return err == nil
}

func applyPatchToLive(shadowRoot, liveRoot string) error {
	patchPath := filepath.Join(shadowRoot, ".lab_assistant_patch.diff")
	if _, err := os.Stat(patchPath); err != nil {
		return err
	}
	// Prefer git apply for repository patches.
	cmd := exec.Command("bash", "-lc", "git apply --whitespace=nowarn .lab_assistant_patch.diff")
	cmd.Dir = liveRoot
	cmd.Env = append(os.Environ())
	_ = copyFile(patchPath, filepath.Join(liveRoot, ".lab_assistant_patch.diff"), 0o644)
	out, err := cmd.CombinedOutput()
	_ = os.Remove(filepath.Join(liveRoot, ".lab_assistant_patch.diff"))
	if err == nil {
		return nil
	}
	// Fallback to POSIX patch.
	patchCmd := exec.Command("bash", "-lc", "patch -p0 < .lab_assistant_patch.diff")
	patchCmd.Dir = liveRoot
	_ = copyFile(patchPath, filepath.Join(liveRoot, ".lab_assistant_patch.diff"), 0o644)
	pOut, pErr := patchCmd.CombinedOutput()
	_ = os.Remove(filepath.Join(liveRoot, ".lab_assistant_patch.diff"))
	if pErr != nil {
		return fmt.Errorf("patch apply failed: git=%s | patch=%s", trimText(string(out)), trimText(string(pOut)))
	}
	return nil
}

func shouldSkipHotSwap(rel string) bool {
	p := normalizeRel(rel)
	if p == "" {
		return false
	}
	skip := []string{
		".git", ".memory", ".codelab", ".skills", ".lab_assistant_patch.diff",
	}
	for _, s := range skip {
		if p == s || strings.HasPrefix(p, s+"/") {
			return true
		}
	}
	return false
}

func fileDigest(b []byte) string {
	sum := sha256.Sum256(bytes.TrimSpace(b))
	return hex.EncodeToString(sum[:])
}

func normalizeRel(rel string) string {
	rel = filepath.ToSlash(strings.TrimSpace(rel))
	rel = strings.TrimPrefix(rel, "./")
	rel = strings.TrimPrefix(rel, "/")
	return rel
}

func samePath(a, b string) bool {
	a = strings.TrimSpace(a)
	b = strings.TrimSpace(b)
	if a == "" || b == "" {
		return false
	}
	aa, errA := filepath.Abs(a)
	bb, errB := filepath.Abs(b)
	if errA != nil || errB != nil {
		return strings.EqualFold(filepath.Clean(a), filepath.Clean(b))
	}
	return strings.EqualFold(filepath.Clean(aa), filepath.Clean(bb))
}

func trimText(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 220 {
		return s[:220] + "...(truncated)"
	}
	return s
}

func keysFromMap(m map[string]struct{}) []string {
	if len(m) == 0 {
		return nil
	}
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
