package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/Thynaptic/P-LMv1/pkg/cognition"
	"github.com/Thynaptic/P-LMv1/pkg/envload"
)

const (
	defaultPlannerInboxDir    = ".memory/planner/inbox"
	defaultPlannerStatePath   = ".memory/planner/state.json"
	defaultPlannerRoadmapDir  = ".memory/planner/roadmaps"
	defaultPlannerPromptsPath = ".memory/planner/prompts_ready.md"
	defaultMirrorBriefsPath   = ".memory/reasoning_mirror_briefs.jsonl"
	defaultPlannerTick        = 3 * time.Second
)

type mirrorBrief struct {
	Timestamp time.Time `json:"timestamp"`
	Source    string    `json:"source,omitempty"`
	Message   string    `json:"message"`
}

type trackedFile struct {
	Path    string `json:"path"`
	ModUnix int64  `json:"mod_unix"`
	Size    int64  `json:"size"`
}

type plannerState struct {
	LastScanAt     time.Time              `json:"last_scan_at"`
	Primed         bool                   `json:"primed"`
	Seen           map[string]trackedFile `json:"seen"`
	ProcessedSeeds int                    `json:"processed_seeds"`
}

type conceptSeed struct {
	ID        string    `json:"id,omitempty"`
	Idea      string    `json:"idea,omitempty"`
	Approved  bool      `json:"approved,omitempty"`
	Priority  string    `json:"priority,omitempty"`
	CreatedAt time.Time `json:"created_at,omitempty"`
}

type plannerDaemon struct {
	inboxDir    string
	statePath   string
	roadmapDir  string
	promptsPath string
	mirrorPath  string
	tick        time.Duration

	mu    sync.Mutex
	state plannerState
}

func main() {
	if err := envload.Autoload(); err != nil {
		fmt.Fprintf(os.Stderr, "warning: env autoload failed: %v\n", err)
	}

	var (
		inboxDir    = flag.String("inbox", defaultPlannerInboxDir, "planner concept seed inbox directory")
		statePath   = flag.String("state-path", defaultPlannerStatePath, "planner daemon state path")
		roadmapDir  = flag.String("roadmap-dir", defaultPlannerRoadmapDir, "roadmap output directory")
		promptsPath = flag.String("prompts-path", defaultPlannerPromptsPath, "handover prompts output file")
		mirrorPath  = flag.String("mirror-briefs", defaultMirrorBriefsPath, "reasoning mirror briefs JSONL path")
		tick        = flag.Duration("tick", defaultPlannerTick, "poll interval")
	)
	flag.Parse()

	d := &plannerDaemon{
		inboxDir:    strings.TrimSpace(*inboxDir),
		statePath:   strings.TrimSpace(*statePath),
		roadmapDir:  strings.TrimSpace(*roadmapDir),
		promptsPath: strings.TrimSpace(*promptsPath),
		mirrorPath:  strings.TrimSpace(*mirrorPath),
		tick:        *tick,
	}
	if d.inboxDir == "" {
		d.inboxDir = defaultPlannerInboxDir
	}
	if d.statePath == "" {
		d.statePath = defaultPlannerStatePath
	}
	if d.roadmapDir == "" {
		d.roadmapDir = defaultPlannerRoadmapDir
	}
	if d.promptsPath == "" {
		d.promptsPath = defaultPlannerPromptsPath
	}
	if d.mirrorPath == "" {
		d.mirrorPath = defaultMirrorBriefsPath
	}
	if d.tick <= 0 {
		d.tick = defaultPlannerTick
	}
	if err := os.MkdirAll(d.inboxDir, 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "planner inbox init failed: %v\n", err)
		os.Exit(1)
	}
	if err := os.MkdirAll(d.roadmapDir, 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "planner roadmap dir init failed: %v\n", err)
		os.Exit(1)
	}
	if err := d.loadState(); err != nil {
		fmt.Fprintf(os.Stderr, "planner state load warning: %v\n", err)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	fmt.Printf("glm-plannerd online. inbox=%s prompts=%s\n", d.inboxDir, d.promptsPath)
	if err := d.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
		fmt.Fprintf(os.Stderr, "glm-plannerd exited with error: %v\n", err)
		os.Exit(1)
	}
}

func (d *plannerDaemon) Run(ctx context.Context) error {
	ticker := time.NewTicker(d.tick)
	defer ticker.Stop()

	for {
		if err := d.scanOnce(); err != nil {
			fmt.Printf("glm-plannerd: scan warning: %v\n", err)
		}
		select {
		case <-ctx.Done():
			_ = d.persistState()
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func (d *plannerDaemon) scanOnce() error {
	paths, err := d.collectChangedSeeds()
	if err != nil {
		return err
	}

	d.mu.Lock()
	primed := d.state.Primed
	if !d.state.Primed {
		d.state.Primed = true
	}
	d.state.LastScanAt = time.Now().UTC()
	d.mu.Unlock()

	// Prime baseline on first scan, then process changes.
	if !primed {
		return d.persistState()
	}

	for _, path := range paths {
		if err := d.processSeedPath(path); err != nil {
			fmt.Printf("glm-plannerd: seed %s warning: %v\n", path, err)
		}
	}
	return d.persistState()
}

func (d *plannerDaemon) collectChangedSeeds() ([]string, error) {
	if d.state.Seen == nil {
		d.state.Seen = map[string]trackedFile{}
	}
	var changed []string
	entries, err := os.ReadDir(d.inboxDir)
	if err != nil {
		return nil, err
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := strings.ToLower(strings.TrimSpace(e.Name()))
		if !strings.HasSuffix(name, ".json") && !strings.HasSuffix(name, ".md") && !strings.HasSuffix(name, ".txt") {
			continue
		}
		path := filepath.Join(d.inboxDir, e.Name())
		info, err := e.Info()
		if err != nil {
			continue
		}
		tf := trackedFile{Path: path, ModUnix: info.ModTime().Unix(), Size: info.Size()}
		prev, ok := d.state.Seen[path]
		if !ok || prev.ModUnix != tf.ModUnix || prev.Size != tf.Size {
			changed = append(changed, path)
		}
		d.state.Seen[path] = tf
	}
	sort.Strings(changed)
	return changed, nil
}

func (d *plannerDaemon) processSeedPath(path string) error {
	seed, err := parseConceptSeed(path)
	if err != nil {
		return err
	}
	if strings.TrimSpace(seed.Idea) == "" {
		return nil
	}
	if strings.TrimSpace(seed.ID) == "" {
		seed.ID = inferSeedID(path)
	}
	if seed.CreatedAt.IsZero() {
		seed.CreatedAt = time.Now().UTC()
	}

	if question, need := clarificationForIdea(seed.Idea); need {
		msg := fmt.Sprintf("Sir, for the '%s' idea, %s", strings.TrimSpace(seed.Idea), question)
		_ = appendMirrorBrief(d.mirrorPath, mirrorBrief{
			Timestamp: time.Now().UTC(),
			Source:    "glm-plannerd",
			Message:   msg,
		})
		return nil
	}

	roadmap := cognition.GenerateHierarchicalRoadmap(seed.Idea)
	roadmapPath := filepath.Join(d.roadmapDir, sanitizeSlug(seed.ID)+".json")
	if _, err := cognition.PersistRoadmap(roadmap, roadmapPath); err != nil {
		return err
	}

	if !seed.Approved {
		_ = appendMirrorBrief(d.mirrorPath, mirrorBrief{
			Timestamp: time.Now().UTC(),
			Source:    "glm-plannerd",
			Message:   fmt.Sprintf("Roadmap draft ready for '%s'. Approve this concept seed to generate execution prompts.", strings.TrimSpace(seed.Idea)),
		})
		return nil
	}

	if err := writePromptsReady(d.promptsPath, seed, roadmap, roadmapPath); err != nil {
		return err
	}
	d.mu.Lock()
	d.state.ProcessedSeeds++
	d.mu.Unlock()
	_ = appendMirrorBrief(d.mirrorPath, mirrorBrief{
		Timestamp: time.Now().UTC(),
		Source:    "glm-plannerd",
		Message:   fmt.Sprintf("Roadmap approved. Handover prompts are ready at %s.", strings.TrimSpace(d.promptsPath)),
	})
	return nil
}

func parseConceptSeed(path string) (conceptSeed, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return conceptSeed{}, err
	}
	text := strings.TrimSpace(string(raw))
	if text == "" {
		return conceptSeed{}, nil
	}
	ext := strings.ToLower(filepath.Ext(path))
	if ext == ".json" {
		var seed conceptSeed
		if err := json.Unmarshal(raw, &seed); err == nil {
			if strings.TrimSpace(seed.Idea) == "" {
				seed.Idea = strings.TrimSpace(seed.Priority)
			}
			return seed, nil
		}
	}
	return conceptSeed{
		ID:       inferSeedID(path),
		Idea:     firstNonEmptyLine(text),
		Approved: strings.Contains(strings.ToLower(text), "approved: true"),
	}, nil
}

func clarificationForIdea(idea string) (string, bool) {
	v := strings.ToLower(strings.TrimSpace(idea))
	if v == "" {
		return "could you clarify the objective and target outcome first?", true
	}
	if strings.Contains(v, "developer hud") && !containsAny(v, "terminal", "overlay", "watcher", "editor") {
		return "should I prioritize the terminal watcher or the editor overlay first?", true
	}
	vagueTokens := []string{
		"thing", "stuff", "do it", "fix it", "something", "improve it", "make it better", "whatever",
	}
	for _, t := range vagueTokens {
		if strings.Contains(v, t) {
			return "which concrete subsystem should be first: infrastructure, logic, perception, or interface?", true
		}
	}
	if len(strings.Fields(v)) < 4 {
		return "which concrete subsystem should be first: infrastructure, logic, perception, or interface?", true
	}
	return "", false
}

func writePromptsReady(path string, seed conceptSeed, roadmap cognition.Roadmap, roadmapPath string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	var b strings.Builder
	b.WriteString("\n## Planner Handover ")
	b.WriteString(time.Now().UTC().Format(time.RFC3339))
	b.WriteString("\n")
	b.WriteString("- Seed ID: " + strings.TrimSpace(seed.ID) + "\n")
	b.WriteString("- Big Idea: " + strings.TrimSpace(seed.Idea) + "\n")
	b.WriteString("- Objective: " + strings.TrimSpace(roadmap.Objective) + "\n")
	b.WriteString("- Roadmap JSON: `" + strings.TrimSpace(roadmapPath) + "`\n")
	b.WriteString("\n### Milestones\n")
	for _, m := range roadmap.Milestones {
		b.WriteString("- [" + strings.TrimSpace(m.Phase) + "] " + strings.TrimSpace(m.Title) + "\n")
	}
	b.WriteString("\n### Work Orders (Prompts)\n")
	for i, wo := range roadmap.WorkOrders {
		b.WriteString(fmt.Sprintf("%d. **%s / %s**\n", i+1, strings.TrimSpace(wo.Phase), strings.TrimSpace(wo.Owner)))
		b.WriteString("   Prompt: " + strings.TrimSpace(wo.Prompt) + "\n")
		if len(wo.SuccessCriteria) > 0 {
			b.WriteString("   Success:\n")
			for _, s := range wo.SuccessCriteria {
				b.WriteString("   - " + strings.TrimSpace(s) + "\n")
			}
		}
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.WriteString(strings.TrimSpace(b.String()) + "\n")
	return err
}

func (d *plannerDaemon) loadState() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	raw, err := os.ReadFile(d.statePath)
	if err != nil {
		if os.IsNotExist(err) {
			d.state = plannerState{Seen: map[string]trackedFile{}}
			return nil
		}
		return err
	}
	var s plannerState
	if err := json.Unmarshal(raw, &s); err != nil {
		return err
	}
	if s.Seen == nil {
		s.Seen = map[string]trackedFile{}
	}
	d.state = s
	return nil
}

func (d *plannerDaemon) persistState() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.state.Seen == nil {
		d.state.Seen = map[string]trackedFile{}
	}
	if err := os.MkdirAll(filepath.Dir(d.statePath), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(d.state, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(d.statePath, b, 0o644)
}

func appendMirrorBrief(path string, brief mirrorBrief) error {
	if strings.TrimSpace(brief.Message) == "" {
		return nil
	}
	if brief.Timestamp.IsZero() {
		brief.Timestamp = time.Now().UTC()
	}
	if strings.TrimSpace(brief.Source) == "" {
		brief.Source = "glm-plannerd"
	}
	return appendJSONL(path, mustJSON(brief))
}

func appendJSONL(path, line string) error {
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

func mustJSON(v interface{}) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func inferSeedID(path string) string {
	base := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	base = sanitizeSlug(base)
	if base == "" {
		base = fmt.Sprintf("seed_%d", time.Now().UnixNano())
	}
	return base
}

func sanitizeSlug(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" {
		return ""
	}
	s = strings.ReplaceAll(s, " ", "_")
	var out []rune
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' || r == '-' || r == '.' {
			out = append(out, r)
		}
	}
	return strings.Trim(strings.TrimSpace(string(out)), "._-")
}

func firstNonEmptyLine(s string) string {
	for _, ln := range strings.Split(s, "\n") {
		ln = strings.TrimSpace(ln)
		if ln != "" {
			return ln
		}
	}
	return ""
}

func containsAny(s string, tokens ...string) bool {
	for _, t := range tokens {
		if strings.Contains(s, strings.ToLower(strings.TrimSpace(t))) {
			return true
		}
	}
	return false
}
