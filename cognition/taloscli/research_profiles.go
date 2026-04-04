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

const defaultResearchProfilesPath = ".memory/research_profiles.json"

var (
	researchProfilesPath = defaultResearchProfilesPath

	researchProfileForRun        string
	researchCategoryForRun       string
	researchProfileApplied       string
	researchCategoriesApplied    []string
	researchProfileManageName    string
	researchProfileManageDesc    string
	researchProfileManageQuery   string
	researchProfileManageCats    []string
	researchProfileShowJSON      bool
	researchProfilesListCat      string
	researchProfileExportOut     string
	researchProfileExportNames   []string
	researchProfileImportIn      string
	researchProfileImportMerge   bool
	researchProfileImportReplace bool

	researchProfileCreateCmd       = &cobra.Command{Use: "create", Short: "Create a saved research profile.", Run: runResearchProfileCreate}
	researchProfileUpdateCmd       = &cobra.Command{Use: "update", Short: "Update a saved research profile.", Run: runResearchProfileUpdate}
	researchProfileDeleteCmd       = &cobra.Command{Use: "delete", Short: "Delete a saved research profile.", Run: runResearchProfileDelete}
	researchProfileShowCmd         = &cobra.Command{Use: "show", Short: "Show a saved research profile.", Run: runResearchProfileShow}
	researchProfileListCmd         = &cobra.Command{Use: "list", Short: "List saved research profiles.", Run: runResearchProfileList}
	researchProfileSetDefaultCmd   = &cobra.Command{Use: "set-default", Short: "Set default research profile.", Run: runResearchProfileSetDefault}
	researchProfileClearDefaultCmd = &cobra.Command{Use: "clear-default", Short: "Clear default research profile.", Run: runResearchProfileClearDefault}
	researchProfileExportCmd       = &cobra.Command{Use: "export", Short: "Export learn/research profiles to a unified bundle.", Run: runResearchProfileExport}
	researchProfileImportCmd       = &cobra.Command{Use: "import", Short: "Import learn/research profiles from a unified bundle.", Run: runResearchProfileImport}
)

type ResearchProfileConfig struct {
	MaxPages         int      `json:"max_pages"`
	CrawlDepth       int      `json:"crawl_depth"`
	MaxResearchLoops int      `json:"max_research_loops"`
	MaxPlanSteps     int      `json:"max_plan_steps"`
	Timeout          string   `json:"timeout,omitempty"`
	Verbose          bool     `json:"verbose"`
	SeedURLs         []string `json:"seed_urls,omitempty"`
	QueryTemplate    string   `json:"query_template,omitempty"`
}

type ResearchProfile struct {
	Name        string                `json:"name"`
	Description string                `json:"description,omitempty"`
	Categories  []string              `json:"categories,omitempty"`
	CreatedAt   string                `json:"created_at"`
	UpdatedAt   string                `json:"updated_at"`
	Config      ResearchProfileConfig `json:"config"`
}

type researchProfilesFile struct {
	Version        int               `json:"version"`
	DefaultProfile string            `json:"default_profile,omitempty"`
	Profiles       []ResearchProfile `json:"profiles"`
}

type researchRuntimeSnapshot struct {
	researchRunMaxPages         int
	researchRunCrawlDepth       int
	researchRunMaxResearchLoops int
	researchRunTimeout          time.Duration
	researchRunVerbose          bool
	researchRunSeedURLs         []string

	researchDeepMaxPages         int
	researchDeepCrawlDepth       int
	researchDeepMaxPlanSteps     int
	researchDeepMaxResearchLoops int
	researchDeepTimeout          time.Duration
	researchDeepVerbose          bool
	researchDeepSeedURLs         []string
}

func installResearchProfileCommands() {
	profilesCmd := &cobra.Command{
		Use:   "profiles",
		Short: "Create, manage, and apply reusable research profiles.",
	}

	researchProfileCreateCmd.Flags().StringVar(&researchProfileManageName, "name", "", "Profile name")
	researchProfileCreateCmd.Flags().StringVar(&researchProfileManageDesc, "description", "", "Profile description")
	researchProfileCreateCmd.Flags().StringSliceVar(&researchProfileManageCats, "category", nil, "Category tag(s) for this profile")
	researchProfileCreateCmd.Flags().StringVar(&researchProfileManageQuery, "query-template", "", "Default query template for this profile")
	bindResearchConfigFlags(researchProfileCreateCmd.Flags())

	researchProfileUpdateCmd.Flags().StringVar(&researchProfileManageName, "name", "", "Profile name")
	researchProfileUpdateCmd.Flags().StringVar(&researchProfileManageDesc, "description", "", "Profile description")
	researchProfileUpdateCmd.Flags().StringSliceVar(&researchProfileManageCats, "category", nil, "Category tag(s) for this profile")
	researchProfileUpdateCmd.Flags().StringVar(&researchProfileManageQuery, "query-template", "", "Default query template for this profile")
	bindResearchConfigFlags(researchProfileUpdateCmd.Flags())

	researchProfileDeleteCmd.Flags().StringVar(&researchProfileManageName, "name", "", "Profile name")
	researchProfileShowCmd.Flags().StringVar(&researchProfileManageName, "name", "", "Profile name")
	researchProfileShowCmd.Flags().BoolVar(&researchProfileShowJSON, "json", false, "Render as JSON")
	researchProfileListCmd.Flags().StringVar(&researchProfilesListCat, "category", "", "Filter by category tag")
	researchProfileSetDefaultCmd.Flags().StringVar(&researchProfileManageName, "name", "", "Profile name")
	researchProfileExportCmd.Flags().StringVar(&researchProfileExportOut, "out", "", "Output bundle path")
	researchProfileExportCmd.Flags().StringSliceVar(&researchProfileExportNames, "name", nil, "Optional research profile name filter (repeatable)")
	researchProfileImportCmd.Flags().StringVar(&researchProfileImportIn, "in", "", "Input bundle path")
	researchProfileImportCmd.Flags().BoolVar(&researchProfileImportMerge, "merge", true, "Merge imported profiles (overwrite by name)")
	researchProfileImportCmd.Flags().BoolVar(&researchProfileImportReplace, "replace", false, "Replace local learn/research profiles with imported profiles")

	profilesCmd.AddCommand(researchProfileCreateCmd)
	profilesCmd.AddCommand(researchProfileUpdateCmd)
	profilesCmd.AddCommand(researchProfileDeleteCmd)
	profilesCmd.AddCommand(researchProfileShowCmd)
	profilesCmd.AddCommand(researchProfileListCmd)
	profilesCmd.AddCommand(researchProfileSetDefaultCmd)
	profilesCmd.AddCommand(researchProfileClearDefaultCmd)
	profilesCmd.AddCommand(researchProfileExportCmd)
	profilesCmd.AddCommand(researchProfileImportCmd)
	researchCmd.AddCommand(profilesCmd)
}

func bindResearchConfigFlags(flags *pflag.FlagSet) {
	flags.IntVar(&researchRunMaxPages, "max-pages", 40, "Maximum pages/sources to consider")
	flags.IntVar(&researchRunCrawlDepth, "crawl-depth", 1, "Crawl depth budget hint")
	flags.IntVar(&researchRunMaxResearchLoops, "max-research-loops", 3, "Maximum research/tool loops")
	flags.IntVar(&researchDeepMaxPlanSteps, "max-plan-steps", 6, "Maximum planner steps (deep mode)")
	flags.DurationVar(&researchRunTimeout, "timeout", 120*time.Second, "Total LLM timeout budget")
	flags.BoolVar(&researchRunVerbose, "verbose", false, "Enable verbose research logs")
	flags.StringSliceVar(&researchRunSeedURLs, "seed-url", nil, "Seed URL(s) to prioritize")
}

func resolveResearchProfileForRun(cmd *cobra.Command, mode, query string) (string, error) {
	researchProfileApplied = ""
	researchCategoriesApplied = nil
	name := normalizeResearchProfileName(researchProfileForRun)
	category := normalizeResearchTag(researchCategoryForRun)
	store, err := loadResearchProfilesFile()
	if err != nil {
		return "", err
	}
	if name == "" {
		if category == "" {
			name = normalizeResearchProfileName(store.DefaultProfile)
			if name == "" {
				return strings.TrimSpace(query), nil
			}
		}
	}
	if name == "" {
		matches := make([]ResearchProfile, 0, len(store.Profiles))
		for _, p := range store.Profiles {
			if profileHasCategory(p, category) {
				matches = append(matches, p)
			}
		}
		if len(matches) == 0 {
			return "", fmt.Errorf("no research profile found for category %s", category)
		}
		if len(matches) > 1 {
			names := make([]string, 0, len(matches))
			for _, m := range matches {
				names = append(names, m.Name)
			}
			sort.Strings(names)
			return "", fmt.Errorf("category %s matched multiple profiles; use --profile. matches: %s", category, strings.Join(names, ", "))
		}
		name = normalizeResearchProfileName(matches[0].Name)
	}
	idx := findResearchProfileIndex(store.Profiles, name)
	if idx < 0 {
		return "", fmt.Errorf("research profile not found: %s", name)
	}
	profile := store.Profiles[idx]
	if err := validateResearchProfileConfig(profile.Config); err != nil {
		return "", fmt.Errorf("invalid research profile %s: %w", profile.Name, err)
	}

	if category != "" && !profileHasCategory(profile, category) {
		return "", fmt.Errorf("research profile %s does not include category %s", profile.Name, category)
	}

	snap := captureResearchRuntimeSnapshot()
	applyResearchProfileConfig(mode, profile.Config)
	restoreVisitedResearchFlags(cmd, snap)

	researchProfileApplied = profile.Name
	researchCategoriesApplied = append([]string(nil), profile.Categories...)
	resolvedQuery := strings.TrimSpace(query)
	if resolvedQuery == "" {
		resolvedQuery = strings.TrimSpace(profile.Config.QueryTemplate)
	}
	fmt.Printf("Using research profile: %s\n", profile.Name)
	return resolvedQuery, nil
}

func runResearchProfileCreate(cmd *cobra.Command, args []string) {
	name := normalizeResearchProfileName(researchProfileManageName)
	if name == "" {
		fmt.Println("Error: --name is required")
		return
	}
	store, err := loadResearchProfilesFile()
	if err != nil {
		fmt.Printf("Error loading research profiles: %v\n", err)
		return
	}
	if findResearchProfileIndex(store.Profiles, name) >= 0 {
		fmt.Printf("Error: profile already exists: %s\n", name)
		return
	}
	cfg := currentResearchProfileConfigFromGlobals()
	cfg.QueryTemplate = strings.TrimSpace(researchProfileManageQuery)
	if err := validateResearchProfileConfig(cfg); err != nil {
		fmt.Printf("Error: invalid profile config: %v\n", err)
		return
	}
	now := time.Now().UTC().Format(time.RFC3339)
	store.Profiles = append(store.Profiles, ResearchProfile{
		Name:        name,
		Description: strings.TrimSpace(researchProfileManageDesc),
		Categories:  normalizeResearchTags(researchProfileManageCats),
		CreatedAt:   now,
		UpdatedAt:   now,
		Config:      cfg,
	})
	if err := saveResearchProfilesFile(store); err != nil {
		fmt.Printf("Error saving research profiles: %v\n", err)
		return
	}
	fmt.Println("Research profile created.")
	fmt.Printf("  name: %s\n", name)
}

func runResearchProfileUpdate(cmd *cobra.Command, args []string) {
	name := normalizeResearchProfileName(researchProfileManageName)
	if name == "" {
		fmt.Println("Error: --name is required")
		return
	}
	store, err := loadResearchProfilesFile()
	if err != nil {
		fmt.Printf("Error loading research profiles: %v\n", err)
		return
	}
	idx := findResearchProfileIndex(store.Profiles, name)
	if idx < 0 {
		fmt.Printf("Error: profile not found: %s\n", name)
		return
	}
	profile := store.Profiles[idx]
	patchResearchProfileFromVisited(cmd, &profile)
	if cmd.Flags().Changed("description") {
		profile.Description = strings.TrimSpace(researchProfileManageDesc)
	}
	if cmd.Flags().Changed("query-template") {
		profile.Config.QueryTemplate = strings.TrimSpace(researchProfileManageQuery)
	}
	if cmd.Flags().Changed("category") {
		profile.Categories = normalizeResearchTags(researchProfileManageCats)
	}
	profile.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	if err := validateResearchProfileConfig(profile.Config); err != nil {
		fmt.Printf("Error: invalid profile config: %v\n", err)
		return
	}
	store.Profiles[idx] = profile
	if err := saveResearchProfilesFile(store); err != nil {
		fmt.Printf("Error saving research profiles: %v\n", err)
		return
	}
	fmt.Println("Research profile updated.")
	fmt.Printf("  name: %s\n", profile.Name)
}

func runResearchProfileDelete(cmd *cobra.Command, args []string) {
	name := normalizeResearchProfileName(researchProfileManageName)
	if name == "" {
		fmt.Println("Error: --name is required")
		return
	}
	store, err := loadResearchProfilesFile()
	if err != nil {
		fmt.Printf("Error loading research profiles: %v\n", err)
		return
	}
	idx := findResearchProfileIndex(store.Profiles, name)
	if idx < 0 {
		fmt.Printf("Error: profile not found: %s\n", name)
		return
	}
	store.Profiles = append(store.Profiles[:idx], store.Profiles[idx+1:]...)
	if strings.EqualFold(strings.TrimSpace(store.DefaultProfile), name) {
		store.DefaultProfile = ""
	}
	if err := saveResearchProfilesFile(store); err != nil {
		fmt.Printf("Error saving research profiles: %v\n", err)
		return
	}
	fmt.Println("Research profile deleted.")
	fmt.Printf("  name: %s\n", name)
}

func runResearchProfileShow(cmd *cobra.Command, args []string) {
	name := normalizeResearchProfileName(researchProfileManageName)
	if name == "" {
		fmt.Println("Error: --name is required")
		return
	}
	store, err := loadResearchProfilesFile()
	if err != nil {
		fmt.Printf("Error loading research profiles: %v\n", err)
		return
	}
	idx := findResearchProfileIndex(store.Profiles, name)
	if idx < 0 {
		fmt.Printf("Error: profile not found: %s\n", name)
		return
	}
	p := store.Profiles[idx]
	if researchProfileShowJSON {
		b, _ := json.MarshalIndent(p, "", "  ")
		fmt.Println(string(b))
		return
	}
	fmt.Println("RESEARCH PROFILE")
	fmt.Printf("\nNAME\n  %s\n", p.Name)
	fmt.Printf("\nDESCRIPTION\n  %s\n", emptyAsNA(p.Description))
	fmt.Printf("\nCATEGORIES\n  %s\n", emptyAsNA(strings.Join(p.Categories, ", ")))
	fmt.Printf("\nTIMESTAMPS\n  created_at: %s\n  updated_at: %s\n", emptyAsNA(p.CreatedAt), emptyAsNA(p.UpdatedAt))
	b, _ := json.MarshalIndent(p.Config, "", "  ")
	fmt.Printf("\nCONFIG\n%s\n", string(b))
}

func runResearchProfileList(cmd *cobra.Command, args []string) {
	store, err := loadResearchProfilesFile()
	if err != nil {
		fmt.Printf("Error loading research profiles: %v\n", err)
		return
	}
	filterCategory := normalizeResearchTag(researchProfilesListCat)
	profiles := make([]ResearchProfile, 0, len(store.Profiles))
	for _, p := range store.Profiles {
		if filterCategory == "" || profileHasCategory(p, filterCategory) {
			profiles = append(profiles, p)
		}
	}
	sort.SliceStable(profiles, func(i, j int) bool {
		return strings.ToLower(profiles[i].Name) < strings.ToLower(profiles[j].Name)
	})

	fmt.Println("RESEARCH PROFILES")
	fmt.Printf("\nSUMMARY\n  count: %d\n", len(profiles))
	fmt.Printf("  default: %s\n", emptyAsNA(store.DefaultProfile))
	fmt.Printf("  filter_category: %s\n", emptyAsNA(filterCategory))
	fmt.Println("\nPROFILES")
	if len(profiles) == 0 {
		fmt.Println("  No profiles found.")
		return
	}
	for i, p := range profiles {
		mark := ""
		if strings.EqualFold(strings.TrimSpace(store.DefaultProfile), p.Name) {
			mark = " (default)"
		}
		fmt.Printf("  %d. %s%s\n", i+1, p.Name, mark)
		fmt.Printf("     desc: %s\n", emptyAsNA(p.Description))
		fmt.Printf("     categories: %s\n", emptyAsNA(strings.Join(p.Categories, ", ")))
		fmt.Printf("     updated: %s\n", emptyAsNA(p.UpdatedAt))
	}
}

func runResearchProfileSetDefault(cmd *cobra.Command, args []string) {
	name := normalizeResearchProfileName(researchProfileManageName)
	if name == "" {
		fmt.Println("Error: --name is required")
		return
	}
	store, err := loadResearchProfilesFile()
	if err != nil {
		fmt.Printf("Error loading research profiles: %v\n", err)
		return
	}
	if findResearchProfileIndex(store.Profiles, name) < 0 {
		fmt.Printf("Error: profile not found: %s\n", name)
		return
	}
	store.DefaultProfile = name
	if err := saveResearchProfilesFile(store); err != nil {
		fmt.Printf("Error saving research profiles: %v\n", err)
		return
	}
	fmt.Println("Default research profile set.")
	fmt.Printf("  name: %s\n", name)
}

func runResearchProfileClearDefault(cmd *cobra.Command, args []string) {
	store, err := loadResearchProfilesFile()
	if err != nil {
		fmt.Printf("Error loading research profiles: %v\n", err)
		return
	}
	store.DefaultProfile = ""
	if err := saveResearchProfilesFile(store); err != nil {
		fmt.Printf("Error saving research profiles: %v\n", err)
		return
	}
	fmt.Println("Default research profile cleared.")
}

func runResearchProfileExport(cmd *cobra.Command, args []string) {
	bundle, err := buildProfileBundle(nil, researchProfileExportNames)
	if err != nil {
		fmt.Printf("Error building profile bundle: %v\n", err)
		return
	}
	out := strings.TrimSpace(researchProfileExportOut)
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

func runResearchProfileImport(cmd *cobra.Command, args []string) {
	in := strings.TrimSpace(researchProfileImportIn)
	if in == "" {
		fmt.Println("Error: --in is required")
		return
	}
	if researchProfileImportMerge && researchProfileImportReplace {
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
	if researchProfileImportReplace {
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

func currentResearchProfileConfigFromGlobals() ResearchProfileConfig {
	return ResearchProfileConfig{
		MaxPages:         researchRunMaxPages,
		CrawlDepth:       researchRunCrawlDepth,
		MaxResearchLoops: researchRunMaxResearchLoops,
		MaxPlanSteps:     researchDeepMaxPlanSteps,
		Timeout:          researchRunTimeout.String(),
		Verbose:          researchRunVerbose,
		SeedURLs:         append([]string(nil), researchRunSeedURLs...),
	}
}

func applyResearchProfileConfig(mode string, cfg ResearchProfileConfig) {
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode == "deep" {
		researchDeepMaxPages = cfg.MaxPages
		researchDeepCrawlDepth = cfg.CrawlDepth
		researchDeepMaxResearchLoops = cfg.MaxResearchLoops
		researchDeepMaxPlanSteps = cfg.MaxPlanSteps
		researchDeepVerbose = cfg.Verbose
		researchDeepSeedURLs = append([]string(nil), cfg.SeedURLs...)
		if d, err := time.ParseDuration(strings.TrimSpace(cfg.Timeout)); err == nil && d > 0 {
			researchDeepTimeout = d
		}
		return
	}
	researchRunMaxPages = cfg.MaxPages
	researchRunCrawlDepth = cfg.CrawlDepth
	researchRunMaxResearchLoops = cfg.MaxResearchLoops
	researchRunVerbose = cfg.Verbose
	researchRunSeedURLs = append([]string(nil), cfg.SeedURLs...)
	if d, err := time.ParseDuration(strings.TrimSpace(cfg.Timeout)); err == nil && d > 0 {
		researchRunTimeout = d
	}
}

func validateResearchProfileConfig(cfg ResearchProfileConfig) error {
	if cfg.MaxPages <= 0 {
		return fmt.Errorf("max_pages must be > 0")
	}
	if cfg.CrawlDepth < 0 {
		return fmt.Errorf("crawl_depth must be >= 0")
	}
	if cfg.MaxResearchLoops <= 0 {
		return fmt.Errorf("max_research_loops must be > 0")
	}
	if cfg.MaxPlanSteps <= 0 {
		return fmt.Errorf("max_plan_steps must be > 0")
	}
	if strings.TrimSpace(cfg.Timeout) != "" {
		d, err := time.ParseDuration(strings.TrimSpace(cfg.Timeout))
		if err != nil || d <= 0 {
			return fmt.Errorf("timeout must be a positive duration")
		}
	}
	if len(cfg.SeedURLs) > 0 {
		clean := 0
		for _, u := range cfg.SeedURLs {
			if strings.TrimSpace(u) != "" {
				clean++
			}
		}
		if clean == 0 {
			return fmt.Errorf("seed_urls must include at least one non-empty URL when set")
		}
	}
	return nil
}

func loadResearchProfilesFile() (researchProfilesFile, error) {
	store := researchProfilesFile{Version: 1, Profiles: []ResearchProfile{}}
	b, err := os.ReadFile(researchProfilesPath)
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
		store.Profiles = []ResearchProfile{}
	}
	if findResearchProfileIndex(store.Profiles, store.DefaultProfile) < 0 {
		store.DefaultProfile = ""
	}
	return store, nil
}

func saveResearchProfilesFile(store researchProfilesFile) error {
	if store.Version <= 0 {
		store.Version = 1
	}
	if store.Profiles == nil {
		store.Profiles = []ResearchProfile{}
	}
	if findResearchProfileIndex(store.Profiles, store.DefaultProfile) < 0 {
		store.DefaultProfile = ""
	}
	if err := os.MkdirAll(filepath.Dir(researchProfilesPath), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(store, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(researchProfilesPath, b, 0o644)
}

func findResearchProfileIndex(profiles []ResearchProfile, name string) int {
	name = normalizeResearchProfileName(name)
	for i := range profiles {
		if normalizeResearchProfileName(profiles[i].Name) == name {
			return i
		}
	}
	return -1
}

func normalizeResearchProfileName(v string) string {
	v = strings.TrimSpace(strings.ToLower(v))
	v = strings.ReplaceAll(v, " ", "-")
	return v
}

func normalizeResearchTag(v string) string {
	v = strings.TrimSpace(strings.ToLower(v))
	v = strings.ReplaceAll(v, " ", "-")
	return v
}

func normalizeResearchTags(tags []string) []string {
	if len(tags) == 0 {
		return nil
	}
	seen := map[string]bool{}
	out := make([]string, 0, len(tags))
	for _, t := range tags {
		t = normalizeResearchTag(t)
		if t == "" || seen[t] {
			continue
		}
		seen[t] = true
		out = append(out, t)
	}
	sort.Strings(out)
	if len(out) == 0 {
		return nil
	}
	return out
}

func profileHasCategory(p ResearchProfile, category string) bool {
	if category == "" {
		return true
	}
	for _, tag := range p.Categories {
		if normalizeResearchTag(tag) == category {
			return true
		}
	}
	return false
}

func captureResearchRuntimeSnapshot() researchRuntimeSnapshot {
	return researchRuntimeSnapshot{
		researchRunMaxPages:          researchRunMaxPages,
		researchRunCrawlDepth:        researchRunCrawlDepth,
		researchRunMaxResearchLoops:  researchRunMaxResearchLoops,
		researchRunTimeout:           researchRunTimeout,
		researchRunVerbose:           researchRunVerbose,
		researchRunSeedURLs:          append([]string(nil), researchRunSeedURLs...),
		researchDeepMaxPages:         researchDeepMaxPages,
		researchDeepCrawlDepth:       researchDeepCrawlDepth,
		researchDeepMaxPlanSteps:     researchDeepMaxPlanSteps,
		researchDeepMaxResearchLoops: researchDeepMaxResearchLoops,
		researchDeepTimeout:          researchDeepTimeout,
		researchDeepVerbose:          researchDeepVerbose,
		researchDeepSeedURLs:         append([]string(nil), researchDeepSeedURLs...),
	}
}

func restoreVisitedResearchFlags(cmd *cobra.Command, snap researchRuntimeSnapshot) {
	visited := map[string]bool{}
	cmd.Flags().Visit(func(f *pflag.Flag) {
		visited[f.Name] = true
	})
	if len(visited) == 0 {
		return
	}
	if visited["max-pages"] {
		if strings.EqualFold(cmd.Name(), "deep") {
			researchDeepMaxPages = snap.researchDeepMaxPages
		} else {
			researchRunMaxPages = snap.researchRunMaxPages
		}
	}
	if visited["crawl-depth"] {
		if strings.EqualFold(cmd.Name(), "deep") {
			researchDeepCrawlDepth = snap.researchDeepCrawlDepth
		} else {
			researchRunCrawlDepth = snap.researchRunCrawlDepth
		}
	}
	if visited["max-research-loops"] {
		if strings.EqualFold(cmd.Name(), "deep") {
			researchDeepMaxResearchLoops = snap.researchDeepMaxResearchLoops
		} else {
			researchRunMaxResearchLoops = snap.researchRunMaxResearchLoops
		}
	}
	if visited["max-plan-steps"] {
		researchDeepMaxPlanSteps = snap.researchDeepMaxPlanSteps
	}
	if visited["timeout"] {
		if strings.EqualFold(cmd.Name(), "deep") {
			researchDeepTimeout = snap.researchDeepTimeout
		} else {
			researchRunTimeout = snap.researchRunTimeout
		}
	}
	if visited["verbose"] {
		if strings.EqualFold(cmd.Name(), "deep") {
			researchDeepVerbose = snap.researchDeepVerbose
		} else {
			researchRunVerbose = snap.researchRunVerbose
		}
	}
	if visited["seed-url"] {
		if strings.EqualFold(cmd.Name(), "deep") {
			researchDeepSeedURLs = append([]string(nil), snap.researchDeepSeedURLs...)
		} else {
			researchRunSeedURLs = append([]string(nil), snap.researchRunSeedURLs...)
		}
	}
}

func patchResearchProfileFromVisited(cmd *cobra.Command, p *ResearchProfile) {
	visited := map[string]bool{}
	cmd.Flags().Visit(func(f *pflag.Flag) {
		visited[f.Name] = true
	})
	if len(visited) == 0 {
		return
	}
	cfg := &p.Config
	if visited["max-pages"] {
		cfg.MaxPages = researchRunMaxPages
	}
	if visited["crawl-depth"] {
		cfg.CrawlDepth = researchRunCrawlDepth
	}
	if visited["max-research-loops"] {
		cfg.MaxResearchLoops = researchRunMaxResearchLoops
	}
	if visited["max-plan-steps"] {
		cfg.MaxPlanSteps = researchDeepMaxPlanSteps
	}
	if visited["timeout"] {
		cfg.Timeout = researchRunTimeout.String()
	}
	if visited["verbose"] {
		cfg.Verbose = researchRunVerbose
	}
	if visited["seed-url"] {
		cfg.SeedURLs = append([]string(nil), researchRunSeedURLs...)
	}
}
