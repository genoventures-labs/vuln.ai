package remote

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

const registryApiBaseURL = "http://85.31.233.157:8002/api/v1"
const (
	pullJobPollInterval = 3 * time.Second
	pullJobTimeout      = 20 * time.Minute
)

// GetReadAPIKey fetches the REGISTRY_READ_KEY from environment variables.
func GetReadAPIKey() (string, error) {
	key := os.Getenv("REGISTRY_READ_KEY")
	if key == "" {
		return "", fmt.Errorf("REGISTRY_READ_KEY environment variable not set")
	}
	return key, nil
}

// GetWriteAPIKey fetches the REGISTRY_WRITE_KEY from environment variables.
func GetWriteAPIKey() (string, error) {
	key := os.Getenv("REGISTRY_WRITE_KEY")
	if key == "" {
		return "", fmt.Errorf("REGISTRY_WRITE_KEY environment variable not set")
	}
	return key, nil
}

// RemoteModel represents a model installed on the remote VPS Ollama instance.
type RemoteModel struct {
	Name string `json:"name"`
	// Add other fields if the /models/local endpoint provides them (e.g., size, digest)
}

// RemoteModelListResponse is the response structure for listing local models.
type RemoteModelListResponse struct {
	Models []RemoteModel `json:"models"`
}

type pullJob struct {
	JobID     string `json:"job_id"`
	ID        string `json:"id"`
	Status    string `json:"status"`
	Error     string `json:"error"`
	ErrorMsg  string `json:"error_message"`
	LastError string `json:"last_error"`
	Model     string `json:"model"`
	ModelTag  string `json:"model_tag"`
}

// ListInstalledModelsRemote lists models installed on the remote Ollama instance via the registry API.
func ListInstalledModelsRemote() ([]RemoteModel, error) {
	apiKey, err := GetReadAPIKey()
	if err != nil {
		return nil, err
	}

	url := fmt.Sprintf("%s/models/local", registryApiBaseURL)
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Add("X-API-Key", apiKey)

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to make request to remote API: %w", err)
	}
	defer resp.Body.Close()

	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("remote API returned non-200 status: %s, body: %s", resp.Status, body)
	}

	var modelListResp RemoteModelListResponse
	if err := json.Unmarshal(body, &modelListResp); err != nil {
		return nil, fmt.Errorf("failed to unmarshal remote model list response: %w", err)
	}

	return modelListResp.Models, nil
}

// DeleteModelRemote deletes a model from the remote Ollama instance via the registry API.
func DeleteModelRemote(modelTag string) error {
	apiKey, err := GetWriteAPIKey()
	if err != nil {
		return err
	}

	endpoint := fmt.Sprintf("%s/models/local?model=%s", registryApiBaseURL, url.QueryEscape(modelTag))
	req, err := http.NewRequest("DELETE", endpoint, nil)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Add("X-API-Key", apiKey)

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to make request to remote API: %w", err)
	}
	defer resp.Body.Close()

	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read response body: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("remote API returned non-200 status for delete: %s, body: %s", resp.Status, body)
	}

	return nil
}

// PullModelRemote pulls a model to the remote Ollama instance via the registry API.
func PullModelRemote(modelTag string) error {
	apiKey, err := GetWriteAPIKey()
	if err != nil {
		return err
	}

	url := fmt.Sprintf("%s/models/local/pull-jobs", registryApiBaseURL)
	payload := map[string]string{"model": modelTag}
	jsonPayload, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal pull request payload: %w", err)
	}

	req, err := http.NewRequest("POST", url, bytes.NewBuffer(jsonPayload))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Add("Content-Type", "application/json")
	req.Header.Add("X-API-Key", apiKey)

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to make request to remote API: %w", err)
	}
	defer resp.Body.Close()

	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read response body: %w", err)
	}

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusAccepted {
		return fmt.Errorf("remote API returned non-success status for pull job enqueue: %s, body: %s", resp.Status, body)
	}

	jobID, err := parsePullJobID(body)
	if err != nil {
		return fmt.Errorf("failed to parse pull job id: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), pullJobTimeout)
	defer cancel()

	return waitForPullJob(ctx, client, apiKey, jobID, modelTag)
}

func parsePullJobID(body []byte) (string, error) {
	var pj pullJob
	if err := json.Unmarshal(body, &pj); err == nil {
		if pj.JobID != "" {
			return pj.JobID, nil
		}
		if pj.ID != "" {
			return pj.ID, nil
		}
	}

	var payload map[string]interface{}
	if err := json.Unmarshal(body, &payload); err != nil {
		return "", fmt.Errorf("invalid JSON response: %w", err)
	}

	for _, key := range []string{"job_id", "id"} {
		if v, ok := payload[key]; ok {
			if s, ok := v.(string); ok && strings.TrimSpace(s) != "" {
				return s, nil
			}
		}
	}

	return "", fmt.Errorf("job id not found in response body: %s", string(body))
}

func waitForPullJob(ctx context.Context, client *http.Client, apiKey, jobID, modelTag string) error {
	ticker := time.NewTicker(pullJobPollInterval)
	defer ticker.Stop()

	for {
		status, err := getPullJobStatus(ctx, client, apiKey, jobID)
		if err != nil {
			return err
		}

		switch normalizeJobStatus(status.Status) {
		case "completed", "succeeded", "success":
			return nil
		case "failed", "error":
			msg := firstNonEmpty(status.Error, status.ErrorMsg, status.LastError)
			if msg == "" {
				msg = "unknown error"
			}
			return fmt.Errorf("pull job %s failed for model %s: %s", jobID, modelTag, msg)
		case "canceled", "cancelled":
			return fmt.Errorf("pull job %s was canceled for model %s", jobID, modelTag)
		}

		select {
		case <-ctx.Done():
			return fmt.Errorf("timed out waiting for pull job %s for model %s", jobID, modelTag)
		case <-ticker.C:
		}
	}
}

func getPullJobStatus(ctx context.Context, client *http.Client, apiKey, jobID string) (*pullJob, error) {
	endpoint := fmt.Sprintf("%s/models/local/pull-jobs/%s", registryApiBaseURL, url.PathEscape(jobID))
	req, err := http.NewRequestWithContext(ctx, "GET", endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create pull job status request: %w", err)
	}
	req.Header.Add("X-API-Key", apiKey)

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to get pull job status: %w", err)
	}
	defer resp.Body.Close()

	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read pull job status body: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("pull job status endpoint returned %s: %s", resp.Status, body)
	}

	var status pullJob
	if err := json.Unmarshal(body, &status); err != nil {
		return nil, fmt.Errorf("failed to parse pull job status response: %w", err)
	}
	return &status, nil
}

func normalizeJobStatus(status string) string {
	return strings.ToLower(strings.TrimSpace(status))
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}
