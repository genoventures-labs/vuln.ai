package rag

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/Thynaptic/P-LMv1/pkg/connectors"
	"github.com/Thynaptic/P-LMv1/pkg/memory"
)

// IndexConnector fetches documents from a Connector and indexes them into memory.
// It reuses the same chunking and AddKnowledge pipeline as file and URL indexing.
func IndexConnector(ctx context.Context, c connectors.Connector, mm *memory.MemoryManager, opts IndexOptions, fetchOpts connectors.FetchOptions) (IndexStats, error) {
	if c == nil {
		return IndexStats{}, fmt.Errorf("connector must not be nil")
	}
	if mm == nil {
		return IndexStats{}, fmt.Errorf("memory manager must not be nil")
	}
	if opts.MaxChunkChars <= 0 {
		opts.MaxChunkChars = 1200
	}
	if opts.ChunkOverlap < 0 {
		opts.ChunkOverlap = 0
	}

	docs, err := c.Fetch(ctx, fetchOpts)
	if err != nil {
		return IndexStats{}, fmt.Errorf("connector %s fetch: %w", c.Name(), err)
	}

	var stats IndexStats
	stats.FilesScanned = len(docs)

	for _, doc := range docs {
		select {
		case <-ctx.Done():
			return stats, ctx.Err()
		default:
		}

		content := strings.TrimSpace(doc.Content)
		if content == "" {
			continue
		}

		chunks := chunkText(content, opts.MaxChunkChars, opts.ChunkOverlap)
		if len(chunks) == 0 {
			continue
		}

		indexErr := false
		for i, chunk := range chunks {
			meta := buildConnectorChunkMeta(doc, c.Name(), i+1, len(chunks))
			if err := mm.AddKnowledge(chunk, meta); err != nil {
				indexErr = true
				stats.IndexErrors++
				break
			}
			stats.ChunksIndexed++
		}

		if indexErr {
			continue
		}

		stats.FilesIndexed++

		if opts.OnSourceIndexed != nil {
			opts.OnSourceIndexed(SourceIndexedEvent{
				SourceType: c.Name(),
				SourceRef:  doc.SourceRef,
				Content:    content,
				ChunkCount: len(chunks),
				Metadata:   doc.Metadata,
			})
		}
	}

	return stats, nil
}

func buildConnectorChunkMeta(doc connectors.ConnectorDocument, connectorName string, chunkIdx, chunkTotal int) map[string]string {
	meta := make(map[string]string, len(doc.Metadata)+6)
	for k, v := range doc.Metadata {
		meta[k] = v
	}
	// Ensure canonical keys are set.
	if _, ok := meta["source_type"]; !ok {
		meta["source_type"] = connectorName
	}
	if _, ok := meta["source_ref"]; !ok {
		meta["source_ref"] = doc.SourceRef
	}
	meta["doc_id"] = doc.ID
	meta["doc_title"] = doc.Title
	meta["chunk_index"] = strconv.Itoa(chunkIdx)
	meta["chunk_total"] = strconv.Itoa(chunkTotal)
	if _, ok := meta["fetched_at"]; !ok {
		meta["fetched_at"] = time.Now().UTC().Format(time.RFC3339)
	}
	return meta
}
