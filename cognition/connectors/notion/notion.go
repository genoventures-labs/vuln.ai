// Package notion provides a T.A.L.O.S. connector for Notion databases.
package notion

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/Thynaptic/P-LMv1/pkg/connectors"
	"github.com/Thynaptic/P-LMv1/pkg/envload"
)

const (
	notionAPIBase  = "https://api.notion.com/v1"
	notionVersion  = "2022-06-28"
	defaultPageMax = 100
)

// envOnce ensures .env is loaded exactly once per process regardless of call path.
var envOnce sync.Once

func ensureEnv() {
	envOnce.Do(func() { _ = envload.Autoload() })
}

// NotionConnector fetches pages from a Notion database.
// Requires NOTION_API_KEY set via shell export or .env file.
type NotionConnector struct {
	apiKey string
	http   *http.Client
}

// NewNotionConnector creates a NotionConnector using NOTION_API_KEY from the environment.
// Credentials are read from shell exports and/or a .env file in the project root.
func NewNotionConnector() (*NotionConnector, error) {
	ensureEnv()
	key := strings.TrimSpace(os.Getenv("NOTION_API_KEY"))
	if key == "" {
		return nil, fmt.Errorf("NOTION_API_KEY is not set")
	}
	return &NotionConnector{
		apiKey: key,
		http:   &http.Client{Timeout: 30 * time.Second},
	}, nil
}

func (c *NotionConnector) Name() string { return "notion" }

// Fetch queries a Notion database and returns its pages as ConnectorDocuments.
// opts.Query is used as the database ID (or set via --notion-database flag).
// opts.Filter is an optional Notion filter object merged into the query request.
// opts.MaxResults caps the total pages fetched; defaults to 100.
func (c *NotionConnector) Fetch(ctx context.Context, opts connectors.FetchOptions) ([]connectors.ConnectorDocument, error) {
	databaseID := strings.TrimSpace(opts.Query)
	if databaseID == "" {
		return nil, fmt.Errorf("notion: database ID is required (set via opts.Query)")
	}
	max := opts.MaxResults
	if max <= 0 {
		max = defaultPageMax
	}

	pages, err := c.queryDatabase(ctx, databaseID, opts.Filter, max)
	if err != nil {
		return nil, fmt.Errorf("notion query: %w", err)
	}

	docs := make([]connectors.ConnectorDocument, 0, len(pages))
	for _, p := range pages {
		select {
		case <-ctx.Done():
			return docs, ctx.Err()
		default:
		}
		blocks, err := c.fetchPageBlocks(ctx, p.ID)
		if err != nil {
			continue
		}
		content := blocksToText(blocks)
		if strings.TrimSpace(content) == "" {
			continue
		}
		title := pageTitle(p)
		sourceRef := p.URL
		docs = append(docs, connectors.ConnectorDocument{
			ID:        p.ID,
			Title:     title,
			Content:   content,
			SourceRef: sourceRef,
			Metadata: map[string]string{
				"source_type": "notion",
				"source_ref":  sourceRef,
				"database_id": databaseID,
				"page_title":  title,
				"created_time": p.CreatedTime,
				"last_edited": p.LastEditedTime,
				"fetched_at":  time.Now().UTC().Format(time.RFC3339),
			},
		})
	}
	return docs, nil
}

// --- Notion API types ---

type notionPage struct {
	ID             string          `json:"id"`
	URL            string          `json:"url"`
	CreatedTime    string          `json:"created_time"`
	LastEditedTime string          `json:"last_edited_time"`
	Properties     json.RawMessage `json:"properties"`
}

type notionQueryResponse struct {
	Results    []notionPage `json:"results"`
	HasMore    bool         `json:"has_more"`
	NextCursor string       `json:"next_cursor"`
}

type notionBlock struct {
	Type string          `json:"type"`
	// We parse only what we need for text extraction.
	Paragraph       *notionRichTextBlock `json:"paragraph"`
	Heading1        *notionRichTextBlock `json:"heading_1"`
	Heading2        *notionRichTextBlock `json:"heading_2"`
	Heading3        *notionRichTextBlock `json:"heading_3"`
	BulletedListItem *notionRichTextBlock `json:"bulleted_list_item"`
	NumberedListItem *notionRichTextBlock `json:"numbered_list_item"`
	ToDo            *notionRichTextBlock `json:"to_do"`
	Toggle          *notionRichTextBlock `json:"toggle"`
	Quote           *notionRichTextBlock `json:"quote"`
	Callout         *notionRichTextBlock `json:"callout"`
	Code            *notionCodeBlock     `json:"code"`
}

type notionRichTextBlock struct {
	RichText []notionRichText `json:"rich_text"`
}

type notionCodeBlock struct {
	RichText []notionRichText `json:"rich_text"`
	Language string           `json:"language"`
}

type notionRichText struct {
	PlainText string `json:"plain_text"`
}

type notionBlocksResponse struct {
	Results    []notionBlock `json:"results"`
	HasMore    bool          `json:"has_more"`
	NextCursor string        `json:"next_cursor"`
}

// --- API methods ---

func (c *NotionConnector) queryDatabase(ctx context.Context, dbID string, filter map[string]any, max int) ([]notionPage, error) {
	var pages []notionPage
	cursor := ""
	for len(pages) < max {
		payload := map[string]any{
			"page_size": min(100, max-len(pages)),
		}
		if filter != nil {
			payload["filter"] = filter
		}
		if cursor != "" {
			payload["start_cursor"] = cursor
		}
		body, err := json.Marshal(payload)
		if err != nil {
			return nil, err
		}
		url := notionAPIBase + "/databases/" + dbID + "/query"
		resp, err := c.doPost(ctx, url, body)
		if err != nil {
			return nil, err
		}
		var result notionQueryResponse
		if err := json.Unmarshal(resp, &result); err != nil {
			return nil, fmt.Errorf("parsing database query response: %w", err)
		}
		pages = append(pages, result.Results...)
		if !result.HasMore || result.NextCursor == "" {
			break
		}
		cursor = result.NextCursor
	}
	if len(pages) > max {
		pages = pages[:max]
	}
	return pages, nil
}

func (c *NotionConnector) fetchPageBlocks(ctx context.Context, pageID string) ([]notionBlock, error) {
	var blocks []notionBlock
	cursor := ""
	for {
		endpoint := notionAPIBase + "/blocks/" + pageID + "/children?page_size=100"
		if cursor != "" {
			endpoint += "&start_cursor=" + cursor
		}
		body, err := c.doGet(ctx, endpoint)
		if err != nil {
			return nil, err
		}
		var result notionBlocksResponse
		if err := json.Unmarshal(body, &result); err != nil {
			return nil, fmt.Errorf("parsing blocks response: %w", err)
		}
		blocks = append(blocks, result.Results...)
		if !result.HasMore || result.NextCursor == "" {
			break
		}
		cursor = result.NextCursor
	}
	return blocks, nil
}

func (c *NotionConnector) doPost(ctx context.Context, url string, body []byte) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	c.setHeaders(req)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("POST %s: %w", url, err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("notion POST %s status %d: %s", url, resp.StatusCode, string(data))
	}
	return data, nil
}

func (c *NotionConnector) doGet(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	c.setHeaders(req)
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("GET %s: %w", url, err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("notion GET %s status %d: %s", url, resp.StatusCode, string(data))
	}
	return data, nil
}

func (c *NotionConnector) setHeaders(req *http.Request) {
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Notion-Version", notionVersion)
	req.Header.Set("Accept", "application/json")
}

// --- Text extraction helpers ---

func blocksToText(blocks []notionBlock) string {
	var sb strings.Builder
	for _, b := range blocks {
		text := blockText(b)
		if text != "" {
			sb.WriteString(text)
			sb.WriteString("\n")
		}
	}
	return strings.TrimSpace(sb.String())
}

func blockText(b notionBlock) string {
	switch b.Type {
	case "paragraph":
		return richText(b.Paragraph)
	case "heading_1":
		return richText(b.Heading1)
	case "heading_2":
		return richText(b.Heading2)
	case "heading_3":
		return richText(b.Heading3)
	case "bulleted_list_item":
		return "• " + richText(b.BulletedListItem)
	case "numbered_list_item":
		return richText(b.NumberedListItem)
	case "to_do":
		return richText(b.ToDo)
	case "toggle":
		return richText(b.Toggle)
	case "quote":
		return richText(b.Quote)
	case "callout":
		return richText(b.Callout)
	case "code":
		if b.Code != nil {
			return richTextSlice(b.Code.RichText)
		}
	}
	return ""
}

func richText(block *notionRichTextBlock) string {
	if block == nil {
		return ""
	}
	return richTextSlice(block.RichText)
}

func richTextSlice(rts []notionRichText) string {
	var parts []string
	for _, rt := range rts {
		if rt.PlainText != "" {
			parts = append(parts, rt.PlainText)
		}
	}
	return strings.Join(parts, "")
}

func pageTitle(p notionPage) string {
	// Page properties are a raw JSON object; attempt to find a "title" property.
	var props map[string]json.RawMessage
	if err := json.Unmarshal(p.Properties, &props); err != nil {
		return p.ID
	}
	for _, v := range props {
		var prop struct {
			Type  string `json:"type"`
			Title []notionRichText `json:"title"`
		}
		if err := json.Unmarshal(v, &prop); err != nil {
			continue
		}
		if prop.Type == "title" && len(prop.Title) > 0 {
			parts := make([]string, 0, len(prop.Title))
			for _, t := range prop.Title {
				if t.PlainText != "" {
					parts = append(parts, t.PlainText)
				}
			}
			if title := strings.Join(parts, ""); title != "" {
				return title
			}
		}
	}
	return p.ID
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

var _ connectors.Connector = (*NotionConnector)(nil)
