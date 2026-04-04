package cognition

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"
)

const defaultProductDecisionFeedPath = ".memory/decision_feed.jsonl"

// ProductWorkOrder is a product-facing work order profile used for tiering and sovereign scoring.
type ProductWorkOrder struct {
	ID                string            `json:"id,omitempty"`
	Name              string            `json:"name,omitempty"`
	Goal              string            `json:"goal,omitempty"`
	Requirements      []string          `json:"requirements,omitempty"`
	Constraints       []string          `json:"constraints,omitempty"`
	Features          []string          `json:"features,omitempty"`
	ExpectedLatencyMS int               `json:"expected_latency_ms,omitempty"`
	Metadata          map[string]string `json:"metadata,omitempty"`
}

// SovereignScore captures principle-by-principle scoring.
type SovereignScore struct {
	Score           float64            `json:"score"`
	PrincipleScores map[string]float64 `json:"principle_scores"`
	Violations      []string           `json:"violations,omitempty"`
	Notes           []string           `json:"notes,omitempty"`
}

// ProductTier defines market tier + required capabilities.
type ProductTier struct {
	Name              string   `json:"name"`
	Description       string   `json:"description"`
	RequiredFeatures  []string `json:"required_features"`
	OptionalFeatures  []string `json:"optional_features,omitempty"`
	Guardrails        []string `json:"guardrails,omitempty"`
	MinSovereignScore float64  `json:"min_sovereign_score"`
}

// TierCatalog bundles known tiers.
type TierCatalog struct {
	Community  ProductTier `json:"community"`
	Enterprise ProductTier `json:"enterprise"`
	Federal    ProductTier `json:"federal"`
}

// DecisionIntuition is one compact entry mined from decision_feed.jsonl.
type DecisionIntuition struct {
	Timestamp  time.Time `json:"timestamp"`
	Query      string    `json:"query"`
	Reasoning  string    `json:"reasoning"`
	ChosenPath string    `json:"chosen_path"`
	Topology   string    `json:"topology"`
}

// FounderAdvisorResult is the recommended product path for the current local tool.
type FounderAdvisorResult struct {
	WorkOrderID          string         `json:"work_order_id,omitempty"`
	CurrentTier          string         `json:"current_tier"`
	RecommendedTier      string         `json:"recommended_tier"`
	Sovereign            SovereignScore `json:"sovereign"`
	EnterpriseUpgrades   []string       `json:"enterprise_upgrades,omitempty"`
	DecisionAlignment    float64        `json:"decision_alignment"`
	AlignmentSignals     []string       `json:"alignment_signals,omitempty"`
	DecisionInsightsUsed int            `json:"decision_insights_used"`
	Summary              string         `json:"summary"`
}

// DefaultProductTiers returns the canonical Product Tiers.
func DefaultProductTiers() TierCatalog {
	return TierCatalog{
		Community: ProductTier{
			Name:              "Community",
			Description:       "Local-first baseline with practical defaults.",
			RequiredFeatures:  []string{"local_execution", "basic_logging"},
			OptionalFeatures:  []string{"cli_mode", "vector_memory"},
			Guardrails:        []string{"no-cloud-by-default"},
			MinSovereignScore: 0.55,
		},
		Enterprise: ProductTier{
			Name:              "Enterprise",
			Description:       "Auditable and supportable local deployment profile.",
			RequiredFeatures:  []string{"local_execution", "structured_logging", "audit_trail", "rbac_hooks"},
			OptionalFeatures:  []string{"policy_profiles", "ops_telemetry", "foundry_pipeline"},
			Guardrails:        []string{"local-first", "latency-budgeted-routes"},
			MinSovereignScore: 0.72,
		},
		Federal: ProductTier{
			Name:              "Federal",
			Description:       "High-assurance sovereign stack with strict controls.",
			RequiredFeatures:  []string{"local_execution", "audit_trail", "tamper_evident_logs", "offline_mode", "policy_enforcement"},
			OptionalFeatures:  []string{"airgap_support", "forensic_packets", "deterministic_builds"},
			Guardrails:        []string{"no-cloud", "no-external-api-core", "full-traceability"},
			MinSovereignScore: 0.86,
		},
	}
}

// EvaluateSovereignScore scores a work order against Sovereign Principles:
// No-cloud, Local-first, Low-latency.
func EvaluateSovereignScore(wo ProductWorkOrder) SovereignScore {
	corpus := strings.ToLower(strings.TrimSpace(strings.Join([]string{
		wo.Name,
		wo.Goal,
		strings.Join(wo.Requirements, " "),
		strings.Join(wo.Constraints, " "),
		strings.Join(wo.Features, " "),
	}, " ")))

	noCloud := 0.5
	localFirst := 0.5
	lowLatency := 0.5
	var violations []string
	var notes []string

	if productContainsAny(corpus, "no-cloud", "no cloud", "offline", "airgap", "local only", "local-only") {
		noCloud += 0.35
		notes = append(notes, "explicit no-cloud/local-only signals found")
	}
	if productContainsAny(corpus, "cloud", "saas", "remote api", "external api", "hosted") {
		noCloud -= 0.45
		violations = append(violations, "cloud dependency signal detected")
	}
	if productContainsAny(corpus, "local-first", "local first", "on-device", "on prem", "on-prem", "localhost") {
		localFirst += 0.32
	}
	if productContainsAny(corpus, "hybrid cloud required", "requires internet", "always online") {
		localFirst -= 0.35
		violations = append(violations, "local-first conflict detected")
	}
	if wo.ExpectedLatencyMS > 0 {
		switch {
		case wo.ExpectedLatencyMS <= 800:
			lowLatency += 0.40
		case wo.ExpectedLatencyMS <= 1800:
			lowLatency += 0.20
		case wo.ExpectedLatencyMS <= 3500:
			lowLatency += 0.05
		default:
			lowLatency -= 0.30
			violations = append(violations, "latency target too high")
		}
	}
	if productContainsAny(corpus, "fast path", "latency budget", "low-latency", "cached route") {
		lowLatency += 0.20
	}
	if productContainsAny(corpus, "batch only", "deferred only", "eventual processing") {
		lowLatency -= 0.12
	}

	noCloud = productClamp01(noCloud)
	localFirst = productClamp01(localFirst)
	lowLatency = productClamp01(lowLatency)

	score := productClamp01((noCloud * 0.4) + (localFirst * 0.35) + (lowLatency * 0.25))
	return SovereignScore{
		Score: score,
		PrincipleScores: map[string]float64{
			"no_cloud":    noCloud,
			"local_first": localFirst,
			"low_latency": lowLatency,
		},
		Violations: productDedupe(violations),
		Notes:      productDedupe(notes),
	}
}

// DetermineProductTier maps a work order to Product Tier based on sovereign score + required features.
func DetermineProductTier(wo ProductWorkOrder, catalog TierCatalog) ProductTier {
	score := EvaluateSovereignScore(wo)
	features := productFeatureSet(wo)

	if productHasAll(features, catalog.Federal.RequiredFeatures) && score.Score >= catalog.Federal.MinSovereignScore {
		return catalog.Federal
	}
	if productHasAll(features, catalog.Enterprise.RequiredFeatures) && score.Score >= catalog.Enterprise.MinSovereignScore {
		return catalog.Enterprise
	}
	return catalog.Community
}

// FounderAdvisor suggests enterprise-grade upgrades while preserving local-first sovereignty.
func FounderAdvisor(wo ProductWorkOrder) FounderAdvisorResult {
	return FounderAdvisorWithDecisionFeed(wo, defaultProductDecisionFeedPath)
}

// FounderAdvisorWithDecisionFeed is the injectable variant using a custom decision feed path.
func FounderAdvisorWithDecisionFeed(wo ProductWorkOrder, decisionFeedPath string) FounderAdvisorResult {
	catalog := DefaultProductTiers()
	sovereign := EvaluateSovereignScore(wo)
	currentTier := DetermineProductTier(wo, catalog)
	intuitions, _ := LoadDecisionIntuitions(decisionFeedPath, 64)
	alignScore, signals := scoreDecisionAlignment(wo, intuitions)

	upgrades := suggestEnterpriseUpgrades(wo, sovereign, alignScore)
	recommended := currentTier
	featureSet := productFeatureSet(wo)
	for _, up := range upgrades {
		featureSet[up] = true
	}
	if productHasAll(featureSet, catalog.Enterprise.RequiredFeatures) && sovereign.Score >= catalog.Enterprise.MinSovereignScore-0.06 {
		recommended = catalog.Enterprise
	}
	if productHasAll(featureSet, catalog.Federal.RequiredFeatures) && sovereign.Score >= catalog.Federal.MinSovereignScore-0.06 {
		recommended = catalog.Federal
	}

	summary := fmt.Sprintf(
		"Founder Advisor: %s -> %s path with %d local-first upgrade(s).",
		currentTier.Name, recommended.Name, len(upgrades),
	)
	if len(upgrades) == 0 {
		summary = "Founder Advisor: current profile is stable; no mandatory enterprise upgrades detected."
	}

	return FounderAdvisorResult{
		WorkOrderID:          strings.TrimSpace(wo.ID),
		CurrentTier:          currentTier.Name,
		RecommendedTier:      recommended.Name,
		Sovereign:            sovereign,
		EnterpriseUpgrades:   upgrades,
		DecisionAlignment:    alignScore,
		AlignmentSignals:     signals,
		DecisionInsightsUsed: len(intuitions),
		Summary:              summary,
	}
}

// LoadDecisionIntuitions parses .memory/decision_feed.jsonl entries for alignment signals.
func LoadDecisionIntuitions(path string, maxN int) ([]DecisionIntuition, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		path = defaultProductDecisionFeedPath
	}
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()

	if maxN <= 0 {
		maxN = 64
	}
	var out []DecisionIntuition
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		ln := strings.TrimSpace(sc.Text())
		if ln == "" {
			continue
		}
		var row DecisionIntuition
		if err := json.Unmarshal([]byte(ln), &row); err != nil {
			continue
		}
		out = append(out, row)
		if len(out) > maxN {
			out = out[len(out)-maxN:]
		}
	}
	return out, nil
}

func suggestEnterpriseUpgrades(wo ProductWorkOrder, sovereign SovereignScore, alignScore float64) []string {
	features := productFeatureSet(wo)
	var out []string
	add := func(f string) {
		if !features[f] {
			out = append(out, f)
			features[f] = true
		}
	}

	// Keep upgrades local-first and auditable.
	add("structured_logging")
	add("audit_trail")
	if alignScore >= 0.45 || sovereign.PrincipleScores["local_first"] >= 0.65 {
		add("ops_telemetry")
	}
	if sovereign.PrincipleScores["no_cloud"] >= 0.6 {
		add("rbac_hooks")
	}
	// Preserve core local promise by avoiding cloud-oriented suggestions.
	return productDedupe(out)
}

func scoreDecisionAlignment(wo ProductWorkOrder, in []DecisionIntuition) (float64, []string) {
	if len(in) == 0 {
		return 0.5, []string{"no decision feed entries available; using neutral alignment"}
	}
	corpus := strings.ToLower(strings.Join([]string{
		wo.Name, wo.Goal, strings.Join(wo.Requirements, " "), strings.Join(wo.Constraints, " "), strings.Join(wo.Features, " "),
	}, " "))
	match := 0.0
	total := 0.0
	var signals []string
	addToken := func(tok, signal string, w float64) {
		total += w
		if strings.Contains(corpus, tok) {
			match += w
			signals = append(signals, signal)
		}
	}
	for _, d := range in {
		txt := strings.ToLower(strings.Join([]string{d.Query, d.Reasoning, d.ChosenPath, d.Topology}, " "))
		if txt == "" {
			continue
		}
		addToken("local", "aligned with local-first intuition", 0.4)
		addToken("go", "aligned with go-native intuition", 0.2)
		addToken("audit", "aligned with auditability intuition", 0.2)
		addToken("latency", "aligned with low-latency intuition", 0.2)
		if productContainsAny(txt, "no external api", "no-cloud", "local-first") {
			total += 0.35
			if productContainsAny(corpus, "no external api", "no-cloud", "local-first") {
				match += 0.35
			}
		}
	}
	if total <= 0 {
		return 0.5, nil
	}
	return productClamp01(match / total), productDedupe(signals)
}

func productFeatureSet(wo ProductWorkOrder) map[string]bool {
	out := map[string]bool{}
	add := func(v string) {
		v = strings.ToLower(strings.TrimSpace(v))
		if v != "" {
			out[v] = true
		}
	}
	for _, v := range wo.Features {
		add(v)
	}
	corpus := strings.ToLower(strings.Join(append(append([]string{}, wo.Requirements...), wo.Constraints...), " "))
	if productContainsAny(corpus, "local", "localhost", "on-device", "on prem", "on-prem") {
		out["local_execution"] = true
	}
	if productContainsAny(corpus, "log", "logger", "trace") {
		out["basic_logging"] = true
	}
	if productContainsAny(corpus, "audit", "audit trail") {
		out["audit_trail"] = true
	}
	if productContainsAny(corpus, "rbac", "access control", "policy profile") {
		out["rbac_hooks"] = true
	}
	if productContainsAny(corpus, "tamper", "immutable log", "forensic") {
		out["tamper_evident_logs"] = true
	}
	if productContainsAny(corpus, "offline", "airgap", "air-gap") {
		out["offline_mode"] = true
	}
	if productContainsAny(corpus, "policy", "compliance") {
		out["policy_enforcement"] = true
	}
	return out
}

func productHasAll(have map[string]bool, req []string) bool {
	for _, r := range req {
		r = strings.ToLower(strings.TrimSpace(r))
		if r == "" {
			continue
		}
		if !have[r] {
			return false
		}
	}
	return true
}

func productContainsAny(s string, tokens ...string) bool {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" {
		return false
	}
	for _, t := range tokens {
		if strings.Contains(s, strings.ToLower(strings.TrimSpace(t))) {
			return true
		}
	}
	return false
}

func productClamp01(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

func productDedupe(in []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(in))
	for _, v := range in {
		v = strings.TrimSpace(v)
		if v == "" {
			continue
		}
		k := strings.ToLower(v)
		if seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, v)
	}
	return out
}
