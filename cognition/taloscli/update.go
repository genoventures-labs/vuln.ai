package taloscli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

const (
	updateModulePath       = "github.com/cassianwolfe/P-LMv1"
	updateCmdPath          = "github.com/cassianwolfe/P-LMv1/cmd/talos"
	defaultAutoUpdateHours = 24
)

type updateConfig struct {
	Enabled       bool      `json:"enabled"`
	AutoApply     bool      `json:"auto_apply"`
	IntervalHours int       `json:"interval_hours"`
	LastCheckedAt time.Time `json:"last_checked_at"`
	LastSeen      string    `json:"last_seen,omitempty"`
}

var (
	updateCmd = &cobra.Command{
		Use:   "update",
		Short: "Check for updates and self-update TALOS.",
	}
	updateCheckCmd = &cobra.Command{
		Use:   "check",
		Short: "Check latest available TALOS version.",
		Run: func(cmd *cobra.Command, args []string) {
			if err := runUpdateCheck(true); err != nil {
				printCommandStatus(cmd.OutOrStdout(), "talos update check", "error")
				printSection(cmd.OutOrStdout(), "error")
				printKV(cmd.OutOrStdout(), "message", err.Error())
			}
		},
	}
	updateApplyCmd = &cobra.Command{
		Use:   "apply",
		Short: "Install latest TALOS binary using Go modules.",
		Run: func(cmd *cobra.Command, args []string) {
			if err := runUpdateApply(); err != nil {
				printCommandStatus(cmd.OutOrStdout(), "talos update apply", "error")
				printSection(cmd.OutOrStdout(), "error")
				printKV(cmd.OutOrStdout(), "message", err.Error())
			}
		},
	}
	updateAutoCmd = &cobra.Command{
		Use:   "auto",
		Short: "Configure background auto-update checks.",
		Run: func(cmd *cobra.Command, args []string) {
			if err := runUpdateAutoConfig(); err != nil {
				printCommandStatus(cmd.OutOrStdout(), "talos update auto", "error")
				printSection(cmd.OutOrStdout(), "error")
				printKV(cmd.OutOrStdout(), "message", err.Error())
			}
		},
	}
	updateAutoEnable   bool
	updateAutoDisable  bool
	updateAutoApply    bool
	updateAutoNoApply  bool
	updateAutoInterval time.Duration
)

func init() {
	updateCmd.AddCommand(updateCheckCmd)
	updateCmd.AddCommand(updateApplyCmd)
	updateCmd.AddCommand(updateAutoCmd)

	updateAutoCmd.Flags().BoolVar(&updateAutoEnable, "enable", false, "Enable automatic update checks")
	updateAutoCmd.Flags().BoolVar(&updateAutoDisable, "disable", false, "Disable automatic update checks")
	updateAutoCmd.Flags().BoolVar(&updateAutoApply, "apply", false, "Enable automatic update apply when a newer version is found")
	updateAutoCmd.Flags().BoolVar(&updateAutoNoApply, "no-apply", false, "Disable automatic update apply")
	updateAutoCmd.Flags().DurationVar(&updateAutoInterval, "interval", 0, "Auto-check interval (for example: 12h, 24h)")

	rootCmd.AddCommand(updateCmd)
}

func runUpdateCheck(verbose bool) error {
	latest, err := fetchLatestVersion(8 * time.Second)
	if err != nil {
		return err
	}
	current := currentTalosVersion()
	cmp := compareSemver(current, latest)
	if verbose {
		printCommandStatus(os.Stdout, "talos update check", "success")
		printSection(os.Stdout, "results")
		printKV(os.Stdout, "current", current)
		printKV(os.Stdout, "latest", latest)
	}
	if cmp < 0 {
		if verbose {
			printSection(os.Stdout, "next")
			printKV(os.Stdout, "action", fmt.Sprintf("Run: talos update apply (%s -> %s)", current, latest))
		}
		return nil
	}
	if cmp == 0 {
		if verbose {
			printSection(os.Stdout, "next")
			printKV(os.Stdout, "action", "TALOS is up to date.")
		}
		return nil
	}
	if verbose {
		printSection(os.Stdout, "next")
		printKV(os.Stdout, "action", "Current version is newer than latest module tag.")
	}
	return nil
}

func runUpdateApply() error {
	latest, err := fetchLatestVersion(8 * time.Second)
	if err != nil {
		return err
	}
	printCommandStatus(os.Stdout, "talos update apply", "running")
	printSection(os.Stdout, "config")
	printKV(os.Stdout, "target_version", latest)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	target := configuredUpdateCmdPath()
	out, err := exec.CommandContext(ctx, "go", "install", target+"@latest").CombinedOutput()
	if err == nil {
		binPath := resolveTalosBinPath()
		printCommandStatus(os.Stdout, "talos update apply", "success")
		printSection(os.Stdout, "results")
		printKV(os.Stdout, "binary_path", binPath)
		printSection(os.Stdout, "next")
		printKV(os.Stdout, "action", "Run: talos version")
		return nil
	}
	primaryErr := fmt.Errorf("go install failed: %w\n%s", err, strings.TrimSpace(string(out)))
	if localErr := installFromLocalCheckout(); localErr == nil {
		binPath := resolveTalosBinPath()
		printCommandStatus(os.Stdout, "talos update apply", "success")
		printSection(os.Stdout, "results")
		printKV(os.Stdout, "method", "local checkout fallback")
		printKV(os.Stdout, "binary_path", binPath)
		printSection(os.Stdout, "next")
		printKV(os.Stdout, "action", "Run: talos version")
		return nil
	}
	if fallbackErr := installFromOriginHead(latest); fallbackErr != nil {
		return fmt.Errorf("%v\nlocal checkout fallback failed.\norigin fallback install failed: %v", primaryErr, fallbackErr)
	}
	binPath := resolveTalosBinPath()
	printCommandStatus(os.Stdout, "talos update apply", "success")
	printSection(os.Stdout, "results")
	printKV(os.Stdout, "method", "origin fallback")
	printKV(os.Stdout, "binary_path", binPath)
	printSection(os.Stdout, "next")
	printKV(os.Stdout, "action", "Run: talos version")
	return nil
}

func runUpdateAutoConfig() error {
	cfg, err := loadUpdateConfig()
	if err != nil {
		return err
	}
	if updateAutoEnable && updateAutoDisable {
		return errors.New("cannot use --enable and --disable together")
	}
	if updateAutoApply && updateAutoNoApply {
		return errors.New("cannot use --apply and --no-apply together")
	}
	changed := false
	if updateAutoEnable {
		cfg.Enabled = true
		changed = true
	}
	if updateAutoDisable {
		cfg.Enabled = false
		changed = true
	}
	if updateAutoApply {
		cfg.AutoApply = true
		changed = true
	}
	if updateAutoNoApply {
		cfg.AutoApply = false
		changed = true
	}
	if updateAutoInterval > 0 {
		hours := int(updateAutoInterval.Hours())
		if hours < 1 {
			hours = 1
		}
		cfg.IntervalHours = hours
		changed = true
	}
	if changed {
		if err := saveUpdateConfig(cfg); err != nil {
			return err
		}
		printCommandStatus(os.Stdout, "talos update auto", "success")
		printSection(os.Stdout, "result")
		printKV(os.Stdout, "message", "Auto-update configuration updated.")
	} else {
		printCommandStatus(os.Stdout, "talos update auto", "success")
	}
	printSection(os.Stdout, "config")
	printKV(os.Stdout, "enabled", fmt.Sprintf("%t", cfg.Enabled))
	printKV(os.Stdout, "auto_apply", fmt.Sprintf("%t", cfg.AutoApply))
	printKV(os.Stdout, "interval_hours", fmt.Sprintf("%d", cfg.IntervalHours))
	if !cfg.LastCheckedAt.IsZero() {
		printKV(os.Stdout, "last_checked", cfg.LastCheckedAt.UTC().Format(time.RFC3339))
	}
	if strings.TrimSpace(cfg.LastSeen) != "" {
		printKV(os.Stdout, "last_seen", cfg.LastSeen)
	}
	printSection(os.Stdout, "next")
	printKV(os.Stdout, "action", "Re-run `talos update check` to verify current release status.")
	return nil
}

func maybeAutoUpdate() {
	cfg, err := loadUpdateConfig()
	if err != nil || !cfg.Enabled {
		return
	}
	if cfg.IntervalHours <= 0 {
		cfg.IntervalHours = defaultAutoUpdateHours
	}
	if !cfg.LastCheckedAt.IsZero() && time.Since(cfg.LastCheckedAt) < time.Duration(cfg.IntervalHours)*time.Hour {
		return
	}
	latest, err := fetchLatestVersion(4 * time.Second)
	if err != nil {
		return
	}
	cfg.LastCheckedAt = time.Now().UTC()
	cfg.LastSeen = latest
	_ = saveUpdateConfig(cfg)

	current := currentTalosVersion()
	if compareSemver(current, latest) >= 0 {
		return
	}
	fmt.Printf("NOTICE: TALOS update available (%s -> %s)\n", current, latest)
	if !cfg.AutoApply {
		fmt.Println("Run: talos update apply")
		return
	}
	if err := runUpdateApply(); err != nil {
		fmt.Printf("NOTICE: auto-update failed: %v\n", err)
	}
}

func updateConfigPath() string {
	return filepath.Join(".memory", "talos_update_config.json")
}

func loadUpdateConfig() (updateConfig, error) {
	cfg := updateConfig{
		Enabled:       true,
		AutoApply:     false,
		IntervalHours: defaultAutoUpdateHours,
	}
	path := updateConfigPath()
	b, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			_ = os.MkdirAll(filepath.Dir(path), 0o755)
			_ = saveUpdateConfig(cfg)
			return cfg, nil
		}
		return cfg, err
	}
	if err := json.Unmarshal(b, &cfg); err != nil {
		return cfg, err
	}
	if cfg.IntervalHours <= 0 {
		cfg.IntervalHours = defaultAutoUpdateHours
	}
	return cfg, nil
}

func saveUpdateConfig(cfg updateConfig) error {
	path := updateConfigPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o644)
}

func fetchLatestVersion(timeout time.Duration) (string, error) {
	v, err := fetchLatestVersionFromModule(timeout)
	if err == nil {
		return v, nil
	}
	moduleErr := err

	v, err = fetchLatestVersionFromGitTags(timeout)
	if err == nil {
		return v, nil
	}
	return "", fmt.Errorf("failed querying latest module version: %v; git tag fallback failed: %v", moduleErr, err)
}

func fetchLatestVersionFromModule(timeout time.Duration) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, "go", "list", "-m", "-json", configuredUpdateModulePath()+"@latest").CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("%w: %s", err, strings.TrimSpace(string(out)))
	}
	var payload struct {
		Version string `json:"Version"`
	}
	if err := json.Unmarshal(out, &payload); err != nil {
		return "", fmt.Errorf("failed parsing latest module version: %w", err)
	}
	v := strings.TrimSpace(payload.Version)
	if v == "" {
		return "", errors.New("latest module version is empty")
	}
	return v, nil
}

func fetchLatestVersionFromGitTags(timeout time.Duration) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, "git", "ls-remote", "--tags", "--refs", "origin").CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("%w: %s", err, strings.TrimSpace(string(out)))
	}
	return extractLatestSemverTag(string(out))
}

func extractLatestSemverTag(raw string) (string, error) {
	lines := strings.Split(raw, "\n")
	tags := make([]string, 0, len(lines))
	for _, ln := range lines {
		ln = strings.TrimSpace(ln)
		if ln == "" {
			continue
		}
		parts := strings.Fields(ln)
		if len(parts) < 2 {
			continue
		}
		ref := strings.TrimSpace(parts[1])
		if !strings.HasPrefix(ref, "refs/tags/") {
			continue
		}
		tag := strings.TrimPrefix(ref, "refs/tags/")
		tag = strings.TrimSpace(tag)
		if tag == "" {
			continue
		}
		if parseSemver(tag) == [3]int{} {
			continue
		}
		tags = append(tags, tag)
	}
	if len(tags) == 0 {
		return "", errors.New("no semantic version tags found on origin")
	}
	sort.SliceStable(tags, func(i, j int) bool {
		return compareSemver(tags[i], tags[j]) > 0
	})
	return tags[0], nil
}

func resolveTalosBinPath() string {
	if gobin := strings.TrimSpace(os.Getenv("GOBIN")); gobin != "" {
		return filepath.Join(gobin, "talos")
	}
	out, err := exec.Command("go", "env", "GOPATH").Output()
	if err != nil {
		return "talos (path unknown)"
	}
	gopath := strings.TrimSpace(string(out))
	if gopath == "" {
		return "talos (path unknown)"
	}
	return filepath.Join(gopath, "bin", "talos")
}

func configuredUpdateModulePath() string {
	if v := strings.TrimSpace(os.Getenv("TALOS_UPDATE_MODULE_PATH")); v != "" {
		return v
	}
	if fromOrigin := resolveModulePathFromOrigin(); fromOrigin != "" {
		return fromOrigin
	}
	return updateModulePath
}

func configuredUpdateCmdPath() string {
	if v := strings.TrimSpace(os.Getenv("TALOS_UPDATE_CMD_PATH")); v != "" {
		return v
	}
	return configuredUpdateModulePath() + "/cmd/talos"
}

func resolveModulePathFromOrigin() string {
	out, err := exec.Command("git", "remote", "get-url", "origin").Output()
	if err != nil {
		return ""
	}
	url := strings.TrimSpace(string(out))
	if url == "" {
		return ""
	}
	url = strings.TrimSuffix(url, ".git")
	url = strings.TrimPrefix(url, "https://")
	url = strings.TrimPrefix(url, "http://")
	url = strings.TrimPrefix(url, "ssh://")
	if strings.Contains(url, "@") && strings.Contains(url, ":") {
		parts := strings.SplitN(url, "@", 2)
		hostAndPath := parts[1]
		hostAndPath = strings.Replace(hostAndPath, ":", "/", 1)
		url = hostAndPath
	}
	url = strings.TrimPrefix(url, "git@")
	if !strings.Contains(url, "/") {
		return ""
	}
	if strings.HasPrefix(url, "github.com/") {
		return url
	}
	return ""
}

func resolveOriginRemoteURL() string {
	out, err := exec.Command("git", "remote", "get-url", "origin").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func installFromOriginHead(_ string) error {
	remote := resolveOriginRemoteURL()
	if remote == "" {
		return errors.New("git origin URL not found")
	}
	tmpDir, err := os.MkdirTemp("", "talos-update-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmpDir)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	cloneOut, cloneErr := exec.CommandContext(ctx, "git", "clone", "--depth", "1", remote, tmpDir).CombinedOutput()
	if cloneErr != nil {
		return fmt.Errorf("git clone failed: %w: %s", cloneErr, strings.TrimSpace(string(cloneOut)))
	}

	installCtx, installCancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer installCancel()
	installCmd := exec.CommandContext(installCtx, "go", "install", "./cmd/talos")
	installCmd.Dir = tmpDir
	installOut, installErr := installCmd.CombinedOutput()
	if installErr != nil {
		return fmt.Errorf("go install ./cmd/talos failed: %w: %s", installErr, strings.TrimSpace(string(installOut)))
	}
	return nil
}

func installFromLocalCheckout() error {
	wd, err := os.Getwd()
	if err != nil {
		return err
	}
	if _, err := os.Stat(filepath.Join(wd, "go.mod")); err != nil {
		return fmt.Errorf("go.mod not found in current directory")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", "install", "./cmd/talos")
	cmd.Dir = wd
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("go install ./cmd/talos failed: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func compareSemver(a, b string) int {
	pa := parseSemver(a)
	pb := parseSemver(b)
	for i := 0; i < 3; i++ {
		if pa[i] < pb[i] {
			return -1
		}
		if pa[i] > pb[i] {
			return 1
		}
	}
	return 0
}

func parseSemver(v string) [3]int {
	var out [3]int
	v = strings.TrimSpace(strings.TrimPrefix(strings.ToLower(v), "v"))
	re := regexp.MustCompile(`^(\d+)\.(\d+)\.(\d+)`)
	m := re.FindStringSubmatch(v)
	if len(m) != 4 {
		return out
	}
	for i := 0; i < 3; i++ {
		n, err := strconv.Atoi(m[i+1])
		if err != nil {
			return [3]int{}
		}
		out[i] = n
	}
	return out
}
