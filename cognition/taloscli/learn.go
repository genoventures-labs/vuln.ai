package taloscli

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/Thynaptic/P-LMv1/pkg/connectors"
	githubconn "github.com/Thynaptic/P-LMv1/pkg/connectors/github"
	googleconn "github.com/Thynaptic/P-LMv1/pkg/connectors/google"
	"github.com/Thynaptic/P-LMv1/pkg/connectors/gutenberg"
	"github.com/Thynaptic/P-LMv1/pkg/connectors/notion"
	"github.com/Thynaptic/P-LMv1/pkg/memory"
	"github.com/Thynaptic/P-LMv1/pkg/rag"
	"github.com/Thynaptic/P-LMv1/pkg/state"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

var learnFile string
var learnDir string
var learnRecursive bool
var learnExtensions string
var learnTypes []string
var learnAllTypes bool
var learnChunkChars int
var learnChunkOverlap int
var learnURLs []string
var learnURLFile string
var learnCrawl bool
var learnCrawlDepth int
var learnMaxPages int
var learnRateLimit float64
var learnAllowedDomains []string
var learnUserAgent string
var learnRemoteTimeout time.Duration
var learnMaxBytes int64
var learnAuthHeaderEnv string
var learnHFDatasets []string
var learnHFConfig string
var learnHFSplit string
var learnHFMaxRecords int
var learnKaggleDatasets []string
var learnKaggleFiles []string
var learnKaggleMaxRecords int
var learnURLSafety bool
var learnURLSafetyTimeout time.Duration
var learnURLSafetyCacheTTL time.Duration
var learnURLSafetyVisibility string
var learnURLSafetyFailOpen bool
var learnFromResearch string
var learnIncludeResearchSources bool
var learnIncludeResearchSummary bool
var learnSummarizeSources bool
var learnSummaryMaxChars int
var learnSummaryMaxPoints int
var learnSummaryModel string
var learnTitleChunks bool
var learnTitleMaxChars int
var learnTitleModel string
var learnIncremental bool
var learnIncrementalManifest string
var learnNamespace string
var learnDryRun bool
var learnVerbose bool
var learnSyntheticTextOut string
var learnSelfTrainSpeech bool

// Google Workspace + Notion connector flags
var learnGmailQuery string
var learnGmailMax int
var learnGdriveFolderID string
var learnGdriveQuery string
var learnGdriveMax int
var learnNotionDatabaseID string
var learnNotionFilter string

// Project Gutenberg connector flags
var learnBookSearch string
var learnBookIDs []string
var learnBookMax int

// GitHub connector flags
var learnGitHubRepos []string
var learnGitHubPath string
var learnGitHubMax int

// Chain flag — comma-separated ordered source tokens
var learnChain string

var learnCmd = &cobra.Command{
	Use:   "learn [text]",
	Short: "Add knowledge to your personal LLM's memory.",
	Long: `Teach TALOS new information by ingesting text, files, directories, URLs, Hugging Face datasets, or live API sources.

LOCAL SOURCES
  talos learn "some text"
  talos learn --self-train-speech "some text"
  talos learn --file ./notes.md
  talos learn --dir ./docs --extensions .md,.txt
  talos learn --dir ./docs --namespace talos-runtime
  talos learn --url https://example.com/page --crawl --crawl-depth 1
  talos learn --url https://example.com/page --crawl --extensions .html,.md
  talos learn --from-research latest
  HF_TOKEN=... talos learn --hf-dataset wikipedia --hf-split train
  KAGGLE_USERNAME=... KAGGLE_KEY=... talos learn --kaggle-dataset owner/dataset

GOOGLE WORKSPACE  (requires GOOGLE_SERVICE_ACCOUNT_JSON + GOOGLE_IMPERSONATE_USER)
  talos learn --gmail-query "label:inbox after:2024/01/01" --gmail-max 100
  talos learn --gdrive-folder <folderID>
  talos learn --gdrive-query "mimeType='application/vnd.google-apps.document'"
  talos learn --gdrive-max 50

NOTION  (requires NOTION_API_KEY)
  talos learn --notion-database <databaseID>
  talos learn --notion-database <databaseID> --notion-filter '{"property":"Status","select":{"equals":"Done"}}'

PROJECT GUTENBERG  (no credentials required)
  talos learn --book-search "frankenstein"
  talos learn --book-search "the art of war" --book-max 3
  talos learn --book-id 84
  talos learn --book-id 84 --book-id 1342 --book-id 11

KAGGLE DATASETS  (requires KAGGLE_USERNAME + KAGGLE_KEY or KAGGLE_API_KEY)
  talos learn --kaggle-dataset owner/dataset
  talos learn --kaggle-dataset owner/dataset --kaggle-file train.csv --kaggle-max-records 250

GITHUB  (GITHUB_TOKEN optional — increases rate limit)
  talos learn --github-repo owner/repo
  talos learn --github-repo owner/repo --github-path pkg/ --github-max 200
  talos learn --github-repo owner/repo1 --github-repo owner/repo2

CHAINED SOURCES  (--chain runs sources in the specified order)
  talos learn --chain "dir,github,hf,kaggle" --dir ./docs --github-repo owner/repo --hf-dataset owner/dataset --kaggle-dataset owner/dataset
  talos learn --chain "books,url" --book-search "moby dick" --url https://example.com
  talos learn --chain "github,notion" --github-repo owner/repo --notion-database <id>

URL CRAWL RULES
  - Learn now shows a single live progress line with synced counters (non-stacking) by default.
  - Default URL/crawl mode indexes text extracted from HTML pages only.
  - If --extensions is set, URL mode switches to strict extension matching.
  - In strict mode, only matching URL path extensions are crawled/indexed; extensionless URLs are skipped.
  - Use --verbose to show the current detailed technical learn summary output.
  - Use --namespace to write all ingested records into an isolated memory domain namespace.
  - When --namespace is supplied, it becomes the persisted active namespace for future chat/research runs.

All sources are chunked and indexed into persistent memory for future retrieval.`,
	Run: func(cmd *cobra.Command, args []string) {
		if _, err := resolveLearnProfileForRun(cmd); err != nil {
			fmt.Printf("Error resolving learn profile: %v\n", err)
			return
		}
		sm, _ := state.NewManager()
		effectiveNamespace := resolveRuntimeNamespace(sm, learnNamespace)
		explicitNamespace := strings.ToLower(strings.TrimSpace(learnNamespace))
		if explicitNamespace != "" || strings.ToLower(strings.TrimSpace(requestedNamespace)) != "" {
			learnNamespace = effectiveNamespace
			if sm != nil {
				sm.SetActiveNamespace(learnNamespace)
				if err := sm.Save(); err != nil {
					fmt.Printf("Warning: Failed to persist active namespace: %v\n", err)
				}
			}
		} else {
			learnNamespace = effectiveNamespace
		}
		if learnDryRun {
			plan, err := renderLearnDryRunPlan(args)
			if err != nil {
				fmt.Printf("Error: %v\n", err)
				return
			}
			fmt.Println(plan)
			return
		}
		if strings.TrimSpace(learnSyntheticTextOut) != "" {
			if err := executeLearnSyntheticText(args); err != nil {
				fmt.Printf("Error generating synthetic text artifacts: %v\n", err)
			}
			return
		}
		if strings.TrimSpace(learnFromResearch) != "" {
			if err := executeLearnFromResearch(learnFromResearch, learnIncludeResearchSummary, learnIncludeResearchSources); err != nil {
				fmt.Printf("Error learning from research artifact: %v\n", err)
			}
			return
		}
		mm, err := memory.NewMemoryManager()
		if err != nil {
			fmt.Printf("Error initializing memory manager: %v\n", err)
			return
		}
		if ns := strings.TrimSpace(learnNamespace); ns != "" {
			mm.SetActiveNamespace(ns)
		}

		// Chain dispatch — runs before all individual source checks.
		if strings.TrimSpace(learnChain) != "" {
			executeLearnChain(args, mm)
			return
		}

		// Google Workspace + Notion + Gutenberg + GitHub connector dispatch
		if learnGmailQuery != "" || learnGdriveFolderID != "" || learnGdriveQuery != "" || learnNotionDatabaseID != "" || learnBookSearch != "" || len(learnBookIDs) > 0 || len(learnGitHubRepos) > 0 {
			executeLearnFromConnectors(mm)
			return
		}

		if learnDir != "" {
			progress := newLearnProgress()
			progress.setPhase("dir", 1, 1)
			session := newLearnSession("DIRECTORY", learnDir, []string{learnDir}, map[string]string{
				"recursive":     fmt.Sprintf("%t", learnRecursive),
				"chunk_chars":   fmt.Sprintf("%d", learnChunkChars),
				"chunk_overlap": fmt.Sprintf("%d", learnChunkOverlap),
				"summarization": fmt.Sprintf("%t", learnSummarizeSources),
				"chunk_titles":  fmt.Sprintf("%t", learnTitleChunks),
				"incremental":   fmt.Sprintf("%t", learnIncremental),
			})
			annotateLearnSessionWithProfile(&session, learnProfileApplied)
			opts := rag.DefaultIndexOptions()
			opts.Recursive = learnRecursive
			opts.MaxChunkChars = learnChunkChars
			opts.ChunkOverlap = learnChunkOverlap
			opts.GenerateChunkTitles = learnTitleChunks
			opts.ChunkTitleMaxChars = learnTitleMaxChars
			opts.ChunkTitleModel = strings.TrimSpace(learnTitleModel)
			opts.Incremental = learnIncremental
			opts.IncrementalManifestPath = strings.TrimSpace(learnIncrementalManifest)
			opts.AllTypes = learnAllTypes

			extensionInput := strings.TrimSpace(learnExtensions)
			if len(learnTypes) > 0 {
				typeCSV := strings.Join(learnTypes, ",")
				if extensionInput == "" {
					extensionInput = typeCSV
				} else {
					extensionInput += "," + typeCSV
				}
			}

			opts.Extensions = rag.ParseExtensionsCSV(extensionInput)
			if !opts.AllTypes && len(opts.Extensions) == 0 {
				opts.Extensions = rag.DefaultIndexOptions().Extensions
			}
			opts.OnFileEvent = func(event rag.FileEvent) {
				progress.onFileEvent(event.Outcome, event.Chunks, event.FilesScanned, event.FilesIndexed)
				if learnVerbose {
					switch event.Outcome {
					case "indexed":
						progress.verbosef("[%d scanned | %d indexed] indexed %s (%d chunks)\n", event.FilesScanned, event.FilesIndexed, event.RelPath, event.Chunks)
					case "skipped-unsupported":
						progress.verbosef("[%d scanned] skipped (type filter) %s\n", event.FilesScanned, event.RelPath)
					case "skipped-binary":
						progress.verbosef("[%d scanned] skipped (binary) %s\n", event.FilesScanned, event.RelPath)
					case "parse-error":
						progress.verbosef("[%d scanned] parse error %s: %s\n", event.FilesScanned, event.RelPath, event.Error)
					case "index-error":
						progress.verbosef("[%d scanned] index error %s: %s\n", event.FilesScanned, event.RelPath, event.Error)
					case "walk-error":
						progress.verbosef("walk error %s: %s\n", event.RelPath, event.Error)
					}
				}
			}
			var summaryWorker *memory.SourceSummaryWorker
			if learnSummarizeSources {
				sw, swErr := memory.NewSourceSummaryWorker(mm, session.SessionID, summaryConfigFromLearnFlags(), 2, 128)
				if swErr != nil {
					progress.verbosef("Warning: source summarization unavailable: %v\n", swErr)
				} else {
					summaryWorker = sw
					opts.OnSourceIndexed = func(ev rag.SourceIndexedEvent) {
						summaryWorker.Enqueue(memory.SourceSummaryRequest{
							SourceType: ev.SourceType,
							SourceRef:  ev.SourceRef,
							Content:    ev.Content,
							SessionID:  session.SessionID,
							ChunkCount: ev.ChunkCount,
							Metadata:   ev.Metadata,
						})
					}
				}
			}

			progress.verbosef("Indexing directory: %s\n", learnDir)
			if opts.AllTypes {
				progress.verbosef("Type filter: all file types (text-like files will be indexed; binary files will be skipped)\n")
			} else {
				progress.verbosef("Type filter: %s\n", rag.ExtensionsToCSV(opts.Extensions))
			}
			stats, err := rag.IndexDirectory(mm, learnDir, opts)
			progress.finish()
			summaryMetrics := memory.SourceSummaryMetrics{}
			if summaryWorker != nil {
				summaryMetrics = summaryWorker.CloseAndFlush(20 * time.Second)
				_ = summaryWorker.AddSessionAggregateSummary(buildLearnSessionSummaryText(session.Mode, session.QueryOrTarget, stats.FilesIndexed, int(summaryMetrics.SummaryDocsIndexed)), int(summaryMetrics.JobsCompleted))
				summaryMetrics = summaryWorker.Metrics()
			}
			if err != nil {
				fmt.Printf("Error indexing directory: %v\n", err)
				metrics := map[string]int64{
					"files_scanned":   int64(stats.FilesScanned),
					"files_indexed":   int64(stats.FilesIndexed),
					"files_unchanged": int64(stats.FilesUnchanged),
					"files_changed":   int64(stats.FilesChanged),
					"files_added":     int64(stats.FilesAdded),
					"files_removed":   int64(stats.FilesRemoved),
					"chunks_archived": int64(stats.ChunksArchived),
					"chunks_indexed":  int64(stats.ChunksIndexed),
					"errors":          int64(stats.ParseErrors + stats.IndexErrors + stats.WalkErrors),
				}
				mergeSummaryMetrics(metrics, summaryMetrics)
				session.finish("FAILED", "Directory indexing failed.", err.Error(), metrics)
				if logErr := appendLearnSessionRecord(session); logErr != nil {
					fmt.Printf("Warning: Failed to write learn session log: %v\n", logErr)
				}
				return
			}
			metrics := map[string]int64{
				"files_scanned":       int64(stats.FilesScanned),
				"files_indexed":       int64(stats.FilesIndexed),
				"files_unchanged":     int64(stats.FilesUnchanged),
				"files_changed":       int64(stats.FilesChanged),
				"files_added":         int64(stats.FilesAdded),
				"files_removed":       int64(stats.FilesRemoved),
				"chunks_archived":     int64(stats.ChunksArchived),
				"chunks_indexed":      int64(stats.ChunksIndexed),
				"skipped_unsupported": int64(stats.SkippedUnsupported),
				"skipped_binary":      int64(stats.SkippedBinary),
				"parse_errors":        int64(stats.ParseErrors),
				"index_errors":        int64(stats.IndexErrors),
				"walk_errors":         int64(stats.WalkErrors),
			}
			mergeSummaryMetrics(metrics, summaryMetrics)
			mergeTopologyMetrics(metrics, mm.TopologyStats())
			session.finish("SUCCESS", "Directory learning completed successfully.", "", metrics)
			if logErr := appendLearnSessionRecord(session); logErr != nil {
				fmt.Printf("Warning: Failed to write learn session log: %v\n", logErr)
			}
			if learnVerbose {
				fmt.Println(renderDirectoryLearnSummary(learnDir, opts, stats, summaryMetrics, mm.TopologyStats()))
			} else {
				fmt.Println(renderDirectoryLearnSummaryFriendly(learnDir, stats))
			}
			return
		}

		if len(learnURLs) > 0 || strings.TrimSpace(learnURLFile) != "" || len(learnHFDatasets) > 0 || len(learnKaggleDatasets) > 0 {
			progress := newLearnProgress()
			progress.setPhase("remote", 1, 1)
			remoteOpts := rag.DefaultRemoteIndexOptions()
			remoteOpts.MaxChunkChars = learnChunkChars
			remoteOpts.ChunkOverlap = learnChunkOverlap
			remoteOpts.GenerateChunkTitles = learnTitleChunks
			remoteOpts.ChunkTitleMaxChars = learnTitleMaxChars
			remoteOpts.ChunkTitleModel = strings.TrimSpace(learnTitleModel)
			remoteOpts.Crawl = learnCrawl
			remoteOpts.CrawlDepth = learnCrawlDepth
			remoteOpts.MaxPages = learnMaxPages
			remoteOpts.RateLimitPerSec = learnRateLimit
			remoteOpts.UserAgent = learnUserAgent
			remoteOpts.Timeout = learnRemoteTimeout
			remoteOpts.MaxBytes = learnMaxBytes
			remoteOpts.HFToken = strings.TrimSpace(os.Getenv("HF_TOKEN"))
			remoteOpts.KaggleUsername = strings.TrimSpace(os.Getenv("KAGGLE_USERNAME"))
			remoteOpts.KaggleKey = strings.TrimSpace(os.Getenv("KAGGLE_KEY"))
			if remoteOpts.KaggleKey == "" {
				remoteOpts.KaggleKey = strings.TrimSpace(os.Getenv("KAGGLE_API_KEY"))
			}
			remoteOpts.URLSafetyEnabled = learnURLSafety
			remoteOpts.URLSafetyTimeout = learnURLSafetyTimeout
			remoteOpts.URLSafetyCacheTTL = learnURLSafetyCacheTTL
			remoteOpts.URLSafetyVisibility = learnURLSafetyVisibility
			remoteOpts.URLSafetyFailOpen = learnURLSafetyFailOpen
			remoteOpts.URLSafetyAPIKey = strings.TrimSpace(os.Getenv("URLSCAN_API_KEY"))
			remoteOpts.URLAllowedExts = remoteURLAllowedExtensions()
			if envName := strings.TrimSpace(learnAuthHeaderEnv); envName != "" {
				remoteOpts.AuthHeader = strings.TrimSpace(os.Getenv(envName))
			}
			if len(learnAllowedDomains) > 0 {
				remoteOpts.AllowedDomains = make(map[string]bool, len(learnAllowedDomains))
				for _, d := range learnAllowedDomains {
					d = strings.ToLower(strings.TrimSpace(d))
					if d != "" {
						remoteOpts.AllowedDomains[d] = true
					}
				}
			}
			remoteOpts.OnEvent = func(ev rag.RemoteEvent) {
				progress.onRemoteEvent(ev.Outcome)
				if learnVerbose {
					if line, ok := formatRemoteLearnEventLine(ev, true); ok {
						progress.verbosef("%s\n", line)
					}
				}
			}

			seedURLs, urlsErr := collectURLs(learnURLs, learnURLFile)
			if urlsErr != nil {
				progress.finish()
				fmt.Printf("Error collecting URLs: %v\n", urlsErr)
				return
			}
			sources := append([]string(nil), seedURLs...)
			for _, ds := range learnHFDatasets {
				ds = strings.TrimSpace(ds)
				if ds != "" {
					sources = append(sources, "hf:"+ds)
				}
			}
			for _, ds := range learnKaggleDatasets {
				ds = strings.TrimSpace(ds)
				if ds != "" {
					sources = append(sources, "kaggle:"+ds)
				}
			}
			session := newLearnSession("REMOTE", "remote", sources, map[string]string{
				"crawl":                fmt.Sprintf("%t", learnCrawl),
				"crawl_depth":          fmt.Sprintf("%d", learnCrawlDepth),
				"max_pages":            fmt.Sprintf("%d", learnMaxPages),
				"url_safety":           fmt.Sprintf("%t", learnURLSafety),
				"url_safety_fail_open": fmt.Sprintf("%t", learnURLSafetyFailOpen),
				"summarization":        fmt.Sprintf("%t", learnSummarizeSources),
				"chunk_titles":         fmt.Sprintf("%t", learnTitleChunks),
				"kaggle_max_records":   fmt.Sprintf("%d", learnKaggleMaxRecords),
			})
			annotateLearnSessionWithProfile(&session, learnProfileApplied)
			var summaryWorker *memory.SourceSummaryWorker
			if learnSummarizeSources {
				sw, swErr := memory.NewSourceSummaryWorker(mm, session.SessionID, summaryConfigFromLearnFlags(), 2, 128)
				if swErr != nil {
					progress.verbosef("Warning: source summarization unavailable: %v\n", swErr)
				} else {
					summaryWorker = sw
					remoteOpts.OnSourceIndexed = func(ev rag.SourceIndexedEvent) {
						summaryWorker.Enqueue(memory.SourceSummaryRequest{
							SourceType: ev.SourceType,
							SourceRef:  ev.SourceRef,
							Content:    ev.Content,
							SessionID:  session.SessionID,
							ChunkCount: ev.ChunkCount,
							Metadata:   ev.Metadata,
						})
					}
				}
			}
			totalStats := rag.RemoteIndexStats{}
			if len(seedURLs) > 0 {
				if remoteOpts.URLSafetyEnabled && strings.TrimSpace(remoteOpts.URLSafetyAPIKey) == "" && !remoteOpts.URLSafetyFailOpen {
					progress.finish()
					fmt.Println("Error: URL safety is enabled but URLSCAN_API_KEY is not set.")
					fmt.Println("Set URLSCAN_API_KEY or run with --url-safety-fail-open.")
					session.finish("FAILED", "Remote learning aborted before execution.", "URL safety key missing", map[string]int64{
						"items_indexed": 0,
						"errors":        1,
					})
					if logErr := appendLearnSessionRecord(session); logErr != nil {
						fmt.Printf("Warning: Failed to write learn session log: %v\n", logErr)
					}
					return
				}
				if learnVerbose {
					progress.verbosef("Indexing remote URLs: %d seed(s)\n", len(seedURLs))
					if !learnCrawl {
						progress.verbosef("Crawl mode: disabled (single URL mode)\n")
					} else {
						progress.verbosef("Crawl mode: enabled (depth=%d, max-pages=%d, rate=%.2f req/s)\n", learnCrawlDepth, learnMaxPages, learnRateLimit)
					}
					if remoteOpts.URLSafetyEnabled {
						progress.verbosef("URL safety: enabled (visibility=%s, timeout=%s, cache-ttl=%s, fail-open=%t)\n", remoteOpts.URLSafetyVisibility, remoteOpts.URLSafetyTimeout, remoteOpts.URLSafetyCacheTTL, remoteOpts.URLSafetyFailOpen)
					} else {
						progress.verbosef("URL safety: disabled\n")
					}
				}
				urlStats, urlErr := rag.IndexURLs(mm, seedURLs, remoteOpts)
				totalStats = mergeRemoteStats(totalStats, urlStats)
				if urlErr != nil {
					progress.addError()
					fmt.Printf("URL indexing error: %v\n", urlErr)
				}
			}

			if len(learnHFDatasets) > 0 {
				specs := make([]rag.HFSpec, 0, len(learnHFDatasets))
				for _, ds := range learnHFDatasets {
					ds = strings.TrimSpace(ds)
					if ds == "" {
						continue
					}
					specs = append(specs, rag.HFSpec{
						DatasetID:  ds,
						Config:     strings.TrimSpace(learnHFConfig),
						Split:      strings.TrimSpace(learnHFSplit),
						MaxRecords: learnHFMaxRecords,
					})
				}
				if len(specs) > 0 {
					progress.setPhase("hf", 1, 1)
					progress.verbosef("Indexing HF datasets: %d dataset(s)\n", len(specs))
					hfStats, hfErr := rag.IndexHFDatasets(mm, specs, remoteOpts)
					totalStats = mergeRemoteStats(totalStats, hfStats)
					if hfErr != nil {
						progress.addError()
						fmt.Printf("HF indexing error: %v\n", hfErr)
					}
				}
			}
			if len(learnKaggleDatasets) > 0 {
				specs := make([]rag.KaggleSpec, 0, len(learnKaggleDatasets))
				for _, ds := range learnKaggleDatasets {
					ds = strings.TrimSpace(ds)
					if ds == "" {
						continue
					}
					specs = append(specs, rag.KaggleSpec{
						DatasetID:  ds,
						Files:      append([]string(nil), learnKaggleFiles...),
						MaxRecords: learnKaggleMaxRecords,
					})
				}
				if len(specs) > 0 {
					progress.setPhase("kaggle", 1, 1)
					progress.verbosef("Indexing Kaggle datasets: %d dataset(s)\n", len(specs))
					kaggleStats, kaggleErr := rag.IndexKaggleDatasets(mm, specs, remoteOpts)
					totalStats = mergeRemoteStats(totalStats, kaggleStats)
					if kaggleErr != nil {
						progress.addError()
						fmt.Printf("Kaggle indexing error: %v\n", kaggleErr)
					}
				}
			}
			progress.finish()

			status := "SUCCESS"
			failureReason := ""
			if totalStats.ItemsIndexed == 0 &&
				(totalStats.HTTPErrors > 0 || totalStats.ParseErrors > 0 || totalStats.IndexErrors > 0 || totalStats.PreflightFailures > 0 || totalStats.SafetyErrors > 0 || totalStats.SafetyBlocked > 0) {
				status = "FAILED"
				failureReason = "No items were indexed."
			} else if totalStats.HTTPErrors > 0 || totalStats.ParseErrors > 0 || totalStats.IndexErrors > 0 || totalStats.PreflightFailures > 0 || totalStats.SafetyErrors > 0 || totalStats.SafetyBlocked > 0 {
				status = "PARTIAL"
			}
			summaryMetrics := memory.SourceSummaryMetrics{}
			if summaryWorker != nil {
				summaryMetrics = summaryWorker.CloseAndFlush(20 * time.Second)
				_ = summaryWorker.AddSessionAggregateSummary(buildLearnSessionSummaryText(session.Mode, session.QueryOrTarget, totalStats.ItemsIndexed, int(summaryMetrics.SummaryDocsIndexed)), int(summaryMetrics.JobsCompleted))
				summaryMetrics = summaryWorker.Metrics()
			}
			metrics := map[string]int64{
				"items_fetched":       int64(totalStats.ItemsFetched),
				"items_indexed":       int64(totalStats.ItemsIndexed),
				"chunks_indexed":      int64(totalStats.ChunksIndexed),
				"bytes_fetched":       totalStats.BytesFetched,
				"preflight_failures":  int64(totalStats.PreflightFailures),
				"safety_blocked":      int64(totalStats.SafetyBlocked),
				"safety_errors":       int64(totalStats.SafetyErrors),
				"safety_cache_hits":   int64(totalStats.SafetyCacheHits),
				"safety_cache_misses": int64(totalStats.SafetyCacheMisses),
				"parse_errors":        int64(totalStats.ParseErrors),
				"http_errors":         int64(totalStats.HTTPErrors),
				"index_errors":        int64(totalStats.IndexErrors),
			}
			mergeSummaryMetrics(metrics, summaryMetrics)
			mergeTopologyMetrics(metrics, mm.TopologyStats())
			session.finish(status, "Remote learning run completed.", failureReason, metrics)
			if logErr := appendLearnSessionRecord(session); logErr != nil {
				fmt.Printf("Warning: Failed to write learn session log: %v\n", logErr)
			}
			if learnVerbose {
				fmt.Println(renderRemoteLearnSummary(totalStats, summaryMetrics, mm.TopologyStats()))
			} else {
				fmt.Println(renderRemoteLearnSummaryFriendly(sources, totalStats))
			}
			return
		}

		var content string
		mode := "INLINE_TEXT"
		target := "inline"
		if learnFile != "" {
			data, err := ioutil.ReadFile(learnFile)
			if err != nil {
				fmt.Printf("Error reading file %s: %v\n", learnFile, err)
				session := newLearnSession("FILE", learnFile, []string{learnFile}, nil)
				annotateLearnSessionWithProfile(&session, learnProfileApplied)
				session.finish("FAILED", "File learning failed.", err.Error(), map[string]int64{"items_indexed": 0, "errors": 1})
				if logErr := appendLearnSessionRecord(session); logErr != nil {
					fmt.Printf("Warning: Failed to write learn session log: %v\n", logErr)
				}
				return
			}
			content = string(data)
			fmt.Printf("Learning from file: %s\n", learnFile)
			mode = "FILE"
			target = learnFile
		} else if len(args) > 0 {
			content = strings.Join(args, " ")
			fmt.Println("Learning from provided text.")
		} else {
			fmt.Println("Please provide text to learn or use the --file flag.")
			return
		}

		err = mm.AddKnowledge(content, nil)
		if err != nil {
			fmt.Printf("Error adding knowledge to memory: %v\n", err)
			sessionCfg := map[string]string{}
			if learnSelfTrainSpeech {
				sessionCfg["self_train_speech"] = "true"
			}
			session := newLearnSession(mode, target, []string{target}, sessionCfg)
			annotateLearnSessionWithProfile(&session, learnProfileApplied)
			session.finish("FAILED", "Inline/file learning failed.", err.Error(), map[string]int64{"items_indexed": 0, "errors": 1})
			if logErr := appendLearnSessionRecord(session); logErr != nil {
				fmt.Printf("Warning: Failed to write learn session log: %v\n", logErr)
			}
			return
		}
		status := "SUCCESS"
		summary := "Inline/file learning completed successfully."
		failureReason := ""
		metrics := map[string]int64{"items_indexed": 1, "chunks_indexed": 1}
		sessionCfg := map[string]string{}
		if learnSelfTrainSpeech {
			sessionCfg["self_train_speech"] = "true"
			selfTrainPath := resolveLearnSelfTrainSpeechOutPath()
			chunksWritten, synthErr := appendSyntheticTextArtifacts(content, target, selfTrainPath, learnChunkChars)
			if synthErr != nil {
				status = "PARTIAL"
				failureReason = "speech self-train generation failed: " + synthErr.Error()
				summary = "Inline/file learning completed; speech self-training artifact generation failed."
				metrics["errors"] = 1
			} else {
				sessionCfg["self_train_out"] = selfTrainPath
				metrics["self_train_chunks"] = int64(chunksWritten)
				fmt.Printf("Speech self-training artifacts updated: %s (%d chunks)\n", selfTrainPath, chunksWritten)
			}
		}
		session := newLearnSession(mode, target, []string{target}, sessionCfg)
		annotateLearnSessionWithProfile(&session, learnProfileApplied)
		session.finish(status, summary, failureReason, metrics)
		if logErr := appendLearnSessionRecord(session); logErr != nil {
			fmt.Printf("Warning: Failed to write learn session log: %v\n", logErr)
		}

		if learnVerbose {
			fmt.Println(renderInlineLearnSummary(learnFile != "", learnFile))
		} else {
			fmt.Println(renderInlineLearnSummaryFriendly(learnFile != "", learnFile))
		}
	},
}

func init() {
	learnCmd.Flags().StringVar(&learnProfile, "profile", "", "Learn profile name to apply (falls back to default profile when omitted)")
	learnCmd.Flags().BoolVar(&learnDryRun, "dry-run", false, "Print resolved learn execution plan without indexing")
	learnCmd.Flags().BoolVar(&learnVerbose, "verbose", false, "Show detailed learn summary output (default shows compact friendly output)")
	bindLearnConfigFlags(learnCmd.Flags())
	if f := learnCmd.Flags().Lookup("from-research"); f != nil {
		f.NoOptDefVal = "latest"
	}
	installLearnProfileCommands()
	rootCmd.AddCommand(learnCmd)
}

func bindLearnConfigFlags(fs *pflag.FlagSet) {
	fs.StringVarP(&learnFile, "file", "f", "", "Path to a file to learn from")
	fs.StringVar(&learnSyntheticTextOut, "synthetic-text-out", "", "Write deterministic synthetic text-generation artifacts (JSONL) and skip indexing")
	fs.BoolVar(&learnSelfTrainSpeech, "self-train-speech", false, "After inline/file learn, auto-generate speech self-training artifacts to default synthetic path")
	fs.StringVarP(&learnDir, "dir", "d", "", "Path to a directory of documents to index")
	fs.StringVar(&learnNamespace, "namespace", "", "Optional memory namespace for all ingested records in this run")
	fs.BoolVarP(&learnRecursive, "recursive", "r", true, "Recursively index subdirectories when using --dir")
	fs.IntVar(&learnChunkChars, "chunk-chars", 1200, "Maximum characters per indexed chunk when using --dir")
	fs.IntVar(&learnChunkOverlap, "chunk-overlap", 200, "Overlap between chunks when using --dir")
	fs.StringVar(&learnExtensions, "extensions", "", "Comma-separated extensions for --dir, and strict URL/crawl filtering when using --url (e.g. .html,.md,.txt)")
	fs.StringSliceVar(&learnTypes, "type", nil, "Repeatable file extension filter with --dir (e.g. --type .md --type .txt)")
	fs.BoolVar(&learnAllTypes, "all-types", false, "Index all file types under --dir (binary files are skipped)")
	fs.StringArrayVar(&learnURLs, "url", nil, "URL to index (repeatable)")
	fs.StringVar(&learnURLFile, "url-file", "", "Path to a newline-delimited URL list")
	fs.BoolVar(&learnCrawl, "crawl", false, "Follow links while indexing URLs")
	fs.IntVar(&learnCrawlDepth, "crawl-depth", 1, "Crawl depth when --crawl is enabled")
	fs.IntVar(&learnMaxPages, "max-pages", 200, "Maximum fetched pages/items per remote run")
	fs.Float64Var(&learnRateLimit, "rate-limit", 2.0, "Remote request rate limit in requests/sec")
	fs.StringSliceVar(&learnAllowedDomains, "allowed-domain", nil, "Allowed domain for URL crawling (repeatable)")
	fs.StringVar(&learnUserAgent, "user-agent", "talos/1.0 (+https://thynaptic.com)", "User agent for remote requests")
	fs.DurationVar(&learnRemoteTimeout, "remote-timeout", 20*time.Second, "Timeout for each remote request")
	fs.Int64Var(&learnMaxBytes, "max-bytes", 10*1024*1024, "Maximum bytes per remote item")
	fs.StringVar(&learnAuthHeaderEnv, "auth-header-env", "", "Environment variable containing Authorization header value for URL fetches")
	fs.BoolVar(&learnURLSafety, "url-safety", true, "Run URL safety verdict checks before indexing URL content")
	fs.DurationVar(&learnURLSafetyTimeout, "url-safety-timeout", 45*time.Second, "Timeout budget for URL safety scan verdict")
	fs.DurationVar(&learnURLSafetyCacheTTL, "url-safety-cache-ttl", 24*time.Hour, "Cache TTL for URL safety verdicts")
	fs.StringVar(&learnURLSafetyVisibility, "url-safety-visibility", "private", "URL safety scan visibility: private|unlisted|public")
	fs.BoolVar(&learnURLSafetyFailOpen, "url-safety-fail-open", false, "Allow URL ingest when safety service errors/unavailable")
	fs.StringArrayVar(&learnHFDatasets, "hf-dataset", nil, "Hugging Face dataset id to index (repeatable)")
	fs.StringVar(&learnHFConfig, "hf-config", "", "Hugging Face dataset config")
	fs.StringVar(&learnHFSplit, "hf-split", "train", "Hugging Face dataset split")
	fs.IntVar(&learnHFMaxRecords, "hf-max-records", 100, "Maximum records per HF dataset to index")
	fs.StringArrayVar(&learnKaggleDatasets, "kaggle-dataset", nil, "Kaggle dataset id to index (owner/dataset, repeatable)")
	fs.StringArrayVar(&learnKaggleFiles, "kaggle-file", nil, "Optional Kaggle file name filter (repeatable)")
	fs.IntVar(&learnKaggleMaxRecords, "kaggle-max-records", 100, "Maximum records per Kaggle dataset file to index")
	fs.StringVar(&learnFromResearch, "from-research", "", "Ingest from a research artifact id or 'latest'")
	fs.BoolVar(&learnIncludeResearchSummary, "include-research-summary", true, "Include research summary/findings text during --from-research ingest")
	fs.BoolVar(&learnIncludeResearchSources, "include-research-sources", true, "Include source URL indexing during --from-research ingest")
	fs.BoolVar(&learnSummarizeSources, "summarize-sources", boolFromEnv("TALOS_LEARN_SUMMARIZE_SOURCES", true), "Generate per-source summaries during directory/remote ingest")
	fs.IntVar(&learnSummaryMaxChars, "summary-max-chars", intFromEnv("TALOS_LEARN_SUMMARY_MAX_CHARS", 900), "Maximum chars for each source summary")
	fs.IntVar(&learnSummaryMaxPoints, "summary-max-points", intFromEnv("TALOS_LEARN_SUMMARY_MAX_POINTS", 5), "Maximum key points per structured summary")
	fs.StringVar(&learnSummaryModel, "summary-model", strings.TrimSpace(os.Getenv("TALOS_LEARN_SUMMARY_MODEL")), "Optional model override for source summarization")
	fs.BoolVar(&learnTitleChunks, "title-chunks", boolFromEnv("TALOS_LEARN_TITLE_CHUNKS", true), "Generate chunk titles during directory/remote ingest")
	fs.IntVar(&learnTitleMaxChars, "title-max-chars", intFromEnv("TALOS_LEARN_TITLE_MAX_CHARS", 96), "Maximum chars for each generated chunk title")
	fs.StringVar(&learnTitleModel, "title-model", strings.TrimSpace(os.Getenv("TALOS_LEARN_TITLE_MODEL")), "Optional model override for chunk title generation")
	fs.BoolVar(&learnIncremental, "incremental", boolFromEnv("TALOS_LEARN_INCREMENTAL", true), "Enable incremental re-indexing for --dir ingest")
	fs.StringVar(&learnIncrementalManifest, "incremental-manifest", strings.TrimSpace(os.Getenv("TALOS_LEARN_INCREMENTAL_MANIFEST")), "Optional override path for directory incremental manifest")

	// Google Workspace
	fs.StringVar(&learnGmailQuery, "gmail-query", "", "Gmail search query (e.g. \"label:inbox after:2024/01/01\")")
	fs.IntVar(&learnGmailMax, "gmail-max", 50, "Maximum Gmail messages to fetch")
	fs.StringVar(&learnGdriveFolderID, "gdrive-folder", "", "Google Drive folder ID to ingest files from")
	fs.StringVar(&learnGdriveQuery, "gdrive-query", "", "Google Drive search query (e.g. \"mimeType='application/vnd.google-apps.document'\")")
	fs.IntVar(&learnGdriveMax, "gdrive-max", 100, "Maximum Google Drive files to fetch")

	// Notion
	fs.StringVar(&learnNotionDatabaseID, "notion-database", "", "Notion database ID to query and ingest")
	fs.StringVar(&learnNotionFilter, "notion-filter", "", "Optional JSON filter for Notion database query")

	// Project Gutenberg
	fs.StringVar(&learnBookSearch, "book-search", "", "Search Project Gutenberg by title/author and ingest top result(s)")
	fs.StringArrayVar(&learnBookIDs, "book-id", nil, "Ingest a specific Gutenberg book by numeric ID (repeatable)")
	fs.IntVar(&learnBookMax, "book-max", 1, "Maximum Gutenberg books to ingest when using --book-search")

	// GitHub
	fs.StringArrayVar(&learnGitHubRepos, "github-repo", nil, "GitHub \"owner/repo\" to ingest (repeatable)")
	fs.StringVar(&learnGitHubPath, "github-path", "", "Subdirectory path within repo to limit ingestion (e.g. \"pkg/\", \"docs/\")")
	fs.IntVar(&learnGitHubMax, "github-max", 500, "Maximum files to ingest per GitHub repo")

	// Chain — ordered multi-source ingestion
	fs.StringVar(&learnChain, "chain", "", "Ordered comma-separated source tokens (e.g. \"dir,github,hf,kaggle,books\"). Valid: file,dir,url,hf,kaggle,gmail,drive,notion,books,github,research")
}

func executeLearnSyntheticText(args []string) error {
	var content string
	source := "inline"
	if strings.TrimSpace(learnFile) != "" {
		data, err := os.ReadFile(learnFile)
		if err != nil {
			return err
		}
		content = string(data)
		source = strings.TrimSpace(learnFile)
	} else if len(args) > 0 {
		content = strings.Join(args, " ")
	} else {
		return fmt.Errorf("provide inline text or --file when using --synthetic-text-out")
	}
	lines := buildSyntheticTextJSONLLines(content, source, learnChunkChars)
	if len(lines) == 0 {
		return fmt.Errorf("no synthetic artifacts produced from input")
	}
	outPath := strings.TrimSpace(learnSyntheticTextOut)
	if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(outPath, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		return err
	}
	session := newLearnSession("SYNTHETIC_TEXT", outPath, []string{source}, map[string]string{
		"synthetic_text_out": outPath,
	})
	annotateLearnSessionWithProfile(&session, learnProfileApplied)
	session.finish("SUCCESS", "Synthetic text artifacts generated.", "", map[string]int64{
		"items_indexed":    0,
		"synthetic_chunks": int64(len(lines)),
	})
	if logErr := appendLearnSessionRecord(session); logErr != nil {
		fmt.Printf("Warning: Failed to write learn session log: %v\n", logErr)
	}
	fmt.Printf("Synthetic text artifacts written: %s (%d lines)\n", outPath, len(lines))
	return nil
}

func buildSyntheticTextJSONLLines(content, source string, chunkChars int) []string {
	content = strings.TrimSpace(content)
	if content == "" {
		return nil
	}
	if chunkChars <= 0 {
		chunkChars = 1200
	}
	normalized := strings.Join(strings.Fields(content), " ")
	if normalized == "" {
		return nil
	}
	chunks := make([]string, 0, 4)
	for len(normalized) > 0 {
		if len(normalized) <= chunkChars {
			chunks = append(chunks, strings.TrimSpace(normalized))
			break
		}
		cut := strings.LastIndex(normalized[:chunkChars], " ")
		if cut <= 0 {
			cut = chunkChars
		}
		chunks = append(chunks, strings.TrimSpace(normalized[:cut]))
		normalized = strings.TrimSpace(normalized[cut:])
	}
	lines := make([]string, 0, len(chunks))
	for i, chunk := range chunks {
		record := map[string]any{
			"type":         "synthetic_text",
			"source":       source,
			"chunk_index":  i + 1,
			"chunk_total":  len(chunks),
			"prompt":       "Ground the answer in this chunk.",
			"target_text":  chunk,
			"token_est":    len(strings.Fields(chunk)),
			"created_mode": "deterministic",
		}
		b, err := json.Marshal(record)
		if err != nil {
			continue
		}
		lines = append(lines, string(b))
	}
	return lines
}

func resolveLearnSelfTrainSpeechOutPath() string {
	if env := strings.TrimSpace(os.Getenv("TALOS_LEARN_SELF_TRAIN_SPEECH_OUT")); env != "" {
		return env
	}
	return filepath.Join(".memory", "synthetic", "speech_self_train.jsonl")
}

func appendSyntheticTextArtifacts(content, source, outPath string, chunkChars int) (int, error) {
	lines := buildSyntheticTextJSONLLines(content, source, chunkChars)
	if len(lines) == 0 {
		return 0, fmt.Errorf("no synthetic artifacts produced from input")
	}
	if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
		return 0, err
	}
	f, err := os.OpenFile(outPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	for _, line := range lines {
		if _, err := f.WriteString(line + "\n"); err != nil {
			return 0, err
		}
	}
	return len(lines), nil
}

func summaryConfigFromLearnFlags() memory.SourceSummaryConfig {
	maxChars := learnSummaryMaxChars
	if maxChars <= 0 {
		maxChars = 900
	}
	maxPoints := learnSummaryMaxPoints
	if maxPoints <= 0 {
		maxPoints = 5
	}
	return memory.SourceSummaryConfig{
		MaxChars:  maxChars,
		MaxPoints: maxPoints,
		Model:     strings.TrimSpace(learnSummaryModel),
	}
}

func annotateLearnSessionWithProfile(session *LearnSessionRecord, profileName string) {
	if session == nil {
		return
	}
	if session.ConfigSnapshot == nil {
		session.ConfigSnapshot = map[string]string{}
	}
	if strings.TrimSpace(profileName) != "" {
		session.ConfigSnapshot["profile_name"] = strings.TrimSpace(profileName)
	}
	if strings.TrimSpace(learnNamespace) != "" {
		session.ConfigSnapshot["namespace"] = strings.TrimSpace(learnNamespace)
	}
}

func resolveRuntimeNamespace(sm *state.Manager, explicit string) string {
	if v := strings.ToLower(strings.TrimSpace(requestedNamespace)); v != "" {
		return v
	}
	if v := strings.ToLower(strings.TrimSpace(explicit)); v != "" {
		return v
	}
	if sm != nil {
		if v := strings.ToLower(strings.TrimSpace(sm.ActiveNamespace())); v != "" {
			return v
		}
	}
	return ""
}

func executeLearnFromResearch(selector string, includeSummary bool, includeSources bool) error {
	if !includeSummary && !includeSources {
		return fmt.Errorf("at least one of --include-research-summary or --include-research-sources must be true")
	}
	artifactID, err := resolveResearchArtifactID(selector)
	if err != nil {
		return err
	}
	artifact, err := loadResearchArtifactByID(artifactID)
	if err != nil {
		return err
	}
	mm, err := memory.NewMemoryManager()
	if err != nil {
		return err
	}
	if ns := strings.TrimSpace(learnNamespace); ns != "" {
		mm.SetActiveNamespace(ns)
	}

	session := newLearnSession("RESEARCH_CHAIN", artifactID, artifact.Sources, map[string]string{
		"artifact_id":      artifactID,
		"research_mode":    strings.ToLower(strings.TrimSpace(artifact.Mode)),
		"include_summary":  fmt.Sprintf("%t", includeSummary),
		"include_sources":  fmt.Sprintf("%t", includeSources),
		"research_status":  strings.ToUpper(strings.TrimSpace(artifact.Status)),
		"research_created": strings.TrimSpace(artifact.CreatedAt),
		"summarization":    fmt.Sprintf("%t", learnSummarizeSources),
		"chunk_titles":     fmt.Sprintf("%t", learnTitleChunks),
	})

	var sourceStats rag.RemoteIndexStats
	summaryIndexed := int64(0)
	var failureReasons []string
	var summaryWorker *memory.SourceSummaryWorker
	if learnSummarizeSources {
		sw, swErr := memory.NewSourceSummaryWorker(mm, session.SessionID, summaryConfigFromLearnFlags(), 2, 128)
		if swErr != nil {
			failureReasons = append(failureReasons, "source summarization unavailable: "+swErr.Error())
		} else {
			summaryWorker = sw
		}
	}
	if includeSummary {
		payload := buildResearchLearnPayload(artifact)
		if strings.TrimSpace(payload) != "" {
			if err := mm.AddKnowledge(payload, map[string]string{
				"source_type":   "research_artifact",
				"research_id":   artifact.SessionID,
				"research_mode": strings.ToLower(strings.TrimSpace(artifact.Mode)),
			}); err != nil {
				failureReasons = append(failureReasons, "summary ingest failed: "+err.Error())
			} else {
				summaryIndexed = 1
			}
		}
	}
	if includeSources && len(artifact.Sources) > 0 {
		opts := rag.DefaultRemoteIndexOptions()
		opts.MaxChunkChars = learnChunkChars
		opts.ChunkOverlap = learnChunkOverlap
		opts.GenerateChunkTitles = learnTitleChunks
		opts.ChunkTitleMaxChars = learnTitleMaxChars
		opts.ChunkTitleModel = strings.TrimSpace(learnTitleModel)
		opts.Crawl = false
		opts.CrawlDepth = 0
		opts.MaxPages = learnMaxInt(1, learnMaxPages)
		opts.RateLimitPerSec = learnRateLimit
		opts.UserAgent = learnUserAgent
		opts.Timeout = learnRemoteTimeout
		opts.MaxBytes = learnMaxBytes
		opts.URLSafetyEnabled = learnURLSafety
		opts.URLSafetyTimeout = learnURLSafetyTimeout
		opts.URLSafetyCacheTTL = learnURLSafetyCacheTTL
		opts.URLSafetyVisibility = learnURLSafetyVisibility
		opts.URLSafetyFailOpen = learnURLSafetyFailOpen
		opts.URLSafetyAPIKey = strings.TrimSpace(os.Getenv("URLSCAN_API_KEY"))
		if summaryWorker != nil {
			opts.OnSourceIndexed = func(ev rag.SourceIndexedEvent) {
				summaryWorker.Enqueue(memory.SourceSummaryRequest{
					SourceType: ev.SourceType,
					SourceRef:  ev.SourceRef,
					Content:    ev.Content,
					SessionID:  session.SessionID,
					ChunkCount: ev.ChunkCount,
					Metadata:   ev.Metadata,
				})
			}
		}

		fmt.Printf("Indexing research sources from artifact %s: %d URL(s)\n", artifactID, len(artifact.Sources))
		if opts.URLSafetyEnabled && strings.TrimSpace(opts.URLSafetyAPIKey) == "" && !opts.URLSafetyFailOpen {
			failureReasons = append(failureReasons, "URL safety enabled but URLSCAN_API_KEY is not set")
		} else {
			stats, idxErr := rag.IndexURLs(mm, artifact.Sources, opts)
			sourceStats = mergeRemoteStats(sourceStats, stats)
			if idxErr != nil {
				failureReasons = append(failureReasons, idxErr.Error())
			}
		}
	}

	status := "SUCCESS"
	if summaryIndexed == 0 && sourceStats.ItemsIndexed == 0 {
		status = "FAILED"
	}
	if len(failureReasons) > 0 && status == "SUCCESS" {
		status = "PARTIAL"
	}
	summaryMetrics := memory.SourceSummaryMetrics{}
	if summaryWorker != nil {
		summaryMetrics = summaryWorker.CloseAndFlush(20 * time.Second)
		totalIndexed := sourceStats.ItemsIndexed + int(summaryIndexed)
		_ = summaryWorker.AddSessionAggregateSummary(buildLearnSessionSummaryText(session.Mode, session.QueryOrTarget, totalIndexed, int(summaryMetrics.SummaryDocsIndexed)), int(summaryMetrics.JobsCompleted))
		summaryMetrics = summaryWorker.Metrics()
	}
	metrics := map[string]int64{
		"artifact_findings":       int64(len(artifact.Findings)),
		"artifact_sources":        int64(len(artifact.Sources)),
		"summary_items_indexed":   summaryIndexed,
		"source_items_fetched":    int64(sourceStats.ItemsFetched),
		"source_items_indexed":    int64(sourceStats.ItemsIndexed),
		"source_chunks_indexed":   int64(sourceStats.ChunksIndexed),
		"source_preflight_errors": int64(sourceStats.PreflightFailures),
		"source_safety_blocked":   int64(sourceStats.SafetyBlocked),
		"source_safety_errors":    int64(sourceStats.SafetyErrors),
		"source_parse_errors":     int64(sourceStats.ParseErrors),
		"source_http_errors":      int64(sourceStats.HTTPErrors),
		"source_index_errors":     int64(sourceStats.IndexErrors),
	}
	mergeSummaryMetrics(metrics, summaryMetrics)
	mergeTopologyMetrics(metrics, mm.TopologyStats())
	session.finish(status, "Research artifact learning run completed.", strings.Join(failureReasons, "; "), metrics)
	if logErr := appendLearnSessionRecord(session); logErr != nil {
		fmt.Printf("Warning: Failed to write learn session log: %v\n", logErr)
	}
	if learnVerbose {
		fmt.Println(renderResearchLearnSummary(artifactID, includeSummary, includeSources, summaryIndexed, sourceStats, summaryMetrics, mm.TopologyStats(), status))
	} else {
		fmt.Println(renderResearchLearnSummaryFriendly(artifactID, status, summaryIndexed, sourceStats))
	}
	if status == "FAILED" {
		return fmt.Errorf("no research artifact content was indexed")
	}
	return nil
}

func buildResearchLearnPayload(artifact ResearchArtifact) string {
	var b strings.Builder
	b.WriteString("Research Artifact\n")
	b.WriteString("Session ID: " + strings.TrimSpace(artifact.SessionID) + "\n")
	b.WriteString("Mode: " + strings.ToUpper(strings.TrimSpace(artifact.Mode)) + "\n")
	b.WriteString("Query: " + strings.TrimSpace(artifact.Query) + "\n\n")
	b.WriteString("Executive Summary\n")
	b.WriteString(strings.TrimSpace(artifact.ExecutiveSummary) + "\n\n")
	if len(artifact.Findings) > 0 {
		b.WriteString("Findings\n")
		for i, f := range artifact.Findings {
			line := fmt.Sprintf("%d. %s", i+1, strings.TrimSpace(f.Text))
			if len(f.Refs) > 0 {
				line += " [refs:"
				refParts := make([]string, 0, len(f.Refs))
				for _, ref := range f.Refs {
					refParts = append(refParts, fmt.Sprintf("%d", ref))
				}
				line += strings.Join(refParts, ",") + "]"
			}
			b.WriteString(line + "\n")
		}
	}
	return strings.TrimSpace(b.String())
}

func renderResearchLearnSummary(artifactID string, includeSummary bool, includeSources bool, summaryIndexed int64, sourceStats rag.RemoteIndexStats, summaryMetrics memory.SourceSummaryMetrics, topology memory.TopologyStats, status string) string {
	var b strings.Builder
	b.WriteString("LEARN SUMMARY\n\n")
	b.WriteString("MODE\n")
	b.WriteString("  RESEARCH_CHAIN\n\n")
	b.WriteString("SOURCE\n")
	b.WriteString("  artifact_id: " + strings.TrimSpace(artifactID) + "\n")
	b.WriteString(fmt.Sprintf("  include_summary: %t\n", includeSummary))
	b.WriteString(fmt.Sprintf("  include_sources: %t\n\n", includeSources))
	b.WriteString("RESULTS\n")
	b.WriteString("  status: " + strings.ToUpper(strings.TrimSpace(status)) + "\n")
	b.WriteString(fmt.Sprintf("  summary_items_indexed: %d\n", summaryIndexed))
	b.WriteString(fmt.Sprintf("  source_items_fetched: %d\n", sourceStats.ItemsFetched))
	b.WriteString(fmt.Sprintf("  source_items_indexed: %d\n", sourceStats.ItemsIndexed))
	b.WriteString(fmt.Sprintf("  source_chunks_indexed: %d\n", sourceStats.ChunksIndexed))
	appendSummaryMetricsSection(&b, summaryMetrics)
	appendTopologyMetricsSection(&b, topology)
	return strings.TrimRight(b.String(), "\n")
}

func renderResearchLearnSummaryFriendly(artifactID string, status string, summaryIndexed int64, sourceStats rag.RemoteIndexStats) string {
	var b strings.Builder
	b.WriteString("LEARN REPORT\n\n")
	b.WriteString("COMMAND\n")
	b.WriteString("  talos learn --from-research\n\n")
	b.WriteString("STATUS\n")
	b.WriteString("  " + strings.ToUpper(strings.TrimSpace(status)) + "\n\n")
	b.WriteString("SOURCE\n")
	b.WriteString("  artifact: " + strings.TrimSpace(artifactID) + "\n\n")
	b.WriteString("PROGRESS\n")
	b.WriteString(fmt.Sprintf("  fetched: %d | indexed: %d\n\n", sourceStats.ItemsFetched, sourceStats.ItemsIndexed))
	b.WriteString("RESULTS\n")
	b.WriteString(fmt.Sprintf("  summary_items_indexed: %d\n", summaryIndexed))
	b.WriteString(fmt.Sprintf("  chunks_indexed: %d\n", sourceStats.ChunksIndexed))
	errors := sourceStats.PreflightFailures + sourceStats.SafetyBlocked + sourceStats.SafetyErrors + sourceStats.ParseErrors + sourceStats.HTTPErrors + sourceStats.IndexErrors
	b.WriteString(fmt.Sprintf("  errors: %d", errors))
	return b.String()
}

func learnMaxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func collectURLs(seed []string, filePath string) ([]string, error) {
	var out []string
	seen := make(map[string]bool)
	add := func(u string) {
		u = strings.TrimSpace(u)
		if u == "" || seen[u] {
			return
		}
		seen[u] = true
		out = append(out, u)
	}
	for _, u := range seed {
		add(u)
	}
	if strings.TrimSpace(filePath) != "" {
		f, err := os.Open(filePath)
		if err != nil {
			return nil, err
		}
		defer f.Close()
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			add(sc.Text())
		}
		if err := sc.Err(); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func remoteURLAllowedExtensions() map[string]bool {
	extensionInput := strings.TrimSpace(learnExtensions)
	if len(learnTypes) > 0 {
		typeCSV := strings.Join(learnTypes, ",")
		if extensionInput == "" {
			extensionInput = typeCSV
		} else {
			extensionInput += "," + typeCSV
		}
	}
	return rag.ParseExtensionsCSV(extensionInput)
}

func mergeRemoteStats(a, b rag.RemoteIndexStats) rag.RemoteIndexStats {
	return rag.RemoteIndexStats{
		ItemsFetched:       a.ItemsFetched + b.ItemsFetched,
		ItemsIndexed:       a.ItemsIndexed + b.ItemsIndexed,
		ChunksIndexed:      a.ChunksIndexed + b.ChunksIndexed,
		BytesFetched:       a.BytesFetched + b.BytesFetched,
		PreflightFailures:  a.PreflightFailures + b.PreflightFailures,
		SafetyBlocked:      a.SafetyBlocked + b.SafetyBlocked,
		SafetyErrors:       a.SafetyErrors + b.SafetyErrors,
		SafetyCacheHits:    a.SafetyCacheHits + b.SafetyCacheHits,
		SafetyCacheMisses:  a.SafetyCacheMisses + b.SafetyCacheMisses,
		SkippedDuplicate:   a.SkippedDuplicate + b.SkippedDuplicate,
		SkippedDomain:      a.SkippedDomain + b.SkippedDomain,
		SkippedUnsupported: a.SkippedUnsupported + b.SkippedUnsupported,
		ParseErrors:        a.ParseErrors + b.ParseErrors,
		HTTPErrors:         a.HTTPErrors + b.HTTPErrors,
		IndexErrors:        a.IndexErrors + b.IndexErrors,
	}
}

func renderDirectoryLearnSummary(dir string, opts rag.IndexOptions, stats rag.IndexStats, summaryMetrics memory.SourceSummaryMetrics, topology memory.TopologyStats) string {
	var b strings.Builder
	b.WriteString("LEARN SUMMARY\n\n")
	b.WriteString("MODE\n")
	b.WriteString("  DIRECTORY\n\n")
	b.WriteString("TARGET\n")
	b.WriteString("  " + strings.TrimSpace(dir) + "\n\n")
	b.WriteString("CONFIG\n")
	b.WriteString(fmt.Sprintf("  recursive: %t\n", opts.Recursive))
	b.WriteString(fmt.Sprintf("  chunk_chars: %d\n", opts.MaxChunkChars))
	b.WriteString(fmt.Sprintf("  chunk_overlap: %d\n", opts.ChunkOverlap))
	b.WriteString(fmt.Sprintf("  incremental: %t\n", opts.Incremental))
	if strings.TrimSpace(opts.IncrementalManifestPath) != "" {
		b.WriteString("  incremental_manifest: " + strings.TrimSpace(opts.IncrementalManifestPath) + "\n")
	}
	if opts.AllTypes {
		b.WriteString("  type_filter: all\n")
	} else {
		b.WriteString("  type_filter: " + normalizeExtensionsList(opts.Extensions) + "\n")
	}
	b.WriteString("\nRESULTS\n")
	b.WriteString(fmt.Sprintf("  files_scanned: %d\n", stats.FilesScanned))
	b.WriteString(fmt.Sprintf("  files_indexed: %d\n", stats.FilesIndexed))
	b.WriteString(fmt.Sprintf("  files_unchanged: %d\n", stats.FilesUnchanged))
	b.WriteString(fmt.Sprintf("  files_changed: %d\n", stats.FilesChanged))
	b.WriteString(fmt.Sprintf("  files_added: %d\n", stats.FilesAdded))
	b.WriteString(fmt.Sprintf("  files_removed: %d\n", stats.FilesRemoved))
	b.WriteString(fmt.Sprintf("  chunks_archived: %d\n", stats.ChunksArchived))
	b.WriteString(fmt.Sprintf("  chunks_indexed: %d\n", stats.ChunksIndexed))
	b.WriteString(fmt.Sprintf("  skipped_unsupported: %d\n", stats.SkippedUnsupported))
	b.WriteString(fmt.Sprintf("  skipped_binary: %d\n", stats.SkippedBinary))
	b.WriteString(fmt.Sprintf("  parse_errors: %d\n", stats.ParseErrors))
	b.WriteString(fmt.Sprintf("  index_errors: %d\n", stats.IndexErrors))
	b.WriteString(fmt.Sprintf("  walk_errors: %d\n", stats.WalkErrors))
	appendSummaryMetricsSection(&b, summaryMetrics)
	appendTopologyMetricsSection(&b, topology)
	return b.String()
}

func renderDirectoryLearnSummaryFriendly(dir string, stats rag.IndexStats) string {
	var b strings.Builder
	b.WriteString("LEARN REPORT\n\n")
	b.WriteString("COMMAND\n")
	b.WriteString("  talos learn --dir\n\n")
	b.WriteString("STATUS\n")
	b.WriteString("  SUCCESS\n\n")
	b.WriteString("SOURCE\n")
	b.WriteString("  " + strings.TrimSpace(dir) + "\n\n")
	b.WriteString("PROGRESS\n")
	b.WriteString(fmt.Sprintf("  scanned_files: %d | indexed_files: %d\n\n", stats.FilesScanned, stats.FilesIndexed))
	b.WriteString("RESULTS\n")
	b.WriteString(fmt.Sprintf("  chunks_indexed: %d\n", stats.ChunksIndexed))
	errors := stats.ParseErrors + stats.IndexErrors + stats.WalkErrors
	b.WriteString(fmt.Sprintf("  errors: %d", errors))
	return b.String()
}

func renderRemoteLearnSummary(stats rag.RemoteIndexStats, summaryMetrics memory.SourceSummaryMetrics, topology memory.TopologyStats) string {
	var b strings.Builder
	b.WriteString("LEARN SUMMARY\n\n")
	b.WriteString("MODE\n")
	b.WriteString("  REMOTE\n\n")
	b.WriteString("RESULTS\n")
	b.WriteString(fmt.Sprintf("  items_fetched: %d\n", stats.ItemsFetched))
	b.WriteString(fmt.Sprintf("  items_indexed: %d\n", stats.ItemsIndexed))
	b.WriteString(fmt.Sprintf("  chunks_indexed: %d\n", stats.ChunksIndexed))
	b.WriteString(fmt.Sprintf("  bytes_fetched: %d\n", stats.BytesFetched))
	b.WriteString(fmt.Sprintf("  preflight_failures: %d\n", stats.PreflightFailures))
	b.WriteString(fmt.Sprintf("  safety_blocked: %d\n", stats.SafetyBlocked))
	b.WriteString(fmt.Sprintf("  safety_errors: %d\n", stats.SafetyErrors))
	b.WriteString(fmt.Sprintf("  safety_cache_hits: %d\n", stats.SafetyCacheHits))
	b.WriteString(fmt.Sprintf("  safety_cache_misses: %d\n", stats.SafetyCacheMisses))
	b.WriteString(fmt.Sprintf("  skipped_duplicate: %d\n", stats.SkippedDuplicate))
	b.WriteString(fmt.Sprintf("  skipped_domain: %d\n", stats.SkippedDomain))
	b.WriteString(fmt.Sprintf("  skipped_unsupported: %d\n", stats.SkippedUnsupported))
	b.WriteString(fmt.Sprintf("  parse_errors: %d\n", stats.ParseErrors))
	b.WriteString(fmt.Sprintf("  http_errors: %d\n", stats.HTTPErrors))
	b.WriteString(fmt.Sprintf("  index_errors: %d\n", stats.IndexErrors))
	appendSummaryMetricsSection(&b, summaryMetrics)
	appendTopologyMetricsSection(&b, topology)
	return b.String()
}

func renderRemoteLearnSummaryFriendly(sources []string, stats rag.RemoteIndexStats) string {
	var b strings.Builder
	b.WriteString("LEARN REPORT\n\n")
	b.WriteString("COMMAND\n")
	b.WriteString("  talos learn --url/--hf-dataset/--kaggle-dataset\n\n")
	b.WriteString("STATUS\n")
	b.WriteString("  SUCCESS\n\n")
	b.WriteString("SOURCE\n")
	if len(sources) > 0 {
		maxSources := 3
		display := sources
		if len(sources) > maxSources {
			display = sources[:maxSources]
		}
		b.WriteString("  " + strings.Join(display, ", "))
		if len(sources) > maxSources {
			b.WriteString(fmt.Sprintf(" (+%d more)", len(sources)-maxSources))
		}
		b.WriteString("\n\n")
	} else {
		b.WriteString("  n/a\n\n")
	}
	b.WriteString("PROGRESS\n")
	b.WriteString(fmt.Sprintf("  fetched: %d | indexed: %d\n\n", stats.ItemsFetched, stats.ItemsIndexed))
	b.WriteString("RESULTS\n")
	b.WriteString(fmt.Sprintf("  chunks_indexed: %d\n", stats.ChunksIndexed))
	errors := stats.PreflightFailures + stats.SafetyBlocked + stats.SafetyErrors + stats.ParseErrors + stats.HTTPErrors + stats.IndexErrors
	b.WriteString(fmt.Sprintf("  errors: %d", errors))
	return b.String()
}

func formatRemoteLearnEventLine(ev rag.RemoteEvent, verbose bool) (string, bool) {
	if verbose {
		switch ev.Outcome {
		case "indexed", "hf-indexed-record", "kaggle-indexed-record":
			return fmt.Sprintf("[%d fetched | %d indexed] %s: %s", ev.ItemsFetched, ev.ItemsIndexed, ev.Outcome, ev.Source), true
		case "safety-allowed", "safety-cache-hit", "safety-cache-miss":
			return fmt.Sprintf("[%d fetched | %d indexed] %s: %s (%s)", ev.ItemsFetched, ev.ItemsIndexed, ev.Outcome, ev.Source, ev.Detail), true
		case "safety-blocked", "safety-error", "preflight-failed", "skipped-domain", "invalid-url", "parse-error", "http-error", "http-status", "hf-http-error", "hf-http-status", "hf-parse-error", "index-error", "hf-index-error", "kaggle-http-error", "kaggle-http-status", "kaggle-parse-error", "kaggle-index-error", "kaggle-invalid-dataset", "kaggle-no-files", "skipped-extension", "skipped-unsupported":
			return fmt.Sprintf("[%d fetched | %d indexed] %s: %s (%s)", ev.ItemsFetched, ev.ItemsIndexed, ev.Outcome, ev.Source, ev.Detail), true
		}
		return "", false
	}
	switch ev.Outcome {
	case "indexed":
		return fmt.Sprintf("Indexed page: %s (%d fetched | %d indexed)", ev.Source, ev.ItemsFetched, ev.ItemsIndexed), true
	case "safety-blocked", "safety-error", "preflight-failed", "parse-error", "http-error", "http-status", "index-error":
		if strings.TrimSpace(ev.Detail) != "" {
			return fmt.Sprintf("Skipped page: %s (%s: %s)", ev.Source, ev.Outcome, ev.Detail), true
		}
		return fmt.Sprintf("Skipped page: %s (%s)", ev.Source, ev.Outcome), true
	default:
		return "", false
	}
}

func appendSummaryMetricsSection(b *strings.Builder, m memory.SourceSummaryMetrics) {
	if b == nil {
		return
	}
	b.WriteString("\nSUMMARY\n")
	b.WriteString(fmt.Sprintf("  jobs_enqueued: %d\n", m.JobsEnqueued))
	b.WriteString(fmt.Sprintf("  jobs_completed: %d\n", m.JobsCompleted))
	b.WriteString(fmt.Sprintf("  jobs_failed: %d\n", m.JobsFailed))
	b.WriteString(fmt.Sprintf("  jobs_dropped: %d\n", m.JobsDropped))
	b.WriteString(fmt.Sprintf("  summary_docs_indexed: %d\n", m.SummaryDocsIndexed))
	b.WriteString(fmt.Sprintf("  session_summary_indexed: %d\n", m.SessionSummaryIndexed))
}

func appendTopologyMetricsSection(b *strings.Builder, t memory.TopologyStats) {
	if b == nil {
		return
	}
	b.WriteString("\nTOPOLOGY\n")
	b.WriteString(fmt.Sprintf("  enabled: %t\n", t.Enabled))
	b.WriteString(fmt.Sprintf("  nodes: %d\n", t.Nodes))
	b.WriteString(fmt.Sprintf("  edges: %d\n", t.Edges))
	b.WriteString(fmt.Sprintf("  edges_added: %d\n", t.EdgesAdded))
	b.WriteString(fmt.Sprintf("  links_used: %d\n", t.LinksUsed))
	if strings.TrimSpace(t.LastError) != "" {
		b.WriteString("  last_error: " + strings.TrimSpace(t.LastError) + "\n")
	}
}

func mergeSummaryMetrics(metrics map[string]int64, m memory.SourceSummaryMetrics) {
	if metrics == nil {
		return
	}
	metrics["summary_jobs_enqueued"] = m.JobsEnqueued
	metrics["summary_jobs_completed"] = m.JobsCompleted
	metrics["summary_jobs_failed"] = m.JobsFailed
	metrics["summary_jobs_dropped"] = m.JobsDropped
	metrics["summary_docs_indexed"] = m.SummaryDocsIndexed
	metrics["session_summary_indexed"] = m.SessionSummaryIndexed
}

func mergeTopologyMetrics(metrics map[string]int64, t memory.TopologyStats) {
	if metrics == nil {
		return
	}
	if t.Enabled {
		metrics["topology_enabled"] = 1
	} else {
		metrics["topology_enabled"] = 0
	}
	metrics["topology_nodes"] = int64(t.Nodes)
	metrics["topology_edges"] = int64(t.Edges)
	metrics["topology_edges_added"] = t.EdgesAdded
	metrics["topology_links_used"] = t.LinksUsed
}

func buildLearnSessionSummaryText(mode string, target string, itemsIndexed int, summaryDocs int) string {
	mode = strings.TrimSpace(strings.ToUpper(mode))
	target = strings.TrimSpace(target)
	if mode == "" {
		mode = "LEARN"
	}
	if target == "" {
		target = "n/a"
	}
	return fmt.Sprintf(
		"Gist: %s ingest completed for %s.\nKey Points:\n- Indexed items: %d\n- Source summaries indexed: %d\n- Retrieval memory now has linked source briefs for faster recall.\nRisks/Uncertainty: none\nSource Fingerprint: ingest_session | %s",
		mode,
		target,
		itemsIndexed,
		summaryDocs,
		target,
	)
}

func renderInlineLearnSummary(fromFile bool, filePath string) string {
	var b strings.Builder
	b.WriteString("LEARN SUMMARY\n\n")
	b.WriteString("MODE\n")
	if fromFile {
		b.WriteString("  FILE\n\n")
		b.WriteString("TARGET\n")
		b.WriteString("  " + strings.TrimSpace(filePath) + "\n\n")
	} else {
		b.WriteString("  INLINE_TEXT\n\n")
	}
	b.WriteString("RESULTS\n")
	b.WriteString("  status: indexed")
	return b.String()
}

func renderInlineLearnSummaryFriendly(fromFile bool, filePath string) string {
	var b strings.Builder
	b.WriteString("LEARN REPORT\n\n")
	b.WriteString("COMMAND\n")
	b.WriteString("  talos learn\n\n")
	b.WriteString("STATUS\n")
	b.WriteString("  SUCCESS\n\n")
	b.WriteString("SOURCE\n")
	if fromFile {
		b.WriteString("  file: " + strings.TrimSpace(filePath) + "\n\n")
	} else {
		b.WriteString("  inline input\n\n")
	}
	b.WriteString("PROGRESS\n")
	b.WriteString("  indexed: 1/1\n\n")
	b.WriteString("RESULTS\n")
	b.WriteString("  chunks_indexed: 1\n")
	b.WriteString("  errors: 0")
	return b.String()
}

func normalizeExtensionsList(exts map[string]bool) string {
	if len(exts) == 0 {
		return "default"
	}
	ordered := make([]string, 0, len(exts))
	for ext, enabled := range exts {
		if enabled {
			ordered = append(ordered, ext)
		}
	}
	sort.Strings(ordered)
	if len(ordered) == 0 {
		return "default"
	}
	return strings.Join(ordered, ",")
}

func executeLearnFromConnectors(mm *memory.MemoryManager) {
	ctx := context.Background()
	opts := rag.DefaultIndexOptions()
	opts.MaxChunkChars = learnChunkChars
	opts.ChunkOverlap = learnChunkOverlap
	progress := newLearnProgress()

	totalIndexed := 0
	totalChunks := 0
	var errors []string
	totalPhases := 0
	if learnGmailQuery != "" {
		totalPhases++
	}
	if learnGdriveFolderID != "" || learnGdriveQuery != "" {
		totalPhases++
	}
	if learnNotionDatabaseID != "" {
		totalPhases++
	}
	if learnBookSearch != "" || len(learnBookIDs) > 0 {
		totalPhases++
	}
	for _, repoSlug := range learnGitHubRepos {
		if strings.TrimSpace(repoSlug) != "" {
			totalPhases++
		}
	}
	phaseIndex := 0

	// Gmail
	if learnGmailQuery != "" {
		phaseIndex++
		progress.setPhase("gmail", phaseIndex, totalPhases)
		auth, err := googleconn.NewGoogleAuth()
		if err != nil {
			progress.addError()
			fmt.Printf("Gmail auth error: %v\n", err)
			fmt.Println("Ensure GOOGLE_SERVICE_ACCOUNT_JSON and GOOGLE_IMPERSONATE_USER are set.")
			errors = append(errors, "gmail: "+err.Error())
		} else {
			connector := googleconn.NewGmailConnector(auth)
			fetchOpts := connectors.FetchOptions{
				Query:      learnGmailQuery,
				MaxResults: learnGmailMax,
			}
			progress.verbosef("Fetching Gmail messages (query: %q, max: %d)...\n", learnGmailQuery, learnGmailMax)
			opts.OnSourceIndexed = func(ev rag.SourceIndexedEvent) {
				progress.onSourceIndexed(ev.ChunkCount)
				progress.verbosef("  [gmail] indexed: %s (%d chunks)\n", ev.SourceRef, ev.ChunkCount)
			}
			stats, err := rag.IndexConnector(ctx, connector, mm, opts, fetchOpts)
			if err != nil {
				progress.addError()
				fmt.Printf("Gmail indexing error: %v\n", err)
				errors = append(errors, "gmail: "+err.Error())
			} else {
				fmt.Printf("Gmail: %d messages indexed, %d chunks.\n", stats.FilesIndexed, stats.ChunksIndexed)
				totalIndexed += stats.FilesIndexed
				totalChunks += stats.ChunksIndexed
			}
		}
	}

	// Google Drive
	if learnGdriveFolderID != "" || learnGdriveQuery != "" {
		phaseIndex++
		progress.setPhase("gdrive", phaseIndex, totalPhases)
		auth, err := googleconn.NewGoogleAuth()
		if err != nil {
			progress.addError()
			fmt.Printf("Google Drive auth error: %v\n", err)
			fmt.Println("Ensure GOOGLE_SERVICE_ACCOUNT_JSON and GOOGLE_IMPERSONATE_USER are set.")
			errors = append(errors, "gdrive: "+err.Error())
		} else {
			connector := googleconn.NewDriveConnector(auth)
			fetchOpts := connectors.FetchOptions{
				FolderID:   learnGdriveFolderID,
				Query:      learnGdriveQuery,
				MaxResults: learnGdriveMax,
			}
			label := learnGdriveFolderID
			if label == "" {
				label = learnGdriveQuery
			}
			progress.verbosef("Fetching Google Drive files (%s, max: %d)...\n", label, learnGdriveMax)
			opts.OnSourceIndexed = func(ev rag.SourceIndexedEvent) {
				progress.onSourceIndexed(ev.ChunkCount)
				progress.verbosef("  [gdrive] indexed: %s (%d chunks)\n", ev.SourceRef, ev.ChunkCount)
			}
			stats, err := rag.IndexConnector(ctx, connector, mm, opts, fetchOpts)
			if err != nil {
				progress.addError()
				fmt.Printf("Google Drive indexing error: %v\n", err)
				errors = append(errors, "gdrive: "+err.Error())
			} else {
				fmt.Printf("Google Drive: %d files indexed, %d chunks.\n", stats.FilesIndexed, stats.ChunksIndexed)
				totalIndexed += stats.FilesIndexed
				totalChunks += stats.ChunksIndexed
			}
		}
	}

	// Notion
	if learnNotionDatabaseID != "" {
		phaseIndex++
		progress.setPhase("notion", phaseIndex, totalPhases)
		connector, err := notion.NewNotionConnector()
		if err != nil {
			progress.addError()
			fmt.Printf("Notion connector error: %v\n", err)
			fmt.Println("Ensure NOTION_API_KEY is set.")
			errors = append(errors, "notion: "+err.Error())
		} else {
			var filter map[string]any
			if learnNotionFilter != "" {
				if jsonErr := json.Unmarshal([]byte(learnNotionFilter), &filter); jsonErr != nil {
					fmt.Printf("Warning: invalid --notion-filter JSON: %v\n", jsonErr)
				}
			}
			fetchOpts := connectors.FetchOptions{
				Query:      learnNotionDatabaseID,
				MaxResults: 100,
				Filter:     filter,
			}
			progress.verbosef("Fetching Notion database (id: %s)...\n", learnNotionDatabaseID)
			opts.OnSourceIndexed = func(ev rag.SourceIndexedEvent) {
				progress.onSourceIndexed(ev.ChunkCount)
				progress.verbosef("  [notion] indexed: %s (%d chunks)\n", ev.SourceRef, ev.ChunkCount)
			}
			stats, err := rag.IndexConnector(ctx, connector, mm, opts, fetchOpts)
			if err != nil {
				progress.addError()
				fmt.Printf("Notion indexing error: %v\n", err)
				errors = append(errors, "notion: "+err.Error())
			} else {
				fmt.Printf("Notion: %d pages indexed, %d chunks.\n", stats.FilesIndexed, stats.ChunksIndexed)
				totalIndexed += stats.FilesIndexed
				totalChunks += stats.ChunksIndexed
			}
		}
	}

	// Project Gutenberg
	if learnBookSearch != "" || len(learnBookIDs) > 0 {
		phaseIndex++
		progress.setPhase("books", phaseIndex, totalPhases)
		connector := gutenberg.NewGutenbergConnector()
		fetchOpts := connectors.FetchOptions{
			Query:      learnBookSearch,
			MaxResults: learnBookMax,
		}
		if len(learnBookIDs) > 0 {
			fetchOpts.Filter = map[string]any{
				"book_ids": strings.Join(learnBookIDs, ","),
			}
			fetchOpts.MaxResults = len(learnBookIDs)
		}
		label := learnBookSearch
		if label == "" {
			label = strings.Join(learnBookIDs, ", ")
		}
		progress.verbosef("Fetching Gutenberg book(s) (%s)...\n", label)
		opts.OnSourceIndexed = func(ev rag.SourceIndexedEvent) {
			progress.onSourceIndexed(ev.ChunkCount)
			progress.verbosef("  [gutenberg] indexed: %s (%d chunks)\n", ev.SourceRef, ev.ChunkCount)
		}
		stats, err := rag.IndexConnector(ctx, connector, mm, opts, fetchOpts)
		if err != nil {
			progress.addError()
			fmt.Printf("Gutenberg indexing error: %v\n", err)
			errors = append(errors, "gutenberg: "+err.Error())
		} else {
			fmt.Printf("Gutenberg: %d book(s) indexed, %d chunks.\n", stats.FilesIndexed, stats.ChunksIndexed)
			totalIndexed += stats.FilesIndexed
			totalChunks += stats.ChunksIndexed
		}
	}

	// GitHub
	for _, repoSlug := range learnGitHubRepos {
		repoSlug = strings.TrimSpace(repoSlug)
		if repoSlug == "" {
			continue
		}
		phaseIndex++
		progress.setPhase("github", phaseIndex, totalPhases)
		connector := githubconn.NewGitHubConnector()
		fetchOpts := connectors.FetchOptions{
			Query:      repoSlug,
			FolderID:   learnGitHubPath,
			MaxResults: learnGitHubMax,
		}
		progress.verbosef("Fetching GitHub repo (%s)...\n", repoSlug)
		opts.OnSourceIndexed = func(ev rag.SourceIndexedEvent) {
			progress.onSourceIndexed(ev.ChunkCount)
			progress.verbosef("  [github] indexed: %s (%d chunks)\n", ev.SourceRef, ev.ChunkCount)
		}
		stats, err := rag.IndexConnector(ctx, connector, mm, opts, fetchOpts)
		if err != nil {
			progress.addError()
			fmt.Printf("GitHub indexing error (%s): %v\n", repoSlug, err)
			errors = append(errors, "github("+repoSlug+"): "+err.Error())
		} else {
			fmt.Printf("GitHub %s: %d files indexed, %d chunks.\n", repoSlug, stats.FilesIndexed, stats.ChunksIndexed)
			totalIndexed += stats.FilesIndexed
			totalChunks += stats.ChunksIndexed
		}
	}
	progress.finish()

	fmt.Printf("\nConnector ingest complete. Total: %d documents, %d chunks.", totalIndexed, totalChunks)
	if len(errors) > 0 {
		fmt.Printf(" Errors: %s", strings.Join(errors, "; "))
	}
	fmt.Println()
}

// executeLearnChain runs sources in the user-specified order given by --chain.
// It shares a single MemoryManager and collects errors without stopping the chain.
func executeLearnChain(args []string, mm *memory.MemoryManager) {
	tokens := strings.Split(learnChain, ",")
	progress := newLearnProgress()
	totalSteps := 0
	for _, raw := range tokens {
		if strings.TrimSpace(raw) != "" {
			totalSteps++
		}
	}
	var errs []string
	grandTotal := 0
	grandChunks := 0
	stepIndex := 0

	for _, raw := range tokens {
		tok := strings.ToLower(strings.TrimSpace(raw))
		if tok == "" {
			continue
		}
		stepIndex++
		progress.setPhase("chain:"+tok, stepIndex, totalSteps)
		progress.verbosef("── chain step: %s ──\n", tok)
		var err error
		var indexed, chunks int
		switch tok {
		case "file":
			indexed, chunks, err = runChainFile(args, mm)
		case "dir":
			indexed, chunks, err = runChainDir(mm)
		case "url":
			indexed, chunks, err = runChainURL(mm)
		case "hf":
			indexed, chunks, err = runChainHF(mm)
		case "kaggle":
			indexed, chunks, err = runChainKaggle(mm)
		case "gmail":
			indexed, chunks, err = runChainGmail(mm)
		case "drive":
			indexed, chunks, err = runChainDrive(mm)
		case "notion":
			indexed, chunks, err = runChainNotion(mm)
		case "books", "gutenberg":
			indexed, chunks, err = runChainBooks(mm)
		case "github":
			indexed, chunks, err = runChainGitHub(mm)
		case "research":
			err = runChainResearch()
		default:
			progress.verbosef("Warning: unknown chain token %q — skipping.\n", tok)
			continue
		}
		if err != nil {
			progress.addError()
			fmt.Printf("Error in chain step %q: %v\n", tok, err)
			errs = append(errs, tok+": "+err.Error())
		} else {
			progress.verbosef("Chain step %q complete: %d documents, %d chunks.\n", tok, indexed, chunks)
			grandTotal += indexed
			grandChunks += chunks
		}
	}
	progress.finish()

	fmt.Printf("\n── chain complete. Total: %d documents, %d chunks.", grandTotal, grandChunks)
	if len(errs) > 0 {
		fmt.Printf(" Errors: %s", strings.Join(errs, "; "))
	}
	fmt.Println()
}

// ── per-source chain runner wrappers ────────────────────────────────────────

func runChainFile(args []string, mm *memory.MemoryManager) (int, int, error) {
	if learnFile == "" && len(args) == 0 {
		return 0, 0, fmt.Errorf("--file not set and no inline text provided")
	}
	var content string
	if learnFile != "" {
		data, err := os.ReadFile(learnFile)
		if err != nil {
			return 0, 0, err
		}
		content = string(data)
	} else {
		content = strings.Join(args, " ")
	}
	if err := mm.AddKnowledge(content, nil); err != nil {
		return 0, 0, err
	}
	return 1, 1, nil
}

func runChainDir(mm *memory.MemoryManager) (int, int, error) {
	if learnDir == "" {
		return 0, 0, fmt.Errorf("--dir not set")
	}
	opts := rag.DefaultIndexOptions()
	opts.MaxChunkChars = learnChunkChars
	opts.ChunkOverlap = learnChunkOverlap
	opts.Recursive = learnRecursive
	opts.Extensions = rag.ParseExtensionsCSV(strings.TrimSpace(learnExtensions))
	opts.OnSourceIndexed = func(ev rag.SourceIndexedEvent) {
		if learnVerbose {
			fmt.Printf("  [dir] indexed: %s (%d chunks)\n", ev.SourceRef, ev.ChunkCount)
		}
	}
	stats, err := rag.IndexDirectory(mm, learnDir, opts)
	if err != nil {
		return 0, 0, err
	}
	return stats.FilesIndexed, stats.ChunksIndexed, nil
}

func runChainURL(mm *memory.MemoryManager) (int, int, error) {
	seedURLs, err := collectURLs(learnURLs, learnURLFile)
	if err != nil {
		return 0, 0, err
	}
	if len(seedURLs) == 0 {
		return 0, 0, fmt.Errorf("--url and --url-file not set")
	}
	remoteOpts := rag.DefaultRemoteIndexOptions()
	remoteOpts.MaxChunkChars = learnChunkChars
	remoteOpts.ChunkOverlap = learnChunkOverlap
	remoteOpts.Crawl = learnCrawl
	remoteOpts.CrawlDepth = learnCrawlDepth
	remoteOpts.MaxPages = learnMaxPages
	remoteOpts.URLSafetyEnabled = learnURLSafety
	remoteOpts.URLSafetyTimeout = learnURLSafetyTimeout
	remoteOpts.URLSafetyCacheTTL = learnURLSafetyCacheTTL
	remoteOpts.URLSafetyVisibility = learnURLSafetyVisibility
	remoteOpts.URLSafetyFailOpen = learnURLSafetyFailOpen
	remoteOpts.URLSafetyAPIKey = strings.TrimSpace(os.Getenv("URLSCAN_API_KEY"))
	remoteOpts.URLAllowedExts = remoteURLAllowedExtensions()
	remoteOpts.OnEvent = func(ev rag.RemoteEvent) {
		if learnVerbose {
			fmt.Printf("  [url] %s: %s\n", ev.Outcome, ev.Source)
		}
	}
	stats, err := rag.IndexURLs(mm, seedURLs, remoteOpts)
	if err != nil {
		return 0, 0, err
	}
	return stats.ItemsIndexed, stats.ChunksIndexed, nil
}

func runChainHF(mm *memory.MemoryManager) (int, int, error) {
	if len(learnHFDatasets) == 0 {
		return 0, 0, fmt.Errorf("--hf-dataset not set")
	}
	specs := make([]rag.HFSpec, 0, len(learnHFDatasets))
	for _, ds := range learnHFDatasets {
		ds = strings.TrimSpace(ds)
		if ds == "" {
			continue
		}
		specs = append(specs, rag.HFSpec{
			DatasetID:  ds,
			Config:     strings.TrimSpace(learnHFConfig),
			Split:      strings.TrimSpace(learnHFSplit),
			MaxRecords: learnHFMaxRecords,
		})
	}
	remoteOpts := rag.DefaultRemoteIndexOptions()
	remoteOpts.HFToken = strings.TrimSpace(os.Getenv("HF_TOKEN"))
	remoteOpts.MaxChunkChars = learnChunkChars
	remoteOpts.ChunkOverlap = learnChunkOverlap
	remoteOpts.OnEvent = func(ev rag.RemoteEvent) {
		if learnVerbose {
			fmt.Printf("  [hf] %s: %s\n", ev.Outcome, ev.Source)
		}
	}
	stats, err := rag.IndexHFDatasets(mm, specs, remoteOpts)
	if err != nil {
		return 0, 0, err
	}
	return stats.ItemsIndexed, stats.ChunksIndexed, nil
}

func runChainKaggle(mm *memory.MemoryManager) (int, int, error) {
	if len(learnKaggleDatasets) == 0 {
		return 0, 0, fmt.Errorf("--kaggle-dataset not set")
	}
	specs := make([]rag.KaggleSpec, 0, len(learnKaggleDatasets))
	for _, ds := range learnKaggleDatasets {
		ds = strings.TrimSpace(ds)
		if ds == "" {
			continue
		}
		specs = append(specs, rag.KaggleSpec{
			DatasetID:  ds,
			Files:      append([]string(nil), learnKaggleFiles...),
			MaxRecords: learnKaggleMaxRecords,
		})
	}
	remoteOpts := rag.DefaultRemoteIndexOptions()
	remoteOpts.KaggleUsername = strings.TrimSpace(os.Getenv("KAGGLE_USERNAME"))
	remoteOpts.KaggleKey = strings.TrimSpace(os.Getenv("KAGGLE_KEY"))
	if remoteOpts.KaggleKey == "" {
		remoteOpts.KaggleKey = strings.TrimSpace(os.Getenv("KAGGLE_API_KEY"))
	}
	remoteOpts.MaxChunkChars = learnChunkChars
	remoteOpts.ChunkOverlap = learnChunkOverlap
	remoteOpts.OnEvent = func(ev rag.RemoteEvent) {
		if learnVerbose {
			fmt.Printf("  [kaggle] %s: %s\n", ev.Outcome, ev.Source)
		}
	}
	stats, err := rag.IndexKaggleDatasets(mm, specs, remoteOpts)
	if err != nil {
		return 0, 0, err
	}
	return stats.ItemsIndexed, stats.ChunksIndexed, nil
}

func runChainGmail(mm *memory.MemoryManager) (int, int, error) {
	if learnGmailQuery == "" {
		return 0, 0, fmt.Errorf("--gmail-query not set")
	}
	auth, err := googleconn.NewGoogleAuth()
	if err != nil {
		return 0, 0, err
	}
	connector := googleconn.NewGmailConnector(auth)
	opts := rag.DefaultIndexOptions()
	opts.MaxChunkChars = learnChunkChars
	opts.ChunkOverlap = learnChunkOverlap
	opts.OnSourceIndexed = func(ev rag.SourceIndexedEvent) {
		if learnVerbose {
			fmt.Printf("  [gmail] indexed: %s (%d chunks)\n", ev.SourceRef, ev.ChunkCount)
		}
	}
	stats, err := rag.IndexConnector(context.Background(), connector, mm, opts, connectors.FetchOptions{
		Query:      learnGmailQuery,
		MaxResults: learnGmailMax,
	})
	if err != nil {
		return 0, 0, err
	}
	return stats.FilesIndexed, stats.ChunksIndexed, nil
}

func runChainDrive(mm *memory.MemoryManager) (int, int, error) {
	if learnGdriveFolderID == "" && learnGdriveQuery == "" {
		return 0, 0, fmt.Errorf("--gdrive-folder or --gdrive-query not set")
	}
	auth, err := googleconn.NewGoogleAuth()
	if err != nil {
		return 0, 0, err
	}
	connector := googleconn.NewDriveConnector(auth)
	opts := rag.DefaultIndexOptions()
	opts.MaxChunkChars = learnChunkChars
	opts.ChunkOverlap = learnChunkOverlap
	opts.OnSourceIndexed = func(ev rag.SourceIndexedEvent) {
		if learnVerbose {
			fmt.Printf("  [drive] indexed: %s (%d chunks)\n", ev.SourceRef, ev.ChunkCount)
		}
	}
	stats, err := rag.IndexConnector(context.Background(), connector, mm, opts, connectors.FetchOptions{
		FolderID:   learnGdriveFolderID,
		Query:      learnGdriveQuery,
		MaxResults: learnGdriveMax,
	})
	if err != nil {
		return 0, 0, err
	}
	return stats.FilesIndexed, stats.ChunksIndexed, nil
}

func runChainNotion(mm *memory.MemoryManager) (int, int, error) {
	if learnNotionDatabaseID == "" {
		return 0, 0, fmt.Errorf("--notion-database not set")
	}
	connector, err := notion.NewNotionConnector()
	if err != nil {
		return 0, 0, err
	}
	var filter map[string]any
	if learnNotionFilter != "" {
		_ = json.Unmarshal([]byte(learnNotionFilter), &filter)
	}
	opts := rag.DefaultIndexOptions()
	opts.MaxChunkChars = learnChunkChars
	opts.ChunkOverlap = learnChunkOverlap
	opts.OnSourceIndexed = func(ev rag.SourceIndexedEvent) {
		if learnVerbose {
			fmt.Printf("  [notion] indexed: %s (%d chunks)\n", ev.SourceRef, ev.ChunkCount)
		}
	}
	stats, err := rag.IndexConnector(context.Background(), connector, mm, opts, connectors.FetchOptions{
		Query:      learnNotionDatabaseID,
		MaxResults: 100,
		Filter:     filter,
	})
	if err != nil {
		return 0, 0, err
	}
	return stats.FilesIndexed, stats.ChunksIndexed, nil
}

func runChainBooks(mm *memory.MemoryManager) (int, int, error) {
	if learnBookSearch == "" && len(learnBookIDs) == 0 {
		return 0, 0, fmt.Errorf("--book-search or --book-id not set")
	}
	connector := gutenberg.NewGutenbergConnector()
	fetchOpts := connectors.FetchOptions{Query: learnBookSearch, MaxResults: learnBookMax}
	if len(learnBookIDs) > 0 {
		fetchOpts.Filter = map[string]any{"book_ids": strings.Join(learnBookIDs, ",")}
		fetchOpts.MaxResults = len(learnBookIDs)
	}
	opts := rag.DefaultIndexOptions()
	opts.MaxChunkChars = learnChunkChars
	opts.ChunkOverlap = learnChunkOverlap
	opts.OnSourceIndexed = func(ev rag.SourceIndexedEvent) {
		if learnVerbose {
			fmt.Printf("  [books] indexed: %s (%d chunks)\n", ev.SourceRef, ev.ChunkCount)
		}
	}
	stats, err := rag.IndexConnector(context.Background(), connector, mm, opts, fetchOpts)
	if err != nil {
		return 0, 0, err
	}
	return stats.FilesIndexed, stats.ChunksIndexed, nil
}

func runChainGitHub(mm *memory.MemoryManager) (int, int, error) {
	if len(learnGitHubRepos) == 0 {
		return 0, 0, fmt.Errorf("--github-repo not set")
	}
	connector := githubconn.NewGitHubConnector()
	opts := rag.DefaultIndexOptions()
	opts.MaxChunkChars = learnChunkChars
	opts.ChunkOverlap = learnChunkOverlap
	opts.OnSourceIndexed = func(ev rag.SourceIndexedEvent) {
		if learnVerbose {
			fmt.Printf("  [github] indexed: %s (%d chunks)\n", ev.SourceRef, ev.ChunkCount)
		}
	}
	totalIndexed := 0
	totalChunks := 0
	for _, slug := range learnGitHubRepos {
		slug = strings.TrimSpace(slug)
		if slug == "" {
			continue
		}
		stats, err := rag.IndexConnector(context.Background(), connector, mm, opts, connectors.FetchOptions{
			Query:      slug,
			FolderID:   learnGitHubPath,
			MaxResults: learnGitHubMax,
		})
		if err != nil {
			return totalIndexed, totalChunks, fmt.Errorf("github %s: %w", slug, err)
		}
		totalIndexed += stats.FilesIndexed
		totalChunks += stats.ChunksIndexed
	}
	return totalIndexed, totalChunks, nil
}

func runChainResearch() error {
	if learnFromResearch == "" {
		return fmt.Errorf("--from-research not set")
	}
	return executeLearnFromResearch(learnFromResearch, learnIncludeResearchSummary, learnIncludeResearchSources)
}
