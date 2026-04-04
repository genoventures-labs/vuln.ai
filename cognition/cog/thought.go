package cog

import (
	"fmt"
	"strings"
	"time"
)

const (
	SourceTypeFile = "file"
	SourceTypeURL  = "url"
	SourceTypeHF   = "hf"
)

// TemporalContext captures when and under which session window telemetry was observed.
type TemporalContext struct {
	ObservedAt time.Time `json:"observed_at"`
	SessionID  string    `json:"session_id,omitempty"`
	Window     string    `json:"window,omitempty"`
}

// ThoughtObject is the canonical cognitive primitive used across COG layers.
type ThoughtObject struct {
	ID              string          `json:"id"`
	Content         string          `json:"content"`
	SourceRef       string          `json:"source_ref,omitempty"`
	SourceType      string          `json:"source_type"`
	ConfidenceScore float64         `json:"confidence_score"`
	TemporalContext TemporalContext `json:"temporal_context"`

	MissionTag   string `json:"mission_tag,omitempty"`
	RequiresTool bool   `json:"requires_tool,omitempty"`
	AllowOffload bool   `json:"allow_offload,omitempty"`
}

// RawTelemetry is an input envelope from research/learn streams before normalization.
type RawTelemetry struct {
	ID              string
	Payload         string
	SourceRef       string
	SourceType      string
	ConfidenceScore float64
	ObservedAt      time.Time
	SessionID       string
	Window          string
	MissionTag      string
	RequiresTool    bool
	AllowOffload    bool
}

// NormalizeTelemetry converts raw telemetry into a ThoughtObject with strict validation.
func NormalizeTelemetry(raw RawTelemetry) (ThoughtObject, error) {
	if strings.TrimSpace(raw.Payload) == "" {
		return ThoughtObject{}, fmt.Errorf("normalize telemetry: payload is empty")
	}
	if raw.SourceType != SourceTypeFile && raw.SourceType != SourceTypeURL && raw.SourceType != SourceTypeHF {
		return ThoughtObject{}, fmt.Errorf("normalize telemetry: unsupported source_type %q", raw.SourceType)
	}
	if raw.ConfidenceScore < 0 || raw.ConfidenceScore > 1 {
		return ThoughtObject{}, fmt.Errorf("normalize telemetry: confidence score must be in [0,1]")
	}

	id := strings.TrimSpace(raw.ID)
	if id == "" {
		id = fmt.Sprintf("thought-%d", time.Now().UTC().UnixNano())
	}

	observedAt := raw.ObservedAt.UTC()
	if observedAt.IsZero() {
		observedAt = time.Now().UTC()
	}

	return ThoughtObject{
		ID:              id,
		Content:         strings.TrimSpace(raw.Payload),
		SourceRef:       strings.TrimSpace(raw.SourceRef),
		SourceType:      raw.SourceType,
		ConfidenceScore: raw.ConfidenceScore,
		TemporalContext: TemporalContext{
			ObservedAt: observedAt,
			SessionID:  strings.TrimSpace(raw.SessionID),
			Window:     strings.TrimSpace(raw.Window),
		},
		MissionTag:   strings.ToLower(strings.TrimSpace(raw.MissionTag)),
		RequiresTool: raw.RequiresTool,
		AllowOffload: raw.AllowOffload,
	}, nil
}
