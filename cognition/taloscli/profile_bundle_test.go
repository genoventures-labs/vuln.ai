package taloscli

import (
	"path/filepath"
	"testing"
	"time"
)

func TestProfileBundleRoundTrip(t *testing.T) {
	oldLearn := learnProfilesPath
	oldResearch := researchProfilesPath
	learnProfilesPath = filepath.Join(t.TempDir(), "learn_profiles.json")
	researchProfilesPath = filepath.Join(t.TempDir(), "research_profiles.json")
	defer func() {
		learnProfilesPath = oldLearn
		researchProfilesPath = oldResearch
	}()

	now := time.Now().UTC().Format(time.RFC3339)
	if err := saveLearnProfilesFile(learnProfilesFile{Version: 1, DefaultProfile: "l1", Profiles: []LearnProfile{{
		Name: "l1", CreatedAt: now, UpdatedAt: now,
		Config: LearnProfileConfig{ChunkChars: 100, ChunkOverlap: 10, MaxPages: 1, RateLimit: 1, MaxBytes: 1, HFMaxRecords: 1, URLSafetyVisibility: "private"},
	}}}); err != nil {
		t.Fatalf("save learn: %v", err)
	}
	if err := saveResearchProfilesFile(researchProfilesFile{Version: 1, DefaultProfile: "r1", Profiles: []ResearchProfile{{
		Name: "r1", CreatedAt: now, UpdatedAt: now,
		Config: ResearchProfileConfig{MaxPages: 1, CrawlDepth: 0, MaxResearchLoops: 1, MaxPlanSteps: 1, Timeout: "30s"},
	}}}); err != nil {
		t.Fatalf("save research: %v", err)
	}

	bundle, err := buildProfileBundle(nil, nil)
	if err != nil {
		t.Fatalf("build bundle: %v", err)
	}
	out := filepath.Join(t.TempDir(), "bundle.json")
	if err := writeProfileBundle(out, bundle); err != nil {
		t.Fatalf("write bundle: %v", err)
	}
	loaded, err := readProfileBundle(out)
	if err != nil {
		t.Fatalf("read bundle: %v", err)
	}
	if len(loaded.Learn.Profiles) != 1 || len(loaded.Research.Profiles) != 1 {
		t.Fatalf("unexpected bundle profile counts: %+v", loaded)
	}
}

func TestProfileBundleFiltering(t *testing.T) {
	profiles := []ResearchProfile{{Name: "a"}, {Name: "b"}}
	filtered := filterResearchProfilesByName(profiles, []string{"b"})
	if len(filtered) != 1 || filtered[0].Name != "b" {
		t.Fatalf("unexpected filter result: %#v", filtered)
	}
}
