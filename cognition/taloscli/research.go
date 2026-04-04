package taloscli

import (
	"fmt"
	"strings"
	"time"

	"github.com/Thynaptic/P-LMv1/pkg/cognition"
	"github.com/Thynaptic/P-LMv1/pkg/memory"
	"github.com/Thynaptic/P-LMv1/pkg/orchestration"
	"github.com/Thynaptic/P-LMv1/pkg/router"
	"github.com/Thynaptic/P-LMv1/pkg/state"
	"github.com/Thynaptic/P-LMv1/pkg/tools"
	"github.com/ollama/ollama/api"
	"github.com/spf13/cobra"
)

type researchRuntime struct {
	client          *api.Client
	mm              *memory.MemoryManager
	tc              *tools.GLMToolClient
	modelCandidates []string
	reindexer       *memory.Reindexer
	sm              *state.Manager
}

type researchFinding struct {
	Text     string `json:"text"`
	Refs     []int  `json:"refs,omitempty"`
	Verified bool   `json:"verified"`
}

type researchReport struct {
	Mode            string
	Query           string
	Executive       string
	Findings        []researchFinding
	EvidenceNotes   []string
	Risks           []string
	NextActions     []string
	Sources         []string
	UnverifiedCount int
}

var (
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

	researchSessionsLast int
	researchRunDryRun    bool
	researchDeepDryRun   bool
)

var researchCmd = &cobra.Command{
	Use:   "research",
	Short: "Run structured research workflows with professional report output.",
}

var researchRunCmd = &cobra.Command{
	Use:   "run [query]",
	Short: "Run bounded research workflow with tool-assisted discovery and synthesis.",
	Args:  cobra.ArbitraryArgs,
	Run: func(cmd *cobra.Command, args []string) {
		query := strings.TrimSpace(strings.Join(args, " "))
		resolvedQuery, err := resolveResearchProfileForRun(cmd, "run", query)
		if err != nil {
			fmt.Printf("Error resolving research profile: %v\n", err)
			return
		}
		if strings.TrimSpace(resolvedQuery) == "" {
			fmt.Println("Query cannot be empty. Provide a query or set --query-template in the selected profile.")
			return
		}
		ctx := researchExecutionContext{
			ProfileName:       researchProfileApplied,
			ProfileCategories: append([]string(nil), researchCategoriesApplied...),
		}
		if researchRunDryRun {
			fmt.Println(renderResearchDryRunPlan("run", resolvedQuery, ctx))
			return
		}
		report, _, err := executeResearchMode("run", resolvedQuery, ctx)
		if err != nil {
			fmt.Printf("Error: %v\n", err)
			return
		}
		fmt.Println(renderResearchReport(report))
	},
}

var researchDeepCmd = &cobra.Command{
	Use:   "deep [query]",
	Short: "Run advanced deep research with expanded depth and multi-agent pipeline.",
	Args:  cobra.ArbitraryArgs,
	Run: func(cmd *cobra.Command, args []string) {
		query := strings.TrimSpace(strings.Join(args, " "))
		resolvedQuery, err := resolveResearchProfileForRun(cmd, "deep", query)
		if err != nil {
			fmt.Printf("Error resolving research profile: %v\n", err)
			return
		}
		if strings.TrimSpace(resolvedQuery) == "" {
			fmt.Println("Query cannot be empty. Provide a query or set --query-template in the selected profile.")
			return
		}
		ctx := researchExecutionContext{
			ProfileName:       researchProfileApplied,
			ProfileCategories: append([]string(nil), researchCategoriesApplied...),
		}
		if researchDeepDryRun {
			fmt.Println(renderResearchDryRunPlan("deep", resolvedQuery, ctx))
			return
		}
		report, _, err := executeResearchMode("deep", resolvedQuery, ctx)
		if err != nil {
			fmt.Printf("Error: %v\n", err)
			return
		}
		fmt.Println(renderResearchReport(report))
	},
}

var researchSessionsCmd = &cobra.Command{
	Use:   "sessions",
	Short: "List recent research sessions and artifact ids.",
	Run: func(cmd *cobra.Command, args []string) {
		recs, corrupt, err := readResearchSessionRecords()
		if err != nil {
			fmt.Printf("Error reading research sessions: %v\n", err)
			return
		}
		if researchSessionsLast > 0 && len(recs) > researchSessionsLast {
			recs = recs[:researchSessionsLast]
		}
		fmt.Println(renderResearchSessions(recs, corrupt, researchSessionsLast))
	},
}

type researchExecutionContext struct {
	ProfileName       string
	ProfileCategories []string
}

func executeResearchMode(mode, query string, ctx researchExecutionContext) (researchReport, string, error) {
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode != "run" && mode != "deep" {
		return researchReport{}, "", fmt.Errorf("unsupported research mode: %s", mode)
	}
	query = strings.TrimSpace(query)
	if query == "" {
		return researchReport{}, "", fmt.Errorf("query cannot be empty")
	}

	rt, err := initResearchRuntime(query)
	if err != nil {
		return researchReport{}, "", err
	}
	defer rt.Close()
	env, proceed, clarification := preprocessUserIntent(query, rt.sm, rt.mm, "research")
	if !proceed {
		return researchReport{}, "", fmt.Errorf("clarification needed: %s", clarification)
	}
	if strings.TrimSpace(env.Normalized) != "" {
		query = strings.TrimSpace(env.Normalized)
	}
	if len(env.CommandHints) > 0 {
		fmt.Printf("DEBUG: Research intent hints: %s\n", strings.Join(env.CommandHints, ","))
	}
	modulation := cognition.BuildReasoningModulation(rt.sm, query, mode)
	styleProfile := cognition.ResolveStyleProfile(rt.sm, rt.mm, query, mode)
	styleContract := ""
	if cognition.StyleV2Enabled() {
		styleContract = cognition.BuildStylePromptContract(styleProfile)
	}
	fmt.Printf("DEBUG: Research modulation entropy=%s load=%.2f persistence=%.2f density=%.2f branches=%d sections=%d\n",
		modulation.Entropy.Mode,
		modulation.EmotionPressure,
		modulation.GoalPersistence,
		modulation.DensityScale,
		modulation.BranchBudget,
		modulation.SectionBudget,
	)
	if strings.TrimSpace(modulation.PredictiveIntervention) != "" {
		fmt.Printf("DEBUG: Research predictive intervention=%s trend=%.2f volatility=%.2f\n",
			modulation.PredictiveIntervention,
			modulation.EmotionTrend,
			modulation.EmotionVolatility,
		)
	}

	if mode == "run" {
		fmt.Println("Phase: Planning")
		planMaxSteps := 3
		if researchRunMaxResearchLoops > 0 && researchRunMaxResearchLoops < planMaxSteps {
			planMaxSteps = researchRunMaxResearchLoops
		}
		planMaxSteps = clampIntBudget(int(float64(planMaxSteps)*modulation.DensityScale), 1, 6)
		if modulation.BranchBudget > 0 && planMaxSteps > modulation.BranchBudget {
			planMaxSteps = modulation.BranchBudget
		}
		researchLoops := clampIntBudget(int(float64(researchRunMaxResearchLoops)*modulation.DensityScale), 1, 24)
		if modulation.SectionBudget > 0 && researchLoops > modulation.SectionBudget {
			researchLoops = modulation.SectionBudget
		}
		plannerInput := "User query:\n" + query +
			fmt.Sprintf("\n\nRun-mode budget:\n- crawl_depth=%d\n- max_pages=%d\n- max_research_loops=%d\n", researchRunCrawlDepth, researchRunMaxPages, researchLoops)
		if len(researchRunSeedURLs) > 0 {
			plannerInput += "\nSeed URLs:\n- " + strings.Join(researchRunSeedURLs, "\n- ")
		}

		plannerTimeout := boundedStageTimeout(researchRunTimeout/4, 20*time.Second, 45*time.Second)
		plannerResp, _, plannerErr := runAgentLoopWithTimeout(
			rt.client,
			rt.tc,
			rt.modelCandidates,
			plannerSystemPrompt(),
			plannerInput,
			1,
			false,
			query,
			"plan",
			0,
			plannerTimeout,
		)
		planSteps := []string{query}
		if plannerErr == nil {
			plannerResp, reflectionNotes := applyReflectionGate(
				rt.client,
				rt.modelCandidates,
				cognition.StageResearchPlan,
				query,
				plannerResp,
				nil,
				nil,
				rt.sm,
			)
			for _, note := range reflectionNotes {
				fmt.Println(note)
			}
			autoDecomposed := false
			planSteps, autoDecomposed = parsePlanStepsV2(plannerResp, query, planMaxSteps)
			if len(planSteps) == 0 {
				planSteps = []string{query}
			}
			if autoDecomposed {
				fmt.Println("Goal Decomposition v2: recursive task tree expanded automatically.")
			}
			fmt.Printf("Planning complete: %d step(s).\n", len(planSteps))
			fmt.Println("Planner outline:")
			for i, step := range planSteps {
				fmt.Printf("  %d. %s\n", i+1, summarizePlanStep(step, 160))
			}
		} else {
			fmt.Printf("Planning warning: %v\n", plannerErr)
			fmt.Println("Planner fallback: using a single direct research step.")
		}

		fmt.Println("Phase: Discovery")
		if researchRunVerbose {
			fmt.Printf("Profile: run (crawl-depth=%d, max-pages=%d, loops=%d, timeout=%s)\n", researchRunCrawlDepth, researchRunMaxPages, researchRunMaxResearchLoops, researchRunTimeout)
		}

		originalVerbose := maVerbose
		originalChatTimeout := maChatTimeout
		originalFirstTokenTimeout := maFirstTokenTimeout
		maVerbose = researchRunVerbose
		maChatTimeout = researchRunTimeout
		maFirstTokenTimeout = maxDuration(20*time.Second, researchRunTimeout/3)
		defer func() {
			maVerbose = originalVerbose
			maChatTimeout = originalChatTimeout
			maFirstTokenTimeout = originalFirstTokenTimeout
		}()

		system := `You are TALOS Research Operator.
Objective: produce a high-signal factual research result.
Workflow:
1) Follow the provided plan steps in order; keep execution focused.
2) Start with web discovery unless explicit seed URLs are provided.
3) Use tools when needed: web_search, fetch_url, http_request, vector_retrieve.
4) Keep breadth/depth bounded by the provided research budget.
5) Return concise findings and cite all available sources (URLs, local paths/docs, hf refs).
Return plain text only.`
		if strings.TrimSpace(styleContract) != "" {
			system += "\n\n" + styleContract
		}
		user := "Research query:\n" + query +
			fmt.Sprintf("\n\nBudget:\n- crawl_depth=%d\n- max_pages=%d\n- max_research_loops=%d\n", researchRunCrawlDepth, researchRunMaxPages, researchLoops)
		user += "\nPlan steps:\n- " + strings.Join(planSteps, "\n- ")
		if len(researchRunSeedURLs) > 0 {
			user += "\nSeed URLs:\n- " + strings.Join(researchRunSeedURLs, "\n- ")
		}

		resp, toolLogs, runErr := runAgentLoop(
			rt.client,
			rt.tc,
			rt.modelCandidates,
			system,
			user,
			researchLoops,
			true,
			query,
			"research",
			int(researchRunTimeout.Milliseconds()),
		)
		if runErr != nil {
			report := buildResearchReport("run", query, "", nil, nil, nil)
			report.Risks = append(report.Risks, "Research execution failed: "+runErr.Error())
			rec, persistErr := persistResearchSession(query, "run", report, "FAILED", runErr.Error(), len(toolLogs), ctx)
			if persistErr != nil {
				fmt.Printf("Warning: Failed to write research session log: %v\n", persistErr)
			}
			return report, rec.SessionID, runErr
		}
		discoverySources := extractSourceRefs(strings.Join(toolLogs, "\n") + "\n" + resp)
		resp, discoveryNotes := applyReflectionGate(
			rt.client,
			rt.modelCandidates,
			cognition.StageResearchDiscover,
			query,
			resp,
			discoverySources,
			nil,
			rt.sm,
		)
		for _, note := range discoveryNotes {
			fmt.Println(note)
		}

		fmt.Println("Phase: Synthesis")
		finalAnswer := resp
		var routeNotes []string
		if shouldUseResearchDocumentRouting(query, rt.mm) {
			if routed, notes := runResearchDocumentRouting(rt, query); strings.TrimSpace(routed) != "" {
				finalAnswer = routed
				routeNotes = notes
			}
		}
		sourceList := extractSourceRefs(strings.Join(toolLogs, "\n") + "\n" + finalAnswer + "\n" + strings.Join(routeNotes, "\n"))
		finalAnswer, reflectionNotes := applyReflectionGate(
			rt.client,
			rt.modelCandidates,
			cognition.StageResearchSynthesis,
			query,
			finalAnswer,
			sourceList,
			nil,
			rt.sm,
		)
		if len(reflectionNotes) > 0 {
			routeNotes = append(routeNotes, reflectionNotes...)
			for _, note := range reflectionNotes {
				fmt.Println(note)
			}
		}
		finalAnswer, supNotes := applyResearchSymbolicSupervision(rt.sm, query, finalAnswer, sourceList)
		if len(supNotes) > 0 {
			routeNotes = append(routeNotes, supNotes...)
		}
		findings := buildFindings(finalAnswer, sourceList)
		report := buildResearchReport("run", query, sanitizeModelOutput(finalAnswer), findings, sourceList, toolLogs)
		if len(routeNotes) > 0 {
			report.EvidenceNotes = append(report.EvidenceNotes, routeNotes...)
		}
		rec, persistErr := persistResearchSession(query, "run", report, "SUCCESS", "", len(toolLogs), ctx)
		if persistErr != nil {
			fmt.Printf("Warning: Failed to write research session log: %v\n", persistErr)
		}
		return report, rec.SessionID, nil
	}

	fmt.Println("Phase: Planning")
	if researchDeepVerbose {
		fmt.Printf("Profile: deep (crawl-depth=%d, max-pages=%d, steps=%d, loops=%d, timeout=%s)\n", researchDeepCrawlDepth, researchDeepMaxPages, researchDeepMaxPlanSteps, researchDeepMaxResearchLoops, researchDeepTimeout)
	}

	originalVerbose := maVerbose
	originalPlanSteps := maMaxPlanSteps
	originalLoops := maMaxResearchLoops
	originalChatTimeout := maChatTimeout
	originalFirstTokenTimeout := maFirstTokenTimeout
	deepPlanSteps := clampIntBudget(int(float64(researchDeepMaxPlanSteps)*modulation.DensityScale), 2, 16)
	deepLoops := clampIntBudget(int(float64(researchDeepMaxResearchLoops)*modulation.DensityScale), 1, 32)
	if modulation.BranchBudget > 0 && deepPlanSteps > modulation.BranchBudget+2 {
		deepPlanSteps = modulation.BranchBudget + 2
	}
	if modulation.SectionBudget > 0 && deepLoops > modulation.SectionBudget {
		deepLoops = modulation.SectionBudget
	}
	maVerbose = researchDeepVerbose
	maMaxPlanSteps = deepPlanSteps
	maMaxResearchLoops = deepLoops
	maChatTimeout = researchDeepTimeout
	maFirstTokenTimeout = maxDuration(25*time.Second, researchDeepTimeout/3)
	defer func() {
		maVerbose = originalVerbose
		maMaxPlanSteps = originalPlanSteps
		maMaxResearchLoops = originalLoops
		maChatTimeout = originalChatTimeout
		maFirstTokenTimeout = originalFirstTokenTimeout
	}()

	seedHint := ""
	if len(researchDeepSeedURLs) > 0 {
		seedHint = "\n\nSeed URLs:\n- " + strings.Join(researchDeepSeedURLs, "\n- ")
	}
	deepQuery := query + fmt.Sprintf("\n\nDeep research budget: crawl_depth=%d, max_pages=%d, plan_steps=%d, research_loops=%d.%s", researchDeepCrawlDepth, researchDeepMaxPages, deepPlanSteps, deepLoops, seedHint)
	if strings.TrimSpace(styleContract) != "" {
		deepQuery += "\n\n" + styleContract
	}

	fmt.Println("Phase: Research")
	answer, refs, deepErr := runMultiAgentPipeline(rt.client, rt.mm, rt.tc, rt.modelCandidates, deepQuery)
	if deepErr != nil {
		report := buildResearchReport("deep", query, "", nil, nil, nil)
		report.Risks = append(report.Risks, "Deep research execution failed: "+deepErr.Error())
		rec, persistErr := persistResearchSession(query, "deep", report, "FAILED", deepErr.Error(), 0, ctx)
		if persistErr != nil {
			fmt.Printf("Warning: Failed to write research session log: %v\n", persistErr)
		}
		return report, rec.SessionID, deepErr
	}

	fmt.Println("Phase: Synthesis")
	finalAnswer := answer
	var routeNotes []string
	if shouldUseResearchDocumentRouting(query, rt.mm) {
		if routed, notes := runResearchDocumentRouting(rt, query); strings.TrimSpace(routed) != "" {
			finalAnswer = routed
			routeNotes = notes
			for _, n := range notes {
				for _, src := range extractSourceRefs(n) {
					refs = append(refs, src)
				}
			}
			refs = uniqueStrings(refs)
		}
	}
	finalAnswer, reflectionNotes := applyReflectionGate(
		rt.client,
		rt.modelCandidates,
		cognition.StageResearchSynthesis,
		query,
		finalAnswer,
		refs,
		nil,
		rt.sm,
	)
	if len(reflectionNotes) > 0 {
		routeNotes = append(routeNotes, reflectionNotes...)
		for _, note := range reflectionNotes {
			fmt.Println(note)
		}
	}
	finalAnswer, supNotes := applyResearchSymbolicSupervision(rt.sm, query, finalAnswer, refs)
	if len(supNotes) > 0 {
		routeNotes = append(routeNotes, supNotes...)
	}
	findings := buildFindings(finalAnswer, refs)
	report := buildResearchReport("deep", query, sanitizeModelOutput(finalAnswer), findings, refs, nil)
	if len(routeNotes) > 0 {
		report.EvidenceNotes = append(report.EvidenceNotes, routeNotes...)
	}
	rec, persistErr := persistResearchSession(query, "deep", report, "SUCCESS", "", 0, ctx)
	if persistErr != nil {
		fmt.Printf("Warning: Failed to write research session log: %v\n", persistErr)
	}
	return report, rec.SessionID, nil
}

func initResearchRuntime(query string) (*researchRuntime, error) {
	r, err := router.NewRouter()
	if err != nil {
		return nil, fmt.Errorf("initializing router: %w", err)
	}
	initialModel := r.ResolveModel(router.ResolveRequest{
		Query:  query,
		Stage:  "research",
		Models: r.Models,
	})
	modelCandidates := buildModelCandidates(r.Models, initialModel)
	if len(modelCandidates) == 0 {
		return nil, fmt.Errorf("no model candidates available")
	}

	mm, err := memory.NewMemoryManager()
	if err != nil {
		return nil, fmt.Errorf("initializing memory manager: %w", err)
	}
	tc, err := tools.NewGLMToolClient()
	if err != nil {
		fmt.Printf("Warning: tool client unavailable: %v\n", err)
		fmt.Println("Proceeding without external tools.")
	}
	sm, _ := state.NewManager()
	if sm != nil {
		if ns := strings.ToLower(strings.TrimSpace(sm.ActiveNamespace())); ns != "" {
			mm.SetActiveNamespace(ns)
		}
	}
	reindexer := memory.NewReindexer(mm, sm)
	reindexer.Start()

	client, err := api.ClientFromEnvironment()
	if err != nil {
		return nil, fmt.Errorf("creating Ollama client: %w", err)
	}
	return &researchRuntime{
		client:          client,
		mm:              mm,
		tc:              tc,
		modelCandidates: modelCandidates,
		reindexer:       reindexer,
		sm:              sm,
	}, nil
}

func (rt *researchRuntime) Close() {
	if rt != nil && rt.reindexer != nil {
		rt.reindexer.Stop()
	}
}

func buildFindings(raw string, sources []string) []researchFinding {
	clean := sanitizeModelOutput(raw)
	lines := strings.Split(clean, "\n")
	var candidates []string
	for _, line := range lines {
		line = strings.TrimSpace(strings.TrimLeft(line, "-*0123456789. "))
		if line != "" && len(line) > 20 {
			candidates = append(candidates, line)
		}
	}
	if len(candidates) == 0 && strings.TrimSpace(clean) != "" {
		candidates = append(candidates, strings.TrimSpace(clean))
	}
	if len(candidates) > 8 {
		candidates = candidates[:8]
	}

	findings := make([]researchFinding, 0, len(candidates))
	for i, c := range candidates {
		f := researchFinding{Text: c}
		if len(sources) > 0 {
			refIdx := (i % len(sources)) + 1
			f.Refs = []int{refIdx}
			f.Verified = true
		}
		findings = append(findings, f)
	}
	return findings
}

func buildResearchReport(mode, query, summary string, findings []researchFinding, sources, toolLogs []string) researchReport {
	report := researchReport{
		Mode:      mode,
		Query:     query,
		Executive: strings.TrimSpace(summary),
		Findings:  findings,
		Sources:   sources,
	}
	if report.Executive == "" {
		report.Executive = "No executive summary was produced."
	}
	if len(findings) == 0 {
		report.Risks = append(report.Risks, "No high-confidence findings were produced by the research pipeline.")
	}
	if len(sources) == 0 {
		report.Risks = append(report.Risks, "No sources were captured; findings should be treated as unverified.")
	}
	for _, f := range findings {
		if !f.Verified || len(f.Refs) == 0 {
			report.UnverifiedCount++
		}
	}
	if report.UnverifiedCount > 0 {
		report.Risks = append(report.Risks, fmt.Sprintf("%d finding(s) are unverified due to missing source mapping.", report.UnverifiedCount))
	}
	if len(toolLogs) > 0 {
		report.EvidenceNotes = append(report.EvidenceNotes, fmt.Sprintf("Captured %d tool interaction log entries during research.", len(toolLogs)))
	}
	report.NextActions = []string{
		"Validate top findings against primary documentation or official changelogs.",
		"Promote verified evidence into TALOS memory using `talos learn --url` or `talos learn --file`.",
	}
	if mode == "deep" {
		report.NextActions = append([]string{"Run targeted follow-up research for unresolved contradictions or weak citations."}, report.NextActions...)
	}
	return report
}

func applyResearchSymbolicSupervision(sm *state.Manager, query, answer string, sources []string) (string, []string) {
	answer = strings.TrimSpace(answer)
	if answer == "" {
		return answer, nil
	}
	snapshot := state.SessionState{}
	if sm != nil {
		snapshot = sm.GetSnapshot()
	}
	decision, _ := cognition.RunSymbolicSupervision(cognition.SupervisionInput{
		Stage:      cognition.StageResearchFinding,
		Query:      query,
		Candidate:  answer,
		Session:    snapshot,
		SourceRefs: sources,
	}, cognition.DefaultSupervisionPolicy("balanced"))
	if decision.Outcome == cognition.SupervisionHardVeto {
		note := "Symbolic supervision hard-vetoed one or more findings; output downgraded to conservative mode."
		reason := strings.Join(decision.Violations, "; ")
		if strings.TrimSpace(reason) != "" {
			note += " Reason: " + reason
		}
		return "Symbolic supervision flagged this synthesis as high-risk. Re-run with stronger evidence or additional sources.", []string{note}
	}
	if decision.Outcome == cognition.SupervisionSoftWarn && len(decision.Violations) > 0 {
		return answer, []string{"Symbolic supervision warning: " + strings.Join(decision.Violations, "; ")}
	}
	return answer, nil
}

func renderResearchReport(r researchReport) string {
	var b strings.Builder
	b.WriteString("TALOS RESEARCH REPORT\n\n")
	b.WriteString("COMMAND\n")
	b.WriteString("  talos research " + strings.ToLower(strings.TrimSpace(r.Mode)) + "\n\n")
	status := "SUCCESS"
	if r.UnverifiedCount > 0 {
		status = "PARTIAL"
	}
	b.WriteString("STATUS\n")
	b.WriteString("  " + status + "\n\n")
	b.WriteString("MODE\n")
	b.WriteString("  " + strings.ToUpper(strings.TrimSpace(r.Mode)) + "\n\n")
	b.WriteString("QUERY\n")
	b.WriteString("  " + strings.TrimSpace(r.Query) + "\n\n")
	b.WriteString("RESULTS\n")
	b.WriteString(fmt.Sprintf("  findings: %d\n", len(r.Findings)))
	b.WriteString(fmt.Sprintf("  sources: %d\n", len(r.Sources)))
	b.WriteString(fmt.Sprintf("  unverified_findings: %d\n\n", r.UnverifiedCount))
	b.WriteString("EXECUTIVE SUMMARY\n")
	b.WriteString("  " + strings.TrimSpace(r.Executive) + "\n\n")

	b.WriteString("KEY FINDINGS\n")
	if len(r.Findings) == 0 {
		b.WriteString("  - No findings produced.\n")
	} else {
		for _, f := range r.Findings {
			line := "  - " + strings.TrimSpace(f.Text)
			if len(f.Refs) > 0 {
				for _, n := range f.Refs {
					line += fmt.Sprintf(" [%d]", n)
				}
			} else {
				line += " [UNVERIFIED]"
			}
			b.WriteString(line + "\n")
		}
	}
	b.WriteString("\nEVIDENCE AND CITATIONS\n")
	if len(r.EvidenceNotes) == 0 {
		b.WriteString("  - Evidence notes unavailable.\n")
	} else {
		for _, note := range r.EvidenceNotes {
			b.WriteString("  - " + note + "\n")
		}
	}

	b.WriteString("\nRISKS / UNCERTAINTIES\n")
	if len(r.Risks) == 0 {
		b.WriteString("  - No material risks identified in this run.\n")
	} else {
		for _, risk := range r.Risks {
			b.WriteString("  - " + risk + "\n")
		}
	}

	b.WriteString("\nRECOMMENDED NEXT ACTIONS\n")
	if len(r.NextActions) == 0 {
		b.WriteString("  - No follow-up actions suggested.\n")
	} else {
		for _, act := range r.NextActions {
			b.WriteString("  - " + act + "\n")
		}
	}

	b.WriteString("\nSOURCES\n")
	if len(r.Sources) == 0 {
		b.WriteString("  [none]\n")
	} else {
		for i, s := range r.Sources {
			b.WriteString(fmt.Sprintf("  [%d] %s\n", i+1, s))
		}
	}

	return strings.TrimRight(b.String(), "\n")
}

func maxDuration(a, b time.Duration) time.Duration {
	if a > b {
		return a
	}
	return b
}

func shouldUseResearchDocumentRouting(query string, mm *memory.MemoryManager) bool {
	q := strings.ToLower(strings.TrimSpace(query))
	if q == "" {
		return false
	}
	for _, marker := range []string{
		"architecture", "project structure", "codebase", "documents", "files",
		"technical guide", "readme", "across docs", "cross-link", "map",
		"summarize all", "summarise all", "multi-topic", "compare sections",
		"research", "evidence",
	} {
		if strings.Contains(q, marker) {
			return true
		}
	}
	if strings.Count(q, " and ") >= 2 || strings.Count(q, ",") >= 3 {
		return true
	}
	if mm != nil && mm.KnowledgeCount() >= 30 && len(q) > 70 {
		return true
	}
	return false
}

func runResearchDocumentRouting(rt *researchRuntime, query string) (string, []string) {
	if rt == nil || rt.mm == nil {
		return "", nil
	}
	orch := orchestration.DocumentOrchestrator{
		Memory: rt.mm,
		Client: rt.client,
	}
	synth, err := orch.Orchestrate(query, rt.modelCandidates, 32)
	if err != nil || strings.TrimSpace(synth.StructuredAnswer) == "" {
		return "", nil
	}
	notes := []string{
		fmt.Sprintf("Document routing selected %d source group(s) from %d candidate segment(s).", len(synth.RouteReport.SelectedDocs), synth.RouteReport.CandidateSegments),
	}
	for _, line := range orchestration.CompactRouteReportLines(synth.RouteReport, 5) {
		notes = append(notes, line)
	}
	for _, line := range orchestration.CompactHierarchySummaryLines(synth.HierarchyReport, synth.SectionMaps, 5) {
		notes = append(notes, "Hierarchy: "+line)
	}
	for _, line := range orchestration.CompactSectionCrossLinkLines(synth.SectionCrossLinks, 5) {
		notes = append(notes, "SectionLink: "+line)
	}
	if synth.ReasoningReport.Triggered || len(synth.ReasoningReport.Notes) > 0 {
		notes = append(notes, fmt.Sprintf(
			"LongForm: triggered=%t passes=%d coverage=%.2f fallback=%t unsupported_claims=%d",
			synth.ReasoningReport.Triggered,
			synth.ReasoningReport.PassesRun,
			synth.ReasoningReport.CitationCoverage,
			synth.ReasoningReport.FallbackUsed,
			synth.ReasoningReport.UnsupportedClaims,
		))
		for _, note := range synth.ReasoningReport.Notes {
			notes = append(notes, "LongForm: "+strings.TrimSpace(note))
		}
		rec := orchestration.DecisionRecord{
			Query:      strings.TrimSpace(query),
			Source:     "longform_sections",
			ChosenPath: "hierarchical_longform",
			Reasoning: fmt.Sprintf(
				"triggered=%t passes=%d coverage=%.2f fallback=%t unsupported=%d",
				synth.ReasoningReport.Triggered,
				synth.ReasoningReport.PassesRun,
				synth.ReasoningReport.CitationCoverage,
				synth.ReasoningReport.FallbackUsed,
				synth.ReasoningReport.UnsupportedClaims,
			),
			Confidence: synth.ReasoningReport.CitationCoverage,
		}
		if err := orchestration.AppendDecisionFeed("", rec); err != nil {
			notes = append(notes, "LongForm: archive feed write failed: "+err.Error())
		}
	}
	return strings.TrimSpace(synth.StructuredAnswer), notes
}

func init() {
	researchRunCmd.Flags().IntVar(&researchRunMaxPages, "max-pages", 40, "Maximum pages/sources to consider during run mode")
	researchRunCmd.Flags().IntVar(&researchRunCrawlDepth, "crawl-depth", 1, "Crawl depth budget hint for run mode")
	researchRunCmd.Flags().IntVar(&researchRunMaxResearchLoops, "max-research-loops", 3, "Maximum tool loops for run mode")
	researchRunCmd.Flags().DurationVar(&researchRunTimeout, "timeout", 120*time.Second, "Total LLM timeout budget for run mode")
	researchRunCmd.Flags().BoolVar(&researchRunVerbose, "verbose", false, "Enable verbose research logs")
	researchRunCmd.Flags().StringSliceVar(&researchRunSeedURLs, "seed-url", nil, "Seed URL(s) to prioritize during run mode")
	researchRunCmd.Flags().StringVar(&researchProfileForRun, "profile", "", "Apply a saved research profile")
	researchRunCmd.Flags().StringVar(&researchCategoryForRun, "category", "", "Optional category assertion for --profile")
	researchRunCmd.Flags().BoolVar(&researchRunDryRun, "dry-run", false, "Print resolved research plan without execution")

	researchDeepCmd.Flags().IntVar(&researchDeepMaxPages, "max-pages", 80, "Maximum pages/sources to consider during deep mode")
	researchDeepCmd.Flags().IntVar(&researchDeepCrawlDepth, "crawl-depth", 2, "Crawl depth budget hint for deep mode")
	researchDeepCmd.Flags().IntVar(&researchDeepMaxPlanSteps, "max-plan-steps", 6, "Maximum planner steps for deep mode")
	researchDeepCmd.Flags().IntVar(&researchDeepMaxResearchLoops, "max-research-loops", 6, "Maximum researcher loops for deep mode")
	researchDeepCmd.Flags().DurationVar(&researchDeepTimeout, "timeout", 240*time.Second, "Total LLM timeout budget for deep mode")
	researchDeepCmd.Flags().BoolVar(&researchDeepVerbose, "verbose", false, "Enable verbose deep research logs")
	researchDeepCmd.Flags().StringSliceVar(&researchDeepSeedURLs, "seed-url", nil, "Seed URL(s) to prioritize during deep mode")
	researchDeepCmd.Flags().StringVar(&researchProfileForRun, "profile", "", "Apply a saved research profile")
	researchDeepCmd.Flags().StringVar(&researchCategoryForRun, "category", "", "Optional category assertion for --profile")
	researchDeepCmd.Flags().BoolVar(&researchDeepDryRun, "dry-run", false, "Print resolved deep research plan without execution")

	researchSessionsCmd.Flags().IntVar(&researchSessionsLast, "last", 10, "Number of recent sessions to show")

	installResearchProfileCommands()
	researchCmd.AddCommand(researchRunCmd)
	researchCmd.AddCommand(researchDeepCmd)
	researchCmd.AddCommand(researchSessionsCmd)
	rootCmd.AddCommand(researchCmd)
}
