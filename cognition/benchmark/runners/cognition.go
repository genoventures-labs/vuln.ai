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

// RunCognitionBenchmarks benchmarks structured reasoning output quality for each model.
func RunCognitionBenchmarks(models []benchmark.OllamaModel) ([]benchmark.CognitionBenchmarkResult, error) {
	fmt.Println("\nRunning cognition structure benchmarks...")
	messages := []api.Message{{
		Role: "user",
		Content: "Return JSON only with this schema for the task 'Plan secure deployment for a Go service': " +
			`{"goal":"...","steps":["..."],"risks":["..."],"decision":"..."}`,
	}}

	var results []benchmark.CognitionBenchmarkResult
	for _, model := range models {
		fmt.Printf("Benching model %s for cognition structure...\n", model.Name)
		var raw string
		res, err := benchmark.RunChatTurnBenchmark(context.Background(), model.Name, messages, func(resp string) {
			raw = resp
		})
		if err != nil {
			fmt.Printf("Error running cognition benchmark for %s: %v\n", model.Name, err)
			continue
		}
		pass, sectionScore := scoreCognitionStructure(raw)
		results = append(results, benchmark.CognitionBenchmarkResult{
			BenchmarkResult: *res,
			StructuredPass:  pass,
			SectionScore:    sectionScore,
		})
	}

	sort.Slice(results, func(i, j int) bool {
		if results[i].SectionScore == results[j].SectionScore {
			return results[i].TokensPerSecond > results[j].TokensPerSecond
		}
		return results[i].SectionScore > results[j].SectionScore
	})
	return results, nil
}

func scoreCognitionStructure(raw string) (bool, float64) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return false, 0
	}
	var payload struct {
		Goal     string   `json:"goal"`
		Steps    []string `json:"steps"`
		Risks    []string `json:"risks"`
		Decision string   `json:"decision"`
	}
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		start := strings.Index(raw, "{")
		end := strings.LastIndex(raw, "}")
		if start == -1 || end <= start {
			return false, 0
		}
		if err := json.Unmarshal([]byte(raw[start:end+1]), &payload); err != nil {
			return false, 0
		}
	}
	sections := 0.0
	if strings.TrimSpace(payload.Goal) != "" {
		sections++
	}
	if len(payload.Steps) >= 3 {
		sections++
	}
	if len(payload.Risks) >= 1 {
		sections++
	}
	if strings.TrimSpace(payload.Decision) != "" {
		sections++
	}
	score := sections / 4.0
	pass := score >= 0.75
	return pass, score
}
