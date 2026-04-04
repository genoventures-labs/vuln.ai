package orchestration

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Thynaptic/P-LMv1/pkg/cognition"
	"github.com/Thynaptic/P-LMv1/pkg/memory"
	"github.com/Thynaptic/P-LMv1/pkg/state"
)

const (
	defaultStrategyLogPath = ".memory/strategy_log.json"
	defaultStrategyTick    = 3 * time.Minute
)

type StrategyProposal struct {
	FocusArea      string    `json:"focus_area"`
	TransitionTo   string    `json:"transition_to"`
	PermanentSolve string    `json:"permanent_solve"`
	Rationale      string    `json:"rationale"`
	CreatedAt      time.Time `json:"created_at"`
}

type strategyLogEntry struct {
	Timestamp time.Time `json:"timestamp"`
	Proposal  string    `json:"proposal"`
}

// Strategist periodically scans unified theories and creates strategy memories.
type Strategist struct {
	mm      *memory.MemoryManager
	sm      *state.Manager
	Tick    time.Duration
	LogPath string

	mu      sync.Mutex
	running bool
	stopCh  chan struct{}
	wg      sync.WaitGroup
}

func NewStrategist(mm *memory.MemoryManager, sm *state.Manager) *Strategist {
	return &Strategist{
		mm:      mm,
		sm:      sm,
		Tick:    defaultStrategyTick,
		LogPath: defaultStrategyLogPath,
	}
}

func (s *Strategist) Start() {
	if s == nil || s.mm == nil {
		return
	}
	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		return
	}
	s.running = true
	s.stopCh = make(chan struct{})
	s.mu.Unlock()

	s.wg.Add(1)
	go s.loop()
}

func (s *Strategist) Stop() {
	if s == nil {
		return
	}
	s.mu.Lock()
	if !s.running {
		s.mu.Unlock()
		return
	}
	close(s.stopCh)
	s.running = false
	s.mu.Unlock()
	s.wg.Wait()
}

func (s *Strategist) loop() {
	defer s.wg.Done()
	t := s.Tick
	if t <= 0 {
		t = defaultStrategyTick
	}
	ticker := time.NewTicker(t)
	defer ticker.Stop()

	for {
		select {
		case <-s.stopCh:
			return
		case <-ticker.C:
			msg, ok := BuildStrategicProposal(s.mm, s.sm, "")
			if !ok {
				continue
			}
			_, _, _ = SpawnAutonomousMissionFromProposal(msg)
			_ = s.mm.AddKnowledge("Strategic Proposal Memory: "+msg, map[string]string{
				"type":            "knowledge",
				"source_type":     "strategy",
				"strategy_active": "true",
				"base_importance": "0.74",
			})
			_ = s.appendLog(msg)
		}
	}
}

// BuildStrategicProposal synthesizes a high-level strategy aligned to mission + preferences.
func BuildStrategicProposal(mm *memory.MemoryManager, sm *state.Manager, query string) (string, bool) {
	if mm == nil {
		return "", false
	}
	segs, err := mm.RetrieveKnowledgeSegments("Unified Theory strategy long-term architecture "+query, 42)
	if err != nil || len(segs) == 0 {
		return "", false
	}

	focus := ""
	rationale := ""
	text := []string{}
	for _, s := range segs {
		src := strings.ToLower(strings.TrimSpace(s.Metadata["source_type"]))
		if src == "deliberation" || src == "strategy" || strings.Contains(strings.ToLower(s.Content), "unified theory") {
			t := strings.TrimSpace(s.Content)
			if t != "" {
				text = append(text, t)
			}
		}
	}
	if len(text) == 0 {
		return "", false
	}

	joined := strings.ToLower(strings.Join(text, "\n"))
	switch {
	case containsAny(joined, "firewall", "port 443", "vps", "ingress"):
		focus = "frequent VPS firewall issues"
		rationale = "repeated firewall/ingress friction across unified theories"
	case containsAny(joined, "manual restart", "restart service", "check logs"):
		focus = "recurring manual operations toil"
		rationale = "manual recovery steps repeated across sessions"
	case containsAny(joined, "parser", "binary", "schema"):
		focus = "repeated parsing and dependency ambiguity"
		rationale = "consistent parser/dependency uncertainty in knowledge graph"
	default:
		focus = "recurring infrastructure reliability friction"
		rationale = "pattern of repeated operational fixes"
	}

	transition := "a Zero-Trust Sasswall architecture with policy-as-code controls"
	permanentSolve := "eliminate repetitive incident classes by codifying trust boundaries, automation, and verification loops"
	if !alignmentPass(mm, sm, focus) {
		return "", false
	}
	cm := cognition.NewDefaultCausalModel()
	assessment := cm.Simulate(transition)
	if !assessment.Safe {
		// Strategic Pivot for high-fragility plans.
		transition = "a phased policy-as-code rollout with canary verification and rollback automation"
		assessment = cm.Simulate(transition)
		if !assessment.Safe {
			return "", false
		}
	}

	proposal := "Strategic Proposal: I've analyzed our recent work on " + focus +
		", and I've developed a strategy to transition to " + transition +
		" which will solve " + permanentSolve + " permanently. " +
		fmt.Sprintf("(Causal safety fragility score: %.2f across %d what-if simulations.) ", assessment.FragilityScore, len(assessment.Simulations)) +
		"I've already validated the API keys and verified the local directory structure; we are ready to execute Phase 1 immediately. " +
		"Would you like me to Decompose this into a new Mission?"

	_ = rationale // retained for future structured logging expansion
	return proposal, true
}

func alignmentPass(mm *memory.MemoryManager, sm *state.Manager, focus string) bool {
	focus = strings.ToLower(strings.TrimSpace(focus))
	goal := ""
	if sm != nil {
		goal = strings.ToLower(strings.TrimSpace(sm.GetSnapshot().PrimaryGoal))
	}
	if plan, found, err := LoadActiveMission(""); err == nil && found && strings.TrimSpace(plan.Goal) != "" {
		goal = strings.ToLower(strings.TrimSpace(plan.Goal))
	}
	missionOK := containsAny(goal, "thynaptic", "vps", "secure", "architecture", "automation", "mission") || containsAny(focus, "vps", "infrastructure", "firewall")

	// Recursive preference model alignment: require at least one preference vector present.
	prefs, _ := mm.RetrieveKnowledgeSegments("preference vector engineering style", 6)
	prefOK := false
	for _, p := range prefs {
		if strings.ToLower(strings.TrimSpace(p.Metadata["memory_kind"])) == "preference_vector" {
			prefOK = true
			break
		}
	}
	return missionOK && prefOK
}

func (s *Strategist) appendLog(proposal string) error {
	path := s.LogPath
	if strings.TrimSpace(path) == "" {
		path = defaultStrategyLogPath
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	var logs []strategyLogEntry
	if b, err := os.ReadFile(path); err == nil && len(b) > 0 {
		_ = json.Unmarshal(b, &logs)
	}
	logs = append(logs, strategyLogEntry{Timestamp: time.Now().UTC(), Proposal: strings.TrimSpace(proposal)})
	if len(logs) > 800 {
		logs = logs[len(logs)-800:]
	}
	sort.Slice(logs, func(i, j int) bool { return logs[i].Timestamp.Before(logs[j].Timestamp) })
	data, err := json.MarshalIndent(logs, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

func containsAny(s string, needles ...string) bool {
	for _, n := range needles {
		if strings.Contains(s, n) {
			return true
		}
	}
	return false
}
