package taloscli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Thynaptic/P-LMv1/pkg/memory"
	"github.com/Thynaptic/P-LMv1/pkg/orchestration"
)

func TestEnsureAndRemovePathExportBlock(t *testing.T) {
	tmp := t.TempDir()
	rc := filepath.Join(tmp, ".bashrc")
	binDir := "/tmp/talos-bin"

	updated, err := ensurePathExportBlock(rc, binDir)
	if err != nil {
		t.Fatalf("ensurePathExportBlock failed: %v", err)
	}
	if !updated {
		t.Fatal("expected first ensurePathExportBlock call to update file")
	}

	content, err := os.ReadFile(rc)
	if err != nil {
		t.Fatalf("read profile failed: %v", err)
	}
	text := string(content)
	if !strings.Contains(text, pathBlockStart) || !strings.Contains(text, pathBlockEnd) {
		t.Fatal("expected TALOS PATH block to be present")
	}
	if !strings.Contains(text, binDir) {
		t.Fatalf("expected TALOS PATH block to include bin directory %q", binDir)
	}

	updated, err = ensurePathExportBlock(rc, binDir)
	if err != nil {
		t.Fatalf("second ensurePathExportBlock failed: %v", err)
	}
	if updated {
		t.Fatal("expected second ensurePathExportBlock call to be no-op")
	}

	removed, err := removePathExportBlock(rc)
	if err != nil {
		t.Fatalf("removePathExportBlock failed: %v", err)
	}
	if !removed {
		t.Fatal("expected removePathExportBlock to remove block")
	}

	content, err = os.ReadFile(rc)
	if err != nil {
		t.Fatalf("read profile after remove failed: %v", err)
	}
	text = string(content)
	if strings.Contains(text, pathBlockStart) || strings.Contains(text, pathBlockEnd) {
		t.Fatal("expected TALOS PATH block to be removed")
	}

	removed, err = removePathExportBlock(rc)
	if err != nil {
		t.Fatalf("second removePathExportBlock failed: %v", err)
	}
	if removed {
		t.Fatal("expected second removePathExportBlock call to be no-op")
	}
}

func TestShellSourceCommand(t *testing.T) {
	got := shellSourceCommand("bash", "/home/alex/.bashrc", "/home/alex")
	if got != "source ~/.bashrc" {
		t.Fatalf("expected bash source command, got %q", got)
	}

	got = shellSourceCommand("zsh", "/home/alex/.zshrc", "/home/alex")
	if got != "source ~/.zshrc" {
		t.Fatalf("expected zsh source command, got %q", got)
	}

	got = shellSourceCommand("bash", "/tmp/custom.rc", "/home/alex")
	if got != "source /tmp/custom.rc" {
		t.Fatalf("expected absolute source command, got %q", got)
	}
}

func TestRenderMonitorStatusReportIncludesMARHealth(t *testing.T) {
	report := renderMonitorStatusReport(
		true,
		"/tmp/bin/talos",
		"/bin/bash",
		"/usr/local/bin:/home/alex/.local/bin",
		memory.MARPolicy{
			Enabled:          true,
			CandidateLimit:   24,
			MaxAnchors:       12,
			MinAnchorScore:   0.20,
			WeightSemantic:   0.40,
			WeightLexical:    0.20,
			WeightImportance: 0.20,
			WeightFreshness:  0.15,
			WeightTopology:   0.05,
			TopologyBoost:    0.08,
			StatusReport:     false,
		},
		orchestration.WorldviewTruthState{},
		false,
		nil,
		orchestration.WorldviewTruthShift{},
		time.Time{},
		false,
		nil,
	)
	required := []string{
		"talos_in_path: yes",
		"resolved_binary: /tmp/bin/talos",
		"active_shell: /bin/bash",
		"path_contains_local_bin: true",
		"MAR HEALTH",
		"enabled: true",
		"candidate_limit: 24 | max_anchors: 12 | min_score: 0.20",
		"weights(s,l,i,f,t): 0.40,0.20,0.20,0.15,0.05",
		"topology_boost: 0.08 | status_report: false",
		"WORLDVIEW TRUTH",
		"state: none",
		"last_shift: none",
	}
	for _, token := range required {
		if !strings.Contains(report, token) {
			t.Fatalf("expected status report to contain %q, got: %s", token, report)
		}
	}
}

func TestRenderMonitorStatusReportIncludesWorldviewTruth(t *testing.T) {
	report := renderMonitorStatusReport(
		true,
		"/tmp/bin/talos",
		"/bin/bash",
		"/usr/local/bin:/home/alex/.local/bin",
		memory.EffectiveMARPolicy(),
		orchestration.WorldviewTruthState{
			TruthHash:     "abc123ef",
			ConflictIndex: 0.72,
			UpdatedAt:     time.Date(2026, 2, 18, 12, 30, 0, 0, time.UTC),
		},
		true,
		nil,
		orchestration.WorldviewTruthShift{
			Detected: true,
			Severity: 0.81,
			Reason:   "resolved winner changed for 50% of overlapping conflicts",
		},
		time.Date(2026, 2, 18, 12, 31, 0, 0, time.UTC),
		true,
		nil,
	)
	for _, token := range []string{
		"WORLDVIEW TRUTH",
		"hash: abc123ef",
		"conflict_index: 0.72",
		"last_shift: detected=true severity=0.81",
		"last_shift_reason: resolved winner changed for 50% of overlapping conflicts",
	} {
		if !strings.Contains(report, token) {
			t.Fatalf("expected worldview token %q, got: %s", token, report)
		}
	}
}

func TestMonitorStatusCommandPrintsMARHealth(t *testing.T) {
	out := &bytes.Buffer{}
	oldPath := os.Getenv("PATH")
	oldShell := os.Getenv("SHELL")
	t.Setenv("PATH", oldPath)
	t.Setenv("SHELL", oldShell)
	t.Setenv("TALOS_MAR_ENABLED", "true")
	t.Setenv("TALOS_MAR_CANDIDATE_LIMIT", "16")
	t.Setenv("TALOS_MAR_MAX_ANCHORS", "10")
	t.Setenv("TALOS_MAR_STATUS_ENABLED", "true")
	monitorStatusCmd.SetOut(out)

	if err := monitorStatusCmd.RunE(monitorStatusCmd, nil); err != nil {
		t.Fatalf("monitor status failed: %v", err)
	}
	rendered := out.String()
	for _, token := range []string{"MAR HEALTH", "candidate_limit:", "max_anchors:", "status_report: true"} {
		if !strings.Contains(rendered, token) {
			t.Fatalf("expected monitor status output to contain %q, got: %s", token, rendered)
		}
	}
}

func TestMonitorCommandIncludesReboot(t *testing.T) {
	cmd, _, err := monitorCmd.Find([]string{"reboot"})
	if err != nil {
		t.Fatalf("expected reboot subcommand to be registered: %v", err)
	}
	if cmd == nil || cmd.Name() != "reboot" {
		t.Fatalf("expected reboot subcommand, got %#v", cmd)
	}
}

func TestMonitorCommandIncludesRebootAlias(t *testing.T) {
	cmd, _, err := monitorCmd.Find([]string{"restart"})
	if err != nil {
		t.Fatalf("expected restart alias to resolve: %v", err)
	}
	if cmd == nil || cmd.Name() != "reboot" {
		t.Fatalf("expected restart alias to map to reboot, got %#v", cmd)
	}
}
