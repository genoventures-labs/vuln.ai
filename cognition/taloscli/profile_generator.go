package taloscli

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

type learnProfileTemplate struct {
	Domain      string
	Description string
	Config      LearnProfileConfig
}

type researchProfileTemplate struct {
	Domain      string
	Description string
	Categories  []string
	Config      ResearchProfileConfig
}

var (
	learnProfileGenName        string
	learnProfileGenDescription string
	learnProfileGenForce       bool
	learnProfileGenDryRun      bool
	learnProfileGenListDomains bool
	learnProfileGenSetDefault  bool

	researchProfileGenName        string
	researchProfileGenDescription string
	researchProfileGenForce       bool
	researchProfileGenDryRun      bool
	researchProfileGenListDomains bool
	researchProfileGenSetDefault  bool
	researchProfileGenCategories  []string
)

var learnProfileGenCmd = &cobra.Command{
	Use:   "profile-gen <domain>",
	Short: "Generate a learn profile from built-in domain templates.",
	Args:  cobra.ArbitraryArgs,
	Run: func(cmd *cobra.Command, args []string) {
		if learnProfileGenListDomains {
			fmt.Println(renderProfileGenDomains("learn", availableLearnProfileTemplateDomains()))
			return
		}
		domain, ok := resolveProfileGenDomainArg(args)
		if !ok {
			fmt.Println("Error: domain is required. Example: talos learn profile-gen security")
			return
		}
		tpl, found := resolveLearnProfileTemplate(domain)
		if !found {
			fmt.Printf("Error: unsupported learn profile domain: %s\n", domain)
			fmt.Println(renderProfileGenDomains("learn", availableLearnProfileTemplateDomains()))
			return
		}
		name := normalizeLearnProfileName(learnProfileGenName)
		if name == "" {
			name = defaultGeneratedProfileName("learn", domain)
		}
		desc := strings.TrimSpace(learnProfileGenDescription)
		if desc == "" {
			desc = tpl.Description
		}
		now := time.Now().UTC().Format(time.RFC3339)
		store, err := loadLearnProfilesFile()
		if err != nil {
			fmt.Printf("Error loading learn profiles: %v\n", err)
			return
		}
		idx := findLearnProfileIndex(store.Profiles, name)
		exists := idx >= 0
		if exists && !learnProfileGenForce {
			fmt.Printf("Error: profile already exists: %s (use --force to overwrite)\n", name)
			return
		}
		profile := LearnProfile{
			Name:        name,
			Description: desc,
			CreatedAt:   now,
			UpdatedAt:   now,
			Config:      tpl.Config,
		}
		if exists {
			profile.CreatedAt = store.Profiles[idx].CreatedAt
		}
		if err := validateLearnProfileConfig(profile.Config); err != nil {
			fmt.Printf("Error: generated profile is invalid: %v\n", err)
			return
		}
		if learnProfileGenDryRun {
			fmt.Println(renderLearnProfileGenDryRun(domain, tpl, profile, exists, learnProfileGenSetDefault))
			return
		}
		if exists {
			store.Profiles[idx] = profile
		} else {
			store.Profiles = append(store.Profiles, profile)
		}
		if learnProfileGenSetDefault {
			store.DefaultProfile = profile.Name
		}
		if err := saveLearnProfilesFile(store); err != nil {
			fmt.Printf("Error saving learn profiles: %v\n", err)
			return
		}
		fmt.Println("Learn profile generated.")
		fmt.Printf("  domain: %s\n", domain)
		fmt.Printf("  name: %s\n", profile.Name)
		if exists {
			fmt.Println("  action: overwritten")
		} else {
			fmt.Println("  action: created")
		}
		fmt.Printf("  set_default: %t\n", learnProfileGenSetDefault)
	},
}

var researchProfileGenCmd = &cobra.Command{
	Use:   "profile-gen <domain>",
	Short: "Generate a research profile from built-in domain templates.",
	Args:  cobra.ArbitraryArgs,
	Run: func(cmd *cobra.Command, args []string) {
		if researchProfileGenListDomains {
			fmt.Println(renderProfileGenDomains("research", availableResearchProfileTemplateDomains()))
			return
		}
		domain, ok := resolveProfileGenDomainArg(args)
		if !ok {
			fmt.Println("Error: domain is required. Example: talos research profile-gen security")
			return
		}
		tpl, found := resolveResearchProfileTemplate(domain)
		if !found {
			fmt.Printf("Error: unsupported research profile domain: %s\n", domain)
			fmt.Println(renderProfileGenDomains("research", availableResearchProfileTemplateDomains()))
			return
		}
		name := normalizeResearchProfileName(researchProfileGenName)
		if name == "" {
			name = defaultGeneratedProfileName("research", domain)
		}
		desc := strings.TrimSpace(researchProfileGenDescription)
		if desc == "" {
			desc = tpl.Description
		}
		categories := normalizeResearchTags(append(append([]string(nil), tpl.Categories...), researchProfileGenCategories...))
		now := time.Now().UTC().Format(time.RFC3339)
		store, err := loadResearchProfilesFile()
		if err != nil {
			fmt.Printf("Error loading research profiles: %v\n", err)
			return
		}
		idx := findResearchProfileIndex(store.Profiles, name)
		exists := idx >= 0
		if exists && !researchProfileGenForce {
			fmt.Printf("Error: profile already exists: %s (use --force to overwrite)\n", name)
			return
		}
		profile := ResearchProfile{
			Name:        name,
			Description: desc,
			Categories:  categories,
			CreatedAt:   now,
			UpdatedAt:   now,
			Config:      tpl.Config,
		}
		if exists {
			profile.CreatedAt = store.Profiles[idx].CreatedAt
		}
		if err := validateResearchProfileConfig(profile.Config); err != nil {
			fmt.Printf("Error: generated profile is invalid: %v\n", err)
			return
		}
		if researchProfileGenDryRun {
			fmt.Println(renderResearchProfileGenDryRun(domain, tpl, profile, exists, researchProfileGenSetDefault))
			return
		}
		if exists {
			store.Profiles[idx] = profile
		} else {
			store.Profiles = append(store.Profiles, profile)
		}
		if researchProfileGenSetDefault {
			store.DefaultProfile = profile.Name
		}
		if err := saveResearchProfilesFile(store); err != nil {
			fmt.Printf("Error saving research profiles: %v\n", err)
			return
		}
		fmt.Println("Research profile generated.")
		fmt.Printf("  domain: %s\n", domain)
		fmt.Printf("  name: %s\n", profile.Name)
		if exists {
			fmt.Println("  action: overwritten")
		} else {
			fmt.Println("  action: created")
		}
		fmt.Printf("  set_default: %t\n", researchProfileGenSetDefault)
	},
}

func installProfileGeneratorCommands() {
	learnProfileGenCmd.Flags().StringVar(&learnProfileGenName, "name", "", "Generated profile name")
	learnProfileGenCmd.Flags().StringVar(&learnProfileGenDescription, "description", "", "Profile description")
	learnProfileGenCmd.Flags().BoolVar(&learnProfileGenForce, "force", false, "Overwrite existing profile if name exists")
	learnProfileGenCmd.Flags().BoolVar(&learnProfileGenDryRun, "dry-run", false, "Render generated profile without saving")
	learnProfileGenCmd.Flags().BoolVar(&learnProfileGenListDomains, "list-domains", false, "List available generator domains")
	learnProfileGenCmd.Flags().BoolVar(&learnProfileGenSetDefault, "set-default", false, "Set generated profile as default learn profile")
	learnCmd.AddCommand(learnProfileGenCmd)

	researchProfileGenCmd.Flags().StringVar(&researchProfileGenName, "name", "", "Generated profile name")
	researchProfileGenCmd.Flags().StringVar(&researchProfileGenDescription, "description", "", "Profile description")
	researchProfileGenCmd.Flags().StringSliceVar(&researchProfileGenCategories, "category", nil, "Extra category tag(s) to append")
	researchProfileGenCmd.Flags().BoolVar(&researchProfileGenForce, "force", false, "Overwrite existing profile if name exists")
	researchProfileGenCmd.Flags().BoolVar(&researchProfileGenDryRun, "dry-run", false, "Render generated profile without saving")
	researchProfileGenCmd.Flags().BoolVar(&researchProfileGenListDomains, "list-domains", false, "List available generator domains")
	researchProfileGenCmd.Flags().BoolVar(&researchProfileGenSetDefault, "set-default", false, "Set generated profile as default research profile")
	researchCmd.AddCommand(researchProfileGenCmd)
}

func resolveProfileGenDomainArg(args []string) (string, bool) {
	if len(args) == 0 {
		return "", false
	}
	domain := normalizeProfileGenDomain(strings.Join(args, " "))
	if domain == "" {
		return "", false
	}
	return domain, true
}

func normalizeProfileGenDomain(v string) string {
	v = strings.TrimSpace(strings.ToLower(v))
	v = strings.ReplaceAll(v, "_", "-")
	v = strings.Join(strings.Fields(v), "-")
	return v
}

func defaultGeneratedProfileName(kind, domain string) string {
	domain = normalizeProfileGenDomain(domain)
	kind = strings.TrimSpace(strings.ToLower(kind))
	if kind == "" {
		return domain + "-profile"
	}
	return kind + "-" + domain
}

func availableLearnProfileTemplateDomains() []string {
	out := make([]string, 0, len(learnProfileTemplates))
	for k := range learnProfileTemplates {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func availableResearchProfileTemplateDomains() []string {
	out := make([]string, 0, len(researchProfileTemplates))
	for k := range researchProfileTemplates {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func renderProfileGenDomains(kind string, domains []string) string {
	var b strings.Builder
	b.WriteString("PROFILE GENERATOR DOMAINS\n\n")
	b.WriteString("TYPE\n")
	b.WriteString("  " + strings.ToUpper(strings.TrimSpace(kind)) + "\n\n")
	b.WriteString("DOMAINS\n")
	for i, d := range domains {
		b.WriteString(fmt.Sprintf("  %d. %s\n", i+1, d))
	}
	if len(domains) == 0 {
		b.WriteString("  [none]\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

func renderLearnProfileGenDryRun(domain string, tpl learnProfileTemplate, profile LearnProfile, exists, setDefault bool) string {
	blob, _ := json.MarshalIndent(profile.Config, "", "  ")
	var b strings.Builder
	b.WriteString("LEARN PROFILE GENERATOR DRY RUN\n\n")
	b.WriteString("INPUT\n")
	b.WriteString("  domain: " + domain + "\n")
	b.WriteString("  template: " + tpl.Domain + "\n")
	b.WriteString("  exists: " + fmt.Sprintf("%t", exists) + "\n\n")
	b.WriteString("OUTPUT\n")
	b.WriteString("  name: " + profile.Name + "\n")
	b.WriteString("  description: " + emptyAsNA(profile.Description) + "\n")
	b.WriteString("  set_default: " + fmt.Sprintf("%t", setDefault) + "\n\n")
	b.WriteString("CONFIG\n")
	b.WriteString(string(blob) + "\n")
	b.WriteString("\nEXECUTION\n")
	b.WriteString("  skipped: true\n")
	b.WriteString("  reason: dry-run mode")
	return strings.TrimRight(b.String(), "\n")
}

func renderResearchProfileGenDryRun(domain string, tpl researchProfileTemplate, profile ResearchProfile, exists, setDefault bool) string {
	blob, _ := json.MarshalIndent(profile.Config, "", "  ")
	var b strings.Builder
	b.WriteString("RESEARCH PROFILE GENERATOR DRY RUN\n\n")
	b.WriteString("INPUT\n")
	b.WriteString("  domain: " + domain + "\n")
	b.WriteString("  template: " + tpl.Domain + "\n")
	b.WriteString("  exists: " + fmt.Sprintf("%t", exists) + "\n\n")
	b.WriteString("OUTPUT\n")
	b.WriteString("  name: " + profile.Name + "\n")
	b.WriteString("  description: " + emptyAsNA(profile.Description) + "\n")
	b.WriteString("  categories: " + emptyAsNA(strings.Join(profile.Categories, ", ")) + "\n")
	b.WriteString("  set_default: " + fmt.Sprintf("%t", setDefault) + "\n\n")
	b.WriteString("CONFIG\n")
	b.WriteString(string(blob) + "\n")
	b.WriteString("\nEXECUTION\n")
	b.WriteString("  skipped: true\n")
	b.WriteString("  reason: dry-run mode")
	return strings.TrimRight(b.String(), "\n")
}

func resolveLearnProfileTemplate(domain string) (learnProfileTemplate, bool) {
	domain = normalizeProfileGenDomain(domain)
	tpl, ok := learnProfileTemplates[domain]
	return tpl, ok
}

func resolveResearchProfileTemplate(domain string) (researchProfileTemplate, bool) {
	domain = normalizeProfileGenDomain(domain)
	tpl, ok := researchProfileTemplates[domain]
	return tpl, ok
}

var learnProfileTemplates = map[string]learnProfileTemplate{
	"security": {
		Domain:      "security",
		Description: "Security-focused learn profile for runbooks, advisories, and control baselines.",
		Config: LearnProfileConfig{
			Recursive:              true,
			AllTypes:               false,
			Extensions:             ".md,.txt,.pdf",
			ChunkChars:             1000,
			ChunkOverlap:           150,
			Crawl:                  true,
			CrawlDepth:             1,
			MaxPages:               120,
			RateLimit:              1.5,
			UserAgent:              "talos/1.0 (+https://thynaptic.com)",
			RemoteTimeout:          "20s",
			MaxBytes:               10485760,
			HFMaxRecords:           150,
			URLSafety:              true,
			URLSafetyTimeout:       "45s",
			URLSafetyCacheTTL:      "24h",
			URLSafetyVisibility:    "private",
			URLSafetyFailOpen:      false,
			IncludeResearchSources: true,
			IncludeResearchSummary: true,
		},
	},
	"compliance": {
		Domain:      "compliance",
		Description: "Compliance-oriented learn profile for policy docs, controls, and evidence catalogs.",
		Config: LearnProfileConfig{
			Recursive:              true,
			AllTypes:               false,
			Extensions:             ".md,.txt,.pdf,.csv",
			ChunkChars:             1200,
			ChunkOverlap:           200,
			Crawl:                  true,
			CrawlDepth:             1,
			MaxPages:               160,
			RateLimit:              1.5,
			UserAgent:              "talos/1.0 (+https://thynaptic.com)",
			RemoteTimeout:          "25s",
			MaxBytes:               12582912,
			HFMaxRecords:           200,
			URLSafety:              true,
			URLSafetyTimeout:       "45s",
			URLSafetyCacheTTL:      "24h",
			URLSafetyVisibility:    "private",
			URLSafetyFailOpen:      false,
			IncludeResearchSources: true,
			IncludeResearchSummary: true,
		},
	},
	"incident-response": {
		Domain:      "incident-response",
		Description: "Incident response learn profile for postmortems, timelines, and triage artifacts.",
		Config: LearnProfileConfig{
			Recursive:              true,
			AllTypes:               false,
			Extensions:             ".md,.txt,.json,.log",
			ChunkChars:             900,
			ChunkOverlap:           120,
			Crawl:                  true,
			CrawlDepth:             1,
			MaxPages:               140,
			RateLimit:              2.0,
			UserAgent:              "talos/1.0 (+https://thynaptic.com)",
			RemoteTimeout:          "20s",
			MaxBytes:               8388608,
			HFMaxRecords:           120,
			URLSafety:              true,
			URLSafetyTimeout:       "45s",
			URLSafetyCacheTTL:      "24h",
			URLSafetyVisibility:    "private",
			URLSafetyFailOpen:      false,
			IncludeResearchSources: true,
			IncludeResearchSummary: true,
		},
	},
	"documentation": {
		Domain:      "documentation",
		Description: "Documentation learn profile for technical docs and runbooks.",
		Config: LearnProfileConfig{
			Recursive:              true,
			AllTypes:               false,
			Extensions:             ".md,.txt,.rst,.pdf",
			ChunkChars:             1300,
			ChunkOverlap:           180,
			Crawl:                  true,
			CrawlDepth:             1,
			MaxPages:               180,
			RateLimit:              2.0,
			UserAgent:              "talos/1.0 (+https://thynaptic.com)",
			RemoteTimeout:          "20s",
			MaxBytes:               12582912,
			HFMaxRecords:           200,
			URLSafety:              true,
			URLSafetyTimeout:       "45s",
			URLSafetyCacheTTL:      "24h",
			URLSafetyVisibility:    "private",
			URLSafetyFailOpen:      false,
			IncludeResearchSources: true,
			IncludeResearchSummary: true,
		},
	},
	"onboarding": {
		Domain:      "onboarding",
		Description: "Onboarding learn profile for team guides and environment setup material.",
		Config: LearnProfileConfig{
			Recursive:              true,
			AllTypes:               false,
			Extensions:             ".md,.txt,.pdf",
			ChunkChars:             1200,
			ChunkOverlap:           180,
			Crawl:                  false,
			CrawlDepth:             0,
			MaxPages:               80,
			RateLimit:              2.0,
			UserAgent:              "talos/1.0 (+https://thynaptic.com)",
			RemoteTimeout:          "20s",
			MaxBytes:               8388608,
			HFMaxRecords:           100,
			URLSafety:              true,
			URLSafetyTimeout:       "45s",
			URLSafetyCacheTTL:      "24h",
			URLSafetyVisibility:    "private",
			URLSafetyFailOpen:      false,
			IncludeResearchSources: true,
			IncludeResearchSummary: true,
		},
	},
}

var researchProfileTemplates = map[string]researchProfileTemplate{
	"security": {
		Domain:      "security",
		Description: "Security research profile for advisories, vulnerabilities, and mitigation tracking.",
		Categories:  []string{"security", "risk"},
		Config: ResearchProfileConfig{
			MaxPages:         70,
			CrawlDepth:       2,
			MaxResearchLoops: 5,
			MaxPlanSteps:     8,
			Timeout:          "180s",
			Verbose:          false,
			QueryTemplate:    "Security posture changes and emerging threats for the target stack",
		},
	},
	"compliance": {
		Domain:      "compliance",
		Description: "Compliance research profile for policy updates, controls, and standards drift.",
		Categories:  []string{"compliance", "governance"},
		Config: ResearchProfileConfig{
			MaxPages:         60,
			CrawlDepth:       2,
			MaxResearchLoops: 4,
			MaxPlanSteps:     7,
			Timeout:          "160s",
			Verbose:          false,
			QueryTemplate:    "Regulatory and policy updates impacting current operations",
		},
	},
	"incident-response": {
		Domain:      "incident-response",
		Description: "Incident response research profile for timeline reconstruction and causal evidence gathering.",
		Categories:  []string{"incident-response", "operations"},
		Config: ResearchProfileConfig{
			MaxPages:         80,
			CrawlDepth:       2,
			MaxResearchLoops: 6,
			MaxPlanSteps:     8,
			Timeout:          "200s",
			Verbose:          false,
			QueryTemplate:    "Incident response best practices and recent post-incident lessons learned",
		},
	},
	"documentation": {
		Domain:      "documentation",
		Description: "Documentation research profile for architecture, release, and process changes.",
		Categories:  []string{"documentation", "engineering"},
		Config: ResearchProfileConfig{
			MaxPages:         50,
			CrawlDepth:       1,
			MaxResearchLoops: 4,
			MaxPlanSteps:     6,
			Timeout:          "150s",
			Verbose:          false,
			QueryTemplate:    "Documentation and process updates relevant to the current codebase",
		},
	},
	"onboarding": {
		Domain:      "onboarding",
		Description: "Onboarding research profile for newcomer guides, setup dependencies, and workflow orientation.",
		Categories:  []string{"onboarding", "enablement"},
		Config: ResearchProfileConfig{
			MaxPages:         40,
			CrawlDepth:       1,
			MaxResearchLoops: 3,
			MaxPlanSteps:     5,
			Timeout:          "120s",
			Verbose:          false,
			QueryTemplate:    "Core onboarding workflows and environment setup requirements",
		},
	},
}

func init() {
	installProfileGeneratorCommands()
}
