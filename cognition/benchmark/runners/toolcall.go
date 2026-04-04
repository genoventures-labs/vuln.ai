package runners

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/Thynaptic/P-LMv1/pkg/benchmark"
	"github.com/ollama/ollama/api"
)

// RunToolCallBenchmarks benchmarks tool-call schema compliance for each model.
func RunToolCallBenchmarks(models []benchmark.OllamaModel) ([]benchmark.ToolCallBenchmarkResult, error) {
	fmt.Println("\nRunning tool-call compliance benchmarks...")
	messages := []api.Message{{
		Role: "user",
		Content: "Return JSON only with a single tool call for current info lookup: " +
			`{"tool":"web_search","args":{"query":"bitcoin digital asset"}}`,
	}}

	var results []benchmark.ToolCallBenchmarkResult
	for _, model := range models {
		fmt.Printf("Benching model %s for tool-call compliance...\n", model.Name)
		var raw string
		res, err := benchmark.RunChatTurnBenchmark(context.Background(), model.Name, messages, func(resp string) {
			raw = resp
		})
		if err != nil {
			fmt.Printf("Error running tool-call benchmark for %s: %v\n", model.Name, err)
			continue
		}
		ok, tool := validateToolCallResponse(raw)
		results = append(results, benchmark.ToolCallBenchmarkResult{
			BenchmarkResult: *res,
			ToolCallValid:   ok,
			ToolName:        tool,
		})
	}

	sort.Slice(results, func(i, j int) bool {
		if results[i].ToolCallValid == results[j].ToolCallValid {
			return results[i].TokensPerSecond > results[j].TokensPerSecond
		}
		return results[i].ToolCallValid
	})
	return results, nil
}

func validateToolCallResponse(raw string) (bool, string) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return false, ""
	}
	type call struct {
		Tool string                 `json:"tool"`
		Args map[string]interface{} `json:"args"`
	}
	var c call
	if err := json.Unmarshal([]byte(raw), &c); err != nil {
		// Try to extract first JSON object from wrapped text.
		start := strings.Index(raw, "{")
		end := strings.LastIndex(raw, "}")
		if start == -1 || end <= start {
			return false, ""
		}
		if err := json.Unmarshal([]byte(raw[start:end+1]), &c); err != nil {
			return false, ""
		}
	}
	tool := strings.TrimSpace(strings.ToLower(c.Tool))
	if tool == "" {
		return false, ""
	}
	query, _ := c.Args["query"].(string)
	if strings.TrimSpace(query) == "" {
		return false, tool
	}
	return true, tool
}
