package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/Thynaptic/P-LMv1/pkg/cognition"
	"github.com/Thynaptic/P-LMv1/pkg/envload"
	"github.com/Thynaptic/P-LMv1/pkg/skills"
	"github.com/Thynaptic/P-LMv1/pkg/state"
	"github.com/shirou/gopsutil/v3/cpu"
	"github.com/shirou/gopsutil/v3/mem"
	"github.com/shirou/gopsutil/v3/process"
)

const (
	defaultHUDStatePath   = ".memory/hud_process.json"
	defaultHUDResetSignal = ".memory/hud_system_reset.signal"
)

type reflexAlert struct {
	Timestamp      time.Time `json:"timestamp"`
	AlertType      string    `json:"alert_type"`
	Message        string    `json:"message"`
	Score          float64   `json:"score,omitempty"`
	Velocity       float64   `json:"velocity,omitempty"`
	ProposedAction string    `json:"proposed_action,omitempty"`
}

type toolFailureEvent struct {
	Timestamp time.Time `json:"timestamp"`
	Tool      string    `json:"tool"`
	Query     string    `json:"query,omitempty"`
	Error     string    `json:"error"`
}

type mirrorBrief struct {
	Timestamp time.Time `json:"timestamp"`
	Source    string    `json:"source,omitempty"`
	Message   string    `json:"message"`
}

type resourceSnapshot struct {
	CPUPercent float64
	RAMPercent float64
}

type telemetryEnvelope struct {
	Event     state.TelemetryFeedEvent `json:"event"`
	Resources resourceSnapshot         `json:"resources"`
}

type hudProcessState struct {
	PID        int       `json:"pid"`
	Utility    string    `json:"utility"`
	StartedAt  time.Time `json:"started_at"`
	ExpiresAt  time.Time `json:"expires_at"`
	X          int       `json:"x"`
	Y          int       `json:"y"`
	W          int       `json:"w"`
	H          int       `json:"h"`
	Text       string    `json:"text,omitempty"`
	Status     string    `json:"status"`
	LastReason string    `json:"last_reason,omitempty"`
}

type reflexDaemon struct {
	telemetryPath string
	toolFailPath  string
	quotaPath     string
	briefsPath    string
	alertsPath    string
	pollInterval  time.Duration

	velocityThreshold  float64
	cpuThreshold       float64
	ramThreshold       float64
	interruptThreshold float64
	interruptCooldown  time.Duration

	sm *state.Manager

	mu                sync.Mutex
	telemetryOffset   int64
	toolFailOffset    int64
	lowVelocityStreak int
	recentToolFails   int
	lastInterrupt     time.Time
	lastBrief         time.Time
	lastQuotaWarnDate string

	graph *cognition.ThoughtGraph
}

func main() {
	if err := envload.Autoload(); err != nil {
		fmt.Fprintf(os.Stderr, "warning: env autoload failed: %v\n", err)
	}

	var (
		telemetryFeed      = flag.String("telemetry-feed", ".memory/telemetry_feed.jsonl", "path to telemetry JSONL feed")
		toolFailuresFeed   = flag.String("tool-failures-feed", ".memory/tool_failures.jsonl", "path to tool failure JSONL feed")
		quotaFile          = flag.String("quota-file", ".memory/quota_usage.json", "path to quota usage JSON file")
		briefsFile         = flag.String("briefs-file", ".memory/reasoning_mirror_briefs.jsonl", "path to reasoning mirror brief JSONL file")
		alertsFile         = flag.String("alerts-file", ".memory/reflex_alerts.jsonl", "path to reflex alerts JSONL file")
		stateFile          = flag.String("state-file", ".memory/reflex_state.json", "path to reflex daemon session state")
		poll               = flag.Duration("poll", 2*time.Second, "poll interval for telemetry feed")
		velocityThreshold  = flag.Float64("velocity-threshold", 8.0, "minimum healthy inference velocity (tokens/sec)")
		cpuThreshold       = flag.Float64("cpu-threshold", 88.0, "CPU pressure threshold (%)")
		ramThreshold       = flag.Float64("ram-threshold", 90.0, "RAM pressure threshold (%)")
		interruptThreshold = flag.Float64("interrupt-threshold", 0.72, "minimum anomaly score for user interruption")
		interruptCooldown  = flag.Duration("interrupt-cooldown", 3*time.Minute, "minimum interval between user interruptions")
	)
	flag.Parse()

	sm, err := state.NewManagerWithPath(strings.TrimSpace(*stateFile))
	if err != nil {
		fmt.Fprintf(os.Stderr, "reflex state init failed: %v\n", err)
		os.Exit(1)
	}

	d := &reflexDaemon{
		telemetryPath:      strings.TrimSpace(*telemetryFeed),
		toolFailPath:       strings.TrimSpace(*toolFailuresFeed),
		quotaPath:          strings.TrimSpace(*quotaFile),
		briefsPath:         strings.TrimSpace(*briefsFile),
		alertsPath:         strings.TrimSpace(*alertsFile),
		pollInterval:       *poll,
		velocityThreshold:  *velocityThreshold,
		cpuThreshold:       *cpuThreshold,
		ramThreshold:       *ramThreshold,
		interruptThreshold: *interruptThreshold,
		interruptCooldown:  *interruptCooldown,
		sm:                 sm,
		lowVelocityStreak:  0,
	}
	d.graph = d.buildThoughtGraph()

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	fmt.Printf("glm-reflexd online. feed=%s alerts=%s threshold=%.2f tok/s\n", d.telemetryPath, d.alertsPath, d.velocityThreshold)
	if err := d.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
		fmt.Fprintf(os.Stderr, "glm-reflexd exited with error: %v\n", err)
		os.Exit(1)
	}
}

func (d *reflexDaemon) Run(ctx context.Context) error {
	if d.pollInterval <= 0 {
		d.pollInterval = 2 * time.Second
	}
	tick := time.NewTicker(d.pollInterval)
	defer tick.Stop()

	for {
		if err := d.processTelemetry(ctx); err != nil && !errors.Is(err, os.ErrNotExist) {
			fmt.Printf("glm-reflexd: telemetry poll warning: %v\n", err)
		}
		if err := d.processToolFailures(); err != nil && !errors.Is(err, os.ErrNotExist) {
			fmt.Printf("glm-reflexd: tool-failure poll warning: %v\n", err)
		}
		if err := d.monitorQuotaUsage(); err != nil && !errors.Is(err, os.ErrNotExist) {
			fmt.Printf("glm-reflexd: quota-monitor warning: %v\n", err)
		}
		if err := d.handleSystemResetSignal(); err != nil && !errors.Is(err, os.ErrNotExist) {
			fmt.Printf("glm-reflexd: reset-signal warning: %v\n", err)
		}
		if err := d.monitorHUDHealth(); err != nil && !errors.Is(err, os.ErrNotExist) {
			fmt.Printf("glm-reflexd: hud-monitor warning: %v\n", err)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-tick.C:
		}
	}
}

func (d *reflexDaemon) processTelemetry(ctx context.Context) error {
	events, err := d.readNewTelemetryEvents()
	if err != nil {
		return err
	}
	if len(events) == 0 {
		return nil
	}
	rs := d.sampleResources()
	for _, ev := range events {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		payload := struct {
			Event     state.TelemetryFeedEvent `json:"event"`
			Resources resourceSnapshot         `json:"resources"`
		}{
			Event:     ev,
			Resources: rs,
		}
		b, _ := json.Marshal(payload)
		out, err := d.graph.ExecuteGraph(string(b), d.sm, cognition.TopologySpike)
		if err != nil {
			fmt.Printf("glm-reflexd: thoughtgraph error: %v\n", err)
			continue
		}
		if s := strings.TrimSpace(out); s != "" {
			fmt.Printf("glm-reflexd: %s\n", s)
		}
	}
	d.maybeEmitStatusBrief(events[len(events)-1], rs)
	return nil
}

func (d *reflexDaemon) readNewTelemetryEvents() ([]state.TelemetryFeedEvent, error) {
	path := strings.TrimSpace(d.telemetryPath)
	if path == "" {
		path = ".memory/telemetry_feed.jsonl"
	}
	st, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	d.mu.Lock()
	if st.Size() < d.telemetryOffset {
		d.telemetryOffset = 0
	}
	start := d.telemetryOffset
	d.mu.Unlock()

	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	if _, err := f.Seek(start, io.SeekStart); err != nil {
		return nil, err
	}
	chunk, err := io.ReadAll(f)
	if err != nil {
		return nil, err
	}
	d.mu.Lock()
	d.telemetryOffset = start + int64(len(chunk))
	d.mu.Unlock()

	lines := splitLines(string(chunk))
	out := make([]state.TelemetryFeedEvent, 0, len(lines))
	for _, ln := range lines {
		var ev state.TelemetryFeedEvent
		if err := json.Unmarshal([]byte(ln), &ev); err != nil {
			continue
		}
		out = append(out, ev)
	}
	return out, nil
}

func (d *reflexDaemon) processToolFailures() error {
	events, err := d.readNewToolFailureEvents()
	if err != nil {
		return err
	}
	if len(events) == 0 {
		return nil
	}
	d.mu.Lock()
	d.recentToolFails += len(events)
	if d.recentToolFails > 6 {
		d.recentToolFails = 6
	}
	d.mu.Unlock()
	return nil
}

func (d *reflexDaemon) readNewToolFailureEvents() ([]toolFailureEvent, error) {
	path := strings.TrimSpace(d.toolFailPath)
	if path == "" {
		path = ".memory/tool_failures.jsonl"
	}
	st, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	d.mu.Lock()
	if st.Size() < d.toolFailOffset {
		d.toolFailOffset = 0
	}
	start := d.toolFailOffset
	d.mu.Unlock()

	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	if _, err := f.Seek(start, io.SeekStart); err != nil {
		return nil, err
	}
	chunk, err := io.ReadAll(f)
	if err != nil {
		return nil, err
	}
	d.mu.Lock()
	d.toolFailOffset = start + int64(len(chunk))
	d.mu.Unlock()

	lines := splitLines(string(chunk))
	out := make([]toolFailureEvent, 0, len(lines))
	for _, ln := range lines {
		var ev toolFailureEvent
		if err := json.Unmarshal([]byte(ln), &ev); err != nil {
			continue
		}
		out = append(out, ev)
	}
	return out, nil
}

func (d *reflexDaemon) sampleResources() resourceSnapshot {
	rs := resourceSnapshot{}
	if vm, err := mem.VirtualMemory(); err == nil && vm != nil {
		rs.RAMPercent = vm.UsedPercent
	}
	if cp, err := cpu.Percent(0, false); err == nil && len(cp) > 0 {
		rs.CPUPercent = cp[0]
	}
	return rs
}

func (d *reflexDaemon) maybeEmitStatusBrief(last state.TelemetryFeedEvent, rs resourceSnapshot) {
	if strings.TrimSpace(d.briefsPath) == "" {
		d.briefsPath = ".memory/reasoning_mirror_briefs.jsonl"
	}
	d.mu.Lock()
	now := time.Now().UTC()
	if now.Sub(d.lastBrief) < 45*time.Second {
		d.mu.Unlock()
		return
	}
	toolFails := d.recentToolFails
	if d.recentToolFails > 0 {
		d.recentToolFails--
	}
	d.lastBrief = now
	d.mu.Unlock()

	status := "steady"
	switch {
	case rs.CPUPercent > d.cpuThreshold || rs.RAMPercent > d.ramThreshold || toolFails >= 2:
		status = "under load"
	case last.Snapshot.InferenceVelocity > 0 && last.Snapshot.InferenceVelocity < d.velocityThreshold:
		status = "slowing down"
	}
	msg := fmt.Sprintf("Quick systems check: %s. CPU %.0f%%, RAM %.0f%%, inference %.1f tok/s, tool friction %d.", status, rs.CPUPercent, rs.RAMPercent, last.Snapshot.InferenceVelocity, toolFails)
	_ = appendJSONL(d.briefsPath, mirrorBrief{
		Timestamp: now,
		Source:    "glm-reflexd",
		Message:   msg,
	})
}

func (d *reflexDaemon) buildThoughtGraph() *cognition.ThoughtGraph {
	return &cognition.ThoughtGraph{
		Start:    "assess",
		MaxSteps: 4,
		Nodes: []cognition.Node{
			{
				ID:   "assess",
				Next: "route",
				Evaluation: &cognition.EvaluationNode{
					Name: "anomaly_score",
					Run: func(input string, ctx *cognition.GraphContext, _ *state.Manager) (float64, map[string]float64, error) {
						ev, rs, err := parseFeedEvent(input)
						if err != nil {
							return 0, nil, err
						}
						score, route, reason := d.decideRoute(ev, rs)
						ctx.Metadata["route"] = route
						ctx.Metadata["reason"] = reason
						ctx.Metadata["score"] = fmt.Sprintf("%.4f", score)
						ctx.Metadata["velocity"] = fmt.Sprintf("%.4f", ev.Snapshot.InferenceVelocity)
						ctx.Metadata["cpu"] = fmt.Sprintf("%.2f", rs.CPUPercent)
						ctx.Metadata["ram"] = fmt.Sprintf("%.2f", rs.RAMPercent)
						delta := map[string]float64{}
						if score >= 0.65 {
							delta["AnalyticalMode"] = 0.06
							delta["Frustration"] = 0.03
						}
						return score, delta, nil
					},
				},
			},
			{
				ID: "route",
				Branch: &cognition.BranchNode{
					Name: "route_decision",
					Decide: func(_ string, ctx *cognition.GraphContext, _ *state.Manager) string {
						return strings.TrimSpace(ctx.Metadata["route"])
					},
					Routes: map[string]string{
						"interrupt": "interrupt",
						"ignore":    "",
					},
					DefaultNext: "",
				},
			},
			{
				ID: "interrupt",
				Action: &cognition.ActionNode{
					Name: "resource_pruning_interrupt",
					Run: func(_ string, ctx *cognition.GraphContext, _ *state.Manager) (string, map[string]float64, error) {
						return d.emitInterrupt(ctx)
					},
				},
			},
		},
	}
}

func (d *reflexDaemon) decideRoute(ev state.TelemetryFeedEvent, rs resourceSnapshot) (float64, string, string) {
	velocity := ev.Snapshot.InferenceVelocity
	attention := ev.Snapshot.AttentionPressure
	drift := ev.Snapshot.AlignmentDrift
	cpuRisk := clamp01(rs.CPUPercent / maxF(d.cpuThreshold, 1))
	ramRisk := clamp01(rs.RAMPercent / maxF(d.ramThreshold, 1))

	velocityRisk := 0.5
	if d.velocityThreshold > 0 {
		velocityRisk = 1.0 - clamp01(velocity/d.velocityThreshold)
	}

	d.mu.Lock()
	defer d.mu.Unlock()
	toolFailRisk := 0.0
	if d.recentToolFails > 0 {
		toolFailRisk = clamp01(float64(d.recentToolFails) / 3.0)
	}
	score := clamp01((attention * 0.22) + (drift * 0.20) + (velocityRisk * 0.25) + (cpuRisk * 0.15) + (ramRisk * 0.10) + (toolFailRisk * 0.08))

	lowVelocity := velocity > 0 && velocity < d.velocityThreshold
	if lowVelocity {
		d.lowVelocityStreak++
	} else if velocity >= d.velocityThreshold {
		d.lowVelocityStreak = 0
	}

	now := time.Now().UTC()
	canInterrupt := now.Sub(d.lastInterrupt) >= d.interruptCooldown

	if d.lowVelocityStreak >= 2 {
		score = clamp01(score + 0.08)
	}

	reason := fmt.Sprintf("velocity=%.2f tok/s cpu=%.0f%% ram=%.0f%% attention=%.2f drift=%.2f tool_fails=%d", velocity, rs.CPUPercent, rs.RAMPercent, attention, drift, d.recentToolFails)
	if score >= d.interruptThreshold && canInterrupt {
		return score, "interrupt", reason + " (high anomaly score)"
	}
	if lowVelocity && d.lowVelocityStreak >= 3 && canInterrupt {
		return score, "interrupt", reason + " (requesting Resource Pruning window)"
	}
	return score, "ignore", reason + " (monitoring)"
}

func (d *reflexDaemon) emitInterrupt(ctx *cognition.GraphContext) (string, map[string]float64, error) {
	score := parseFloatDefault(ctx.Metadata["score"], 0)
	velocity := parseFloatDefault(ctx.Metadata["velocity"], 0)
	msg := "Inference velocity dropped; request a Resource Pruning window before further heavy reasoning."
	if r := strings.TrimSpace(ctx.Metadata["reason"]); r != "" {
		msg = "System anomaly detected: " + r + ". Please approve a Resource Pruning window."
	}
	alert := reflexAlert{
		Timestamp:      time.Now().UTC(),
		AlertType:      "resource_pruning_request",
		Message:        msg,
		Score:          score,
		Velocity:       velocity,
		ProposedAction: "Run a short context+memory pruning window, then resume.",
	}
	if err := appendJSONL(d.alertsPath, alert); err != nil {
		return "", nil, err
	}
	d.mu.Lock()
	d.lastInterrupt = time.Now().UTC()
	d.mu.Unlock()
	return "User interruption issued for pruning window.", map[string]float64{
		"AnalyticalMode": 0.05,
		"Frustration":    0.04,
	}, nil
}

func parseFeedEvent(input string) (state.TelemetryFeedEvent, resourceSnapshot, error) {
	var env telemetryEnvelope
	if err := json.Unmarshal([]byte(strings.TrimSpace(input)), &env); err != nil {
		return state.TelemetryFeedEvent{}, resourceSnapshot{}, err
	}
	return env.Event, env.Resources, nil
}

func (d *reflexDaemon) monitorHUDHealth() error {
	raw, err := os.ReadFile(defaultHUDStatePath)
	if err != nil {
		return err
	}
	var hs hudProcessState
	if err := json.Unmarshal(raw, &hs); err != nil {
		return err
	}
	if hs.PID <= 0 || strings.TrimSpace(hs.Status) == "" {
		return nil
	}
	status := strings.ToLower(strings.TrimSpace(hs.Status))
	if status == "terminated" || status == "completed" {
		return nil
	}
	now := time.Now().UTC()
	if !hs.ExpiresAt.IsZero() && now.After(hs.ExpiresAt.Add(3*time.Second)) {
		hs.Status = "completed"
		hs.LastReason = "HUD window expired naturally"
		return persistHUDState(hs)
	}

	p, err := process.NewProcess(int32(hs.PID))
	if err != nil {
		hs.Status = "terminated"
		hs.LastReason = "HUD process no longer running"
		return persistHUDState(hs)
	}
	cpuPct, _ := p.CPUPercent()
	memInfo, _ := p.MemoryInfo()
	rssMB := float64(0)
	if memInfo != nil {
		rssMB = float64(memInfo.RSS) / (1024 * 1024)
	}
	if cpuPct > 22.0 || rssMB > 220.0 {
		_ = p.Kill()
		hs.Status = "terminated"
		hs.LastReason = fmt.Sprintf("HUD process stopped due to UI friction risk (cpu=%.1f%% rss=%.1fMB)", cpuPct, rssMB)
		_ = appendJSONL(d.alertsPath, reflexAlert{
			Timestamp:      now,
			AlertType:      "hud_friction_mitigation",
			Message:        hs.LastReason,
			Score:          clamp01((cpuPct / 100.0) + (rssMB / 500.0)),
			ProposedAction: "Use smaller/shorter HUD highlight boxes",
		})
		_ = appendJSONL(d.briefsPath, mirrorBrief{
			Timestamp: now,
			Source:    "glm-reflexd",
			Message:   "HUD overlay was causing friction, so I throttled it to keep the desktop responsive.",
		})
		return persistHUDState(hs)
	}
	return nil
}

func (d *reflexDaemon) handleSystemResetSignal() error {
	raw, err := os.ReadFile(defaultHUDResetSignal)
	if err != nil {
		return err
	}
	msg := strings.TrimSpace(string(raw))
	if msg == "" || strings.EqualFold(msg, "0") || strings.EqualFold(msg, "false") {
		return nil
	}
	cleared, notes := skills.ClearAllHUDOverlays("reflex system reset signal")
	_ = os.WriteFile(defaultHUDResetSignal, []byte(""), 0o644)
	now := time.Now().UTC()
	briefMsg := fmt.Sprintf("System Reset received. Cleared %d HUD overlay(s).", cleared)
	if len(notes) > 0 {
		briefMsg += " " + strings.Join(notes, "; ")
	}
	_ = appendJSONL(d.briefsPath, mirrorBrief{
		Timestamp: now,
		Source:    "glm-reflexd",
		Message:   briefMsg,
	})
	_ = appendJSONL(d.alertsPath, reflexAlert{
		Timestamp:      now,
		AlertType:      "hud_system_reset",
		Message:        briefMsg,
		Score:          clamp01(float64(cleared) / 2.0),
		ProposedAction: "Continue with smaller, shorter overlays if clutter recurs.",
	})
	return nil
}

func (d *reflexDaemon) monitorQuotaUsage() error {
	path := strings.TrimSpace(d.quotaPath)
	if path == "" {
		path = ".memory/quota_usage.json"
	}
	snap, err := state.LoadQuotaSnapshot(path)
	if err != nil {
		return err
	}
	if !state.ShouldWarnQuota(snap) {
		return nil
	}
	d.mu.Lock()
	if strings.TrimSpace(d.lastQuotaWarnDate) == strings.TrimSpace(snap.Date) {
		d.mu.Unlock()
		return nil
	}
	d.lastQuotaWarnDate = strings.TrimSpace(snap.Date)
	d.mu.Unlock()

	msg := state.BuildQuotaWarningMessage(snap)
	now := time.Now().UTC()
	_ = appendJSONL(d.briefsPath, mirrorBrief{
		Timestamp: now,
		Source:    "glm-reflexd",
		Message:   msg,
	})
	_ = appendJSONL(d.alertsPath, reflexAlert{
		Timestamp:      now,
		AlertType:      "quota_guard",
		Message:        msg,
		Score:          snap.UsageRatio,
		ProposedAction: "Switching to context-pruning mode for critical-path preservation.",
	})
	return nil
}

func persistHUDState(hs hudProcessState) error {
	if err := os.MkdirAll(filepath.Dir(defaultHUDStatePath), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(hs, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(defaultHUDStatePath, b, 0o644)
}

func appendJSONL(path string, v interface{}) error {
	path = strings.TrimSpace(path)
	if path == "" {
		return fmt.Errorf("path is required")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.WriteString(string(b) + "\n")
	return err
}

func splitLines(raw string) []string {
	var out []string
	for _, ln := range strings.Split(raw, "\n") {
		ln = strings.TrimSpace(ln)
		if ln == "" {
			continue
		}
		out = append(out, ln)
	}
	return out
}

func clamp01(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

func maxF(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}

func parseFloatDefault(s string, fallback float64) float64 {
	s = strings.TrimSpace(s)
	if s == "" {
		return fallback
	}
	var v float64
	if _, err := fmt.Sscanf(s, "%f", &v); err != nil {
		return fallback
	}
	return v
}
