package rag

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Thynaptic/P-LMv1/pkg/memory"
)

const (
	defaultRemoteTimeout    = 20 * time.Second
	defaultRemoteMaxBytes   = int64(10 * 1024 * 1024)
	defaultCrawlDepth       = 1
	defaultMaxPages         = 200
	defaultRateLimitPerSec  = 2.0
	defaultHFDatasetAPIBase = "https://datasets-server.huggingface.co"
	defaultKaggleAPIBase    = "https://www.kaggle.com/api/v1"
)

var (
	htmlTagRE             = regexp.MustCompile(`(?s)<[^>]+>`)
	htmlSpaceRE           = regexp.MustCompile(`\s+`)
	hrefRE                = regexp.MustCompile(`(?is)href\s*=\s*["']([^"'#]+)["']`)
	hostLabelRE           = regexp.MustCompile(`^[a-z0-9-]{1,63}$`)
	errUnsupportedContent = errors.New("unsupported content type")
)

type RemoteIndexOptions struct {
	MaxChunkChars       int
	ChunkOverlap        int
	GenerateChunkTitles bool
	ChunkTitleMaxChars  int
	ChunkTitleModel     string
	Timeout             time.Duration
	MaxBytes            int64
	Crawl               bool
	CrawlDepth          int
	MaxPages            int
	RateLimitPerSec     float64
	AllowedDomains      map[string]bool
	UserAgent           string
	AuthHeader          string
	HFToken             string
	HFAPIBaseURL        string
	KaggleUsername      string
	KaggleKey           string
	KaggleAPIBaseURL    string
	URLAllowedExts      map[string]bool
	URLSafetyEnabled    bool
	URLSafetyTimeout    time.Duration
	URLSafetyCacheTTL   time.Duration
	URLSafetyVisibility string
	URLSafetyFailOpen   bool
	URLSafetyAPIKey     string
	URLSafetyBaseURL    string
	OnEvent             func(RemoteEvent)
	OnSourceIndexed     func(SourceIndexedEvent)
	HTTPClient          *http.Client
}

type RemoteIndexStats struct {
	ItemsFetched       int
	ItemsIndexed       int
	ChunksIndexed      int
	BytesFetched       int64
	PreflightFailures  int
	SafetyBlocked      int
	SafetyErrors       int
	SafetyCacheHits    int
	SafetyCacheMisses  int
	SkippedDuplicate   int
	SkippedDomain      int
	SkippedUnsupported int
	ParseErrors        int
	HTTPErrors         int
	IndexErrors        int
}

type RemoteEvent struct {
	Source       string
	Outcome      string
	Detail       string
	ItemsFetched int
	ItemsIndexed int
}

type HFSpec struct {
	DatasetID  string
	Config     string
	Split      string
	MaxRecords int
}

type KaggleSpec struct {
	DatasetID  string
	Files      []string
	MaxRecords int
}

func DefaultRemoteIndexOptions() RemoteIndexOptions {
	return RemoteIndexOptions{
		MaxChunkChars:       DefaultIndexOptions().MaxChunkChars,
		ChunkOverlap:        DefaultIndexOptions().ChunkOverlap,
		GenerateChunkTitles: true,
		ChunkTitleMaxChars:  96,
		ChunkTitleModel:     "",
		Timeout:             defaultRemoteTimeout,
		MaxBytes:            defaultRemoteMaxBytes,
		CrawlDepth:          defaultCrawlDepth,
		MaxPages:            defaultMaxPages,
		RateLimitPerSec:     defaultRateLimitPerSec,
		UserAgent:           "talos/1.0 (+https://thynaptic.com)",
		HFAPIBaseURL:        defaultHFDatasetAPIBase,
		KaggleAPIBaseURL:    defaultKaggleAPIBase,
		URLSafetyEnabled:    true,
		URLSafetyTimeout:    45 * time.Second,
		URLSafetyCacheTTL:   24 * time.Hour,
		URLSafetyVisibility: "private",
		URLSafetyFailOpen:   false,
		URLSafetyBaseURL:    defaultURLSafetyBaseURL,
	}
}

func IndexURLs(mm *memory.MemoryManager, seedURLs []string, opts RemoteIndexOptions) (RemoteIndexStats, error) {
	var stats RemoteIndexStats
	if mm == nil {
		return stats, fmt.Errorf("memory manager is required")
	}
	opts = normalizeRemoteOptions(opts)
	client := opts.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: opts.Timeout}
	}

	type queueItem struct {
		URL   string
		Depth int
	}
	seedHosts := make(map[string]bool)
	queue := make([]queueItem, 0, len(seedURLs))
	seen := make(map[string]bool)
	for _, raw := range seedURLs {
		n, host, err := normalizeHTTPURL(raw)
		if err != nil {
			emitRemoteEvent(opts, RemoteEvent{Source: raw, Outcome: "invalid-url", Detail: err.Error(), ItemsFetched: stats.ItemsFetched, ItemsIndexed: stats.ItemsIndexed})
			continue
		}
		if !urlAllowedForFetch(n, opts.URLAllowedExts) {
			stats.SkippedUnsupported++
			emitRemoteEvent(opts, RemoteEvent{Source: n, Outcome: "skipped-extension", Detail: "URL extension not in allowlist", ItemsFetched: stats.ItemsFetched, ItemsIndexed: stats.ItemsIndexed})
			continue
		}
		seedHosts[host] = true
		if !seen[n] {
			seen[n] = true
			queue = append(queue, queueItem{URL: n, Depth: 0})
		}
	}
	if len(queue) == 0 {
		return stats, fmt.Errorf("no valid URLs to index")
	}

	var nextAllowed time.Time
	hostOnlineCache := make(map[string]bool)
	for len(queue) > 0 {
		if stats.ItemsFetched >= opts.MaxPages {
			break
		}
		item := queue[0]
		queue = queue[1:]
		if !urlAllowedForFetch(item.URL, opts.URLAllowedExts) {
			stats.SkippedUnsupported++
			emitRemoteEvent(opts, RemoteEvent{Source: item.URL, Outcome: "skipped-extension", Detail: "URL extension not in allowlist", ItemsFetched: stats.ItemsFetched, ItemsIndexed: stats.ItemsIndexed})
			continue
		}
		host := hostFromURL(item.URL)
		if !isDomainAllowed(host, seedHosts, opts.AllowedDomains, opts.Crawl) {
			stats.SkippedDomain++
			emitRemoteEvent(opts, RemoteEvent{Source: item.URL, Outcome: "skipped-domain", ItemsFetched: stats.ItemsFetched, ItemsIndexed: stats.ItemsIndexed})
			continue
		}
		if online, ok := hostOnlineCache[host]; ok && !online {
			stats.PreflightFailures++
			emitRemoteEvent(opts, RemoteEvent{Source: item.URL, Outcome: "preflight-failed", Detail: "host previously marked offline", ItemsFetched: stats.ItemsFetched, ItemsIndexed: stats.ItemsIndexed})
			continue
		}
		if _, ok := hostOnlineCache[host]; !ok {
			if err := preflightURLReachable(client, item.URL, opts); err != nil {
				hostOnlineCache[host] = false
				stats.PreflightFailures++
				emitRemoteEvent(opts, RemoteEvent{Source: item.URL, Outcome: "preflight-failed", Detail: err.Error(), ItemsFetched: stats.ItemsFetched, ItemsIndexed: stats.ItemsIndexed})
				continue
			}
			hostOnlineCache[host] = true
		}
		if opts.URLSafetyEnabled {
			v, cacheHit, safetyErr := CheckURLSafety(context.Background(), item.URL, URLSafetyOptions{
				APIKey:     strings.TrimSpace(opts.URLSafetyAPIKey),
				BaseURL:    strings.TrimSpace(opts.URLSafetyBaseURL),
				Timeout:    opts.URLSafetyTimeout,
				Visibility: opts.URLSafetyVisibility,
				CacheTTL:   opts.URLSafetyCacheTTL,
				FailOpen:   opts.URLSafetyFailOpen,
			}, client)
			if cacheHit {
				stats.SafetyCacheHits++
				emitRemoteEvent(opts, RemoteEvent{Source: item.URL, Outcome: "safety-cache-hit", Detail: v.Status, ItemsFetched: stats.ItemsFetched, ItemsIndexed: stats.ItemsIndexed})
			} else {
				stats.SafetyCacheMisses++
				emitRemoteEvent(opts, RemoteEvent{Source: item.URL, Outcome: "safety-cache-miss", ItemsFetched: stats.ItemsFetched, ItemsIndexed: stats.ItemsIndexed})
			}
			if safetyErr != nil {
				stats.SafetyErrors++
				emitRemoteEvent(opts, RemoteEvent{Source: item.URL, Outcome: "safety-error", Detail: safetyErr.Error(), ItemsFetched: stats.ItemsFetched, ItemsIndexed: stats.ItemsIndexed})
				if !opts.URLSafetyFailOpen {
					continue
				}
			} else if !v.Allowed {
				stats.SafetyBlocked++
				emitRemoteEvent(opts, RemoteEvent{Source: item.URL, Outcome: "safety-blocked", Detail: v.Reason, ItemsFetched: stats.ItemsFetched, ItemsIndexed: stats.ItemsIndexed})
				continue
			} else {
				emitRemoteEvent(opts, RemoteEvent{Source: item.URL, Outcome: "safety-allowed", Detail: v.Reason, ItemsFetched: stats.ItemsFetched, ItemsIndexed: stats.ItemsIndexed})
			}
		}

		waitForRateLimit(opts.RateLimitPerSec, &nextAllowed)

		body, contentType, status, fetchErr := fetchURL(client, item.URL, opts)
		if fetchErr != nil {
			stats.HTTPErrors++
			emitRemoteEvent(opts, RemoteEvent{Source: item.URL, Outcome: "http-error", Detail: fetchErr.Error(), ItemsFetched: stats.ItemsFetched, ItemsIndexed: stats.ItemsIndexed})
			continue
		}
		stats.ItemsFetched++
		stats.BytesFetched += int64(len(body))
		if status < 200 || status >= 300 {
			stats.HTTPErrors++
			emitRemoteEvent(opts, RemoteEvent{Source: item.URL, Outcome: "http-status", Detail: strconv.Itoa(status), ItemsFetched: stats.ItemsFetched, ItemsIndexed: stats.ItemsIndexed})
			continue
		}

		text, isHTML, parseErr := parseFetchedContent(item.URL, contentType, body, len(opts.URLAllowedExts) > 0)
		if parseErr != nil {
			if errors.Is(parseErr, errUnsupportedContent) {
				stats.SkippedUnsupported++
				emitRemoteEvent(opts, RemoteEvent{Source: item.URL, Outcome: "skipped-unsupported", Detail: parseErr.Error(), ItemsFetched: stats.ItemsFetched, ItemsIndexed: stats.ItemsIndexed})
			} else {
				stats.ParseErrors++
				emitRemoteEvent(opts, RemoteEvent{Source: item.URL, Outcome: "parse-error", Detail: parseErr.Error(), ItemsFetched: stats.ItemsFetched, ItemsIndexed: stats.ItemsIndexed})
			}
		} else {
			chunks := chunkText(normalizeText(text), opts.MaxChunkChars, opts.ChunkOverlap)
			if len(chunks) == 0 {
				stats.SkippedUnsupported++
				emitRemoteEvent(opts, RemoteEvent{Source: item.URL, Outcome: "empty", ItemsFetched: stats.ItemsFetched, ItemsIndexed: stats.ItemsIndexed})
			} else {
				if err := addRemoteChunks(mm, chunks, map[string]string{
					"type":          "knowledge",
					"source_type":   "url",
					"source_url":    item.URL,
					"source_host":   host,
					"topology_node": item.URL,
					"content_type":  contentType,
					"crawl_depth":   strconv.Itoa(item.Depth),
				}, opts); err != nil {
					stats.IndexErrors++
					emitRemoteEvent(opts, RemoteEvent{Source: item.URL, Outcome: "index-error", Detail: err.Error(), ItemsFetched: stats.ItemsFetched, ItemsIndexed: stats.ItemsIndexed})
				} else {
					stats.ItemsIndexed++
					stats.ChunksIndexed += len(chunks)
					_ = mm.UpsertTopologySource(memory.SourceFingerprint{
						SourceType: "url",
						SourceRef:  item.URL,
						SourceURL:  item.URL,
						SourceHost: host,
						Content:    text,
					})
					emitRemoteEvent(opts, RemoteEvent{Source: item.URL, Outcome: "indexed", Detail: fmt.Sprintf("%d chunks", len(chunks)), ItemsFetched: stats.ItemsFetched, ItemsIndexed: stats.ItemsIndexed})
					emitRemoteSourceIndexed(opts, SourceIndexedEvent{
						SourceType: "url",
						SourceRef:  item.URL,
						Content:    text,
						ChunkCount: len(chunks),
						Metadata: map[string]string{
							"source_type": "url",
							"source_url":  item.URL,
							"source_host": host,
						},
					})
				}
			}
		}

		if opts.Crawl && isHTML && item.Depth < opts.CrawlDepth {
			links := extractLinks(item.URL, body)
			for _, l := range links {
				norm, linkHost, err := normalizeHTTPURL(l)
				if err != nil {
					continue
				}
				if !isDomainAllowed(linkHost, seedHosts, opts.AllowedDomains, opts.Crawl) {
					continue
				}
				if !urlAllowedForFetch(norm, opts.URLAllowedExts) {
					continue
				}
				if seen[norm] {
					stats.SkippedDuplicate++
					continue
				}
				seen[norm] = true
				queue = append(queue, queueItem{URL: norm, Depth: item.Depth + 1})
			}
		}
	}

	return stats, nil
}

func IndexHFDatasets(mm *memory.MemoryManager, specs []HFSpec, opts RemoteIndexOptions) (RemoteIndexStats, error) {
	var stats RemoteIndexStats
	if mm == nil {
		return stats, fmt.Errorf("memory manager is required")
	}
	opts = normalizeRemoteOptions(opts)
	client := opts.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: opts.Timeout}
	}

	base := strings.TrimRight(strings.TrimSpace(opts.HFAPIBaseURL), "/")
	if base == "" {
		base = defaultHFDatasetAPIBase
	}

	for _, spec := range specs {
		ds := strings.TrimSpace(spec.DatasetID)
		if ds == "" {
			continue
		}
		split := strings.TrimSpace(spec.Split)
		if split == "" {
			split = "train"
		}
		maxRecords := spec.MaxRecords
		if maxRecords <= 0 {
			maxRecords = 100
		}

		endpoint := base + "/first-rows?dataset=" + url.QueryEscape(ds) + "&split=" + url.QueryEscape(split)
		if cfg := strings.TrimSpace(spec.Config); cfg != "" {
			endpoint += "&config=" + url.QueryEscape(cfg)
		}

		body, _, status, err := fetchURL(client, endpoint, RemoteIndexOptions{
			MaxBytes:   opts.MaxBytes,
			UserAgent:  opts.UserAgent,
			AuthHeader: bearerHeader(opts.HFToken),
		})
		if err != nil {
			stats.HTTPErrors++
			emitRemoteEvent(opts, RemoteEvent{Source: ds, Outcome: "hf-http-error", Detail: err.Error(), ItemsFetched: stats.ItemsFetched, ItemsIndexed: stats.ItemsIndexed})
			continue
		}
		stats.ItemsFetched++
		stats.BytesFetched += int64(len(body))
		if status < 200 || status >= 300 {
			stats.HTTPErrors++
			detail := strconv.Itoa(status)
			if msg := extractHFErrorMessage(body); msg != "" {
				detail += ": " + msg
			}
			if status == http.StatusBadRequest || status == http.StatusUnprocessableEntity {
				if hint, hintErr := fetchHFSplitHint(client, base, ds, opts); hintErr == nil && strings.TrimSpace(hint) != "" {
					detail += " | " + hint
				}
			}
			emitRemoteEvent(opts, RemoteEvent{Source: ds, Outcome: "hf-http-status", Detail: detail, ItemsFetched: stats.ItemsFetched, ItemsIndexed: stats.ItemsIndexed})
			continue
		}

		rows, parseErr := parseHFFirstRows(body)
		if parseErr != nil {
			stats.ParseErrors++
			emitRemoteEvent(opts, RemoteEvent{Source: ds, Outcome: "hf-parse-error", Detail: parseErr.Error(), ItemsFetched: stats.ItemsFetched, ItemsIndexed: stats.ItemsIndexed})
			continue
		}
		if len(rows) > maxRecords {
			rows = rows[:maxRecords]
		}
		for i, row := range rows {
			text := normalizeText(row)
			if text == "" {
				continue
			}
			chunks := chunkText(text, opts.MaxChunkChars, opts.ChunkOverlap)
			if len(chunks) == 0 {
				continue
			}
			meta := map[string]string{
				"type":          "knowledge",
				"source_type":   "hf_dataset",
				"hf_dataset":    ds,
				"hf_split":      split,
				"topology_node": fmt.Sprintf("hf:%s:%s", ds, split),
				"record_index":  strconv.Itoa(i + 1),
				"record_total":  strconv.Itoa(len(rows)),
				"source_url":    endpoint,
				"fetched_at":    time.Now().UTC().Format(time.RFC3339),
			}
			if err := addRemoteChunks(mm, chunks, meta, opts); err != nil {
				stats.IndexErrors++
				emitRemoteEvent(opts, RemoteEvent{Source: ds, Outcome: "hf-index-error", Detail: err.Error(), ItemsFetched: stats.ItemsFetched, ItemsIndexed: stats.ItemsIndexed})
				continue
			}
			stats.ItemsIndexed++
			stats.ChunksIndexed += len(chunks)
			_ = mm.UpsertTopologySource(memory.SourceFingerprint{
				SourceType: "hf_dataset",
				SourceRef:  fmt.Sprintf("hf:%s:%s", ds, split),
				SourcePath: fmt.Sprintf("hf:%s:%s", ds, split),
				Content:    text,
			})
			emitRemoteEvent(opts, RemoteEvent{Source: ds, Outcome: "hf-indexed-record", Detail: fmt.Sprintf("record %d", i+1), ItemsFetched: stats.ItemsFetched, ItemsIndexed: stats.ItemsIndexed})
			emitRemoteSourceIndexed(opts, SourceIndexedEvent{
				SourceType: "hf_dataset",
				SourceRef:  fmt.Sprintf("hf:%s:%s:%s:%d", ds, strings.TrimSpace(spec.Config), split, i+1),
				Content:    text,
				ChunkCount: len(chunks),
				Metadata: map[string]string{
					"source_type": "hf_dataset",
					"hf_dataset":  ds,
					"hf_config":   strings.TrimSpace(spec.Config),
					"hf_split":    split,
				},
			})
		}
	}

	return stats, nil
}

func IndexKaggleDatasets(mm *memory.MemoryManager, specs []KaggleSpec, opts RemoteIndexOptions) (RemoteIndexStats, error) {
	var stats RemoteIndexStats
	if mm == nil {
		return stats, fmt.Errorf("memory manager is required")
	}
	opts = normalizeRemoteOptions(opts)
	client := opts.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: opts.Timeout}
	}
	username, key, credErr := resolveKaggleCredentials(opts)
	if credErr != nil {
		return stats, credErr
	}
	base := strings.TrimRight(strings.TrimSpace(opts.KaggleAPIBaseURL), "/")
	if base == "" {
		base = defaultKaggleAPIBase
	}

	for _, spec := range specs {
		datasetID := strings.TrimSpace(spec.DatasetID)
		if datasetID == "" {
			continue
		}
		owner, dataset, err := splitKaggleDatasetID(datasetID)
		if err != nil {
			emitRemoteEvent(opts, RemoteEvent{Source: datasetID, Outcome: "kaggle-invalid-dataset", Detail: err.Error(), ItemsFetched: stats.ItemsFetched, ItemsIndexed: stats.ItemsIndexed})
			stats.ParseErrors++
			continue
		}
		files, err := fetchKaggleDatasetFiles(client, base, owner, dataset, username, key, opts)
		if err != nil {
			stats.HTTPErrors++
			emitRemoteEvent(opts, RemoteEvent{Source: datasetID, Outcome: "kaggle-http-error", Detail: err.Error(), ItemsFetched: stats.ItemsFetched, ItemsIndexed: stats.ItemsIndexed})
			continue
		}
		stats.ItemsFetched++
		selected := selectKaggleFiles(files, spec.Files)
		if len(selected) == 0 {
			stats.SkippedUnsupported++
			emitRemoteEvent(opts, RemoteEvent{Source: datasetID, Outcome: "kaggle-no-files", Detail: "no supported files selected", ItemsFetched: stats.ItemsFetched, ItemsIndexed: stats.ItemsIndexed})
			continue
		}
		maxRecords := spec.MaxRecords
		if maxRecords <= 0 {
			maxRecords = 100
		}
		for _, fileName := range selected {
			endpoint := fmt.Sprintf("%s/datasets/download/%s/%s/%s", base, url.PathEscape(owner), url.PathEscape(dataset), url.PathEscape(fileName))
			body, _, status, fetchErr := fetchURL(client, endpoint, RemoteIndexOptions{
				MaxBytes:   opts.MaxBytes,
				UserAgent:  opts.UserAgent,
				AuthHeader: kaggleBasicAuthHeader(username, key),
			})
			if fetchErr != nil {
				stats.HTTPErrors++
				emitRemoteEvent(opts, RemoteEvent{Source: datasetID, Outcome: "kaggle-http-error", Detail: fetchErr.Error(), ItemsFetched: stats.ItemsFetched, ItemsIndexed: stats.ItemsIndexed})
				continue
			}
			stats.ItemsFetched++
			stats.BytesFetched += int64(len(body))
			if status < 200 || status >= 300 {
				stats.HTTPErrors++
				emitRemoteEvent(opts, RemoteEvent{Source: datasetID, Outcome: "kaggle-http-status", Detail: strconv.Itoa(status), ItemsFetched: stats.ItemsFetched, ItemsIndexed: stats.ItemsIndexed})
				continue
			}
			rows, parseErr := parseKaggleRows(fileName, body, maxRecords)
			if parseErr != nil {
				stats.ParseErrors++
				emitRemoteEvent(opts, RemoteEvent{Source: datasetID, Outcome: "kaggle-parse-error", Detail: parseErr.Error(), ItemsFetched: stats.ItemsFetched, ItemsIndexed: stats.ItemsIndexed})
				continue
			}
			for i, row := range rows {
				text := normalizeText(row)
				if text == "" {
					continue
				}
				chunks := chunkText(text, opts.MaxChunkChars, opts.ChunkOverlap)
				if len(chunks) == 0 {
					continue
				}
				meta := map[string]string{
					"type":           "knowledge",
					"source_type":    "kaggle_dataset",
					"kaggle_dataset": datasetID,
					"kaggle_file":    fileName,
					"topology_node":  fmt.Sprintf("kaggle:%s", datasetID),
					"record_index":   strconv.Itoa(i + 1),
					"record_total":   strconv.Itoa(len(rows)),
					"source_url":     endpoint,
					"fetched_at":     time.Now().UTC().Format(time.RFC3339),
				}
				if err := addRemoteChunks(mm, chunks, meta, opts); err != nil {
					stats.IndexErrors++
					emitRemoteEvent(opts, RemoteEvent{Source: datasetID, Outcome: "kaggle-index-error", Detail: err.Error(), ItemsFetched: stats.ItemsFetched, ItemsIndexed: stats.ItemsIndexed})
					continue
				}
				stats.ItemsIndexed++
				stats.ChunksIndexed += len(chunks)
				_ = mm.UpsertTopologySource(memory.SourceFingerprint{
					SourceType: "kaggle_dataset",
					SourceRef:  fmt.Sprintf("kaggle:%s", datasetID),
					SourcePath: fmt.Sprintf("kaggle:%s:%s", datasetID, fileName),
					Content:    text,
				})
				emitRemoteEvent(opts, RemoteEvent{Source: datasetID, Outcome: "kaggle-indexed-record", Detail: fmt.Sprintf("%s row %d", fileName, i+1), ItemsFetched: stats.ItemsFetched, ItemsIndexed: stats.ItemsIndexed})
				emitRemoteSourceIndexed(opts, SourceIndexedEvent{
					SourceType: "kaggle_dataset",
					SourceRef:  fmt.Sprintf("kaggle:%s:%s:%d", datasetID, fileName, i+1),
					Content:    text,
					ChunkCount: len(chunks),
					Metadata: map[string]string{
						"source_type":    "kaggle_dataset",
						"kaggle_dataset": datasetID,
						"kaggle_file":    fileName,
					},
				})
			}
		}
	}

	return stats, nil
}

func normalizeRemoteOptions(opts RemoteIndexOptions) RemoteIndexOptions {
	if opts.MaxChunkChars <= 0 {
		opts.MaxChunkChars = DefaultIndexOptions().MaxChunkChars
	}
	if opts.ChunkOverlap < 0 {
		opts.ChunkOverlap = 0
	}
	if opts.ChunkOverlap >= opts.MaxChunkChars {
		opts.ChunkOverlap = opts.MaxChunkChars / 4
	}
	if !opts.GenerateChunkTitles && opts.ChunkTitleMaxChars <= 0 && strings.TrimSpace(opts.ChunkTitleModel) == "" {
		opts.GenerateChunkTitles = true
	}
	if opts.ChunkTitleMaxChars <= 0 {
		opts.ChunkTitleMaxChars = 96
	}
	if opts.Timeout <= 0 {
		opts.Timeout = defaultRemoteTimeout
	}
	if opts.MaxBytes <= 0 {
		opts.MaxBytes = defaultRemoteMaxBytes
	}
	if opts.CrawlDepth < 0 {
		opts.CrawlDepth = 0
	}
	if opts.CrawlDepth == 0 {
		opts.CrawlDepth = defaultCrawlDepth
	}
	if opts.MaxPages <= 0 {
		opts.MaxPages = defaultMaxPages
	}
	if opts.RateLimitPerSec <= 0 {
		opts.RateLimitPerSec = defaultRateLimitPerSec
	}
	if strings.TrimSpace(opts.UserAgent) == "" {
		opts.UserAgent = "talos/1.0 (+https://thynaptic.com)"
	}
	if opts.URLSafetyTimeout <= 0 {
		opts.URLSafetyTimeout = 45 * time.Second
	}
	if opts.URLSafetyCacheTTL <= 0 {
		opts.URLSafetyCacheTTL = 24 * time.Hour
	}
	if strings.TrimSpace(opts.URLSafetyVisibility) == "" {
		opts.URLSafetyVisibility = "private"
	}
	switch strings.ToLower(strings.TrimSpace(opts.URLSafetyVisibility)) {
	case "private", "unlisted", "public":
	default:
		opts.URLSafetyVisibility = "private"
	}
	if strings.TrimSpace(opts.URLSafetyBaseURL) == "" {
		opts.URLSafetyBaseURL = defaultURLSafetyBaseURL
	}
	if len(opts.URLAllowedExts) > 0 {
		normalized := make(map[string]bool, len(opts.URLAllowedExts))
		for ext, ok := range opts.URLAllowedExts {
			if !ok {
				continue
			}
			e := strings.ToLower(strings.TrimSpace(ext))
			if e == "" {
				continue
			}
			if !strings.HasPrefix(e, ".") {
				e = "." + e
			}
			normalized[e] = true
		}
		opts.URLAllowedExts = normalized
	}
	return opts
}

func fetchURL(client *http.Client, rawURL string, opts RemoteIndexOptions) ([]byte, string, int, error) {
	req, err := http.NewRequest(http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, "", 0, err
	}
	if ua := strings.TrimSpace(opts.UserAgent); ua != "" {
		req.Header.Set("User-Agent", ua)
	}
	if ah := strings.TrimSpace(opts.AuthHeader); ah != "" {
		req.Header.Set("Authorization", ah)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, "", 0, err
	}
	defer resp.Body.Close()

	reader := io.LimitReader(resp.Body, opts.MaxBytes+1)
	body, err := io.ReadAll(reader)
	if err != nil {
		return nil, "", resp.StatusCode, err
	}
	if int64(len(body)) > opts.MaxBytes {
		return nil, "", resp.StatusCode, fmt.Errorf("content exceeds max-bytes limit")
	}
	return body, resp.Header.Get("Content-Type"), resp.StatusCode, nil
}

func parseFetchedContent(rawURL, contentType string, body []byte, allowNonHTML bool) (string, bool, error) {
	u, _ := url.Parse(rawURL)
	ext := strings.ToLower(filepath.Ext(u.Path))
	ct := strings.ToLower(strings.TrimSpace(strings.Split(contentType, ";")[0]))
	isHTML := strings.Contains(ct, "text/html") || ext == ".html" || ext == ".htm"
	isPDF := strings.Contains(ct, "application/pdf") || ext == ".pdf"

	if isHTML {
		text := htmlToText(string(body))
		return text, true, nil
	}
	if !allowNonHTML {
		return "", false, fmt.Errorf("%w: only HTML pages are indexed unless --extensions is set", errUnsupportedContent)
	}
	if isPDF {
		tmp, err := os.CreateTemp("", "talos_learn_*.pdf")
		if err != nil {
			return "", false, err
		}
		defer os.Remove(tmp.Name())
		if _, err := tmp.Write(body); err != nil {
			_ = tmp.Close()
			return "", false, err
		}
		_ = tmp.Close()
		text, err := readPDF(tmp.Name())
		return text, false, err
	}
	if bytes.Contains(body, []byte{0}) {
		return "", false, fmt.Errorf("binary content")
	}
	if strings.Contains(ct, "text/") ||
		strings.Contains(ct, "application/json") ||
		strings.Contains(ct, "application/xml") ||
		strings.Contains(ct, "text/csv") ||
		ext == ".txt" || ext == ".md" || ext == ".json" || ext == ".csv" || ext == ".yaml" || ext == ".yml" {
		return string(body), false, nil
	}
	return "", false, fmt.Errorf("%w", errUnsupportedContent)
}

func urlAllowedByExtension(rawURL string, allowed map[string]bool) bool {
	if len(allowed) == 0 {
		return true
	}
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return false
	}
	ext := strings.ToLower(strings.TrimSpace(filepath.Ext(u.Path)))
	if ext == "" {
		return false
	}
	return allowed[ext]
}

func urlAllowedForFetch(rawURL string, allowed map[string]bool) bool {
	if len(allowed) > 0 {
		return urlAllowedByExtension(rawURL, allowed)
	}
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return false
	}
	ext := strings.ToLower(strings.TrimSpace(filepath.Ext(u.Path)))
	if ext == "" {
		return true
	}
	switch ext {
	case ".html", ".htm", ".php", ".asp", ".aspx", ".jsp", ".jspx", ".cfm", ".cgi", ".shtml", ".xhtml":
		return true
	default:
		return false
	}
}

func htmlToText(s string) string {
	s = regexp.MustCompile(`(?is)<script.*?>.*?</script>`).ReplaceAllString(s, " ")
	s = regexp.MustCompile(`(?is)<style.*?>.*?</style>`).ReplaceAllString(s, " ")
	s = htmlTagRE.ReplaceAllString(s, " ")
	s = htmlSpaceRE.ReplaceAllString(s, " ")
	return strings.TrimSpace(s)
}

func extractLinks(baseURL string, body []byte) []string {
	matches := hrefRE.FindAllStringSubmatch(string(body), -1)
	if len(matches) == 0 {
		return nil
	}
	u, err := url.Parse(baseURL)
	if err != nil {
		return nil
	}
	var out []string
	seen := make(map[string]bool)
	for _, m := range matches {
		if len(m) < 2 {
			continue
		}
		ref, err := url.Parse(strings.TrimSpace(m[1]))
		if err != nil {
			continue
		}
		resolved := u.ResolveReference(ref)
		if resolved.Scheme != "http" && resolved.Scheme != "https" {
			continue
		}
		s := resolved.String()
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

func addRemoteChunks(mm *memory.MemoryManager, chunks []string, baseMeta map[string]string, opts RemoteIndexOptions) error {
	total := len(chunks)
	titleGen := memory.NewChunkTitleGenerator(mm, memory.ChunkTitleConfig{
		MaxChars: opts.ChunkTitleMaxChars,
		Model:    strings.TrimSpace(opts.ChunkTitleModel),
	})
	sourceType := strings.TrimSpace(baseMeta["source_type"])
	sourceRef := remoteChunkSourceRef(baseMeta)
	sectionMetas := inferChunkSections(chunks, sourceRef)
	for i, chunk := range chunks {
		meta := make(map[string]string, len(baseMeta)+3)
		for k, v := range baseMeta {
			meta[k] = v
		}
		meta["chunk_index"] = strconv.Itoa(i + 1)
		meta["chunk_total"] = strconv.Itoa(total)
		meta["indexed_at"] = time.Now().UTC().Format(time.RFC3339)
		if i < len(sectionMetas) {
			sectionID, sectionTitle, sectionLevel, sectionInferred := sectionMetaAsStrings(sectionMetas[i])
			meta["section_id"] = sectionID
			meta["section_title"] = sectionTitle
			meta["section_level"] = sectionLevel
			meta["section_inferred"] = sectionInferred
		}
		if opts.GenerateChunkTitles {
			title, titleErr := titleGen.Generate(context.Background(), sourceType, sourceRef, chunk, i+1, total)
			if titleErr != nil {
				title = remoteChunkFallbackTitle(baseMeta, i+1, total)
			}
			meta["chunk_title"] = title
		}
		if err := mm.AddKnowledge(chunk, meta); err != nil {
			return err
		}
	}
	return nil
}

func remoteChunkSourceRef(meta map[string]string) string {
	if u := strings.TrimSpace(meta["source_url"]); u != "" {
		return u
	}
	kaggleDS := strings.TrimSpace(meta["kaggle_dataset"])
	kaggleFile := strings.TrimSpace(meta["kaggle_file"])
	if kaggleDS != "" {
		if kaggleFile == "" {
			return "kaggle:" + kaggleDS
		}
		return "kaggle:" + kaggleDS + ":" + kaggleFile
	}
	ds := strings.TrimSpace(meta["hf_dataset"])
	split := strings.TrimSpace(meta["hf_split"])
	if ds != "" {
		if split == "" {
			return "hf:" + ds
		}
		return "hf:" + ds + ":" + split
	}
	return "remote"
}

func remoteChunkFallbackTitle(meta map[string]string, index int, total int) string {
	ref := remoteChunkSourceRef(meta)
	if total > 1 && index > 0 {
		return fmt.Sprintf("%s [%d/%d]", ref, index, total)
	}
	return ref
}

func emitRemoteEvent(opts RemoteIndexOptions, ev RemoteEvent) {
	if opts.OnEvent != nil {
		opts.OnEvent(ev)
	}
}

func emitRemoteSourceIndexed(opts RemoteIndexOptions, ev SourceIndexedEvent) {
	if opts.OnSourceIndexed != nil {
		opts.OnSourceIndexed(ev)
	}
}

func waitForRateLimit(rate float64, nextAllowed *time.Time) {
	if rate <= 0 {
		return
	}
	interval := time.Duration(float64(time.Second) / rate)
	now := time.Now()
	if now.Before(*nextAllowed) {
		time.Sleep(nextAllowed.Sub(now))
	}
	*nextAllowed = time.Now().Add(interval)
}

func normalizeHTTPURL(raw string) (string, string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return "", "", err
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", "", fmt.Errorf("unsupported URL scheme")
	}
	if strings.TrimSpace(u.Host) == "" {
		return "", "", fmt.Errorf("URL host is empty")
	}
	host := strings.ToLower(u.Hostname())
	if !isValidDomainOrIP(host) {
		return "", "", fmt.Errorf("invalid domain")
	}
	u.Fragment = ""
	n := u.String()
	return n, host, nil
}

func hostFromURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return strings.ToLower(u.Hostname())
}

func isDomainAllowed(host string, seedHosts map[string]bool, allow map[string]bool, crawling bool) bool {
	if host == "" {
		return false
	}
	if len(allow) > 0 {
		return allow[strings.ToLower(host)]
	}
	if !crawling {
		return true
	}
	return seedHosts[strings.ToLower(host)]
}

func bearerHeader(token string) string {
	t := strings.TrimSpace(token)
	if t == "" {
		return ""
	}
	return "Bearer " + t
}

func isValidDomainOrIP(host string) bool {
	host = strings.ToLower(strings.TrimSpace(host))
	if host == "" {
		return false
	}
	if host == "localhost" {
		return true
	}
	if ip := net.ParseIP(host); ip != nil {
		return true
	}
	if strings.HasSuffix(host, ".") {
		host = strings.TrimSuffix(host, ".")
	}
	parts := strings.Split(host, ".")
	if len(parts) < 2 {
		return false
	}
	for _, p := range parts {
		if !hostLabelRE.MatchString(p) {
			return false
		}
		if strings.HasPrefix(p, "-") || strings.HasSuffix(p, "-") {
			return false
		}
	}
	return true
}

func preflightURLReachable(client *http.Client, rawURL string, opts RemoteIndexOptions) error {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return err
	}
	host := strings.ToLower(strings.TrimSpace(u.Hostname()))
	if !isValidDomainOrIP(host) {
		return fmt.Errorf("invalid domain")
	}
	if _, err := net.LookupHost(host); err != nil {
		return fmt.Errorf("domain not resolvable: %w", err)
	}

	origin := u.Scheme + "://" + u.Host
	req, err := http.NewRequest(http.MethodHead, origin, nil)
	if err != nil {
		return err
	}
	if ua := strings.TrimSpace(opts.UserAgent); ua != "" {
		req.Header.Set("User-Agent", ua)
	}
	if ah := strings.TrimSpace(opts.AuthHeader); ah != "" {
		req.Header.Set("Authorization", ah)
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("host offline: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusMethodNotAllowed || resp.StatusCode == http.StatusNotImplemented {
		reqGet, err := http.NewRequest(http.MethodGet, origin, nil)
		if err != nil {
			return err
		}
		if ua := strings.TrimSpace(opts.UserAgent); ua != "" {
			reqGet.Header.Set("User-Agent", ua)
		}
		if ah := strings.TrimSpace(opts.AuthHeader); ah != "" {
			reqGet.Header.Set("Authorization", ah)
		}
		resp2, err := client.Do(reqGet)
		if err != nil {
			return fmt.Errorf("host offline: %w", err)
		}
		defer resp2.Body.Close()
	}
	return nil
}

func parseHFFirstRows(body []byte) ([]string, error) {
	var payload map[string]interface{}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, err
	}
	rawRows, ok := payload["rows"].([]interface{})
	if !ok || len(rawRows) == 0 {
		return nil, fmt.Errorf("no rows in dataset response")
	}
	out := make([]string, 0, len(rawRows))
	for _, item := range rawRows {
		text := flattenHFRow(item)
		if strings.TrimSpace(text) != "" {
			out = append(out, text)
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("rows were empty after flattening")
	}
	return out, nil
}

func extractHFErrorMessage(body []byte) string {
	var payload map[string]interface{}
	if err := json.Unmarshal(body, &payload); err != nil {
		msg := strings.TrimSpace(string(body))
		if msg == "" {
			return ""
		}
		if len(msg) > 200 {
			msg = msg[:200] + "..."
		}
		return msg
	}
	for _, k := range []string{"error", "message", "cause"} {
		if v, ok := payload[k]; ok {
			msg := strings.TrimSpace(flattenHFRow(v))
			if msg != "" {
				if len(msg) > 200 {
					msg = msg[:200] + "..."
				}
				return msg
			}
		}
	}
	return ""
}

func fetchHFSplitHint(client *http.Client, base, datasetID string, opts RemoteIndexOptions) (string, error) {
	endpoint := strings.TrimRight(base, "/") + "/splits?dataset=" + url.QueryEscape(strings.TrimSpace(datasetID))
	body, _, status, err := fetchURL(client, endpoint, RemoteIndexOptions{
		MaxBytes:   opts.MaxBytes,
		UserAgent:  opts.UserAgent,
		AuthHeader: bearerHeader(opts.HFToken),
	})
	if err != nil {
		return "", err
	}
	if status < 200 || status >= 300 {
		return "", fmt.Errorf("splits endpoint status %d", status)
	}

	var payload map[string]interface{}
	if err := json.Unmarshal(body, &payload); err != nil {
		return "", err
	}
	raw, ok := payload["splits"].([]interface{})
	if !ok || len(raw) == 0 {
		return "", fmt.Errorf("no split data returned")
	}
	configs := map[string]bool{}
	splits := map[string]bool{}
	for _, item := range raw {
		rec, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		cfg := strings.TrimSpace(fmt.Sprintf("%v", rec["config"]))
		spl := strings.TrimSpace(fmt.Sprintf("%v", rec["split"]))
		if cfg != "" && cfg != "<nil>" {
			configs[cfg] = true
		}
		if spl != "" && spl != "<nil>" {
			splits[spl] = true
		}
	}
	if len(configs) == 0 && len(splits) == 0 {
		return "", fmt.Errorf("split/config metadata empty")
	}
	return fmt.Sprintf("available configs=%s splits=%s", joinSortedKeys(configs), joinSortedKeys(splits)), nil
}

func resolveKaggleCredentials(opts RemoteIndexOptions) (string, string, error) {
	username := strings.TrimSpace(opts.KaggleUsername)
	if username == "" {
		username = strings.TrimSpace(os.Getenv("KAGGLE_USERNAME"))
	}
	key := strings.TrimSpace(opts.KaggleKey)
	if key == "" {
		key = strings.TrimSpace(os.Getenv("KAGGLE_KEY"))
	}
	if key == "" {
		apiKey := strings.TrimSpace(os.Getenv("KAGGLE_API_KEY"))
		if strings.Contains(apiKey, ":") {
			parts := strings.SplitN(apiKey, ":", 2)
			if username == "" {
				username = strings.TrimSpace(parts[0])
			}
			key = strings.TrimSpace(parts[1])
		} else {
			key = apiKey
		}
	}
	if username == "" || key == "" {
		return "", "", fmt.Errorf("kaggle credentials missing: set KAGGLE_USERNAME and KAGGLE_KEY (or KAGGLE_API_KEY)")
	}
	return username, key, nil
}

func kaggleBasicAuthHeader(username, key string) string {
	raw := strings.TrimSpace(username) + ":" + strings.TrimSpace(key)
	if strings.TrimSpace(username) == "" || strings.TrimSpace(key) == "" {
		return ""
	}
	return "Basic " + base64.StdEncoding.EncodeToString([]byte(raw))
}

func splitKaggleDatasetID(datasetID string) (string, string, error) {
	parts := strings.Split(strings.TrimSpace(datasetID), "/")
	if len(parts) != 2 || strings.TrimSpace(parts[0]) == "" || strings.TrimSpace(parts[1]) == "" {
		return "", "", fmt.Errorf("dataset id must be owner/dataset")
	}
	return strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1]), nil
}

func fetchKaggleDatasetFiles(client *http.Client, base, owner, dataset, username, key string, opts RemoteIndexOptions) ([]string, error) {
	endpoint := fmt.Sprintf("%s/datasets/list/%s/%s/files", strings.TrimRight(base, "/"), url.PathEscape(owner), url.PathEscape(dataset))
	body, _, status, err := fetchURL(client, endpoint, RemoteIndexOptions{
		MaxBytes:   opts.MaxBytes,
		UserAgent:  opts.UserAgent,
		AuthHeader: kaggleBasicAuthHeader(username, key),
	})
	if err != nil {
		return nil, err
	}
	if status < 200 || status >= 300 {
		return nil, fmt.Errorf("list files status %d", status)
	}
	var asList []map[string]interface{}
	if err := json.Unmarshal(body, &asList); err == nil {
		out := make([]string, 0, len(asList))
		for _, item := range asList {
			name := strings.TrimSpace(fmt.Sprintf("%v", item["name"]))
			if name != "" && name != "<nil>" {
				out = append(out, name)
			}
		}
		return out, nil
	}
	var wrapped struct {
		Files []map[string]interface{} `json:"files"`
	}
	if err := json.Unmarshal(body, &wrapped); err != nil {
		return nil, fmt.Errorf("parse files response: %w", err)
	}
	out := make([]string, 0, len(wrapped.Files))
	for _, item := range wrapped.Files {
		name := strings.TrimSpace(fmt.Sprintf("%v", item["name"]))
		if name != "" && name != "<nil>" {
			out = append(out, name)
		}
	}
	return out, nil
}

func selectKaggleFiles(allFiles []string, include []string) []string {
	allow := map[string]bool{}
	for _, f := range include {
		f = strings.TrimSpace(f)
		if f != "" {
			allow[f] = true
		}
	}
	out := make([]string, 0, len(allFiles))
	for _, name := range allFiles {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		if len(allow) > 0 && !allow[name] {
			continue
		}
		ext := strings.ToLower(filepath.Ext(name))
		switch ext {
		case ".csv", ".tsv", ".jsonl", ".txt", ".md":
			out = append(out, name)
		}
	}
	return out
}

func parseKaggleRows(fileName string, body []byte, maxRecords int) ([]string, error) {
	if maxRecords <= 0 {
		maxRecords = 100
	}
	ext := strings.ToLower(filepath.Ext(strings.TrimSpace(fileName)))
	switch ext {
	case ".csv", ".tsv":
		reader := csv.NewReader(bytes.NewReader(body))
		if ext == ".tsv" {
			reader.Comma = '\t'
		}
		rows := make([][]string, 0, maxRecords+1)
		for len(rows) < maxRecords+1 {
			rec, err := reader.Read()
			if err == io.EOF {
				break
			}
			if err != nil {
				return nil, err
			}
			rows = append(rows, rec)
		}
		if len(rows) == 0 {
			return nil, fmt.Errorf("empty tabular file")
		}
		header := rows[0]
		out := make([]string, 0, len(rows)-1)
		for i := 1; i < len(rows); i++ {
			out = append(out, stringifyTabularRow(header, rows[i]))
		}
		if len(out) == 0 {
			out = append(out, strings.Join(header, ", "))
		}
		return out, nil
	case ".jsonl":
		sc := bufio.NewScanner(bytes.NewReader(body))
		out := make([]string, 0, maxRecords)
		for sc.Scan() {
			line := strings.TrimSpace(sc.Text())
			if line == "" {
				continue
			}
			out = append(out, line)
			if len(out) >= maxRecords {
				break
			}
		}
		if err := sc.Err(); err != nil {
			return nil, err
		}
		if len(out) == 0 {
			return nil, fmt.Errorf("empty jsonl file")
		}
		return out, nil
	case ".txt", ".md":
		sc := bufio.NewScanner(bytes.NewReader(body))
		out := make([]string, 0, maxRecords)
		for sc.Scan() {
			line := strings.TrimSpace(sc.Text())
			if line == "" {
				continue
			}
			out = append(out, line)
			if len(out) >= maxRecords {
				break
			}
		}
		if err := sc.Err(); err != nil {
			return nil, err
		}
		if len(out) == 0 {
			return nil, fmt.Errorf("empty text file")
		}
		return out, nil
	default:
		return nil, fmt.Errorf("unsupported file type: %s", ext)
	}
}

func stringifyTabularRow(header, row []string) string {
	if len(header) == 0 {
		return strings.Join(row, ", ")
	}
	parts := make([]string, 0, len(row))
	for i := 0; i < len(row); i++ {
		key := fmt.Sprintf("col_%d", i+1)
		if i < len(header) && strings.TrimSpace(header[i]) != "" {
			key = strings.TrimSpace(header[i])
		}
		parts = append(parts, key+": "+strings.TrimSpace(row[i]))
	}
	return strings.Join(parts, "\n")
}

func joinSortedKeys(m map[string]bool) string {
	if len(m) == 0 {
		return "n/a"
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	if len(keys) > 8 {
		keys = append(keys[:8], "...")
	}
	return strings.Join(keys, ",")
}

func flattenHFRow(row interface{}) string {
	switch v := row.(type) {
	case map[string]interface{}:
		if inner, ok := v["row"]; ok {
			return flattenHFRow(inner)
		}
		var lines []string
		for k, val := range v {
			lines = append(lines, fmt.Sprintf("%s: %s", k, flattenHFRow(val)))
		}
		return strings.Join(lines, "\n")
	case []interface{}:
		var parts []string
		for _, e := range v {
			parts = append(parts, flattenHFRow(e))
		}
		return strings.Join(parts, ", ")
	case string:
		return v
	case float64, bool, int, int64:
		return fmt.Sprintf("%v", v)
	case nil:
		return ""
	default:
		b, _ := json.Marshal(v)
		return string(b)
	}
}
