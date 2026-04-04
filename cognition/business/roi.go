package business

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	defaultLabResultsPath = ".memory/lab_assistant/results.jsonl"
	defaultDecisionFeed   = ".memory/decision_feed.jsonl"
	defaultROILogPath     = ".memory/business/roi_log.jsonl"

	taskBuildFix           = "build_fix"
	taskOracleEscalation   = "oracle_escalation"
	taskSecurityAudit      = "security_audit"
	taskMaintenance        = "maintenance"
	taskGeneralAutomation  = "general_automation"
	taskLabAssistantFix    = "lab_assistant_fix"
	taskComplianceAudit    = "compliance_audit"
	taskCapabilityPipeline = "capability_pipeline"
)

// TaskValueMap defines reclaimed-time value per swarm task type.
// Required founder metrics baseline:
// - LabAssistantFix = 45m
// - ComplianceAudit = 30m
// - CapabilityPipeline = 20m
var TaskValueMap = map[string]time.Duration{
	taskLabAssistantFix:    45 * time.Minute,
	taskComplianceAudit:    30 * time.Minute,
	taskCapabilityPipeline: 20 * time.Minute,
	taskBuildFix:           30 * time.Minute,
	taskOracleEscalation:   1 * time.Hour,
	taskSecurityAudit:      45 * time.Minute,
	taskMaintenance:        20 * time.Minute,
	taskGeneralAutomation:  15 * time.Minute,
}

// DailyROI summarizes reclaimed engineering time over the last 24 hours.
type DailyROI struct {
	WindowStart        time.Time     `json:"window_start"`
	WindowEnd          time.Time     `json:"window_end"`
	BuildFixes         int           `json:"build_fixes"`
	OracleEscalations  int           `json:"oracle_escalations"`
	SecurityAudits     int           `json:"security_audits"`
	MaintenanceActions int           `json:"maintenance_actions"`
	GeneralAutomations int           `json:"general_automations"`
	LabAssistantFixes  int           `json:"lab_assistant_fixes"`
	ComplianceAudits   int           `json:"compliance_audits"`
	CapabilityPipeline int           `json:"capability_pipeline"`
	TotalReclaimed     time.Duration `json:"total_reclaimed"`
	Report             string        `json:"report"`
}

type labResultRow struct {
	Timestamp time.Time `json:"timestamp"`
	Success   bool      `json:"success"`
}

type decisionRow struct {
	Timestamp  time.Time `json:"timestamp"`
	Query      string    `json:"query"`
	Reasoning  string    `json:"reasoning"`
	Topology   string    `json:"topology"`
	ChosenPath string    `json:"chosen_path"`
}

// ReclaimedTime maps task classes to reclaimed time value.
func ReclaimedTime(taskType string) time.Duration {
	t := strings.ToLower(strings.TrimSpace(taskType))
	if v, ok := TaskValueMap[t]; ok {
		return v
	}
	return 10 * time.Minute
}

// CalculateDailyROI scans the last 24h of lab results and decision feed, then persists ROI metrics.
func CalculateDailyROI() (DailyROI, error) {
	roi, err := CalculateDailyROIWithPaths(defaultLabResultsPath, defaultDecisionFeed)
	if err != nil {
		return roi, err
	}
	if perr := AppendROILog(defaultROILogPath, roi); perr != nil {
		return roi, perr
	}
	return roi, nil
}

// CalculateDailyROIWithPaths is an injectable variant for tests/custom files.
func CalculateDailyROIWithPaths(labResultsPath, decisionFeedPath string) (DailyROI, error) {
	now := time.Now().UTC()
	start := now.Add(-24 * time.Hour)
	out := DailyROI{
		WindowStart: start,
		WindowEnd:   now,
	}

	// Lab results => Build Fix + Founder-focused LabAssistantFix reclaim.
	labRows, err := readLabRowsSince(labResultsPath, start)
	if err != nil && !os.IsNotExist(err) {
		return out, err
	}
	for _, r := range labRows {
		if !r.Success {
			continue
		}
		out.BuildFixes++
		out.LabAssistantFixes++
		out.TotalReclaimed += ReclaimedTime(taskBuildFix)
		out.TotalReclaimed += ReclaimedTime(taskLabAssistantFix)
	}

	// Decision feed => classify swarm effort.
	decRows, err := readDecisionRowsSince(decisionFeedPath, start)
	if err != nil && !os.IsNotExist(err) {
		return out, err
	}
	for _, r := range decRows {
		task := classifyDecisionTask(r)
		switch task {
		case taskOracleEscalation:
			out.OracleEscalations++
		case taskSecurityAudit:
			out.SecurityAudits++
		case taskMaintenance:
			out.MaintenanceActions++
		case taskComplianceAudit:
			out.ComplianceAudits++
		case taskCapabilityPipeline:
			out.CapabilityPipeline++
		default:
			out.GeneralAutomations++
		}
		out.TotalReclaimed += ReclaimedTime(task)
	}

	hours := out.TotalReclaimed.Hours()
	out.Report = fmt.Sprintf(
		"Founder ROI Update: The swarm has handled the heavy lifting today, reclaiming %.1f hours for Thynaptic strategic vision.",
		hours,
	)
	return out, nil
}

// AppendROILog persists founder efficiency metrics to .memory/business/roi_log.jsonl.
func AppendROILog(path string, roi DailyROI) error {
	path = strings.TrimSpace(path)
	if path == "" {
		path = defaultROILogPath
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	b, err := json.Marshal(roi)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.WriteString(strings.TrimSpace(string(b)) + "\n")
	return err
}

func readLabRowsSince(path string, since time.Time) ([]labResultRow, error) {
	f, err := os.Open(strings.TrimSpace(path))
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []labResultRow
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		ln := strings.TrimSpace(sc.Text())
		if ln == "" {
			continue
		}
		var row labResultRow
		if err := json.Unmarshal([]byte(ln), &row); err != nil {
			continue
		}
		if !row.Timestamp.IsZero() && !row.Timestamp.After(since) {
			continue
		}
		out = append(out, row)
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func readDecisionRowsSince(path string, since time.Time) ([]decisionRow, error) {
	f, err := os.Open(strings.TrimSpace(path))
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []decisionRow
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		ln := strings.TrimSpace(sc.Text())
		if ln == "" {
			continue
		}
		var row decisionRow
		if err := json.Unmarshal([]byte(ln), &row); err != nil {
			continue
		}
		if !row.Timestamp.IsZero() && !row.Timestamp.After(since) {
			continue
		}
		out = append(out, row)
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func classifyDecisionTask(r decisionRow) string {
	text := strings.ToLower(strings.TrimSpace(strings.Join([]string{
		r.Query, r.Reasoning, r.Topology, r.ChosenPath,
	}, " ")))
	switch {
	case containsAny(text, "oracle", "tier-1", "deadlock", "external consensus", "escalat"):
		return taskOracleEscalation
	case containsAny(text, "security", "audit", "federal", "policy", "sasswall"):
		return taskSecurityAudit
	case containsAny(text, "compliance hunter", "compliance-daemon", "compliance", "hardening backlog"):
		return taskComplianceAudit
	case containsAny(text, "capability pipeline", "manufactur", "smith", "tool build", "jit tool"):
		return taskCapabilityPipeline
	case containsAny(text, "maintenance", "cleanup", "reindex", "prune", "refactor", "backlog"):
		return taskMaintenance
	default:
		return taskGeneralAutomation
	}
}

func containsAny(s string, tokens ...string) bool {
	for _, t := range tokens {
		if strings.Contains(s, strings.ToLower(strings.TrimSpace(t))) {
			return true
		}
	}
	return false
}
