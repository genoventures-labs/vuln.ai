package cognition

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	defaultSelfModelPath    = ".memory/self_model.json"
	defaultSkillExecLog     = ".memory/skill_exec_log.json"
	defaultSelfModelTick    = 2 * time.Minute
	defaultTotalRAMFallback = 8.0
)

type ResourceState struct {
	CPULoadRatio float64   `json:"cpu_load_ratio"`
	RAMUsedGB    float64   `json:"ram_used_gb"`
	RAMTotalGB   float64   `json:"ram_total_gb"`
	RAMUsage     float64   `json:"ram_usage"`
	UpdatedAt    time.Time `json:"updated_at"`
}

type CapabilityEntry struct {
	Name        string  `json:"name"`
	Reliability float64 `json:"reliability"`
	UseCount    int     `json:"use_count"`
	Path        string  `json:"path,omitempty"`
}

// SelfModel stores current capability and runtime operating constraints.
type SelfModel struct {
	HardwareConstraints   map[string]string          `json:"hardware_constraints"`
	SupportedLanguages    []string                   `json:"supported_languages"`
	KnownAPIs             []string                   `json:"known_apis"`
	ConfidenceThresholds  map[string]float64         `json:"confidence_thresholds"`
	SkillReliability      map[string]float64         `json:"skill_reliability"`
	ResourceState         ResourceState              `json:"resource_state"`
	CapabilityMap         map[string]CapabilityEntry `json:"capability_map"`
	ContextWindowPressure float64                    `json:"context_window_pressure"`
	NeedsDeliberation     []string                   `json:"needs_deliberation,omitempty"`
	LastAudit             time.Time                  `json:"last_audit"`
}

type SkillExecutionLog struct {
	Timestamp time.Time `json:"timestamp"`
	SkillID   string    `json:"skill_id"`
	SkillName string    `json:"skill_name,omitempty"`
	SkillPath string    `json:"skill_path"`
	Success   bool      `json:"success"`
}

type DelegationDecision struct {
	Approved              bool   `json:"approved"`
	Strategy              string `json:"strategy,omitempty"`
	Reason                string `json:"reason,omitempty"`
	SelfAwareFeedback     string `json:"self_aware_feedback,omitempty"`
	NeedsSkillCompilation bool   `json:"needs_skill_compilation,omitempty"`
}

type SelfModelAuditor struct {
	Tick   time.Duration
	stopCh chan struct{}
	wg     sync.WaitGroup
	mu     sync.Mutex
	run    bool
}

var selfModelRuntime struct {
	mu       sync.RWMutex
	snapshot SelfModel
}

func NewSelfModelAuditor() *SelfModelAuditor {
	return &SelfModelAuditor{
		Tick: defaultSelfModelTick,
	}
}

func (a *SelfModelAuditor) Start() {
	if a == nil {
		return
	}
	a.mu.Lock()
	if a.run {
		a.mu.Unlock()
		return
	}
	a.run = true
	a.stopCh = make(chan struct{})
	a.mu.Unlock()

	a.wg.Add(1)
	go func() {
		defer a.wg.Done()
		tick := a.Tick
		if tick <= 0 {
			tick = defaultSelfModelTick
		}
		t := time.NewTicker(tick)
		defer t.Stop()

		_, _ = AuditSkillInventory()
		for {
			select {
			case <-a.stopCh:
				return
			case <-t.C:
				_, _ = AuditSkillInventory()
			}
		}
	}()
}

func (a *SelfModelAuditor) Stop() {
	if a == nil {
		return
	}
	a.mu.Lock()
	if !a.run {
		a.mu.Unlock()
		return
	}
	close(a.stopCh)
	a.run = false
	a.mu.Unlock()
	a.wg.Wait()
}

// AuditSkillInventory scans permanent skills, computes reliability, and refreshes runtime resource metrics.
func AuditSkillInventory() (SelfModel, error) {
	sm := SelfModel{
		HardwareConstraints: detectHardwareConstraints(),
		SupportedLanguages:  []string{"go", "bash", "python", "javascript"},
		KnownAPIs:           []string{"ollama", "glm_toolserver", "chromem_vector_store"},
		ConfidenceThresholds: map[string]float64{
			"min_skill_reliability": 0.55,
			"max_binary_ram_ratio":  0.40,
			"max_cpu_load_ratio":    0.85,
			"max_ram_usage":         0.85,
		},
		SkillReliability:      map[string]float64{},
		CapabilityMap:         map[string]CapabilityEntry{},
		ContextWindowPressure: CurrentContextWindowPressure(),
		NeedsDeliberation:     []string{},
		LastAudit:             time.Now().UTC(),
	}

	resource := detectResourceState()
	sm.ResourceState = resource

	type meta struct {
		ID       string `json:"id"`
		Name     string `json:"name"`
		Language string `json:"language"`
		UseCount int    `json:"use_count"`
	}
	execLogs := loadSkillExecLogs()
	logStats := map[string]struct {
		total int
		ok    int
	}{}
	familyStats := map[string]struct {
		total int
		ok    int
	}{}
	for _, l := range execLogs {
		idKey := strings.TrimSpace(l.SkillID)
		if idKey != "" {
			x := logStats[idKey]
			x.total++
			if l.Success {
				x.ok++
			}
			logStats[idKey] = x
		}
		fam := normalizeSkillFamily(l.SkillName, l.SkillID, l.SkillPath)
		if fam != "" {
			x := familyStats[fam]
			x.total++
			if l.Success {
				x.ok++
			}
			familyStats[fam] = x
		}
	}

	entries, _ := os.ReadDir(defaultSkillsPermDir)
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		metaPath := filepath.Join(defaultSkillsPermDir, e.Name(), "metadata.json")
		b, err := os.ReadFile(metaPath)
		if err != nil {
			continue
		}
		var m meta
		if err := json.Unmarshal(b, &m); err != nil {
			continue
		}
		if strings.TrimSpace(m.ID) == "" {
			m.ID = e.Name()
		}
		if m.Language != "" {
			sm.SupportedLanguages = append(sm.SupportedLanguages, strings.ToLower(strings.TrimSpace(m.Language)))
		}

		stats := logStats[m.ID]
		successRate := 0.5
		if stats.total > 0 {
			successRate = float64(stats.ok) / float64(stats.total)
		}
		useBoost := mathMin(0.25, float64(m.UseCount)*0.03)
		reliability := clamp01Self((successRate * 0.75) + 0.20 + useBoost)
		sm.SkillReliability[m.ID] = reliability

		name := strings.TrimSpace(m.Name)
		if name == "" {
			name = strings.TrimSpace(m.ID)
		}
		sm.CapabilityMap[name] = CapabilityEntry{
			Name:        name,
			Reliability: reliability,
			UseCount:    m.UseCount,
			Path:        filepath.Join(defaultSkillsPermDir, e.Name()),
		}
	}
	sm.SupportedLanguages = dedupeStrings(sm.SupportedLanguages)
	sort.Strings(sm.SupportedLanguages)

	for family, st := range familyStats {
		if st.total < 3 {
			continue
		}
		successRate := float64(st.ok) / float64(st.total)
		if successRate < 0.8 {
			sm.NeedsDeliberation = append(sm.NeedsDeliberation, family)
		}
	}
	sort.Strings(sm.NeedsDeliberation)
	sm.NeedsDeliberation = dedupeStrings(sm.NeedsDeliberation)

	setSelfModelSnapshot(sm)
	if err := saveSelfModel(sm); err != nil {
		return sm, err
	}
	return sm, nil
}

func AppendSkillExecutionLog(entry SkillExecutionLog) {
	path := defaultSkillExecLog
	_ = os.MkdirAll(filepath.Dir(path), 0o755)
	var logs []SkillExecutionLog
	if b, err := os.ReadFile(path); err == nil && len(b) > 0 {
		_ = json.Unmarshal(b, &logs)
	}
	if entry.Timestamp.IsZero() {
		entry.Timestamp = time.Now().UTC()
	}
	logs = append(logs, entry)
	if len(logs) > 4000 {
		logs = logs[len(logs)-4000:]
	}
	b, err := json.MarshalIndent(logs, "", "  ")
	if err != nil {
		return
	}
	_ = os.WriteFile(path, b, 0o644)
}

func loadSkillExecLogs() []SkillExecutionLog {
	b, err := os.ReadFile(defaultSkillExecLog)
	if err != nil || len(b) == 0 {
		return nil
	}
	var logs []SkillExecutionLog
	_ = json.Unmarshal(b, &logs)
	return logs
}

func LoadSelfModel() (SelfModel, error) {
	b, err := os.ReadFile(defaultSelfModelPath)
	if err != nil {
		if os.IsNotExist(err) {
			return AuditSkillInventory()
		}
		return SelfModel{}, err
	}
	var sm SelfModel
	if err := json.Unmarshal(b, &sm); err != nil {
		return SelfModel{}, err
	}
	if sm.CapabilityMap == nil {
		sm.CapabilityMap = map[string]CapabilityEntry{}
	}
	if sm.SkillReliability == nil {
		sm.SkillReliability = map[string]float64{}
	}
	setSelfModelSnapshot(sm)
	return sm, nil
}

func CurrentSelfModelSnapshot() SelfModel {
	selfModelRuntime.mu.RLock()
	defer selfModelRuntime.mu.RUnlock()
	return selfModelRuntime.snapshot
}

func UpdateContextWindowPressure(pressure float64) {
	selfModelRuntime.mu.Lock()
	selfModelRuntime.snapshot.ContextWindowPressure = clamp01Self(pressure)
	selfModelRuntime.mu.Unlock()
}

func CurrentContextWindowPressure() float64 {
	selfModelRuntime.mu.RLock()
	defer selfModelRuntime.mu.RUnlock()
	if selfModelRuntime.snapshot.ContextWindowPressure <= 0 {
		return 0.20
	}
	return clamp01Self(selfModelRuntime.snapshot.ContextWindowPressure)
}

func saveSelfModel(sm SelfModel) error {
	_ = os.MkdirAll(filepath.Dir(defaultSelfModelPath), 0o755)
	b, err := json.MarshalIndent(sm, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(defaultSelfModelPath, b, 0o644)
}

func detectHardwareConstraints() map[string]string {
	out := map[string]string{
		"goos":          runtime.GOOS,
		"goarch":        runtime.GOARCH,
		"cpu_cores":     strconv.Itoa(runtime.NumCPU()),
		"memory_gb_est": fmt.Sprintf("%.1f", detectMemoryGB()),
	}
	return out
}

func detectResourceState() ResourceState {
	total := detectMemoryGB()
	used := detectRAMUsedGB(total)
	cpu := detectCPULoadRatio()
	usage := 0.0
	if total > 0 {
		usage = used / total
	}
	return ResourceState{
		CPULoadRatio: clamp01Self(cpu),
		RAMUsedGB:    mathMax(0, used),
		RAMTotalGB:   total,
		RAMUsage:     clamp01Self(usage),
		UpdatedAt:    time.Now().UTC(),
	}
}

func detectCPULoadRatio() float64 {
	if b, err := os.ReadFile("/proc/loadavg"); err == nil {
		f := strings.Fields(strings.TrimSpace(string(b)))
		if len(f) > 0 {
			if load1, err := strconv.ParseFloat(f[0], 64); err == nil {
				cores := float64(runtime.NumCPU())
				if cores <= 0 {
					cores = 1
				}
				return load1 / cores
			}
		}
	}
	return 0.25
}

func detectRAMUsedGB(totalGB float64) float64 {
	if b, err := os.ReadFile("/proc/meminfo"); err == nil {
		reAvail := regexp.MustCompile(`(?m)^MemAvailable:\s+(\d+)\s+kB$`)
		m := reAvail.FindStringSubmatch(string(b))
		if len(m) == 2 {
			if kb, err := strconv.ParseFloat(m[1], 64); err == nil {
				availGB := kb / (1024 * 1024)
				if totalGB > 0 {
					used := totalGB - availGB
					if used < 0 {
						used = 0
					}
					return used
				}
			}
		}
	}
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	allocGB := float64(ms.Alloc) / (1024 * 1024 * 1024)
	if allocGB > 0 {
		return allocGB
	}
	if totalGB > 0 {
		return totalGB * 0.25
	}
	return 1.5
}

func detectMemoryGB() float64 {
	if b, err := os.ReadFile("/proc/meminfo"); err == nil {
		re := regexp.MustCompile(`(?m)^MemTotal:\s+(\d+)\s+kB$`)
		m := re.FindStringSubmatch(string(b))
		if len(m) == 2 {
			if kb, err := strconv.ParseFloat(m[1], 64); err == nil && kb > 0 {
				return kb / (1024 * 1024)
			}
		}
	}
	return defaultTotalRAMFallback
}

// AssessTaskAgainstSelfModel is the adaptive delegator gate for planner-time checks.
func AssessTaskAgainstSelfModel(task string) (bool, string) {
	decision := AssessDelegation(task)
	if !decision.Approved {
		if strings.TrimSpace(decision.Reason) != "" {
			return false, strings.TrimSpace(decision.Reason)
		}
		if strings.TrimSpace(decision.SelfAwareFeedback) != "" {
			return false, strings.TrimSpace(decision.SelfAwareFeedback)
		}
		return false, "task blocked by self-model"
	}
	return true, ""
}

// AssessDelegation evaluates resources/capabilities and recommends strategy pivots.
func AssessDelegation(task string) DelegationDecision {
	task = strings.TrimSpace(task)
	if task == "" {
		return DelegationDecision{Approved: true, Strategy: "default"}
	}
	sm, err := LoadSelfModel()
	if err != nil {
		return DelegationDecision{Approved: true, Strategy: "default"}
	}

	// Refresh transient runtime values before scoring the task.
	sm.ResourceState = detectResourceState()
	sm.ContextWindowPressure = CurrentContextWindowPressure()
	setSelfModelSnapshot(sm)

	l := strings.ToLower(task)
	feedback := ""

	if containsAnyWord(l, "binary", "elf", "firmware") {
		re := regexp.MustCompile(`(?i)(\d+(?:\.\d+)?)\s*gb`)
		m := re.FindStringSubmatch(l)
		requestedGB := 0.0
		if len(m) == 2 {
			requestedGB, _ = strconv.ParseFloat(m[1], 64)
		}
		if requestedGB == 0 {
			requestedGB = 1.0
		}

		ratio := requestedGB / mathMax(0.1, sm.ResourceState.RAMTotalGB)
		highResource := ratio > sm.ConfidenceThresholds["max_binary_ram_ratio"] ||
			sm.ResourceState.RAMUsage > sm.ConfidenceThresholds["max_ram_usage"] ||
			sm.ResourceState.CPULoadRatio > sm.ConfidenceThresholds["max_cpu_load_ratio"]
		if highResource {
			feedback = "I've analyzed the task, but based on my current capability map, I'll need to compile a more robust parser before I can guarantee the result."
			return DelegationDecision{
				Approved:              true,
				Strategy:              "chunked_analysis",
				Reason:                "Resource pressure detected for large binary analysis; pivoting to chunked analysis.",
				SelfAwareFeedback:     feedback,
				NeedsSkillCompilation: true,
			}
		}
	}

	if strings.Contains(l, "analyz") && strings.Contains(l, "rust") && !containsStringSelf(sm.SupportedLanguages, "rust") {
		feedback = "I've analyzed the task, but based on my current capability map, I'll need to compile a more robust parser before I can guarantee the result."
		return DelegationDecision{
			Approved:              false,
			Reason:                "I currently lack the resource/skill for this, I'll need to compile a specialized primitive first.",
			SelfAwareFeedback:     feedback,
			NeedsSkillCompilation: true,
		}
	}

	if sm.ContextWindowPressure > 0.85 {
		return DelegationDecision{
			Approved: true,
			Strategy: "chunked_analysis",
			Reason:   "Reasoning graph context window is crowded; using chunked execution.",
		}
	}

	return DelegationDecision{Approved: true, Strategy: "default"}
}

func setSelfModelSnapshot(sm SelfModel) {
	selfModelRuntime.mu.Lock()
	selfModelRuntime.snapshot = sm
	selfModelRuntime.mu.Unlock()
}

func normalizeSkillFamily(skillName, skillID, skillPath string) string {
	for _, c := range []string{skillName, skillID, filepath.Base(skillPath)} {
		s := strings.ToLower(strings.TrimSpace(c))
		if s == "" {
			continue
		}
		reDigits := regexp.MustCompile(`_?\d{6,}`)
		s = reDigits.ReplaceAllString(s, "")
		s = strings.Trim(s, "_-. ")
		if strings.HasPrefix(s, "skill") && len(strings.FieldsFunc(s, func(r rune) bool {
			return r == '_' || r == '-' || r == ' '
		})) <= 1 {
			continue
		}
		if s != "" {
			return s
		}
	}
	return ""
}

func clamp01Self(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

func mathMin(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}

func mathMax(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}

func dedupeStrings(in []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(in))
	for _, v := range in {
		s := strings.ToLower(strings.TrimSpace(v))
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}

func containsStringSelf(in []string, target string) bool {
	target = strings.ToLower(strings.TrimSpace(target))
	for _, v := range in {
		if strings.ToLower(strings.TrimSpace(v)) == target {
			return true
		}
	}
	return false
}

func containsAll(s string, subs ...string) bool {
	for _, sub := range subs {
		if !strings.Contains(s, sub) {
			return false
		}
	}
	return true
}

func containsAnyWord(s string, words ...string) bool {
	for _, w := range words {
		if strings.Contains(s, strings.ToLower(strings.TrimSpace(w))) {
			return true
		}
	}
	return false
}
