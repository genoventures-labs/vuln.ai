package mesh

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"
)

const (
	defaultMeshQueryPath        = "/mesh/query"
	defaultLabCraftPath         = "/lab/craft"
	defaultMeshSyncPath         = "/mesh/sync"
	defaultThoughtBroadcastPath = "/mesh/thought/broadcast"
	defaultShardQueryPath       = "/mesh/shards/query"
	defaultShardPublishPath     = "/mesh/shards/publish"
	defaultMasterForgeNode2URL  = "http://85.31.233.157:8082"
)

// ToolRequirement describes the requested capability to resolve.
type ToolRequirement struct {
	Name          string         `json:"name,omitempty"`
	Requirement   string         `json:"requirement,omitempty"`
	Input         map[string]any `json:"input,omitempty"`
	TaskType      string         `json:"task_type,omitempty"`      // architecture | debug | forensic | ...
	ReasoningTier string         `json:"reasoning_tier,omitempty"` // t0, t1, t2, t3...
}

// ToolDescriptor is the normalized tool metadata used by resolver decisions.
type ToolDescriptor struct {
	ID                int64          `json:"id,omitempty"`
	Name              string         `json:"name,omitempty"`
	ExecutionEndpoint string         `json:"execution_endpoint,omitempty"`
	Source            string         `json:"source,omitempty"`
	Language          string         `json:"language,omitempty"`
	VersionHash       string         `json:"version_hash,omitempty"`
	Verified          bool           `json:"verified"`
	Meta              map[string]any `json:"meta,omitempty"`
}

// Resolution describes where a tool invocation should be executed.
type Resolution struct {
	Mode     string         `json:"mode"` // local | remote | crafted
	Endpoint string         `json:"endpoint"`
	Tool     ToolDescriptor `json:"tool"`
	NodeURL  string         `json:"node_url,omitempty"`
}

// ExecutionResult captures the runtime outcome of a tool call.
type ExecutionResult struct {
	StatusCode int               `json:"status_code"`
	Body       []byte            `json:"body,omitempty"`
	Metadata   map[string]string `json:"metadata,omitempty"`
}

// CognitiveShard is an abstracted reusable reasoning pattern.
type CognitiveShard struct {
	ID            int64          `json:"id,omitempty"`
	Title         string         `json:"title,omitempty"`
	Pattern       string         `json:"pattern,omitempty"`
	ReasoningTier string         `json:"reasoning_tier,omitempty"`
	ToolName      string         `json:"tool_name,omitempty"`
	SourceNodeURL string         `json:"source_node_url,omitempty"`
	Meta          map[string]any `json:"meta,omitempty"`
	CreatedAt     time.Time      `json:"created_at,omitempty"`
}

// ToolStore provides local tool lookup and metadata upsert.
type ToolStore interface {
	FindVerified(ctx context.Context, requirement ToolRequirement) (*ToolDescriptor, error)
	Upsert(ctx context.Context, tool ToolDescriptor) error
}

// ThoughtShardStore persists and counts local thought shards.
type ThoughtShardStore interface {
	SaveLocalShard(ctx context.Context, shard CognitiveShard) error
	CountLocalShards(ctx context.Context) (int, error)
}

// ToolExecutor runs a resolved tool endpoint.
type ToolExecutor interface {
	Execute(ctx context.Context, endpoint string, requirement ToolRequirement) (ExecutionResult, error)
}

// ActiveNeighborLister lists current active mesh peers.
type ActiveNeighborLister interface {
	ListActiveNeighbors(ctx context.Context) ([]Neighbor, error)
}

// MeshResolver resolves tool execution across local, mesh, and lab-fabrication paths.
type MeshResolver struct {
	Store      ToolStore
	ShardStore ThoughtShardStore
	Neighbors  ActiveNeighborLister
	Executor   ToolExecutor
	Client     *http.Client
	Logger     *log.Logger

	LocalNodeURL   string
	MasterForgeURL string
	TrustToken     string

	MeshQueryPath        string
	LabCraftPath         string
	MeshSyncPath         string
	ThoughtBroadcastPath string
	ShardQueryPath       string
	ShardPublishPath     string
}

// Resolve applies local lookup, mesh discovery, remote execution resolution,
// lab escalation, and post-craft registry sync.
func (r *MeshResolver) Resolve(ctx context.Context, requirement ToolRequirement) (Resolution, error) {
	if r.Store == nil {
		return Resolution{}, errors.New("mesh resolver: nil Store")
	}
	if r.Neighbors == nil {
		return Resolution{}, errors.New("mesh resolver: nil Neighbors")
	}

	if r.shouldThoughtBroadcast(requirement) {
		hints, err := r.thoughtBroadcast(ctx, requirement)
		if err != nil {
			r.logf("mesh resolver: thought broadcast failed: %v", err)
		} else if len(hints.Hints) > 0 {
			requirement = withInputField(requirement, "council_hints", hints.Hints)
		}
	}

	if local, err := r.Store.FindVerified(ctx, requirement); err != nil {
		return Resolution{}, fmt.Errorf("local lookup failed: %w", err)
	} else if local != nil {
		endpoint := strings.TrimSpace(local.ExecutionEndpoint)
		if endpoint == "" {
			endpoint = localExecuteEndpoint(r.LocalNodeURL, local.Name)
		}
		return Resolution{Mode: "local", Endpoint: endpoint, Tool: *local, NodeURL: strings.TrimSpace(r.LocalNodeURL)}, nil
	}

	return r.resolveMeshOrCraft(ctx, requirement)
}

// ExecuteWithFederatedFallback executes a resolved tool and enforces:
// local 404 -> neighbor lookup -> Master Forge craft fallback.
func (r *MeshResolver) ExecuteWithFederatedFallback(ctx context.Context, requirement ToolRequirement) (Resolution, ExecutionResult, error) {
	if r.Executor == nil {
		r.Executor = HTTPToolExecutor{Client: r.client(), TrustToken: r.TrustToken}
	}

	resolution, err := r.Resolve(ctx, requirement)
	if err != nil {
		return Resolution{}, ExecutionResult{}, err
	}

	result, execErr := r.Executor.Execute(ctx, resolution.Endpoint, requirement)
	if execErr != nil {
		return resolution, result, execErr
	}
	if resolution.Mode == "local" && result.StatusCode == http.StatusNotFound {
		r.logf("mesh resolver: local execution 404 for tool=%s; switching to federated resolver", requirement.Name)
		remoteResolution, resolveErr := r.resolveMeshOrCraft(ctx, requirement)
		if resolveErr != nil {
			return resolution, result, resolveErr
		}
		remoteResult, remoteErr := r.Executor.Execute(ctx, remoteResolution.Endpoint, requirement)
		return remoteResolution, remoteResult, remoteErr
	}

	if resolution.Mode == "local" && result.StatusCode >= 200 && result.StatusCode < 300 {
		if err := r.saveCompletionShard(ctx, requirement, resolution, result); err != nil {
			r.logf("mesh resolver: failed to save completion shard: %v", err)
		}
	}
	return resolution, result, nil
}

func (r *MeshResolver) resolveMeshOrCraft(ctx context.Context, requirement ToolRequirement) (Resolution, error) {
	neighbors, err := r.Neighbors.ListActiveNeighbors(ctx)
	if err != nil {
		return Resolution{}, fmt.Errorf("list active neighbors failed: %w", err)
	}

	for _, n := range neighbors {
		res, status, qErr := r.queryNeighbor(ctx, n, requirement)
		if qErr != nil {
			r.logf("mesh resolver: neighbor query failed node=%s err=%v", n.URL, qErr)
			continue
		}
		if status == http.StatusNotFound {
			continue
		}
		if status != http.StatusOK || !res.Found {
			r.logf("mesh resolver: neighbor returned non-match node=%s status=%d", n.URL, status)
			continue
		}

		tool := res.Tool
		if !tool.Verified {
			tool.Verified = true
		}
		if strings.TrimSpace(tool.Name) == "" {
			tool.Name = requirement.Name
		}

		endpoint := firstNonEmpty(
			strings.TrimSpace(res.ExecutionEndpoint),
			strings.TrimSpace(tool.ExecutionEndpoint),
			localExecuteEndpoint(n.URL, tool.Name),
		)
		return Resolution{Mode: "remote", Endpoint: endpoint, Tool: tool, NodeURL: n.URL}, nil
	}

	if tierAtLeast(requirement.ReasoningTier, 2) {
		shards, sErr := r.queryMeshShards(ctx, requirement)
		if sErr != nil {
			r.logf("mesh resolver: shard query failed prior to craft: %v", sErr)
		} else if len(shards) > 0 {
			requirement = withInputField(requirement, "mesh_shards", shards)
		}
	}

	crafted, craftErr := r.craftTool(ctx, requirement)
	if craftErr != nil {
		return Resolution{}, craftErr
	}
	if err := r.Store.Upsert(ctx, crafted); err != nil {
		r.logf("mesh resolver: local upsert after craft failed tool=%s err=%v", crafted.Name, err)
	}
	if err := r.broadcastTool(ctx, neighbors, crafted); err != nil {
		r.logf("mesh resolver: metadata broadcast encountered errors: %v", err)
	}

	endpoint := strings.TrimSpace(crafted.ExecutionEndpoint)
	if endpoint == "" {
		endpoint = localExecuteEndpoint(r.LocalNodeURL, crafted.Name)
	}
	return Resolution{Mode: "crafted", Endpoint: endpoint, Tool: crafted, NodeURL: strings.TrimSpace(r.LocalNodeURL)}, nil
}

func (r *MeshResolver) saveCompletionShard(ctx context.Context, req ToolRequirement, res Resolution, out ExecutionResult) error {
	if r.ShardStore == nil {
		return nil
	}

	tier := strings.ToLower(strings.TrimSpace(req.ReasoningTier))
	if tier == "" {
		tier = "t1"
	}
	shard := CognitiveShard{
		Title:         "Resolved " + strings.TrimSpace(req.Name),
		Pattern:       abstractPattern(req, res, out),
		ReasoningTier: tier,
		ToolName:      res.Tool.Name,
		SourceNodeURL: strings.TrimSpace(r.LocalNodeURL),
		Meta: map[string]any{
			"task_type":       strings.TrimSpace(req.TaskType),
			"status_code":     out.StatusCode,
			"resolution_mode": res.Mode,
		},
	}
	if err := r.ShardStore.SaveLocalShard(ctx, shard); err != nil {
		return err
	}
	return r.publishShard(ctx, shard)
}

func abstractPattern(req ToolRequirement, res Resolution, out ExecutionResult) string {
	parts := []string{
		"task=" + strings.TrimSpace(req.TaskType),
		"tool=" + strings.TrimSpace(res.Tool.Name),
		"mode=" + strings.TrimSpace(res.Mode),
		fmt.Sprintf("status=%d", out.StatusCode),
	}
	if v := strings.TrimSpace(req.Requirement); v != "" {
		parts = append(parts, "requirement="+v)
	}
	return strings.Join(parts, "; ")
}

// SQLToolStore implements ToolStore over database/sql and the local tool table.
type SQLToolStore struct {
	DB *sql.DB
}

func (s SQLToolStore) FindVerified(ctx context.Context, requirement ToolRequirement) (*ToolDescriptor, error) {
	if s.DB == nil {
		return nil, errors.New("nil DB")
	}

	name := strings.TrimSpace(requirement.Name)
	query := strings.TrimSpace(requirement.Requirement)

	row := s.DB.QueryRowContext(ctx, `
		SELECT id,
			   COALESCE(name, ''),
			   COALESCE(execution_endpoint, ''),
			   COALESCE(source, ''),
			   COALESCE(language, ''),
			   COALESCE(version_hash, ''),
			   COALESCE(is_verified, false)
		FROM tool
		WHERE is_verified = true
		  AND (
			(name <> '' AND name = $1)
			OR
			($2 <> '' AND (name ILIKE '%' || $2 || '%' OR source ILIKE '%' || $2 || '%'))
		  )
		LIMIT 1
	`, name, query)

	var out ToolDescriptor
	if err := row.Scan(&out.ID, &out.Name, &out.ExecutionEndpoint, &out.Source, &out.Language, &out.VersionHash, &out.Verified); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &out, nil
}

func (s SQLToolStore) Upsert(ctx context.Context, tool ToolDescriptor) error {
	if s.DB == nil {
		return errors.New("nil DB")
	}
	if strings.TrimSpace(tool.Name) == "" {
		return errors.New("tool name required")
	}

	_, err := s.DB.ExecContext(ctx, `
		INSERT INTO tool (name, execution_endpoint, source, language, version_hash, is_verified, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, CURRENT_TIMESTAMP)
		ON CONFLICT(name) DO UPDATE SET
			execution_endpoint = excluded.execution_endpoint,
			source = excluded.source,
			language = excluded.language,
			version_hash = excluded.version_hash,
			is_verified = excluded.is_verified,
			updated_at = CURRENT_TIMESTAMP
	`, tool.Name, tool.ExecutionEndpoint, tool.Source, tool.Language, tool.VersionHash, tool.Verified)
	return err
}

// SQLThoughtShardStore persists local cognitive shards in thought_shards.
type SQLThoughtShardStore struct {
	DB *sql.DB
}

func (s SQLThoughtShardStore) SaveLocalShard(ctx context.Context, shard CognitiveShard) error {
	if s.DB == nil {
		return errors.New("nil DB")
	}
	metaJSON, err := json.Marshal(shard.Meta)
	if err != nil {
		return err
	}
	_, err = s.DB.ExecContext(ctx, `
		INSERT INTO thought_shards (title, pattern, reasoning_tier, tool_name, source_node_url, meta, created_at)
		VALUES ($1, $2, $3, $4, $5, $6::jsonb, CURRENT_TIMESTAMP)
	`, shard.Title, shard.Pattern, shard.ReasoningTier, shard.ToolName, shard.SourceNodeURL, string(metaJSON))
	return err
}

func (s SQLThoughtShardStore) CountLocalShards(ctx context.Context) (int, error) {
	if s.DB == nil {
		return 0, errors.New("nil DB")
	}
	var count int
	if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM thought_shards`).Scan(&count); err != nil {
		return 0, err
	}
	return count, nil
}

type meshQueryRequest struct {
	Requirement ToolRequirement `json:"requirement"`
}

type meshQueryResponse struct {
	Found             bool           `json:"found"`
	Tool              ToolDescriptor `json:"tool"`
	ExecutionEndpoint string         `json:"execution_endpoint,omitempty"`
}

type labCraftRequest struct {
	Requirement ToolRequirement `json:"requirement"`
}

type labCraftResponse struct {
	Tool              ToolDescriptor `json:"tool"`
	ExecutionEndpoint string         `json:"execution_endpoint,omitempty"`
}

type meshSyncRequest struct {
	Tool ToolDescriptor `json:"tool"`
}

type thoughtBroadcastResponse struct {
	Hints []string `json:"hints,omitempty"`
}

type shardQueryResponse struct {
	Shards []CognitiveShard `json:"shards,omitempty"`
}

func (r *MeshResolver) queryNeighbor(ctx context.Context, n Neighbor, req ToolRequirement) (meshQueryResponse, int, error) {
	endpoint, err := joinURLPath(n.URL, r.pathOrDefault(r.MeshQueryPath, defaultMeshQueryPath))
	if err != nil {
		return meshQueryResponse{}, 0, err
	}

	payload, err := json.Marshal(meshQueryRequest{Requirement: req})
	if err != nil {
		return meshQueryResponse{}, 0, err
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return meshQueryResponse{}, 0, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	r.applyTrustHeader(httpReq)

	resp, err := r.client().Do(httpReq)
	if err != nil {
		return meshQueryResponse{}, 0, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return meshQueryResponse{}, resp.StatusCode, nil
	}

	var out meshQueryResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return meshQueryResponse{}, resp.StatusCode, err
	}
	return out, resp.StatusCode, nil
}

func (r *MeshResolver) thoughtBroadcast(ctx context.Context, req ToolRequirement) (thoughtBroadcastResponse, error) {
	endpoint, err := joinURLPath(r.meshBaseURL(), r.pathOrDefault(r.ThoughtBroadcastPath, defaultThoughtBroadcastPath))
	if err != nil {
		return thoughtBroadcastResponse{}, err
	}

	payload, err := json.Marshal(map[string]any{
		"task_type":      req.TaskType,
		"reasoning_tier": req.ReasoningTier,
		"requirement":    req.Requirement,
		"tool_name":      req.Name,
		"input":          req.Input,
	})
	if err != nil {
		return thoughtBroadcastResponse{}, err
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return thoughtBroadcastResponse{}, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	r.applyTrustHeader(httpReq)

	resp, err := r.client().Do(httpReq)
	if err != nil {
		return thoughtBroadcastResponse{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= http.StatusBadRequest {
		return thoughtBroadcastResponse{}, fmt.Errorf("thought broadcast status %d", resp.StatusCode)
	}

	var out thoughtBroadcastResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return thoughtBroadcastResponse{}, err
	}
	return out, nil
}

func (r *MeshResolver) queryMeshShards(ctx context.Context, req ToolRequirement) ([]CognitiveShard, error) {
	endpoint, err := joinURLPath(r.meshBaseURL(), r.pathOrDefault(r.ShardQueryPath, defaultShardQueryPath))
	if err != nil {
		return nil, err
	}

	payload, err := json.Marshal(map[string]any{
		"query":          firstNonEmpty(req.Requirement, req.Name),
		"reasoning_tier": strings.ToLower(strings.TrimSpace(req.ReasoningTier)),
		"task_type":      strings.TrimSpace(req.TaskType),
	})
	if err != nil {
		return nil, err
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	r.applyTrustHeader(httpReq)

	resp, err := r.client().Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil, nil
	}
	if resp.StatusCode >= http.StatusBadRequest {
		return nil, fmt.Errorf("shard query status %d", resp.StatusCode)
	}

	var out shardQueryResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	return out.Shards, nil
}

func (r *MeshResolver) publishShard(ctx context.Context, shard CognitiveShard) error {
	endpoint, err := joinURLPath(r.meshBaseURL(), r.pathOrDefault(r.ShardPublishPath, defaultShardPublishPath))
	if err != nil {
		return err
	}

	payload, err := json.Marshal(map[string]any{"shard": shard})
	if err != nil {
		return err
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	r.applyTrustHeader(httpReq)

	resp, err := r.client().Do(httpReq)
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode >= http.StatusBadRequest {
		return fmt.Errorf("shard publish status %d", resp.StatusCode)
	}
	return nil
}

func (r *MeshResolver) craftTool(ctx context.Context, req ToolRequirement) (ToolDescriptor, error) {
	target := strings.TrimSpace(r.MasterForgeURL)
	if target == "" {
		target = strings.TrimSpace(r.LocalNodeURL)
	}
	if target == "" {
		target = defaultMasterForgeNode2URL
	}

	endpoint, err := joinURLPath(target, r.pathOrDefault(r.LabCraftPath, defaultLabCraftPath))
	if err != nil {
		return ToolDescriptor{}, err
	}

	payload, err := json.Marshal(labCraftRequest{Requirement: req})
	if err != nil {
		return ToolDescriptor{}, err
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return ToolDescriptor{}, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	r.applyTrustHeader(httpReq)

	resp, err := r.client().Do(httpReq)
	if err != nil {
		return ToolDescriptor{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= http.StatusBadRequest {
		return ToolDescriptor{}, fmt.Errorf("lab craft failed: status %d", resp.StatusCode)
	}

	var out labCraftResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return ToolDescriptor{}, err
	}
	if strings.TrimSpace(out.Tool.Name) == "" {
		out.Tool.Name = strings.TrimSpace(req.Name)
	}
	out.Tool.Verified = true
	if strings.TrimSpace(out.Tool.ExecutionEndpoint) == "" {
		out.Tool.ExecutionEndpoint = strings.TrimSpace(out.ExecutionEndpoint)
	}
	return out.Tool, nil
}

func (r *MeshResolver) broadcastTool(ctx context.Context, neighbors []Neighbor, tool ToolDescriptor) error {
	syncPath := r.pathOrDefault(r.MeshSyncPath, defaultMeshSyncPath)
	payload, err := json.Marshal(meshSyncRequest{Tool: tool})
	if err != nil {
		return err
	}

	var errs []string
	for _, n := range neighbors {
		endpoint, joinErr := joinURLPath(n.URL, syncPath)
		if joinErr != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", n.URL, joinErr))
			continue
		}

		httpReq, reqErr := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
		if reqErr != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", n.URL, reqErr))
			continue
		}
		httpReq.Header.Set("Content-Type", "application/json")
		r.applyTrustHeader(httpReq)

		resp, doErr := r.client().Do(httpReq)
		if doErr != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", n.URL, doErr))
			continue
		}
		resp.Body.Close()
		if resp.StatusCode >= http.StatusBadRequest {
			errs = append(errs, fmt.Sprintf("%s: status %d", n.URL, resp.StatusCode))
		}
	}

	if len(errs) > 0 {
		return errors.New(strings.Join(errs, "; "))
	}
	return nil
}

// HTTPToolExecutor posts tool requirements to a tool execution endpoint.
type HTTPToolExecutor struct {
	Client     *http.Client
	TrustToken string
}

func (e HTTPToolExecutor) Execute(ctx context.Context, endpoint string, requirement ToolRequirement) (ExecutionResult, error) {
	payload, err := json.Marshal(map[string]any{
		"tool":        requirement.Name,
		"requirement": requirement.Requirement,
		"input":       requirement.Input,
	})
	if err != nil {
		return ExecutionResult{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return ExecutionResult{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	if token := strings.TrimSpace(e.TrustToken); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("X-Mesh-Trust-Token", token)
	}

	client := e.Client
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return ExecutionResult{}, err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	return ExecutionResult{StatusCode: resp.StatusCode, Body: body}, nil
}

func (r *MeshResolver) client() *http.Client {
	if r.Client != nil {
		return r.Client
	}
	return http.DefaultClient
}

func (r *MeshResolver) logf(format string, args ...any) {
	if r.Logger != nil {
		r.Logger.Printf(format, args...)
		return
	}
	log.Printf(format, args...)
}

func (r *MeshResolver) meshBaseURL() string {
	if strings.TrimSpace(r.MasterForgeURL) != "" {
		return strings.TrimSpace(r.MasterForgeURL)
	}
	return strings.TrimSpace(r.LocalNodeURL)
}

func (r *MeshResolver) applyTrustHeader(req *http.Request) {
	token := strings.TrimSpace(r.TrustToken)
	if token == "" {
		return
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("X-Mesh-Trust-Token", token)
}

func (r *MeshResolver) pathOrDefault(v, dflt string) string {
	v = strings.TrimSpace(v)
	if v == "" {
		return dflt
	}
	if strings.HasPrefix(v, "/") {
		return v
	}
	return "/" + v
}

func (r *MeshResolver) shouldThoughtBroadcast(req ToolRequirement) bool {
	task := strings.ToLower(strings.TrimSpace(req.TaskType))
	query := strings.ToLower(strings.TrimSpace(req.Requirement + " " + req.Name))
	if strings.Contains(task, "architecture") || strings.Contains(task, "debug") || strings.Contains(task, "forensic") {
		return true
	}
	return strings.Contains(query, "architecture") || strings.Contains(query, "debug") || strings.Contains(query, "forensic")
}

func localExecuteEndpoint(baseURL, toolName string) string {
	baseURL = strings.TrimSpace(baseURL)
	toolName = strings.TrimSpace(toolName)
	if baseURL == "" || toolName == "" {
		return ""
	}
	u, err := url.Parse(baseURL)
	if err != nil {
		return ""
	}
	u.Path = path.Join(strings.TrimRight(u.Path, "/"), "/tools/execute", url.PathEscape(toolName))
	return u.String()
}

func joinURLPath(baseURL, appendPath string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil {
		return "", fmt.Errorf("invalid base URL %q: %w", baseURL, err)
	}
	if u.Scheme == "" || u.Host == "" {
		return "", fmt.Errorf("invalid base URL %q: missing scheme or host", baseURL)
	}
	u.Path = path.Join(strings.TrimRight(u.Path, "/"), appendPath)
	u.RawQuery = ""
	u.Fragment = ""
	return u.String(), nil
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func tierAtLeast(raw string, min int) bool {
	t := strings.ToLower(strings.TrimSpace(raw))
	if t == "" {
		return false
	}
	t = strings.TrimPrefix(t, "t")
	var tier int
	_, err := fmt.Sscanf(t, "%d", &tier)
	if err != nil {
		return false
	}
	return tier >= min
}

func withInputField(req ToolRequirement, key string, value any) ToolRequirement {
	out := req
	if out.Input == nil {
		out.Input = make(map[string]any, 1)
	}
	out.Input[key] = value
	return out
}
