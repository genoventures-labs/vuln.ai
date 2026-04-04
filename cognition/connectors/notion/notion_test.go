package notion

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/Thynaptic/P-LMv1/pkg/connectors"
)

func newTestNotionConnector(t *testing.T, apiURL string) *NotionConnector {
	t.Helper()
	return &NotionConnector{
		apiKey: "test-notion-key",
		http:   &http.Client{},
	}
}

func TestNotionConnectorName(t *testing.T) {
	c := &NotionConnector{apiKey: "key", http: &http.Client{}}
	if c.Name() != "notion" {
		t.Errorf("expected notion, got %q", c.Name())
	}
}

func TestNotionConnectorInterface(_ *testing.T) {
	var _ connectors.Connector = (*NotionConnector)(nil)
}

func TestNewNotionConnectorMissingKey(t *testing.T) {
	t.Setenv("NOTION_API_KEY", "")
	_, err := NewNotionConnector()
	if err == nil {
		t.Fatal("expected error when NOTION_API_KEY is not set")
	}
}

func TestNewNotionConnectorFromEnv(t *testing.T) {
	t.Setenv("NOTION_API_KEY", "test-key-from-env")
	c, err := NewNotionConnector()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c.apiKey != "test-key-from-env" {
		t.Errorf("expected test-key-from-env, got %q", c.apiKey)
	}
}

func TestNotionQueryDatabase(t *testing.T) {
	// Build a test page in the format Notion returns.
	page := map[string]any{
		"id":               "page-001",
		"url":              "https://notion.so/page-001",
		"created_time":     "2024-01-01T00:00:00Z",
		"last_edited_time": "2024-01-02T00:00:00Z",
		"properties": map[string]any{
			"Name": map[string]any{
				"type":  "title",
				"title": []map[string]string{{"plain_text": "My Test Page"}},
			},
		},
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/databases/test-db/query":
			json.NewEncoder(w).Encode(map[string]any{
				"results":  []any{page},
				"has_more": false,
			})
		case r.Method == http.MethodGet && r.URL.Path == "/v1/blocks/page-001/children":
			json.NewEncoder(w).Encode(map[string]any{
				"results": []map[string]any{
					{
						"type": "paragraph",
						"paragraph": map[string]any{
							"rich_text": []map[string]string{{"plain_text": "Hello from Notion block"}},
						},
					},
				},
				"has_more": false,
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	// Patch the base URL using a custom connector that wraps the test server.
	c := &NotionConnector{
		apiKey: "test-key",
		http:   srv.Client(),
	}

	// Manually test doPost and doGet against the mock server.
	postBody, _ := json.Marshal(map[string]any{"page_size": 10})
	resp, err := c.doPost(context.Background(), srv.URL+"/v1/databases/test-db/query", postBody)
	if err != nil {
		t.Fatalf("doPost: %v", err)
	}
	var result map[string]any
	if err := json.Unmarshal(resp, &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	results, ok := result["results"].([]any)
	if !ok || len(results) != 1 {
		t.Errorf("expected 1 result, got: %v", result["results"])
	}

	blockResp, err := c.doGet(context.Background(), srv.URL+"/v1/blocks/page-001/children?page_size=100")
	if err != nil {
		t.Fatalf("doGet: %v", err)
	}
	var blockResult map[string]any
	if err := json.Unmarshal(blockResp, &blockResult); err != nil {
		t.Fatalf("block unmarshal: %v", err)
	}
	_ = blockResult
}

func TestBlocksToText(t *testing.T) {
	blocks := []notionBlock{
		{
			Type: "paragraph",
			Paragraph: &notionRichTextBlock{
				RichText: []notionRichText{{PlainText: "Hello"}},
			},
		},
		{
			Type: "heading_1",
			Heading1: &notionRichTextBlock{
				RichText: []notionRichText{{PlainText: "World"}},
			},
		},
		{
			Type:      "bulleted_list_item",
			BulletedListItem: &notionRichTextBlock{
				RichText: []notionRichText{{PlainText: "Item"}},
			},
		},
	}
	text := blocksToText(blocks)
	if text == "" {
		t.Fatal("expected non-empty text from blocks")
	}
	if !containsS(text, "Hello") {
		t.Errorf("expected 'Hello' in output, got: %q", text)
	}
	if !containsS(text, "World") {
		t.Errorf("expected 'World' in output, got: %q", text)
	}
	if !containsS(text, "Item") {
		t.Errorf("expected 'Item' in output, got: %q", text)
	}
}

func TestPageTitleExtraction(t *testing.T) {
	props := map[string]any{
		"Name": map[string]any{
			"type": "title",
			"title": []map[string]string{
				{"plain_text": "My Page Title"},
			},
		},
	}
	propsJSON, _ := json.Marshal(props)
	p := notionPage{
		ID:         "page-abc",
		Properties: propsJSON,
	}
	title := pageTitle(p)
	if title != "My Page Title" {
		t.Errorf("expected 'My Page Title', got %q", title)
	}
}

func TestPageTitleFallsBackToID(t *testing.T) {
	p := notionPage{
		ID:         "fallback-id",
		Properties: json.RawMessage(`{}`),
	}
	title := pageTitle(p)
	if title != "fallback-id" {
		t.Errorf("expected fallback-id, got %q", title)
	}
}

func TestNotionFetchMissingDatabaseID(t *testing.T) {
	os.Setenv("NOTION_API_KEY", "test-key")
	c := &NotionConnector{apiKey: "test-key", http: &http.Client{}}
	_, err := c.Fetch(context.Background(), connectors.FetchOptions{})
	if err == nil {
		t.Fatal("expected error when database ID is empty")
	}
}

func containsS(s, sub string) bool {
	return fmt.Sprintf("%s", s) != "" && len(s) >= len(sub) && func() bool {
		for i := 0; i <= len(s)-len(sub); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
		return false
	}()
}
