// Package gutenberg provides a T.A.L.O.S. connector for Project Gutenberg.
// It searches the Gutendex API (https://gutendex.com) and downloads full plain-text books.
// No API key or authentication required — all content is public domain.
package gutenberg

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/Thynaptic/P-LMv1/pkg/connectors"
)

const (
	gutendexBase  = "https://gutendex.com/books"
	gutenbergBase = "https://www.gutenberg.org/ebooks"
	defaultMax    = 1
	httpTimeout   = 60 * time.Second // books can be large
)

// GutenbergConnector fetches full-text books from Project Gutenberg via the Gutendex API.
type GutenbergConnector struct {
	http *http.Client
}

// NewGutenbergConnector creates a GutenbergConnector. No credentials are required.
func NewGutenbergConnector() *GutenbergConnector {
	return &GutenbergConnector{
		http: &http.Client{Timeout: httpTimeout},
	}
}

func (c *GutenbergConnector) Name() string { return "gutenberg" }

// Fetch retrieves books from Project Gutenberg.
//
// Modes:
//   - If opts.Filter["book_ids"] is set (comma-separated IDs), fetches each book by ID.
//   - Otherwise searches Gutendex with opts.Query (title/author keywords).
//
// opts.MaxResults caps results (default 1). Content is the full plain-text of the book.
func (c *GutenbergConnector) Fetch(ctx context.Context, opts connectors.FetchOptions) ([]connectors.ConnectorDocument, error) {
	maxResults := opts.MaxResults
	if maxResults <= 0 {
		maxResults = defaultMax
	}

	// Direct fetch by book IDs if provided in Filter.
	if ids, ok := opts.Filter["book_ids"]; ok {
		idStr, _ := ids.(string)
		return c.fetchByIDs(ctx, splitIDs(idStr), maxResults)
	}

	// Search by query.
	if strings.TrimSpace(opts.Query) == "" {
		return nil, fmt.Errorf("gutenberg: opts.Query or opts.Filter[\"book_ids\"] must be set")
	}
	return c.search(ctx, opts.Query, maxResults)
}

// gutendexBook mirrors the fields we use from a Gutendex book object.
type gutendexBook struct {
	ID      int               `json:"id"`
	Title   string            `json:"title"`
	Authors []gutendexAuthor  `json:"authors"`
	Formats map[string]string `json:"formats"`
	Subjects []string         `json:"subjects"`
}

type gutendexAuthor struct {
	Name string `json:"name"`
}

type gutendexResponse struct {
	Count   int            `json:"count"`
	Next    string         `json:"next"`
	Results []gutendexBook `json:"results"`
}

func (c *GutenbergConnector) search(ctx context.Context, query string, max int) ([]connectors.ConnectorDocument, error) {
	var collected []gutendexBook
	pageURL := gutendexBase + "/?search=" + url.QueryEscape(query)

	for pageURL != "" && len(collected) < max {
		books, next, err := c.fetchPage(ctx, pageURL)
		if err != nil {
			return nil, fmt.Errorf("gutenberg search: %w", err)
		}
		collected = append(collected, books...)
		pageURL = next
	}

	if len(collected) > max {
		collected = collected[:max]
	}
	return c.booksToDocuments(ctx, collected)
}

func (c *GutenbergConnector) fetchByIDs(ctx context.Context, ids []string, max int) ([]connectors.ConnectorDocument, error) {
	if len(ids) > max {
		ids = ids[:max]
	}
	var docs []connectors.ConnectorDocument
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		u := gutendexBase + "/" + id + "/"
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
		if err != nil {
			return nil, err
		}
		resp, err := c.http.Do(req)
		if err != nil {
			return nil, fmt.Errorf("gutenberg fetch id %s: %w", id, err)
		}
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			return nil, fmt.Errorf("gutenberg read id %s: %w", id, err)
		}
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("gutenberg id %s: HTTP %d", id, resp.StatusCode)
		}
		var book gutendexBook
		if err := json.Unmarshal(body, &book); err != nil {
			return nil, fmt.Errorf("gutenberg parse id %s: %w", id, err)
		}
		partial, err := c.booksToDocuments(ctx, []gutendexBook{book})
		if err != nil {
			return nil, err
		}
		docs = append(docs, partial...)
	}
	return docs, nil
}

func (c *GutenbergConnector) fetchPage(ctx context.Context, u string) ([]gutendexBook, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, "", err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, "", err
	}
	body, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		return nil, "", err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("HTTP %d from %s", resp.StatusCode, u)
	}
	var gr gutendexResponse
	if err := json.Unmarshal(body, &gr); err != nil {
		return nil, "", err
	}
	return gr.Results, gr.Next, nil
}

func (c *GutenbergConnector) booksToDocuments(ctx context.Context, books []gutendexBook) ([]connectors.ConnectorDocument, error) {
	var docs []connectors.ConnectorDocument
	for _, book := range books {
		textURL := plainTextURL(book.Formats)
		if textURL == "" {
			// Book has no plain-text format — skip silently.
			continue
		}
		text, err := c.downloadText(ctx, textURL)
		if err != nil {
			return nil, fmt.Errorf("gutenberg download book %d: %w", book.ID, err)
		}
		idStr := strconv.Itoa(book.ID)
		docs = append(docs, connectors.ConnectorDocument{
			ID:        idStr,
			Title:     book.Title,
			Content:   text,
			SourceRef: gutenbergBase + "/" + idStr,
			Metadata: map[string]string{
				"source_type": "gutenberg",
				"book_id":     idStr,
				"author":      authorNames(book.Authors),
				"subjects":    strings.Join(book.Subjects, "; "),
			},
		})
	}
	return docs, nil
}

func (c *GutenbergConnector) downloadText(ctx context.Context, rawURL string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return "", err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return "", err
	}
	body, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		return "", err
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("HTTP %d downloading %s", resp.StatusCode, rawURL)
	}
	return string(body), nil
}

// plainTextURL picks the best plain-text download URL from a Gutendex formats map.
// Preference order: text/plain;charset=utf-8 → text/plain → first text/plain variant.
func plainTextURL(formats map[string]string) string {
	if u, ok := formats["text/plain; charset=utf-8"]; ok {
		return u
	}
	if u, ok := formats["text/plain"]; ok {
		return u
	}
	for k, v := range formats {
		if strings.HasPrefix(k, "text/plain") {
			return v
		}
	}
	return ""
}

func authorNames(authors []gutendexAuthor) string {
	names := make([]string, 0, len(authors))
	for _, a := range authors {
		names = append(names, a.Name)
	}
	return strings.Join(names, ", ")
}

func splitIDs(s string) []string {
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if t := strings.TrimSpace(p); t != "" {
			out = append(out, t)
		}
	}
	return out
}

// Compile-time interface check.
var _ connectors.Connector = (*GutenbergConnector)(nil)
