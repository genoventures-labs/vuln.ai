package forge

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const (
	defaultShadowRoot = "/tmp/thynaptic-forge"
)

// CloneToShadow hot-clones the source project into /tmp/thynaptic-forge/<project>.
// It prefers rsync for speed and fidelity, and falls back to native Go copying.
func CloneToShadow(srcPath string) (string, error) {
	srcPath = strings.TrimSpace(srcPath)
	if srcPath == "" {
		srcPath = "."
	}
	absSrc, err := filepath.Abs(srcPath)
	if err != nil {
		return "", fmt.Errorf("resolve source path: %w", err)
	}
	st, err := os.Stat(absSrc)
	if err != nil {
		return "", fmt.Errorf("stat source path: %w", err)
	}
	if !st.IsDir() {
		return "", fmt.Errorf("source path must be a directory: %s", absSrc)
	}

	project := sanitizeShadowName(filepath.Base(absSrc))
	if project == "" {
		project = fmt.Sprintf("project_%d", time.Now().Unix())
	}
	shadowPath := filepath.Join(defaultShadowRoot, project)

	if err := os.MkdirAll(defaultShadowRoot, 0o755); err != nil {
		return "", fmt.Errorf("create shadow root: %w", err)
	}
	_ = os.RemoveAll(shadowPath)
	if err := os.MkdirAll(shadowPath, 0o755); err != nil {
		return "", fmt.Errorf("create shadow workspace: %w", err)
	}

	if err := cloneWithRsync(absSrc, shadowPath); err != nil {
		// Fallback if rsync is unavailable.
		if !strings.Contains(strings.ToLower(err.Error()), "executable file not found") {
			// Keep going to fallback anyway; native copy can still recover.
		}
		if copyErr := cloneWithNative(absSrc, shadowPath); copyErr != nil {
			return "", fmt.Errorf("shadow clone failed (rsync: %v, native: %w)", err, copyErr)
		}
	}

	if err := ensureDependencyArtifacts(absSrc, shadowPath); err != nil {
		return "", err
	}
	return shadowPath, nil
}

// WipeShadowWorkspace removes the entire shadow root after a hot-swap approval/rejection.
func WipeShadowWorkspace() error {
	return os.RemoveAll(defaultShadowRoot)
}

// CleanShadowWorkspace removes a specific shadow workspace path.
func CleanShadowWorkspace(shadowPath string) error {
	shadowPath = strings.TrimSpace(shadowPath)
	if shadowPath == "" {
		return WipeShadowWorkspace()
	}
	abs, err := filepath.Abs(shadowPath)
	if err != nil {
		return err
	}
	if !strings.HasPrefix(abs, defaultShadowRoot) {
		return fmt.Errorf("refusing to wipe non-shadow path: %s", abs)
	}
	return os.RemoveAll(abs)
}

func cloneWithRsync(src, dst string) error {
	if _, err := exec.LookPath("rsync"); err != nil {
		return err
	}
	args := []string{
		"-a",
		"--delete",
		"--exclude", ".git/",
		"--exclude", ".memory/",
		"--exclude", ".codelab/",
		src + "/",
		dst + "/",
	}
	cmd := exec.Command("rsync", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("rsync failed: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func cloneWithNative(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		if shouldSkipClonePath(rel) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		target := filepath.Join(dst, rel)
		info, err := d.Info()
		if err != nil {
			return err
		}
		if d.IsDir() {
			return os.MkdirAll(target, info.Mode().Perm())
		}
		return copyFile(path, target, info.Mode().Perm())
	})
}

func ensureDependencyArtifacts(src, dst string) error {
	critical := []string{
		"go.mod",
		"go.sum",
		"vendor",
		"package.json",
		"package-lock.json",
		"pnpm-lock.yaml",
		"yarn.lock",
		"node_modules",
	}
	for _, rel := range critical {
		srcP := filepath.Join(src, rel)
		dstP := filepath.Join(dst, rel)
		srcInfo, err := os.Stat(srcP)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return err
		}
		if _, err := os.Stat(dstP); err == nil {
			continue
		}
		if srcInfo.IsDir() {
			if err := copyDir(srcP, dstP); err != nil {
				return fmt.Errorf("copy dependency dir %s: %w", rel, err)
			}
			continue
		}
		if err := copyFile(srcP, dstP, srcInfo.Mode().Perm()); err != nil {
			return fmt.Errorf("copy dependency file %s: %w", rel, err)
		}
	}
	// If source has go.mod, shadow must have go.mod.
	if _, err := os.Stat(filepath.Join(src, "go.mod")); err == nil {
		if _, derr := os.Stat(filepath.Join(dst, "go.mod")); derr != nil {
			return fmt.Errorf("shadow workspace missing go.mod after clone")
		}
	}
	return nil
}

func copyDir(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		info, err := d.Info()
		if err != nil {
			return err
		}
		if d.IsDir() {
			return os.MkdirAll(target, info.Mode().Perm())
		}
		return copyFile(path, target, info.Mode().Perm())
	})
}

func copyFile(src, dst string, perm os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, perm)
	if err != nil {
		return err
	}
	defer out.Close()
	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return nil
}

func shouldSkipClonePath(rel string) bool {
	rel = filepath.ToSlash(strings.ToLower(strings.TrimSpace(rel)))
	if rel == "" {
		return false
	}
	for _, p := range []string{
		".git", ".memory", ".codelab",
	} {
		if rel == p || strings.HasPrefix(rel, p+"/") {
			return true
		}
	}
	return false
}

func sanitizeShadowName(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" {
		return ""
	}
	var b strings.Builder
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' || r == '-' || r == '.' {
			b.WriteRune(r)
		}
	}
	return strings.Trim(b.String(), "._-")
}
