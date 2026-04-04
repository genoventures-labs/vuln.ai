package benchmark

// ChatBenchmarkResult holds benchmark results for a single turn in a chat.
type ChatBenchmarkResult struct {
	BenchmarkResult
	Turn int
}

// JSONBenchmarkResult holds the JSON generation benchmark results for a single model.
type JSONBenchmarkResult struct {
	BenchmarkResult
	JSONValid bool `json:"json_valid"`
}

// ToolCallBenchmarkResult holds tool-call protocol benchmark results for a single model.
type ToolCallBenchmarkResult struct {
	BenchmarkResult
	ToolCallValid bool   `json:"tool_call_valid"`
	ToolName      string `json:"tool_name,omitempty"`
}

// CognitionBenchmarkResult holds structured cognition benchmark results for a single model.
type CognitionBenchmarkResult struct {
	BenchmarkResult
	StructuredPass bool    `json:"structured_pass"`
	SectionScore   float64 `json:"section_score"`
}

// ModelBenchmarkResults holds all benchmark results for a single model.
type ModelBenchmarkResults struct {
	ModelName          string                   `json:"model_name"`
	StandardBenchmark  BenchmarkResult          `json:"standard_benchmark"`
	JSONBenchmark      JSONBenchmarkResult      `json:"json_benchmark"`
	CodegenBenchmark   BenchmarkResult          `json:"codegen_benchmark"`
	ChatBenchmark      BenchmarkResult          `json:"chat_benchmark"`
	ToolCallBenchmark  ToolCallBenchmarkResult  `json:"tool_call_benchmark"`
	CognitionBenchmark CognitionBenchmarkResult `json:"cognition_benchmark"`
}

// ModelScore holds the calculated score for a model.
type ModelScore struct {
	ModelName string  `json:"model_name"`
	Score     float64 `json:"score"`
}
