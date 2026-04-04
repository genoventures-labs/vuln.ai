package taloscli

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
	defaultResearchSessionsPath = ".memory/research_sessions.jsonl"
	defaultResearchArtifactsDir = ".memory/research_artifacts"
)

var (
	researchSessionsPath = defaultResearchSessionsPath
	researchArtifactsDir = defaultResearchArtifactsDir
)

type ResearchSessionRecord struct {
	SessionID    string `json:"session_id"`
	StartedAt    string `json:"started_at"`
	FinishedAt   string `json:"finished_at"`
	Status       string `json:"status"`
	Mode         string `json:"mode"`
	Query        string `json:"query"`
	ProfileName  string `json:"profile_name,omitempty"`
	CategoryList string `json:"category_list,omitempty"`
	ArtifactPath string `json:"artifact_path,omitempty"`
	SourceCount  int    `json:"source_count"`
	FindingCount int    `json:"finding_count"`
	ToolLogCount int    `json:"tool_log_count"`
	Error        string `json:"error,omitempty"`
	Summary      string `json:"summary"`
}

type ResearchArtifact struct {
	SessionID        string            `json:"session_id"`
	CreatedAt        string            `json:"created_at"`
	Mode             string            `json:"mode"`
	Query            string            `json:"query"`
	ProfileName      string            `json:"profile_name,omitempty"`
	Categories       []string          `json:"categories,omitempty"`
	Status           string            `json:"status"`
	ExecutiveSummary string            `json:"executive_summary"`
	Findings         []researchFinding `json:"findings"`
	EvidenceNotes    []string          `json:"evidence_notes"`
	Risks            []string          `json:"risks"`
	NextActions      []string          `json:"next_actions"`
	Sources          []string          `json:"sources"`
	ToolLogCount     int               `json:"tool_log_count"`
	Error            string            `json:"error,omitempty"`
}

func persistResearchSession(query, mode string, report researchReport, status, errText string, toolLogCount int, ctx researchExecutionContext) (ResearchSessionRecord, error) {
	now := time.Now().UTC()
	sessionID := fmt.Sprintf("research-%d", now.UnixNano())
	categories := normalizeResearchTags(ctx.ProfileCategories)
	categoryList := strings.Join(categories, ",")
	artifact := ResearchArtifact{
		SessionID:        sessionID,
		CreatedAt:        now.Format(time.RFC3339),
		Mode:             strings.ToLower(strings.TrimSpace(mode)),
		Query:            strings.TrimSpace(query),
		ProfileName:      strings.TrimSpace(ctx.ProfileName),
		Categories:       categories,
		Status:           strings.ToUpper(strings.TrimSpace(status)),
		ExecutiveSummary: strings.TrimSpace(report.Executive),
		Findings:         report.Findings,
		EvidenceNotes:    report.EvidenceNotes,
		Risks:            report.Risks,
		NextActions:      report.NextActions,
		Sources:          uniqueStrings(report.Sources),
		ToolLogCount:     toolLogCount,
		Error:            strings.TrimSpace(errText),
	}

	artifactPath, writeErr := writeResearchArtifact(artifact)
	rec := ResearchSessionRecord{
		SessionID:    sessionID,
		StartedAt:    now.Format(time.RFC3339),
		FinishedAt:   time.Now().UTC().Format(time.RFC3339),
		Status:       artifact.Status,
		Mode:         strings.ToUpper(artifact.Mode),
		Query:        artifact.Query,
		ProfileName:  artifact.ProfileName,
		CategoryList: categoryList,
		ArtifactPath: artifactPath,
		SourceCount:  len(artifact.Sources),
		FindingCount: len(artifact.Findings),
		ToolLogCount: artifact.ToolLogCount,
		Error:        artifact.Error,
		Summary:      summarizeResearchSession(report, artifact.Status),
	}

	appendErr := appendResearchSessionRecord(rec)
	if writeErr != nil {
		return rec, writeErr
	}
	if appendErr != nil {
		return rec, appendErr
	}
	return rec, nil
}

func summarizeResearchSession(report researchReport, status string) string {
	status = strings.ToUpper(strings.TrimSpace(status))
	if status != "SUCCESS" {
		if len(report.Risks) > 0 {
			return strings.TrimSpace(report.Risks[0])
		}
		return "Research run did not complete successfully."
	}
	if strings.TrimSpace(report.Executive) != "" {
		return strings.TrimSpace(report.Executive)
	}
	if len(report.Findings) > 0 {
		return strings.TrimSpace(report.Findings[0].Text)
	}
	return "Research run completed successfully."
}

func writeResearchArtifact(a ResearchArtifact) (string, error) {
	if err := os.MkdirAll(researchArtifactsDir, 0o755); err != nil {
		return "", err
	}
	path := filepath.Join(researchArtifactsDir, a.SessionID+".json")
	b, err := json.MarshalIndent(a, "", "  ")
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(path, append(b, '\n'), 0o644); err != nil {
		return "", err
	}
	return path, nil
}

func appendResearchSessionRecord(rec ResearchSessionRecord) error {
	if err := os.MkdirAll(filepath.Dir(researchSessionsPath), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(researchSessionsPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	return json.NewEncoder(f).Encode(rec)
}

func readResearchSessionRecords() ([]ResearchSessionRecord, int, error) {
	f, err := os.Open(researchSessionsPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, 0, nil
		}
		return nil, 0, err
	}
	defer f.Close()

	var out []ResearchSessionRecord
	corrupt := 0
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var rec ResearchSessionRecord
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			corrupt++
			continue
		}
		out = append(out, rec)
	}
	if err := sc.Err(); err != nil {
		return nil, 0, err
	}

	sort.SliceStable(out, func(i, j int) bool {
		return out[i].StartedAt > out[j].StartedAt
	})
	return out, corrupt, nil
}

func loadResearchArtifactByID(id string) (ResearchArtifact, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return ResearchArtifact{}, fmt.Errorf("research artifact id is required")
	}
	path := filepath.Join(researchArtifactsDir, id+".json")
	b, err := os.ReadFile(path)
	if err != nil {
		return ResearchArtifact{}, err
	}
	var artifact ResearchArtifact
	if err := json.Unmarshal(b, &artifact); err != nil {
		return ResearchArtifact{}, err
	}
	return artifact, nil
}

func resolveResearchArtifactID(sel string) (string, error) {
	sel = strings.TrimSpace(sel)
	if sel != "" && !strings.EqualFold(sel, "latest") {
		return sel, nil
	}
	recs, _, err := readResearchSessionRecords()
	if err != nil {
		return "", err
	}
	for _, rec := range recs {
		if strings.EqualFold(rec.Status, "SUCCESS") {
			return rec.SessionID, nil
		}
	}
	return "", fmt.Errorf("no successful research sessions found")
}

func renderResearchSessions(records []ResearchSessionRecord, corrupt int, last int) string {
	var b strings.Builder
	b.WriteString("TALOS RESEARCH SESSIONS\n\n")
	b.WriteString("COMMAND\n")
	b.WriteString("  talos research sessions\n\n")
	b.WriteString("WINDOW\n")
	b.WriteString(fmt.Sprintf("  requested_last: %d\n", last))
	b.WriteString(fmt.Sprintf("  returned_sessions: %d\n", len(records)))
	b.WriteString(fmt.Sprintf("  corrupt_records_skipped: %d\n\n", corrupt))
	if len(records) == 0 {
		b.WriteString("STATUS\n")
		b.WriteString("  SUCCESS\n\n")
		b.WriteString("SESSIONS\n")
		b.WriteString("  No research sessions recorded yet.\n")
		b.WriteString("\nNEXT\n")
		b.WriteString("  Run: talos research run <query>\n")
		return b.String()
	}
	status := "SUCCESS"
	for _, rec := range records {
		switch strings.ToUpper(strings.TrimSpace(rec.Status)) {
		case "FAILED":
			status = "FAILED"
		case "PARTIAL":
			if status != "FAILED" {
				status = "PARTIAL"
			}
		}
	}
	b.WriteString("STATUS\n")
	b.WriteString("  " + status + "\n\n")
	b.WriteString("SESSIONS\n")
	for i, rec := range records {
		b.WriteString(fmt.Sprintf("  %d) %s | %s | %s\n", i+1, rec.StartedAt, strings.ToUpper(rec.Status), strings.ToUpper(rec.Mode)))
		b.WriteString(fmt.Sprintf("     session_id: %s\n", rec.SessionID))
		if strings.TrimSpace(rec.Query) != "" {
			b.WriteString(fmt.Sprintf("     query: %s\n", rec.Query))
		}
		if strings.TrimSpace(rec.ProfileName) != "" {
			b.WriteString(fmt.Sprintf("     profile: %s\n", rec.ProfileName))
		}
		if strings.TrimSpace(rec.CategoryList) != "" {
			b.WriteString(fmt.Sprintf("     categories: %s\n", rec.CategoryList))
		}
		b.WriteString(fmt.Sprintf("     findings: %d\n", rec.FindingCount))
		b.WriteString(fmt.Sprintf("     sources: %d\n", rec.SourceCount))
		if strings.TrimSpace(rec.Error) != "" {
			b.WriteString(fmt.Sprintf("     error: %s\n", rec.Error))
		}
	}
	b.WriteString("\nNEXT\n")
	b.WriteString("  Use --last to narrow session history or inspect artifact paths for follow-up.\n")
	return b.String()
}
