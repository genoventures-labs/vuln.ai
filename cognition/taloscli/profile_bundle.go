package taloscli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const profileBundleVersion = 1

type profileBundle struct {
	Version    int                  `json:"version"`
	ExportedAt string               `json:"exported_at"`
	Learn      learnProfilesFile    `json:"learn"`
	Research   researchProfilesFile `json:"research"`
}

func buildProfileBundle(learnNames, researchNames []string) (profileBundle, error) {
	learnStore, err := loadLearnProfilesFile()
	if err != nil {
		return profileBundle{}, err
	}
	researchStore, err := loadResearchProfilesFile()
	if err != nil {
		return profileBundle{}, err
	}
	learnStore.Profiles = filterLearnProfilesByName(learnStore.Profiles, learnNames)
	researchStore.Profiles = filterResearchProfilesByName(researchStore.Profiles, researchNames)
	if findLearnProfileIndex(learnStore.Profiles, learnStore.DefaultProfile) < 0 {
		learnStore.DefaultProfile = ""
	}
	if findResearchProfileIndex(researchStore.Profiles, researchStore.DefaultProfile) < 0 {
		researchStore.DefaultProfile = ""
	}
	return profileBundle{
		Version:    profileBundleVersion,
		ExportedAt: time.Now().UTC().Format(time.RFC3339),
		Learn:      learnStore,
		Research:   researchStore,
	}, nil
}

func writeProfileBundle(path string, b profileBundle) error {
	path = strings.TrimSpace(path)
	if path == "" {
		return fmt.Errorf("output path is required")
	}
	if b.Version <= 0 {
		b.Version = profileBundleVersion
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	blob, err := json.MarshalIndent(b, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(blob, '\n'), 0o644)
}

func readProfileBundle(path string) (profileBundle, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return profileBundle{}, fmt.Errorf("input path is required")
	}
	blob, err := os.ReadFile(path)
	if err != nil {
		return profileBundle{}, err
	}
	var b profileBundle
	if err := json.Unmarshal(blob, &b); err != nil {
		return profileBundle{}, err
	}
	if b.Version != profileBundleVersion {
		return profileBundle{}, fmt.Errorf("unsupported profile bundle version: %d", b.Version)
	}
	if b.Learn.Version <= 0 {
		b.Learn.Version = 1
	}
	if b.Research.Version <= 0 {
		b.Research.Version = 1
	}
	if b.Learn.Profiles == nil {
		b.Learn.Profiles = []LearnProfile{}
	}
	if b.Research.Profiles == nil {
		b.Research.Profiles = []ResearchProfile{}
	}
	return b, nil
}

func mergeLearnProfiles(dst, src []LearnProfile) []LearnProfile {
	out := append([]LearnProfile(nil), dst...)
	for _, p := range src {
		idx := findLearnProfileIndex(out, p.Name)
		if idx >= 0 {
			out[idx] = p
			continue
		}
		out = append(out, p)
	}
	return out
}

func mergeResearchProfiles(dst, src []ResearchProfile) []ResearchProfile {
	out := append([]ResearchProfile(nil), dst...)
	for _, p := range src {
		idx := findResearchProfileIndex(out, p.Name)
		if idx >= 0 {
			out[idx] = p
			continue
		}
		out = append(out, p)
	}
	return out
}

func filterLearnProfilesByName(profiles []LearnProfile, names []string) []LearnProfile {
	if len(names) == 0 {
		return append([]LearnProfile(nil), profiles...)
	}
	allow := map[string]bool{}
	for _, n := range names {
		nn := normalizeLearnProfileName(n)
		if nn != "" {
			allow[nn] = true
		}
	}
	out := make([]LearnProfile, 0, len(profiles))
	for _, p := range profiles {
		if allow[normalizeLearnProfileName(p.Name)] {
			out = append(out, p)
		}
	}
	return out
}

func filterResearchProfilesByName(profiles []ResearchProfile, names []string) []ResearchProfile {
	if len(names) == 0 {
		return append([]ResearchProfile(nil), profiles...)
	}
	allow := map[string]bool{}
	for _, n := range names {
		nn := normalizeResearchProfileName(n)
		if nn != "" {
			allow[nn] = true
		}
	}
	out := make([]ResearchProfile, 0, len(profiles))
	for _, p := range profiles {
		if allow[normalizeResearchProfileName(p.Name)] {
			out = append(out, p)
		}
	}
	return out
}
