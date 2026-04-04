package tools

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/Thynaptic/P-LMv1/pkg/state"
)

const (
	defaultToolServerBaseURL = "http://85.31.233.157:8081"
	toolServerBaseURLEnv     = "GLM_TOOLSERVER_BASE_URL"
	toolBaseURLEnvAlias      = "GLM_TOOL_BASE_URL"
	adminTokenEnv            = "GLM_ADMIN_TOKEN"
	adminAllowRemoteEnv      = "GLM_ADMIN_ALLOW_REMOTE"
)

// GLMToolClient is a client for the TALOS Tool Server.
type GLMToolClient struct {
	BaseURL  string
	APIKey   string
	ClientID string
	HTTP     *http.Client
}

// WithCredentials clones the client with task-scoped credentials.
func (c *GLMToolClient) WithCredentials(clientID, apiKey string) *GLMToolClient {
	if c == nil {
		return nil
	}
	cloned := *c
	if strings.TrimSpace(clientID) != "" {
		cloned.ClientID = strings.TrimSpace(clientID)
	}
	if strings.TrimSpace(apiKey) != "" {
		cloned.APIKey = strings.TrimSpace(apiKey)
	}
	if cloned.HTTP == nil {
		cloned.HTTP = &http.Client{Timeout: 60 * time.Second}
	}
	return &cloned
}

// NewGLMToolClient initializes a new GLMToolClient with credentials from environment variables.
func NewGLMToolClient() (*GLMToolClient, error) {
	apiKey := os.Getenv("GLM_API_KEY")
	clientId := os.Getenv("GLM_CLIENT_ID")

	if apiKey == "" || clientId == "" {
		return nil, fmt.Errorf("GLM_API_KEY and GLM_CLIENT_ID environment variables must be set")
	}

	return &GLMToolClient{
		BaseURL:  resolveToolserverBaseURL(),
		APIKey:   apiKey,
		ClientID: clientId,
		HTTP:     &http.Client{Timeout: 60 * time.Second},
	}, nil
}

func (c *GLMToolClient) post(endpoint string, payload interface{}) ([]byte, error) {
	return c.doJSON(http.MethodPost, endpoint, payload, c.clientAuthHeaders())
}

func (c *GLMToolClient) doJSON(method string, endpoint string, payload interface{}, extraHeaders map[string]string) ([]byte, error) {
	jsonPayload, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal payload: %w", err)
	}

	url := strings.TrimRight(c.BaseURL, "/") + endpoint
	req, err := http.NewRequest(method, url, bytes.NewBuffer(jsonPayload))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	for k, v := range extraHeaders {
		if strings.TrimSpace(k) == "" || strings.TrimSpace(v) == "" {
			continue
		}
		req.Header.Set(k, v)
	}
	req.Header.Add("Content-Type", "application/json")

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to make request to tool server: %w", err)
	}
	defer resp.Body.Close()

	// Quota accounting: sniff provider quota headers when available, else count locally.
	if _, qerr := state.RecordQuotaFromHeaders("", resp.Header); qerr != nil {
		// Non-fatal: do not interrupt tool flow due to quota accounting issues.
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("tool server returned non-200 status: %s, body: %s", resp.Status, string(body))
	}

	return body, nil
}

func (c *GLMToolClient) clientAuthHeaders() map[string]string {
	return map[string]string{
		"Authorization": "Bearer " + c.APIKey,
		"X-Client-Id":   c.ClientID,
	}
}

func resolveToolserverBaseURL() string {
	base := strings.TrimSpace(os.Getenv(toolServerBaseURLEnv))
	if base == "" {
		base = strings.TrimSpace(os.Getenv(toolBaseURLEnvAlias))
	}
	if base == "" {
		base = defaultToolServerBaseURL
	}
	return strings.TrimRight(base, "/")
}

// GLMAdminClient provides access to key provisioning admin endpoints.
type GLMAdminClient struct {
	BaseURL    string
	AdminToken string
	HTTP       *http.Client
}

type AdminClientInfo struct {
	ClientID  string    `json:"client_id"`
	CreatedAt time.Time `json:"created_at,omitempty"`
	UpdatedAt time.Time `json:"updated_at,omitempty"`
}

type AdminCreateClientResponse struct {
	ClientID string `json:"client_id"`
	APIKey   string `json:"api_key"`
}

// AdminPluginInfo describes one plugin known by the toolserver.
type AdminPluginInfo struct {
	ID        string `json:"id,omitempty"`
	Name      string `json:"name,omitempty"`
	Publisher string `json:"publisher,omitempty"`
	Enabled   bool   `json:"enabled,omitempty"`
	Status    string `json:"status,omitempty"`
}

// AdminPluginInstallRequest requests plugin installation.
type AdminPluginInstallRequest struct {
	PluginID string                 `json:"plugin_id,omitempty"`
	Manifest map[string]interface{} `json:"manifest,omitempty"`
	Source   string                 `json:"source,omitempty"`
}

// AdminPluginInstallResponse is the install response payload.
type AdminPluginInstallResponse struct {
	PluginID string `json:"plugin_id,omitempty"`
	Status   string `json:"status,omitempty"`
}

// AdminToolgenRequest asks the toolgen endpoint to generate a plugin + manifest.
type AdminToolgenRequest struct {
	Name        string                 `json:"name,omitempty"`
	Description string                 `json:"description,omitempty"`
	Publisher   string                 `json:"publisher,omitempty"`
	Goal        string                 `json:"goal,omitempty"`
	Requirement string                 `json:"requirement,omitempty"`
	Metadata    map[string]interface{} `json:"metadata,omitempty"`
}

// AdminToolgenResponse contains generated plugin metadata.
type AdminToolgenResponse struct {
	ID         string                 `json:"id,omitempty"`
	JobID      string                 `json:"job_id,omitempty"`
	PluginID   string                 `json:"plugin_id,omitempty"`
	PluginName string                 `json:"plugin_name,omitempty"`
	Name       string                 `json:"name,omitempty"`
	Publisher  string                 `json:"publisher,omitempty"`
	Status     string                 `json:"status,omitempty"`
	State      string                 `json:"state,omitempty"`
	Manifest   map[string]interface{} `json:"manifest,omitempty"`
	Endpoint   string                 `json:"endpoint,omitempty"`
	Activation string                 `json:"activation,omitempty"`
}

// AdminRegistrationResponse captures tool/capability registration acknowledgments.
type AdminRegistrationResponse struct {
	Endpoint string                 `json:"endpoint"`
	Status   string                 `json:"status"`
	Payload  map[string]interface{} `json:"payload,omitempty"`
}

// NewGLMAdminClientFromEnv initializes an admin client from environment.
func NewGLMAdminClientFromEnv() (*GLMAdminClient, error) {
	token := strings.TrimSpace(os.Getenv(adminTokenEnv))
	if token == "" {
		return nil, fmt.Errorf("%s environment variable must be set", adminTokenEnv)
	}
	base := resolveToolserverBaseURL()
	if !envBool(adminAllowRemoteEnv) {
		if err := enforceLocalAdminBaseURL(base); err != nil {
			return nil, err
		}
	}
	return &GLMAdminClient{
		BaseURL:    base,
		AdminToken: token,
		HTTP:       &http.Client{Timeout: 60 * time.Second},
	}, nil
}

func envBool(key string) bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv(key)))
	switch v {
	case "1", "true", "yes", "y", "on":
		return true
	default:
		return false
	}
}

func enforceLocalAdminBaseURL(base string) error {
	u, err := url.Parse(strings.TrimSpace(base))
	if err != nil {
		return fmt.Errorf("invalid toolserver base url: %w", err)
	}
	host := strings.TrimSpace(u.Hostname())
	if host == "" {
		return fmt.Errorf("invalid toolserver base url: missing host")
	}
	if strings.EqualFold(host, "localhost") {
		return nil
	}
	ip := net.ParseIP(host)
	if ip != nil && ip.IsLoopback() {
		return nil
	}
	return fmt.Errorf("admin endpoints are localhost-only; current base URL host is %q", host)
}

func (a *GLMAdminClient) doAdminJSON(method, endpoint string, payload interface{}) ([]byte, error) {
	if a == nil {
		return nil, fmt.Errorf("admin client is nil")
	}
	bodyBytes := []byte("{}")
	if payload != nil {
		b, err := json.Marshal(payload)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal admin payload: %w", err)
		}
		bodyBytes = b
	}
	url := strings.TrimRight(a.BaseURL, "/") + endpoint
	req, err := http.NewRequest(method, url, bytes.NewBuffer(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("failed to create admin request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+a.AdminToken)
	req.Header.Set("Content-Type", "application/json")

	resp, err := a.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to call admin endpoint: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read admin response body: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("admin endpoint returned %s: %s", resp.Status, string(respBody))
	}
	return respBody, nil
}

// ListClients calls GET /admin/clients.
func (a *GLMAdminClient) ListClients() ([]AdminClientInfo, error) {
	body, err := a.doAdminJSON(http.MethodGet, "/admin/clients", map[string]interface{}{})
	if err != nil {
		return nil, err
	}
	var out []AdminClientInfo
	if err := json.Unmarshal(body, &out); err == nil {
		return out, nil
	}
	var wrapped struct {
		Clients []AdminClientInfo `json:"clients"`
	}
	if err := json.Unmarshal(body, &wrapped); err == nil {
		return wrapped.Clients, nil
	}
	var wrappedNames struct {
		Clients []string `json:"clients"`
	}
	if err := json.Unmarshal(body, &wrappedNames); err != nil {
		return nil, fmt.Errorf("failed to parse admin clients response: %w", err)
	}
	for _, id := range wrappedNames.Clients {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		out = append(out, AdminClientInfo{ClientID: id})
	}
	return out, nil
}

// CreateClient calls POST /admin/clients and returns the key once.
func (a *GLMAdminClient) CreateClient(clientID string) (AdminCreateClientResponse, error) {
	payload := map[string]string{}
	if strings.TrimSpace(clientID) != "" {
		payload["client_id"] = strings.TrimSpace(clientID)
	}
	body, err := a.doAdminJSON(http.MethodPost, "/admin/clients", payload)
	if err != nil {
		return AdminCreateClientResponse{}, err
	}
	var out AdminCreateClientResponse
	if err := json.Unmarshal(body, &out); err != nil {
		return AdminCreateClientResponse{}, fmt.Errorf("failed to parse create client response: %w", err)
	}
	return out, nil
}

// RotateClientKey calls POST /admin/clients/{client_id}/rotate.
func (a *GLMAdminClient) RotateClientKey(clientID string) (AdminCreateClientResponse, error) {
	clientID = strings.TrimSpace(clientID)
	if clientID == "" {
		return AdminCreateClientResponse{}, fmt.Errorf("client_id is required")
	}
	body, err := a.doAdminJSON(http.MethodPost, "/admin/clients/"+clientID+"/rotate", map[string]interface{}{})
	if err != nil {
		return AdminCreateClientResponse{}, err
	}
	var out AdminCreateClientResponse
	if err := json.Unmarshal(body, &out); err != nil {
		return AdminCreateClientResponse{}, fmt.Errorf("failed to parse rotate response: %w", err)
	}
	return out, nil
}

// DeleteClient calls DELETE /admin/clients/{client_id}.
func (a *GLMAdminClient) DeleteClient(clientID string) error {
	clientID = strings.TrimSpace(clientID)
	if clientID == "" {
		return fmt.Errorf("client_id is required")
	}
	_, err := a.doAdminJSON(http.MethodDelete, "/admin/clients/"+clientID, map[string]interface{}{})
	return err
}

// RegisterCapability registers a manufactured tool capability using available admin endpoints.
func (a *GLMAdminClient) RegisterCapability(payload map[string]interface{}) (AdminRegistrationResponse, error) {
	if a == nil {
		return AdminRegistrationResponse{}, fmt.Errorf("admin client is nil")
	}
	if payload == nil {
		payload = map[string]interface{}{}
	}
	endpoints := []string{
		"/admin/tools/register",
		"/admin/tools",
		"/admin/capabilities",
		"/admin/capability/register",
	}
	var lastErr error
	for _, ep := range endpoints {
		body, err := a.doAdminJSON(http.MethodPost, ep, payload)
		if err != nil {
			lastErr = err
			continue
		}
		out := AdminRegistrationResponse{
			Endpoint: ep,
			Status:   "ok",
		}
		var raw map[string]interface{}
		if json.Unmarshal(body, &raw) == nil {
			out.Payload = raw
		}
		return out, nil
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("no registration endpoint accepted payload")
	}
	return AdminRegistrationResponse{}, lastErr
}

// ListPlugins calls GET /admin/plugins.
func (a *GLMAdminClient) ListPlugins() ([]AdminPluginInfo, error) {
	body, err := a.doAdminJSON(http.MethodGet, "/admin/plugins", map[string]interface{}{})
	if err != nil {
		return nil, err
	}
	var out []AdminPluginInfo
	if err := json.Unmarshal(body, &out); err == nil {
		return out, nil
	}
	var wrapped struct {
		Plugins []AdminPluginInfo `json:"plugins"`
	}
	if err := json.Unmarshal(body, &wrapped); err == nil {
		return wrapped.Plugins, nil
	}
	return nil, fmt.Errorf("failed to parse plugins response")
}

// InstallPlugin calls POST /admin/plugins/install.
func (a *GLMAdminClient) InstallPlugin(req AdminPluginInstallRequest) (AdminPluginInstallResponse, error) {
	body, err := a.doAdminJSON(http.MethodPost, "/admin/plugins/install", req)
	if err != nil {
		return AdminPluginInstallResponse{}, err
	}
	var out AdminPluginInstallResponse
	if err := json.Unmarshal(body, &out); err != nil {
		return AdminPluginInstallResponse{}, fmt.Errorf("failed to parse install plugin response: %w", err)
	}
	return out, nil
}

// EnablePlugin calls POST /admin/plugins/{id}/enable.
func (a *GLMAdminClient) EnablePlugin(pluginID string) error {
	pluginID = strings.TrimSpace(pluginID)
	if pluginID == "" {
		return fmt.Errorf("plugin_id is required")
	}
	_, err := a.doAdminJSON(http.MethodPost, "/admin/plugins/"+pluginID+"/enable", map[string]interface{}{})
	return err
}

// DisablePlugin calls POST /admin/plugins/{id}/disable.
func (a *GLMAdminClient) DisablePlugin(pluginID string) error {
	pluginID = strings.TrimSpace(pluginID)
	if pluginID == "" {
		return fmt.Errorf("plugin_id is required")
	}
	_, err := a.doAdminJSON(http.MethodPost, "/admin/plugins/"+pluginID+"/disable", map[string]interface{}{})
	return err
}

// GenerateToolPlugin requests async plugin generation.
// Primary endpoint: POST /admin/plugins (per README.glm-toolserver.md).
// Compatibility fallback: POST /admin/toolgen/generate.
func (a *GLMAdminClient) GenerateToolPlugin(req AdminToolgenRequest) (AdminToolgenResponse, error) {
	body, err := a.doAdminJSON(http.MethodPost, "/admin/plugins", req)
	if err != nil {
		body, err = a.doAdminJSON(http.MethodPost, "/admin/toolgen/generate", req)
		if err != nil {
			return AdminToolgenResponse{}, err
		}
	}
	var out AdminToolgenResponse
	if err := json.Unmarshal(body, &out); err != nil {
		return AdminToolgenResponse{}, fmt.Errorf("failed to parse toolgen response: %w", err)
	}
	if strings.TrimSpace(out.JobID) == "" {
		out.JobID = strings.TrimSpace(out.ID)
	}
	if strings.TrimSpace(out.Name) == "" {
		out.Name = strings.TrimSpace(out.PluginName)
	}
	return out, nil
}

// GetToolgenJob fetches async generation job status.
// Primary endpoint: GET /admin/plugins/jobs/{job_id} (per README.glm-toolserver.md).
// Compatibility fallback: GET /admin/toolgen/{job_id}.
func (a *GLMAdminClient) GetToolgenJob(jobID string) (AdminToolgenResponse, error) {
	jobID = strings.TrimSpace(jobID)
	if jobID == "" {
		return AdminToolgenResponse{}, fmt.Errorf("job_id is required")
	}
	body, err := a.doAdminJSON(http.MethodGet, "/admin/plugins/jobs/"+jobID, map[string]interface{}{})
	if err != nil {
		body, err = a.doAdminJSON(http.MethodGet, "/admin/toolgen/"+jobID, map[string]interface{}{})
		if err != nil {
			return AdminToolgenResponse{}, err
		}
	}
	var out AdminToolgenResponse
	if err := json.Unmarshal(body, &out); err != nil {
		return AdminToolgenResponse{}, fmt.Errorf("failed to parse toolgen job response: %w", err)
	}
	if strings.TrimSpace(out.JobID) == "" {
		out.JobID = strings.TrimSpace(out.ID)
	}
	if strings.TrimSpace(out.Name) == "" {
		out.Name = strings.TrimSpace(out.PluginName)
	}
	return out, nil
}

// WebSearchParams represents the input for the web search tool.
type WebSearchParams struct {
	Query       string   `json:"query"`
	TopK        int      `json:"top_k,omitempty"`
	RecencyDays int      `json:"recency_days,omitempty"`
	SiteFilter  []string `json:"site_filter,omitempty"`
}

// WebSearch executes a web search via the TALOS Tool Server.
func (c *GLMToolClient) WebSearch(params WebSearchParams) (map[string]interface{}, error) {
	respBody, err := c.post("/tools/web_search", params)
	if err != nil {
		return nil, err
	}

	var result map[string]interface{}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("failed to unmarshal response: %w", err)
	}
	return result, nil
}

// FetchURLParams represents the input for the fetch URL tool.
type FetchURLParams struct {
	URL      string `json:"url"`
	MaxChars int    `json:"max_chars,omitempty"`
}

// FetchURL fetches and parses content from a URL via the TALOS Tool Server.
func (c *GLMToolClient) FetchURL(params FetchURLParams) (map[string]interface{}, error) {
	respBody, err := c.post("/tools/fetch_url", params)
	if err != nil {
		return nil, err
	}

	var result map[string]interface{}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("failed to unmarshal response: %w", err)
	}
	return result, nil
}

// HTTPRequestParams represents the input for generic HTTP requests.
type HTTPRequestParams struct {
	Method           string            `json:"method"`
	URL              string            `json:"url"`
	Headers          map[string]string `json:"headers,omitempty"`
	Body             string            `json:"body,omitempty"`
	AllowlistProfile string            `json:"allowlist_profile,omitempty"`
}

// HTTPRequest executes a generic HTTP request via the TALOS Tool Server.
func (c *GLMToolClient) HTTPRequest(params HTTPRequestParams) (map[string]interface{}, error) {
	respBody, err := c.post("/tools/http_request", params)
	if err != nil {
		return nil, err
	}

	var result map[string]interface{}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("failed to unmarshal response: %w", err)
	}
	return result, nil
}

// VectorRetrieveParams represents the input for semantic vector retrieval.
type VectorRetrieveParams struct {
	Query     string                 `json:"query"`
	Namespace string                 `json:"namespace"`
	TopK      int                    `json:"top_k"`
	Filters   map[string]interface{} `json:"filters,omitempty"`
}

// VectorRetrieve performs semantic retrieval via the TALOS Tool Server.
func (c *GLMToolClient) VectorRetrieve(params VectorRetrieveParams) (map[string]interface{}, error) {
	respBody, err := c.post("/tools/vector_retrieve", params)
	if err != nil {
		return nil, err
	}

	var result map[string]interface{}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("failed to unmarshal response: %w", err)
	}
	return result, nil
}

// CodeExecParams represents the input for the code execution sandbox tool.
type CodeExecParams struct {
	Language       string `json:"language"`
	Code           string `json:"code"`
	TimeoutSeconds int    `json:"timeout_seconds"`
}

// SkillPreflightToolRequest describes one tool/plugin request for skill preflight.
type SkillPreflightToolRequest struct {
	Kind string `json:"kind"`
	Name string `json:"name"`
}

// SkillPreflightRequest is the /skills/preflight request payload.
type SkillPreflightRequest struct {
	SkillName        string                      `json:"skill_name"`
	Intent           string                      `json:"intent"`
	RequestedTools   []SkillPreflightToolRequest `json:"requested_tools,omitempty"`
	RequestedDomains []string                    `json:"requested_domains,omitempty"`
}

// SkillPreflightInvoke tells the caller what endpoint/path can be invoked.
type SkillPreflightInvoke struct {
	Path string `json:"path,omitempty"`
	URL  string `json:"url,omitempty"`
}

// SkillPreflightResponse is the /skills/preflight response payload.
type SkillPreflightResponse struct {
	Decision string                 `json:"decision,omitempty"` // allow|deny|review
	Reason   string                 `json:"reason,omitempty"`
	Invoke   []SkillPreflightInvoke `json:"invoke,omitempty"`
	Limits   map[string]interface{} `json:"limits,omitempty"`
}

// ExecuteCode executes code in a sandbox via the TALOS Tool Server.
func (c *GLMToolClient) ExecuteCode(params CodeExecParams) (map[string]interface{}, error) {
	respBody, err := c.post("/tools/code_exec_sandbox", params)
	if err != nil {
		return nil, err
	}

	var result map[string]interface{}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("failed to unmarshal response: %w", err)
	}
	return result, nil
}

// SkillPreflight checks policy for user-defined skill actions.
func (c *GLMToolClient) SkillPreflight(req SkillPreflightRequest) (SkillPreflightResponse, error) {
	respBody, err := c.post("/skills/preflight", req)
	if err != nil {
		return SkillPreflightResponse{}, err
	}

	var out SkillPreflightResponse
	if err := json.Unmarshal(respBody, &out); err != nil {
		return SkillPreflightResponse{}, fmt.Errorf("failed to unmarshal preflight response: %w", err)
	}
	return out, nil
}
