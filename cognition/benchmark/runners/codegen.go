package runners

import (
	"context"
	"fmt"
	"sort"

	"github.com/Thynaptic/P-LMv1/pkg/benchmark"
)

// RunCodegenBenchmarks runs the code generation benchmark against a list of models.
func RunCodegenBenchmarks(models []benchmark.OllamaModel) ([]benchmark.BenchmarkResult, error) {
	fmt.Println("\nRunning code generation benchmarks...")
	prompt := "Write a Python function that takes a list of integers and returns the sum of all even numbers in the list."
	var results []benchmark.BenchmarkResult

	for _, model := range models {
		fmt.Printf("Benching model %s for code generation...\n", model.Name)

		res, err := benchmark.RunBenchmark(context.Background(), model.Name, prompt)

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
