// Package github provides a T.A.L.O.S. connector for ingesting code and text
// from public GitHub repositories via the GitHub Contents API.
// Set GITHUB_TOKEN for higher rate limits; public repos work without it.
package github

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/Thynaptic/P-LMv1/pkg/connectors"
	"github.com/Thynaptic/P-LMv1/pkg/envload"
)

const (
	githubAPIBase  = "https://api.github.com"
	defaultFileMax = 500
	httpTimeout    = 30 * time.Second
)

// envOnce ensures .env is loaded exactly once per process regardless of call path.
var envOnce sync.Once

func ensureEnv() {
	envOnce.Do(func() { _ = envload.Autoload() })
}

// GitHubConnector fetches source files from a public GitHub repository.
type GitHubConnector struct {
	token string
	http  *http.Client
}

// NewGitHubConnector creates a GitHubConnector.
// GITHUB_TOKEN is read from the environment (shell export or .env) if present.
func NewGitHubConnector() *GitHubConnector {
	ensureEnv()
	return &GitHubConnector{
		token: strings.TrimSpace(os.Getenv("GITHUB_TOKEN")),
		http:  &http.Client{Timeout: httpTimeout},
	}
}

func (c *GitHubConnector) Name() string { return "github" }

// Fetch retrieves text/code files from a GitHub repository.
//
// opts.Query   = "owner/repo"
// opts.FolderID = optional subdirectory path prefix (e.g. "pkg/", "docs/")
// opts.MaxResults = max files to ingest (default 500)
func (c *GitHubConnector) Fetch(ctx context.Context, opts connectors.FetchOptions) ([]connectors.ConnectorDocument, error) {
	slug := strings.TrimSpace(opts.Query)
	if slug == "" {
		return nil, fmt.Errorf("github: opts.Query must be \"owner/repo\"")
	}
	parts := strings.SplitN(slug, "/", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return nil, fmt.Errorf("github: invalid repo slug %q, expected \"owner/repo\"", slug)
	}
	owner, repo := parts[0], parts[1]

	max := opts.MaxResults
	if max <= 0 {
		max = defaultFileMax
	}
	pathPrefix := strings.TrimPrefix(strings.TrimSpace(opts.FolderID), "/")

	// 1. Get default branch.
	branch, err := c.defaultBranch(ctx, owner, repo)
	if err != nil {
		return nil, fmt.Errorf("github: get default branch for %s/%s: %w", owner, repo, err)
	}

	// 2. Get full recursive tree.
	tree, err := c.repoTree(ctx, owner, repo, branch)
	if err != nil {
		return nil, fmt.Errorf("github: get tree for %s/%s@%s: %w", owner, repo, branch, err)
	}

	// 3. Filter to text files under pathPrefix, up to max.
	var files []treeBlob
	for _, item := range tree {
		if item.Type != "blob" {
			continue
		}
		if pathPrefix != "" && !strings.HasPrefix(item.Path, pathPrefix) {
			continue
		}
		if isBinaryExtension(item.Path) {
			continue
		}
		files = append(files, item)
		if len(files) >= max {
			break
		}
	}

	// 4. Fetch content for each file.
	var docs []connectors.ConnectorDocument
	for _, f := range files {
		content, err := c.fileContent(ctx, owner, repo, f.Path)
		if err != nil {
			// Skip files that fail to fetch (e.g. large blobs redirected to LFS).
			continue
		}
		docs = append(docs, connectors.ConnectorDocument{
			ID:        fmt.Sprintf("%s/%s/%s", owner, repo, f.Path),
			Title:     path.Base(f.Path),
			Content:   content,
			SourceRef: fmt.Sprintf("https://github.com/%s/%s/blob/%s/%s", owner, repo, branch, f.Path),
			Metadata: map[string]string{
				"source_type": "github",
				"owner":       owner,
				"repo":        repo,
				"path":        f.Path,
				"branch":      branch,
				"language":    languageFromPath(f.Path),
			},
		})
	}
	return docs, nil
}

// ── API types ────────────────────────────────────────────────────────────────

type repoMeta struct {
	DefaultBranch string `json:"default_branch"`
}

type treeBlob struct {
	Path string `json:"path"`
	Type string `json:"type"` // "blob" | "tree"
	SHA  string `json:"sha"`
}

type treeResponse struct {
	Tree     []treeBlob `json:"tree"`
	Truncated bool      `json:"truncated"`
}

type contentsResponse struct {
	Content  string `json:"content"`  // base64-encoded, may have newlines
	Encoding string `json:"encoding"` // "base64"
	Size     int    `json:"size"`
}

// ── API helpers ──────────────────────────────────────────────────────────────

func (c *GitHubConnector) defaultBranch(ctx context.Context, owner, repo string) (string, error) {
	var meta repoMeta
	if err := c.getJSON(ctx, fmt.Sprintf("%s/repos/%s/%s", githubAPIBase, owner, repo), &meta); err != nil {
		return "", err
	}
	if meta.DefaultBranch == "" {
		return "main", nil
	}
	return meta.DefaultBranch, nil
}

func (c *GitHubConnector) repoTree(ctx context.Context, owner, repo, branch string) ([]treeBlob, error) {
	var tr treeResponse
	u := fmt.Sprintf("%s/repos/%s/%s/git/trees/%s?recursive=1", githubAPIBase, owner, repo, branch)
	if err := c.getJSON(ctx, u, &tr); err != nil {
		return nil, err
	}
	return tr.Tree, nil
}

func (c *GitHubConnector) fileContent(ctx context.Context, owner, repo, filePath string) (string, error) {
	var cr contentsResponse
	u := fmt.Sprintf("%s/repos/%s/%s/contents/%s", githubAPIBase, owner, repo, filePath)
	if err := c.getJSON(ctx, u, &cr); err != nil {
		return "", err
	}
	if cr.Encoding != "base64" {
		return "", fmt.Errorf("unsupported encoding %q for %s", cr.Encoding, filePath)
	}
	// GitHub embeds newlines in the base64 — strip them before decoding.
	clean := strings.ReplaceAll(cr.Content, "\n", "")
	raw, err := base64.StdEncoding.DecodeString(clean)
	if err != nil {
		return "", fmt.Errorf("base64 decode %s: %w", filePath, err)
	}
	return string(raw), nil
}

func (c *GitHubConnector) getJSON(ctx context.Context, u string, dst any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	body, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d from %s", resp.StatusCode, u)
	}
	return json.Unmarshal(body, dst)
}

// ── Helpers ──────────────────────────────────────────────────────────────────

// isBinaryExtension returns true for common binary file extensions that should be skipped.
func isBinaryExtension(p string) bool {
	ext := strings.ToLower(path.Ext(p))
	switch ext {
	case ".png", ".jpg", ".jpeg", ".gif", ".bmp", ".ico", ".svg",
		".pdf", ".zip", ".tar", ".gz", ".bz2", ".xz", ".7z", ".rar",
		".exe", ".dll", ".so", ".dylib", ".a", ".o", ".wasm",
		".mp3", ".mp4", ".wav", ".ogg", ".webm", ".avi", ".mov",
		".ttf", ".otf", ".woff", ".woff2", ".eot",
		".db", ".sqlite", ".bin", ".dat", ".pkl", ".pt", ".pth", ".onnx",
		".lock": // package lock files — large, not human-readable knowledge
		return true
	}
	return false
}

// languageFromPath infers a language label from a file extension.
func languageFromPath(p string) string {
	ext := strings.ToLower(path.Ext(p))
	switch ext {
	case ".go":
		return "Go"
	case ".py":
		return "Python"
	case ".js":
		return "JavaScript"
	case ".ts":
		return "TypeScript"
	case ".rs":
		return "Rust"
	case ".java":
		return "Java"
	case ".c", ".h":
		return "C"
	case ".cpp", ".cc", ".cxx", ".hpp":
		return "C++"
	case ".cs":
		return "C#"
	case ".rb":
		return "Ruby"
	case ".sh", ".bash":
		return "Shell"
	case ".md", ".mdx":
		return "Markdown"
	case ".yaml", ".yml":
		return "YAML"
	case ".json":
		return "JSON"
	case ".toml":
		return "TOML"
	case ".html", ".htm":
		return "HTML"
	case ".css":
		return "CSS"
	case ".sql":
		return "SQL"
	case ".tf":
		return "Terraform"
	case ".dockerfile", "":
		if strings.EqualFold(path.Base(p), "dockerfile") {
			return "Dockerfile"
		}
	}
	return ""
}

// Compile-time interface check.
var _ connectors.Connector = (*GitHubConnector)(nil)
