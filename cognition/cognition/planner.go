package cognition

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

const defaultArchivistDecisionFeed = ".memory/decision_feed.jsonl"

// WorkOrder is an executable mission prompt for Codex/TALOS.
type WorkOrder struct {
	ID              string   `json:"id"`
	Phase           string   `json:"phase"`
	Owner           string   `json:"owner"`
	Prompt          string   `json:"prompt"`
	DependsOn       []string `json:"depends_on,omitempty"`
	SuccessCriteria []string `json:"success_criteria,omitempty"`
}

// RoadmapMilestone is a discrete technical win.
type RoadmapMilestone struct {
	ID               string   `json:"id"`
	Phase            string   `json:"phase"`
	Title            string   `json:"title"`
	DefinitionOfDone []string `json:"definition_of_done"`
}

// RoadmapPhase represents one hierarchical phase with constraints + execution plan.
type RoadmapPhase struct {
	Name          string             `json:"name"`
	Goal          string             `json:"goal"`
	Constraints   []string           `json:"constraints"`
	Milestones    []RoadmapMilestone `json:"milestones"`
	WorkOrders    []WorkOrder        `json:"work_orders"`
	ArchivistRefs []string           `json:"archivist_refs,omitempty"`
}

// Roadmap is the structured planning artifact for "Jarvis-state" progression.
type Roadmap struct {
	Objective   string             `json:"objective"`
	BigIdea     string             `json:"big_idea"`
	GeneratedAt time.Time          `json:"generated_at"`
	Constraints []string           `json:"constraints"`
	Milestones  []RoadmapMilestone `json:"milestones"`
	WorkOrders  []WorkOrder        `json:"work_orders"`
	Phases      []RoadmapPhase     `json:"phases"`
}

type archivistRecord struct {
	Query      string `json:"query"`
	Reasoning  string `json:"reasoning"`
	Topology   string `json:"topology"`
	ChosenPath string `json:"chosen_path"`
}

type extractedStandard struct {
	Rule      string
	Reference string
}

// GenerateHierarchicalRoadmap decomposes a big idea into phase roadmap + work orders.
func GenerateHierarchicalRoadmap(bigIdea string) Roadmap {
	return GenerateHierarchicalRoadmapWithFeed(bigIdea, defaultArchivistDecisionFeed)
}

// GenerateHierarchicalRoadmapWithFeed is injectable for tests/custom archivist feeds.
func GenerateHierarchicalRoadmapWithFeed(bigIdea, decisionFeedPath string) Roadmap {
	idea := strings.TrimSpace(bigIdea)
	if idea == "" {
		idea = "Evolve the system to a reliable Jarvis-grade autonomous engineering runtime."
	}
	objective := "Jarvis-state: a resilient, local-first technical intelligence system that can plan, reason, verify, and execute safely."

	baseline := []string{
		"Must be Go-native for orchestration/runtime components.",
		"Must use local Vision/VLM paths for visual reasoning.",
		"No external APIs in core reasoning loops unless explicitly allowlisted.",
	}

	standards, refs := mineArchivistStandards(decisionFeedPath, 36)
	allConstraints := dedupeStringsPlanner(append(append([]string{}, baseline...), standards...))

	phaseDefs := []struct {
		Name string
		Goal string
	}{
		{Name: "Infrastructure", Goal: "Harden runtime primitives, daemon health, and local-first execution surfaces."},
		{Name: "Logic", Goal: "Strengthen planning/reasoning reliability, contradiction controls, and policy gates."},
		{Name: "Perception", Goal: "Improve visual/document understanding with grounded verification loops."},
		{Name: "Interface", Goal: "Deliver concise operator-facing status, controls, and handoff clarity."},
	}

	phases := make([]RoadmapPhase, 0, len(phaseDefs))
	var allMilestones []RoadmapMilestone
	var allOrders []WorkOrder
	for i, ph := range phaseDefs {
		phaseConstraints := mapConstraintsForPhase(ph.Name, allConstraints)
		phaseRefs := mapRefsForPhase(ph.Name, refs)
		milestones := phaseMilestones(ph.Name, i+1)
		orders := phaseWorkOrders(ph.Name, i+1, idea, objective, phaseConstraints)

		phase := RoadmapPhase{
			Name:          ph.Name,
			Goal:          ph.Goal,
			Constraints:   phaseConstraints,
			Milestones:    milestones,
			WorkOrders:    orders,
			ArchivistRefs: phaseRefs,
		}
		phases = append(phases, phase)
		allMilestones = append(allMilestones, milestones...)
		allOrders = append(allOrders, orders...)
	}

	return Roadmap{
		Objective:   objective,
		BigIdea:     idea,
		GeneratedAt: time.Now().UTC(),
		Constraints: allConstraints,
		Milestones:  allMilestones,
		WorkOrders:  allOrders,
		Phases:      phases,
	}
}

// RoadmapJSON returns a stable pretty-printed JSON roadmap artifact.
func RoadmapJSON(bigIdea string) string {
	roadmap := GenerateHierarchicalRoadmap(bigIdea)
	b, err := json.MarshalIndent(roadmap, "", "  ")
	if err != nil {
		return `{"objective":"Jarvis-state","error":"failed to marshal roadmap"}`
	}
	return string(b)
}

func mineArchivistStandards(path string, maxRecords int) ([]string, []string) {
	path = strings.TrimSpace(path)
	if path == "" {
		path = defaultArchivistDecisionFeed
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, nil
	}
	defer f.Close()

	if maxRecords <= 0 {
		maxRecords = 36
	}
	var records []archivistRecord
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		ln := strings.TrimSpace(sc.Text())
		if ln == "" {
			continue
		}
		var rec archivistRecord
		if err := json.Unmarshal([]byte(ln), &rec); err != nil {
			continue
		}
		records = append(records, rec)
		if len(records) >= maxRecords {
			break
		}
	}
	if len(records) == 0 {
		return nil, nil
	}

	ruleCount := map[string]int{}
	ruleRef := map[string]string{}
	for _, rec := range records {
		corpus := strings.Join([]string{
			strings.TrimSpace(rec.Query),
			strings.TrimSpace(rec.Reasoning),
			strings.TrimSpace(rec.Topology),
			strings.TrimSpace(rec.ChosenPath),
		}, "\n")
		for _, st := range extractStandardsFromText(corpus) {
			rule := strings.TrimSpace(st.Rule)
			if rule == "" {
				continue
			}
			ruleCount[rule]++
			if _, ok := ruleRef[rule]; !ok && strings.TrimSpace(st.Reference) != "" {
				ruleRef[rule] = st.Reference
			}
		}
	}

	type pair struct {
		Rule  string
		Count int
	}
	var ranked []pair
	for r, c := range ruleCount {
		ranked = append(ranked, pair{Rule: r, Count: c})
	}
	sort.SliceStable(ranked, func(i, j int) bool {
		if ranked[i].Count == ranked[j].Count {
			return ranked[i].Rule < ranked[j].Rule
		}
		return ranked[i].Count > ranked[j].Count
	})

	maxRules := 12
	if len(ranked) < maxRules {
		maxRules = len(ranked)
	}
	standards := make([]string, 0, maxRules)
	refs := make([]string, 0, maxRules)
	for i := 0; i < maxRules; i++ {
		standards = append(standards, ranked[i].Rule)
		if ref := strings.TrimSpace(ruleRef[ranked[i].Rule]); ref != "" {
			refs = append(refs, ref)
		}
	}
	return dedupeStringsPlanner(standards), dedupeStringsPlanner(refs)
}

func extractStandardsFromText(text string) []extractedStandard {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}
	lines := strings.Split(text, "\n")
	var out []extractedStandard

	mustRE := regexp.MustCompile(`(?i)\bmust\b[^.]{0,140}`)
	noRE := regexp.MustCompile(`(?i)\bno\s+(external|remote|internet|third[- ]party)[^.]{0,140}`)
	goRE := regexp.MustCompile(`(?i)\bgo[- ]?native\b|\bgo\b[^.]{0,60}\b(runtime|orchestrat|daemon|tooling)\b`)
	visionRE := regexp.MustCompile(`(?i)\blocal\s+(vision|vlm|llava|moondream)\b`)

	add := func(rule, ref string) {
		rule = normalizeConstraint(rule)
		if rule == "" {
			return
		}
		out = append(out, extractedStandard{Rule: rule, Reference: strings.TrimSpace(ref)})
	}

	for _, raw := range lines {
		ln := strings.TrimSpace(raw)
		if ln == "" {
			continue
		}
		lower := strings.ToLower(ln)
		switch {
		case strings.Contains(lower, "must be go-native"), goRE.MatchString(ln):
			add("Must be Go-native for core orchestration/runtime.", ln)
		case strings.Contains(lower, "no external api"), noRE.MatchString(ln):
			add("No external APIs in core loops unless explicitly allowlisted.", ln)
		case strings.Contains(lower, "local vision"), visionRE.MatchString(ln):
			add("Must use local Vision/VLM processing for perception tasks.", ln)
		}
		for _, m := range mustRE.FindAllString(ln, -1) {
			add(m, ln)
		}
	}
	return out
}

func mapConstraintsForPhase(phase string, constraints []string) []string {
	phase = strings.ToLower(strings.TrimSpace(phase))
	var out []string
	for _, c := range constraints {
		l := strings.ToLower(c)
		switch phase {
		case "infrastructure":
			if containsAnyPlanner(l, "go-native", "runtime", "daemon", "allowlisted", "external api") {
				out = append(out, c)
			}
		case "logic":
			if containsAnyPlanner(l, "go-native", "policy", "allowlisted", "contradiction", "safety") {
				out = append(out, c)
			}
		case "perception":
			if containsAnyPlanner(l, "vision", "vlm", "local", "external api", "allowlisted") {
				out = append(out, c)
			}
		case "interface":
			if containsAnyPlanner(l, "policy", "safety", "precision", "allowlisted", "go-native") {
				out = append(out, c)
			}
		}
	}
	if len(out) == 0 {
		out = append(out, constraints...)
	}
	if len(out) > 6 {
		out = out[:6]
	}
	return dedupeStringsPlanner(out)
}

func mapRefsForPhase(phase string, refs []string) []string {
	phase = strings.ToLower(strings.TrimSpace(phase))
	var out []string
	for _, r := range refs {
		l := strings.ToLower(r)
		switch phase {
		case "infrastructure":
			if containsAnyPlanner(l, "daemon", "runtime", "service", "policy", "api") {
				out = append(out, r)
			}
		case "logic":
			if containsAnyPlanner(l, "reason", "graph", "mcts", "policy", "audit") {
				out = append(out, r)
			}
		case "perception":
			if containsAnyPlanner(l, "vision", "ocr", "screen", "visual") {
				out = append(out, r)
			}
		case "interface":
			if containsAnyPlanner(l, "brief", "mirror", "status", "response") {
				out = append(out, r)
			}
		}
	}
	if len(out) > 4 {
		out = out[:4]
	}
	return dedupeStringsPlanner(out)
}

func phaseMilestones(phase string, idx int) []RoadmapMilestone {
	base := strings.ToLower(strings.TrimSpace(phase))
	switch base {
	case "infrastructure":
		return []RoadmapMilestone{
			{
				ID:    fmt.Sprintf("m%d_infra_runtime", idx),
				Phase: phase,
				Title: "Stable daemon/runtime baseline",
				DefinitionOfDone: []string{
					"All core daemons start cleanly and report healthy state.",
					"Router/fallback paths are bounded by latency constraints.",
				},
			},
			{
				ID:    fmt.Sprintf("m%d_infra_policy", idx),
				Phase: phase,
				Title: "Policy-safe infrastructure surface",
				DefinitionOfDone: []string{
					"No disallowed external API dependency in core loops.",
					"Admin/exec tools remain behind symbolic safety checks.",
				},
			},
		}
	case "logic":
		return []RoadmapMilestone{
			{
				ID:    fmt.Sprintf("m%d_logic_graph", idx),
				Phase: phase,
				Title: "Deterministic reasoning graph with arbitration",
				DefinitionOfDone: []string{
					"ThoughtGraph uses adaptive branching and pruning reliably.",
					"Contradictions clamp low-confidence branches before synthesis.",
				},
			},
			{
				ID:    fmt.Sprintf("m%d_logic_alignment", idx),
				Phase: phase,
				Title: "Alignment + symbolic gates hardened",
				DefinitionOfDone: []string{
					"Final outputs pass policy audit with correction loop fallback.",
					"Reflection layer records interventions and goal-drift recoveries.",
				},
			},
		}
	case "perception":
		return []RoadmapMilestone{
			{
				ID:    fmt.Sprintf("m%d_percept_grounding", idx),
				Phase: phase,
				Title: "Visual grounding + verification loop",
				DefinitionOfDone: []string{
					"Visual node correlates shell + screen evidence with coordinates.",
					"Post-fix visual verification confirms target error is cleared.",
				},
			},
			{
				ID:    fmt.Sprintf("m%d_percept_docs", idx),
				Phase: phase,
				Title: "Documentary/temporal synthesis reliability",
				DefinitionOfDone: []string{
					"Chronos timeline catches causal violations and emits alerts.",
					"Documentary pipeline links entities/signatures with confidence scores.",
				},
			},
		}
	default:
		return []RoadmapMilestone{
			{
				ID:    fmt.Sprintf("m%d_ui_handover", idx),
				Phase: phase,
				Title: "Operator-first interface + handover",
				DefinitionOfDone: []string{
					"Morning brief contains swarm delta and top anomaly.",
					"Reasoning mirror exposes concise causal/audit insights in real time.",
				},
			},
			{
				ID:    fmt.Sprintf("m%d_ui_control", idx),
				Phase: phase,
				Title: "Clear control plane for delegation and resets",
				DefinitionOfDone: []string{
					"Sub-agent delegation and hand-back are visible to the operator.",
					"HUD/system reset controls recover UI cleanly under load.",
				},
			},
		}
	}
}

func phaseWorkOrders(phase string, idx int, idea string, objective string, constraints []string) []WorkOrder {
	phaseSlug := strings.ToLower(strings.TrimSpace(phase))
	basePrompt := fmt.Sprintf(
		"Mission objective: %s\nBig idea: %s\nPhase: %s\nConstraints:\n- %s\nDeliver a production-grade implementation and verification notes.",
		objective,
		idea,
		phase,
		strings.Join(constraints, "\n- "),
	)
	switch phaseSlug {
	case "infrastructure":
		return []WorkOrder{
			{
				ID:     fmt.Sprintf("wo%d_infra_hardening", idx),
				Phase:  phase,
				Owner:  "codex",
				Prompt: basePrompt + "\nFocus: harden daemon lifecycle, restart safety, and latency-aware routing fallbacks.",
				SuccessCriteria: []string{
					"Go tests/build pass for touched packages.",
					"Health/telemetry paths remain operational under degraded mode.",
				},
			},
			{
				ID:        fmt.Sprintf("wo%d_infra_policy_surface", idx),
				Phase:     phase,
				Owner:     "talos",
				DependsOn: []string{fmt.Sprintf("wo%d_infra_hardening", idx)},
				Prompt:    basePrompt + "\nFocus: audit tool/exec surfaces against symbolic policy and enforce safe defaults.",
				SuccessCriteria: []string{
					"Blocked command/API classes are explicitly tested.",
					"No new bypass path for restricted operations.",
				},
			},
		}
	case "logic":
		return []WorkOrder{
			{
				ID:     fmt.Sprintf("wo%d_logic_planner", idx),
				Phase:  phase,
				Owner:  "codex",
				Prompt: basePrompt + "\nFocus: strengthen hierarchical planning, branch pruning, and contradiction-aware scoring.",
				SuccessCriteria: []string{
					"MCTS/ToT branches below threshold are pruned deterministically.",
					"Final synthesis picks highest verified path with explicit score basis.",
				},
			},
			{
				ID:        fmt.Sprintf("wo%d_logic_audit", idx),
				Phase:     phase,
				Owner:     "talos",
				DependsOn: []string{fmt.Sprintf("wo%d_logic_planner", idx)},
				Prompt:    basePrompt + "\nFocus: alignment correction loop with minimal utility loss and logged interventions.",
				SuccessCriteria: []string{
					"Policy violations trigger correction, not silent refusal.",
					"Audit logs include reason + corrected output metadata.",
				},
			},
		}
	case "perception":
		return []WorkOrder{
			{
				ID:     fmt.Sprintf("wo%d_percept_grounding", idx),
				Phase:  phase,
				Owner:  "codex",
				Prompt: basePrompt + "\nFocus: improve visual grounding + OCR spatial linking + verification loop closures.",
				SuccessCriteria: []string{
					"UI targets can be re-checked post-fix with coordinate validation.",
					"Visual summaries remain local-first and privacy-gated.",
				},
			},
			{
				ID:        fmt.Sprintf("wo%d_percept_timeline", idx),
				Phase:     phase,
				Owner:     "talos",
				DependsOn: []string{fmt.Sprintf("wo%d_percept_grounding", idx)},
				Prompt:    basePrompt + "\nFocus: tighten Chronos forensic timeline and anomaly confidence weighting.",
				SuccessCriteria: []string{
					"Temporal conflicts produce explicit anomaly alerts.",
					"Archivist history influences veracity weighting transparently.",
				},
			},
		}
	default:
		return []WorkOrder{
			{
				ID:     fmt.Sprintf("wo%d_iface_mirror", idx),
				Phase:  phase,
				Owner:  "talos",
				Prompt: basePrompt + "\nFocus: operator UX - concise mirrors, morning handovers, and anomaly-first callouts.",
				SuccessCriteria: []string{
					"Morning brief includes scout/capability deltas and critical anomaly action.",
					"Mirror lines are causal, concise, and non-noisy during active work.",
				},
			},
			{
				ID:        fmt.Sprintf("wo%d_iface_controls", idx),
				Phase:     phase,
				Owner:     "codex",
				DependsOn: []string{fmt.Sprintf("wo%d_iface_mirror", idx)},
				Prompt:    basePrompt + "\nFocus: add robust control hooks for delegation handback and system resets.",
				SuccessCriteria: []string{
					"Sub-agent completion events are reflected in-session.",
					"Recovery/reset hooks are one-command accessible and safe.",
				},
			},
		}
	}
}

func normalizeConstraint(s string) string {
	s = strings.TrimSpace(strings.Join(strings.Fields(s), " "))
	if s == "" {
		return ""
	}
	s = strings.TrimSuffix(s, ".")
	if !strings.HasPrefix(strings.ToUpper(s[:1]), s[:1]) {
		s = strings.ToUpper(s[:1]) + s[1:]
	}
	if !strings.HasSuffix(s, ".") {
		s += "."
	}
	return s
}

func containsAnyPlanner(s string, tokens ...string) bool {
	for _, t := range tokens {
		if strings.Contains(s, strings.ToLower(strings.TrimSpace(t))) {
			return true
		}
	}
	return false
}

func dedupeStringsPlanner(in []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(in))
	for _, v := range in {
		key := strings.ToLower(strings.TrimSpace(v))
		if key == "" || seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, strings.TrimSpace(v))
	}
	return out
}

// PersistRoadmap writes roadmap JSON to a local path (defaults to .memory/roadmap.json).
func PersistRoadmap(r Roadmap, path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		path = filepath.Join(".memory", "roadmap.json")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(path, b, 0o644); err != nil {
		return "", err
	}
	return path, nil
}
