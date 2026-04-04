package cognition

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Thynaptic/P-LMv1/pkg/memory"
	"github.com/Thynaptic/P-LMv1/pkg/state"
	"github.com/ollama/ollama/api"
)

const (
	defaultIdleAfter        = 90 * time.Second
	defaultDeliberationTick = 20 * time.Second
	defaultDeliberationLog  = ".memory/deliberation_log.json"
)

type Deliberator struct {
	mm *memory.MemoryManager
	sm *state.Manager
	sc *SkillCompiler

	IdleAfter time.Duration
	Tick      time.Duration
	LogPath   string

	mu           sync.Mutex
	lastActivity time.Time
	running      bool
	stopCh       chan struct{}
	wg           sync.WaitGroup
}

type deliberationLogEntry struct {
	Timestamp     time.Time `json:"timestamp"`
	ClusterID     string    `json:"cluster_id"`
	ClusterLabel  string    `json:"cluster_label"`
	WeakScore     float64   `json:"weak_score"`
	UnifiedTheory string    `json:"unified_theory"`
	SkillPromoted string    `json:"skill_promoted,omitempty"`
}

type weakCluster struct {
	ID            string
	Label         string
	AvgImportance float64
	Contradiction float64
	WeakScore     float64
	Segments      []memory.KnowledgeSegment
}

// NewDeliberator creates a low-priority background deliberator.
func NewDeliberator(mm *memory.MemoryManager, sm *state.Manager) *Deliberator {
	return &Deliberator{
		mm:           mm,
		sm:           sm,
		sc:           NewSkillCompiler(),
		IdleAfter:    defaultIdleAfter,
		Tick:         defaultDeliberationTick,
		LogPath:      defaultDeliberationLog,
		lastActivity: time.Now().UTC(),
	}
}

// Start begins background ouroboros loop.
func (d *Deliberator) Start() {
	if d == nil || d.mm == nil {
		return
	}
	d.mu.Lock()
	if d.running {
		d.mu.Unlock()
		return
	}
	d.running = true
	d.stopCh = make(chan struct{})
	d.mu.Unlock()

	d.wg.Add(1)
	go d.loop()
}

// Stop stops background processing.
func (d *Deliberator) Stop() {
	if d == nil {
		return
	}
	d.mu.Lock()
	if !d.running {
		d.mu.Unlock()
		return
	}
	close(d.stopCh)
	d.running = false
	d.mu.Unlock()
	d.wg.Wait()
}

// NotifyActivity resets idle timer.
func (d *Deliberator) NotifyActivity() {
	if d == nil {
		return
	}
	d.mu.Lock()
	d.lastActivity = time.Now().UTC()
	d.mu.Unlock()
}

func (d *Deliberator) loop() {
	defer d.wg.Done()
	tick := d.Tick
	if tick <= 0 {
		tick = defaultDeliberationTick
	}
	idleAfter := d.IdleAfter
	if idleAfter <= 0 {
		idleAfter = defaultIdleAfter
	}

	t := time.NewTicker(tick)
	defer t.Stop()

	for {
		select {
		case <-d.stopCh:
			return
		case <-t.C:
			if !d.isIdle(idleAfter) {
				continue
			}
			_ = d.runOnce()
			d.NotifyActivity()
		}
	}
}

func (d *Deliberator) isIdle(idleAfter time.Duration) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return time.Since(d.lastActivity) >= idleAfter
}

func (d *Deliberator) runOnce() error {
	if d.mm == nil {
		return nil
	}
	count := d.mm.KnowledgeCount()
	if count < 8 {
		return nil
	}
	k := count
	if k > 220 {
		k = 220
	}
	segs, err := d.mm.RetrieveKnowledgeSegments(" ", k)
	if err != nil || len(segs) < 6 {
		return err
	}

	cluster, ok := pickWeakCluster(segs)
	if !ok || cluster.WeakScore < 0.42 || len(cluster.Segments) < 3 {
		return nil
	}

	theory := d.selfRefineTheory(cluster)
	if strings.TrimSpace(theory) == "" {
		return nil
	}
	// Conflict Arbiter: when weak cluster has contradiction, run epistemic audit before storing theory.
	if cluster.Contradiction >= 0.55 {
		_, _ = TriggerEpistemicAudit("deliberation:"+cluster.Label, theory, cluster.Segments, d.mm)
	}

	meta := map[string]string{
		"type":            "knowledge",
		"source_type":     "deliberation",
		"cluster_id":      cluster.ID,
		"cluster_label":   cluster.Label,
		"base_importance": "0.62",
	}
	_ = d.mm.AddKnowledge(theory, meta)

	skillPath := d.maybePromoteSkill(cluster, theory)
	_ = d.appendLog(deliberationLogEntry{
		Timestamp:     time.Now().UTC(),
		ClusterID:     cluster.ID,
		ClusterLabel:  cluster.Label,
		WeakScore:     cluster.WeakScore,
		UnifiedTheory: truncateDelib(theory, 420),
		SkillPromoted: skillPath,
	})

	if d.sm != nil {
		d.sm.UpdateDelta(map[string]float64{
			"AnalyticalMode": 0.04,
			"Confidence":     0.02,
		})
		_ = d.sm.Save()
	}
	return nil
}

func pickWeakCluster(segs []memory.KnowledgeSegment) (weakCluster, bool) {
	group := map[string][]memory.KnowledgeSegment{}
	label := map[string]string{}
	for _, s := range segs {
		cid := strings.TrimSpace(s.Metadata["cluster_id"])
		if cid == "" {
			cid = "unclustered"
		}
		group[cid] = append(group[cid], s)
		if l := strings.TrimSpace(s.Metadata["cluster_label"]); l != "" {
			label[cid] = l
		}
	}
	var best weakCluster
	found := false
	for cid, arr := range group {
		if len(arr) < 3 {
			continue
		}
		avgImp := 0.0
		for _, s := range arr {
			avgImp += parseFloatDefault(s.Metadata["base_importance"], 0.5)
		}
		avgImp /= float64(len(arr))
		ctr := contradictionDensity(arr)
		weak := ((1.0 - avgImp) * 0.55) + (ctr * 0.45)
		c := weakCluster{
			ID:            cid,
			Label:         labelOrFallback(label[cid], cid),
			AvgImportance: avgImp,
			Contradiction: ctr,
			WeakScore:     weak,
			Segments:      arr,
		}
		if !found || c.WeakScore > best.WeakScore {
			best = c
			found = true
		}
	}
	return best, found
}

func contradictionDensity(arr []memory.KnowledgeSegment) float64 {
	if len(arr) < 2 {
		return 0
	}
	// Sample only top few pairs for low-priority operation.
	maxPairs := 6
	sum := 0.0
	pairs := 0
	for i := 0; i < len(arr); i++ {
		for j := i + 1; j < len(arr); j++ {
			sum += DetectContradiction(arr[i].Content, arr[j].Content)
			pairs++
			if pairs >= maxPairs {
				return clamp01Delib(sum / float64(pairs))
			}
		}
	}
	return clamp01Delib(sum / float64(maxIntDelib(pairs, 1)))
}

func (d *Deliberator) selfRefineTheory(cluster weakCluster) string {
	base := heuristicTheory(cluster)
	refined := base
	for i := 0; i < 2; i++ {
		if out, ok := llmRefineTheory(cluster, refined); ok {
			refined = out
		}
	}
	return strings.TrimSpace(refined)
}

func heuristicTheory(cluster weakCluster) string {
	lines := []string{}
	for _, s := range cluster.Segments {
		c := strings.TrimSpace(s.Content)
		if c == "" {
			continue
		}
		lines = append(lines, truncateDelib(c, 140))
		if len(lines) >= 6 {
			break
		}
	}
	summary := "Unified Theory: Across repeated sessions in cluster '" + cluster.Label + "', inconsistencies suggest a recurring root cause pattern. Consolidate into a durable troubleshooting guide and automate the repeated fix path."
	if len(lines) > 0 {
		summary += "\nEvidence:\n- " + strings.Join(lines, "\n- ")
	}
	return summary
}

func llmRefineTheory(cluster weakCluster, current string) (string, bool) {
	client, err := api.ClientFromEnvironment()
	if err != nil {
		return "", false
	}
	system := `You are an internal deliberation engine.
Produce a concise "Unified Theory" for a weak knowledge cluster.
Focus on recurring root-cause patterns and permanent remediation guidance.
Return plain text only.`
	user := "Cluster: " + cluster.Label +
		fmt.Sprintf("\nWeak score: %.2f", cluster.WeakScore) +
		"\nCurrent theory:\n" + current
	opts, _ := state.ResolveEntropyOptions(user)
	req := &api.ChatRequest{
		Model:   "llama3.2:1b",
		Options: opts,
		Messages: []api.Message{
			{Role: "system", Content: system},
			{Role: "user", Content: user},
		},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	var out strings.Builder
	if err := client.Chat(ctx, req, func(resp api.ChatResponse) error {
		out.WriteString(resp.Message.Content)
		return nil
	}); err != nil {
		return "", false
	}
	res := strings.TrimSpace(out.String())
	return res, res != ""
}

func (d *Deliberator) maybePromoteSkill(cluster weakCluster, theory string) string {
	if d.sc == nil {
		return ""
	}
	process := detectRepeatedManualProcess(cluster.Segments)
	if process == "" {
		return ""
	}
	gap := CapabilityGap{
		Detected:    true,
		Description: "automate repeated process: " + process,
	}
	skill, err := d.sc.CompileSkillPrimitive(gap, theory)
	if err != nil {
		return ""
	}
	_ = d.mm.AddKnowledge(
		"Deliberation promoted automation skill for repeated process '"+process+"': "+skill.RootDir,
		map[string]string{
			"type":            "knowledge",
			"source_type":     "deliberation_skill_promotion",
			"cluster_id":      cluster.ID,
			"cluster_label":   cluster.Label,
			"base_importance": "0.70",
		},
	)
	return skill.RootDir
}

func detectRepeatedManualProcess(segs []memory.KnowledgeSegment) string {
	counter := map[string]int{}
	for _, s := range segs {
		lc := strings.ToLower(s.Content)
		for _, marker := range []string{
			"manually restart", "manual restart",
			"run tests", "verify backups", "restart service",
			"check logs", "open firewall", "update config",
		} {
			if strings.Contains(lc, marker) {
				counter[marker]++
			}
		}
	}
	type kv struct {
		K string
		V int
	}
	var arr []kv
	for k, v := range counter {
		arr = append(arr, kv{k, v})
	}
	sort.Slice(arr, func(i, j int) bool { return arr[i].V > arr[j].V })
	if len(arr) == 0 || arr[0].V < 2 {
		return ""
	}
	return arr[0].K
}

func (d *Deliberator) appendLog(entry deliberationLogEntry) error {
	path := d.LogPath
	if strings.TrimSpace(path) == "" {
		path = defaultDeliberationLog
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}

	var logs []deliberationLogEntry
	if b, err := os.ReadFile(path); err == nil && len(b) > 0 {
		_ = json.Unmarshal(b, &logs)
	}
	logs = append(logs, entry)
	if len(logs) > 1200 {
		logs = logs[len(logs)-1200:]
	}
	data, err := json.MarshalIndent(logs, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

func labelOrFallback(label string, cid string) string {
	label = strings.TrimSpace(label)
	if label != "" {
		return label
	}
	return cid
}

func parseFloatDefault(v string, fallback float64) float64 {
	v = strings.TrimSpace(v)
	if v == "" {
		return fallback
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil {
		return fallback
	}
	return clamp01Delib(f)
}

func clamp01Delib(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

func truncateDelib(s string, n int) string {
	s = strings.TrimSpace(strings.ReplaceAll(s, "\n", " "))
	if len(s) <= n {
		return s
	}
	if n < 4 {
		return s[:n]
	}
	return s[:n-3] + "..."
}

func maxIntDelib(a, b int) int {
	if a > b {
		return a
	}
	return b
}
