package taloscli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

const defaultLearnProfilesPath = ".memory/learn_profiles.json"

var (
	learnProfilesPath           = defaultLearnProfilesPath
	learnProfile                string
	learnProfileApplied         string
	learnProfileManageName      string
	learnProfileManageDesc      string
	learnProfileShowJSON        bool
	learnProfileExportOut       string
	learnProfileExportNames     []string
	learnProfileImportIn        string
	learnProfileImportMerge     bool
	learnProfileImportReplace   bool
	learnProfileCreateCmd       = &cobra.Command{Use: "create", Short: "Create a saved learn profile.", Run: runLearnProfileCreate}
	learnProfileUpdateCmd       = &cobra.Command{Use: "update", Short: "Update an existing learn profile.", Run: runLearnProfileUpdate}
	learnProfileDeleteCmd       = &cobra.Command{Use: "delete", Short: "Delete a learn profile.", Run: runLearnProfileDelete}
	learnProfileShowCmd         = &cobra.Command{Use: "show", Short: "Show a learn profile.", Run: runLearnProfileShow}
	learnProfileListCmd         = &cobra.Command{Use: "list", Short: "List learn profiles.", Run: runLearnProfileList}
	learnProfileSetDefaultCmd   = &cobra.Command{Use: "set-default", Short: "Set default learn profile.", Run: runLearnProfileSetDefault}
	learnProfileClearDefaultCmd = &cobra.Command{Use: "clear-default", Short: "Clear default learn profile.", Run: runLearnProfileClearDefault}
	learnProfileExportCmd       = &cobra.Command{Use: "export", Short: "Export learn/research profiles to a unified bundle.", Run: runLearnProfileExport}
	learnProfileImportCmd       = &cobra.Command{Use: "import", Short: "Import learn/research profiles from a unified bundle.", Run: runLearnProfileImport}
)

type LearnProfileConfig struct {
	File                   string   `json:"file,omitempty"`
	Dir                    string   `json:"dir,omitempty"`
	Namespace              string   `json:"namespace,omitempty"`
	Recursive              bool     `json:"recursive"`
	Extensions             string   `json:"extensions,omitempty"`
	Types                  []string `json:"types,omitempty"`
	AllTypes               bool     `json:"all_types"`
	ChunkChars             int      `json:"chunk_chars"`
	ChunkOverlap           int      `json:"chunk_overlap"`
	URLs                   []string `json:"urls,omitempty"`
	URLFile                string   `json:"url_file,omitempty"`
	Crawl                  bool     `json:"crawl"`
	CrawlDepth             int      `json:"crawl_depth"`
	MaxPages               int      `json:"max_pages"`
	RateLimit              float64  `json:"rate_limit"`
	AllowedDomains         []string `json:"allowed_domains,omitempty"`
	UserAgent              string   `json:"user_agent,omitempty"`
	RemoteTimeout          string   `json:"remote_timeout,omitempty"`
	MaxBytes               int64    `json:"max_bytes"`
	AuthHeaderEnv          string   `json:"auth_header_env,omitempty"`
	HFDatasets             []string `json:"hf_datasets,omitempty"`
	HFConfig               string   `json:"hf_config,omitempty"`
	HFSplit                string   `json:"hf_split,omitempty"`
	HFMaxRecords           int      `json:"hf_max_records"`
	KaggleDatasets         []string `json:"kaggle_datasets,omitempty"`
	KaggleFiles            []string `json:"kaggle_files,omitempty"`
	KaggleMaxRecords       int      `json:"kaggle_max_records"`
	URLSafety              bool     `json:"url_safety"`
	URLSafetyTimeout       string   `json:"url_safety_timeout,omitempty"`
	URLSafetyCacheTTL      string   `json:"url_safety_cache_ttl,omitempty"`
	URLSafetyVisibility    string   `json:"url_safety_visibility,omitempty"`
	URLSafetyFailOpen      bool     `json:"url_safety_fail_open"`
	FromResearch           string   `json:"from_research,omitempty"`
	IncludeResearchSources bool     `json:"include_research_sources"`
	IncludeResearchSummary bool     `json:"include_research_summary"`
	SummarizeSources       bool     `json:"summarize_sources"`
	SummaryMaxChars        int      `json:"summary_max_chars"`
	SummaryMaxPoints       int      `json:"summary_max_points"`
	SummaryModel           string   `json:"summary_model,omitempty"`
	TitleChunks            bool     `json:"title_chunks"`
	TitleMaxChars          int      `json:"title_max_chars"`
	TitleModel             string   `json:"title_model,omitempty"`
	Incremental            bool     `json:"incremental"`
	IncrementalManifest    string   `json:"incremental_manifest,omitempty"`

	// Google Workspace — Gmail
	GmailQuery string `json:"gmail_query,omitempty"`
	GmailMax   int    `json:"gmail_max,omitempty"`

	// Google Workspace — Drive (includes Docs, Sheets, Slides)
	GdriveFolderID string `json:"gdrive_folder_id,omitempty"`
	GdriveQuery    string `json:"gdrive_query,omitempty"`
	GdriveMax      int    `json:"gdrive_max,omitempty"`

	// Notion
	NotionDatabaseID string `json:"notion_database_id,omitempty"`
	NotionFilter     string `json:"notion_filter,omitempty"`

	// Project Gutenberg
	BookSearch string   `json:"book_search,omitempty"`
	BookIDs    []string `json:"book_ids,omitempty"`
	BookMax    int      `json:"book_max,omitempty"`

	// GitHub
	GitHubRepos []string `json:"github_repos,omitempty"`
	GitHubPath  string   `json:"github_path,omitempty"`
	GitHubMax   int      `json:"github_max,omitempty"`

	// Chain
	Chain string `json:"chain,omitempty"`
}

type LearnProfile struct {
	Name        string             `json:"name"`
	Description string             `json:"description,omitempty"`
	CreatedAt   string             `json:"created_at"`
	UpdatedAt   string             `json:"updated_at"`
	Config      LearnProfileConfig `json:"config"`
}

type learnProfilesFile struct {
	Version        int            `json:"version"`
	DefaultProfile string         `json:"default_profile,omitempty"`
	Profiles       []LearnProfile `json:"profiles"`
}

type learnRuntimeSnapshot struct {
	learnFile                   string
	learnDir                    string
	learnNamespace              string
	learnRecursive              bool
	learnExtensions             string
	learnTypes                  []string
	learnAllTypes               bool
	learnChunkChars             int
	learnChunkOverlap           int
	learnURLs                   []string
	learnURLFile                string
	learnCrawl                  bool
	learnCrawlDepth             int
	learnMaxPages               int
	learnRateLimit              float64
	learnAllowedDomains         []string
	learnUserAgent              string
	learnRemoteTimeout          time.Duration
	learnMaxBytes               int64
	learnAuthHeaderEnv          string
	learnHFDatasets             []string
	learnHFConfig               string
	learnHFSplit                string
	learnHFMaxRecords           int
	learnKaggleDatasets         []string
	learnKaggleFiles            []string
	learnKaggleMaxRecords       int
	learnURLSafety              bool
	learnURLSafetyTimeout       time.Duration
	learnURLSafetyCacheTTL      time.Duration
	learnURLSafetyVisibility    string
	learnURLSafetyFailOpen      bool
	learnFromResearch           string
	learnIncludeResearchSources bool
	learnIncludeResearchSummary bool
	learnSummarizeSources       bool
	learnSummaryMaxChars        int
	learnSummaryMaxPoints       int
	learnSummaryModel           string
	learnTitleChunks            bool
	learnTitleMaxChars          int
	learnTitleModel             string
	learnIncremental            bool
	learnIncrementalManifest    string

	// Google Workspace + Notion connector fields
	learnGmailQuery       string
	learnGmailMax         int
	learnGdriveFolderID   string
	learnGdriveQuery      string
	learnGdriveMax        int
	learnNotionDatabaseID string
	learnNotionFilter     string
	learnBookSearch       string
	learnBookIDs          []string
	learnBookMax          int
	learnGitHubRepos      []string
	learnGitHubPath       string
	learnGitHubMax        int
	learnChain            string
}

func installLearnProfileCommands() {
	profileCmd := &cobra.Command{
		Use:   "profile",
		Short: "Create, manage, and apply reusable learn profiles.",
	}

	learnProfileCreateCmd.Flags().StringVar(&learnProfileManageName, "name", "", "Profile name")
	learnProfileCreateCmd.Flags().StringVar(&learnProfileManageDesc, "description", "", "Profile description")
	bindLearnConfigFlags(learnProfileCreateCmd.Flags())

	learnProfileUpdateCmd.Flags().StringVar(&learnProfileManageName, "name", "", "Profile name")
	learnProfileUpdateCmd.Flags().StringVar(&learnProfileManageDesc, "description", "", "Profile description")
	bindLearnConfigFlags(learnProfileUpdateCmd.Flags())

	learnProfileDeleteCmd.Flags().StringVar(&learnProfileManageName, "name", "", "Profile name")

	learnProfileShowCmd.Flags().StringVar(&learnProfileManageName, "name", "", "Profile name")
	learnProfileShowCmd.Flags().BoolVar(&learnProfileShowJSON, "json", false, "Render as JSON")

	learnProfileSetDefaultCmd.Flags().StringVar(&learnProfileManageName, "name", "", "Profile name")
	learnProfileExportCmd.Flags().StringVar(&learnProfileExportOut, "out", "", "Output bundle path")
	learnProfileExportCmd.Flags().StringSliceVar(&learnProfileExportNames, "name", nil, "Optional learn profile name filter (repeatable)")
	learnProfileImportCmd.Flags().StringVar(&learnProfileImportIn, "in", "", "Input bundle path")
	learnProfileImportCmd.Flags().BoolVar(&learnProfileImportMerge, "merge", true, "Merge imported profiles (overwrite by name)")
	learnProfileImportCmd.Flags().BoolVar(&learnProfileImportReplace, "replace", false, "Replace local learn/research profiles with imported profiles")

	profileCmd.AddCommand(learnProfileCreateCmd)
	profileCmd.AddCommand(learnProfileUpdateCmd)
	profileCmd.AddCommand(learnProfileDeleteCmd)
	profileCmd.AddCommand(learnProfileShowCmd)
	profileCmd.AddCommand(learnProfileListCmd)
	profileCmd.AddCommand(learnProfileSetDefaultCmd)
	profileCmd.AddCommand(learnProfileClearDefaultCmd)
	profileCmd.AddCommand(learnProfileExportCmd)
	profileCmd.AddCommand(learnProfileImportCmd)
	learnCmd.AddCommand(profileCmd)
}

func resolveLearnProfileForRun(cmd *cobra.Command) (string, error) {
	learnProfileApplied = ""
	store, err := loadLearnProfilesFile()
	if err != nil {
		return "", err
	}
	requested := strings.TrimSpace(learnProfile)
	if requested == "" {
		requested = strings.TrimSpace(store.DefaultProfile)
	}
	if requested == "" {
		return "", nil
	}
	idx := findLearnProfileIndex(store.Profiles, requested)
	if idx < 0 {
		return "", fmt.Errorf("learn profile not found: %s", requested)
	}
	selected := store.Profiles[idx]
	if err := validateLearnProfileConfig(selected.Config); err != nil {
		return "", fmt.Errorf("invalid learn profile %s: %w", selected.Name, err)
	}
	snap := captureLearnRuntimeSnapshot()
	applyLearnProfileConfig(selected.Config)
	restoreVisitedLearnFlags(cmd, snap)
	learnProfileApplied = selected.Name
	fmt.Printf("Using learn profile: %s\n", selected.Name)
	return selected.Name, nil
}

func runLearnProfileCreate(cmd *cobra.Command, args []string) {
	name := normalizeLearnProfileName(learnProfileManageName)
	if name == "" {
		fmt.Println("Error: --name is required")
		return
	}
	store, err := loadLearnProfilesFile()
	if err != nil {
		fmt.Printf("Error loading learn profiles: %v\n", err)
		return
	}
	if findLearnProfileIndex(store.Profiles, name) >= 0 {
		fmt.Printf("Error: profile already exists: %s\n", name)
		return
	}
	cfg := currentLearnProfileConfigFromGlobals()
	if err := validateLearnProfileConfig(cfg); err != nil {
		fmt.Printf("Error: invalid profile config: %v\n", err)
		return
	}
	now := time.Now().UTC().Format(time.RFC3339)
	store.Profiles = append(store.Profiles, LearnProfile{
		Name:        name,
		Description: strings.TrimSpace(learnProfileManageDesc),
		CreatedAt:   now,
		UpdatedAt:   now,
		Config:      cfg,
	})
	if err := saveLearnProfilesFile(store); err != nil {
		fmt.Printf("Error saving learn profiles: %v\n", err)
		return
	}
	fmt.Println("Learn profile created.")
	fmt.Printf("  name: %s\n", name)
}

func runLearnProfileUpdate(cmd *cobra.Command, args []string) {
	name := normalizeLearnProfileName(learnProfileManageName)
	if name == "" {
		fmt.Println("Error: --name is required")
		return
	}
	store, err := loadLearnProfilesFile()
	if err != nil {
		fmt.Printf("Error loading learn profiles: %v\n", err)
		return
	}
	idx := findLearnProfileIndex(store.Profiles, name)
	if idx < 0 {
		fmt.Printf("Error: profile not found: %s\n", name)
		return
	}
	profile := store.Profiles[idx]
	patchLearnProfileFromVisited(cmd, &profile)
	if strings.TrimSpace(learnProfileManageDesc) != "" || cmd.Flags().Changed("description") {
		profile.Description = strings.TrimSpace(learnProfileManageDesc)
	}
	profile.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	if err := validateLearnProfileConfig(profile.Config); err != nil {
		fmt.Printf("Error: invalid profile config: %v\n", err)
		return
	}
	store.Profiles[idx] = profile
	if err := saveLearnProfilesFile(store); err != nil {
		fmt.Printf("Error saving learn profiles: %v\n", err)
		return
	}
	fmt.Println("Learn profile updated.")
	fmt.Printf("  name: %s\n", profile.Name)
}

func runLearnProfileDelete(cmd *cobra.Command, args []string) {
	name := normalizeLearnProfileName(learnProfileManageName)
	if name == "" {
		fmt.Println("Error: --name is required")
		return
	}
	store, err := loadLearnProfilesFile()
	if err != nil {
		fmt.Printf("Error loading learn profiles: %v\n", err)
		return
	}
	idx := findLearnProfileIndex(store.Profiles, name)
	if idx < 0 {
		fmt.Printf("Error: profile not found: %s\n", name)
		return
	}
	store.Profiles = append(store.Profiles[:idx], store.Profiles[idx+1:]...)
	if strings.EqualFold(strings.TrimSpace(store.DefaultProfile), name) {
		store.DefaultProfile = ""
	}
	if err := saveLearnProfilesFile(store); err != nil {
		fmt.Printf("Error saving learn profiles: %v\n", err)
		return
	}
	fmt.Println("Learn profile deleted.")
	fmt.Printf("  name: %s\n", name)
}

func runLearnProfileShow(cmd *cobra.Command, args []string) {
	name := normalizeLearnProfileName(learnProfileManageName)
	if name == "" {
		fmt.Println("Error: --name is required")
		return
	}
	store, err := loadLearnProfilesFile()
	if err != nil {
		fmt.Printf("Error loading learn profiles: %v\n", err)
		return
	}
	idx := findLearnProfileIndex(store.Profiles, name)
	if idx < 0 {
		fmt.Printf("Error: profile not found: %s\n", name)
		return
	}
	p := store.Profiles[idx]
	if learnProfileShowJSON {
		b, _ := json.MarshalIndent(p, "", "  ")
		fmt.Println(string(b))
		return
	}
	fmt.Println("LEARN PROFILE")
	fmt.Printf("\nNAME\n  %s\n", p.Name)
	fmt.Printf("\nDESCRIPTION\n  %s\n", emptyAsNA(p.Description))
	fmt.Printf("\nTIMESTAMPS\n  created_at: %s\n  updated_at: %s\n", emptyAsNA(p.CreatedAt), emptyAsNA(p.UpdatedAt))
	fmt.Printf("\nDEFAULT\n  %t\n", strings.EqualFold(strings.TrimSpace(store.DefaultProfile), p.Name))
	b, _ := json.MarshalIndent(p.Config, "", "  ")
	fmt.Printf("\nCONFIG\n%s\n", string(b))
}

func runLearnProfileList(cmd *cobra.Command, args []string) {
	store, err := loadLearnProfilesFile()
	if err != nil {
		fmt.Printf("Error loading learn profiles: %v\n", err)
		return
	}
	sort.SliceStable(store.Profiles, func(i, j int) bool {
		return strings.ToLower(store.Profiles[i].Name) < strings.ToLower(store.Profiles[j].Name)
	})
	fmt.Println("LEARN PROFILES")
	fmt.Printf("\nSUMMARY\n  count: %d\n", len(store.Profiles))
	fmt.Printf("  default: %s\n", emptyAsNA(store.DefaultProfile))
	fmt.Println("\nPROFILES")
	if len(store.Profiles) == 0 {
		fmt.Println("  No profiles found.")
		return
	}
	for i, p := range store.Profiles {
		isDefault := strings.EqualFold(strings.TrimSpace(store.DefaultProfile), p.Name)
		mark := ""
		if isDefault {
			mark = " (default)"
		}
		fmt.Printf("  %d. %s%s\n", i+1, p.Name, mark)
		fmt.Printf("     desc: %s\n", emptyAsNA(p.Description))
		fmt.Printf("     updated: %s\n", emptyAsNA(p.UpdatedAt))
		fmt.Printf("     mode hints: hf_datasets=%d kaggle_datasets=%d urls=%d dir=%t file=%t\n", len(p.Config.HFDatasets), len(p.Config.KaggleDatasets), len(p.Config.URLs), strings.TrimSpace(p.Config.Dir) != "", strings.TrimSpace(p.Config.File) != "")
	}
}

func runLearnProfileSetDefault(cmd *cobra.Command, args []string) {
	name := normalizeLearnProfileName(learnProfileManageName)
	if name == "" {
		fmt.Println("Error: --name is required")
		return
	}
	store, err := loadLearnProfilesFile()
	if err != nil {
		fmt.Printf("Error loading learn profiles: %v\n", err)
		return
	}
	if findLearnProfileIndex(store.Profiles, name) < 0 {
		fmt.Printf("Error: profile not found: %s\n", name)
		return
	}
	store.DefaultProfile = name
	if err := saveLearnProfilesFile(store); err != nil {
		fmt.Printf("Error saving learn profiles: %v\n", err)
		return
	}
	fmt.Println("Default learn profile set.")
	fmt.Printf("  name: %s\n", name)
}

func runLearnProfileClearDefault(cmd *cobra.Command, args []string) {
	store, err := loadLearnProfilesFile()
	if err != nil {
		fmt.Printf("Error loading learn profiles: %v\n", err)
		return
	}
	store.DefaultProfile = ""
	if err := saveLearnProfilesFile(store); err != nil {
		fmt.Printf("Error saving learn profiles: %v\n", err)
		return
	}
	fmt.Println("Default learn profile cleared.")
}

func runLearnProfileExport(cmd *cobra.Command, args []string) {
	bundle, err := buildProfileBundle(learnProfileExportNames, nil)
	if err != nil {
		fmt.Printf("Error building profile bundle: %v\n", err)
		return
	}
	out := strings.TrimSpace(learnProfileExportOut)
	if out == "" {
		out = filepath.Join(".memory", "talos_profiles_bundle.json")
	}
	if err := writeProfileBundle(out, bundle); err != nil {
		fmt.Printf("Error writing profile bundle: %v\n", err)
		return
	}
	fmt.Println("Profile bundle exported.")
	fmt.Printf("  path: %s\n", out)
	fmt.Printf("  learn_profiles: %d\n", len(bundle.Learn.Profiles))
	fmt.Printf("  research_profiles: %d\n", len(bundle.Research.Profiles))
}

func runLearnProfileImport(cmd *cobra.Command, args []string) {
	in := strings.TrimSpace(learnProfileImportIn)
	if in == "" {
		fmt.Println("Error: --in is required")
		return
	}
	if learnProfileImportMerge && learnProfileImportReplace {
		fmt.Println("Error: --merge and --replace cannot be used together")
		return
	}
	bundle, err := readProfileBundle(in)
	if err != nil {
		fmt.Printf("Error reading profile bundle: %v\n", err)
		return
	}
	for _, p := range bundle.Learn.Profiles {
		if err := validateLearnProfileConfig(p.Config); err != nil {
			fmt.Printf("Error: invalid imported learn profile %s: %v\n", p.Name, err)
			return
		}
	}
	for _, p := range bundle.Research.Profiles {
		if err := validateResearchProfileConfig(p.Config); err != nil {
			fmt.Printf("Error: invalid imported research profile %s: %v\n", p.Name, err)
			return
		}
	}

	learnStore, err := loadLearnProfilesFile()
	if err != nil {
		fmt.Printf("Error loading local learn profiles: %v\n", err)
		return
	}
	researchStore, err := loadResearchProfilesFile()
	if err != nil {
		fmt.Printf("Error loading local research profiles: %v\n", err)
		return
	}

	if learnProfileImportReplace {
		learnStore.Profiles = append([]LearnProfile(nil), bundle.Learn.Profiles...)
		researchStore.Profiles = append([]ResearchProfile(nil), bundle.Research.Profiles...)
	} else {
		learnStore.Profiles = mergeLearnProfiles(learnStore.Profiles, bundle.Learn.Profiles)
		researchStore.Profiles = mergeResearchProfiles(researchStore.Profiles, bundle.Research.Profiles)
	}
	if findLearnProfileIndex(learnStore.Profiles, bundle.Learn.DefaultProfile) >= 0 {
		learnStore.DefaultProfile = normalizeLearnProfileName(bundle.Learn.DefaultProfile)
	}
	if findResearchProfileIndex(researchStore.Profiles, bundle.Research.DefaultProfile) >= 0 {
		researchStore.DefaultProfile = normalizeResearchProfileName(bundle.Research.DefaultProfile)
	}
	if findLearnProfileIndex(learnStore.Profiles, learnStore.DefaultProfile) < 0 {
		learnStore.DefaultProfile = ""
	}
	if findResearchProfileIndex(researchStore.Profiles, researchStore.DefaultProfile) < 0 {
		researchStore.DefaultProfile = ""
	}
	if err := saveLearnProfilesFile(learnStore); err != nil {
		fmt.Printf("Error saving learn profiles: %v\n", err)
		return
	}
	if err := saveResearchProfilesFile(researchStore); err != nil {
		fmt.Printf("Error saving research profiles: %v\n", err)
		return
	}
	fmt.Println("Profile bundle imported.")
	fmt.Printf("  path: %s\n", in)
	fmt.Printf("  learn_profiles: %d\n", len(learnStore.Profiles))
	fmt.Printf("  research_profiles: %d\n", len(researchStore.Profiles))
}

func currentLearnProfileConfigFromGlobals() LearnProfileConfig {
	return LearnProfileConfig{
		File:                   strings.TrimSpace(learnFile),
		Dir:                    strings.TrimSpace(learnDir),
		Namespace:              strings.TrimSpace(learnNamespace),
		Recursive:              learnRecursive,
		Extensions:             strings.TrimSpace(learnExtensions),
		Types:                  append([]string(nil), learnTypes...),
		AllTypes:               learnAllTypes,
		ChunkChars:             learnChunkChars,
		ChunkOverlap:           learnChunkOverlap,
		URLs:                   append([]string(nil), learnURLs...),
		URLFile:                strings.TrimSpace(learnURLFile),
		Crawl:                  learnCrawl,
		CrawlDepth:             learnCrawlDepth,
		MaxPages:               learnMaxPages,
		RateLimit:              learnRateLimit,
		AllowedDomains:         append([]string(nil), learnAllowedDomains...),
		UserAgent:              strings.TrimSpace(learnUserAgent),
		RemoteTimeout:          learnRemoteTimeout.String(),
		MaxBytes:               learnMaxBytes,
		AuthHeaderEnv:          strings.TrimSpace(learnAuthHeaderEnv),
		HFDatasets:             append([]string(nil), learnHFDatasets...),
		HFConfig:               strings.TrimSpace(learnHFConfig),
		HFSplit:                strings.TrimSpace(learnHFSplit),
		HFMaxRecords:           learnHFMaxRecords,
		KaggleDatasets:         append([]string(nil), learnKaggleDatasets...),
		KaggleFiles:            append([]string(nil), learnKaggleFiles...),
		KaggleMaxRecords:       learnKaggleMaxRecords,
		URLSafety:              learnURLSafety,
		URLSafetyTimeout:       learnURLSafetyTimeout.String(),
		URLSafetyCacheTTL:      learnURLSafetyCacheTTL.String(),
		URLSafetyVisibility:    strings.TrimSpace(learnURLSafetyVisibility),
		URLSafetyFailOpen:      learnURLSafetyFailOpen,
		FromResearch:           strings.TrimSpace(learnFromResearch),
		IncludeResearchSources: learnIncludeResearchSources,
		IncludeResearchSummary: learnIncludeResearchSummary,
		SummarizeSources:       learnSummarizeSources,
		SummaryMaxChars:        learnSummaryMaxChars,
		SummaryMaxPoints:       learnSummaryMaxPoints,
		SummaryModel:           strings.TrimSpace(learnSummaryModel),
		TitleChunks:            learnTitleChunks,
		TitleMaxChars:          learnTitleMaxChars,
		TitleModel:             strings.TrimSpace(learnTitleModel),
		Incremental:            learnIncremental,
		IncrementalManifest:    strings.TrimSpace(learnIncrementalManifest),

		GmailQuery:       strings.TrimSpace(learnGmailQuery),
		GmailMax:         learnGmailMax,
		GdriveFolderID:   strings.TrimSpace(learnGdriveFolderID),
		GdriveQuery:      strings.TrimSpace(learnGdriveQuery),
		GdriveMax:        learnGdriveMax,
		NotionDatabaseID: strings.TrimSpace(learnNotionDatabaseID),
		NotionFilter:     strings.TrimSpace(learnNotionFilter),
		BookSearch:       strings.TrimSpace(learnBookSearch),
		BookIDs:          learnBookIDs,
		BookMax:          learnBookMax,
		GitHubRepos:      learnGitHubRepos,
		GitHubPath:       strings.TrimSpace(learnGitHubPath),
		GitHubMax:        learnGitHubMax,
		Chain:            strings.TrimSpace(learnChain),
	}
}

func applyLearnProfileConfig(cfg LearnProfileConfig) {
	learnFile = strings.TrimSpace(cfg.File)
	learnDir = strings.TrimSpace(cfg.Dir)
	learnNamespace = strings.TrimSpace(cfg.Namespace)
	learnRecursive = cfg.Recursive
	learnExtensions = strings.TrimSpace(cfg.Extensions)
	learnTypes = append([]string(nil), cfg.Types...)
	learnAllTypes = cfg.AllTypes
	learnChunkChars = cfg.ChunkChars
	learnChunkOverlap = cfg.ChunkOverlap
	learnURLs = append([]string(nil), cfg.URLs...)
	learnURLFile = strings.TrimSpace(cfg.URLFile)
	learnCrawl = cfg.Crawl
	learnCrawlDepth = cfg.CrawlDepth
	learnMaxPages = cfg.MaxPages
	learnRateLimit = cfg.RateLimit
	learnAllowedDomains = append([]string(nil), cfg.AllowedDomains...)
	learnUserAgent = strings.TrimSpace(cfg.UserAgent)
	if d, err := time.ParseDuration(strings.TrimSpace(cfg.RemoteTimeout)); err == nil && d > 0 {
		learnRemoteTimeout = d
	}
	learnMaxBytes = cfg.MaxBytes
	learnAuthHeaderEnv = strings.TrimSpace(cfg.AuthHeaderEnv)
	learnHFDatasets = append([]string(nil), cfg.HFDatasets...)
	learnHFConfig = strings.TrimSpace(cfg.HFConfig)
	learnHFSplit = strings.TrimSpace(cfg.HFSplit)
	learnHFMaxRecords = cfg.HFMaxRecords
	learnKaggleDatasets = append([]string(nil), cfg.KaggleDatasets...)
	learnKaggleFiles = append([]string(nil), cfg.KaggleFiles...)
	learnKaggleMaxRecords = cfg.KaggleMaxRecords
	learnURLSafety = cfg.URLSafety
	if d, err := time.ParseDuration(strings.TrimSpace(cfg.URLSafetyTimeout)); err == nil && d > 0 {
		learnURLSafetyTimeout = d
	}
	if d, err := time.ParseDuration(strings.TrimSpace(cfg.URLSafetyCacheTTL)); err == nil && d > 0 {
		learnURLSafetyCacheTTL = d
	}
	learnURLSafetyVisibility = strings.TrimSpace(cfg.URLSafetyVisibility)
	learnURLSafetyFailOpen = cfg.URLSafetyFailOpen
	learnFromResearch = strings.TrimSpace(cfg.FromResearch)
	learnIncludeResearchSources = cfg.IncludeResearchSources
	learnIncludeResearchSummary = cfg.IncludeResearchSummary
	learnSummarizeSources = cfg.SummarizeSources
	if cfg.SummaryMaxChars > 0 {
		learnSummaryMaxChars = cfg.SummaryMaxChars
	}
	if cfg.SummaryMaxPoints > 0 {
		learnSummaryMaxPoints = cfg.SummaryMaxPoints
	}
	learnSummaryModel = strings.TrimSpace(cfg.SummaryModel)
	titleChunks := cfg.TitleChunks
	if !titleChunks && cfg.TitleMaxChars <= 0 && strings.TrimSpace(cfg.TitleModel) == "" {
		titleChunks = true
	}
	learnTitleChunks = titleChunks
	if cfg.TitleMaxChars > 0 {
		learnTitleMaxChars = cfg.TitleMaxChars
	}
	learnTitleModel = strings.TrimSpace(cfg.TitleModel)
	learnIncremental = cfg.Incremental
	learnIncrementalManifest = strings.TrimSpace(cfg.IncrementalManifest)

	learnGmailQuery = strings.TrimSpace(cfg.GmailQuery)
	learnGmailMax = cfg.GmailMax
	learnGdriveFolderID = strings.TrimSpace(cfg.GdriveFolderID)
	learnGdriveQuery = strings.TrimSpace(cfg.GdriveQuery)
	learnGdriveMax = cfg.GdriveMax
	learnNotionDatabaseID = strings.TrimSpace(cfg.NotionDatabaseID)
	learnNotionFilter = strings.TrimSpace(cfg.NotionFilter)
	learnBookSearch = strings.TrimSpace(cfg.BookSearch)
	learnBookIDs = cfg.BookIDs
	learnBookMax = cfg.BookMax
	learnGitHubRepos = cfg.GitHubRepos
	learnGitHubPath = strings.TrimSpace(cfg.GitHubPath)
	learnGitHubMax = cfg.GitHubMax
	learnChain = strings.TrimSpace(cfg.Chain)
}

func validateLearnProfileConfig(cfg LearnProfileConfig) error {
	if cfg.ChunkChars <= 0 {
		return fmt.Errorf("chunk_chars must be > 0")
	}
	if cfg.ChunkOverlap < 0 || cfg.ChunkOverlap >= cfg.ChunkChars {
		return fmt.Errorf("chunk_overlap must be >=0 and < chunk_chars")
	}
	if cfg.CrawlDepth < 0 {
		return fmt.Errorf("crawl_depth must be >= 0")
	}
	if cfg.MaxPages <= 0 {
		return fmt.Errorf("max_pages must be > 0")
	}
	if cfg.RateLimit <= 0 {
		return fmt.Errorf("rate_limit must be > 0")
	}
	if cfg.MaxBytes <= 0 {
		return fmt.Errorf("max_bytes must be > 0")
	}
	if cfg.HFMaxRecords <= 0 {
		return fmt.Errorf("hf_max_records must be > 0")
	}
	if len(cfg.KaggleDatasets) > 0 && cfg.KaggleMaxRecords <= 0 {
		return fmt.Errorf("kaggle_max_records must be > 0")
	}
	if cfg.RemoteTimeout != "" {
		if d, err := time.ParseDuration(cfg.RemoteTimeout); err != nil || d <= 0 {
			return fmt.Errorf("remote_timeout must be a positive duration")
		}
	}
	if cfg.URLSafetyTimeout != "" {
		if d, err := time.ParseDuration(cfg.URLSafetyTimeout); err != nil || d <= 0 {
			return fmt.Errorf("url_safety_timeout must be a positive duration")
		}
	}
	if cfg.URLSafetyCacheTTL != "" {
		if d, err := time.ParseDuration(cfg.URLSafetyCacheTTL); err != nil || d <= 0 {
			return fmt.Errorf("url_safety_cache_ttl must be a positive duration")
		}
	}
	vis := strings.TrimSpace(strings.ToLower(cfg.URLSafetyVisibility))
	if vis == "" {
		vis = "private"
	}
	switch vis {
	case "private", "unlisted", "public":
	default:
		return fmt.Errorf("url_safety_visibility must be private|unlisted|public")
	}
	if len(cfg.HFDatasets) > 0 {
		split := strings.TrimSpace(cfg.HFSplit)
		if split == "" {
			return fmt.Errorf("hf_split is required when hf_datasets is set")
		}
	}
	if cfg.SummaryMaxChars < 0 {
		return fmt.Errorf("summary_max_chars must be >= 0")
	}
	if cfg.SummaryMaxPoints < 0 {
		return fmt.Errorf("summary_max_points must be >= 0")
	}
	if cfg.TitleMaxChars < 0 {
		return fmt.Errorf("title_max_chars must be >= 0")
	}
	return nil
}

func loadLearnProfilesFile() (learnProfilesFile, error) {
	store := learnProfilesFile{Version: 1, Profiles: []LearnProfile{}}
	b, err := os.ReadFile(learnProfilesPath)
	if err != nil {
		if os.IsNotExist(err) {
			return store, nil
		}
		return store, err
	}
	if len(strings.TrimSpace(string(b))) == 0 {
		return store, nil
	}
	if err := json.Unmarshal(b, &store); err != nil {
		return store, err
	}
	if store.Version <= 0 {
		store.Version = 1
	}
	if store.Profiles == nil {
		store.Profiles = []LearnProfile{}
	}
	return store, nil
}

func saveLearnProfilesFile(store learnProfilesFile) error {
	if store.Version <= 0 {
		store.Version = 1
	}
	if store.Profiles == nil {
		store.Profiles = []LearnProfile{}
	}
	if err := os.MkdirAll(filepath.Dir(learnProfilesPath), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(store, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(learnProfilesPath, b, 0o644)
}

func findLearnProfileIndex(profiles []LearnProfile, name string) int {
	name = normalizeLearnProfileName(name)
	for i := range profiles {
		if normalizeLearnProfileName(profiles[i].Name) == name {
			return i
		}
	}
	return -1
}

func normalizeLearnProfileName(v string) string {
	v = strings.TrimSpace(strings.ToLower(v))
	v = strings.ReplaceAll(v, " ", "-")
	return v
}

func captureLearnRuntimeSnapshot() learnRuntimeSnapshot {
	return learnRuntimeSnapshot{
		learnFile:                   learnFile,
		learnDir:                    learnDir,
		learnNamespace:              learnNamespace,
		learnRecursive:              learnRecursive,
		learnExtensions:             learnExtensions,
		learnTypes:                  append([]string(nil), learnTypes...),
		learnAllTypes:               learnAllTypes,
		learnChunkChars:             learnChunkChars,
		learnChunkOverlap:           learnChunkOverlap,
		learnURLs:                   append([]string(nil), learnURLs...),
		learnURLFile:                learnURLFile,
		learnCrawl:                  learnCrawl,
		learnCrawlDepth:             learnCrawlDepth,
		learnMaxPages:               learnMaxPages,
		learnRateLimit:              learnRateLimit,
		learnAllowedDomains:         append([]string(nil), learnAllowedDomains...),
		learnUserAgent:              learnUserAgent,
		learnRemoteTimeout:          learnRemoteTimeout,
		learnMaxBytes:               learnMaxBytes,
		learnAuthHeaderEnv:          learnAuthHeaderEnv,
		learnHFDatasets:             append([]string(nil), learnHFDatasets...),
		learnHFConfig:               learnHFConfig,
		learnHFSplit:                learnHFSplit,
		learnHFMaxRecords:           learnHFMaxRecords,
		learnKaggleDatasets:         append([]string(nil), learnKaggleDatasets...),
		learnKaggleFiles:            append([]string(nil), learnKaggleFiles...),
		learnKaggleMaxRecords:       learnKaggleMaxRecords,
		learnURLSafety:              learnURLSafety,
		learnURLSafetyTimeout:       learnURLSafetyTimeout,
		learnURLSafetyCacheTTL:      learnURLSafetyCacheTTL,
		learnURLSafetyVisibility:    learnURLSafetyVisibility,
		learnURLSafetyFailOpen:      learnURLSafetyFailOpen,
		learnFromResearch:           learnFromResearch,
		learnIncludeResearchSources: learnIncludeResearchSources,
		learnIncludeResearchSummary: learnIncludeResearchSummary,
		learnSummarizeSources:       learnSummarizeSources,
		learnSummaryMaxChars:        learnSummaryMaxChars,
		learnSummaryMaxPoints:       learnSummaryMaxPoints,
		learnSummaryModel:           learnSummaryModel,
		learnTitleChunks:            learnTitleChunks,
		learnTitleMaxChars:          learnTitleMaxChars,
		learnTitleModel:             learnTitleModel,
		learnIncremental:            learnIncremental,
		learnIncrementalManifest:    learnIncrementalManifest,

		learnGmailQuery:       learnGmailQuery,
		learnGmailMax:         learnGmailMax,
		learnGdriveFolderID:   learnGdriveFolderID,
		learnGdriveQuery:      learnGdriveQuery,
		learnGdriveMax:        learnGdriveMax,
		learnNotionDatabaseID: learnNotionDatabaseID,
		learnNotionFilter:     learnNotionFilter,
		learnBookSearch:       learnBookSearch,
		learnBookIDs:          learnBookIDs,
		learnBookMax:          learnBookMax,
		learnGitHubRepos:      learnGitHubRepos,
		learnGitHubPath:       learnGitHubPath,
		learnGitHubMax:        learnGitHubMax,
		learnChain:            learnChain,
	}
}

func restoreVisitedLearnFlags(cmd *cobra.Command, snap learnRuntimeSnapshot) {
	visited := map[string]bool{}
	cmd.Flags().Visit(func(f *pflag.Flag) {
		visited[f.Name] = true
	})
	if len(visited) == 0 {
		return
	}
	if visited["file"] {
		learnFile = snap.learnFile
	}
	if visited["dir"] {
		learnDir = snap.learnDir
	}
	if visited["namespace"] {
		learnNamespace = snap.learnNamespace
	}
	if visited["recursive"] {
		learnRecursive = snap.learnRecursive
	}
	if visited["extensions"] {
		learnExtensions = snap.learnExtensions
	}
	if visited["type"] {
		learnTypes = append([]string(nil), snap.learnTypes...)
	}
	if visited["all-types"] {
		learnAllTypes = snap.learnAllTypes
	}
	if visited["chunk-chars"] {
		learnChunkChars = snap.learnChunkChars
	}
	if visited["chunk-overlap"] {
		learnChunkOverlap = snap.learnChunkOverlap
	}
	if visited["url"] {
		learnURLs = append([]string(nil), snap.learnURLs...)
	}
	if visited["url-file"] {
		learnURLFile = snap.learnURLFile
	}
	if visited["crawl"] {
		learnCrawl = snap.learnCrawl
	}
	if visited["crawl-depth"] {
		learnCrawlDepth = snap.learnCrawlDepth
	}
	if visited["max-pages"] {
		learnMaxPages = snap.learnMaxPages
	}
	if visited["rate-limit"] {
		learnRateLimit = snap.learnRateLimit
	}
	if visited["allowed-domain"] {
		learnAllowedDomains = append([]string(nil), snap.learnAllowedDomains...)
	}
	if visited["user-agent"] {
		learnUserAgent = snap.learnUserAgent
	}
	if visited["remote-timeout"] {
		learnRemoteTimeout = snap.learnRemoteTimeout
	}
	if visited["max-bytes"] {
		learnMaxBytes = snap.learnMaxBytes
	}
	if visited["auth-header-env"] {
		learnAuthHeaderEnv = snap.learnAuthHeaderEnv
	}
	if visited["hf-dataset"] {
		learnHFDatasets = append([]string(nil), snap.learnHFDatasets...)
	}
	if visited["hf-config"] {
		learnHFConfig = snap.learnHFConfig
	}
	if visited["hf-split"] {
		learnHFSplit = snap.learnHFSplit
	}
	if visited["hf-max-records"] {
		learnHFMaxRecords = snap.learnHFMaxRecords
	}
	if visited["kaggle-dataset"] {
		learnKaggleDatasets = append([]string(nil), snap.learnKaggleDatasets...)
	}
	if visited["kaggle-file"] {
		learnKaggleFiles = append([]string(nil), snap.learnKaggleFiles...)
	}
	if visited["kaggle-max-records"] {
		learnKaggleMaxRecords = snap.learnKaggleMaxRecords
	}
	if visited["url-safety"] {
		learnURLSafety = snap.learnURLSafety
	}
	if visited["url-safety-timeout"] {
		learnURLSafetyTimeout = snap.learnURLSafetyTimeout
	}
	if visited["url-safety-cache-ttl"] {
		learnURLSafetyCacheTTL = snap.learnURLSafetyCacheTTL
	}
	if visited["url-safety-visibility"] {
		learnURLSafetyVisibility = snap.learnURLSafetyVisibility
	}
	if visited["url-safety-fail-open"] {
		learnURLSafetyFailOpen = snap.learnURLSafetyFailOpen
	}
	if visited["from-research"] {
		learnFromResearch = snap.learnFromResearch
	}
	if visited["include-research-summary"] {
		learnIncludeResearchSummary = snap.learnIncludeResearchSummary
	}
	if visited["include-research-sources"] {
		learnIncludeResearchSources = snap.learnIncludeResearchSources
	}
	if visited["summarize-sources"] {
		learnSummarizeSources = snap.learnSummarizeSources
	}
	if visited["summary-max-chars"] {
		learnSummaryMaxChars = snap.learnSummaryMaxChars
	}
	if visited["summary-max-points"] {
		learnSummaryMaxPoints = snap.learnSummaryMaxPoints
	}
	if visited["summary-model"] {
		learnSummaryModel = snap.learnSummaryModel
	}
	if visited["title-chunks"] {
		learnTitleChunks = snap.learnTitleChunks
	}
	if visited["title-max-chars"] {
		learnTitleMaxChars = snap.learnTitleMaxChars
	}
	if visited["title-model"] {
		learnTitleModel = snap.learnTitleModel
	}
	if visited["incremental"] {
		learnIncremental = snap.learnIncremental
	}
	if visited["incremental-manifest"] {
		learnIncrementalManifest = snap.learnIncrementalManifest
	}
	if visited["gmail-query"] {
		learnGmailQuery = snap.learnGmailQuery
	}
	if visited["gmail-max"] {
		learnGmailMax = snap.learnGmailMax
	}
	if visited["gdrive-folder"] {
		learnGdriveFolderID = snap.learnGdriveFolderID
	}
	if visited["gdrive-query"] {
		learnGdriveQuery = snap.learnGdriveQuery
	}
	if visited["gdrive-max"] {
		learnGdriveMax = snap.learnGdriveMax
	}
	if visited["notion-database"] {
		learnNotionDatabaseID = snap.learnNotionDatabaseID
	}
	if visited["notion-filter"] {
		learnNotionFilter = snap.learnNotionFilter
	}
	if visited["book-search"] {
		learnBookSearch = snap.learnBookSearch
	}
	if visited["book-id"] {
		learnBookIDs = snap.learnBookIDs
	}
	if visited["book-max"] {
		learnBookMax = snap.learnBookMax
	}
	if visited["github-repo"] {
		learnGitHubRepos = snap.learnGitHubRepos
	}
	if visited["github-path"] {
		learnGitHubPath = snap.learnGitHubPath
	}
	if visited["github-max"] {
		learnGitHubMax = snap.learnGitHubMax
	}
	if visited["chain"] {
		learnChain = snap.learnChain
	}
}

func patchLearnProfileFromVisited(cmd *cobra.Command, p *LearnProfile) {
	visited := map[string]bool{}
	cmd.Flags().Visit(func(f *pflag.Flag) {
		visited[f.Name] = true
	})
	if len(visited) == 0 {
		return
	}
	cfg := &p.Config
	if visited["file"] {
		cfg.File = strings.TrimSpace(learnFile)
	}
	if visited["dir"] {
		cfg.Dir = strings.TrimSpace(learnDir)
	}
	if visited["namespace"] {
		cfg.Namespace = strings.TrimSpace(learnNamespace)
	}
	if visited["recursive"] {
		cfg.Recursive = learnRecursive
	}
	if visited["extensions"] {
		cfg.Extensions = strings.TrimSpace(learnExtensions)
	}
	if visited["type"] {
		cfg.Types = append([]string(nil), learnTypes...)
	}
	if visited["all-types"] {
		cfg.AllTypes = learnAllTypes
	}
	if visited["chunk-chars"] {
		cfg.ChunkChars = learnChunkChars
	}
	if visited["chunk-overlap"] {
		cfg.ChunkOverlap = learnChunkOverlap
	}
	if visited["url"] {
		cfg.URLs = append([]string(nil), learnURLs...)
	}
	if visited["url-file"] {
		cfg.URLFile = strings.TrimSpace(learnURLFile)
	}
	if visited["crawl"] {
		cfg.Crawl = learnCrawl
	}
	if visited["crawl-depth"] {
		cfg.CrawlDepth = learnCrawlDepth
	}
	if visited["max-pages"] {
		cfg.MaxPages = learnMaxPages
	}
	if visited["rate-limit"] {
		cfg.RateLimit = learnRateLimit
	}
	if visited["allowed-domain"] {
		cfg.AllowedDomains = append([]string(nil), learnAllowedDomains...)
	}
	if visited["user-agent"] {
		cfg.UserAgent = strings.TrimSpace(learnUserAgent)
	}
	if visited["remote-timeout"] {
		cfg.RemoteTimeout = learnRemoteTimeout.String()
	}
	if visited["max-bytes"] {
		cfg.MaxBytes = learnMaxBytes
	}
	if visited["auth-header-env"] {
		cfg.AuthHeaderEnv = strings.TrimSpace(learnAuthHeaderEnv)
	}
	if visited["hf-dataset"] {
		cfg.HFDatasets = append([]string(nil), learnHFDatasets...)
	}
	if visited["hf-config"] {
		cfg.HFConfig = strings.TrimSpace(learnHFConfig)
	}
	if visited["hf-split"] {
		cfg.HFSplit = strings.TrimSpace(learnHFSplit)
	}
	if visited["hf-max-records"] {
		cfg.HFMaxRecords = learnHFMaxRecords
	}
	if visited["kaggle-dataset"] {
		cfg.KaggleDatasets = append([]string(nil), learnKaggleDatasets...)
	}
	if visited["kaggle-file"] {
		cfg.KaggleFiles = append([]string(nil), learnKaggleFiles...)
	}
	if visited["kaggle-max-records"] {
		cfg.KaggleMaxRecords = learnKaggleMaxRecords
	}
	if visited["url-safety"] {
		cfg.URLSafety = learnURLSafety
	}
	if visited["url-safety-timeout"] {
		cfg.URLSafetyTimeout = learnURLSafetyTimeout.String()
	}
	if visited["url-safety-cache-ttl"] {
		cfg.URLSafetyCacheTTL = learnURLSafetyCacheTTL.String()
	}
	if visited["url-safety-visibility"] {
		cfg.URLSafetyVisibility = strings.TrimSpace(learnURLSafetyVisibility)
	}
	if visited["url-safety-fail-open"] {
		cfg.URLSafetyFailOpen = learnURLSafetyFailOpen
	}
	if visited["from-research"] {
		cfg.FromResearch = strings.TrimSpace(learnFromResearch)
	}
	if visited["include-research-summary"] {
		cfg.IncludeResearchSummary = learnIncludeResearchSummary
	}
	if visited["include-research-sources"] {
		cfg.IncludeResearchSources = learnIncludeResearchSources
	}
	if visited["summarize-sources"] {
		cfg.SummarizeSources = learnSummarizeSources
	}
	if visited["summary-max-chars"] {
		cfg.SummaryMaxChars = learnSummaryMaxChars
	}
	if visited["summary-max-points"] {
		cfg.SummaryMaxPoints = learnSummaryMaxPoints
	}
	if visited["summary-model"] {
		cfg.SummaryModel = strings.TrimSpace(learnSummaryModel)
	}
	if visited["title-chunks"] {
		cfg.TitleChunks = learnTitleChunks
	}
	if visited["title-max-chars"] {
		cfg.TitleMaxChars = learnTitleMaxChars
	}
	if visited["title-model"] {
		cfg.TitleModel = strings.TrimSpace(learnTitleModel)
	}
	if visited["incremental"] {
		cfg.Incremental = learnIncremental
	}
	if visited["incremental-manifest"] {
		cfg.IncrementalManifest = strings.TrimSpace(learnIncrementalManifest)
	}
	if visited["gmail-query"] {
		cfg.GmailQuery = strings.TrimSpace(learnGmailQuery)
	}
	if visited["gmail-max"] {
		cfg.GmailMax = learnGmailMax
	}
	if visited["gdrive-folder"] {
		cfg.GdriveFolderID = strings.TrimSpace(learnGdriveFolderID)
	}
	if visited["gdrive-query"] {
		cfg.GdriveQuery = strings.TrimSpace(learnGdriveQuery)
	}
	if visited["gdrive-max"] {
		cfg.GdriveMax = learnGdriveMax
	}
	if visited["notion-database"] {
		cfg.NotionDatabaseID = strings.TrimSpace(learnNotionDatabaseID)
	}
	if visited["notion-filter"] {
		cfg.NotionFilter = strings.TrimSpace(learnNotionFilter)
	}
	if visited["book-search"] {
		cfg.BookSearch = strings.TrimSpace(learnBookSearch)
	}
	if visited["book-id"] {
		cfg.BookIDs = learnBookIDs
	}
	if visited["book-max"] {
		cfg.BookMax = learnBookMax
	}
	if visited["github-repo"] {
		cfg.GitHubRepos = learnGitHubRepos
	}
	if visited["github-path"] {
		cfg.GitHubPath = strings.TrimSpace(learnGitHubPath)
	}
	if visited["github-max"] {
		cfg.GitHubMax = learnGitHubMax
	}
	if visited["chain"] {
		cfg.Chain = strings.TrimSpace(learnChain)
	}
}

func emptyAsNA(v string) string {
	v = strings.TrimSpace(v)
	if v == "" {
		return "n/a"
	}
	return v
}
