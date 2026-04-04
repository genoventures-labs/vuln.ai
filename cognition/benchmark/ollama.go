package benchmark

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/ollama/ollama/api"
)

// OllamaModel represents an installed ollama model.
type OllamaModel struct {
	Name     string
	Size     string
	Modified string
}

// BenchmarkResult holds the benchmark results for a single model.
type BenchmarkResult struct {
	ModelName        string
	TimeToFirstToken time.Duration
	TotalTime        time.Duration
	TokensPerSecond  float64
	TotalTokens      int
}

// GetOllamaModels executes `ollama list` and returns a slice of OllamaModel.
func GetOllamaModels() ([]OllamaModel, error) {
	client, err := api.ClientFromEnvironment()
	if err != nil {
		return nil, fmt.Errorf("failed to create ollama client: %w", err)
	}

	modelsResponse, err := client.List(context.Background())
	if err != nil {
		return nil, fmt.Errorf("failed to list ollama models: %w", err)
	}

	var models []OllamaModel
	for _, model := range modelsResponse.Models {
		models = append(models, OllamaModel{
			Name:     model.Name,
			Size:     fmt.Sprintf("%.1f GB", float64(model.Size)/(1024*1024*1024)), // Convert bytes to GB
			Modified: model.ModifiedAt.Format("2006-01-02 15:04:05"),
		})
	}

	return models, nil
}

// RunBenchmark runs a benchmark on a single model.
func RunBenchmark(ctx context.Context, modelName string, prompt string, callbacks ...func(string)) (*BenchmarkResult, error) {
	client, err := api.ClientFromEnvironment()
	if err != nil {
		return nil, fmt.Errorf("failed to create ollama client: %w", err)
	}

	req := &api.GenerateRequest{
		Model:  modelName,
		Prompt: prompt,
	}

	startTime := time.Now()
	var firstTokenTime time.Time
	var firstToken bool
	var fullResponse strings.Builder

	result := &BenchmarkResult{
		ModelName: modelName,
	}

	err = client.Generate(ctx, req, func(resp api.GenerateResponse) error {
		if !firstToken {
			firstTokenTime = time.Now()
			firstToken = true
			result.TimeToFirstToken = firstTokenTime.Sub(startTime)
		}
		fullResponse.WriteString(resp.Response)
		result.TotalTokens++
		return nil
	})

	if err != nil {
		if err == context.Canceled {
			return nil, err
		}
		return nil, fmt.Errorf("failed to run benchmark: %w", err)
	}

	endTime := time.Now()
	result.TotalTime = endTime.Sub(startTime)
	if result.TotalTime.Seconds() > 0 {
		result.TokensPerSecond = float64(result.TotalTokens) / result.TotalTime.Seconds()
	}

	if len(callbacks) > 0 {
		callbacks[0](fullResponse.String())
	}

	return result, nil
}

// RunChatTurnBenchmark runs a single chat turn benchmark and captures timing/throughput.
func RunChatTurnBenchmark(ctx context.Context, modelName string, messages []api.Message, callbacks ...func(string)) (*BenchmarkResult, error) {
	client, err := api.ClientFromEnvironment()
	if err != nil {
		return nil, fmt.Errorf("failed to create ollama client: %w", err)
	}

	req := &api.ChatRequest{
		Model:    modelName,
		Messages: messages,
	}

	startTime := time.Now()
	var firstTokenTime time.Time
	var firstToken bool
	var fullResponse strings.Builder

	result := &BenchmarkResult{
		ModelName: modelName,
	}

	err = client.Chat(ctx, req, func(resp api.ChatResponse) error {
		if !firstToken {
			firstTokenTime = time.Now()
			firstToken = true
			result.TimeToFirstToken = firstTokenTime.Sub(startTime)
		}
		fullResponse.WriteString(resp.Message.Content)
		return nil
	})
	if err != nil {
		if err == context.Canceled {
			return nil, err
		}
		return nil, fmt.Errorf("failed to run chat turn benchmark: %w", err)
	}

	endTime := time.Now()
	result.TotalTime = endTime.Sub(startTime)
	// Rough token approximation; Ollama stream callback does not expose token counts directly.
	result.TotalTokens = len(strings.Fields(fullResponse.String()))
	if result.TotalTime.Seconds() > 0 {
		result.TokensPerSecond = float64(result.TotalTokens) / result.TotalTime.Seconds()
	}

	if len(callbacks) > 0 {
		callbacks[0](fullResponse.String())
	}
	return result, nil
}

// RunWarmup sends a series of simple prompts to the model to warm it up.
func RunWarmup(ctx context.Context, modelName string) error {
	client, err := api.ClientFromEnvironment()
	if err != nil {
		return fmt.Errorf("failed to create ollama client: %w", err)
	}

	warmupPrompts := []string{
		"Hello",
		"What is 1 + 1?",
		"Translate 'hello' to French",
	}

	for _, prompt := range warmupPrompts {
		req := &api.GenerateRequest{
			Model:  modelName,
			Prompt: prompt,
		}
		err := client.Generate(ctx, req, func(resp api.GenerateResponse) error {
			// We don't need to do anything with the response, just consume it.
			return nil
		})
		if err != nil {
			if err == context.Canceled {
				return err
			}
			return fmt.Errorf("failed to run warmup prompt '%s': %w", prompt, err)
		}
	}

	return nil
}

// RunChatBenchmark runs a benchmark on a single model with a series of messages.
func RunChatBenchmark(ctx context.Context, modelName string, messages []api.Message) (*BenchmarkResult, error) {
	client, err := api.ClientFromEnvironment()
	if err != nil {
		return nil, fmt.Errorf("failed to create ollama client: %w", err)
	}

	totalTokens := 0
	totalTime := time.Duration(0)
	var conversation []api.Message

	for i, msg := range messages {
		conversation = append(conversation, msg)

		req := &api.ChatRequest{
			Model:    modelName,
			Messages: conversation,
		}

		startTime := time.Now()
		var firstToken bool
		var fullResponse strings.Builder
		turnTokens := 0

		err := client.Chat(ctx, req, func(resp api.ChatResponse) error {
			if !firstToken {
				firstToken = true
			}
			fullResponse.WriteString(resp.Message.Content)
			// This is a rough token count, as the API doesn't give us token count per response chunk.
			// We will get the final token count from the response object.
			return nil
		})
		if err != nil {
			return nil, fmt.Errorf("error on turn %d: %w", i+1, err)
		}

		endTime := time.Now()
		totalTime += endTime.Sub(startTime)

		// Create the assistant's response message and add it to the conversation
		assistantMsg := api.Message{
			Role:    "assistant",
			Content: fullResponse.String(),
		}
		conversation = append(conversation, assistantMsg)
		// We can get the actual number of tokens from the response, but ChatResponse doesn't provide it.
		// We'll have to rely on a rough estimate for now.
		// A more accurate way would be to use a local tokenizer, but that's a larger change.
		turnTokens = len(strings.Fields(fullResponse.String())) // A rough approximation
		totalTokens += turnTokens
	}

	result := &BenchmarkResult{
		ModelName:   modelName,
		TotalTime:   totalTime,
		TotalTokens: totalTokens,
	}
	if totalTime.Seconds() > 0 {
		result.TokensPerSecond = float64(totalTokens) / totalTime.Seconds()
	}

	return result, nil
}
