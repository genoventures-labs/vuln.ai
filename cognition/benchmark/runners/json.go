package runners

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/Thynaptic/P-LMv1/pkg/benchmark"
)

// RunJSONBenchmarks runs the JSON generation benchmark against a list of models.
func RunJSONBenchmarks(models []benchmark.OllamaModel) ([]benchmark.JSONBenchmarkResult, error) {
	fmt.Println("\nRunning JSON generation benchmarks...")
	prompt := "Generate a JSON object representing a user with the following properties: 'name' (string), 'age' (number), and 'is_active' (boolean)."
	var results []benchmark.JSONBenchmarkResult

	for _, model := range models {
		fmt.Printf("Benching model %s for JSON generation...\n", model.Name)

		var fullResponse string
		res, err := benchmark.RunBenchmark(context.Background(), model.Name, prompt, func(resp string) {
			fullResponse = resp
		})

		if err != nil {
			fmt.Printf("Error running benchmark for %s: %v\n", model.Name, err)
			continue
		}

		// Validate the JSON
		var jsonData map[string]interface{}
		err = json.Unmarshal([]byte(fullResponse), &jsonData)
		isValid := err == nil

		results = append(results, benchmark.JSONBenchmarkResult{
			BenchmarkResult: *res,
			JSONValid:       isValid,
		})
	}

	// Sort results by TokensPerSecond in descending order
	sort.Slice(results, func(i, j int) bool {
		return results[i].BenchmarkResult.TokensPerSecond > results[j].BenchmarkResult.TokensPerSecond
	})

	return results, nil
}
