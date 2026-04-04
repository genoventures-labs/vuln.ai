package mesh

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	defaultPulseInterval      = 60 * time.Second
	defaultFailureThreshold   = 3
	defaultRequestTimeout     = 8 * time.Second
	defaultPingPath           = "/mesh/ping"
	dropNarrationDocumentName = "talos_distributed_tool_mesh_handoff.md"
)

var ErrNeighborSignatureInvalid = errors.New("neighbor signature invalid")

// Neighbor is a mesh neighbor row needed by the pulse loop.
type Neighbor struct {
	ID    int64
	URL   string
	Label string
}

// NeighborStore reads active neighbors and deactivates unreachable ones.
type NeighborStore interface {
	ListActiveNeighbors(ctx context.Context) ([]Neighbor, error)
	MarkInactive(ctx context.Context, id int64, lastError string) error
}

// Pinger checks if a neighbor is reachable.
type Pinger interface {
	Ping(ctx context.Context, neighbor Neighbor) error
}

// Pulse runs background health checks against mesh neighbors.
type Pulse struct {
	store  NeighborStore
	pinger Pinger
	logger *log.Logger

	interval       time.Duration
	failureLimit   int
	requestTimeout time.Duration

	mu           sync.Mutex
	failuresByID map[int64]int
}

// NewPulse creates a pulse worker with sane defaults.
func NewPulse(store NeighborStore, pinger Pinger, logger *log.Logger) *Pulse {
	if logger == nil {
		logger = log.Default()
	}
	return &Pulse{
		store:          store,
		pinger:         pinger,
		logger:         logger,
		interval:       defaultPulseInterval,
		failureLimit:   defaultFailureThreshold,
		requestTimeout: defaultRequestTimeout,
		failuresByID:   make(map[int64]int),
	}
}

// WithInterval overrides the default 60-second pulse cadence.
func (p *Pulse) WithInterval(interval time.Duration) *Pulse {
	if interval > 0 {
		p.interval = interval
	}
	return p
}

// WithFailureLimit overrides the default of 3 consecutive failed pings.
func (p *Pulse) WithFailureLimit(limit int) *Pulse {
	if limit > 0 {
		p.failureLimit = limit
	}
	return p
}

// WithRequestTimeout overrides the per-neighbor ping timeout.
func (p *Pulse) WithRequestTimeout(timeout time.Duration) *Pulse {
	if timeout > 0 {
		p.requestTimeout = timeout
	}
	return p
}

// Run starts the background pulse and exits when ctx is canceled.
func (p *Pulse) Run(ctx context.Context) {
	if p.store == nil || p.pinger == nil {
		p.logger.Printf("mesh pulse not started: missing store or pinger")
		return
	}

	p.tick(ctx)

	ticker := time.NewTicker(p.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			p.tick(ctx)
		}
	}
}

func (p *Pulse) tick(ctx context.Context) {
	neighbors, err := p.store.ListActiveNeighbors(ctx)
	if err != nil {
		p.logger.Printf("mesh pulse: failed to read active neighbors: %v", err)
		return
	}
	if len(neighbors) == 0 {
		return
	}

	for _, n := range neighbors {
		pingCtx, cancel := context.WithTimeout(ctx, p.requestTimeout)
		err := p.pinger.Ping(pingCtx, n)
		cancel()

		if err == nil {
			p.setFailures(n.ID, 0)
			continue
		}

		if errors.Is(err, ErrNeighborSignatureInvalid) {
			p.logger.Printf("mesh pulse: invalid heartbeat signature from neighbor id=%d url=%s. isolating from Council.", n.ID, n.URL)
			if markErr := p.store.MarkInactive(ctx, n.ID, err.Error()); markErr != nil {
				p.logger.Printf("mesh pulse: failed to isolate neighbor id=%d url=%s err=%v", n.ID, n.URL, markErr)
				continue
			}
			p.setFailures(n.ID, 0)
			continue
		}

		count := p.incrementFailures(n.ID)
		p.logger.Printf("mesh pulse: ping failed for neighbor id=%d url=%s consecutive_failures=%d err=%v", n.ID, n.URL, count, err)
		if count < p.failureLimit {
			continue
		}
		if markErr := p.store.MarkInactive(ctx, n.ID, err.Error()); markErr != nil {
			p.logger.Printf("mesh pulse: failed to deactivate neighbor id=%d url=%s err=%v", n.ID, n.URL, markErr)
			continue
		}

		p.setFailures(n.ID, 0)
		label := strings.TrimSpace(n.Label)
		if label == "" {
			label = fmt.Sprintf("Node %d", n.ID)
		}
		p.logger.Printf("Sir, %s (%s) has dropped from the mesh. Re-routing distributed tasks to local forge. %s", label, n.URL, dropNarrationDocumentName)
	}
}

func (p *Pulse) incrementFailures(id int64) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.failuresByID[id]++
	return p.failuresByID[id]
}

func (p *Pulse) setFailures(id int64, count int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if count == 0 {
		delete(p.failuresByID, id)
		return
	}
	p.failuresByID[id] = count
}

// SQLNeighborStore is a database/sql-backed implementation for mesh_neighbor.
type SQLNeighborStore struct {
	DB *sql.DB
}

func (s SQLNeighborStore) ListActiveNeighbors(ctx context.Context) ([]Neighbor, error) {
	if s.DB == nil {
		return nil, errors.New("nil DB")
	}

	rows, err := s.DB.QueryContext(ctx, `
		SELECT id, url, COALESCE(label, '')
		FROM mesh_neighbor
		WHERE is_active = true
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	neighbors := make([]Neighbor, 0, 8)
	for rows.Next() {
		var n Neighbor
		if scanErr := rows.Scan(&n.ID, &n.URL, &n.Label); scanErr != nil {
			return nil, scanErr
		}
		neighbors = append(neighbors, n)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	return neighbors, nil
}

func (s SQLNeighborStore) MarkInactive(ctx context.Context, id int64, lastError string) error {
	if s.DB == nil {
		return errors.New("nil DB")
	}

	_, err := s.DB.ExecContext(ctx, `
		UPDATE mesh_neighbor
		SET is_active = false,
			last_error = $1,
			updated_at = CURRENT_TIMESTAMP
		WHERE id = $2
	`, lastError, id)
	return err
}

// HTTPPinger sends signed heartbeat pings.
type HTTPPinger struct {
	Client *http.Client

	TrustToken    string
	NodeID        string
	NodeURL       string
	NodeLabel     string
	Capabilities  []string
	ShardCount    int
	CurrentLoad   float64
	ReasoningTier string
	PingPath      string
}

func (h HTTPPinger) Ping(ctx context.Context, neighbor Neighbor) error {
	if strings.TrimSpace(neighbor.URL) == "" {
		return errors.New("empty neighbor URL")
	}

	pingPath := strings.TrimSpace(h.PingPath)
	if pingPath == "" {
		pingPath = defaultPingPath
	}
	endpoint, err := joinPulseURLPath(neighbor.URL, pingPath)
	if err != nil {
		return err
	}

	payload := h.pingPayload()
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return err
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
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return ErrNeighborSignatureInvalid
	}
	if resp.StatusCode >= http.StatusBadRequest {
		return fmt.Errorf("mesh ping status %d", resp.StatusCode)
	}

	// Optional server ack.
	var ack struct {
		SignatureValid *bool `json:"signature_valid,omitempty"`
	}
	if decodeErr := json.NewDecoder(resp.Body).Decode(&ack); decodeErr == nil {
		if ack.SignatureValid != nil && !*ack.SignatureValid {
			return ErrNeighborSignatureInvalid
		}
	}
	return nil
}

func (h HTTPPinger) pingPayload() map[string]any {
	now := time.Now().Unix()
	nonce := strconv.FormatInt(time.Now().UTC().UnixNano(), 10)
	capabilities := canonicalCapabilities(h.Capabilities)
	capCSV := strings.Join(capabilities, ",")
	reasoningTier := strings.ToLower(strings.TrimSpace(h.ReasoningTier))
	signBase := fmt.Sprintf("%s|%s|%s|%d|%s|%s|%d|%.3f|%s",
		strings.TrimSpace(h.NodeID),
		strings.TrimSpace(h.NodeURL),
		strings.TrimSpace(h.NodeLabel),
		now,
		nonce,
		capCSV,
		h.ShardCount,
		h.CurrentLoad,
		reasoningTier,
	)

	return map[string]any{
		"trust_token": strings.TrimSpace(h.TrustToken),
		"node_id":     strings.TrimSpace(h.NodeID),
		"node_url":    strings.TrimSpace(h.NodeURL),
		"node_label":  strings.TrimSpace(h.NodeLabel),
		"timestamp":   now,
		"nonce":       nonce,
		"signature":   hmacSHA256Hex(strings.TrimSpace(h.TrustToken), signBase),
		"metadata": map[string]any{
			"capabilities":   capabilities,
			"shard_count":    h.ShardCount,
			"current_load":   h.CurrentLoad,
			"reasoning_tier": reasoningTier,
		},
	}
}

func canonicalCapabilities(values []string) []string {
	out := make([]string, 0, len(values))
	for _, v := range values {
		if n := strings.ToLower(strings.TrimSpace(v)); n != "" {
			out = append(out, n)
		}
	}
	sort.Strings(out)
	return out
}

func hmacSHA256Hex(secret, input string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(input))
	return hex.EncodeToString(mac.Sum(nil))
}

func joinPulseURLPath(baseURL, appendPath string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil {
		return "", fmt.Errorf("invalid base URL %q: %w", baseURL, err)
	}
	if u.Scheme == "" || u.Host == "" {
		return "", fmt.Errorf("invalid base URL %q: missing scheme or host", baseURL)
	}
	u.Path = strings.TrimRight(u.Path, "/") + "/" + strings.TrimLeft(appendPath, "/")
	u.RawQuery = ""
	u.Fragment = ""
	return u.String(), nil
}
