package toolflow

import (
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// PlanOptions controls compilation behavior.
type PlanOptions struct {
	SequentialDefault bool
}

// BuildDeterministicPlan keeps compatibility with existing callers.
func BuildDeterministicPlan(invocations []NormalizedInvocation, opts PlanOptions) (Plan, error) {
	return BuildDeterministicPlanV2(invocations, opts, PlannerHints{})
}

// BuildDeterministicPlanV2 compiles invocations into a deterministic DAG with dependency inference.
func BuildDeterministicPlanV2(invocations []NormalizedInvocation, opts PlanOptions, hints PlannerHints) (Plan, error) {
	if len(invocations) == 0 {
		return Plan{}, fmt.Errorf("no invocations provided")
	}
	nodes := make([]PlanNode, 0, len(invocations))
	ordered := make([]string, 0, len(invocations))
	inferredEdges := 0

	for i, inv := range invocations {
		id := fmt.Sprintf("n%02d_%s", i+1, inv.Tool)
		node := PlanNode{
			ID:        id,
			Tool:      inv.Tool,
			Args:      inv.Args,
			Priority:  1000 - i,
			CostClass: classifyCostClass(inv.Tool),
		}
		if opts.SequentialDefault && i > 0 {
			node.DependsOn = []string{nodes[i-1].ID}
		}
		if dep, ok := inv.Args["depends_on"]; ok {
			node.DependsOn = parseDepends(dep)
		}
		nodes = append(nodes, node)
		ordered = append(ordered, id)
	}

	// Infer dependency edges only when caller didn't provide explicit depends_on.
	for i := range nodes {
		if len(nodes[i].DependsOn) > 0 {
			continue
		}
		inferred := inferDependencies(nodes, i)
		if len(inferred) > 0 {
			nodes[i].DependsOn = append([]string(nil), inferred...)
			nodes[i].InferredDependsOn = append([]string(nil), inferred...)
			inferredEdges += len(inferred)
		}
	}

	if err := validateDependencies(nodes); err != nil {
		return Plan{}, err
	}
	groups := estimateParallelGroups(nodes)
	hash := deterministicPlanHash(nodes, ordered)
	return Plan{
		Nodes:             nodes,
		OrderedIDs:        ordered,
		DeterministicHash: hash,
		ParallelGroups:    groups,
		InferredEdgeCount: inferredEdges,
	}, nil
}

func parseDepends(v interface{}) []string {
	out := []string{}
	switch t := v.(type) {
	case []string:
		out = append(out, t...)
	case []interface{}:
		for _, raw := range t {
			if s, ok := raw.(string); ok && strings.TrimSpace(s) != "" {
				out = append(out, strings.TrimSpace(s))
			}
		}
	case string:
		parts := strings.Split(t, ",")
		for _, p := range parts {
			p = strings.TrimSpace(p)
			if p != "" {
				out = append(out, p)
			}
		}
	}
	return uniqueStrings(out)
}

func inferDependencies(nodes []PlanNode, idx int) []string {
	if idx <= 0 || idx >= len(nodes) {
		return nil
	}
	cur := nodes[idx]
	deps := []string{}

	// 1) Placeholder-based references to prior node IDs.
	for _, dep := range findNodeReferencesInArgs(cur.Args) {
		if nodeExistsBefore(nodes, idx, dep) {
			deps = append(deps, dep)
		}
	}

	// 2) Semantic inference by tool shape.
	switch cur.Tool {
	case "fetch_url", "http_request":
		if argEmpty(cur.Args, "url") {
			if prev := lastPriorTool(nodes, idx, "web_search"); prev != "" {
				deps = append(deps, prev)
			}
		}
	case "vector_retrieve":
		if argEmpty(cur.Args, "query") {
			if prev := lastPriorTool(nodes, idx, "web_search"); prev != "" {
				deps = append(deps, prev)
			}
		}
	}

	// 3) Shared target hazard: serialize write-like operations to same target.
	if target := mutableTarget(cur.Args); target != "" {
		for i := idx - 1; i >= 0; i-- {
			if mutableTarget(nodes[i].Args) == target {
				deps = append(deps, nodes[i].ID)
				break
			}
		}
	}

	return uniqueStrings(deps)
}

var refNodeRE = regexp.MustCompile(`n\d{2}_[a-z0-9_]+`)

func findNodeReferencesInArgs(args map[string]interface{}) []string {
	var out []string
	var walk func(v interface{})
	walk = func(v interface{}) {
		switch t := v.(type) {
		case string:
			for _, m := range refNodeRE.FindAllString(strings.ToLower(t), -1) {
				out = append(out, strings.TrimSpace(m))
			}
		case []interface{}:
			for _, e := range t {
				walk(e)
			}
		case map[string]interface{}:
			for _, e := range t {
				walk(e)
			}
		}
	}
	walk(args)
	return uniqueStrings(out)
}

func nodeExistsBefore(nodes []PlanNode, idx int, id string) bool {
	for i := 0; i < idx; i++ {
		if nodes[i].ID == id {
			return true
		}
	}
	return false
}

func lastPriorTool(nodes []PlanNode, idx int, tool string) string {
	for i := idx - 1; i >= 0; i-- {
		if nodes[i].Tool == tool {
			return nodes[i].ID
		}
	}
	return ""
}

func argEmpty(args map[string]interface{}, key string) bool {
	v, ok := args[key]
	if !ok || v == nil {
		return true
	}
	s, ok := v.(string)
	return ok && strings.TrimSpace(s) == ""
}

func mutableTarget(args map[string]interface{}) string {
	for _, k := range []string{"output_path", "path", "file", "target"} {
		if v, ok := args[k]; ok {
			if s, ok := v.(string); ok {
				s = strings.TrimSpace(strings.ToLower(s))
				if s != "" {
					return s
				}
			}
		}
	}
	return ""
}

func classifyCostClass(tool string) string {
	switch tool {
	case "web_search", "fetch_url", "http_request", "vector_retrieve":
		return "network"
	case "execute_code", "sys_exec":
		return "cpu"
	default:
		return "local_io"
	}
}

func validateDependencies(nodes []PlanNode) error {
	nodesByID := map[string]PlanNode{}
	for _, n := range nodes {
		nodesByID[n.ID] = n
	}
	for _, n := range nodes {
		for _, dep := range n.DependsOn {
			if _, ok := nodesByID[dep]; !ok {
				return fmt.Errorf("invalid dependency: node=%s depends_on=%s not found", n.ID, dep)
			}
		}
	}
	return nil
}

func estimateParallelGroups(nodes []PlanNode) int {
	levels := map[string]int{}
	maxLevel := 0
	for _, n := range nodes {
		level := 0
		for _, dep := range n.DependsOn {
			if dl := levels[dep] + 1; dl > level {
				level = dl
			}
		}
		levels[n.ID] = level
		if level > maxLevel {
			maxLevel = level
		}
	}
	return maxLevel + 1
}

func deterministicPlanHash(nodes []PlanNode, ordered []string) string {
	parts := make([]string, 0, len(nodes)+1)
	parts = append(parts, strings.Join(ordered, ","))
	sorted := append([]PlanNode(nil), nodes...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].ID < sorted[j].ID })
	for _, n := range sorted {
		parts = append(parts,
			n.ID+"|"+n.Tool+"|"+strings.Join(n.DependsOn, ",")+"|"+n.CostClass+"|"+fmt.Sprintf("%d", n.Priority),
		)
	}
	s := strings.Join(parts, "\n")
	sum := sha1.Sum([]byte(s))
	return hex.EncodeToString(sum[:])
}

func uniqueStrings(in []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(in))
	for _, v := range in {
		v = strings.TrimSpace(v)
		if v == "" || seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	sort.Strings(out)
	return out
}
