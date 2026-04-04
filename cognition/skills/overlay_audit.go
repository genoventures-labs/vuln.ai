package skills

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const defaultOverlayAuditPath = ".memory/hud_overlay_audit.jsonl"

type overlayAuditRecord struct {
	Timestamp     time.Time `json:"timestamp"`
	Stage         string    `json:"stage,omitempty"`
	SessionID     string    `json:"session_id,omitempty"`
	Mode          string    `json:"mode"`
	Render        bool      `json:"render"`
	Reason        string    `json:"reason"`
	CandidateCnt  int       `json:"candidate_count"`
	SelectedCnt   int       `json:"selected_count"`
	DroppedCnt    int       `json:"dropped_count"`
	LatencyMicros int64     `json:"latency_micros"`
	Cached        bool      `json:"cached,omitempty"`
}

func appendOverlayAudit(d OverlayDecision, ctx OverlayContext, policy OverlayPolicy, candidateCount int) {
	rec := overlayAuditRecord{
		Timestamp:     time.Now().UTC(),
		Stage:         strings.TrimSpace(ctx.Stage),
		SessionID:     strings.TrimSpace(ctx.SessionID),
		Mode:          strings.TrimSpace(policy.DefaultMode),
		Render:        d.Render,
		Reason:        truncateOverlayText(strings.TrimSpace(d.Reason), 140),
		CandidateCnt:  candidateCount,
		SelectedCnt:   len(d.Selected),
		DroppedCnt:    len(d.Dropped),
		LatencyMicros: d.LatencyMicros,
		Cached:        d.Cached,
	}
	_ = os.MkdirAll(filepath.Dir(defaultOverlayAuditPath), 0o755)
	f, err := os.OpenFile(defaultOverlayAuditPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	b, err := json.Marshal(rec)
	if err != nil {
		return
	}
	_, _ = f.Write(append(b, '\n'))
}
