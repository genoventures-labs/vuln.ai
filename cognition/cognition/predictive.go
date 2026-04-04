package cognition

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/Thynaptic/P-LMv1/pkg/memory"
)

const (
	defaultMissionPath = ".memory/active_mission.json"
)

type predictiveMission struct {
	Goal   string            `json:"goal"`
	Phases []predictivePhase `json:"phases"`
}

type predictivePhase struct {
	Title string           `json:"title"`
	Tasks []predictiveTask `json:"tasks"`
}

type predictiveTask struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Status      string `json:"status"`
}

type PredictiveInsight struct {
	TaskID      string
	Dependency  string
	Friction    string
	SkillPath   string
	ResearchHit bool
}

// PredictiveForecaster proactively scans mission risk and pre-compiles missing skills.
type PredictiveForecaster struct {
	mm       *memory.MemoryManager
	compiler *SkillCompiler

	mu             sync.Mutex
	lastRun        time.Time
	running        bool
	compiledByTask map[string]bool
	notices        []string
}

func NewPredictiveForecaster(mm *memory.MemoryManager) *PredictiveForecaster {
	return &PredictiveForecaster{
		mm:             mm,
		compiler:       NewSkillCompiler(),
		compiledByTask: make(map[string]bool),
		notices:        []string{},
	}
}

// EvaluateAsync runs prediction in background with throttling.
func (pf *PredictiveForecaster) EvaluateAsync(trigger string) {
	if pf == nil {
		return
	}
	pf.mu.Lock()
	if pf.running {
		pf.mu.Unlock()
		return
	}
	if !pf.lastRun.IsZero() && time.Since(pf.lastRun) < 8*time.Second {
		pf.mu.Unlock()
		return
	}
	pf.running = true
	pf.lastRun = time.Now().UTC()
	pf.mu.Unlock()

	go func() {
		defer func() {
			pf.mu.Lock()
			pf.running = false
			pf.mu.Unlock()
		}()
		insights := pf.forecast(trigger)
		if len(insights) == 0 {
			return
		}
		pf.mu.Lock()
		defer pf.mu.Unlock()
		for _, in := range insights {
			notice := buildPredictiveNotice(in)
			if notice == "" || containsString(pf.notices, notice) {
				continue
			}
			pf.notices = append(pf.notices, notice)
			if len(pf.notices) > 8 {
				pf.notices = pf.notices[len(pf.notices)-8:]
			}
		}
	}()
}

func (pf *PredictiveForecaster) forecast(trigger string) []PredictiveInsight {
	mission, err := loadPredictiveMission(defaultMissionPath)
	if err != nil || len(mission.Phases) == 0 {
		return nil
	}

	// Prefer active task first, then pending tasks in declared order.
	var tasks []predictiveTask
	for _, ph := range mission.Phases {
		for _, t := range ph.Tasks {
			status := strings.ToLower(strings.TrimSpace(t.Status))
			if status == "active" {
				tasks = append([]predictiveTask{t}, tasks...)
			} else if status == "pending" {
				tasks = append(tasks, t)
			}
		}
	}
	if len(tasks) == 0 {
		return nil
	}

	limit := 2
	insights := []PredictiveInsight{}
	for _, t := range tasks {
		if len(insights) >= limit {
			break
		}
		taskText := strings.ToLower(strings.TrimSpace(t.Title + " " + t.Description))
		dep := detectDependency(taskText)
		if dep == "" {
			continue
		}

		// Pre-emptive research for ambiguous dependencies.
		researchHit := false
		if pf.mm != nil {
			if segs, err := pf.mm.RetrieveKnowledgeSegments(dep+" dependency api sdk docs", 6); err == nil && len(segs) > 0 {
				researchHit = true
			}
		}
		friction := "Potential technical friction: dependency '" + dep + "' is not yet grounded in known docs."
		if researchHit {
			friction = "Dependency '" + dep + "' exists but appears ambiguous across sources."
		}

		insight := PredictiveInsight{
			TaskID:      t.ID,
			Dependency:  dep,
			Friction:    friction,
			ResearchHit: researchHit,
		}

		// Proactive skill compilation before synthesis.
		compileKey := t.ID + "|" + dep
		pf.mu.Lock()
		already := pf.compiledByTask[compileKey]
		pf.mu.Unlock()
		if !already && requiresCompiler(dep) {
			gap := CapabilityGap{
				Detected:    true,
				Description: "need parser for " + dep,
			}
			skill, err := pf.compiler.CompileSkillPrimitive(gap, t.Description)
			if err == nil {
				insight.SkillPath = skill.RootDir
				pf.mu.Lock()
				pf.compiledByTask[compileKey] = true
				pf.mu.Unlock()
			}
		}
		insights = append(insights, insight)
	}
	return insights
}

func (pf *PredictiveForecaster) Notices() []string {
	if pf == nil {
		return nil
	}
	pf.mu.Lock()
	defer pf.mu.Unlock()
	out := make([]string, len(pf.notices))
	copy(out, pf.notices)
	return out
}

func loadPredictiveMission(path string) (predictiveMission, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return predictiveMission{}, err
	}
	var m predictiveMission
	if err := json.Unmarshal(b, &m); err != nil {
		return predictiveMission{}, err
	}
	return m, nil
}

func detectDependency(taskText string) string {
	candidates := []string{
		"parser", "protobuf", "json schema", "openapi", "api", "sdk",
		"webhook", "auth", "firewall", "yaml", "binary", "regex",
	}
	for _, c := range candidates {
		if strings.Contains(taskText, c) {
			return c
		}
	}
	return ""
}

func requiresCompiler(dep string) bool {
	dep = strings.ToLower(strings.TrimSpace(dep))
	for _, d := range []string{"parser", "protobuf", "json schema", "yaml", "binary", "regex"} {
		if strings.Contains(dep, d) {
			return true
		}
	}
	return false
}

func buildPredictiveNotice(in PredictiveInsight) string {
	if strings.TrimSpace(in.SkillPath) != "" {
		return "I've noticed we're likely to need a parser for " + in.Dependency + ", so I've already compiled a prototype skill for it in " + filepath.ToSlash(in.SkillPath) + "."
	}
	if in.Dependency != "" {
		return "Predictive note: upcoming dependency '" + in.Dependency + "' may create friction; I started pre-emptive research on ambiguous docs."
	}
	return ""
}

func containsString(in []string, v string) bool {
	for _, x := range in {
		if x == v {
			return true
		}
	}
	return false
}
