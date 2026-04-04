package runners

import (
	"context"
	"fmt"
	"sort"

	"github.com/Thynaptic/P-LMv1/pkg/benchmark"
	"github.com/ollama/ollama/api"
)

// RunChatBenchmarks runs the chat benchmark against a list of models.
func RunChatBenchmarks(models []benchmark.OllamaModel) ([]benchmark.BenchmarkResult, error) {
	fmt.Println("\nRunning chat benchmarks...")
	// A simple 3-turn conversation script
	script := []api.Message{
		{
			Role:    "user",
			Content: "My name is John. I am a software engineer.",
		},
		{
			Role:    "user",
			Content: "What is my name and what do I do?",
		},
		{
			Role:    "user",
			Content: "Based on my profession, what are three things I might be interested in?",
		},
	}

	var results []benchmark.BenchmarkResult

	for _, model := range models {
		fmt.Printf("Benching model %s for chat...\n", model.Name)

		res, err := benchmark.RunChatBenchmark(context.Background(), model.Name, script)

		if err != nil {
			fmt.Printf("Error running benchmark for %s: %v\n", model.Name, err)
			continue
		}
		results = append(results, *res)
	}

	// Sort results by TokensPerSecond in descending order
	sort.Slice(results, func(i, j int) bool {
		return results[i].TokensPerSecond > results[j].TokensPerSecond
	})

	return results, nil
}
