package taloscli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

type releaseCheck struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Detail string `json:"detail,omitempty"`
	Action string `json:"action,omitempty"`
}

type releaseCheckReport struct {
	Strict bool           `json:"strict"`
	Ready  bool           `json:"ready"`
	Pass   int            `json:"pass"`
	Warn   int            `json:"warn"`
	Fail   int            `json:"fail"`
	Checks []releaseCheck `json:"checks"`
}

var (
	releaseCheckStrict    bool
	releaseCheckNoNetwork bool
	releaseCheckTimeout   time.Duration
	releaseCheckJSON      bool

	releaseCheckTestRunner = defaultReleaseCheckTestRunner
)

var releaseCheckCmd = &cobra.Command{
	Use:   "release-check",
	Short: "Run pre-release readiness checks and enforce release gates.",
	Long:  "Runs doctor diagnostics and release-focused checks, then returns non-zero on blocked release policy.",
	RunE: func(cmd *cobra.Command, args []string) error {
		report := runReleaseChecks(releaseCheckTimeout, releaseCheckNoNetwork, releaseCheckStrict)
		if releaseCheckJSON {
			blob, _ := json.MarshalIndent(report, "", "  ")
			fmt.Fprintln(cmd.OutOrStdout(), string(blob))
		} else {
			fmt.Fprintln(cmd.OutOrStdout(), renderReleaseCheckReport(report))
		}
		if !report.Ready {
			return fmt.Errorf("release-check failed")
		}
		return nil
	},
}

func runReleaseChecks(timeout time.Duration, noNetwork, strict bool) releaseCheckReport {
	checks := make([]releaseCheck, 0)
	for _, d := range runDoctorChecks(timeout, noNetwork) {
		checks = append(checks, releaseCheck{Name: d.Name, Status: d.Status, Detail: d.Detail, Action: d.Action})
	}

	if err := releaseCheckTestRunner(2 * time.Minute); err != nil {
		checks = append(checks, releaseCheck{
			Name:   "CLI Package Tests",
			Status: "FAIL",
			Detail: err.Error(),
			Action: "Run: go test ./pkg/taloscli and fix failing tests before release.",
		})
	} else {
		checks = append(checks, releaseCheck{
			Name:   "CLI Package Tests",
			Status: "PASS",
			Detail: "go test ./pkg/taloscli succeeded.",
		})
	}

	if strings.TrimSpace(currentTalosVersion()) == "" {
		checks = append(checks, releaseCheck{
			Name:   "Build Version Metadata",
			Status: "WARN",
			Detail: "Version metadata is empty.",
			Action: "Set build ldflags for talosVersion before release build.",
		})
	} else {
		checks = append(checks, releaseCheck{
			Name:   "Build Version Metadata",
			Status: "PASS",
			Detail: "Version metadata present: " + strings.TrimSpace(currentTalosVersion()),
		})
	}

	readmeBlob, err := os.ReadFile("README.md")
	if err != nil {
		checks = append(checks, releaseCheck{
			Name:   "README Release-Check Example",
			Status: "WARN",
			Detail: "Unable to read README.md.",
			Action: "Ensure README.md includes release-check command examples.",
		})
	} else if !strings.Contains(strings.ToLower(string(readmeBlob)), "talos release-check") {
		checks = append(checks, releaseCheck{
			Name:   "README Release-Check Example",
			Status: "WARN",
			Detail: "README.md does not contain 'talos release-check'.",
			Action: "Add release-check usage to README.md.",
		})
	} else {
		checks = append(checks, releaseCheck{
			Name:   "README Release-Check Example",
			Status: "PASS",
			Detail: "README.md contains release-check usage.",
		})
	}

	report := releaseCheckReport{Strict: strict, Checks: checks}
	for _, c := range checks {
		switch strings.ToUpper(strings.TrimSpace(c.Status)) {
		case "PASS":
			report.Pass++
		case "WARN":
			report.Warn++
		default:
			report.Fail++
		}
	}
	if strict {
		report.Ready = report.Warn == 0 && report.Fail == 0
	} else {
		report.Ready = report.Fail == 0
	}
	return report
}

func renderReleaseCheckReport(report releaseCheckReport) string {
	var b strings.Builder
	b.WriteString("TALOS RELEASE CHECK\n\n")
	b.WriteString("COMMAND\n")
	b.WriteString("  talos release-check\n\n")
	status := "SUCCESS"
	if !report.Ready && report.Fail > 0 {
		status = "FAILED"
	} else if !report.Ready {
		status = "PARTIAL"
	}
	b.WriteString("STATUS\n")
	b.WriteString("  " + status + "\n\n")
	b.WriteString("SUMMARY\n")
	b.WriteString(fmt.Sprintf("  strict: %t\n", report.Strict))
	b.WriteString(fmt.Sprintf("  ready: %t\n", report.Ready))
	b.WriteString(fmt.Sprintf("  pass: %d\n", report.Pass))
	b.WriteString(fmt.Sprintf("  warn: %d\n", report.Warn))
	b.WriteString(fmt.Sprintf("  fail: %d\n", report.Fail))
	b.WriteString("\nCHECKS\n")
	for _, c := range report.Checks {
		b.WriteString(fmt.Sprintf("  [%s] %s\n", strings.ToUpper(strings.TrimSpace(c.Status)), c.Name))
		if strings.TrimSpace(c.Detail) != "" {
			b.WriteString(fmt.Sprintf("    Detail: %s\n", c.Detail))
		}
		if strings.TrimSpace(c.Action) != "" {
			b.WriteString(fmt.Sprintf("    Action: %s\n", c.Action))
		}
	}
	b.WriteString("\nNEXT\n")
	if report.Ready {
		b.WriteString("  Release gate passed. Proceed with tagging/publish workflow.\n")
	} else if report.Fail > 0 {
		b.WriteString("  Resolve FAIL checks, then rerun: talos release-check\n")
	} else {
		b.WriteString("  Resolve WARN checks or run with --strict=false if policy allows.\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

func defaultReleaseCheckTestRunner(timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, "go", "test", "./pkg/taloscli").CombinedOutput()
	if err != nil {
		trimmed := strings.TrimSpace(string(out))
		if trimmed == "" {
			trimmed = err.Error()
		}
		return errors.New(trimmed)
	}
	return nil
}

func init() {
	releaseCheckCmd.Flags().BoolVar(&releaseCheckStrict, "strict", true, "Fail the gate when WARN or FAIL checks exist")
	releaseCheckCmd.Flags().BoolVar(&releaseCheckNoNetwork, "no-network", false, "Skip network-dependent checks")
	releaseCheckCmd.Flags().DurationVar(&releaseCheckTimeout, "timeout", 2*time.Second, "Network probe timeout")
	releaseCheckCmd.Flags().BoolVar(&releaseCheckJSON, "json", false, "Render report as JSON")
	rootCmd.AddCommand(releaseCheckCmd)
}
