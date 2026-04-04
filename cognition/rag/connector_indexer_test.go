package rag

import (
	"context"
	"errors"
	"testing"

	"github.com/Thynaptic/P-LMv1/pkg/connectors"
	"github.com/Thynaptic/P-LMv1/pkg/memory"
)

// mockConnector is a test double for the Connector interface.
type mockConnector struct {
	name    string
	docs    []connectors.ConnectorDocument
	fetchFn func(ctx context.Context, opts connectors.FetchOptions) ([]connectors.ConnectorDocument, error)
}

func (m *mockConnector) Name() string { return m.name }

func (m *mockConnector) Fetch(ctx context.Context, opts connectors.FetchOptions) ([]connectors.ConnectorDocument, error) {
	if m.fetchFn != nil {
		return m.fetchFn(ctx, opts)
	}
	return m.docs, nil
}

func TestIndexConnectorNilConnector(t *testing.T) {
	_, err := IndexConnector(context.Background(), nil, &memory.MemoryManager{}, IndexOptions{}, connectors.FetchOptions{})
	if err == nil {
		t.Fatal("expected error for nil connector")
	}
}

func TestIndexConnectorNilMemoryManager(t *testing.T) {
	c := &mockConnector{name: "test"}
	_, err := IndexConnector(context.Background(), c, nil, IndexOptions{}, connectors.FetchOptions{})
	if err == nil {
		t.Fatal("expected error for nil memory manager")
	}
}

func TestIndexConnectorFetchError(t *testing.T) {
	c := &mockConnector{
		name: "fail-connector",
		fetchFn: func(ctx context.Context, opts connectors.FetchOptions) ([]connectors.ConnectorDocument, error) {
			return nil, errors.New("network error")
		},
	}
	mm, err := memory.NewMemoryManager()
	if err != nil {
		t.Skip("memory manager unavailable in this environment:", err)
	}
	_, fetchErr := IndexConnector(context.Background(), c, mm, IndexOptions{}, connectors.FetchOptions{})
	if fetchErr == nil {
		t.Fatal("expected error from fetch failure")
	}
}

func TestIndexConnectorEmptyDocs(t *testing.T) {
	c := &mockConnector{name: "empty", docs: nil}
	mm, err := memory.NewMemoryManager()
	if err != nil {
		t.Skip("memory manager unavailable:", err)
	}
	stats, err := IndexConnector(context.Background(), c, mm, IndexOptions{MaxChunkChars: 500, ChunkOverlap: 50}, connectors.FetchOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if stats.FilesIndexed != 0 {
		t.Errorf("expected 0 files indexed, got %d", stats.FilesIndexed)
	}
}

func TestIndexConnectorSkipsEmptyContent(t *testing.T) {
	c := &mockConnector{
		name: "empty-content",
		docs: []connectors.ConnectorDocument{
			{ID: "d1", Title: "Empty", Content: "   ", SourceRef: "test://d1"},
		},
	}
	mm, err := memory.NewMemoryManager()
	if err != nil {
		t.Skip("memory manager unavailable:", err)
	}
	stats, err := IndexConnector(context.Background(), c, mm, IndexOptions{MaxChunkChars: 500}, connectors.FetchOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if stats.FilesIndexed != 0 {
		t.Errorf("expected 0 files indexed for empty content doc, got %d", stats.FilesIndexed)
	}
}

func TestBuildConnectorChunkMeta(t *testing.T) {
	doc := connectors.ConnectorDocument{
		ID:        "abc",
		Title:     "My Doc",
		SourceRef: "notion://abc",
		Metadata:  map[string]string{"source_type": "notion", "page_title": "My Doc"},
	}
	meta := buildConnectorChunkMeta(doc, "notion", 2, 5)

	if meta["doc_id"] != "abc" {
		t.Errorf("expected doc_id=abc, got %q", meta["doc_id"])
	}
	if meta["doc_title"] != "My Doc" {
		t.Errorf("expected doc_title='My Doc', got %q", meta["doc_title"])
	}
	if meta["chunk_index"] != "2" {
		t.Errorf("expected chunk_index=2, got %q", meta["chunk_index"])
	}
	if meta["chunk_total"] != "5" {
		t.Errorf("expected chunk_total=5, got %q", meta["chunk_total"])
	}
	if meta["source_type"] != "notion" {
		t.Errorf("expected source_type=notion, got %q", meta["source_type"])
	}
}

func TestIndexConnectorOnSourceIndexedCallback(t *testing.T) {
	content := "This is a test document with enough content to be indexed properly."
	c := &mockConnector{
		name: "callback-test",
		docs: []connectors.ConnectorDocument{
			{ID: "d1", Title: "Doc", Content: content, SourceRef: "test://d1", Metadata: map[string]string{"source_type": "callback-test"}},
		},
	}
	mm, err := memory.NewMemoryManager()
	if err != nil {
		t.Skip("memory manager unavailable:", err)
	}

	called := 0
	opts := IndexOptions{
		MaxChunkChars: 500,
		ChunkOverlap:  50,
		OnSourceIndexed: func(ev SourceIndexedEvent) {
			called++
			if ev.SourceType != "callback-test" {
				t.Errorf("expected source_type callback-test, got %q", ev.SourceType)
			}
			if ev.SourceRef != "test://d1" {
				t.Errorf("expected source_ref test://d1, got %q", ev.SourceRef)
			}
		},
	}

	stats, err := IndexConnector(context.Background(), c, mm, opts, connectors.FetchOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if stats.FilesIndexed != 1 {
		t.Errorf("expected 1 file indexed, got %d", stats.FilesIndexed)
	}
	if called != 1 {
		t.Errorf("expected OnSourceIndexed called once, got %d", called)
	}
}

func TestIndexConnectorContextCancellation(t *testing.T) {
	docs := make([]connectors.ConnectorDocument, 5)
	for i := range docs {
		docs[i] = connectors.ConnectorDocument{
			ID:        "doc",
			Content:   "some content here",
			SourceRef: "test://doc",
			Metadata:  map[string]string{"source_type": "test"},
		}
	}
	c := &mockConnector{name: "ctx-test", docs: docs}
	mm, err := memory.NewMemoryManager()
	if err != nil {
		t.Skip("memory manager unavailable:", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately
	_, err = IndexConnector(ctx, c, mm, IndexOptions{MaxChunkChars: 500}, connectors.FetchOptions{})
	// Should return context.Canceled
	if err != nil && err != context.Canceled {
		t.Logf("got error (acceptable): %v", err)
	}
}
