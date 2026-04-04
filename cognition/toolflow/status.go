package toolflow

import (
	"fmt"
	"strings"
)

// FormatASCIIStatus renders concise status for toolflow execution.
func FormatASCIIStatus(plan Plan, trace ExecutionTrace, results []ToolResult, maxWorkers int) string {
	if maxWorkers <= 0 {
		maxWorkers = 1
	}
	total := len(plan.Nodes)
	ok := 0
	errCount := 0
	retriesUsed := 0
	for _, r := range results {
		if r.Err != nil {
			errCount++
		} else {
			ok++
		}
		if r.Attempts > 1 {
			retriesUsed += (r.Attempts - 1)
		}
	}
	loadPct := 0
	if total > 0 {
		loadPct = int(float64(ok+errCount) / float64(total) * 100)
	}
	activeShards := minInt(total, maxWorkers)

	var b strings.Builder
	b.WriteString("+------------------ TOOLFLOW STATUS ------------------+\n")
	b.WriteString(fmt.Sprintf("| Plan Hash      : %-34s |\n", truncate(plan.DeterministicHash, 34)))
	b.WriteString(fmt.Sprintf("| Cognitive Load : %3d%%                                     |\n", loadPct))
	b.WriteString(fmt.Sprintf("| Active Shards  : %3d                                      |\n", activeShards))
	b.WriteString(fmt.Sprintf("| Nodes          : total=%-3d ok=%-3d err=%-3d                |\n", total, ok, errCount))
	b.WriteString(fmt.Sprintf("| Plan Shape     : groups=%-3d inferred_edges=%-3d             |\n", maxInt(1, plan.ParallelGroups), plan.InferredEdgeCount))
	b.WriteString(fmt.Sprintf("| Retry Budget   : retries_used=%-3d                         |\n", retriesUsed))
	b.WriteString(fmt.Sprintf("| Citations      : %-3d                                      |\n", len(trace.Citations)))
	b.WriteString("+------------------------------------------------------+\n")
	return b.String()
}

func truncate(s string, n int) string {
	s = strings.TrimSpace(s)
	if n <= 0 || len(s) <= n {
		return s
	}
	if n <= 3 {
		return s[:n]
	}
	return s[:n-3] + "..."
}
