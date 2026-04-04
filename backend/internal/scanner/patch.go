package scanner

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// ApplyPatch is a toolflow.ToolHandler that safely replaces a vulnerable snippet in a local file.
// It wraps the operation in a new Git branch and commit to ensure safety and reviewability.
// Expects: "file_path", "target_snippet", "replacement_code" in args.
func ApplyPatch(ctx context.Context, args map[string]interface{}) (string, error) {
	filePath, ok := args["file_path"].(string)
	if !ok || filePath == "" {
		return "", fmt.Errorf("file_path argument missing or invalid")
	}

	targetSnippet, ok := args["target_snippet"].(string)
	if !ok || targetSnippet == "" {
		return "", fmt.Errorf("target_snippet argument missing or invalid")
	}

	replacementCode, ok := args["replacement_code"].(string)
	if !ok || replacementCode == "" {
		return "", fmt.Errorf("replacement_code argument missing or invalid")
	}

	// 1. Ensure file exists and we can access it
	contentBytes, err := os.ReadFile(filePath)
	if err != nil {
		return "", fmt.Errorf("failed to read file %s: %v", filePath, err)
	}

	contentStr := string(contentBytes)
	if !strings.Contains(contentStr, targetSnippet) {
		return "", fmt.Errorf("target_snippet not found identically in the file. Patch aborted for safety.")
	}

	// 2. Setup Git Operations
	absPath, err := filepath.Abs(filePath)
	if err != nil {
		return "", fmt.Errorf("failed to get absolute path: %v", err)
	}
	repoDir := filepath.Dir(absPath)

	// Ensure we are inside a git repository for this file
	verifyGitCmd := exec.CommandContext(ctx, "git", "rev-parse", "--is-inside-work-tree")
	verifyGitCmd.Dir = repoDir
	if err := verifyGitCmd.Run(); err != nil {
		return "", fmt.Errorf("file is not inside a git repository: %s", repoDir)
	}

	// Create unique branch name
	timestamp := time.Now().Format("20060102-150405")
	branchName := fmt.Sprintf("vuln-patch-%s", timestamp)

	// Checkout new branch
	checkoutCmd := exec.CommandContext(ctx, "git", "checkout", "-b", branchName)
	checkoutCmd.Dir = repoDir
	if out, err := checkoutCmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("failed to create git branch %s: %v (Output: %s)", branchName, err, string(out))
	}

	// 3. Apply the patch
	patchedContent := strings.Replace(contentStr, targetSnippet, replacementCode, 1)

	err = os.WriteFile(filePath, []byte(patchedContent), 0644)
	if err != nil {
		// Attempt revert if write fails
		_ = exec.CommandContext(ctx, "git", "checkout", "-").Run()
		return "", fmt.Errorf("failed to write patched file %s: %v", filePath, err)
	}

	// 4. Git Add
	addCmd := exec.CommandContext(ctx, "git", "add", filepath.Base(filePath))
	addCmd.Dir = repoDir
	if out, err := addCmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("failed to git add: %v (Output: %s)", err, string(out))
	}

	// 5. Git Commit
	commitMsg := fmt.Sprintf("Security Patch: Auto-remediated vulnerability in %s", filepath.Base(filePath))
	commitCmd := exec.CommandContext(ctx, "git", "commit", "-m", commitMsg)
	commitCmd.Dir = repoDir
	if out, err := commitCmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("failed to git commit: %v (Output: %s)", err, string(out))
	}

	return fmt.Sprintf("Successfully branch '%s' created, patched, and committed %s", branchName, filepath.Base(filePath)), nil
}
