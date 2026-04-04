package skills

import (
	"context"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
	"time"
)

const defaultSysExecTimeout = 20 * time.Second

var (
	sysExecForbiddenRE = []*regexp.Regexp{
		regexp.MustCompile(`(?i)\brm\s+-rf\b`),
		regexp.MustCompile(`(?i)\bmkfs(\.[a-z0-9]+)?\b`),
		regexp.MustCompile(`(?i)\bdd\s+if=`),
		regexp.MustCompile(`(?i)>\s*/dev/sda\b`),
		regexp.MustCompile(`(?i)\b/boot\b`),
		regexp.MustCompile(`(?i)\b/etc/shadow\b`),
		regexp.MustCompile(`(?i)\b/proc/sys\b`),
	}
	sensitiveWriteTargetsRE = []*regexp.Regexp{
		regexp.MustCompile(`(?i)\bnginx\.conf\b`),
		regexp.MustCompile(`(?i)\b/etc/nginx/`),
	}
	sensitiveWriteOpsRE = []*regexp.Regexp{
		regexp.MustCompile(`(?i)\bsed\s+-i\b`),
		regexp.MustCompile(`(?i)\btee\b`),
		regexp.MustCompile(`(?i)\bperl\s+-pi\b`),
		regexp.MustCompile(`(?i)\becho\b.*>`),
		regexp.MustCompile(`(?i)\bmv\b`),
	}
)

// SysExecRequest controls verified Linux execution.
type SysExecRequest struct {
	Command        string
	Override       bool
	TimeoutSeconds int
}

// SysExecResult is standardized feedback for orchestration loops.
type SysExecResult struct {
	Command           string `json:"command"`
	Allowed           bool   `json:"allowed"`
	VetoReason        string `json:"veto_reason,omitempty"`
	ReadFirstRequired bool   `json:"read_first_required,omitempty"`
	SuggestedPlan     string `json:"suggested_plan,omitempty"`
	Stdout            string `json:"stdout,omitempty"`
	Stderr            string `json:"stderr,omitempty"`
	ExitCode          int    `json:"exit_code"`
	DurationMS        int64  `json:"duration_ms"`
	TriggerOuroboros  bool   `json:"trigger_ouroboros,omitempty"`
	SuggestedFix      string `json:"suggested_fix,omitempty"`
}

// SysExec runs a command only after symbolic safety checks.
func SysExec(req SysExecRequest) SysExecResult {
	cmdText := strings.TrimSpace(req.Command)
	out := SysExecResult{
		Command:  cmdText,
		Allowed:  false,
		ExitCode: 1,
	}
	if cmdText == "" {
		out.VetoReason = "command is required"
		return out
	}

	if ok, reason := sysExecPassesBlacklist(cmdText, req.Override); !ok {
		out.VetoReason = reason
		out.SuggestedPlan = "Use explicit override=true only when the user has clearly approved the risky command."
		return out
	}

	if ok, plan := sysExecPassesReadFirstPolicy(cmdText); !ok {
		out.ReadFirstRequired = true
		out.VetoReason = "read-first policy violation on sensitive file operation"
		out.SuggestedPlan = plan
		return out
	}

	timeout := time.Duration(req.TimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = defaultSysExecTimeout
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	start := time.Now()
	cmd := exec.CommandContext(ctx, "bash", "-lc", cmdText)
	combined, err := cmd.CombinedOutput()
	out.DurationMS = time.Since(start).Milliseconds()
	text := strings.TrimSpace(string(combined))
	if err != nil {
		out.Allowed = true
		out.ExitCode = commandExitCode(err)
		out.Stderr = text
		out.TriggerOuroboros = true
		out.SuggestedFix = suggestShellFix(text, cmdText)
		return out
	}

	out.Allowed = true
	out.ExitCode = 0
	out.Stdout = text
	return out
}

func sysExecPassesBlacklist(cmd string, override bool) (bool, string) {
	if override || strings.Contains(strings.ToLower(cmd), "--override") {
		return true, ""
	}
	for _, re := range sysExecForbiddenRE {
		if re.MatchString(cmd) {
			return false, "forbidden command pattern matched: " + re.String()
		}
	}
	return true, ""
}

func sysExecPassesReadFirstPolicy(cmd string) (bool, string) {
	lc := strings.ToLower(cmd)
	sensitive := false
	for _, re := range sensitiveWriteTargetsRE {
		if re.MatchString(lc) {
			sensitive = true
			break
		}
	}
	if !sensitive {
		return true, ""
	}

	isWrite := false
	for _, re := range sensitiveWriteOpsRE {
		if re.MatchString(lc) {
			isWrite = true
			break
		}
	}
	if !isWrite {
		return true, ""
	}

	hasRead := strings.Contains(lc, "cat ") || strings.Contains(lc, "stat ")
	hasBackup := strings.Contains(lc, "cp ") && strings.Contains(lc, ".bak")
	if hasRead && hasBackup {
		return true, ""
	}

	plan := "Run read-first and backup before edit, e.g.:\n" +
		"1) stat /etc/nginx/nginx.conf && cat /etc/nginx/nginx.conf\n" +
		"2) cp /etc/nginx/nginx.conf /etc/nginx/nginx.conf.bak\n" +
		"3) apply your sed/tee change"
	return false, plan
}

func suggestShellFix(stderr, cmd string) string {
	s := strings.ToLower(stderr)
	switch {
	case strings.Contains(s, "permission denied"):
		return "Permission denied: verify current user/sudo policy and file ownership."
	case strings.Contains(s, "command not found"):
		return "Command not found: install missing package or use absolute binary path."
	case strings.Contains(s, "no such file"):
		return "Path missing: verify file path with 'ls -la' or 'stat' before retrying."
	case strings.Contains(s, "syntax error"):
		return "Shell syntax error: validate quoting and operators in the command."
	default:
		return fmt.Sprintf("Command failed. Re-check assumptions and run read-only diagnostics first. Command: %s", strings.TrimSpace(cmd))
	}
}
