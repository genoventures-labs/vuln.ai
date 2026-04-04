package state

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	defaultMaxContextTokens      = 8192
	defaultAttentionPruneTrigger = 0.85
)

// CognitiveTelemetry tracks live reasoning pressure and reliability metrics.
type CognitiveTelemetry struct {
	mu sync.RWMutex

	MaxContextTokens int `json:"max_context_tokens"`

	AttentionPressure float64 `json:"attention_pressure"`
	DecisionCertainty float64 `json:"decision_certainty"`
	AlignmentDrift    float64 `json:"alignment_drift"`
	InferenceVelocity float64 `json:"inference_velocity"`

	usedTokens int

	symbolicAudits int
	symbolicVetoes int

	lastSummary string
	lastPruned  int
	lastPruneAt time.Time
}

// TelemetrySnapshot is a read-only copy of current telemetry.
type TelemetrySnapshot struct {
	AttentionPressure float64   `json:"attention_pressure"`
	DecisionCertainty float64   `json:"decision_certainty"`
	AlignmentDrift    float64   `json:"alignment_drift"`
	InferenceVelocity float64   `json:"inference_velocity"`
	UsedTokens        int       `json:"used_tokens"`
	MaxContextTokens  int       `json:"max_context_tokens"`
	LastSummary       string    `json:"last_summary,omitempty"`
	LastPruned        int       `json:"last_pruned,omitempty"`
	LastPruneAt       time.Time `json:"last_prune_at,omitempty"`
}

// NewCognitiveTelemetry initializes a telemetry collector.
func NewCognitiveTelemetry(maxContextTokens int) *CognitiveTelemetry {
	if maxContextTokens <= 0 {
		maxContextTokens = defaultMaxContextTokens
	}
	return &CognitiveTelemetry{MaxContextTokens: maxContextTokens}
}

// UpdateAttention sets context pressure from used token estimate.
func (t *CognitiveTelemetry) UpdateAttention(usedTokens int) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()

	if usedTokens < 0 {
		usedTokens = 0
	}
	t.usedTokens = usedTokens
	maxTokens := t.MaxContextTokens
	if maxTokens <= 0 {
		maxTokens = defaultMaxContextTokens
	}
	t.AttentionPressure = clamp01Telemetry(float64(usedTokens) / float64(maxTokens))
}

// RecordDecisionCertainty ingests branch confidence scores and updates running certainty.
func (t *CognitiveTelemetry) RecordDecisionCertainty(scores []float64) {
	if t == nil || len(scores) == 0 {
		return
	}
	var sum float64
	var n int
	for _, s := range scores {
		if math.IsNaN(s) || math.IsInf(s, 0) {
			continue
		}
		sum += clamp01Telemetry(s)
		n++
	}
	if n == 0 {
		return
	}
	avg := sum / float64(n)

	t.mu.Lock()
	defer t.mu.Unlock()
	if t.DecisionCertainty == 0 {
		t.DecisionCertainty = avg
		return
	}
	// EWMA for stability.
	t.DecisionCertainty = clamp01Telemetry((t.DecisionCertainty * 0.65) + (avg * 0.35))
}

// RecordSymbolicAudit updates alignment drift counters.
func (t *CognitiveTelemetry) RecordSymbolicAudit(veto bool) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.symbolicAudits++
	if veto {
		t.symbolicVetoes++
	}
	if t.symbolicAudits > 0 {
		t.AlignmentDrift = clamp01Telemetry(float64(t.symbolicVetoes) / float64(t.symbolicAudits))
	}
}

// RecordInferenceVelocity updates token-per-second throughput estimate.
func (t *CognitiveTelemetry) RecordInferenceVelocity(tokens int, elapsed time.Duration) {
	if t == nil || tokens <= 0 || elapsed <= 0 {
		return
	}
	v := float64(tokens) / elapsed.Seconds()
	if math.IsNaN(v) || math.IsInf(v, 0) || v < 0 {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.InferenceVelocity == 0 {
		t.InferenceVelocity = v
		return
	}
	// EWMA smoothing.
	t.InferenceVelocity = (t.InferenceVelocity * 0.70) + (v * 0.30)
}

// ShouldPrune decides whether attention pressure warrants local pruning.
func (t *CognitiveTelemetry) ShouldPrune() bool {
	if t == nil {
		return false
	}
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.AttentionPressure >= defaultAttentionPruneTrigger
}

// BuildStateSummary creates user-facing transparency copy.
func (t *CognitiveTelemetry) BuildStateSummary(action string) string {
	if t == nil {
		return ""
	}
	t.mu.RLock()
	pressure := t.AttentionPressure
	t.mu.RUnlock()

	action = strings.TrimSpace(action)
	if action == "" {
		action = "focus on the current task"
	}
	return fmt.Sprintf("I'm currently operating at %.0f%% attention pressure; I am pruning my local memory to %s.", pressure*100, action)
}

// MarkPruneEvent records a completed prune event and stores summary.
func (t *CognitiveTelemetry) MarkPruneEvent(pruned int, summary string) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.lastPruned = pruned
	t.lastPruneAt = time.Now().UTC()
	t.lastSummary = strings.TrimSpace(summary)
}

// ConsumeStateSummary returns and clears the last state summary.
func (t *CognitiveTelemetry) ConsumeStateSummary() string {
	if t == nil {
		return ""
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	s := strings.TrimSpace(t.lastSummary)
	t.lastSummary = ""
	return s
}

// Snapshot returns current telemetry values.
func (t *CognitiveTelemetry) Snapshot() TelemetrySnapshot {
	if t == nil {
		return TelemetrySnapshot{}
	}
	t.mu.RLock()
	defer t.mu.RUnlock()
	return TelemetrySnapshot{
		AttentionPressure: t.AttentionPressure,
		DecisionCertainty: t.DecisionCertainty,
		AlignmentDrift:    t.AlignmentDrift,
		InferenceVelocity: t.InferenceVelocity,
		UsedTokens:        t.usedTokens,
		MaxContextTokens:  t.MaxContextTokens,
		LastSummary:       t.lastSummary,
		LastPruned:        t.lastPruned,
		LastPruneAt:       t.lastPruneAt,
	}
}

// TelemetryFeedEvent is an append-only record for daemon consumers.
type TelemetryFeedEvent struct {
	Timestamp time.Time         `json:"timestamp"`
	Snapshot  TelemetrySnapshot `json:"snapshot"`
}

// AppendTelemetryFeed appends a telemetry snapshot to a JSONL feed.
func AppendTelemetryFeed(path string, snap TelemetrySnapshot) error {
	path = strings.TrimSpace(path)
	if path == "" {
		return fmt.Errorf("telemetry feed path is required")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	ev := TelemetryFeedEvent{
		Timestamp: time.Now().UTC(),
		Snapshot:  snap,
	}
	b, err := json.Marshal(ev)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.WriteString(string(b) + "\n")
	return err
}

func clamp01Telemetry(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}
