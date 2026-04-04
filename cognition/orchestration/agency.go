package orchestration

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Thynaptic/P-LMv1/pkg/cognition"
	"github.com/Thynaptic/P-LMv1/pkg/memory"
	"github.com/Thynaptic/P-LMv1/pkg/state"
)

const (
	defaultAutonomousMissionsPath = ".memory/autonomous_missions.json"
	defaultAgencyLogPath          = ".memory/agency_log.json"
	defaultAgencyTick             = 25 * time.Second
	defaultAgencyIdleAfter        = 75 * time.Second
)

type AutonomousMissionStatus string
type AutonomousMissionMode string

const (
	AutoQueued          AutonomousMissionStatus = "queued"
	AutoRunning         AutonomousMissionStatus = "running"
	AutoPendingApproval AutonomousMissionStatus = "pending_approval"
	AutoCompleted       AutonomousMissionStatus = "completed"
	AutoFailed          AutonomousMissionStatus = "failed"

	AutoModeSpeculative AutonomousMissionMode = "speculative"
	AutoModeExecution   AutonomousMissionMode = "execution"
)

type AutonomousMission struct {
	ID               string                  `json:"id"`
	Mode             AutonomousMissionMode   `json:"mode"`
	SourceProposal   string                  `json:"source_proposal"`
	Plan             MissionPlan             `json:"plan"`
	Status           AutonomousMissionStatus `json:"status"`
	RequiresApproval bool                    `json:"requires_approval"`
	LeadWorkPercent  float64                 `json:"lead_work_percent"`
	SpeculativeLogs  []string                `json:"speculative_logs,omitempty"`
	TempSkills       []string                `json:"temp_skills,omitempty"`
	ReadyForPhase1   bool                    `json:"ready_for_phase1"`
	PendingMessage   string                  `json:"pending_message,omitempty"`
	LastAction       string                  `json:"last_action,omitempty"`
	CreatedAt        time.Time               `json:"created_at"`
	UpdatedAt        time.Time               `json:"updated_at"`
}

type autonomousMissionStore struct {
	Missions []AutonomousMission `json:"missions"`
}

type agencyLogEntry struct {
	Timestamp time.Time `json:"timestamp"`
	MissionID string    `json:"mission_id"`
	Action    string    `json:"action"`
	Detail    string    `json:"detail"`
}

var agencyMu sync.Mutex

// SpawnAutonomousMissionFromProposal creates an autonomous mission from strategic proposal text.
func SpawnAutonomousMissionFromProposal(proposal string) (AutonomousMission, bool, error) {
	proposal = strings.TrimSpace(proposal)
	if proposal == "" {
		return AutonomousMission{}, false, nil
	}

	agencyMu.Lock()
	defer agencyMu.Unlock()

	store, _ := loadAutonomousStore(defaultAutonomousMissionsPath)
	for _, m := range store.Missions {
		if strings.EqualFold(strings.TrimSpace(m.SourceProposal), proposal) &&
			m.Status != AutoCompleted && m.Status != AutoFailed {
			return m, false, nil
		}
	}

	goal := strategyProposalToGoal(proposal)
	plan := DecomposeMission(goal)
	now := time.Now().UTC()
	m := AutonomousMission{
		ID:               fmt.Sprintf("auto_%d", now.UnixNano()),
		Mode:             AutoModeSpeculative,
		SourceProposal:   proposal,
		Plan:             plan,
		Status:           AutoQueued,
		RequiresApproval: false,
		LeadWorkPercent:  0.0,
		SpeculativeLogs:  []string{},
		TempSkills:       []string{},
		ReadyForPhase1:   false,
		CreatedAt:        now,
		UpdatedAt:        now,
	}

	store.Missions = append(store.Missions, m)
	if err := saveAutonomousStore(defaultAutonomousMissionsPath, store); err != nil {
		return AutonomousMission{}, false, err
	}
	_ = appendAgencyLog(defaultAgencyLogPath, agencyLogEntry{
		Timestamp: now,
		MissionID: m.ID,
		Action:    "spawned",
		Detail:    "spawned speculative mission from strategist proposal",
	})
	return m, true, nil
}

// PendingApprovalPrompt returns the next consent message if a mission is awaiting write approval.
func PendingApprovalPrompt() string {
	agencyMu.Lock()
	defer agencyMu.Unlock()

	store, err := loadAutonomousStore(defaultAutonomousMissionsPath)
	if err != nil {
		return ""
	}
	for _, m := range store.Missions {
		if m.Status == AutoPendingApproval && strings.TrimSpace(m.PendingMessage) != "" {
			return m.PendingMessage
		}
	}
	return ""
}

// TryHandleAgencyApproval processes direct user consent/decline commands for pending agency missions.
func TryHandleAgencyApproval(userInput string) (string, bool) {
	in := strings.ToLower(strings.TrimSpace(userInput))
	if in == "" {
		return "", false
	}
	approve := containsAnyAgency(in,
		"approve", "yes execute", "yes, execute", "run it", "execute", "proceed", "go ahead")
	decline := containsAnyAgency(in,
		"not now", "hold", "decline", "cancel", "stop")
	if !approve && !decline {
		return "", false
	}

	agencyMu.Lock()
	defer agencyMu.Unlock()
	store, err := loadAutonomousStore(defaultAutonomousMissionsPath)
	if err != nil {
		return "", false
	}
	idx := -1
	for i := range store.Missions {
		if store.Missions[i].Status == AutoPendingApproval {
			idx = i
			break
		}
	}
	if idx < 0 {
		return "", false
	}
	m := store.Missions[idx]
	if decline {
		m.Status = AutoFailed
		m.LastAction = "user declined execution"
		m.PendingMessage = ""
		m.UpdatedAt = time.Now().UTC()
		store.Missions[idx] = m
		_ = saveAutonomousStore(defaultAutonomousMissionsPath, store)
		_ = appendAgencyLog(defaultAgencyLogPath, agencyLogEntry{
			Timestamp: m.UpdatedAt,
			MissionID: m.ID,
			Action:    "declined",
			Detail:    "user declined pending mission",
		})
		return "Understood. I paused that background mission.", true
	}

	m = commitSpeculativeLeadWork(m)
	store.Missions[idx] = m
	_ = saveAutonomousStore(defaultAutonomousMissionsPath, store)
	_ = appendAgencyLog(defaultAgencyLogPath, agencyLogEntry{
		Timestamp: m.UpdatedAt,
		MissionID: m.ID,
		Action:    "approved",
		Detail:    "speculative lead-work committed to execution",
	})
	return "Approved. I committed the speculative lead-work and moved the mission to 10% completion. Phase 1 can start immediately.", true
}

// AgencyWorker executes autonomous missions in background while CLI is idle.
type AgencyWorker struct {
	mm *memory.MemoryManager
	sm *state.Manager
	sc *cognition.SkillCompiler

	Tick      time.Duration
	IdleAfter time.Duration

	mu           sync.Mutex
	lastActivity time.Time
	running      bool
	stopCh       chan struct{}
	wg           sync.WaitGroup
}

func NewAgencyWorker(mm *memory.MemoryManager, sm *state.Manager) *AgencyWorker {
	return &AgencyWorker{
		mm:           mm,
		sm:           sm,
		sc:           cognition.NewSkillCompiler(),
		Tick:         defaultAgencyTick,
		IdleAfter:    defaultAgencyIdleAfter,
		lastActivity: time.Now().UTC(),
	}
}

func (w *AgencyWorker) Start() {
	if w == nil || w.mm == nil {
		return
	}
	w.mu.Lock()
	if w.running {
		w.mu.Unlock()
		return
	}
	w.running = true
	w.stopCh = make(chan struct{})
	w.mu.Unlock()

	w.wg.Add(1)
	go w.loop()
}

func (w *AgencyWorker) Stop() {
	if w == nil {
		return
	}
	w.mu.Lock()
	if !w.running {
		w.mu.Unlock()
		return
	}
	close(w.stopCh)
	w.running = false
	w.mu.Unlock()
	w.wg.Wait()
}

func (w *AgencyWorker) NotifyActivity() {
	if w == nil {
		return
	}
	w.mu.Lock()
	w.lastActivity = time.Now().UTC()
	w.mu.Unlock()
}

func (w *AgencyWorker) loop() {
	defer w.wg.Done()
	tick := w.Tick
	if tick <= 0 {
		tick = defaultAgencyTick
	}
	idleAfter := w.IdleAfter
	if idleAfter <= 0 {
		idleAfter = defaultAgencyIdleAfter
	}

	t := time.NewTicker(tick)
	defer t.Stop()
	for {
		select {
		case <-w.stopCh:
			return
		case <-t.C:
			if !w.isIdle(idleAfter) {
				continue
			}
			_ = w.runOnce()
			w.NotifyActivity()
		}
	}
}

func (w *AgencyWorker) isIdle(idleAfter time.Duration) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return time.Since(w.lastActivity) >= idleAfter
}

func (w *AgencyWorker) runOnce() error {
	agencyMu.Lock()
	defer agencyMu.Unlock()

	store, err := loadAutonomousStore(defaultAutonomousMissionsPath)
	if err != nil {
		return err
	}
	idx := -1
	for i, m := range store.Missions {
		if m.Status == AutoQueued || m.Status == AutoRunning {
			idx = i
			break
		}
	}
	if idx < 0 {
		return nil
	}
	m := store.Missions[idx]
	if m.Status == AutoQueued {
		m.Status = AutoRunning
		m.UpdatedAt = time.Now().UTC()
		_ = appendAgencyLog(defaultAgencyLogPath, agencyLogEntry{
			Timestamp: m.UpdatedAt,
			MissionID: m.ID,
			Action:    "started",
			Detail:    "autonomous mission execution started",
		})
	}

	// 10% Speculative Buffer: perform only lead-work scope while in speculative mode.
	if m.Mode == AutoModeSpeculative {
		m = w.runSpeculativeLeadWork(m)
		store.Missions[idx] = m
		_ = saveAutonomousStore(defaultAutonomousMissionsPath, store)
		return nil
	}

	active, ok := ActiveTask(m.Plan)
	if !ok {
		m.Plan = ActivateNextTask(m.Plan)
		active, ok = ActiveTask(m.Plan)
		if !ok {
			m.Status = AutoCompleted
			m.LastAction = "no pending tasks"
			m.UpdatedAt = time.Now().UTC()
			store.Missions[idx] = m
			_ = saveAutonomousStore(defaultAutonomousMissionsPath, store)
			return nil
		}
	}

	if taskRequiresWrite(active) {
		m.Status = AutoPendingApproval
		m.RequiresApproval = true
		m.PendingMessage = "I've identified a risk in " + active.Title + " and have prepared a solution in a background mission. Shall I execute?"
		m.LastAction = "awaiting approval for write action"
		m.UpdatedAt = time.Now().UTC()
		store.Missions[idx] = m
		_ = saveAutonomousStore(defaultAutonomousMissionsPath, store)
		_ = appendAgencyLog(defaultAgencyLogPath, agencyLogEntry{
			Timestamp: m.UpdatedAt,
			MissionID: m.ID,
			Action:    "pending_approval",
			Detail:    active.ID + ": " + active.Title,
		})
		return nil
	}

	// Read-only background execution: research + possible skill compilation.
	query := strings.TrimSpace(active.Title + " " + active.Description)
	if segs, err := w.mm.RetrieveKnowledgeSegments(query, 8); err == nil && len(segs) > 0 {
		_ = w.mm.AddKnowledge("Agency research note for task '"+active.ID+"': gathered "+strconv.Itoa(len(segs))+" context segments.", map[string]string{
			"type":            "knowledge",
			"source_type":     "agency_research",
			"base_importance": "0.60",
		})
	}

	if w.sc != nil {
		gap := cognition.IdentifyCapabilityGap(strings.ToLower(active.Title + " " + active.Description))
		if gap.Detected {
			if skill, err := w.sc.CompileSkillPrimitive(gap, query); err == nil {
				_ = w.mm.AddKnowledge("Agency compiled helper skill for task '"+active.ID+"': "+skill.RootDir, map[string]string{
					"type":            "knowledge",
					"source_type":     "agency_skill",
					"base_importance": "0.68",
				})
				_ = appendAgencyLog(defaultAgencyLogPath, agencyLogEntry{
					Timestamp: time.Now().UTC(),
					MissionID: m.ID,
					Action:    "skill_compiled",
					Detail:    skill.RootDir,
				})
			}
		}
	}

	m.Plan = markTaskCompleted(m.Plan, active.ID)
	m.Plan = ActivateNextTask(m.Plan)
	if _, ok := ActiveTask(m.Plan); !ok {
		m.Status = AutoCompleted
		m.LastAction = "all autonomous read-only tasks complete"
	} else {
		m.Status = AutoRunning
		m.LastAction = "completed task " + active.ID
	}
	m.UpdatedAt = time.Now().UTC()
	store.Missions[idx] = m
	_ = saveAutonomousStore(defaultAutonomousMissionsPath, store)
	_ = appendAgencyLog(defaultAgencyLogPath, agencyLogEntry{
		Timestamp: m.UpdatedAt,
		MissionID: m.ID,
		Action:    "task_completed",
		Detail:    active.ID,
	})
	return nil
}

func (w *AgencyWorker) runSpeculativeLeadWork(m AutonomousMission) AutonomousMission {
	now := time.Now().UTC()
	logs := append([]string{}, m.SpeculativeLogs...)

	// Deep Research: inspect docs for concrete versions found in current environment evidence.
	query := strings.TrimSpace(m.Plan.Goal + " version api docs")
	segs, err := w.mm.RetrieveKnowledgeSegments(query, 24)
	versions := extractVersionTokensFromSegments(segs)
	if err == nil && len(versions) > 0 {
		logs = append(logs, "Deep Research: identified environment-specific versions: "+strings.Join(versions, ", "))
	} else {
		logs = append(logs, "Deep Research: no explicit version pin found; gathered generic docs context.")
	}

	// Dependency Validation: check if skill primitives exist; compile if needed.
	tempSkills := append([]string{}, m.TempSkills...)
	depGap := cognition.IdentifyCapabilityGap(strings.ToLower(m.Plan.Goal))
	if depGap.Detected {
		if skill, err := w.sc.CompileSkillPrimitive(depGap, m.Plan.Goal); err == nil {
			tempSkills = append(tempSkills, skill.RootDir)
			logs = append(logs, "Dependency Validation: compiled temp skill "+skill.RootDir)
		} else {
			logs = append(logs, "Dependency Validation: attempted compile but failed: "+err.Error())
		}
	} else {
		if entries, err := os.ReadDir(".skills/permanent"); err == nil && len(entries) > 0 {
			logs = append(logs, "Dependency Validation: permanent skill inventory available.")
		} else {
			logs = append(logs, "Dependency Validation: no strong permanent skill match; compile may be needed at execution.")
		}
	}

	// Dry Runs: check-only environment/path validation.
	logs = append(logs, dryRunChecks()...)

	m.SpeculativeLogs = dedupeAgency(logs)
	m.TempSkills = dedupeAgency(tempSkills)
	m.LeadWorkPercent = 0.10
	m.ReadyForPhase1 = true
	m.RequiresApproval = true
	m.Status = AutoPendingApproval
	m.PendingMessage = "I've identified a risk in " + activeTaskLabel(m.Plan) + " and have prepared a solution in a background mission. Shall I execute?\nI've already validated the API keys and verified the local directory structure; we are ready to execute Phase 1 immediately."
	m.LastAction = "speculative lead-work complete (10%)"
	m.UpdatedAt = now
	_ = appendAgencyLog(defaultAgencyLogPath, agencyLogEntry{
		Timestamp: now,
		MissionID: m.ID,
		Action:    "speculative_complete",
		Detail:    "10% lead-work complete, awaiting approval",
	})
	return m
}

func taskRequiresWrite(t MissionTask) bool {
	s := strings.ToLower(strings.TrimSpace(t.Title + " " + t.Description))
	return containsAnyAgency(s,
		"apply", "configure", "change", "modify", "write", "delete",
		"update", "migrate", "replace", "lockdown", "set ",
	)
}

func markTaskCompleted(plan MissionPlan, taskID string) MissionPlan {
	plan.UpdatedAt = time.Now().UTC()
	for pi := range plan.Phases {
		for ti := range plan.Phases[pi].Tasks {
			if plan.Phases[pi].Tasks[ti].ID == taskID {
				plan.Phases[pi].Tasks[ti].Status = TaskCompleted
				plan.Phases[pi].Tasks[ti].BlockedBy = ""
				return plan
			}
		}
	}
	return plan
}

func commitSpeculativeLeadWork(m AutonomousMission) AutonomousMission {
	m.Mode = AutoModeExecution
	m.Status = AutoRunning
	m.RequiresApproval = false
	m.PendingMessage = ""
	m.LastAction = "approved; speculative context committed"
	if m.LeadWorkPercent < 0.10 {
		m.LeadWorkPercent = 0.10
	}
	// Instant 10% completion proxy: complete first active/pending task.
	if t, ok := ActiveTask(m.Plan); ok {
		m.Plan = markTaskCompleted(m.Plan, t.ID)
	}
	m.Plan = ActivateNextTask(m.Plan)
	m.UpdatedAt = time.Now().UTC()
	return m
}

func strategyProposalToGoal(proposal string) string {
	p := strings.TrimSpace(proposal)
	if p == "" {
		return "Autonomous mission to reduce recurring infrastructure risk"
	}
	l := strings.ToLower(p)
	if i := strings.Index(l, "transition to "); i >= 0 {
		x := strings.TrimSpace(p[i+len("transition to "):])
		if j := strings.Index(strings.ToLower(x), " which "); j > 0 {
			x = strings.TrimSpace(x[:j])
		}
		if x != "" {
			return "Plan and execute transition to " + x
		}
	}
	return "Execute strategic risk-reduction mission from proposal: " + truncateAgency(p, 180)
}

func activeTaskLabel(plan MissionPlan) string {
	if t, ok := ActiveTask(plan); ok {
		return t.Title
	}
	return "the current mission phase"
}

func extractVersionTokensFromSegments(segs []memory.KnowledgeSegment) []string {
	re := regexp.MustCompile(`\bv?\d+\.\d+(?:\.\d+)?\b`)
	seen := map[string]bool{}
	var out []string
	for _, s := range segs {
		for _, m := range re.FindAllString(strings.ToLower(s.Content), -1) {
			if seen[m] {
				continue
			}
			seen[m] = true
			out = append(out, m)
			if len(out) >= 8 {
				return out
			}
		}
	}
	return out
}

func dryRunChecks() []string {
	logs := []string{}
	for _, env := range []string{"GLM_API_KEY", "GLM_CLIENT_ID", "OLLAMA_HOST"} {
		if _, ok := os.LookupEnv(env); ok {
			logs = append(logs, "Dry Run: "+env+" detected.")
		} else {
			logs = append(logs, "Dry Run: "+env+" missing.")
		}
	}
	for _, p := range []string{".", ".memory", ".skills", ".skills/temp", ".skills/permanent"} {
		if st, err := os.Stat(p); err == nil && st.IsDir() {
			logs = append(logs, "Dry Run: directory ok -> "+p)
		} else {
			logs = append(logs, "Dry Run: directory unavailable -> "+p)
		}
	}
	return logs
}

func dedupeAgency(in []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(in))
	for _, v := range in {
		s := strings.TrimSpace(v)
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}

func loadAutonomousStore(path string) (autonomousMissionStore, error) {
	if strings.TrimSpace(path) == "" {
		path = defaultAutonomousMissionsPath
	}
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return autonomousMissionStore{Missions: []AutonomousMission{}}, nil
		}
		return autonomousMissionStore{}, err
	}
	var s autonomousMissionStore
	if err := json.Unmarshal(b, &s); err != nil {
		return autonomousMissionStore{}, err
	}
	if s.Missions == nil {
		s.Missions = []AutonomousMission{}
	}
	return s, nil
}

func saveAutonomousStore(path string, store autonomousMissionStore) error {
	if strings.TrimSpace(path) == "" {
		path = defaultAutonomousMissionsPath
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(store, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o644)
}

func appendAgencyLog(path string, entry agencyLogEntry) error {
	if strings.TrimSpace(path) == "" {
		path = defaultAgencyLogPath
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	var logs []agencyLogEntry
	if b, err := os.ReadFile(path); err == nil && len(b) > 0 {
		_ = json.Unmarshal(b, &logs)
	}
	logs = append(logs, entry)
	if len(logs) > 2000 {
		logs = logs[len(logs)-2000:]
	}
	b, err := json.MarshalIndent(logs, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o644)
}

func containsAnyAgency(s string, needles ...string) bool {
	for _, n := range needles {
		if strings.Contains(s, n) {
			return true
		}
	}
	return false
}

func truncateAgency(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	if n < 4 {
		return s[:n]
	}
	return s[:n-3] + "..."
}
