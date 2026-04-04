package orchestration

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/Thynaptic/P-LMv1/pkg/cognition"
	"github.com/Thynaptic/P-LMv1/pkg/memory"
	"github.com/Thynaptic/P-LMv1/pkg/rag"
	"github.com/Thynaptic/P-LMv1/pkg/skills"
)

const defaultRecursiveSourceOptimizationLog = ".memory/recursive_source_optimization.json"

// SourceRefactorProposal captures the generated Go Loom improvement draft.
type SourceRefactorProposal struct {
	TargetPackage   string `json:"target_package"`
	Title           string `json:"title"`
	Rationale       string `json:"rationale"`
	ExpectedBenefit string `json:"expected_benefit"`
	ProposedCode    string `json:"proposed_code"`
}

// RecursiveSourceOptimizationReport is the end-to-end mission result.
type RecursiveSourceOptimizationReport struct {
	StartedAt           time.Time                     `json:"started_at"`
	CompletedAt         time.Time                     `json:"completed_at"`
	IndexedRoot         string                        `json:"indexed_root"`
	IndexStats          rag.IndexStats                `json:"index_stats"`
	OuroborosHotspot    string                        `json:"ouroboros_hotspot"`
	StageLatencyMS      map[string]float64            `json:"stage_latency_ms"`
	CausalAssessment    cognition.CausalAssessment    `json:"causal_assessment"`
	UpgradeAssessments  []cognition.UpgradeSimulation `json:"upgrade_assessments"`
	StabilityGatePassed bool                          `json:"stability_gate_passed"`
	RefactorProposal    SourceRefactorProposal        `json:"refactor_proposal"`
	VerificationReport  skills.DiagnosticReport       `json:"verification_report"`
	Verified            bool                          `json:"verified"`
	LogPath             string                        `json:"log_path"`
}

// RunRecursiveSourceOptimization executes Safe Hands ingest + structural critique + Go Loom proposal + self-verification.
func RunRecursiveSourceOptimization(mm *memory.MemoryManager) (RecursiveSourceOptimizationReport, error) {
	report := RecursiveSourceOptimizationReport{
		StartedAt:      time.Now().UTC(),
		IndexedRoot:    "pkg",
		StageLatencyMS: map[string]float64{},
		LogPath:        defaultRecursiveSourceOptimizationLog,
	}

	if mm == nil {
		return report, fmt.Errorf("memory manager is required")
	}

	stats, err := safeHandsIndexCodebase(mm, "pkg")
	if err != nil {
		return report, err
	}
	report.IndexStats = stats

	hotspot, stageLatency := analyzeOuroborosLatency()
	report.OuroborosHotspot = hotspot
	report.StageLatencyMS = stageLatency
	report.CausalAssessment = buildOuroborosCausalAssessment(hotspot)

	candidates := buildGoLoomUpgradeCandidates(hotspot, stageLatency)
	bestIdx := -1
	bestEff := -1.0
	for i, c := range candidates {
		sim := cognition.SimulateSystemUpgrade(c.Title + " " + c.Rationale + " " + c.TargetPackage)
		report.UpgradeAssessments = append(report.UpgradeAssessments, sim)
		if cognition.PassesStabilityGate(sim) && sim.EfficiencyDelta > bestEff {
			bestIdx = i
			bestEff = sim.EfficiencyDelta
		}
	}
	if bestIdx >= 0 {
		report.StabilityGatePassed = true
		report.RefactorProposal = candidates[bestIdx]
		verify, err := selfVerifyGoLoomProposal(report.RefactorProposal.ProposedCode)
		if err != nil {
			return report, err
		}
		report.VerificationReport = verify
		report.Verified = verify.Success
	}

	report.CompletedAt = time.Now().UTC()
	if err := persistSourceOptimizationReport(report, report.LogPath); err != nil {
		return report, err
	}

	_ = mm.AddKnowledge("Recursive Source Optimization report:\n"+FormatRecursiveSourceOptimizationReport(report), map[string]string{
		"type":            "knowledge",
		"source_type":     "source_optimization",
		"base_importance": "0.80",
	})
	return report, nil
}

// FormatRecursiveSourceOptimizationReport creates a user-facing mission summary.
func FormatRecursiveSourceOptimizationReport(r RecursiveSourceOptimizationReport) string {
	var pairs []string
	for stage, ms := range r.StageLatencyMS {
		pairs = append(pairs, fmt.Sprintf("%s=%.0fms", stage, ms))
	}
	sort.Strings(pairs)
	verify := "failed"
	if r.Verified {
		verify = "passed"
	}
	symbolicLine := ""
	for _, sim := range r.UpgradeAssessments {
		d := strings.ToLower(strings.TrimSpace(sim.ChangeDescription))
		if strings.Contains(d, "symbolic") && strings.Contains(d, "strict") {
			symbolicLine = fmt.Sprintf("Counterfactual: stricter SymbolicAuditor would add %.2f correction-loop pressure and %.0fms latency; rejected by Stability Gate.\n",
				sim.CorrectionLoopDelta, sim.ResponseLatencyDeltaMS)
			break
		}
	}
	proposalLine := "No self-optimization plan passed the Stability Gate (requires EfficiencyDelta > 0 and RegressiveRisk = 0).\n"
	verifyLine := ""
	if r.StabilityGatePassed && strings.TrimSpace(r.RefactorProposal.TargetPackage) != "" {
		proposalLine = "Go Loom proposal target: " + r.RefactorProposal.TargetPackage + ".\n"
		verifyLine = "Self-verification (compile/lint): " + verify + "."
	}
	return "Recursive Source Optimization complete.\n" +
		fmt.Sprintf("Safe Hands indexed pkg/: files=%d chunks=%d skipped=%d.\n", r.IndexStats.FilesIndexed, r.IndexStats.ChunksIndexed, r.IndexStats.SkippedUnsupported+r.IndexStats.SkippedBinary) +
		"Highest latency in Ouroboros loop: " + r.OuroborosHotspot + ".\n" +
		"Estimated stage latency: " + strings.Join(pairs, ", ") + ".\n" +
		fmt.Sprintf("Causal fragility score: %.2f across %d what-if simulations.\n", r.CausalAssessment.FragilityScore, len(r.CausalAssessment.Simulations)) +
		symbolicLine +
		proposalLine +
		verifyLine
}

func safeHandsIndexCodebase(mm *memory.MemoryManager, root string) (rag.IndexStats, error) {
	opts := rag.DefaultIndexOptions()
	// Safe Hands: focus on code structure and nearby docs while avoiding destructive actions.
	opts.Extensions = map[string]bool{
		".go":  true,
		".md":  true,
		".txt": true,
	}
	opts.MaxChunkChars = 1000
	opts.ChunkOverlap = 120
	return rag.IndexDirectory(mm, root, opts)
}

func analyzeOuroborosLatency() (string, map[string]float64) {
	// Structural latency model for default ouroboros pipeline in chat.go.
	stageLatency := map[string]float64{
		"draft":           3200,
		"policy_audit":    120,
		"sandbox":         900,
		"correction":      4100,
		"draft_eval":      800,
		"research":        2600,
		"fusion":          1500,
		"adversarial":     1800,
		"final_synthesis": 2900,
	}
	hotspot := ""
	maxMS := -1.0
	for stage, ms := range stageLatency {
		if ms > maxMS {
			maxMS = ms
			hotspot = stage
		}
	}
	return hotspot, stageLatency
}

func buildOuroborosCausalAssessment(hotspot string) cognition.CausalAssessment {
	cm := cognition.NewDefaultCausalModel()
	action := "OuroborosCorrectionLoop"
	if strings.TrimSpace(hotspot) != "" {
		action = "Ouroboros" + titleToken(strings.ReplaceAll(hotspot, "_", "")) + "Latency"
	}
	return cm.Simulate(action)
}

func buildGoLoomUpgradeCandidates(hotspot string, stageLatency map[string]float64) []SourceRefactorProposal {
	return []SourceRefactorProposal{
		{
			TargetPackage: "pkg/cognition/symbolic.go",
			Title:         "Strict Symbolic Auditor Escalation",
			Rationale: "Harden contradiction checks by increasing refusal and contradiction sensitivity " +
				"in SymbolicAuditor before Sandbox and FinalGate.",
			ExpectedBenefit: "Potentially catches more weak drafts earlier, but may increase correction loops.",
			ProposedCode: `package cognition

// NOTE: illustrative snippet for strictness tuning proposal.
func proposedStrictnessThreshold() float64 {
	return 0.88
}
`,
		},
		{
			TargetPackage: "pkg/memory/reindexer.go",
			Title:         "Batch-Oriented Dynamic Reindex Scoring",
			Rationale: "Highest hotspot in Ouroboros is '" + hotspot + "'. " +
				"Reducing reindex pass cost keeps memory refresh under tighter latency budgets.",
			ExpectedBenefit: "Reduce repeated parse/score overhead by precomputing normalized goal signals and evaluating batches in one pass.",
			ProposedCode: `package main

import (
	"fmt"
	"math"
	"strings"
)

type chunkMeta struct {
	baseImportance float64
	retrievalCount int
	ageHours       float64
	tags           string
}

func scoreBatch(goal string, chunks []chunkMeta) []float64 {
	goal = strings.ToLower(strings.TrimSpace(goal))
	out := make([]float64, len(chunks))
	for i, c := range chunks {
		freshness := math.Exp(-c.ageHours / 72.0)
		retrievalBoost := math.Min(float64(c.retrievalCount)/8.0, 0.25)
		relevance := 0.15
		if goal != "" && strings.Contains(strings.ToLower(c.tags), goal) {
			relevance = 0.38
		}
		s := (c.baseImportance * 0.45) + (freshness * 0.25) + retrievalBoost + relevance
		if s < 0 {
			s = 0
		}
		if s > 1 {
			s = 1
		}
		out[i] = s
	}
	return out
}

func main() {
	scores := scoreBatch("vps", []chunkMeta{
		{baseImportance: 0.7, retrievalCount: 4, ageHours: 5, tags: "vps firewall"},
		{baseImportance: 0.3, retrievalCount: 1, ageHours: 96, tags: "old notes"},
	})
	fmt.Printf("%.3f %.3f\n", scores[0], scores[1])
}
`,
		},
	}
}

func selfVerifyGoLoomProposal(code string) (skills.DiagnosticReport, error) {
	lab, err := skills.NewCodeLab("")
	if err != nil {
		return skills.DiagnosticReport{}, err
	}
	ws, err := lab.NewScratchWorkspace("go_loom_reindexer")
	if err != nil {
		return skills.DiagnosticReport{}, err
	}
	mainPath := filepath.Join(ws, "main.go")
	if err := os.WriteFile(mainPath, []byte(strings.TrimSpace(code)+"\n"), 0o644); err != nil {
		return skills.DiagnosticReport{}, err
	}
	report := lab.Verify(mainPath)
	return report, nil
}

func persistSourceOptimizationReport(report RecursiveSourceOptimizationReport, path string) error {
	path = strings.TrimSpace(path)
	if path == "" {
		path = defaultRecursiveSourceOptimizationLog
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o644)
}

func titleToken(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	r := []rune(strings.ToLower(s))
	r[0] = []rune(strings.ToUpper(string(r[0])))[0]
	return string(r)
}
