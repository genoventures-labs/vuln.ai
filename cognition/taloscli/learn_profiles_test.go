package taloscli

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/spf13/cobra"
)

func TestLearnProfilesFileRoundtrip(t *testing.T) {
	oldPath := learnProfilesPath
	learnProfilesPath = filepath.Join(t.TempDir(), "learn_profiles.json")
	defer func() { learnProfilesPath = oldPath }()

	store := learnProfilesFile{
		Version:        1,
		DefaultProfile: "hf-default",
		Profiles: []LearnProfile{
			{
				Name:        "hf-default",
				Description: "test",
				CreatedAt:   time.Now().UTC().Format(time.RFC3339),
				UpdatedAt:   time.Now().UTC().Format(time.RFC3339),
				Config: LearnProfileConfig{
					ChunkChars:             1200,
					ChunkOverlap:           200,
					MaxPages:               200,
					RateLimit:              2.0,
					MaxBytes:               10 * 1024 * 1024,
					HFMaxRecords:           100,
					URLSafetyVisibility:    "private",
					Recursive:              true,
					RemoteTimeout:          "20s",
					URLSafetyTimeout:       "45s",
					URLSafetyCacheTTL:      "24h",
					IncludeResearchSources: true,
					IncludeResearchSummary: true,
				},
			},
		},
	}
	if err := saveLearnProfilesFile(store); err != nil {
		t.Fatalf("save profiles: %v", err)
	}
	loaded, err := loadLearnProfilesFile()
	if err != nil {
		t.Fatalf("load profiles: %v", err)
	}
	if loaded.DefaultProfile != "hf-default" {
		t.Fatalf("expected default profile hf-default, got %q", loaded.DefaultProfile)
	}
	if len(loaded.Profiles) != 1 || loaded.Profiles[0].Name != "hf-default" {
		t.Fatalf("unexpected loaded profiles: %#v", loaded.Profiles)
	}
}

func TestResolveLearnProfileForRunRespectsExplicitFlagOverrides(t *testing.T) {
	oldPath := learnProfilesPath
	oldProfile := learnProfile
	oldNamespace := learnNamespace
	defer func() {
		learnProfilesPath = oldPath
		learnProfile = oldProfile
		learnNamespace = oldNamespace
	}()
	learnProfilesPath = filepath.Join(t.TempDir(), "learn_profiles.json")

	store := learnProfilesFile{
		Version: 1,
		Profiles: []LearnProfile{{
			Name:      "hf-default",
			CreatedAt: time.Now().UTC().Format(time.RFC3339),
			UpdatedAt: time.Now().UTC().Format(time.RFC3339),
			Config: LearnProfileConfig{
				ChunkChars:             1200,
				ChunkOverlap:           200,
				Namespace:              "profile-ns",
				MaxPages:               200,
				RateLimit:              2.0,
				MaxBytes:               10 * 1024 * 1024,
				HFMaxRecords:           100,
				URLSafetyVisibility:    "private",
				Recursive:              true,
				RemoteTimeout:          "20s",
				URLSafetyTimeout:       "45s",
				URLSafetyCacheTTL:      "24h",
				IncludeResearchSources: true,
				IncludeResearchSummary: true,
			}},
		},
	}
	if err := saveLearnProfilesFile(store); err != nil {
		t.Fatalf("save profiles: %v", err)
	}

	learnProfile = "hf-default"
	cmd := &cobra.Command{Use: "learn"}
	bindLearnConfigFlags(cmd.Flags())
	if err := cmd.Flags().Set("hf-max-records", "250"); err != nil {
		t.Fatalf("set explicit flag: %v", err)
	}
	if err := cmd.Flags().Set("chunk-chars", "1500"); err != nil {
		t.Fatalf("set explicit flag: %v", err)
	}
	if err := cmd.Flags().Set("namespace", "explicit-ns"); err != nil {
		t.Fatalf("set explicit namespace flag: %v", err)
	}

	if _, err := resolveLearnProfileForRun(cmd); err != nil {
		t.Fatalf("resolve profile: %v", err)
	}
	if learnHFMaxRecords != 250 {
		t.Fatalf("expected explicit hf-max-records override (250), got %d", learnHFMaxRecords)
	}
	if learnChunkChars != 1500 {
		t.Fatalf("expected explicit chunk-chars override (1500), got %d", learnChunkChars)
	}
	if learnNamespace != "explicit-ns" {
		t.Fatalf("expected explicit namespace override (explicit-ns), got %q", learnNamespace)
	}
}

func TestValidateLearnProfileConfigRejectsInvalid(t *testing.T) {
	cfg := LearnProfileConfig{ChunkChars: 100, ChunkOverlap: 100, MaxPages: 1, RateLimit: 1, MaxBytes: 1, HFMaxRecords: 1, URLSafetyVisibility: "private"}
	if err := validateLearnProfileConfig(cfg); err == nil {
		t.Fatal("expected validation error for chunk overlap")
	}
	cfg = LearnProfileConfig{ChunkChars: 100, ChunkOverlap: 10, MaxPages: 1, RateLimit: 1, MaxBytes: 1, HFMaxRecords: 1, URLSafetyVisibility: "invalid"}
	if err := validateLearnProfileConfig(cfg); err == nil {
		t.Fatal("expected validation error for url safety visibility")
	}
	cfg = LearnProfileConfig{ChunkChars: 100, ChunkOverlap: 10, MaxPages: 1, RateLimit: 1, MaxBytes: 1, HFMaxRecords: 1, URLSafetyVisibility: "private", TitleMaxChars: -1}
	if err := validateLearnProfileConfig(cfg); err == nil {
		t.Fatal("expected validation error for title_max_chars")
	}
}

func TestLoadLearnProfilesFileMissing(t *testing.T) {
	oldPath := learnProfilesPath
	learnProfilesPath = filepath.Join(t.TempDir(), "missing.json")
	defer func() { learnProfilesPath = oldPath }()

	store, err := loadLearnProfilesFile()
	if err != nil {
		t.Fatalf("expected no error for missing file: %v", err)
	}
	if store.Version != 1 {
		t.Fatalf("expected default version 1, got %d", store.Version)
	}
	if len(store.Profiles) != 0 {
		t.Fatalf("expected empty profiles, got %d", len(store.Profiles))
	}
	if _, err := os.Stat(learnProfilesPath); err == nil {
		t.Fatalf("did not expect missing file to be auto-created")
	}
}
