package benchmark

import (
	"fmt"
)

// CalculateScore calculates a score for a model based on its benchmark results.
func CalculateScore(results ModelBenchmarkResults) float64 {
	// Define weights for each benchmark type
	standardWeight := 1.0
	jsonWeight := 1.0
	codegenWeight := 1.0
	chatWeight := 1.0
	toolCallWeight := 1.2
	cognitionWeight := 1.3
	jsonValidBonus := 1.2 // 20% bonus for valid JSON
	toolCallBonus := 1.15
	cognitionBonus := 1.15

	score := (results.StandardBenchmark.TokensPerSecond * standardWeight) +
		(results.JSONBenchmark.TokensPerSecond * jsonWeight) +
		(results.CodegenBenchmark.TokensPerSecond * codegenWeight) +
		(results.ChatBenchmark.TokensPerSecond * chatWeight) +
		(results.ToolCallBenchmark.TokensPerSecond * toolCallWeight) +
		(results.CognitionBenchmark.TokensPerSecond * cognitionWeight * results.CognitionBenchmark.SectionScore)

	if results.JSONBenchmark.JSONValid {
		score *= jsonValidBonus
	}
	if results.ToolCallBenchmark.ToolCallValid {
		score *= toolCallBonus
	}
	if results.CognitionBenchmark.StructuredPass {
		score *= cognitionBonus
	}

	return score
}

// Helper function to parse model size string like "4.9 GB" into float64 GB
func ParseSizeGB(sizeStr string) (float64, error) {
	var size float64
	var unit string
	_, err := fmt.Sscanf(sizeStr, "%f %s", &size, &unit)
	if err != nil {
		return 0, err
	}
	// Assuming the size is always in GB for this implementation
	return size, nil
}
