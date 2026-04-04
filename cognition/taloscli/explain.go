package taloscli

import (
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/spf13/cobra"
)

type capabilityDoc struct {
	Name      string
	Summary   string
	Usage     []string
	Abilities []string
	Examples  []string
	Related   []string
	Aliases   []string
}

const (
	explainUsageLimit    = 3
	explainFeatureLimit  = 4
	explainExamplesLimit = 3
)

var explainCmd = &cobra.Command{
	Use:   "explain <capability>",
	Short: "Explain TALOS capability usage and behavior.",
	Long:  "Explains what a TALOS capability does, how to use it, and where it fits in the runtime.",
	Args:  cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		query := normalizeCapability(strings.Join(args, " "))
		doc, ok := lookupCapabilityDoc(query)
		if !ok {
			writeExplainNotFound(cmd.OutOrStdout(), query)
			return nil
		}
		writeCapabilityDoc(cmd.OutOrStdout(), doc)
		return nil
	},
}

var capabilityDocs = []capabilityDoc{
	{
		Name:    "profile-gen",
		Summary: "Generates domain-based baseline profiles for learn and research workflows.",
		Usage: []string{
			"talos learn profile-gen <domain>",
			"talos learn profile-gen --list-domains",
			"talos learn profile-gen security --set-default",
			"talos research profile-gen <domain>",
			"talos research profile-gen --list-domains",
			"talos research profile-gen compliance --set-default",
		},
		Abilities: []string{
			"Builds deterministic profile presets from built-in domains.",
			"Supports dry-run preview without saving profile records.",
			"Uses safe collision behavior by default and requires --force to overwrite existing profile names.",
			"Can set generated profiles as defaults for learn or research workflows.",
		},
		Examples: []string{
			"talos learn profile-gen security --set-default",
			"talos research profile-gen compliance --category audit --set-default",
		},
		Related: []string{"learn profile", "research profile", "learn", "research"},
		Aliases: []string{"profile generator", "generate profile"},
	},
	{
		Name:    "find",
		Summary: "Searches TALOS commands by keyword across command names and descriptions.",
		Usage: []string{
			"talos find <keyword>",
			"talos find research",
			"talos find profile",
		},
		Abilities: []string{
			"Searches root and subcommands for keyword matches.",
			"Matches command name, use-line, aliases, and summary text.",
			"Returns a professional ASCII table of matching command paths.",
		},
		Examples: []string{
			"talos find research",
			"talos find doctor",
		},
		Related: []string{"help", "explain"},
		Aliases: []string{"search command", "lookup command"},
	},
	{
		Name:    "doc-search",
		Summary: "Safely performs recursive local document/file term search with bounded output.",
		Usage: []string{
			"talos doc-search <term>",
			"talos doc-search --dir ./docs \"sasswall\"",
			"talos doc-search --regex \"SCAC|Sasswall\" --max-matches 200",
			"talos doc-search --json \"intent normalization\"",
		},
		Abilities: []string{
			"Recursively searches workspace-local files for literal terms by default.",
			"Supports optional regex matching with configurable case sensitivity.",
			"Skips hidden/binary/oversized files by default with explicit safety bounds.",
			"Returns balanced output: per-file hit counts and contextual line snippets.",
			"Can emit structured JSON for automation with --json.",
		},
		Examples: []string{
			`talos doc-search "goal persistence coefficient" --dir ./pkg`,
			`talos doc-search --regex "TALOS_[A-Z_]+" --extensions .go,.md`,
		},
		Related: []string{"multi-agent", "tools", "find"},
		Aliases: []string{"search docs", "recursive search", "doc search"},
	},
	{
		Name:    "help",
		Summary: "Displays compact root help with top commands and a full command table.",
		Usage: []string{
			"talos --help",
			"talos find research",
			"talos chat --help",
		},
		Abilities: []string{
			"Renders root help in a single non-paginated view.",
			"Shows a Top Commands table plus a full All Commands table.",
			"Caps example output for readability and points to find/per-command help for detail.",
		},
		Examples: []string{
			"talos --help",
			"talos find profile",
		},
		Related: []string{"explain", "completion"},
		Aliases: []string{"command help", "help table"},
	},
	{
		Name:    "chat",
		Summary: "Primary conversational interface for interactive or one-shot prompts.",
		Usage: []string{
			"talos chat",
			"talos --namespace talos-runtime chat <prompt>",
			"talos chat <prompt>",
			"talos --skill <skill_id|name> chat <prompt>",
			"talos chat --domain talos-runtime <prompt>",
			"talos chat --namespace talos-runtime <prompt>",
			"talos chat <prompt> --text-gen",
			"talos chat --cognition auto|minimal|balanced|deep <prompt>",
			"talos chat --timeout-profile quick|normal|deep <prompt>",
			"talos chat --warmup=false <prompt>",
		},
		Abilities: []string{
			"Runs interactive REPL when no prompt is supplied.",
			"Sends a single prompt and exits when prompt args are supplied.",
			"Uses routing, memory, and tool orchestration during response generation.",
			"Supports explicit domain pinning via --domain (or --namespace / PLM_CHAT_DOMAIN) to scope retrieval to one work domain namespace.",
			"Root-level --namespace takes precedence for the command run and enforces workspace scope across command flows.",
			"Explicit --domain/--namespace selections persist as the active namespace for later runs when no override is provided.",
			"Supports explicit skill selection with global --skill flag.",
			"Supports experimental TALOS-native text generation via --text-gen (no value required; no Ollama calls in that mode).",
			"Supports PLM_CHAT_TEXT_GEN_PRIMARY=1 to make native text-gen the default output path unless overridden.",
			"Supports cognition budgeting to avoid heavy reasoning on simple prompts.",
			"Supports latency profiles and warmup behavior for chat responsiveness.",
		},
		Examples: []string{
			`talos chat "Draft a release note from recent commits"`,
			`talos chat --domain talos-runtime "Summarize latest runtime learnings"`,
			`talos chat --namespace talos-runtime "Summarize latest runtime learnings"`,
			`PLM_CHAT_DOMAIN=talos-runtime talos chat "What changed this week?"`,
			`talos --skill report2markdown chat "Convert this report into markdown with headings"`,
			"talos chat",
		},
		Related: []string{"multi-agent", "learn", "doctor"},
	},
	{
		Name:    "learn",
		Summary: "Ingests knowledge into TALOS memory from local sources, APIs, or live connectors.",
		Usage: []string{
			"talos learn <text>",
			"talos --namespace talos-runtime learn --dir ./docs",
			"talos learn --profile <name>",
			"talos learn --dry-run --profile <name>",
			"talos learn --verbose --dir <path>",
			"talos learn --self-train-speech <text>",
			"talos learn --synthetic-text-out <path> <text>",
			"talos learn --file <path>",
			"talos learn --dir <path> --extensions .md,.txt",
			"talos learn --dir <path> --type .go --type .md",
			"talos learn --dir <path> --all-types",
			"talos learn --url https://example.com/doc",
			"talos learn --url-file urls.txt --crawl --crawl-depth 1",
			"talos learn --url https://example.com/doc --crawl --extensions .html,.md",
			"talos learn --dir ./docs --namespace talos-runtime",
			"talos learn --from-research latest",
			"HF_TOKEN=... talos learn --hf-dataset wikipedia --hf-split train",
			"KAGGLE_USERNAME=... KAGGLE_KEY=... talos learn --kaggle-dataset owner/dataset",
			"talos learn --gmail-query \"label:inbox after:2024/01/01\" --gmail-max 100",
			"talos learn --gdrive-folder <folderID> --gdrive-max 50",
			"talos learn --gdrive-query \"mimeType='application/vnd.google-apps.document'\"",
			"talos learn --notion-database <databaseID>",
			`talos learn --notion-database <id> --notion-filter '{"property":"Status","select":{"equals":"Done"}}'`,
			"talos learn --book-search \"frankenstein\" --book-max 1",
			"talos learn --book-id 84 --book-id 1342",
			"talos learn --github-repo owner/repo",
			"talos learn --github-repo owner/repo --github-path pkg/ --github-max 200",
			"talos learn --chain \"dir,github,hf,kaggle\" --dir ./docs --github-repo owner/repo --hf-dataset owner/ds --kaggle-dataset owner/ds",
		},
		Abilities: []string{
			"Ingests direct text, file content, or directory content.",
			"Supports reusable learn profiles via --profile with explicit flag overrides.",
			"Uses compact friendly output by default; --verbose prints the detailed technical summary format.",
			"Renders a single live learn progress line (non-stacking) with synced counters during ingestion phases.",
			"Supports namespace tagging via --namespace so ingested knowledge can be isolated per work domain.",
			"Root-level --namespace overrides command-level namespace and applies one workspace scope to the full command run.",
			"Persists supplied --namespace as the active runtime namespace for subsequent chat/research retrieval.",
			"Can auto-generate speech self-training artifacts with --self-train-speech (no output path required).",
			"Can generate deterministic synthetic text-generation JSONL artifacts via --synthetic-text-out without indexing.",
			"Ingests remote URL content with optional bounded crawling.",
			"URL/crawl mode defaults to HTML page text extraction only.",
			"When --extensions is provided in URL mode, crawl+ingest uses strict URL extension matching (extensionless URLs are skipped).",
			"Runs URL safety checks (urlscan) before indexing remote URL content.",
			"Ingests persisted research artifacts (summary/findings/sources) for chained workflows.",
			"Ingests Hugging Face dataset rows with optional HF_TOKEN auth.",
			"Ingests Kaggle dataset rows from owner/dataset using KAGGLE_USERNAME + KAGGLE_KEY (or KAGGLE_API_KEY alias).",
			"Ingests Gmail messages via service-account auth (GOOGLE_SERVICE_ACCOUNT_JSON + GOOGLE_IMPERSONATE_USER).",
			"Ingests Google Drive files — auto-exports Docs as text, Sheets as CSV, Slides as text.",
			"Ingests Notion database pages with block-level text extraction (NOTION_API_KEY).",
			"Ingests full-text public domain books from Project Gutenberg by search query or book ID (no auth required). Auto-paginates results when --book-max > 32.",
			"Ingests GitHub repos via --github-repo (owner/repo). GITHUB_TOKEN optional (higher rate limits). Filters by --github-path, caps at --github-max files.",
			"Chains multiple sources in user-specified order via --chain (e.g. \"dir,github,hf,kaggle\"). Shares one MemoryManager; continues through errors.",
			"Chunks and indexes all material for future retrieval.",
			"Supports configurable chunk sizing and overlap.",
			"Directory ingest uses incremental re-indexing by default (skip unchanged files, archive removed-source chunks).",
			"Reports per-file crawl progress and continues through recoverable file errors.",
			"Supports dry-run planning output without performing indexing.",
		},
		Examples: []string{
			`talos learn "Runbook: restart sequence is A -> B -> C"`,
			"talos learn --profile hf-train-default",
			"talos learn --file ./docs/ops.md",
			"talos learn --gmail-query \"label:inbox after:2025/01/01\" --gmail-max 200",
			"talos learn --gdrive-folder 1aBcDeFgHiJkLmNoPqRsTuVwXyZ --gdrive-max 50",
			"talos learn --notion-database abc123 --notion-filter '{\"property\":\"Status\",\"select\":{\"equals\":\"Done\"}}'",
			"talos learn --book-search \"moby dick\"",
			"talos learn --book-id 2701 --book-id 84",
			"KAGGLE_USERNAME=... KAGGLE_KEY=... talos learn --kaggle-dataset zillow/zecon --kaggle-max-records 200",
			"talos learn --github-repo octocat/Hello-World",
			"talos learn --chain \"github,books,hf,kaggle\" --github-repo owner/repo --book-search \"dune\" --hf-dataset owner/ds --kaggle-dataset owner/ds",
		},
		Related: []string{"connectors", "learn profile", "learn-image", "skills", "chat"},
	},
	{
		Name:    "learn profile",
		Summary: "Creates, manages, and applies saved TALOS learning configurations.",
		Usage: []string{
			"talos learn profile create --name <name> [learn flags]",
			"talos learn profile-gen <domain>",
			"talos learn profile update --name <name> [learn flags]",
			"talos learn profile show --name <name>",
			"talos learn profile list",
			"talos learn profile delete --name <name>",
			"talos learn profile set-default --name <name>",
			"talos learn profile clear-default",
			"talos learn profile export --out <path> [--name <name>]",
			"talos learn profile import --in <path> [--merge|--replace]",
			"talos learn --profile <name> [learn flags]",
		},
		Abilities: []string{
			"Saves reusable learn configurations including HF dataset args, URL crawl settings, chunking options, and connector flags (Gmail/Drive/Notion).",
			"Persists namespace scope via --namespace so profile-driven runs can ingest into a consistent work domain.",
			"Generates domain-specific profile baselines via `talos learn profile-gen <domain>`.",
			"Supports default profile auto-apply when learn runs without --profile.",
			"Applies explicit CLI flags as runtime overrides on top of profile values.",
			"Stores only env-var references for secrets (no raw token persistence in profiles).",
			"Exports/imports unified learn+research profile bundles for portability.",
		},
		Examples: []string{
			`talos learn profile create --name hf-train-default --hf-dataset TeichAI/claude-4.5-opus-high-reasoning-250x --hf-config default --hf-split train`,
			"talos learn profile create --name runtime-domain --namespace talos-runtime --dir ./docs",
			"talos learn profile-gen security --set-default",
			"talos learn profile set-default --name hf-train-default",
			"talos learn --profile hf-train-default --hf-max-records 250",
		},
		Related: []string{"learn", "learned", "research"},
		Aliases: []string{"profiles", "learning profile"},
	},
	{
		Name:    "learn-image",
		Summary: "Indexes image-based content for visual retrieval and reasoning.",
		Usage: []string{
			"talos learn-image --image <path>",
			"talos learn-image <image-path ...>",
		},
		Abilities: []string{
			"Loads one or more images into TALOS memory.",
			"Supports repeated image flags and shell globs.",
			"Improves visual context recall in later tasks.",
		},
		Examples: []string{
			"talos learn-image --image ./screenshots/failure.png",
		},
		Related: []string{"learn", "chat"},
	},
	{
		Name:    "namespace",
		Summary: "Explains workspace namespace scoping, persistence, and exit behavior.",
		Usage: []string{
			"talos --namespace <name> <command ...>",
			"talos chat --namespace <name> <prompt>",
			"talos learn --namespace <name> --dir ./docs",
			"talos namespace show",
			"talos namespace list",
			"talos namespace clear",
			"talos namespace bind-skill --id <skill_id>",
			"talos namespace bind-tool --id <tool_id>",
			"talos namespace bindings",
		},
		Abilities: []string{
			"Sets a global workspace scope per invocation with root --namespace.",
			"Persists active namespace between runs until cleared.",
			"Applies active namespace across chat, learn, and research retrieval/ingest flows.",
			"Supports per-namespace allowlists for skills and tools/plugins with default deny when unbound.",
			"Supports explicit exit from workspace scope via `talos namespace clear`.",
		},
		Examples: []string{
			"talos --namespace talos-runtime research run \"What changed in runtime policy?\"",
			"talos namespace show",
			"talos namespace bind-tool --id web_search",
			"talos namespace bind-skill --id skill_123",
			"talos namespace clear",
		},
		Related: []string{"chat", "learn", "memory", "research"},
		Aliases: []string{"namespaces", "workspace", "workspace namespace"},
	},
	{
		Name:    "tasks",
		Summary: "Explains autonomous structured TODO boards and visible task progression.",
		Usage: []string{
			"talos multi-agent \"<objective>\" --mode planning",
			"talos tasks show",
			"talos tasks list",
			"talos tasks next",
			"talos tasks update --id <task_id> --status <pending|in_progress|done|blocked>",
			"talos tasks clear",
		},
		Abilities: []string{
			"Auto-materializes a structured task board from eligible planning/execution flows.",
			"Tracks task lifecycle with dependency-aware statuses: pending, in_progress, done, blocked.",
			"Persists task boards in namespace scope for isolated operator workflows.",
			"Shows compact task progression snapshots so operators can see active work at a glance.",
			"Supports manual status override for human-in-the-loop control.",
		},
		Examples: []string{
			"talos --namespace training multi-agent \"Build rollout plan\" --mode planning",
			"talos tasks show",
			"talos tasks next",
			"talos tasks update --id task-1 --status in_progress",
			"talos tasks update --id task-1 --status done",
		},
		Related: []string{"multi-agent", "pipeline", "namespace", "research"},
		Aliases: []string{"todo", "taskboard", "task board"},
	},
	{
		Name:    "memory",
		Summary: "Explains TALOS memory retrieval strategy and hybrid vector/BM25 tuning.",
		Usage: []string{
			"talos explain memory",
			"talos namespace show",
			"talos namespace clear",
			"talos chat --domain talos-runtime \"...\"",
			"talos learn --namespace talos-runtime --dir ./docs",
			"talos research belief-audit \"STRATA architecture\" --since 2025-01-01",
			"TALOS_MEMORY_RETRIEVAL_MODE=hybrid talos chat \"...\"",
			"TALOS_RETRIEVAL_CANDIDATE_WEIGHTS=0.6,0.4 talos chat \"...\"",
			"TALOS_RETRIEVAL_DYNAMIC_WEIGHTS=0.4,0.2,0.25,0.15 talos chat \"...\"",
			"TALOS_RETRIEVAL_KNOWLEDGE_WEIGHTS=0.7,0.3 TALOS_RETRIEVAL_SEGMENT_WEIGHTS=0.7,0.3 talos research run \"...\"",
			"TALOS_MAR_ENABLED=true TALOS_MAR_CANDIDATE_LIMIT=24 TALOS_MAR_MAX_ANCHORS=12 talos chat \"...\"",
			"TALOS_MAR_WEIGHTS=0.4,0.2,0.2,0.15,0.05 TALOS_MAR_MIN_SCORE=0.20 TALOS_MAR_TOPOLOGY_BOOST=0.08 talos multi-agent \"...\"",
			"TALOS_MAR_STATUS_ENABLED=true talos chat \"...\"",
			"TALOS_TOPOLOGY_ENABLED=true TALOS_TOPOLOGY_EXPANSION_LIMIT=6 talos research run \"...\"",
			"TALOS_TOPOLOGY_WEIGHTS=0.45,0.20,0.15,0.20 talos chat \"...\"",
			"TALOS_DOC_ROUTING_MAX_DOC_GROUPS=8 TALOS_DOC_ROUTING_MAX_CHUNKS_PER_DOC=5 talos research deep \"...\"",
			"TALOS_DOC_ROUTING_WEIGHTS=0.45,0.25,0.20,0.10 talos chat \"...\"",
			"TALOS_HIER_READ_MAX_SECTIONS_PER_DOC=3 TALOS_HIER_READ_MAX_TOTAL_SECTIONS=12 talos research run \"...\"",
			"TALOS_LONGFORM_ENABLED=true TALOS_LONGFORM_MAX_PASSES=3 talos research deep \"...\"",
			"TALOS_STYLE_V2_ENABLED=true TALOS_STYLE_MODEL_ENABLED=true talos chat \"...\"",
			"TALOS_STYLE_MODEL_TIMEOUT_MS=120 TALOS_STYLE_CACHE_TTL_SEC=600 TALOS_STYLE_CACHE_MAX=256 talos research run \"...\"",
			"TALOS_SYMBOLIC_SUPERVISION_ENABLED=true TALOS_SYMBOLIC_ENFORCEMENT_MODE=tiered talos chat \"...\"",
			"TALOS_SYMBOLIC_MAX_CORRECTIONS=2 TALOS_SYMBOLIC_TIMEOUT_MS=120 TALOS_SYMBOLIC_CACHE_TTL_SEC=600 talos multi-agent \"...\"",
			"TALOS_OVERLAY_POLICY_ENABLED=true TALOS_OVERLAY_DEFAULT_MODE=quiet TALOS_OVERLAY_MAX_BOXES=5 talos chat \"...\"",
			"TALOS_OVERLAY_MIN_CONFIDENCE=0.62 TALOS_OVERLAY_HIGH_RISK_AUTO_THRESHOLD=0.82 TALOS_OVERLAY_SESSION_CONSENT_TTL_SEC=900 talos chat \"...\"",
			"TALOS_REFLECTION_V2_ENABLED=true TALOS_REFLECTION_ENFORCEMENT_MODE=tiered TALOS_REFLECTION_CITATION_GATE=true talos research run \"...\"",
			"TALOS_REFLECTION_WARN_THRESHOLD=0.32 TALOS_REFLECTION_STEER_THRESHOLD=0.52 TALOS_REFLECTION_VETO_THRESHOLD=0.78 talos multi-agent \"...\"",
			"TALOS_REFLECTION_TIMEOUT_MS=250 TALOS_REFLECTION_STATUS_ENABLED=true TALOS_REFLECTION_LOG_PATH=.memory/reflection_audit.jsonl talos chat \"...\"",
		},
		Abilities: []string{
			"Memory retrieval is hybrid by default: semantic vector recall + lexical BM25 recall.",
			"Memory supports namespace scoping; chat can pin namespace with --domain (or PLM_CHAT_DOMAIN), and learn can ingest with --namespace.",
			"TALOS_MEMORY_RETRIEVAL_MODE controls retrieval path: hybrid (default), semantic, or lexical.",
			"TALOS_RETRIEVAL_CANDIDATE_WEIGHTS sets semantic,lexical fusion for initial candidate ranking (default: 0.60,0.40).",
			"TALOS_RETRIEVAL_DYNAMIC_WEIGHTS sets semantic,lexical,importance,freshness for chat context ranking (default: 0.40,0.20,0.25,0.15).",
			"TALOS_RETRIEVAL_KNOWLEDGE_WEIGHTS sets semantic,lexical mix for knowledge ordering (default: 0.70,0.30).",
			"TALOS_RETRIEVAL_SEGMENT_WEIGHTS sets semantic,lexical mix for segment similarity scores (default: 0.70,0.30).",
			"TALOS_MAR_ENABLED toggles Memory-Anchored Reasoning v2 context assembly (default: true).",
			"TALOS_MAR_CANDIDATE_LIMIT caps pre-ranked candidates per memory lane before anchor selection (default: 24).",
			"TALOS_MAR_MAX_ANCHORS caps total retained anchors emitted into context status (default: 12).",
			"TALOS_MAR_WEIGHTS sets semantic,lexical,importance,freshness,topology anchor scoring (default: 0.40,0.20,0.20,0.15,0.05).",
			"TALOS_MAR_MIN_SCORE filters weak anchors before reasoning context composition (default: 0.20).",
			"TALOS_MAR_TOPOLOGY_BOOST adds bounded bonus for topology-linked anchors (default: 0.08).",
			"TALOS_MAR_CACHE_TTL_SEC sets MAR context cache lifetime before refresh (default: 180s).",
			"TALOS_MAR_CACHE_MAX caps in-memory MAR context cache entries (default: 512).",
			"TALOS_MAR_STATUS_ENABLED prints ASCII MAR status and active shards during turn context build (default: true).",
			"TALOS_TOPOLOGY_ENABLED toggles topology-aware source linking (default: true).",
			"TALOS_TOPOLOGY_EDGE_THRESHOLD sets minimum link strength to persist (default: 0.45).",
			"TALOS_TOPOLOGY_MAX_NEIGHBORS caps persisted neighbors per source node (default: 8).",
			"TALOS_TOPOLOGY_EXPANSION_LIMIT caps 1-hop retrieval expansion from linked sources (default: 6).",
			"TALOS_TOPOLOGY_WEIGHTS sets ref,url_host,path_proximity,lexical_overlap weighting (default: 0.45,0.20,0.15,0.20).",
			"TALOS_TOPOLOGY_LEXICAL_MIN_SCORE filters weak lexical links before weighting (default: 0.12).",
			"TALOS_DOC_ROUTING_MAX_DOC_GROUPS caps routed source groups per synthesis pass (default: 8).",
			"TALOS_DOC_ROUTING_MAX_CHUNKS_PER_DOC caps routed chunks per source group (default: 5).",
			"TALOS_DOC_ROUTING_CANDIDATE_K controls initial segment candidate pool size (default: 48).",
			"TALOS_DOC_ROUTING_TIMEOUT_MS sets routing budget before graceful fallback (default: 1200ms).",
			"TALOS_DOC_ROUTING_WEIGHTS sets semantic,lexical,topology,metadata routing weights (default: 0.45,0.25,0.20,0.10).",
			"TALOS_HIER_READ_MAX_SECTIONS_PER_DOC caps selected section shards per source (default: 3).",
			"TALOS_HIER_READ_MAX_TOTAL_SECTIONS caps total selected sections per synthesis pass (default: 12).",
			"TALOS_HIER_READ_MIN_SECTION_SCORE filters weak sections before reduce (default: 0.20).",
			"TALOS_LONGFORM_ENABLED toggles bounded long-form cross-section reasoning (default: true).",
			"TALOS_LONGFORM_MAX_PASSES sets reasoning passes across section windows (default: 3).",
			"TALOS_LONGFORM_MAX_SECTIONS_PER_PASS caps sections considered per pass (default: 8).",
			"TALOS_LONGFORM_TIMEOUT_MS sets total long-form reasoning budget before fallback (default: 6000ms).",
			"TALOS_LONGFORM_TRIGGER_MIN_COMPLEXITY sets query complexity threshold for auto-trigger (default: 4).",
			"TALOS_LONGFORM_TRIGGER_MIN_SECTIONS sets minimum selected sections for auto-trigger (default: 4).",
			"TALOS_STYLE_V2_ENABLED toggles adaptive style profile conditioning (default: true).",
			"TALOS_STYLE_MODEL_ENABLED toggles lightweight model-based style refinement (default: true).",
			"TALOS_STYLE_MODEL_TIMEOUT_MS sets the per-turn style-refinement timeout budget (default: 120ms).",
			"TALOS_STYLE_CACHE_TTL_SEC sets style-profile cache lifetime before refresh (default: 600s).",
			"TALOS_STYLE_CACHE_MAX caps in-memory style-profile cache size (default: 256).",
			"TALOS_SYMBOLIC_SUPERVISION_ENABLED toggles unified symbolic supervision gates (default: true).",
			"TALOS_SYMBOLIC_ENFORCEMENT_MODE sets supervision behavior: tiered (default), hard, advisory.",
			"TALOS_SYMBOLIC_MAX_CORRECTIONS caps correction retries after symbolic veto (default: 2).",
			"TALOS_SYMBOLIC_TIMEOUT_MS sets bounded symbolic-check timeout budget (default: 120ms).",
			"TALOS_SELF_ALIGNMENT_ENABLED enables recursive self-alignment against evolving project philosophy (default: true).",
			"TALOS_SELF_ALIGNMENT_MAX_DEPTH sets recursive self-alignment passes (default: 2).",
			"TALOS_SELF_ALIGNMENT_LOG_PATH sets JSONL audit path for self-alignment drift events (default: .memory/self_alignment_audit.jsonl).",
			"TALOS_SYMBOLIC_CACHE_TTL_SEC sets symbolic decision cache TTL in seconds (default: 600).",
			"TALOS_SYMBOLIC_CACHE_MAX caps symbolic decision cache entries (default: 512).",
			"TALOS_OVERLAY_POLICY_ENABLED toggles precision-ranked symbolic overlays in chat visual reasoning (default: true).",
			"TALOS_OVERLAY_DEFAULT_MODE sets overlay behavior: quiet (default), on_demand, auto_high_risk.",
			"TALOS_OVERLAY_MAX_BOXES caps rendered overlay boxes per decision (default: 5).",
			"TALOS_OVERLAY_MIN_CONFIDENCE filters low-confidence overlay candidates (default: 0.62).",
			"TALOS_OVERLAY_HIGH_RISK_AUTO_THRESHOLD sets auto-render risk threshold in auto_high_risk mode (default: 0.82).",
			"TALOS_OVERLAY_SESSION_CONSENT_TTL_SEC sets session consent cache TTL for follow-up overlays (default: 900s).",
			"TALOS_OVERLAY_TAXONOMY_STRICT enforces canonical overlay kind/color/label mapping (default: true).",
			"TALOS_REFLECTION_V2_ENABLED toggles Reflection Layer v2 risk gating across chat/research/multi-agent (default: true).",
			"TALOS_REFLECTION_ENFORCEMENT_MODE sets reflection behavior: tiered (default), hard, advisory.",
			"TALOS_REFLECTION_WARN_THRESHOLD sets risk score threshold for warning intervention (default: 0.32).",
			"TALOS_REFLECTION_STEER_THRESHOLD sets risk score threshold for steer/correction intervention (default: 0.52).",
			"TALOS_REFLECTION_VETO_THRESHOLD sets risk score threshold for veto-level intervention (default: 0.78).",
			"TALOS_REFLECTION_TIMEOUT_MS sets bounded reflection correction timeout in milliseconds (default: 250).",
			"TALOS_REFLECTION_CITATION_GATE enforces citation marker checks for source-backed synthesis stages (default: true).",
			"TALOS_REFLECTION_STATUS_ENABLED emits compact runtime reflection status lines (default: true).",
			"TALOS_REFLECTION_LOG_PATH sets append-only JSONL reflection audit path (default: .memory/reflection_audit.jsonl).",
			"TALOS_REFLECTION_CACHE_TTL_SEC sets reflection decision cache lifetime (default: 90s).",
			"TALOS_REFLECTION_CACHE_MAX caps in-memory reflection decision cache entries (default: 512).",
			"Epistemic Worldview Fusion persists current project truth hash at .memory/worldview_truth.json and shift events at .memory/worldview_truth_shifts.jsonl.",
			"`talos monitor status` reports worldview truth hash, conflict index, and latest truth-shift reason to expose project-truth drift.",
			"`talos research belief-audit <topic> --since <date>` diffs baseline vs current worldview anchors to explain how TALOS understanding changed over time.",
			"All weight env values are comma-separated, auto-normalized, and invalid values safely fall back to defaults.",
		},
		Examples: []string{
			"talos explain memory",
			"TALOS_MEMORY_RETRIEVAL_MODE=lexical talos research run \"find exact policy wording\"",
			"TALOS_RETRIEVAL_DYNAMIC_WEIGHTS=0.3,0.1,0.45,0.15 talos chat \"use high-importance recall\"",
			"TALOS_RETRIEVAL_KNOWLEDGE_WEIGHTS=0.5,0.5 TALOS_RETRIEVAL_SEGMENT_WEIGHTS=0.6,0.4 talos research deep \"compare source claims\"",
		},
		Related: []string{"learn", "research", "chat", "debug-memory"},
		Aliases: []string{"memory tuning", "retrieval", "bm25", "vector memory"},
	},
	{
		Name:    "mcts",
		Summary: "Explains TALOS Monte Carlo Tree Search v2 strategy, widening, concurrency, and fallback controls.",
		Usage: []string{
			"talos explain mcts",
			"TALOS_MCTS_STRATEGY=puct TALOS_MCTS_MAX_CONCURRENCY=4 talos chat \"...\"",
			"TALOS_MCTS_WIDENING_ALPHA=0.5 TALOS_MCTS_WIDENING_K=1.5 TALOS_MCTS_PRIOR_WEIGHT=1.25 talos chat \"...\"",
			"TALOS_MCTS_LEGACY_FALLBACK=true TALOS_MCTS_STRATEGY=ucb1 talos chat \"...\"",
			"TALOS_MCTS_STATUS_ENABLED=true TALOS_MCTS_TRACE_PATH=.memory/mcts_trace.jsonl talos chat \"...\"",
		},
		Abilities: []string{
			"MCTS v2 defaults to PUCT selection with progressive widening for stronger branch-quality under bounded budgets.",
			"TALOS_MCTS_STRATEGY selects puct (default) or ucb1 fallback behavior.",
			"TALOS_MCTS_MAX_CONCURRENCY bounds parallel branch evaluation workers (default: 4).",
			"TALOS_MCTS_WIDENING_ALPHA controls widening growth rate by visit count (default: 0.5).",
			"TALOS_MCTS_WIDENING_K controls widening scaling factor (default: 1.5).",
			"TALOS_MCTS_PRIOR_WEIGHT controls prior-guided exploration pressure in PUCT (default: 1.25).",
			"TALOS_MCTS_VIRTUAL_LOSS reduces duplicate leaf selection during parallel search (default: 0.2).",
			"TALOS_MCTS_MAX_CHILDREN_PER_NODE caps local branch fanout during widening.",
			"TALOS_MCTS_LEGACY_FALLBACK forces UCB1-compatible behavior for rollback/debug scenarios.",
			"TALOS_MCTS_STATUS_ENABLED emits compact MCTS status lines in runtime output.",
			"TALOS_MCTS_TRACE_PATH sets optional JSONL trace sink for deep diagnostics (read-only observability path).",
			"Tuning playbook: start with puct defaults, raise concurrency for latency headroom, lower widening for deterministic/stable outputs.",
		},
		Examples: []string{
			"talos explain mcts",
			"TALOS_MCTS_STRATEGY=puct TALOS_MCTS_MAX_CONCURRENCY=6 talos chat \"Compare migration approaches\"",
			"TALOS_MCTS_LEGACY_FALLBACK=true talos chat \"Need stable rollback behavior\"",
		},
		Related: []string{"chat", "memory", "reflection", "multi-agent"},
		Aliases: []string{"monte carlo", "tree search", "puct", "ucb1"},
	},
	{
		Name:    "multi-agent",
		Summary: "Runs an extended planner-researcher-verifier-synthesizer flow.",
		Usage: []string{
			"talos multi-agent <query>",
			"talos multi-agent <query> --mode planning",
			"talos multi-agent <query> --agents planner,researcher,verifier,synthesizer",
			"talos multi-agent <query> --agents all --parallel",
		},
		Abilities: []string{
			"Executes staged multi-agent analysis.",
			"Performs source extraction and verification passes.",
			"Returns synthesized output after arbitration.",
			"Supports planner-only mode for initial task decomposition.",
			"Goal Decomposition v2 auto-expands high-level intents into recursive task trees (10-step default) for agentic autonomy.",
			"TALOS_GOAL_DECOMP_V2_ENABLED toggles recursive decomposition auto-expansion (default: true).",
			"TALOS_GOAL_DECOMP_V2_TARGET_STEPS sets target auto-expanded plan size (default: 10, max: 16).",
			"Supports targeted sub-agent execution in any requested order.",
			"Supports parallel execution when explicit agent selection is provided.",
		},
		Examples: []string{
			`talos multi-agent "What changed in dependency policy this week?"`,
			`talos multi-agent "Prepare migration sequence" --mode planning`,
			`talos multi-agent "Review architecture changes" --agents researcher,verifier,synthesizer`,
		},
		Related: []string{"chat", "research", "doctor"},
	},
	{
		Name:    "reflection",
		Summary: "Explains Reflection Layer v2 risk gating, correction passes, and enforcement tuning.",
		Usage: []string{
			"talos explain reflection",
			"TALOS_REFLECTION_V2_ENABLED=true TALOS_REFLECTION_ENFORCEMENT_MODE=tiered talos chat \"...\"",
			"TALOS_REFLECTION_ENFORCEMENT_MODE=hard TALOS_REFLECTION_CITATION_GATE=true talos research deep \"...\"",
			"TALOS_REFLECTION_ENFORCEMENT_MODE=advisory TALOS_REFLECTION_STATUS_ENABLED=true talos multi-agent \"...\"",
		},
		Abilities: []string{
			"Runs stage-aware reflection checks across chat, research, and multi-agent pipelines.",
			"Scores goal drift, contradiction pressure, symbolic violations, and citation coverage risk.",
			"Supports one bounded correction pass before fallback when risk is elevated.",
			"TALOS_REFLECTION_V2_ENABLED toggles Reflection v2 globally (default: true).",
			"TALOS_REFLECTION_ENFORCEMENT_MODE selects intervention strictness: tiered (default), hard, advisory.",
			"TALOS_REFLECTION_WARN_THRESHOLD sets warning trigger risk score (default: 0.32).",
			"TALOS_REFLECTION_STEER_THRESHOLD sets correction/steer trigger risk score (default: 0.52).",
			"TALOS_REFLECTION_VETO_THRESHOLD sets veto trigger risk score (default: 0.78).",
			"TALOS_REFLECTION_TIMEOUT_MS sets reflection correction timeout budget (default: 250ms).",
			"TALOS_REFLECTION_CITATION_GATE enforces citation markers when sources are available (default: true).",
			"TALOS_REFLECTION_STATUS_ENABLED prints compact intervention lines in runtime output (default: true).",
			"TALOS_REFLECTION_LOG_PATH sets append-only JSONL audit path (default: .memory/reflection_audit.jsonl).",
			"TALOS_REFLECTION_CACHE_TTL_SEC and TALOS_REFLECTION_CACHE_MAX tune decision cache reuse.",
			"Playbook: use tiered mode for production defaults, hard mode for high-assurance audits, advisory mode for exploratory runs.",
		},
		Examples: []string{
			"talos explain reflection",
			"TALOS_REFLECTION_ENFORCEMENT_MODE=tiered TALOS_REFLECTION_STATUS_ENABLED=true talos chat \"Draft incident response summary\"",
			"TALOS_REFLECTION_ENFORCEMENT_MODE=hard TALOS_REFLECTION_CITATION_GATE=true talos research run \"Summarize policy deltas\"",
			"TALOS_REFLECTION_ENFORCEMENT_MODE=advisory talos multi-agent \"Compare migration approaches\"",
		},
		Related: []string{"memory", "research", "multi-agent", "chat"},
		Aliases: []string{"reflection layer", "reflect", "guardrails reflection"},
	},
	{
		Name:    "skills",
		Summary: "Manages local TALOS cognitive skills lifecycle.",
		Usage: []string{
			"talos skills list",
			"talos skills show --id <skill_id>",
			"talos skills create --name <name> --intent <intent>",
			"talos skills create --name <name> --description <text>",
			"talos skills preflight --name <name> --intent <intent>",
			"talos skills preflight --name <name> --description <text>",
			"talos skills revisions --id <skill_id>",
			"talos skills validate --id <skill_id> [--revision <rev>]",
			"talos skills activate --id <skill_id> [--revision <rev>]",
			"talos skills deprecate --id <skill_id> [--revision <rev>]",
			"talos skills export --id <skill_id> --revision <rev> --out <bundle.json>",
			"talos skills import --in <bundle.json>",
			"talos skills migrate",
			"talos skills migrate --apply",
		},
		Abilities: []string{
			"Lists and inspects available skills.",
			"Creates new skills from operator requests.",
			"Accepts --description as intent fallback when --intent is omitted.",
			"Runs preflight checks before synthesis or activation.",
			"Can be selected at runtime with global --skill for chat commands.",
			"Supports V3 lifecycle states (draft, validated, active, deprecated, revoked) with revision-aware activation.",
			"Uses deterministic routing with confidence gates and legacy fallback support.",
			"Supports signed bundle export/import with integrity verification.",
			"Supports explicit legacy-to-V3 migration reporting with dry-run and --apply persistence.",
			"TALOS_JIT_SKILL_CHAINING_ENABLED allows planner-time drafting of a temporary Super-Skill that chains 3 V3 skills for complex tasks (default: true).",
			"TALOS_JIT_SKILL_CHAIN_MIN_COMPLEXITY sets minimum complexity score before JIT chaining is attempted (default: 7).",
			"TALOS_JIT_SKILL_CHAIN_MIN_CONFIDENCE sets minimum router confidence for each chained skill candidate (default: 0.35).",
		},
		Examples: []string{
			"talos skills list",
			`talos skills create --name report2markdown --description "Converts reports to markdown"`,
			`talos skills preflight --name report2markdown --description "Converts reports to markdown"`,
			"talos skills show --id talos_user_skill",
		},
		Related: []string{"learn", "tools admin"},
		Aliases: []string{"skill"},
	},
	{
		Name:    "tools",
		Summary: "Explains TALOS Tool Calling V3 deterministic orchestration, governance, and rollout controls.",
		Usage: []string{
			"talos explain tools",
			"TALOS_TOOLFLOW_V3_ENABLED=true talos chat \"...\"",
			"TALOS_TOOLFLOW_V3_ENABLED=true TALOS_TOOLFLOW_SHADOW_EVAL=true talos research run \"...\"",
			"TALOS_TOOLFLOW_V3_ENABLED=true TALOS_TOOLFLOW_MAX_WORKERS=4 talos multi-agent \"...\"",
			"TALOS_TOOLFLOW_AUTO_DEP_INFERENCE=true TALOS_TOOLFLOW_RETRY_ENABLED=true talos chat \"...\"",
			`talos chat "{\"tool\":\"doc_search\",\"args\":{\"query\":\"Sasswall\",\"dir\":\"./docs\"}}"`,
			`TALOS_MULTIMODAL_TOOLS_ENABLED=true talos chat "Use multimodal_tool to analyze this UI target with artifact context and web corroboration"`,
			`TALOS_AUTO_TOOLS_ENABLED=true talos chat "auto_tool: compare 3 approaches and then fetch implementation docs"`,
			"TALOS_VISUAL_HANDSHAKE_ENABLED=true TALOS_VISUAL_HANDSHAKE_AUTO_DISPATCH=true talos chat \"Analyze this specific UI element I'm looking at\"",
		},
		Abilities: []string{
			"Parses legacy and canonical tool-call formats with backward-compatible normalization.",
			"Builds deterministic tool execution plans with explicit policy validation before execution.",
			"Infers dependency edges when depends_on is omitted and parallelizes independent branches.",
			"Supports strict allowlist + typed schema validation for built-in tools.",
			"Runs fail-closed by default, with selective intervention pausing high-risk calls for explicit manual sign-off.",
			"Emits concise ASCII status for toolflow cognitive load, active shards, and node outcomes.",
			"TALOS_TOOLFLOW_V3_ENABLED toggles shared V3 runtime (chat/research/multi-agent) (default: true).",
			"TALOS_TOOLFLOW_SHADOW_EVAL toggles shadow policy-eval checks while legacy runtime executes (default: true).",
			"TALOS_TOOLFLOW_MAX_WORKERS sets bounded parallel execution workers (default: 4).",
			"TALOS_TOOLFLOW_NODE_TIMEOUT_MS sets per-node timeout budget in milliseconds (default: 12000).",
			"TALOS_TOOLFLOW_TRACE_ENABLED toggles concise runtime status output (default: true).",
			"TALOS_TOOLFLOW_TRACE_VERBOSE toggles additional debug-level orchestration traces (default: false).",
			"TALOS_TOOLFLOW_FAIL_POLICY selects execution safety mode: closed (default) or open.",
			"TALOS_TOOLFLOW_DEPRECATION_WARNINGS toggles legacy format warning lines (default: true).",
			"TALOS_TOOLFLOW_AUTO_DEP_INFERENCE toggles dependency inference and hybrid auto-parallel planning (default: true).",
			"TALOS_TOOLFLOW_RETRY_ENABLED toggles bounded retry handling for retryable transient failures (default: true).",
			"TALOS_TOOLFLOW_RETRY_MAX_ATTEMPTS sets max retry attempts per node (default: 2).",
			"TALOS_TOOLFLOW_RETRY_BACKOFF_MS sets retry backoff base in milliseconds (default: 250).",
			"TALOS_TOOLFLOW_RETRY_JITTER_MS sets retry jitter range in milliseconds (default: 100).",
			"TALOS_TOOLFLOW_GLOBAL_BUDGET_MS sets global plan execution budget before cancellation (default: 20000).",
			"TALOS_TOOLFLOW_AUTOTUNE_ENABLED enables self-optimizing tool arguments from local performance history (default: true).",
			"TALOS_TOOLFLOW_AUTOTUNE_PATH sets the persisted profile JSON path for learned argument recommendations (default: .memory/toolflow_autotune.json).",
			"multimodal_tool fuses visual ROI analysis, artifact context, memory retrieval, and web search into one deterministic handoff.",
			"TALOS_MULTIMODAL_TOOLS_ENABLED toggles multimodal tool integration (default: true).",
			"doc_search performs safe recursive local file/doc search with path allowlisting, binary skipping, and bounded result caps.",
			"TALOS_COLLAB_TOOL_STREAMS_ENABLED toggles collaborative live stream fan-out to planner/verifier/synthesizer watchers during multi-agent tool execution (default: true).",
			"TALOS_HITL_TRIGGERS_ENABLED toggles interactive Human-in-the-Loop runtime triggers (default: true).",
			"TALOS_HITL_INTERACTIVE toggles terminal prompt mode for HITL decisions (default: true when stdin is a TTY).",
			"TALOS_HITL_TIMEOUT_SEC sets HITL prompt timeout before safe defer/fail-closed behavior (default: 30).",
			"TALOS_HITL_STREAM_TRIGGER_ENABLED toggles HITL confirmation when collaborative stream pivots are detected (default: true).",
			"auto_tool enables recursive tool synthesis: TALOS decomposes a high-level goal into deterministic sub-steps and executes them with bounded depth/steps.",
			"TALOS_AUTO_TOOLS_ENABLED toggles recursive auto-tool synthesis (default: true).",
			"TALOS_AUTO_TOOLS_MAX_DEPTH caps recursive synthesis depth (default: 2, max: 4).",
			"TALOS_AUTO_TOOLS_MAX_STEPS caps synthesized execution steps per auto-tool invocation (default: 8, max: 24).",
			"TALOS_SELECTIVE_INTERVENTION_ENABLED toggles high-risk manual sign-off gating before tool execution (default: true).",
			"TALOS_SELECTIVE_INTERVENTION_THRESHOLD sets risk score threshold for intervention pause (default: 0.78).",
			"TALOS_SELECTIVE_AUTO_APPROVE bypasses manual gate for trusted environments (default: false).",
			"TALOS_SELECTIVE_APPROVED_IDS allows per-ticket approvals (comma-separated, e.g., si-abc123,si-def456).",
			"Artifact-driven inputs let web_search/fetch_url/http_request/vector_retrieve ingest research artifacts by artifact_id or artifact_path.",
			"Reflection v2 evidence notes can be ingested from reflection_audit_path and merged into tool query context.",
			"Shared execution buffers stream tool output shards to multi-agent councils, enabling mid-execution pivots on smoking-gun signals.",
			"analyze_visual_target accepts ROI coordinates/snippet/provenance and returns focused UI-element analysis.",
			"TALOS_VISUAL_HANDSHAKE_ENABLED toggles Vision->Tool handshake bridge (default: true).",
			"TALOS_VISUAL_HANDSHAKE_AUTO_DISPATCH auto-routes high-confidence visual targets into analyze_visual_target (default: true).",
			"TALOS_VISUAL_HANDSHAKE_MIN_CONFIDENCE sets minimum confidence for auto-dispatch (default: 0.68).",
			"TALOS_VISUAL_HANDSHAKE_REQUIRE_CONSENT enforces consent before handshake dispatch (default: true).",
			"TALOS_VISUAL_HANDSHAKE_REDACT_SNIPPETS enables sensitive snippet redaction before tool dispatch (default: true).",
		},
		Examples: []string{
			"talos explain tools",
			"TALOS_TOOLFLOW_V3_ENABLED=true talos chat \"compare migration options\"",
			"TALOS_TOOLFLOW_V3_ENABLED=true TALOS_TOOLFLOW_FAIL_POLICY=closed talos research deep \"audit source claims\"",
		},
		Related: []string{"chat", "research", "multi-agent", "tools admin"},
		Aliases: []string{"tool calling", "toolflow", "tool orchestration"},
	},
	{
		Name:    "tools admin",
		Summary: "Administers toolserver credentials, plugins, and tool generation jobs.",
		Usage: []string{
			"talos tools admin list",
			"talos tools admin create --client-id <id>",
			"talos tools admin plugins list",
			"talos tools admin toolgen generate --name <name> --goal <goal>",
		},
		Abilities: []string{
			"Provision and rotate toolserver client credentials.",
			"Install and enable/disable plugins.",
			"Request and track tool generation jobs.",
		},
		Examples: []string{
			"talos tools admin list",
			"talos tools admin plugins list",
		},
		Related: []string{"doctor", "monitor"},
		Aliases: []string{"tools", "admin", "toolserver"},
	},
	{
		Name:    "monitor",
		Summary: "Installs and manages TALOS shell integration across terminals.",
		Usage: []string{
			"talos monitor install",
			"talos monitor reboot",
			"talos monitor status",
			"talos monitor uninstall",
		},
		Abilities: []string{
			"Builds and installs talos binary into a PATH location.",
			"Can modify shell profile PATH blocks for bash/zsh.",
			"Reboot mode runs install plus profile-source verification and prints the source command to run in current terminal.",
			"Reports integration status and reverses integration cleanly.",
		},
		Examples: []string{
			"talos monitor install",
			"talos monitor reboot",
			"talos monitor status",
		},
		Related: []string{"doctor", "about"},
		Aliases: []string{"reboot", "monitor reboot", "shell reboot"},
	},
	{
		Name:    "doctor",
		Summary: "Runs runtime diagnostics and connectivity checks.",
		Usage: []string{
			"talos doctor",
			"talos doctor --no-network",
			"talos doctor --timeout 5s",
		},
		Abilities: []string{
			"Checks environment, local files, and writable runtime paths.",
			"Optionally probes Ollama and toolserver endpoints.",
			"Reports actionable PASS/WARN/FAIL diagnostics.",
		},
		Examples: []string{
			"talos doctor",
			"talos doctor --no-network",
		},
		Related: []string{"monitor", "tools admin"},
	},
	{
		Name:    "research",
		Summary: "Structured research workflows with bounded `run` and advanced `deep` modes.",
		Usage: []string{
			"talos research run <query>",
			"talos research run --profile <name> [query]",
			"talos research run --category <tag> [query]",
			"talos research run --dry-run --profile <name> [query]",
			"talos research deep <query>",
			"talos research deep --profile <name> [query]",
			"talos research deep --category <tag> [query]",
			"talos research deep --dry-run --profile <name> [query]",
			"talos research sessions --last 10",
			"talos research profiles create --name <name> --category <tag> [research flags]",
			"talos research profiles list --category <tag>",
			"talos research deep <query> --max-pages 120 --max-research-loops 8",
		},
		Abilities: []string{
			"Run mode uses a lighter tool-assisted research workflow.",
			"Deep mode uses expanded depth and multi-agent pipeline execution.",
			"Supports reusable research profiles via --profile with explicit flag overrides.",
			"Supports category-tagged profile grouping for targeted profile discovery.",
			"Supports category quick-run: --category auto-selects one matching profile, and errors when category matches none or multiple profiles.",
			"Supports dry-run planning output without starting model/tool execution.",
			"Persists session artifacts for chaining and later ingestion.",
			"Returns a professional ASCII report with findings, risks, actions, and numbered citations.",
		},
		Examples: []string{
			`talos research run "What changed in Kubernetes security this month?"`,
			`talos research run --profile market-scan "Bitcoin policy update"`,
			`talos research run --category crypto "Bitcoin policy update"`,
			`talos research deep "Evaluate tradeoffs of vector DB choices for this codebase"`,
		},
		Related: []string{"multi-agent", "doctor", "learn"},
	},
	{
		Name:    "research profile",
		Summary: "Creates, manages, and applies saved TALOS research configurations.",
		Usage: []string{
			"talos research profiles create --name <name> [--category <tag>] [research flags]",
			"talos research profile-gen <domain>",
			"talos research profiles update --name <name> [--category <tag>] [research flags]",
			"talos research profiles show --name <name>",
			"talos research profiles list",
			"talos research profiles list --category <tag>",
			"talos research profiles delete --name <name>",
			"talos research profiles set-default --name <name>",
			"talos research profiles clear-default",
			"talos research profiles export --out <path> [--name <name>]",
			"talos research profiles import --in <path> [--merge|--replace]",
			"talos research run --profile <name> [query]",
			"talos research deep --profile <name> [query]",
		},
		Abilities: []string{
			"Saves reusable research defaults for crawl depth, page budgets, loop budgets, timeout, and seed URLs.",
			"Generates domain-specific research profile baselines via `talos research profile-gen <domain>`.",
			"Supports query templates so run/deep can execute without positional query text.",
			"Supports category tags for profile grouping and filtered listing.",
			"Supports default profile auto-apply when run/deep omits --profile and --category.",
			"Applies explicit CLI flags as runtime overrides on top of profile values.",
			"Exports/imports unified learn+research profile bundles for portability.",
		},
		Examples: []string{
			"talos research profiles create --name market-scan --category crypto --query-template \"weekly BTC market scan\" --max-pages 60 --crawl-depth 2",
			"talos research profile-gen compliance --set-default",
			"talos research profiles list --category crypto",
			"talos research profiles set-default --name market-scan",
			"talos research deep --profile market-scan",
		},
		Related: []string{"research", "learn profile", "pipeline"},
		Aliases: []string{"research profiles", "research category"},
	},
	{
		Name:    "pipeline",
		Summary: "Executes strict, allowlisted command chains with structured handoff.",
		Usage: []string{
			`talos pipeline "research run 'query' --skill analyst ; learn --from-research latest --skill memory_curator"`,
			`talos pipeline "research run 'query' ; learn --from-research latest"`,
			`talos "research deep 'query' ; learn --from-research latest"`,
		},
		Abilities: []string{
			"Parses semicolon/pipe-separated step chains from a single quoted argument.",
			"Enforces allowlisted transitions only (v1: research run/deep -> learn --from-research).",
			"Supports per-step --skill bindings with fail-fast validation for missing/disabled skills.",
			"Rejects global --skill for chain mode to keep skill scope explicit per step.",
			"Stops on first step failure and reports the failing step.",
		},
		Examples: []string{
			`talos pipeline "research run 'incident review checklist' --skill researcher_v1 ; learn --from-research latest --skill kb_ingestor"`,
			`talos "research run 'incident review checklist' --skill researcher_v1 ; learn --from-research latest --skill kb_ingestor"`,
			`talos pipeline "research run 'incident review checklist' ; learn --from-research latest"`,
		},
		Related: []string{"research", "learn", "skills"},
	},
	{
		Name:    "chaining",
		Summary: "Explains TALOS chaining patterns across learn source chains and pipeline command chains.",
		Usage: []string{
			`talos learn --chain "url,books,hf,kaggle" --url https://example.com --book-search "frankenstein" --hf-dataset wikipedia --kaggle-dataset owner/dataset`,
			`talos learn --chain "dir,github,books" --dir ./docs --github-repo owner/repo --book-search "the art of war"`,
			`talos pipeline "research run 'query' ; learn --from-research latest"`,
			`talos "research run 'query' --skill analyst ; learn --from-research latest --skill memory_curator"`,
		},
		Abilities: []string{
			"Supports ordered multi-source learning chains with --chain (file, dir, url, hf, kaggle, gmail, drive, notion, books/gutenberg, github, research).",
			"Executes learn chain steps in user-specified order and reuses one MemoryManager across all chain steps.",
			"Continues through per-step errors in learn chain mode and reports aggregate success/errors at completion.",
			"Supports command-level chaining with talos pipeline for allowlisted research->learn workflows.",
			"Supports per-step skill binding in pipeline mode via --skill on each step.",
			"Supports chained flows in profile-driven runs by combining --profile with chain-relevant flags.",
		},
		Examples: []string{
			`talos learn --chain "url,books,hf"`,
			`talos learn --chain "url,books,hf" --url https://example.com --book-search "frankenstein" --hf-dataset wikipedia`,
			`talos learn --chain "dir,kaggle,github,books" --dir ./docs --kaggle-dataset owner/dataset --github-repo owner/repo --book-search "dune"`,
			`talos pipeline "research run 'incident checklist' ; learn --from-research latest"`,
			`talos pipeline "research run 'incident checklist' --skill researcher_v1 ; learn --from-research latest --skill memory_curator"`,
		},
		Related: []string{"learn", "pipeline", "connectors", "learn profile"},
		Aliases: []string{"chain workflows", "multi-source chaining"},
	},
	{
		Name:    "learned",
		Summary: "Review what TALOS learned across recent learn/training sessions.",
		Usage: []string{
			"talos learned",
			"talos learned --last 5",
			"talos learned --status failed",
			"talos learned --json",
		},
		Abilities: []string{
			"Shows recent learn sessions across inline, file, directory, remote, and image learning modes.",
			"Reports session status, metrics, top sources, and concise observations.",
			"Supports status/mode filters and JSON output for automation.",
		},
		Examples: []string{
			"talos learned --last 5",
			"talos learned --status success",
		},
		Related: []string{"learn", "learn-image"},
	},
	{
		Name:    "about",
		Summary: "Displays TALOS identity, mission, and architecture context.",
		Usage: []string{
			"talos about",
		},
		Abilities: []string{
			"Provides architecture overview from local repository documents.",
			"Summarizes mission, layers, and operating model.",
		},
		Examples: []string{
			"talos about",
		},
		Related: []string{"explain", "doctor"},
	},
	{
		Name:    "version",
		Summary: "Displays TALOS build and runtime version metadata.",
		Usage: []string{
			"talos version",
		},
		Abilities: []string{
			"Prints semantic version, commit, and build date metadata.",
			"Prints runtime target information (OS/architecture).",
		},
		Examples: []string{
			"talos version",
		},
		Related: []string{"update", "doctor"},
	},
	{
		Name:    "completion",
		Summary: "Generates shell completion scripts for TALOS commands and flags.",
		Usage: []string{
			"talos completion bash",
			"talos completion zsh",
			"talos completion fish",
			"talos completion powershell",
		},
		Abilities: []string{
			"Outputs shell-specific completion scripts to stdout.",
			"Supports Bash, Zsh, Fish, and PowerShell completions.",
			"Improves operator speed and reduces command/flag typing errors.",
		},
		Examples: []string{
			"talos completion bash > ~/.local/share/bash-completion/completions/talos",
			"talos completion zsh > ~/.zfunc/_talos",
		},
		Related: []string{"monitor", "version", "update"},
		Aliases: []string{"completions", "shell completion"},
	},
	{
		Name:    "release-check",
		Summary: "Runs strict pre-release readiness checks and returns gate exit status.",
		Usage: []string{
			"talos release-check",
			"talos release-check --strict=false",
			"talos release-check --json",
			"talos release-check --no-network --timeout 5s",
		},
		Abilities: []string{
			"Aggregates doctor diagnostics and release-focused checks into one report.",
			"Runs CLI package tests as part of release validation.",
			"Returns non-zero on WARN/FAIL by default for strict release gates.",
			"Supports JSON output for CI automation.",
		},
		Examples: []string{
			"talos release-check",
			"talos release-check --json",
		},
		Related: []string{"doctor", "update", "version"},
		Aliases: []string{"release check", "releasecheck"},
	},
	{
		Name:    "update",
		Summary: "Checks for updates and applies new TALOS versions.",
		Usage: []string{
			"talos update check",
			"talos update apply",
			"talos update auto --enable --interval 24h",
			"talos update auto --apply",
		},
		Abilities: []string{
			"Checks current installed TALOS version against latest module version.",
			"Applies updates through the Go install path.",
			"Configures periodic auto-update checks with optional auto-apply.",
			"Persists auto-update preferences and last-check metadata in local runtime state.",
		},
		Examples: []string{
			"talos update check",
			"talos update auto --enable --interval 24h",
		},
		Related: []string{"version", "doctor", "monitor"},
	},
	{
		Name:    "benchmark",
		Summary: "Runs model benchmark profiles and full recommendation orchestration.",
		Usage: []string{
			"talos benchmark run",
			"talos benchmark toolcall",
			"talos benchmark cognition",
			"talos benchmark full",
			"talos benchmark load --model llama3.2:latest --concurrency 4",
		},
		Abilities: []string{
			"Benchmarks standard, chat, json, codegen, tool-call, and cognition profiles.",
			"Runs full orchestration to produce ranking scores and recommended model list.",
			"Supports load testing for one model with configurable concurrency.",
		},
		Examples: []string{
			"talos benchmark full",
			"talos benchmark toolcall",
		},
		Related: []string{"doctor", "research"},
	},
	{
		Name:    "documentary",
		Summary: "Runs documentary provisioning and daemon workflows.",
		Usage: []string{
			"talos documentary provision",
			"talos documentary daemon",
		},
		Abilities: []string{
			"Provisions documentary runtime artifacts.",
			"Executes documentary daemon processing loop.",
		},
		Examples: []string{
			"talos documentary provision",
		},
		Related: []string{"archive-daemon", "scout-daemon"},
	},
	{
		Name:    "reflex-daemon",
		Summary: "Monitors runtime pressure, cadence drift, and reflex telemetry signals.",
		Usage: []string{
			"go run ./cmd/reflex-daemon",
			"go run ./cmd/reflex-daemon --interval 30s",
		},
		Abilities: []string{
			"Scans runtime telemetry loops and emits reflex alerts into local memory feeds.",
			"Tracks reasoning cadence anomalies for operator visibility.",
			"Maintains daemon state under .memory for restart continuity.",
		},
		Examples: []string{
			"go run ./cmd/reflex-daemon",
		},
		Related: []string{"scout-daemon", "archive-daemon", "doctor"},
		Aliases: []string{"reflex", "reflex daemon"},
	},
	{
		Name:    "scout-daemon",
		Summary: "Performs contradiction detection and deep evidence scouting.",
		Usage: []string{
			"go run ./cmd/scout-daemon",
			"go run ./cmd/scout-daemon --scan-interval 45s",
		},
		Abilities: []string{
			"Analyzes evidence streams and flags contradictions across findings.",
			"Writes scout work logs and oracle deadlock traces into .memory/scout_daemon.",
			"Supports persistent state for continuous background operation.",
		},
		Examples: []string{
			"go run ./cmd/scout-daemon",
		},
		Related: []string{"reflex-daemon", "archive-daemon", "research"},
		Aliases: []string{"scout", "scout daemon"},
	},
	{
		Name:    "archive-daemon",
		Summary: "Archives reasoning traces, decision records, and historical operational context.",
		Usage: []string{
			"go run ./cmd/archive-daemon",
		},
		Abilities: []string{
			"Persists reasoning/decision artifacts for historical retrieval.",
			"Supports traceability across planning and execution sessions.",
			"Feeds long-horizon context for later audits and explainability.",
		},
		Examples: []string{
			"go run ./cmd/archive-daemon",
		},
		Related: []string{"scout-daemon", "reflex-daemon", "about"},
		Aliases: []string{"archive", "archive daemon"},
	},
	{
		Name:    "planner-daemon",
		Summary: "Continuously decomposes goals into mission phases and task handoffs.",
		Usage: []string{
			"go run ./cmd/planner-daemon",
		},
		Abilities: []string{
			"Maintains planning state for active missions and phased execution.",
			"Generates actionable handoff structure for multi-stage workflows.",
			"Persists planner state for resilient restart behavior.",
		},
		Examples: []string{
			"go run ./cmd/planner-daemon",
		},
		Related: []string{"multi-agent", "research", "archive-daemon"},
		Aliases: []string{"planner daemon", "planner"},
	},
	{
		Name:    "dream-daemon",
		Summary: "Runs active-dreaming memory consolidation to densify topology links while idle.",
		Usage: []string{
			"go run ./cmd/dream-daemon",
			"go run ./cmd/dream-daemon --interval 45s --idle-after 90s",
			"go run ./cmd/dream-daemon --run-once",
		},
		Abilities: []string{
			"Scans indexed knowledge shards and derives cross-sectional links offline.",
			"Reinforces topology edges using section-link evidence to optimize lattice density.",
			"Writes dream cycle telemetry at .memory/dream_daemon/cycles.jsonl and resumable state at .memory/dream_daemon/state.json.",
		},
		Examples: []string{
			"go run ./cmd/dream-daemon",
		},
		Related: []string{"learn", "research", "planner-daemon", "archive-daemon"},
		Aliases: []string{"dream", "active dreaming", "dream daemon"},
	},
	{
		Name:    "compliance-daemon",
		Summary: "Runs advisory security/compliance scans over lab and runtime outputs.",
		Usage: []string{
			"go run ./cmd/compliance-daemon",
		},
		Abilities: []string{
			"Reads compliance-relevant findings from local result streams.",
			"Produces advisory hardening insights for operators.",
			"Maintains state for incremental scan progression.",
		},
		Examples: []string{
			"go run ./cmd/compliance-daemon",
		},
		Related: []string{"lab-assistant", "doctor", "research"},
		Aliases: []string{"compliance", "compliance daemon"},
	},
	{
		Name:    "lab-assistant",
		Summary: "Runs shadow fix-and-verify loops against detected issues.",
		Usage: []string{
			"go run ./cmd/lab-assistant",
		},
		Abilities: []string{
			"Tracks candidate files and evaluates proposed fixes in a controlled loop.",
			"Emits result artifacts for downstream compliance and audit daemons.",
			"Persists daemon state for stable incremental processing.",
		},
		Examples: []string{
			"go run ./cmd/lab-assistant",
		},
		Related: []string{"compliance-daemon", "reflex-daemon", "debug-memory"},
		Aliases: []string{"lab assistant", "lab"},
	},
	{
		Name:    "debug-memory",
		Summary: "Inspects and prints memory diagnostics for troubleshooting.",
		Usage: []string{
			"talos debug-memory",
		},
		Abilities: []string{
			"Shows memory state for debugging and validation.",
		},
		Examples: []string{
			"talos debug-memory",
		},
		Related: []string{"doctor", "learn"},
	},
	{
		Name:    "connectors",
		Summary: "API connector layer for ingesting live data from Google Workspace, Notion, Project Gutenberg, GitHub, and Kaggle datasets.",
		Usage: []string{
			"# Gmail — set GOOGLE_SERVICE_ACCOUNT_JSON and GOOGLE_IMPERSONATE_USER",
			"talos learn --gmail-query \"label:inbox after:2024/01/01\" --gmail-max 100",
			"# Google Drive — Docs/Sheets/Slides are auto-exported as plain text",
			"talos learn --gdrive-folder <folderID> --gdrive-max 50",
			"talos learn --gdrive-query \"mimeType='application/vnd.google-apps.document'\"",
			"# Notion — set NOTION_API_KEY",
			"talos learn --notion-database <databaseID>",
			`talos learn --notion-database <id> --notion-filter '{"property":"Status","select":{"equals":"Done"}}'`,
			"# Project Gutenberg — no credentials required",
			"talos learn --book-search \"frankenstein\" --book-max 1",
			"talos learn --book-id 84",
			"talos learn --book-id 84 --book-id 1342 --book-id 11",
			"# GitHub — GITHUB_TOKEN optional (higher rate limits)",
			"talos learn --github-repo owner/repo",
			"talos learn --github-repo owner/repo --github-path pkg/ --github-max 200",
			"talos learn --github-repo owner/repo1 --github-repo owner/repo2",
			"# Kaggle datasets — set KAGGLE_USERNAME + KAGGLE_KEY (or KAGGLE_API_KEY alias)",
			"talos learn --kaggle-dataset owner/dataset",
			"talos learn --kaggle-dataset owner/dataset --kaggle-file train.csv --kaggle-max-records 250",
			"# Chained sources — run in specified order, shared memory",
			"talos learn --chain \"dir,github,hf,kaggle\" --dir ./docs --github-repo owner/repo --hf-dataset owner/ds --kaggle-dataset owner/ds",
			"talos learn --chain \"books,url\" --book-search \"moby dick\" --url https://example.com",
		},
		Abilities: []string{
			"Gmail connector: searches messages by query, extracts text/plain body (strips HTML fallback).",
			"Drive connector: lists files in a folder or by query; exports Google Docs as text, Sheets as CSV, Slides as text; downloads other text files directly.",
			"Notion connector: queries a database, iterates pages, and extracts block-level rich text.",
			"Gutenberg connector: searches ~70k public domain books via Gutendex API; downloads full plain-text. No auth required.",
			"Gutenberg search auto-paginates across result pages — set --book-max > 32 to collect books beyond the first page.",
			"--book-search ingests the top N matching books (--book-max, default 1). --book-id fetches specific books by numeric Gutenberg ID.",
			"GitHub connector: walks full repo tree via git/trees API; filters by --github-path prefix; fetches and base64-decodes each text file. GITHUB_TOKEN optional.",
			"Kaggle ingest indexes dataset files from owner/dataset with row-capped tabular parsing (CSV/TSV/JSONL/TXT/MD).",
			"--chain runs sources in user-specified order (comma-separated tokens: file,dir,url,hf,kaggle,gmail,drive,notion,books,github,research). Continues through errors.",
			"All connectors implement a common Connector interface (pkg/connectors) — pluggable and extensible.",
			"Connectors can be registered and dispatched as sub-agents via ConnectorSubAgent.",
			"Output of every connector is chunked and indexed into TALOS memory via IndexConnector().",
			"Google/Notion credentials are read from shell exports or .env — never persisted in profiles.",
		},
		Examples: []string{
			"GOOGLE_SERVICE_ACCOUNT_JSON=./sa.json GOOGLE_IMPERSONATE_USER=you@domain.com talos learn --gmail-query \"label:inbox\" --gmail-max 50",
			"talos learn --gdrive-folder 1aBcDeFgHiJkLmNoPqRsTuVwXyZ",
			"NOTION_API_KEY=secret_xxx talos learn --notion-database abc123def456",
			"talos learn --book-search \"the art of war\" --book-max 1",
			"talos learn --book-id 2701 --book-id 84 --book-id 11",
			"GITHUB_TOKEN=ghp_xxx talos learn --github-repo torvalds/linux --github-path Documentation/ --github-max 300",
			"KAGGLE_USERNAME=... KAGGLE_KEY=... talos learn --kaggle-dataset zillow/zecon --kaggle-max-records 150",
			"talos learn --chain \"github,books,hf,kaggle\" --github-repo owner/repo --book-search \"dune\" --hf-dataset owner/ds --kaggle-dataset owner/ds",
		},
		Related: []string{"learn", "learn profile", "tools", "memory"},
		Aliases: []string{"connector", "google workspace", "gmail", "drive", "gdrive", "notion", "gutenberg", "book", "books", "github", "kaggle", "chain"},
	},
}

func normalizeCapability(s string) string {
	return strings.Join(strings.Fields(strings.ToLower(strings.TrimSpace(s))), " ")
}

func lookupCapabilityDoc(query string) (capabilityDoc, bool) {
	for _, doc := range capabilityDocs {
		if normalizeCapability(doc.Name) == query {
			return doc, true
		}
		for _, a := range doc.Aliases {
			if normalizeCapability(a) == query {
				return doc, true
			}
		}
	}
	return capabilityDoc{}, false
}

func writeCapabilityDoc(w io.Writer, doc capabilityDoc) {
	_, _ = fmt.Fprintf(w, "TALOS EXPLAIN\n\nCOMMAND\n  talos explain %s\n\nSTATUS\n  SUCCESS\n\nCAPABILITY\n  %s\n\nSUMMARY\n  %s\n", doc.Name, doc.Name, doc.Summary)
	if len(doc.Usage) > 0 {
		_, _ = io.WriteString(w, "\nUSAGE\n")
		limit := explainMin(len(doc.Usage), explainUsageLimit)
		for _, u := range doc.Usage[:limit] {
			_, _ = fmt.Fprintf(w, "  %s\n", u)
		}
		if len(doc.Usage) > limit {
			_, _ = fmt.Fprintf(w, "  ... (%d more)\n", len(doc.Usage)-limit)
		}
	}
	if len(doc.Abilities) > 0 {
		_, _ = io.WriteString(w, "\nFUNCTIONALITY\n")
		limit := explainMin(len(doc.Abilities), explainFeatureLimit)
		for i, f := range doc.Abilities[:limit] {
			_, _ = fmt.Fprintf(w, "  %d. %s\n", i+1, f)
		}
		if len(doc.Abilities) > limit {
			_, _ = fmt.Fprintf(w, "  ... (%d more)\n", len(doc.Abilities)-limit)
		}
	}
	if len(doc.Examples) > 0 {
		_, _ = io.WriteString(w, "\nEXAMPLES\n")
		limit := explainMin(len(doc.Examples), explainExamplesLimit)
		for _, ex := range doc.Examples[:limit] {
			_, _ = fmt.Fprintf(w, "  %s\n", ex)
		}
		if len(doc.Examples) > limit {
			_, _ = fmt.Fprintf(w, "  ... (%d more)\n", len(doc.Examples)-limit)
		}
	}
	if len(doc.Related) > 0 {
		_, _ = io.WriteString(w, "\nRELATED\n")
		for _, rel := range doc.Related {
			_, _ = fmt.Fprintf(w, "  talos explain %s\n", rel)
		}
	}
	_, _ = io.WriteString(w, "\nNEXT\n  Use: talos find <keyword>\n")
}

func explainMin(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func writeExplainNotFound(w io.Writer, query string) {
	names := make([]string, 0, len(capabilityDocs))
	for _, d := range capabilityDocs {
		names = append(names, d.Name)
	}
	sort.Strings(names)

	_, _ = fmt.Fprintf(w, "TALOS EXPLAIN\n\nCOMMAND\n  talos explain %s\n\nSTATUS\n  FAILED\n\nERROR\n  Unknown capability: %s\n", query, query)
	_, _ = io.WriteString(w, "\nAVAILABLE CAPABILITIES\n")
	for _, n := range names {
		_, _ = fmt.Fprintf(w, "  %s\n", n)
	}
	_, _ = io.WriteString(w, "\nNEXT\n  Use: talos explain <capability>\n")
}

func init() {
	rootCmd.AddCommand(explainCmd)
}
