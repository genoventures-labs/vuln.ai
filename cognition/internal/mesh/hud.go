package mesh

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
)

const councilMirrorLogLine = "Council of TALOS is in session. Cognitive shards are synchronized. The Sentinel is now a Legion, Mike/Cassian."

// SentinelHUDSnapshot is a compact status payload for UI/HUD rendering.
type SentinelHUDSnapshot struct {
	CouncilActive   bool   `json:"council_active"`
	ActiveNeighbors int    `json:"active_neighbors"`
	TotalShards     int    `json:"total_shards"`
	StatusLabel     string `json:"status_label"`
	MirrorLog       string `json:"mirror_log"`
}

// SentinelHUD composes live status counters for the Council view.
type SentinelHUD struct {
	Neighbors ActiveNeighborLister
	Shards    ThoughtShardStore
	Client    *http.Client

	MeshBaseURL string
	TrustToken  string
}

// EmitCouncilMirrorLog writes the Council activation mirror line.
func EmitCouncilMirrorLog(logger *log.Logger) {
	if logger == nil {
		log.Print(councilMirrorLogLine)
		return
	}
	logger.Print(councilMirrorLogLine)
}

func (h *SentinelHUD) Snapshot(ctx context.Context) (SentinelHUDSnapshot, error) {
	var snap SentinelHUDSnapshot
	if h.Neighbors == nil || h.Shards == nil {
		return snap, fmt.Errorf("sentinel hud: missing neighbors or shards store")
	}

	neighbors, err := h.Neighbors.ListActiveNeighbors(ctx)
	if err != nil {
		return snap, err
	}
	localShardCount, err := h.Shards.CountLocalShards(ctx)
	if err != nil {
		return snap, err
	}
	meshShardCount, _ := h.meshShardCount(ctx)

	snap = SentinelHUDSnapshot{
		CouncilActive:   true,
		ActiveNeighbors: len(neighbors),
		TotalShards:     localShardCount + meshShardCount,
		StatusLabel:     "COUNCIL ACTIVE",
		MirrorLog:       councilMirrorLogLine,
	}
	return snap, nil
}

func (h *SentinelHUD) meshShardCount(ctx context.Context) (int, error) {
	base := strings.TrimSpace(h.MeshBaseURL)
	if base == "" {
		return 0, nil
	}
	endpoint, err := joinURLPath(base, defaultShardQueryPath)
	if err != nil {
		return 0, err
	}

	payload, err := json.Marshal(map[string]any{"count_only": true})
	if err != nil {
		return 0, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	if token := strings.TrimSpace(h.TrustToken); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("X-Mesh-Trust-Token", token)
	}

	client := h.Client
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= http.StatusBadRequest {
		return 0, fmt.Errorf("mesh shard count status %d", resp.StatusCode)
	}

	var out struct {
		Total int `json:"total,omitempty"`
		Count int `json:"count,omitempty"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return 0, err
	}
	if out.Total > 0 {
		return out.Total, nil
	}
	return out.Count, nil
}
