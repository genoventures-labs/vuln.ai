package orchestration

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/Thynaptic/P-LMv1/pkg/memory"
	"github.com/Thynaptic/P-LMv1/pkg/skills"
	"github.com/Thynaptic/P-LMv1/pkg/tools"
)

const (
	defaultDocumentaryInboxDir      = ".memory/documentary/inbox"
	defaultDocumentaryProcessedDir  = ".memory/documentary/processed"
	defaultDocumentaryFailedDir     = ".memory/documentary/failed"
	defaultDocumentaryCollectionLog = ".memory/pocketbase/documentary_intel.jsonl"
	defaultIntelligenceAlertLog     = ".memory/intelligence_alerts.jsonl"
	defaultDaemonStatePath          = ".memory/documentary_daemon/state.json"
	defaultDocumentaryTick          = 45 * time.Second
	defaultDocumentaryClientID      = "documentary-engine"
)

// DocumentaryAlert is emitted when a critical contradiction crosses threshold.
type DocumentaryAlert struct {
	Timestamp       time.Time `json:"timestamp"`
	AlertType       string    `json:"alert_type"`
	Message         string    `json:"message"`
	Confidence      float64   `json:"confidence"`
	OldSourcePath   string    `json:"old_source_path,omitempty"`
	NewEvidencePath string    `json:"new_evidence_path,omitempty"`
	Significance    float64   `json:"significance_delta,omitempty"`
}

type documentaryState struct {
	LastRunAt      time.Time `json:"last_run_at"`
	ProcessedCount int       `json:"processed_count"`
	FailedCount    int       `json:"failed_count"`
}

// DocumentaryDaemon processes incoming evidence artifacts continuously.
type DocumentaryDaemon struct {
	Memory       *memory.MemoryManager
	Processor    *skills.DocumentaryProcessor
	InboxDir     string
	ProcessedDir string
	FailedDir    string
	Collection   string
	AlertsPath   string
	StatePath    string
	Tick         time.Duration
}

func NewDocumentaryDaemon(mm *memory.MemoryManager) *DocumentaryDaemon {
	return &DocumentaryDaemon{
		Memory:       mm,
		Processor:    &skills.DocumentaryProcessor{Memory: mm},
		InboxDir:     defaultDocumentaryInboxDir,
		ProcessedDir: defaultDocumentaryProcessedDir,
		FailedDir:    defaultDocumentaryFailedDir,
		Collection:   defaultDocumentaryCollectionLog,
		AlertsPath:   defaultIntelligenceAlertLog,
		StatePath:    defaultDaemonStatePath,
		Tick:         defaultDocumentaryTick,
	}
}

// Run starts the 24/7 daemon loop until context cancellation.
func (d *DocumentaryDaemon) Run(ctx context.Context) error {
	if err := d.ensureDirs(); err != nil {
		return err
	}
	if err := d.RunOnce(ctx); err != nil {
		// keep running despite first-cycle failures
	}
	t := d.Tick
	if t <= 0 {
		t = defaultDocumentaryTick
	}
	ticker := time.NewTicker(t)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			_ = d.RunOnce(ctx)
		}
	}
}

// RunOnce executes one inbox scan/process cycle.
func (d *DocumentaryDaemon) RunOnce(ctx context.Context) error {
	if err := d.ensureDirs(); err != nil {
		return err
	}
	files, err := d.listInboxArtifacts()
	if err != nil {
		return err
	}
	if len(files) == 0 {
		_ = d.writeState(func(s *documentaryState) { s.LastRunAt = time.Now().UTC() })
		return nil
	}

	var procCount, failCount int
	for _, f := range files {
		select {
		case <-ctx.Done():
			return nil
		default:
		}

		res, err := d.Processor.ProcessArtifact(skills.DocumentaryArtifact{
			Path:       f,
			SourceType: "documentary_daemon",
			Metadata: map[string]string{
				"daemon":          "documentary",
				"pb_collection":   "documentary_intel",
				"collection_name": "documentary_intel",
			},
		})
		if err != nil {
			failCount++
			_ = d.moveTo(f, d.FailedDir)
			continue
		}
		procCount++
		_ = d.persistCollectionEntry(res)
		_ = d.checkCriticalLinks(res)
		_ = d.moveTo(f, d.ProcessedDir)
	}
	_ = d.writeState(func(s *documentaryState) {
		s.LastRunAt = time.Now().UTC()
		s.ProcessedCount += procCount
		s.FailedCount += failCount
	})
	return nil
}

func (d *DocumentaryDaemon) ensureDirs() error {
	for _, p := range []string{
		d.InboxDir,
		d.ProcessedDir,
		d.FailedDir,
		filepath.Dir(d.Collection),
		filepath.Dir(d.AlertsPath),
		filepath.Dir(d.StatePath),
	} {
		if strings.TrimSpace(p) == "" {
			continue
		}
		if err := os.MkdirAll(p, 0o755); err != nil {
			return err
		}
	}
	return nil
}

func (d *DocumentaryDaemon) listInboxArtifacts() ([]string, error) {
	entries, err := os.ReadDir(d.InboxDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		ext := strings.ToLower(filepath.Ext(e.Name()))
		switch ext {
		case ".pdf", ".png", ".jpg", ".jpeg", ".webp", ".bmp", ".tiff":
			out = append(out, filepath.Join(d.InboxDir, e.Name()))
		}
	}
	sort.Strings(out)
	return out, nil
}

func (d *DocumentaryDaemon) persistCollectionEntry(res skills.DocumentaryResult) error {
	entry := map[string]interface{}{
		"timestamp":           time.Now().UTC().Format(time.RFC3339),
		"collection":          "documentary_intel",
		"source_path":         res.Artifact.Path,
		"rendered_pages":      len(res.RenderedPages),
		"high_value_entities": len(res.HighValueEntities),
		"graph_nodes":         len(res.Graph.Nodes),
		"graph_edges":         len(res.Graph.Edges),
		"reindex_findings":    len(res.ReindexFindings),
	}
	b, _ := json.Marshal(entry)
	if err := appendLineFile(d.Collection, string(b)); err != nil {
		return err
	}
	if d.Memory != nil {
		_ = d.Memory.AddKnowledge(
			fmt.Sprintf("Documentary daemon pushed artifact '%s' into collection documentary_intel.", res.Artifact.Path),
			map[string]string{
				"type":            "knowledge",
				"source_type":     "documentary_daemon",
				"source_path":     res.Artifact.Path,
				"pb_collection":   "documentary_intel",
				"collection_name": "documentary_intel",
				"base_importance": "0.83",
			},
		)
	}
	return nil
}

func (d *DocumentaryDaemon) checkCriticalLinks(res skills.DocumentaryResult) error {
	for _, f := range res.ReindexFindings {
		if f.SignificanceDelta <= 0.95 {
			continue
		}
		if !looksLikePublicRecord(f.OlderSourcePath) {
			continue
		}
		msg := "Critical Link: high-confidence contradiction with public record detected."
		alert := DocumentaryAlert{
			Timestamp:       time.Now().UTC(),
			AlertType:       "intelligence_alert",
			Message:         msg,
			Confidence:      f.SignificanceDelta,
			OldSourcePath:   f.OlderSourcePath,
			NewEvidencePath: f.NewEvidencePath,
			Significance:    f.SignificanceDelta,
		}
		b, _ := json.Marshal(alert)
		_ = appendLineFile(d.AlertsPath, string(b))
	}
	return nil
}

func looksLikePublicRecord(path string) bool {
	p := strings.ToLower(strings.TrimSpace(path))
	if p == "" {
		return false
	}
	for _, m := range []string{"public", "wikipedia", "wiki", "readme", "press", "news", "official"} {
		if strings.Contains(p, m) {
			return true
		}
	}
	return false
}

func (d *DocumentaryDaemon) moveTo(src, dstDir string) error {
	base := filepath.Base(src)
	dst := filepath.Join(dstDir, fmt.Sprintf("%d_%s", time.Now().Unix(), base))
	return os.Rename(src, dst)
}

func (d *DocumentaryDaemon) writeState(mut func(*documentaryState)) error {
	var s documentaryState
	if b, err := os.ReadFile(d.StatePath); err == nil && len(b) > 0 {
		_ = json.Unmarshal(b, &s)
	}
	if mut != nil {
		mut(&s)
	}
	b, _ := json.MarshalIndent(s, "", "  ")
	return os.WriteFile(d.StatePath, b, 0o644)
}

// ConsumeIntelligenceAlerts returns and clears pending intelligence alerts.
func ConsumeIntelligenceAlerts(path string, maxN int) ([]DocumentaryAlert, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		path = defaultIntelligenceAlertLog
	}
	if maxN <= 0 {
		maxN = 3
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	lines := splitNonEmptyLines(string(raw))
	if len(lines) == 0 {
		return nil, nil
	}
	var out []DocumentaryAlert
	for _, ln := range lines {
		var a DocumentaryAlert
		if err := json.Unmarshal([]byte(ln), &a); err != nil {
			continue
		}
		out = append(out, a)
	}
	if len(out) > maxN {
		out = out[len(out)-maxN:]
	}
	// clear after consume
	_ = os.WriteFile(path, []byte(""), 0o644)
	return out, nil
}

// ProvisionDocumentaryEngineClient provisions dedicated IAM credentials for the daemon.
func ProvisionDocumentaryEngineClient(clientID string, localStorePath string) (string, error) {
	if strings.TrimSpace(clientID) == "" {
		clientID = defaultDocumentaryClientID
	}
	if strings.TrimSpace(localStorePath) == "" {
		localStorePath = "api_keys.yaml"
	}
	admin, err := tools.NewGLMAdminClientFromEnv()
	if err != nil {
		return "", err
	}
	resp, err := admin.CreateClient(clientID)
	if err != nil {
		return "", err
	}
	if err := tools.UpsertLocalAPIKey(localStorePath, strings.TrimSpace(resp.ClientID), strings.TrimSpace(resp.APIKey)); err != nil {
		return "", err
	}
	return strings.TrimSpace(resp.ClientID), nil
}

func appendLineFile(path, line string) error {
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

func splitNonEmptyLines(s string) []string {
	var out []string
	for _, ln := range strings.Split(s, "\n") {
		ln = strings.TrimSpace(ln)
		if ln == "" {
			continue
		}
		out = append(out, ln)
	}
	return out
}
