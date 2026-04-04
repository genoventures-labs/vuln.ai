package router

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"sort"
	"strings"
	"time"
)

// Router handles model selection based on prompt complexity.
type Router struct {
	Models []string
}

type oriModelsResponse struct {
	Data []struct {
		ID string `json:"id"`
	} `json:"data"`
}

// NewRouter initializes a Router with live model discovery from ORI API.
func NewRouter() (*Router, error) {
	models, err := discoverModels()
	if err != nil {
		// Fall back to known default rather than failing hard
		return &Router{Models: []string{"qwen3:1.7b"}}, nil
	}
	return &Router{Models: models}, nil
}

// SelectModel chooses the best model for the given prompt.
func (r *Router) SelectModel(prompt string) string {
	if len(r.Models) == 0 {
		return ""
	}

	promptLower := strings.ToLower(prompt)

	// Hard tasks keywords
	hardKeywords := []string{"code", "python", "script", "function", "json", "format", "analyze", "calculate", "solve", "complex"}

	// Tool usage keywords
	toolKeywords := []string{"search", "weather", "latest", "news", "fetch", "url", "http"}

	isHard := false
	for _, kw := range hardKeywords {
		if strings.Contains(promptLower, kw) {
			isHard = true
			break
		}
	}

	needsTools := false
	for _, kw := range toolKeywords {
		if strings.Contains(promptLower, kw) {
			needsTools = true
			break
		}
	}

	// Heuristics
	if isHard || needsTools || len(prompt) > 250 {
		// Use the 2nd best model (usually more capable than the absolute fastest 1b model)
		// Or 3rd if available. Let's say index 1 or 2.
		if len(r.Models) > 1 {
			return r.Models[1] // qwen2.5:3b-instruct in our case
		}
	}

	// Default to the fastest model
	return r.Models[0] // llama3.2:1b in our case
}

const (
	defaultOrchestrationBaseURL = "http://localhost:8089/v1"
	defaultOllamaHost           = "http://localhost:8089"
	defaultResolveTimeout       = 6 * time.Second
	defaultTagsTimeout          = 6 * time.Second
)

// ResolveRequest carries remote orchestration routing hints.
type ResolveRequest struct {
	Query        string   `json:"query,omitempty"`
	Stage        string   `json:"stage,omitempty"`
	MaxLatencyMS int      `json:"max_latency_ms,omitempty"`
	Models       []string `json:"models,omitempty"`
}

// ResolveModel attempts orchestration routing and falls back to local selection.
func (r *Router) ResolveModel(req ResolveRequest) string {
	if model, err := ResolveRemote(req); err == nil && strings.TrimSpace(model) != "" {
		return model
	}
	return r.SelectModel(req.Query)
}

// ResolveRemote calls the orchestration router endpoint.
func ResolveRemote(req ResolveRequest) (string, error) {
	baseURL := strings.TrimRight(strings.TrimSpace(os.Getenv("ORCHESTRATION_BASE_URL")), "/")
	if baseURL == "" {
		baseURL = defaultOrchestrationBaseURL
	}

	fullURL := baseURL + path.Clean("/orchestration/router/resolve")
	body, err := json.Marshal(req)
	if err != nil {
		return "", fmt.Errorf("marshal resolve request: %w", err)
	}

	httpReq, err := http.NewRequest(http.MethodPost, fullURL, bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("create resolve request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if apiKey := strings.TrimSpace(os.Getenv("REGISTRY_READ_KEY")); apiKey != "" {
		httpReq.Header.Set("X-API-Key", apiKey)
	}

	client := &http.Client{Timeout: defaultResolveTimeout}
	resp, err := client.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("request resolve endpoint: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return "", fmt.Errorf("resolve endpoint status %d: %s", resp.StatusCode, strings.TrimSpace(string(msg)))
	}

	var payload map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return "", fmt.Errorf("decode resolve response: %w", err)
	}
	for _, key := range []string{"model", "model_name", "model_identifier", "resolved_model"} {
		if v, ok := payload[key].(string); ok && strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v), nil
		}
	}
	if route, ok := payload["route"].(map[string]interface{}); ok {
		for _, key := range []string{"model", "model_name", "model_identifier", "resolved_model"} {
			if v, ok := route[key].(string); ok && strings.TrimSpace(v) != "" {
				return strings.TrimSpace(v), nil
			}
		}
	}
	return "", fmt.Errorf("resolve response did not include model field")
}

func discoverModels() ([]string, error) {
	host := strings.TrimSpace(os.Getenv("AI_BASE_URL"))
	if host == "" {
		host = defaultOllamaHost
	}
	if !strings.HasPrefix(host, "http://") && !strings.HasPrefix(host, "https://") {
		host = "http://" + host
	}
	host = strings.TrimRight(host, "/")
	url := host + "/v1/models"

	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("create model discovery request: %w", err)
	}
	apiKey := strings.TrimSpace(os.Getenv("AI_API_KEY"))
	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}
	client := &http.Client{Timeout: defaultTagsTimeout}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("discover models from %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, fmt.Errorf("discover models status %d: %s", resp.StatusCode, strings.TrimSpace(string(msg)))
	}
	var payload oriModelsResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, fmt.Errorf("decode models response: %w", err)
	}
	seen := make(map[string]bool)
	var models []string
	for _, m := range payload.Data {
		name := strings.TrimSpace(m.ID)
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		models = append(models, name)
	}
	if len(models) == 0 {
		return nil, fmt.Errorf("no models returned by %s", url)
	}
	sort.Strings(models)
	return models, nil
}
