package cognition

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const defaultSupervisionAuditPath = ".memory/supervision_audit.jsonl"

type supervisionAuditRecord struct {
	Timestamp       time.Time          `json:"timestamp"`
	Stage           SupervisionStage   `json:"stage"`
	Outcome         SupervisionOutcome `json:"outcome"`
	RiskTier        RiskTier           `json:"risk_tier"`
	Violations      []string           `json:"violations,omitempty"`
	NextAction      string             `json:"next_action,omitempty"`
	ConfidenceDelta float64            `json:"confidence_delta,omitempty"`
	LatencyMicros   int64              `json:"latency_micros,omitempty"`
	Cached          bool               `json:"cached,omitempty"`
	QueryPreview    string             `json:"query_preview,omitempty"`
	OutputPreview   string             `json:"output_preview,omitempty"`
}

func appendSupervisionAudit(in SupervisionInput, decision SupervisionDecision) {
	record := supervisionAuditRecord{
		Timestamp:       time.Now().UTC(),
		Stage:           in.Stage,
		Outcome:         decision.Outcome,
		RiskTier:        decision.RiskTier,
		Violations:      append([]string(nil), decision.Violations...),
		NextAction:      decision.NextAction,
		ConfidenceDelta: decision.ConfidencePenalty,
		LatencyMicros:   decision.LatencyMicros,
		Cached:          decision.Cached,
		QueryPreview:    truncateSupervision(strings.TrimSpace(in.Query), 220),
		OutputPreview:   truncateSupervision(strings.TrimSpace(in.Candidate), 320),
	}
	if err := os.MkdirAll(filepath.Dir(defaultSupervisionAuditPath), 0o755); err != nil {
		return
	}
	f, err := os.OpenFile(defaultSupervisionAuditPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	b, err := json.Marshal(record)
	if err != nil {
		return
	}
	_, _ = f.Write(append(b, '\n'))
}

func truncateSupervision(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	if n < 4 {
		return s[:n]
	}
	return s[:n-3] + "..."
}
