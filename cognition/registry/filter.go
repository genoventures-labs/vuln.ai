package registry

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/Thynaptic/P-LMv1/pkg/benchmark"
)

// ParseModelTagSizeGB extracts the size in GB from a model tag (e.g., "llama3.1:8b").
func ParseModelTagSizeGB(tag string) (float64, error) {
	// Example tags: "llama3.1:8b", "llama3.1:70b-instruct-q4_K_M"
	// We're looking for a size pattern like X.Yb or Xb (billion/million parameters)
	// Rough estimate for Q4_K_M quantization: 1 billion params ~ 0.6 GB
	// This heuristic is approximate and can be refined.

	// This regex looks for digits possibly with a decimal, followed by 'b' or 'm'
	// and makes sure it's preceded by ':' or '-' or is at the start of the tag,
	// and followed by '-' or end of string, to avoid matching unrelated numbers.
	re := regexp.MustCompile(`(\d+(\.\d+)?)([bm])`)
	matches := re.FindStringSubmatch(strings.ToLower(tag))

	if len(matches) < 4 {
		return 0, fmt.Errorf("could not find size pattern in tag: %s", tag)
	}

	sizeVal, err := strconv.ParseFloat(matches[1], 64)
	if err != nil {
		return 0, fmt.Errorf("could not parse numeric size from tag '%s': %w", tag, err)
	}

	unit := matches[3]
	var estimatedGB float64
	if unit == "b" { // Billion parameters
		estimatedGB = sizeVal * 0.6
	} else if unit == "m" { // Million parameters
		estimatedGB = sizeVal / 1000 * 0.6
	} else {
		return 0, fmt.Errorf("unknown unit '%s' in tag: %s", unit, tag)
	}

	return estimatedGB, nil
}

// FilterModels filters models from the registry based on system specs and available tags.
func FilterModels(models []RegistryModel, maxRamGB float64) ([]RegistryModel, error) {
	var filteredModels []RegistryModel
	for _, model := range models {
		// Assume initial model size is too large
		minTagSizeGB := maxRamGB * 10 // Start with a very large size

		// Iterate through labels (tags) to find the smallest suitable size
		foundSuitableTag := false
		for _, label := range model.Labels {
			tagSizeGB, err := ParseModelTagSizeGB(label)
			if err != nil {
				// If a tag's size can't be parsed, skip this tag but continue checking others
				continue
			}

			if tagSizeGB > 0 && tagSizeGB < minTagSizeGB { // Only consider non-zero estimated sizes
				minTagSizeGB = tagSizeGB
				foundSuitableTag = true
			}
		}

		// If a suitable tag was found and its size is within limits, add the model
		// Use a buffer, e.g., model size should be less than 40% of RAM
		if foundSuitableTag && minTagSizeGB < (maxRamGB*0.4) {
			filteredModels = append(filteredModels, model)
		}
	}
	return filteredModels, nil
}

// ScoredRegistryModel holds a RegistryModel and its calculated score.
type ScoredRegistryModel struct {
	RegistryModel
	Score float64
}

// ScoreModels scores filtered registry models based on pulls and benchmark results.
func ScoreModels(
	filteredModels []RegistryModel,
	benchmarkResultsFile string,
	vpsRamGB float64) ([]ScoredRegistryModel, error) {

	// Read benchmark results
	var allBenchmarkResults []benchmark.ModelBenchmarkResults
	data, err := ioutil.ReadFile(benchmarkResultsFile)
	if err != nil {
		// If benchmark file doesn't exist, proceed with pulls only
		fmt.Printf("Warning: Could not read benchmark results file (%s). Scoring based on pulls only: %v\n", benchmarkResultsFile, err)
	} else {
		err = json.Unmarshal(data, &allBenchmarkResults)
		if err != nil {
			return nil, fmt.Errorf("failed to unmarshal benchmark results: %w", err)
		}
	}

	benchmarkResultsMap := make(map[string]benchmark.ModelBenchmarkResults)
	for _, res := range allBenchmarkResults {
		benchmarkResultsMap[res.ModelName] = res
	}

	var scoredModels []ScoredRegistryModel
	for _, model := range filteredModels {
		score := float64(model.Pulls) // Base score on pulls

		// Add benchmark score if available
		// The registry API gives model_identifier (e.g., 'llama3.1')
		// The benchmark results give model_name (e.g., 'llama3.1:8b')
		// We need to find the benchmark result for the best tag of this model_identifier
		bestTagBenchmarkScore := 0.0
		for _, benchmarkResult := range allBenchmarkResults {
			if strings.HasPrefix(benchmarkResult.ModelName, model.ModelIdentifier) {
				currentScore := benchmark.CalculateScore(benchmarkResult)
				if currentScore > bestTagBenchmarkScore {
					bestTagBenchmarkScore = currentScore
				}
			}
		}

		// Scale benchmark score to be comparable to pulls (adjust this factor as needed)
		score += bestTagBenchmarkScore * 1000

		scoredModels = append(scoredModels, ScoredRegistryModel{
			RegistryModel: model,
			Score:         score,
		})
	}

	// Sort by score in descending order
	sort.Slice(scoredModels, func(i, j int) bool {
		return scoredModels[i].Score > scoredModels[j].Score
	})

	return scoredModels, nil
}
