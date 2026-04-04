package registry

import (
	"encoding/json"
	"fmt"
	"net/http"
)

const registryBaseURL = "http://85.31.233.157:8002/api/v1"

// RegistryModel represents a model from the external registry.
type RegistryModel struct {
	ModelIdentifier string   `json:"model_identifier"`
	Namespace       string   `json:"namespace"`
	ModelName       string   `json:"model_name"`
	ModelType       string   `json:"model_type"`
	Description     string   `json:"description"`
	Capability      string   `json:"capability"`
	Labels          []string `json:"labels"`
	Pulls           int      `json:"pulls"`
	Tags            int      `json:"tags"` // This is tags count now
	LastUpdated     string   `json:"last_updated"`
	URL             string   `json:"url"`
}

// RegistryResponse is the response from the registry API.
type RegistryResponse struct {
	Models     []RegistryModel `json:"models"`
	TotalCount int             `json:"total_count"`
	Limit      int             `json:"limit"`
	Skip       int             `json:"skip"`
}

// GetModels fetches all models from the registry, handling pagination.
func GetModels() ([]RegistryModel, error) {
	var allModels []RegistryModel
	limit := 100 // The API might have a max limit, 100 is a reasonable guess
	skip := 0

	for {
		url := fmt.Sprintf("%s/models?limit=%d&skip=%d", registryBaseURL, limit, skip)
		resp, err := http.Get(url)
		if err != nil {
			return nil, fmt.Errorf("failed to get models from registry: %w", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("registry API returned non-200 status: %s", resp.Status)
		}

		var registryResp RegistryResponse
		if err := json.NewDecoder(resp.Body).Decode(&registryResp); err != nil {
			return nil, fmt.Errorf("failed to decode registry response: %w", err)
		}

		allModels = append(allModels, registryResp.Models...)

		if len(allModels) >= registryResp.TotalCount {
			break // Fetched all models
		}

		skip += limit
	}

	return allModels, nil
}
