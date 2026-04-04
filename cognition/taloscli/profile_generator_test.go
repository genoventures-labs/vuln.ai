package taloscli

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveProfileTemplates(t *testing.T) {
	if _, ok := resolveLearnProfileTemplate("security"); !ok {
		t.Fatal("expected learn security template")
	}
	if _, ok := resolveResearchProfileTemplate("security"); !ok {
		t.Fatal("expected research security template")
	}
	if _, ok := resolveLearnProfileTemplate("does-not-exist"); ok {
		t.Fatal("did not expect unknown learn template")
	}
}

func TestDefaultGeneratedProfileName(t *testing.T) {
	if got := defaultGeneratedProfileName("learn", "incident response"); got != "learn-incident-response" {
		t.Fatalf("unexpected generated name: %s", got)
	}
}

func TestProfileGenDomainLists(t *testing.T) {
	learnDomains := availableLearnProfileTemplateDomains()
	researchDomains := availableResearchProfileTemplateDomains()
	if len(learnDomains) < 3 || len(researchDomains) < 3 {
		t.Fatalf("expected populated domain lists: learn=%v research=%v", learnDomains, researchDomains)
	}
}

func TestLearnProfileGenDryRunAndCreate(t *testing.T) {
	oldPath := learnProfilesPath
	oldName := learnProfileGenName
	oldDesc := learnProfileGenDescription
	oldForce := learnProfileGenForce
	oldDry := learnProfileGenDryRun
	oldList := learnProfileGenListDomains
	oldSet := learnProfileGenSetDefault
	defer func() {
		learnProfilesPath = oldPath
		learnProfileGenName = oldName
		learnProfileGenDescription = oldDesc
		learnProfileGenForce = oldForce
		learnProfileGenDryRun = oldDry
		learnProfileGenListDomains = oldList
		learnProfileGenSetDefault = oldSet
	}()
	learnProfilesPath = filepath.Join(t.TempDir(), "learn_profiles.json")

	learnProfileGenName = ""
	learnProfileGenDescription = ""
	learnProfileGenForce = false
	learnProfileGenDryRun = true
	learnProfileGenListDomains = false
	learnProfileGenSetDefault = false
	learnProfileGenCmd.Run(learnProfileGenCmd, []string{"security"})
	learnProfileGenDryRun = false
	learnProfileGenSetDefault = true
	learnProfileGenCmd.Run(learnProfileGenCmd, []string{"security"})

	store, err := loadLearnProfilesFile()
	if err != nil {
		t.Fatalf("load learn profiles: %v", err)
	}
	if len(store.Profiles) != 1 {
		t.Fatalf("expected one generated learn profile, got %d", len(store.Profiles))
	}
	if strings.TrimSpace(store.DefaultProfile) == "" {
		t.Fatal("expected default learn profile set")
	}
}

func TestResearchProfileGenCreateWithCategories(t *testing.T) {
	oldPath := researchProfilesPath
	oldName := researchProfileGenName
	oldDesc := researchProfileGenDescription
	oldForce := researchProfileGenForce
	oldDry := researchProfileGenDryRun
	oldList := researchProfileGenListDomains
	oldSet := researchProfileGenSetDefault
	oldCats := append([]string(nil), researchProfileGenCategories...)
	defer func() {
		researchProfilesPath = oldPath
		researchProfileGenName = oldName
		researchProfileGenDescription = oldDesc
		researchProfileGenForce = oldForce
		researchProfileGenDryRun = oldDry
		researchProfileGenListDomains = oldList
		researchProfileGenSetDefault = oldSet
		researchProfileGenCategories = oldCats
	}()
	researchProfilesPath = filepath.Join(t.TempDir(), "research_profiles.json")

	researchProfileGenName = ""
	researchProfileGenDescription = ""
	researchProfileGenForce = false
	researchProfileGenDryRun = false
	researchProfileGenListDomains = false
	researchProfileGenSetDefault = true
	researchProfileGenCategories = []string{"threat-intel", "security"}
	researchProfileGenCmd.Run(researchProfileGenCmd, []string{"security"})

	store, err := loadResearchProfilesFile()
	if err != nil {
		t.Fatalf("load research profiles: %v", err)
	}
	if len(store.Profiles) != 1 {
		t.Fatalf("expected one generated research profile, got %d", len(store.Profiles))
	}
	if !profileHasCategory(store.Profiles[0], "threat-intel") {
		t.Fatalf("expected generated category merge, got %v", store.Profiles[0].Categories)
	}
	if strings.TrimSpace(store.DefaultProfile) == "" {
		t.Fatal("expected default research profile set")
	}
}
