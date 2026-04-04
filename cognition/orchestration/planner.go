package orchestration

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const defaultActiveMissionPath = ".memory/active_mission.json"

type TaskStatus string

const (
	TaskPending   TaskStatus = "pending"
	TaskActive    TaskStatus = "active"
	TaskBlocked   TaskStatus = "blocked"
	TaskCompleted TaskStatus = "completed"
)

type MissionTask struct {
	ID           string     `json:"id"`
	Title        string     `json:"title"`
	Description  string     `json:"description"`
	Status       TaskStatus `json:"status"`
	Dependencies []string   `json:"dependencies,omitempty"`
	BlockedBy    string     `json:"blocked_by,omitempty"`
}

type MissionPhase struct {
	ID    string        `json:"id"`
	Title string        `json:"title"`
	Tasks []MissionTask `json:"tasks"`
}

type MissionPlan struct {
	ID        string         `json:"id"`
	Goal      string         `json:"goal"`
	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
	Phases    []MissionPhase `json:"phases"`
}

func DecomposeMission(goal string) MissionPlan {
	goal = strings.TrimSpace(goal)
	now := time.Now().UTC()
	plan := MissionPlan{
		ID:        fmt.Sprintf("mission_%d", now.UnixNano()),
		Goal:      goal,
		CreatedAt: now,
		UpdatedAt: now,
	}

	lg := strings.ToLower(goal)
	if strings.Contains(lg, "vps") || strings.Contains(lg, "server") || strings.Contains(lg, "secure") {
		plan.Phases = []MissionPhase{
			{
				ID:    "phase_discovery",
				Title: "Discovery",
				Tasks: []MissionTask{
					newTask("collect_requirements", "Collect requirements", "Confirm target workload, exposure model, and operational constraints."),
					newTask("inventory_current_state", "Inventory current state", "Gather current VPS config, open ports, and running services."),
				},
			},
			{
				ID:    "phase_hardening",
				Title: "Hardening",
				Tasks: []MissionTask{
					newTaskWithDeps("baseline_updates", "Apply baseline updates", "Patch OS and core packages.", "inventory_current_state"),
					newTaskWithDeps("access_controls", "Configure access controls", "Set SSH hardening, firewall rules, and principle-of-least-privilege.", "baseline_updates"),
					newTaskWithDeps("service_policy", "Service policy lockdown", "Disable unused services and enforce policy constraints.", "access_controls"),
				},
			},
			{
				ID:    "phase_validation",
				Title: "Validation",
				Tasks: []MissionTask{
					newTaskWithDeps("verification_checks", "Run verification checks", "Validate ports, auth, logs, and fail2ban/monitoring posture.", "service_policy"),
					newTaskWithDeps("ops_handoff", "Operational handoff", "Document controls, recovery path, and maintenance cadence.", "verification_checks"),
				},
			},
		}
		return ActivateNextTask(plan)
	}

	plan.Phases = []MissionPhase{
		{
			ID:    "phase_plan",
			Title: "Plan",
			Tasks: []MissionTask{
				newTask("define_scope", "Define scope", "Clarify objective, constraints, and success criteria."),
				newTask("collect_context", "Collect context", "Gather relevant system and documentation evidence."),
			},
		},
		{
			ID:    "phase_build",
			Title: "Build",
			Tasks: []MissionTask{
				newTaskWithDeps("execute_steps", "Execute implementation steps", "Run implementation actions in dependency order.", "collect_context"),
			},
		},
		{
			ID:    "phase_verify",
			Title: "Verify",
			Tasks: []MissionTask{
				newTaskWithDeps("validate_outcome", "Validate outcome", "Check final system behavior against success criteria.", "execute_steps"),
				newTaskWithDeps("document_result", "Document result", "Summarize final state and open risks.", "validate_outcome"),
			},
		},
	}
	return ActivateNextTask(plan)
}

func IsProjectGoal(goal string) bool {
	g := strings.ToLower(strings.TrimSpace(goal))
	if g == "" {
		return false
	}
	for _, m := range []string{
		"set up", "setup", "build", "implement", "deploy", "migrate", "harden", "secure", "project", "architecture", "plan",
	} {
		if strings.Contains(g, m) {
			return true
		}
	}
	return strings.Count(g, " and ") >= 2 || len(g) > 120
}

func LoadActiveMission(path string) (MissionPlan, bool, error) {
	if strings.TrimSpace(path) == "" {
		path = defaultActiveMissionPath
	}
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return MissionPlan{}, false, nil
		}
		return MissionPlan{}, false, err
	}
	var p MissionPlan
	if err := json.Unmarshal(b, &p); err != nil {
		return MissionPlan{}, false, err
	}
	return p, true, nil
}

func SaveActiveMission(plan MissionPlan, path string) error {
	if strings.TrimSpace(path) == "" {
		path = defaultActiveMissionPath
	}
	plan.UpdatedAt = time.Now().UTC()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(plan, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o644)
}

func ActivateNextTask(plan MissionPlan) MissionPlan {
	plan.UpdatedAt = time.Now().UTC()
	// Ensure only one active task.
	for pi := range plan.Phases {
		for ti := range plan.Phases[pi].Tasks {
			if plan.Phases[pi].Tasks[ti].Status == TaskActive {
				return plan
			}
		}
	}
	for pi := range plan.Phases {
		for ti := range plan.Phases[pi].Tasks {
			t := &plan.Phases[pi].Tasks[ti]
			if t.Status != TaskPending {
				continue
			}
			if depsSatisfied(plan, t.Dependencies) {
				t.Status = TaskActive
				return plan
			}
		}
	}
	return plan
}

func HasBlockedTask(plan MissionPlan) bool {
	for _, ph := range plan.Phases {
		for _, t := range ph.Tasks {
			if t.Status == TaskBlocked {
				return true
			}
		}
	}
	return false
}

func MarkTaskBlocked(plan MissionPlan, taskID, reason string) MissionPlan {
	plan.UpdatedAt = time.Now().UTC()
	for pi := range plan.Phases {
		for ti := range plan.Phases[pi].Tasks {
			if plan.Phases[pi].Tasks[ti].ID == taskID {
				plan.Phases[pi].Tasks[ti].Status = TaskBlocked
				plan.Phases[pi].Tasks[ti].BlockedBy = strings.TrimSpace(reason)
				return plan
			}
		}
	}
	return plan
}

func ActiveTask(plan MissionPlan) (MissionTask, bool) {
	for _, ph := range plan.Phases {
		for _, t := range ph.Tasks {
			if t.Status == TaskActive {
				return t, true
			}
		}
	}
	return MissionTask{}, false
}

func PlanSummary(plan MissionPlan) string {
	var b strings.Builder
	b.WriteString("Mission Plan: " + plan.Goal + "\n")
	for _, ph := range plan.Phases {
		b.WriteString("- " + ph.Title + "\n")
		for _, t := range ph.Tasks {
			b.WriteString("  - [" + string(t.Status) + "] " + t.Title)
			if t.Status == TaskBlocked && strings.TrimSpace(t.BlockedBy) != "" {
				b.WriteString(" (blocked: " + t.BlockedBy + ")")
			}
			b.WriteString("\n")
		}
	}
	return strings.TrimSpace(b.String())
}

func newTask(id, title, desc string) MissionTask {
	return MissionTask{
		ID:          id,
		Title:       title,
		Description: desc,
		Status:      TaskPending,
	}
}

func newTaskWithDeps(id, title, desc string, deps ...string) MissionTask {
	t := newTask(id, title, desc)
	t.Dependencies = deps
	return t
}

func depsSatisfied(plan MissionPlan, deps []string) bool {
	if len(deps) == 0 {
		return true
	}
	completed := make(map[string]bool)
	for _, ph := range plan.Phases {
		for _, t := range ph.Tasks {
			if t.Status == TaskCompleted {
				completed[t.ID] = true
			}
		}
	}
	for _, dep := range deps {
		if !completed[dep] {
			return false
		}
	}
	return true
}
