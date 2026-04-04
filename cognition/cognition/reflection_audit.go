package cognition

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type reflectionAuditEntry struct {
	Timestamp       time.Time         `json:"timestamp"`
	Stage           ReflectionStage   `json:"stage"`
	NodeID          string            `json:"node_id,omitempty"`
	Step            int               `json:"step,omitempty"`
	Outcome         ReflectionOutcome `json:"outcome"`
	RiskScore       float64           `json:"risk_score"`
	RelevanceScore  float64           `json:"relevance_score"`
	Violations      []string          `json:"violations,omitempty"`
	NeedsCorrection bool              `json:"needs_correction"`
	AuditID         string            `json:"audit_id,omitempty"`
}

var reflectionAuditMu sync.Mutex

func appendReflectionAudit(path string, entry reflectionAuditEntry) {
	if strings.TrimSpace(path) == "" {
		path = defaultReflectionAuditPath
	}
	reflectionAuditMu.Lock()
	defer reflectionAuditMu.Unlock()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return
	}
	line, err := json.Marshal(entry)
	if err != nil {
		return
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = f.Write(append(line, '\n'))
}

func reflectionAuditID(stage ReflectionStage, step int, nodeID string) string {
	return fmt.Sprintf("%s-%d-%s-%d", stage, step, strings.TrimSpace(nodeID), time.Now().UTC().UnixNano())
}
