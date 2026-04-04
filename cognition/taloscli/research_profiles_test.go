package taloscli

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/spf13/cobra"
)

func TestResearchProfilesFileRoundtrip(t *testing.T) {
	oldPath := researchProfilesPath
	researchProfilesPath = filepath.Join(t.TempDir(), "research_profiles.json")
	defer func() { researchProfilesPath = oldPath }()

	store := researchProfilesFile{
		Version: 1,
		Profiles: []ResearchProfile{{
			Name:        "market-scan",
			Description: "daily market scan",
			Categories:  []string{"crypto", "daily"},
			CreatedAt:   time.Now().UTC().Format(time.RFC3339),
			UpdatedAt:   time.Now().UTC().Format(time.RFC3339),
			Config: ResearchProfileConfig{
				MaxPages:         75,
				CrawlDepth:       2,
				MaxResearchLoops: 5,
				MaxPlanSteps:     8,
				Timeout:          "90s",
				Verbose:          false,
				SeedURLs:         []string{"https://example.com"},
				QueryTemplate:    "weekly market outlook",
			},
		}},
	}
	if err := saveResearchProfilesFile(store); err != nil {
		t.Fatalf("save profiles: %v", err)
	}
	loaded, err := loadResearchProfilesFile()
	if err != nil {
		t.Fatalf("load profiles: %v", err)
	}
	if len(loaded.Profiles) != 1 || loaded.Profiles[0].Name != "market-scan" {
		t.Fatalf("unexpected loaded profiles: %#v", loaded.Profiles)
	}
}

func TestResolveResearchProfileForRunRespectsExplicitFlagOverrides(t *testing.T) {
	oldPath := researchProfilesPath
	oldProfile := researchProfileForRun
	oldCategory := researchCategoryForRun
	defer func() {
		researchProfilesPath = oldPath
		researchProfileForRun = oldProfile
		researchCategoryForRun = oldCategory
	}()
	researchProfilesPath = filepath.Join(t.TempDir(), "research_profiles.json")

	store := researchProfilesFile{
		Version: 1,
		Profiles: []ResearchProfile{{
			Name:       "market-scan",
			Categories: []string{"crypto"},
			CreatedAt:  time.Now().UTC().Format(time.RFC3339),
			UpdatedAt:  time.Now().UTC().Format(time.RFC3339),
			Config: ResearchProfileConfig{
				MaxPages:         75,
				CrawlDepth:       2,
				MaxResearchLoops: 5,
				MaxPlanSteps:     8,
				Timeout:          "90s",
				SeedURLs:         []string{"https://seed.example"},
				QueryTemplate:    "from profile",
			},
		}},
	}
	if err := saveResearchProfilesFile(store); err != nil {
		t.Fatalf("save profiles: %v", err)
	}

	researchProfileForRun = "market-scan"
	researchCategoryForRun = "crypto"

	cmd := &cobra.Command{Use: "run"}
	cmd.Flags().IntVar(&researchRunMaxPages, "max-pages", 40, "")
	cmd.Flags().IntVar(&researchRunCrawlDepth, "crawl-depth", 1, "")
	cmd.Flags().IntVar(&researchRunMaxResearchLoops, "max-research-loops", 3, "")
	cmd.Flags().DurationVar(&researchRunTimeout, "timeout", 120*time.Second, "")
	cmd.Flags().BoolVar(&researchRunVerbose, "verbose", false, "")
	cmd.Flags().StringSliceVar(&researchRunSeedURLs, "seed-url", nil, "")
	if err := cmd.Flags().Set("max-pages", "20"); err != nil {
		t.Fatalf("set explicit max-pages: %v", err)
	}
	if err := cmd.Flags().Set("seed-url", "https://manual.example"); err != nil {
		t.Fatalf("set explicit seed-url: %v", err)
	}

	resolved, err := resolveResearchProfileForRun(cmd, "run", "")
	if err != nil {
		t.Fatalf("resolve profile: %v", err)
	}
	if resolved != "from profile" {
		t.Fatalf("expected profile query template, got %q", resolved)
	}
	if researchRunMaxPages != 20 {
		t.Fatalf("expected explicit max-pages override (20), got %d", researchRunMaxPages)
	}
	if len(researchRunSeedURLs) != 1 || researchRunSeedURLs[0] != "https://manual.example" {
		t.Fatalf("expected explicit seed-url override, got %#v", researchRunSeedURLs)
	}
}

func TestValidateResearchProfileConfigRejectsInvalid(t *testing.T) {
	cfg := ResearchProfileConfig{MaxPages: 0, CrawlDepth: 1, MaxResearchLoops: 1, MaxPlanSteps: 1, Timeout: "30s"}
	if err := validateResearchProfileConfig(cfg); err == nil {
		t.Fatal("expected validation error for max_pages")
	}
	cfg = ResearchProfileConfig{MaxPages: 1, CrawlDepth: 1, MaxResearchLoops: 1, MaxPlanSteps: 1, Timeout: "0s"}
	if err := validateResearchProfileConfig(cfg); err == nil {
		t.Fatal("expected validation error for timeout")
	}
}

func TestResolveResearchProfileByCategorySingleMatch(t *testing.T) {
	oldPath := researchProfilesPath
	oldProfile := researchProfileForRun
	oldCategory := researchCategoryForRun
	defer func() {
		researchProfilesPath = oldPath
		researchProfileForRun = oldProfile
		researchCategoryForRun = oldCategory
	}()
	researchProfilesPath = filepath.Join(t.TempDir(), "research_profiles.json")

	store := researchProfilesFile{
		Version: 1,
		Profiles: []ResearchProfile{
			{
				Name:       "market-scan",
				Categories: []string{"crypto"},
				CreatedAt:  time.Now().UTC().Format(time.RFC3339),
				UpdatedAt:  time.Now().UTC().Format(time.RFC3339),
				Config: ResearchProfileConfig{
					MaxPages:         30,
					CrawlDepth:       1,
					MaxResearchLoops: 2,
					MaxPlanSteps:     6,
					Timeout:          "60s",
					QueryTemplate:    "template query",
				},
			},
		},
	}
	if err := saveResearchProfilesFile(store); err != nil {
		t.Fatalf("save profiles: %v", err)
	}

	researchProfileForRun = ""
	researchCategoryForRun = "crypto"
	cmd := &cobra.Command{Use: "run"}
	cmd.Flags().IntVar(&researchRunMaxPages, "max-pages", 40, "")
	cmd.Flags().IntVar(&researchRunCrawlDepth, "crawl-depth", 1, "")
	cmd.Flags().IntVar(&researchRunMaxResearchLoops, "max-research-loops", 3, "")
	cmd.Flags().DurationVar(&researchRunTimeout, "timeout", 120*time.Second, "")
	cmd.Flags().BoolVar(&researchRunVerbose, "verbose", false, "")
	cmd.Flags().StringSliceVar(&researchRunSeedURLs, "seed-url", nil, "")

	resolved, err := resolveResearchProfileForRun(cmd, "run", "")
	if err != nil {
		t.Fatalf("resolve profile by category: %v", err)
	}
	if resolved != "template query" {
		t.Fatalf("expected query template, got %q", resolved)
	}
	if researchProfileApplied != "market-scan" {
		t.Fatalf("expected applied profile market-scan, got %q", researchProfileApplied)
	}
}

func TestResolveResearchProfileByCategoryAmbiguous(t *testing.T) {
	oldPath := researchProfilesPath
	oldProfile := researchProfileForRun
	oldCategory := researchCategoryForRun
	defer func() {
		researchProfilesPath = oldPath
		researchProfileForRun = oldProfile
		researchCategoryForRun = oldCategory
	}()
	researchProfilesPath = filepath.Join(t.TempDir(), "research_profiles.json")

	now := time.Now().UTC().Format(time.RFC3339)
	store := researchProfilesFile{
		Version: 1,
		Profiles: []ResearchProfile{
			{
				Name:       "alpha",
				Categories: []string{"shared"},
				CreatedAt:  now,
				UpdatedAt:  now,
				Config: ResearchProfileConfig{
					MaxPages: 1, CrawlDepth: 0, MaxResearchLoops: 1, MaxPlanSteps: 1, Timeout: "30s",
				},
			},
			{
				Name:       "beta",
				Categories: []string{"shared"},
				CreatedAt:  now,
				UpdatedAt:  now,
				Config: ResearchProfileConfig{
					MaxPages: 1, CrawlDepth: 0, MaxResearchLoops: 1, MaxPlanSteps: 1, Timeout: "30s",
				},
			},
		},
	}
	if err := saveResearchProfilesFile(store); err != nil {
		t.Fatalf("save profiles: %v", err)
	}

	researchProfileForRun = ""
	researchCategoryForRun = "shared"
	cmd := &cobra.Command{Use: "run"}
	cmd.Flags().IntVar(&researchRunMaxPages, "max-pages", 40, "")
	cmd.Flags().IntVar(&researchRunCrawlDepth, "crawl-depth", 1, "")
	cmd.Flags().IntVar(&researchRunMaxResearchLoops, "max-research-loops", 3, "")
	cmd.Flags().DurationVar(&researchRunTimeout, "timeout", 120*time.Second, "")
	cmd.Flags().BoolVar(&researchRunVerbose, "verbose", false, "")
	cmd.Flags().StringSliceVar(&researchRunSeedURLs, "seed-url", nil, "")

	if _, err := resolveResearchProfileForRun(cmd, "run", "query"); err == nil {
		t.Fatal("expected error for ambiguous category")
	}
}

func TestLoadResearchProfilesFileMissing(t *testing.T) {
	oldPath := researchProfilesPath
	researchProfilesPath = filepath.Join(t.TempDir(), "missing.json")
	defer func() { researchProfilesPath = oldPath }()

	store, err := loadResearchProfilesFile()
	if err != nil {
		t.Fatalf("expected no error for missing file: %v", err)
	}
	if store.Version != 1 {
		t.Fatalf("expected default version 1, got %d", store.Version)
	}
	if len(store.Profiles) != 0 {
		t.Fatalf("expected empty profiles, got %d", len(store.Profiles))
	}
	if _, err := os.Stat(researchProfilesPath); err == nil {
		t.Fatalf("did not expect missing file to be auto-created")
	}
}

func TestResolveResearchProfileUsesDefaultWhenUnset(t *testing.T) {
	oldPath := researchProfilesPath
	oldProfile := researchProfileForRun
	oldCategory := researchCategoryForRun
	defer func() {
		researchProfilesPath = oldPath
		researchProfileForRun = oldProfile
		researchCategoryForRun = oldCategory
	}()
	researchProfilesPath = filepath.Join(t.TempDir(), "research_profiles.json")

	now := time.Now().UTC().Format(time.RFC3339)
	store := researchProfilesFile{
		Version:        1,
		DefaultProfile: "daily-default",
		Profiles: []ResearchProfile{{
			Name:      "daily-default",
			CreatedAt: now,
			UpdatedAt: now,
			Config: ResearchProfileConfig{
				MaxPages:         10,
				CrawlDepth:       1,
				MaxResearchLoops: 2,
				MaxPlanSteps:     3,
				Timeout:          "60s",
				QueryTemplate:    "default query",
			},
		}},
	}
	if err := saveResearchProfilesFile(store); err != nil {
		t.Fatalf("save profiles: %v", err)
	}

	researchProfileForRun = ""
	researchCategoryForRun = ""
	cmd := &cobra.Command{Use: "run"}
	cmd.Flags().IntVar(&researchRunMaxPages, "max-pages", 40, "")
	cmd.Flags().IntVar(&researchRunCrawlDepth, "crawl-depth", 1, "")
	cmd.Flags().IntVar(&researchRunMaxResearchLoops, "max-research-loops", 3, "")
	cmd.Flags().DurationVar(&researchRunTimeout, "timeout", 120*time.Second, "")
	cmd.Flags().BoolVar(&researchRunVerbose, "verbose", false, "")
	cmd.Flags().StringSliceVar(&researchRunSeedURLs, "seed-url", nil, "")

	resolved, err := resolveResearchProfileForRun(cmd, "run", "")
	if err != nil {
		t.Fatalf("resolve profile: %v", err)
	}
	if resolved != "default query" {
		t.Fatalf("expected default profile query, got %q", resolved)
	}
}
