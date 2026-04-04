package taloscli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/Thynaptic/P-LMv1/pkg/memory"
	"github.com/Thynaptic/P-LMv1/pkg/orchestration"
	"github.com/spf13/cobra"
)

const (
	pathBlockStart = "# >>> TALOS PATH >>>"
	pathBlockEnd   = "# <<< TALOS PATH <<<"
)

var (
	monitorBinDir    string
	monitorShell     string
	monitorNoProfile bool
)

var monitorCmd = &cobra.Command{
	Use:   "monitor",
	Short: "Terminal integration utilities for TALOS.",
	Example: `  talos monitor install
  talos monitor reboot
  talos monitor status
  talos monitor uninstall`,
}

var monitorInstallCmd = &cobra.Command{
	Use:   "install",
	Short: "Install TALOS to a PATH directory and wire shell profile.",
	RunE: func(cmd *cobra.Command, args []string) error {
		return runMonitorInstall(cmd.OutOrStdout(), false)
	},
}

var monitorRebootCmd = &cobra.Command{
	Use:     "reboot",
	Aliases: []string{"restart"},
	Short:   "Install TALOS and run profile source verification for bash/zsh.",
	RunE: func(cmd *cobra.Command, args []string) error {
		return runMonitorInstall(cmd.OutOrStdout(), true)
	},
}

func runMonitorInstall(w io.Writer, reboot bool) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("determine home directory: %w", err)
	}
	binDir, err := expandHome(monitorBinDir, home)
	if err != nil {
		return err
	}
	if strings.TrimSpace(binDir) == "" {
		binDir = filepath.Join(home, ".local", "bin")
	}
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		return fmt.Errorf("create bin directory %s: %w", binDir, err)
	}

	repoRoot, err := findRepoRoot()
	if err != nil {
		return err
	}

	target := filepath.Join(binDir, "talos")
	build := exec.Command("go", "build", "-o", target, "./cmd/talos")
	build.Dir = repoRoot
	build.Stdout = os.Stdout
	build.Stderr = os.Stderr
	if err := build.Run(); err != nil {
		return fmt.Errorf("build talos binary: %w", err)
	}

	updatedFiles := make([]string, 0, 2)
	rcFiles := make([]string, 0, 2)
	if !monitorNoProfile {
		rcFiles, err = shellProfileFiles(strings.TrimSpace(monitorShell), home)
		if err != nil {
			return err
		}
		for _, rc := range rcFiles {
			updated, err := ensurePathExportBlock(rc, binDir)
			if err != nil {
				return err
			}
			if updated {
				updatedFiles = append(updatedFiles, rc)
			}
		}
	}

	printCommandStatus(w, "talos monitor install", "success")
	printSection(w, "results")
	printKV(w, "installed_binary", target)
	if len(updatedFiles) > 0 {
		printSection(w, "updated_profiles")
		for _, f := range updatedFiles {
			fmt.Fprintf(w, "  - %s\n", f)
		}
	}

	if reboot && !monitorNoProfile && len(rcFiles) > 0 {
		sourceCmd := shellSourceCommand(strings.TrimSpace(monitorShell), rcFiles[0], home)
		if strings.TrimSpace(sourceCmd) == "" {
			sourceCmd = "source " + rcFiles[0]
		}
		if err := runShellSourceProbe(strings.TrimSpace(monitorShell), sourceCmd); err != nil {
			fmt.Fprintf(w, "Profile source verification: warning (%v)\n", err)
		} else {
			fmt.Fprintln(w, "Profile source verification: ok")
		}
		printSection(w, "next")
		printKV(w, "source_command", sourceCmd)
	} else {
		printSection(w, "next")
		printKV(w, "action", "Open a new terminal (or source your profile) and run: talos --help")
	}
	return nil
}

var monitorUninstallCmd = &cobra.Command{
	Use:   "uninstall",
	Short: "Remove TALOS terminal integration and installed binary.",
	RunE: func(cmd *cobra.Command, args []string) error {
		home, err := os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("determine home directory: %w", err)
		}
		binDir, err := expandHome(monitorBinDir, home)
		if err != nil {
			return err
		}
		if strings.TrimSpace(binDir) == "" {
			binDir = filepath.Join(home, ".local", "bin")
		}

		target := filepath.Join(binDir, "talos")
		removedBinary := false
		if err := os.Remove(target); err == nil {
			removedBinary = true
		} else if err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("remove talos binary %s: %w", target, err)
		}

		updatedFiles := make([]string, 0, 2)
		if !monitorNoProfile {
			rcFiles, err := shellProfileFiles(strings.TrimSpace(monitorShell), home)
			if err != nil {
				return err
			}
			for _, rc := range rcFiles {
				updated, err := removePathExportBlock(rc)
				if err != nil {
					return err
				}
				if updated {
					updatedFiles = append(updatedFiles, rc)
				}
			}
		}

		printCommandStatus(cmd.OutOrStdout(), "talos monitor uninstall", "success")
		printSection(cmd.OutOrStdout(), "results")
		if removedBinary {
			printKV(cmd.OutOrStdout(), "removed_binary", target)
		} else {
			printKV(cmd.OutOrStdout(), "removed_binary", "none")
		}
		if len(updatedFiles) > 0 {
			printSection(cmd.OutOrStdout(), "updated_profiles")
			for _, f := range updatedFiles {
				fmt.Fprintf(cmd.OutOrStdout(), "  - %s\n", f)
			}
		}
		printSection(cmd.OutOrStdout(), "next")
		printKV(cmd.OutOrStdout(), "action", "Open a new terminal (or source your profile) to apply PATH changes.")
		return nil
	},
}

var monitorStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show terminal integration status for TALOS.",
	RunE: func(cmd *cobra.Command, args []string) error {
		path, err := exec.LookPath("talos")
		pathFound := err == nil
		worldview, worldviewFound, worldviewErr := orchestration.LoadCurrentWorldviewTruth()
		lastShift, lastShiftAt, shiftFound, shiftErr := orchestration.LoadLatestWorldviewTruthShift()
		report := renderMonitorStatusReport(
			pathFound,
			path,
			strings.TrimSpace(os.Getenv("SHELL")),
			os.Getenv("PATH"),
			memory.EffectiveMARPolicy(),
			worldview,
			worldviewFound,
			worldviewErr,
			lastShift,
			lastShiftAt,
			shiftFound,
			shiftErr,
		)
		fmt.Fprint(cmd.OutOrStdout(), report)
		return nil
	},
}

func renderMonitorStatusReport(pathFound bool, resolvedPath string, activeShell string, pathValue string, mar memory.MARPolicy, worldview orchestration.WorldviewTruthState, worldviewFound bool, worldviewErr error, lastShift orchestration.WorldviewTruthShift, lastShiftAt time.Time, shiftFound bool, shiftErr error) string {
	var b strings.Builder
	b.WriteString("MONITOR STATUS\n\nCOMMAND\n  talos monitor status\n\nSTATUS\n  SUCCESS\n\nRESULTS\n")
	if pathFound {
		b.WriteString("  talos_in_path: yes\n")
		b.WriteString(fmt.Sprintf("  resolved_binary: %s\n", strings.TrimSpace(resolvedPath)))
	} else {
		b.WriteString("  talos_in_path: no\n")
	}
	b.WriteString(fmt.Sprintf("  active_shell: %s\n", strings.TrimSpace(activeShell)))
	b.WriteString(fmt.Sprintf("  path_contains_local_bin: %t\n", pathContainsLocalBin(pathValue)))
	b.WriteString("MAR HEALTH\n")
	b.WriteString(fmt.Sprintf("  enabled: %t\n", mar.Enabled))
	b.WriteString(fmt.Sprintf("  candidate_limit: %d | max_anchors: %d | min_score: %.2f\n", mar.CandidateLimit, mar.MaxAnchors, mar.MinAnchorScore))
	b.WriteString(fmt.Sprintf("  weights(s,l,i,f,t): %.2f,%.2f,%.2f,%.2f,%.2f\n", mar.WeightSemantic, mar.WeightLexical, mar.WeightImportance, mar.WeightFreshness, mar.WeightTopology))
	b.WriteString(fmt.Sprintf("  topology_boost: %.2f | status_report: %t\n", mar.TopologyBoost, mar.StatusReport))
	b.WriteString("WORLDVIEW TRUTH\n")
	if worldviewErr != nil {
		b.WriteString(fmt.Sprintf("  state: error (%v)\n", worldviewErr))
	} else if worldviewFound {
		b.WriteString(fmt.Sprintf("  hash: %s\n", strings.TrimSpace(worldview.TruthHash)))
		b.WriteString(fmt.Sprintf("  conflict_index: %.2f\n", worldview.ConflictIndex))
		if worldview.UpdatedAt.IsZero() {
			b.WriteString("  updated_at: n/a\n")
		} else {
			b.WriteString(fmt.Sprintf("  updated_at: %s\n", worldview.UpdatedAt.UTC().Format(time.RFC3339)))
		}
	} else {
		b.WriteString("  state: none\n")
	}
	if shiftErr != nil {
		b.WriteString(fmt.Sprintf("  last_shift: error (%v)\n", shiftErr))
	} else if shiftFound {
		b.WriteString(fmt.Sprintf("  last_shift: detected=%t severity=%.2f\n", lastShift.Detected, lastShift.Severity))
		if !lastShiftAt.IsZero() {
			b.WriteString(fmt.Sprintf("  last_shift_at: %s\n", lastShiftAt.UTC().Format(time.RFC3339)))
		}
		if strings.TrimSpace(lastShift.Reason) != "" {
			b.WriteString(fmt.Sprintf("  last_shift_reason: %s\n", strings.TrimSpace(lastShift.Reason)))
		}
	} else {
		b.WriteString("  last_shift: none\n")
	}
	return b.String()
}

func findRepoRoot() (string, error) {
	wd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("determine working directory: %w", err)
	}
	dir := wd
	for {
		if fileExists(filepath.Join(dir, "go.mod")) && fileExists(filepath.Join(dir, "cmd", "talos", "main.go")) {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", errors.New("could not find TALOS repository root; run this from inside the repository")
		}
		dir = parent
	}
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func expandHome(path string, home string) (string, error) {
	p := strings.TrimSpace(path)
	if p == "" {
		return "", nil
	}
	if p == "~" {
		return home, nil
	}
	if strings.HasPrefix(p, "~/") {
		return filepath.Join(home, p[2:]), nil
	}
	return p, nil
}

func shellProfileFiles(shell string, home string) ([]string, error) {
	sh := shell
	if sh == "" || sh == "auto" {
		sh = strings.ToLower(strings.TrimSpace(filepath.Base(os.Getenv("SHELL"))))
	}
	switch sh {
	case "bash":
		return []string{filepath.Join(home, ".bashrc")}, nil
	case "zsh":
		return []string{filepath.Join(home, ".zshrc")}, nil
	default:
		return nil, fmt.Errorf("unsupported shell %q (supported: auto, bash, zsh)", sh)
	}
}

func shellSourceCommand(shell string, rcFile string, home string) string {
	rc := strings.TrimSpace(rcFile)
	if rc == "" {
		return ""
	}
	prettyRC := rc
	home = strings.TrimSpace(home)
	if home != "" && strings.HasPrefix(rc, home+"/") {
		prettyRC = "~/" + strings.TrimPrefix(rc, home+"/")
	}

	sh := strings.TrimSpace(shell)
	if sh == "" || sh == "auto" {
		sh = strings.ToLower(strings.TrimSpace(filepath.Base(os.Getenv("SHELL"))))
	}
	switch sh {
	case "bash", "zsh":
		return "source " + prettyRC
	default:
		return "source " + prettyRC
	}
}

func runShellSourceProbe(shell string, sourceCmd string) error {
	sh := strings.TrimSpace(shell)
	if sh == "" || sh == "auto" {
		sh = strings.ToLower(strings.TrimSpace(filepath.Base(os.Getenv("SHELL"))))
	}
	if sh != "bash" && sh != "zsh" {
		return fmt.Errorf("unsupported shell %q for source probe", sh)
	}

	check := sourceCmd + " >/dev/null 2>&1 && command -v talos >/dev/null 2>&1"
	cmd := exec.Command(sh, "-lc", check)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("probe command failed for %s", sh)
	}
	return nil
}

func ensurePathExportBlock(rcFile string, binDir string) (bool, error) {
	block := fmt.Sprintf("%s\nexport PATH=\"%s:$PATH\"\n%s\n", pathBlockStart, binDir, pathBlockEnd)

	content, err := os.ReadFile(rcFile)
	if err != nil && !os.IsNotExist(err) {
		return false, fmt.Errorf("read profile %s: %w", rcFile, err)
	}
	text := string(content)
	if strings.Contains(text, pathBlockStart) && strings.Contains(text, pathBlockEnd) {
		return false, nil
	}

	var out strings.Builder
	if strings.TrimSpace(text) != "" {
		out.WriteString(strings.TrimRight(text, "\n"))
		out.WriteString("\n\n")
	}
	out.WriteString(block)
	if err := os.WriteFile(rcFile, []byte(out.String()), 0o644); err != nil {
		return false, fmt.Errorf("write profile %s: %w", rcFile, err)
	}
	return true, nil
}

func removePathExportBlock(rcFile string) (bool, error) {
	content, err := os.ReadFile(rcFile)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, fmt.Errorf("read profile %s: %w", rcFile, err)
	}
	text := string(content)
	start := strings.Index(text, pathBlockStart)
	end := strings.Index(text, pathBlockEnd)
	if start == -1 || end == -1 || end < start {
		return false, nil
	}
	end += len(pathBlockEnd)

	newText := text[:start] + text[end:]
	newText = strings.TrimLeft(newText, "\n")
	newText = strings.TrimRight(newText, " \t\n") + "\n"

	if err := os.WriteFile(rcFile, []byte(newText), 0o644); err != nil {
		return false, fmt.Errorf("write profile %s: %w", rcFile, err)
	}
	return true, nil
}

func pathContainsLocalBin(pathValue string) bool {
	for _, part := range strings.Split(pathValue, ":") {
		if strings.HasSuffix(strings.TrimSpace(part), "/.local/bin") {
			return true
		}
	}
	return false
}

func init() {
	monitorInstallCmd.Flags().StringVar(&monitorBinDir, "bin-dir", "~/.local/bin", "directory where talos binary is installed")
	monitorInstallCmd.Flags().StringVar(&monitorShell, "shell", "auto", "shell profile to update (auto|bash|zsh)")
	monitorInstallCmd.Flags().BoolVar(&monitorNoProfile, "no-profile", false, "skip shell profile updates")
	monitorRebootCmd.Flags().StringVar(&monitorBinDir, "bin-dir", "~/.local/bin", "directory where talos binary is installed")
	monitorRebootCmd.Flags().StringVar(&monitorShell, "shell", "auto", "shell profile to update (auto|bash|zsh)")
	monitorRebootCmd.Flags().BoolVar(&monitorNoProfile, "no-profile", false, "skip shell profile updates")
	monitorUninstallCmd.Flags().StringVar(&monitorBinDir, "bin-dir", "~/.local/bin", "directory where talos binary is installed")
	monitorUninstallCmd.Flags().StringVar(&monitorShell, "shell", "auto", "shell profile to update (auto|bash|zsh)")
	monitorUninstallCmd.Flags().BoolVar(&monitorNoProfile, "no-profile", false, "skip shell profile updates")
	monitorCmd.AddCommand(monitorInstallCmd)
	monitorCmd.AddCommand(monitorRebootCmd)
	monitorCmd.AddCommand(monitorUninstallCmd)
	monitorCmd.AddCommand(monitorStatusCmd)
	rootCmd.AddCommand(monitorCmd)
}
