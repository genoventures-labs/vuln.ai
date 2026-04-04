package db

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
)

type PocketbaseClient struct {
	BaseURL    string
	Identity   string
	Password   string
	Collection string
	Token      string
}

type AuthRequest struct {
	Identity string `json:"identity"`
	Password string `json:"password"`
}

type AuthResponse struct {
	Token string `json:"token"`
}

func NewPocketbaseClient(baseURL string, identity string, password string, authCollection string) *PocketbaseClient {
	return &PocketbaseClient{
		BaseURL:    baseURL,
		Identity:   identity,
		Password:   password,
		Collection: authCollection,
	}
}

func (c *PocketbaseClient) Authenticate() error {
	if c.BaseURL == "" || c.Identity == "" || c.Password == "" {
		return fmt.Errorf("pocketbase credentials not fully configured")
	}

	url := fmt.Sprintf("%s/api/collections/%s/auth-with-password", c.BaseURL, c.Collection)
	authReq := AuthRequest{
		Identity: c.Identity,
		Password: c.Password,
	}
	body, _ := json.Marshal(authReq)

	resp, err := http.Post(url, "application/json", bytes.NewBuffer(body))
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		var errData interface{}
		_ = json.NewDecoder(resp.Body).Decode(&errData)
		return fmt.Errorf("pocketbase auth failed with status: %d, body: %+v", resp.StatusCode, errData)
	}

	var authResp AuthResponse
	if err := json.NewDecoder(resp.Body).Decode(&authResp); err != nil {
		return err
	}

	c.Token = authResp.Token
	return nil
}

func (c *PocketbaseClient) SaveFinding(collection string, finding interface{}) error {
	if c.BaseURL == "" {
		return fmt.Errorf("pocketbase URL not configured")
	}

	url := fmt.Sprintf("%s/api/collections/%s/records", c.BaseURL, collection)
	body, _ := json.Marshal(finding)

	req, err := http.NewRequest("POST", url, bytes.NewBuffer(body))
	if err != nil {
		return err
	}

	req.Header.Set("Content-Type", "application/json")
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return fmt.Errorf("pocketbase returned status: %d", resp.StatusCode)
	}

	log.Printf("DB: Successfully saved record to collection: %s", collection)
	return nil
}

type SecurityMission struct {
	ID                string   `json:"id"`
	TargetURL         string   `json:"target_url"`
	VulnerabilityType string   `json:"vulnerability_type"`
	Status            string   `json:"status"` // Pending, Exploited, Patched, Resolved
	History           []string `json:"history"`
}

type MemoryNode struct {
	TenantID  string                 `json:"tenant_id,omitempty"`
	SessionID string                 `json:"session_id,omitempty"`
	Key       string                 `json:"key"`
	Content   string                 `json:"content"`
	Metadata  map[string]interface{} `json:"metadata,omitempty"`
}

func (c *PocketbaseClient) SaveMemory(node MemoryNode) error {
	return c.SaveFinding("memory_nodes", node)
}

func (c *PocketbaseClient) SaveProbingTarget(target interface{}) error {
	return c.SaveFinding("probing_targets", target)
}

func (c *PocketbaseClient) SaveIntelligence(intel interface{}) error {
	return c.SaveFinding("vulnerability_intelligence", intel)
}

func (c *PocketbaseClient) SaveMission(mission SecurityMission) error {
	return c.SaveFinding("security_missions", mission)
}

type PBMissionResponse struct {
	Items []struct {
		ID              string `json:"id"`
		SecurityMission `json:",inline"`
	} `json:"items"`
}

func (c *PocketbaseClient) GetMission(missionID string) (*SecurityMission, string, error) {
	if c.BaseURL == "" {
		return nil, "", fmt.Errorf("pocketbase URL not configured")
	}

	url := fmt.Sprintf("%s/api/collections/security_missions/records?filter=(id='%s')", c.BaseURL, missionID)
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, "", err
	}

	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return nil, "", fmt.Errorf("pocketbase returned status: %d", resp.StatusCode)
	}

	var pbResp PBMissionResponse
	if err := json.NewDecoder(resp.Body).Decode(&pbResp); err != nil {
		return nil, "", err
	}

	if len(pbResp.Items) == 0 {
		return nil, "", fmt.Errorf("mission not found")
	}

	return &pbResp.Items[0].SecurityMission, pbResp.Items[0].ID, nil
}

func (c *PocketbaseClient) UpdateMission(id string, mission SecurityMission) error {
	if c.BaseURL == "" {
		return fmt.Errorf("pocketbase URL not configured")
	}

	url := fmt.Sprintf("%s/api/collections/security_missions/records/%s", c.BaseURL, id)
	body, _ := json.Marshal(mission)

	req, err := http.NewRequest("PATCH", url, bytes.NewBuffer(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return fmt.Errorf("pocketbase returned status: %d", resp.StatusCode)
	}

	return nil
}

type PBProbingTargetListResponse struct {
	Items []struct {
		ID      string                 `json:"id"`
		Created string                 `json:"created"`
		Updated string                 `json:"updated"`
		Data    map[string]interface{} `json:",inline"`
	} `json:"items"`
}

func (c *PocketbaseClient) ListProbingTargets() ([]map[string]interface{}, error) {
	if c.BaseURL == "" {
		return nil, fmt.Errorf("pocketbase URL not configured")
	}

	url := fmt.Sprintf("%s/api/collections/probing_targets/records?perPage=500", c.BaseURL)
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}

	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("pocketbase returned status: %d", resp.StatusCode)
	}

	var pbResp PBProbingTargetListResponse
	if err := json.NewDecoder(resp.Body).Decode(&pbResp); err != nil {
		return nil, err
	}

	targets := make([]map[string]interface{}, len(pbResp.Items))
	for i, item := range pbResp.Items {
		targets[i] = item.Data
		targets[i]["id"] = item.ID
		targets[i]["created"] = item.Created
		targets[i]["updated"] = item.Updated
	}

	return targets, nil
}

type CogShard struct {
	Embedding []float32              `json:"embedding"`
	Title     string                 `json:"title,omitempty"`
	Summary   string                 `json:"summary,omitempty"`
	Text      string                 `json:"text,omitempty"`
	Content   string                 `json:"content"`
	Tags      []string               `json:"tags,omitempty"`
	Type      string                 `json:"type,omitempty"`
	Source    string                 `json:"source,omitempty"`
	NodeID    string                 `json:"node_id,omitempty"`
	NodeURL   string                 `json:"node_url,omitempty"`
	NodeLabel string                 `json:"node_label,omitempty"`
	Metadata  map[string]interface{} `json:"metadata,omitempty"`
}

func (c *PocketbaseClient) SaveShard(shard CogShard) error {
	return c.SaveFinding("glm_cog_shards", shard)
}

type PBListResponse struct {
	Items []struct {
		ID       string `json:"id"`
		CogShard `json:",inline"`
	} `json:"items"`
}

func (c *PocketbaseClient) GetShardByHash(hash string) (*CogShard, string, error) {
	if c.BaseURL == "" {
		return nil, "", fmt.Errorf("pocketbase URL not configured")
	}

	url := fmt.Sprintf("%s/api/collections/glm_cog_shards/records?filter=(metadata.last_seen_hash='%s')", c.BaseURL, hash)
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, "", err
	}

	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return nil, "", fmt.Errorf("pocketbase returned status: %d", resp.StatusCode)
	}

	var pbResp PBListResponse
	if err := json.NewDecoder(resp.Body).Decode(&pbResp); err != nil {
		return nil, "", err
	}

	if len(pbResp.Items) == 0 {
		return nil, "", fmt.Errorf("shard not found")
	}

	return &pbResp.Items[0].CogShard, pbResp.Items[0].ID, nil
}

func (c *PocketbaseClient) UpdateShard(id string, shard CogShard) error {
	if c.BaseURL == "" {
		return fmt.Errorf("pocketbase URL not configured")
	}

	url := fmt.Sprintf("%s/api/collections/glm_cog_shards/records/%s", c.BaseURL, id)
	body, _ := json.Marshal(shard)

	req, err := http.NewRequest("PATCH", url, bytes.NewBuffer(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return fmt.Errorf("pocketbase returned status: %d", resp.StatusCode)
	}

	return nil
}

func (c *PocketbaseClient) ListShards() ([]CogShard, error) {
	if c.BaseURL == "" {
		return nil, fmt.Errorf("pocketbase URL not configured")
	}

	url := fmt.Sprintf("%s/api/collections/glm_cog_shards/records?perPage=500", c.BaseURL)
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}

	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("pocketbase returned status: %d", resp.StatusCode)
	}

	var pbResp PBListResponse
	if err := json.NewDecoder(resp.Body).Decode(&pbResp); err != nil {
		return nil, err
	}

	shards := make([]CogShard, len(pbResp.Items))
	for i, item := range pbResp.Items {
		shards[i] = item.CogShard
	}

	return shards, nil
}
