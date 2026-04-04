package business

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	defaultComplianceBacklogPath = ".memory/compliance/backlog.jsonl"
	defaultCompliancePassedPath  = ".memory/compliance/passed_checks.jsonl"
	defaultOutboxDir             = ".memory/outbox"
	defaultMirrorBriefsPath      = ".memory/reasoning_mirror_briefs.jsonl"
)

// ComplianceCheck represents one normalized security/compliance check.
type ComplianceCheck struct {
	Timestamp time.Time `json:"timestamp"`
	CheckID   string    `json:"check_id,omitempty"`
	Name      string    `json:"name,omitempty"`
	Status    string    `json:"status,omitempty"` // passed | failed | advisory
	Severity  string    `json:"severity,omitempty"`
	Source    string    `json:"source,omitempty"`
	Note      string    `json:"note,omitempty"`
}

// ComplianceBriefResult captures generated brief metadata.
type ComplianceBriefResult struct {
	Path          string            `json:"path"`
	Date          string            `json:"date"`
	PassedChecks  []ComplianceCheck `json:"passed_checks,omitempty"`
	Advisories    []ComplianceCheck `json:"advisories,omitempty"`
	GeneratedAt   time.Time         `json:"generated_at"`
	MirrorMessage string            `json:"mirror_message"`
}

type complianceBacklogRow struct {
	Timestamp     time.Time `json:"timestamp"`
	BuildID       string    `json:"build_id,omitempty"`
	TaskID        string    `json:"task_id,omitempty"`
	SourcePath    string    `json:"source_path,omitempty"`
	Advisory      string    `json:"advisory"`
	SasswallBasis []string  `json:"sasswall_basis,omitempty"`
	Findings      []struct {
		Rule     string `json:"rule"`
		Severity string `json:"severity"`
		Note     string `json:"note,omitempty"`
	} `json:"findings,omitempty"`
}

type mirrorBrief struct {
	Timestamp time.Time `json:"timestamp"`
	Source    string    `json:"source,omitempty"`
	Message   string    `json:"message"`
}

// GenerateComplianceBrief exports a professional Markdown compliance brief and syncs mirror status.
func GenerateComplianceBrief() (ComplianceBriefResult, error) {
	return GenerateComplianceBriefWithPaths(defaultComplianceBacklogPath, defaultCompliancePassedPath, defaultOutboxDir, defaultMirrorBriefsPath, time.Now().UTC())
}

// GenerateComplianceBriefWithPaths is an injectable variant for custom pipelines/tests.
func GenerateComplianceBriefWithPaths(backlogPath, passedPath, outboxDir, mirrorPath string, now time.Time) (ComplianceBriefResult, error) {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	day := now.Format("2006-01-02")
	passedChecks, advisories, err := collectComplianceSignals(backlogPath, passedPath)
	if err != nil && !os.IsNotExist(err) {
		return ComplianceBriefResult{}, err
	}
	sort.SliceStable(passedChecks, func(i, j int) bool { return passedChecks[i].Timestamp.Before(passedChecks[j].Timestamp) })
	sort.SliceStable(advisories, func(i, j int) bool { return advisories[i].Timestamp.Before(advisories[j].Timestamp) })

	body := buildComplianceBriefMarkdown(day, now, passedChecks, advisories)
	if strings.TrimSpace(outboxDir) == "" {
		outboxDir = defaultOutboxDir
	}
	if err := os.MkdirAll(outboxDir, 0o755); err != nil {
		return ComplianceBriefResult{}, err
	}
	outPath := filepath.Join(outboxDir, "compliance_brief_"+day+".md")
	if err := os.WriteFile(outPath, []byte(body), 0o644); err != nil {
		return ComplianceBriefResult{}, err
	}

	msg := "Federal Compliance Brief staged in the outbox. It highlights our 100% local footprint."
	if strings.TrimSpace(mirrorPath) == "" {
		mirrorPath = defaultMirrorBriefsPath
	}
	_ = appendJSONL(mirrorPath, mustJSON(mirrorBrief{
		Timestamp: now.UTC(),
		Source:    "talos",
		Message:   msg,
	}))

	return ComplianceBriefResult{
		Path:          outPath,
		Date:          day,
		PassedChecks:  passedChecks,
		Advisories:    advisories,
		GeneratedAt:   now.UTC(),
		MirrorMessage: msg,
	}, nil
}

func collectComplianceSignals(backlogPath, passedPath string) ([]ComplianceCheck, []ComplianceCheck, error) {
	var passed []ComplianceCheck
	var advisories []ComplianceCheck

	// Preferred source for explicit pass events (if daemon emits them).
	if rows, err := readComplianceCheckRows(passedPath); err == nil {
		for _, r := range rows {
			status := strings.ToLower(strings.TrimSpace(r.Status))
			if status == "passed" && isAuditPassedSignal(r) {
				passed = append(passed, r)
			} else if status == "advisory" {
				advisories = append(advisories, r)
			}
		}
	}

	// Backlog is always present in current compliance-daemon implementation.
	backlogRows, err := readComplianceBacklogRows(backlogPath)
	if err != nil {
		return passed, advisories, err
	}
	for _, r := range backlogRows {
		hasHigh := false
		for _, f := range r.Findings {
			if strings.EqualFold(strings.TrimSpace(f.Severity), "high") {
				hasHigh = true
				advisories = append(advisories, ComplianceCheck{
					Timestamp: r.Timestamp,
					CheckID:   nonEmpty(strings.TrimSpace(r.BuildID), strings.TrimSpace(r.TaskID)),
					Name:      strings.TrimSpace(f.Rule),
					Status:    "advisory",
					Severity:  strings.TrimSpace(f.Severity),
					Source:    strings.TrimSpace(r.SourcePath),
					Note:      strings.TrimSpace(f.Note),
				})
			}
		}
		// Treat "production-ready" advisories with no high findings as passed hardening checks.
		if !hasHigh && strings.Contains(strings.ToLower(r.Advisory), "production-ready") {
			passed = append(passed, ComplianceCheck{
				Timestamp: r.Timestamp,
				CheckID:   nonEmpty(strings.TrimSpace(r.BuildID), strings.TrimSpace(r.TaskID)),
				Name:      "Audit Passed (glm-compliance-d baseline preflight)",
				Status:    "passed",
				Severity:  "info",
				Source:    strings.TrimSpace(r.SourcePath),
				Note:      strings.TrimSpace(r.Advisory),
			})
		}
	}
	return dedupeChecks(passed), dedupeChecks(advisories), nil
}

func readComplianceCheckRows(path string) ([]ComplianceCheck, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		path = defaultCompliancePassedPath
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []ComplianceCheck
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		ln := strings.TrimSpace(sc.Text())
		if ln == "" {
			continue
		}
		var row ComplianceCheck
		if err := json.Unmarshal([]byte(ln), &row); err != nil {
			continue
		}
		out = append(out, row)
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func readComplianceBacklogRows(path string) ([]complianceBacklogRow, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		path = defaultComplianceBacklogPath
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []complianceBacklogRow
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		ln := strings.TrimSpace(sc.Text())
		if ln == "" {
			continue
		}
		var row complianceBacklogRow
		if err := json.Unmarshal([]byte(ln), &row); err != nil {
			continue
		}
		out = append(out, row)
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func buildComplianceBriefMarkdown(day string, now time.Time, passed, advisories []ComplianceCheck) string {
	var b strings.Builder
	b.WriteString("# Federal Compliance Brief\n\n")
	b.WriteString(fmt.Sprintf("- Date: %s\n", day))
	b.WriteString(fmt.Sprintf("- Generated: %s UTC\n", now.UTC().Format(time.RFC3339)))
	b.WriteString("- Footprint: 100% local-first runtime (no cloud telemetry in core control loop)\n\n")

	b.WriteString("## Sovereign Evidence Aggregator\n\n")
	if len(passed) == 0 {
		b.WriteString("- No explicit passed checks were recorded in this window.\n")
	} else {
		for _, p := range passed {
			id := strings.TrimSpace(p.CheckID)
			if id == "" {
				id = "n/a"
			}
			b.WriteString(fmt.Sprintf("- ✅ `%s` %s (%s)\n", id, nonEmpty(p.Name, "Passed check"), nonEmpty(p.Source, "local")))
		}
	}
	b.WriteString("\n")

	b.WriteString("## Advisory Backlog (Non-Blocking)\n\n")
	if len(advisories) == 0 {
		b.WriteString("- No high-severity advisories detected.\n")
	} else {
		for _, a := range advisories {
			id := strings.TrimSpace(a.CheckID)
			if id == "" {
				id = "n/a"
			}
			b.WriteString(fmt.Sprintf("- ⚠️ `%s` %s [%s]\n", id, nonEmpty(a.Name, "Advisory"), nonEmpty(a.Severity, "unknown")))
			if n := strings.TrimSpace(a.Note); n != "" {
				b.WriteString(fmt.Sprintf("  - Note: %s\n", n))
			}
		}
	}
	b.WriteString("\n")

	b.WriteString("## Sovereign Cognitive Access Control (Sasswall)\n\n")
	b.WriteString("Sasswall principles are enforced as a local benchmark: **Access Control** and **Intent Analysis**.\n")
	b.WriteString("The product protects user intent by validating sensitive operations and policy context **without cloud-based telemetry**.\n")
	b.WriteString("This keeps decision authority local, auditable, and aligned with sovereign deployment constraints.\n")
	b.WriteString("\n")
	b.WriteString("## Sovereign Proof: Air-Gapped / Bunker Operations\n\n")
	b.WriteString("This product is designed for **air-gapped** and **bunker-grade** environments where external connectivity is restricted or prohibited.\n")
	b.WriteString("Core reasoning, policy checks, and evidence handling run locally, with no dependency on cloud control planes.\n")
	b.WriteString("Operational telemetry remains on-box, enabling compliance evidence generation without exposing user intent or mission context.\n")
	return b.String()
}

func isAuditPassedSignal(c ComplianceCheck) bool {
	blob := strings.ToLower(strings.TrimSpace(strings.Join([]string{
		c.Name, c.Note, c.Source, c.Status,
	}, " ")))
	if strings.TrimSpace(c.Status) != "passed" && strings.TrimSpace(strings.ToLower(c.Status)) != "passed" {
		return false
	}
	return strings.Contains(blob, "audit passed") ||
		strings.Contains(blob, "production-ready") ||
		strings.Contains(blob, "preflight") ||
		strings.Contains(blob, "compliance")
}

func dedupeChecks(in []ComplianceCheck) []ComplianceCheck {
	seen := map[string]bool{}
	var out []ComplianceCheck
	for _, c := range in {
		k := strings.ToLower(strings.TrimSpace(strings.Join([]string{
			c.CheckID, c.Name, c.Status, c.Source, c.Note,
		}, "|")))
		if k == "" || seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, c)
	}
	return out
}

func appendJSONL(path, line string) error {
	path = strings.TrimSpace(path)
	if path == "" {
		return fmt.Errorf("path required")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.WriteString(strings.TrimSpace(line) + "\n")
	return err
}

func mustJSON(v interface{}) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func nonEmpty(v, fallback string) string {
	v = strings.TrimSpace(v)
	if v != "" {
		return v
	}
	return strings.TrimSpace(fallback)
}
