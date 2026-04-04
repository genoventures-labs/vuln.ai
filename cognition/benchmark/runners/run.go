package runners

import (
	"context"
	"fmt"
	"sort"

	"github.com/Thynaptic/P-LMv1/pkg/benchmark"
)

// RunStandardBenchmarks runs the standard benchmark against a list of models.
func RunStandardBenchmarks(models []benchmark.OllamaModel, warmup bool) ([]benchmark.BenchmarkResult, error) {
	if warmup {
		fmt.Println("\nWarming up models...")
		for _, model := range models {
			fmt.Printf("Warming up %s...\n", model.Name)
			if err := benchmark.RunWarmup(context.Background(), model.Name); err != nil {
				fmt.Printf("Error during warmup for %s: %v\n", model.Name, err)
			}
		}
		fmt.Println("Warm-up complete.")
	}

	fmt.Println("\nRunning standard benchmarks...")
	prompt := "What is the meaning of life?"
	var results []benchmark.BenchmarkResult

	for _, model := range models {
		fmt.Printf("Benching model %s...\n", model.Name)
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
