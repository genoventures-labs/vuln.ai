// Package connectors defines the Connector interface and shared types for
// all external API data source integrations.
package connectors

import "context"

// FetchOptions configures a connector fetch operation.
type FetchOptions struct {
	// Query is a source-specific search string (e.g. Gmail search, Drive query).
	Query string
	// MaxResults caps the number of documents returned.
	MaxResults int
	// FolderID restricts the fetch to a specific container (e.g. Drive folder ID).
	FolderID string
	// Filter is an optional source-specific structured filter (e.g. Notion filter JSON).
	Filter map[string]any
}

// ConnectorDocument is a normalized document returned by a Connector.
type ConnectorDocument struct {
	// ID is the source-native document identifier.
	ID string
	// Title is a human-readable document title.
	Title string
	// Content is the plain-text body of the document.
	Content string
	// SourceRef is a stable reference URI or identifier for this document.
	SourceRef string
	// Metadata carries source-specific key-value pairs for memory attribution.
	Metadata map[string]string
}

// Connector is the interface all API data source connectors implement.
// Implementations must be safe for concurrent use.
type Connector interface {
	// Name returns the connector identifier (e.g. "google_gmail", "google_drive", "notion").
	Name() string
	// Fetch retrieves documents from the external source using the provided options.
	Fetch(ctx context.Context, opts FetchOptions) ([]ConnectorDocument, error)
}
