package taloscli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Thynaptic/P-LMv1/pkg/cognition"
	"github.com/Thynaptic/P-LMv1/pkg/memory"
	"github.com/Thynaptic/P-LMv1/pkg/router"
	"github.com/Thynaptic/P-LMv1/pkg/state"
	"github.com/Thynaptic/P-LMv1/pkg/toolflow"
	"github.com/Thynaptic/P-LMv1/pkg/tools"
	"github.com/ollama/ollama/api"
	"github.com/spf13/cobra"
)

var (
	maMaxPlanSteps     int
	maMaxResearchLoops int
	maVerbose          bool
	maAgents           string
	maParallel         bool
	maMode             string
)

var supportedSubAgents = []string{"planner", "researcher", "docsearcher", "verifier", "synthesizer"}

const (
	maDefaultFirstTokenTimeout = 75 * time.Second
	maDefaultChatTimeout       = 180 * time.Second
	maFinalLatencyBudgetMS     = 45000
)

var (
	maFirstTokenTimeout = durationFromEnv("PLM_AGENT_FIRST_TOKEN_TIMEOUT", maDefaultFirstTokenTimeout)
	maChatTimeout       = durationFromEnv("PLM_AGENT_CHAT_TIMEOUT", maDefaultChatTimeout)
)

var multiAgentCmd = &cobra.Command{
	Use:   "multi-agent [query]",
	Short: "Run an extended multi-agent pipeline (planner -> researcher -> verifier -> synthesizer).",
	Long:  `Runs a Perplexity-style multi-agent pipeline with tool arbitration, source extraction, verification, and synthesis. You can also target specific sub-agents in any order or run them in parallel.`,
	Run: func(cmd *cobra.Command, args []string) {
		if len(args) == 0 {
			printCommandStatus(cmd.OutOrStdout(), "talos multi-agent", "error")
			printSection(cmd.OutOrStdout(), "error")
			printKV(cmd.OutOrStdout(), "message", "please provide a query (example: talos multi-agent \"What changed in X this week?\")")
			return
		}
		query := strings.TrimSpace(strings.Join(args, " "))
		if query == "" {
			printCommandStatus(cmd.OutOrStdout(), "talos multi-agent", "error")
			printSection(cmd.OutOrStdout(), "error")
			printKV(cmd.OutOrStdout(), "message", "query cannot be empty")
			return
		}

		r, err := router.NewRouter()
		if err != nil {
			printCommandStatus(cmd.OutOrStdout(), "talos multi-agent", "error")
			printSection(cmd.OutOrStdout(), "error")
			printKV(cmd.OutOrStdout(), "message", fmt.Sprintf("initializing router: %v", err))
			return
		}
		initialModel := r.ResolveModel(router.ResolveRequest{
			Query:  query,
			Stage:  "plan",
			Models: r.Models,
		})
		modelCandidates := buildModelCandidates(r.Models, initialModel)
		if len(modelCandidates) == 0 {
			printCommandStatus(cmd.OutOrStdout(), "talos multi-agent", "error")
			printSection(cmd.OutOrStdout(), "error")
			printKV(cmd.OutOrStdout(), "message", "no model candidates available")
			return
		}

		mm, err := memory.NewMemoryManager()
		if err != nil {
			printCommandStatus(cmd.OutOrStdout(), "talos multi-agent", "error")
			printSection(cmd.OutOrStdout(), "error")
			printKV(cmd.OutOrStdout(), "message", fmt.Sprintf("initializing memory manager: %v", err))
			return
		}
		sm, err := state.NewManager()
		if err == nil {
			_, note := applyPersistentGoalLock(sm, query, "multi-agent")
			if strings.TrimSpace(note) != "" {
				fmt.Printf("DEBUG: %s\n", strings.TrimSpace(note))
			}
		}
		reindexer := memory.NewReindexer(mm, sm)
		reindexer.Start()
		defer reindexer.Stop()

		tc, err := tools.NewGLMToolClient()
		if err != nil {
			fmt.Printf("Warning: tool client unavailable: %v\n", err)
			fmt.Println("Proceeding without external tools.")
		}

		client, err := api.ClientFromEnvironment()
		if err != nil {
			printCommandStatus(cmd.OutOrStdout(), "talos multi-agent", "error")
			printSection(cmd.OutOrStdout(), "error")
			printKV(cmd.OutOrStdout(), "message", fmt.Sprintf("creating Ollama client: %v", err))
			return
		}

		if err := mm.AddMessage("user", query); err != nil {
			fmt.Printf("Warning: Failed to store user query in memory: %v\n", err)
		}

		selectedAgents, selErr := parseSelectedSubAgents(maAgents)
		if selErr != nil {
			printCommandStatus(cmd.OutOrStdout(), "talos multi-agent", "error")
			printSection(cmd.OutOrStdout(), "error")
			printKV(cmd.OutOrStdout(), "message", selErr.Error())
			return
		}
		mode := normalizeMultiAgentMode(maMode)
		if mode == "" {
			printCommandStatus(cmd.OutOrStdout(), "talos multi-agent", "error")
			printSection(cmd.OutOrStdout(), "error")
			printKV(cmd.OutOrStdout(), "message", fmt.Sprintf("unsupported --mode %q (supported: pipeline, planning)", strings.TrimSpace(maMode)))
			return
		}
		if mode == "planning" && len(selectedAgents) > 0 {
			printCommandStatus(cmd.OutOrStdout(), "talos multi-agent", "error")
			printSection(cmd.OutOrStdout(), "error")
			printKV(cmd.OutOrStdout(), "message", "--mode planning cannot be combined with --agents")
			return
		}
		if mode == "planning" && maParallel {
			printCommandStatus(cmd.OutOrStdout(), "talos multi-agent", "error")
			printSection(cmd.OutOrStdout(), "error")
			printKV(cmd.OutOrStdout(), "message", "--mode planning cannot be combined with --parallel")
			return
		}
		if maParallel && len(selectedAgents) == 0 {
			printCommandStatus(cmd.OutOrStdout(), "talos multi-agent", "error")
			printSection(cmd.OutOrStdout(), "error")
			printKV(cmd.OutOrStdout(), "message", "--parallel requires --agents")
			return
		}

		var (
			answer string
			refs   []string
		)
		if mode == "planning" {
			answer, refs, err = runPlanningMode(client, mm, modelCandidates, query)
		} else if len(selectedAgents) == 0 {
			answer, refs, err = runMultiAgentPipeline(client, mm, tc, modelCandidates, query)
		} else if maParallel {
			answer, refs, err = runSelectedSubAgentsParallel(client, tc, modelCandidates, query, selectedAgents)
		} else {
			answer, refs, err = runSelectedSubAgentsSequential(client, tc, modelCandidates, query, selectedAgents)
		}
		if err != nil {
			printCommandStatus(cmd.OutOrStdout(), "talos multi-agent", "error")
			printSection(cmd.OutOrStdout(), "error")
			printKV(cmd.OutOrStdout(), "message", err.Error())
			return
		}

		printCommandStatus(cmd.OutOrStdout(), "talos multi-agent", "success")
		printSection(cmd.OutOrStdout(), "result")
		fmt.Fprintln(cmd.OutOrStdout(), strings.TrimSpace(answer))
		if len(refs) > 0 {
			printSection(cmd.OutOrStdout(), "sources")
			for i, r := range refs {
				fmt.Fprintf(cmd.OutOrStdout(), "  [%d] %s\n", i+1, r)
			}
		}

		if err := mm.AddMessage("assistant", answer); err != nil {
			fmt.Printf("Warning: Failed to store assistant answer in memory: %v\n", err)
		}
	},
}

func normalizeMultiAgentMode(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", "pipeline", "default", "full":
		return "pipeline"
	case "planning", "plan", "planner":
		return "planning"
	default:
		return ""
	}
}

type evidenceRecord struct {
	Step      string
	Summary   string
	ToolLogs  []string
	SourceURL []string
}

type subAgentResult struct {
	Agent   string
	Output  string
	ToolLog []string
	Sources []string
	Err     error
}

func plannerSystemPrompt() string {
	return `You are the Planner agent.
Break the user task into concrete research steps.
Return JSON only in this format:
{"steps":["step 1","step 2","step 3"]}`
}

func researcherSystemPrompt() string {
	return `You are the Researcher agent.
You may call tools to gather evidence.
Prefer web_search for discovery, fetch_url for page content, http_request for APIs, vector_retrieve for semantic retrieval.
When done, provide a concise factual summary and include any available sources (URLs, local paths/docs, hf refs).`
}

func docSearcherSystemPrompt() string {
	return `You are the DocSearcher agent.
You may call tools to gather local evidence.
Prefer doc_search for recursive local file and directory scans.
Return concise findings with local path citations (and line hints when available).`
}

func verifierSystemPrompt() string {
	return `You are the Verifier agent.
Check consistency of collected evidence, identify conflicts/gaps, and produce concise verification notes.
Return plain text.`
}

func synthesizerSystemPrompt() string {
	return `You are the Synthesizer agent.
Write the final answer using evidence and verifier notes.
Use concise, high-signal prose.
If sources are provided, cite with [n] markers that map to the numbered source list (URLs and local refs).`
}

func parseSelectedSubAgents(raw string) ([]string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	parts := strings.Split(raw, ",")
	var out []string
	seen := make(map[string]bool)
	add := func(agent string) {
		if agent == "" || seen[agent] {
			return
		}
		seen[agent] = true
		out = append(out, agent)
	}
	for _, part := range parts {
		token := strings.ToLower(strings.TrimSpace(part))
		switch token {
		case "":
			continue
		case "all":
			for _, a := range supportedSubAgents {
				add(a)
			}
		case "planner", "plan":
			add("planner")
		case "researcher", "research":
			add("researcher")
		case "docsearcher", "docsearch", "doc-search", "searcher":
			add("docsearcher")
		case "verifier", "verify":
			add("verifier")
		case "synthesizer", "synth", "synthesis", "final":
			add("synthesizer")
		default:
			return nil, fmt.Errorf("unknown sub-agent %q (supported: planner,researcher,docsearcher,verifier,synthesizer,all)", token)
		}
	}
	return out, nil
}

func runSubAgent(
	client *api.Client,
	tc *tools.GLMToolClient,
	modelCandidates []string,
	query string,
	agent string,
	priorContext string,
) subAgentResult {
	res := subAgentResult{Agent: agent}
	var (
		systemPrompt    string
		userPrompt      string
		maxLoops        int
		allowTools      bool
		stage           string
		reflectionStage cognition.ReflectionStage
	)

	basePrompt := "User query:\n" + query
	if strings.TrimSpace(priorContext) != "" {
		basePrompt += "\n\nPrior sub-agent outputs:\n" + priorContext
	}

	switch agent {
	case "planner":
		systemPrompt = plannerSystemPrompt()
		userPrompt = basePrompt
		maxLoops = 1
		allowTools = false
		stage = "plan"
		reflectionStage = cognition.StageMAPlan
	case "researcher":
		systemPrompt = researcherSystemPrompt()
		userPrompt = basePrompt
		maxLoops = maMaxResearchLoops
		if maxLoops <= 0 {
			maxLoops = 1
		}
		allowTools = true
		stage = "research"
		reflectionStage = cognition.StageMAResearch
	case "docsearcher":
		systemPrompt = docSearcherSystemPrompt()
		userPrompt = basePrompt + "\n\nFocus on exact wording search across local docs/files. Use doc_search."
		maxLoops = 2
		allowTools = true
		stage = "docsearch"
		reflectionStage = cognition.StageMAResearch
	case "verifier":
		systemPrompt = verifierSystemPrompt()
		userPrompt = basePrompt
		maxLoops = 1
		allowTools = true
		stage = "verify"
		reflectionStage = cognition.StageMAVerify
	case "synthesizer":
		systemPrompt = synthesizerSystemPrompt()
		userPrompt = basePrompt + "\n\nProvide final answer now."
		maxLoops = 1
		allowTools = false
		stage = "final"
		reflectionStage = cognition.StageMASynthesize
	default:
		res.Err = fmt.Errorf("unsupported sub-agent: %s", agent)
		return res
	}

	resp, toolLogs, err := runAgentLoop(client, tc, modelCandidates, systemPrompt, userPrompt, maxLoops, allowTools, query, stage, maFinalLatencyBudgetMS)
	if err != nil {
		if isSelectiveInterventionRequiredError(err) {
			res.Output = selectiveInterventionErrorMessage(err)
			res.Sources = nil
			res.ToolLog = toolLogs
			return res
		}
		res.Err = err
		return res
	}
	res.Output = sanitizeModelOutput(resp)
	res.ToolLog = toolLogs
	res.Sources = extractSourceRefs(strings.Join(toolLogs, "\n") + "\n" + res.Output)
	res.Output, _ = applyReflectionGate(
		client,
		modelCandidates,
		reflectionStage,
		query,
		res.Output,
		res.Sources,
		nil,
		nil,
	)
	return res
}

func runSelectedSubAgentsSequential(
	client *api.Client,
	tc *tools.GLMToolClient,
	modelCandidates []string,
	query string,
	agents []string,
) (string, []string, error) {
	var (
		priorContext strings.Builder
		sections     []string
		allSources   []string
	)

	for _, agent := range agents {
		res := runSubAgent(client, tc, modelCandidates, query, agent, priorContext.String())
		if res.Err != nil {
			return "", nil, fmt.Errorf("%s failed: %w", agent, res.Err)
		}
		sections = append(sections, fmt.Sprintf("[%s]\n%s", strings.ToUpper(agent), strings.TrimSpace(res.Output)))
		allSources = append(allSources, res.Sources...)
		if strings.TrimSpace(res.Output) != "" {
			if priorContext.Len() > 0 {
				priorContext.WriteString("\n\n")
			}
			priorContext.WriteString(fmt.Sprintf("%s:\n%s", agent, strings.TrimSpace(res.Output)))
		}
	}

	answer := strings.Join(sections, "\n\n")
	if strings.TrimSpace(answer) == "" {
		answer = "No sub-agent output was produced."
	}
	return answer, uniqueStrings(allSources), nil
}

func runSelectedSubAgentsParallel(
	client *api.Client,
	tc *tools.GLMToolClient,
	modelCandidates []string,
	query string,
	agents []string,
) (string, []string, error) {
	results := make(map[string]subAgentResult)
	var mu sync.Mutex
	var wg sync.WaitGroup

	for _, agent := range agents {
		agent := agent
		wg.Add(1)
		go func() {
			defer wg.Done()
			res := runSubAgent(client, tc, modelCandidates, query, agent, "")
			mu.Lock()
			results[agent] = res
			mu.Unlock()
		}()
	}
	wg.Wait()

	var (
		sections   []string
		allSources []string
	)
	for _, agent := range agents {
		res, ok := results[agent]
		if !ok {
			return "", nil, fmt.Errorf("%s did not return a result", agent)
		}
		if res.Err != nil {
			return "", nil, fmt.Errorf("%s failed: %w", agent, res.Err)
		}
		sections = append(sections, fmt.Sprintf("[%s]\n%s", strings.ToUpper(agent), strings.TrimSpace(res.Output)))
		allSources = append(allSources, res.Sources...)
	}

	answer := strings.Join(sections, "\n\n")
	if strings.TrimSpace(answer) == "" {
		answer = "No sub-agent output was produced."
	}
	return answer, uniqueStrings(allSources), nil
}

func runMultiAgentPipeline(
	client *api.Client,
	mm *memory.MemoryManager,
	tc *tools.GLMToolClient,
	modelCandidates []string,
	query string,
) (string, []string, error) {
	plannerTimeout := boundedStageTimeout(maChatTimeout/4, 30*time.Second, 75*time.Second)
	researchStepTimeout := boundedStageTimeout(maChatTimeout/3, 45*time.Second, 90*time.Second)
	verifierTimeout := boundedStageTimeout(maChatTimeout/4, 30*time.Second, 60*time.Second)
	synthTimeout := boundedStageTimeout(maChatTimeout/4, 30*time.Second, 75*time.Second)

	knowledgeCtx, _, plannerInput := buildPlannerInput(mm, query)

	plannerResp, _, err := runAgentLoopWithTimeout(client, tc, modelCandidates, plannerSystemPrompt(), plannerInput, 1, false, query, "plan", 0, plannerTimeout)
	if err != nil {
		if isSelectiveInterventionRequiredError(err) {
			return selectiveInterventionErrorMessage(err), nil, nil
		}
		return "", nil, fmt.Errorf("planner failed: %w", err)
	}
	plannerResp, plannerReflectionNotes := applyReflectionGate(
		client,
		modelCandidates,
		cognition.StageMAPlan,
		query,
		plannerResp,
		nil,
		knowledgeCtx,
		nil,
	)
	for _, note := range plannerReflectionNotes {
		fmt.Println(note)
	}
	steps, autoDecomposed := parsePlanStepsV2(plannerResp, query, maMaxPlanSteps)
	if len(steps) == 0 {
		steps = []string{query}
	}
	if autoDecomposed {
		fmt.Println("Goal Decomposition v2: recursive task tree expanded automatically.")
	}
	fmt.Printf("Planning complete: %d step(s).\n", len(steps))
	fmt.Println("Planner outline:")
	for i, step := range steps {
		fmt.Printf("  %d. %s\n", i+1, summarizePlanStep(step, 180))
	}

	if maVerbose {
		fmt.Printf("DEBUG: Planner created %d step(s).\n", len(steps))
		for i, s := range steps {
			fmt.Printf("DEBUG:   %d) %s\n", i+1, s)
		}
	}

	var evidence []evidenceRecord
	allSources := make(map[string]bool)
	taskBoardID := ""
	var taskStore *state.TaskBoardStore
	if store, err := taskBoardStoreFactory(); err == nil {
		if board, boardErr := store.CreateBoardFromSteps(currentRuntimeNamespace(), query, steps); boardErr == nil {
			taskStore = store
			taskBoardID = board.ID
			_ = taskStore.Save()
			fmt.Printf("Task board active: %s\n", taskBoardID)
		}
	}

	for i, step := range steps {
		fmt.Printf("Research progress: step %d/%d - %s\n", i+1, len(steps), summarizePlanStep(step, 140))
		if taskStore != nil {
			taskID := fmt.Sprintf("task-%d", i+1)
			_ = taskStore.UpdateTaskStatus(taskBoardID, taskID, state.TaskStatusInProgress)
			_ = taskStore.Save()
			p := taskStore.Progress(taskBoardID)
			fmt.Printf("Task progression: done:%d/%d pending:%d in_progress:%d blocked:%d active:%s\n", p.Done, p.Total, p.Pending, p.InProgress, p.Blocked, taskID)
		}
		if maVerbose {
			fmt.Printf("DEBUG: Research step %d/%d\n", i+1, len(steps))
		}
		researchResp, toolLogs, runErr := runAgentLoopWithTimeout(
			client, tc, modelCandidates,
			researcherSystemPrompt(),
			"Research step:\n"+step+"\n\nOriginal query:\n"+query,
			maMaxResearchLoops,
			true,
			query,
			"research",
			0,
			researchStepTimeout,
		)
		if runErr != nil {
			if isSelectiveInterventionRequiredError(runErr) {
				return selectiveInterventionErrorMessage(runErr), nil, nil
			}
			researchResp = "Research step failed: " + runErr.Error()
		}
		srcs := extractSourceRefs(strings.Join(toolLogs, "\n") + "\n" + researchResp)
		researchResp, researchReflectionNotes := applyReflectionGate(
			client,
			modelCandidates,
			cognition.StageMAResearch,
			query,
			researchResp,
			srcs,
			knowledgeCtx,
			nil,
		)
		for _, note := range researchReflectionNotes {
			fmt.Println(note)
		}
		srcs = extractSourceRefs(strings.Join(toolLogs, "\n") + "\n" + researchResp)
		for _, s := range srcs {
			allSources[s] = true
		}

		evidence = append(evidence, evidenceRecord{
			Step:      step,
			Summary:   sanitizeModelOutput(researchResp),
			ToolLogs:  toolLogs,
			SourceURL: srcs,
		})
		if taskStore != nil {
			taskID := fmt.Sprintf("task-%d", i+1)
			_ = taskStore.UpdateTaskStatus(taskBoardID, taskID, state.TaskStatusDone)
			_ = taskStore.Save()
			p := taskStore.Progress(taskBoardID)
			fmt.Printf("Task progression: done:%d/%d pending:%d in_progress:%d blocked:%d\n", p.Done, p.Total, p.Pending, p.InProgress, p.Blocked)
		}
	}

	evidence, adversarialReport := hardenEvidenceWithAdversarialSelfPlay(evidence, knowledgeCtx)
	fmt.Println("Aether Council adversarial self-play complete.")
	symbolicAudit := buildSymbolicSiblingAudit(query, evidence, knowledgeCtx)
	verifierInput := "Original query:\n" + query +
		"\n\nEvidence:\n" + formatEvidenceForVerifier(evidence) +
		"\n\nAdversarial self-play:\n" + adversarialReport +
		"\n\nSymbolic consistency audit:\n" + symbolicAudit
	verifierResp, _, err := runAgentLoopWithTimeout(client, tc, modelCandidates, verifierSystemPrompt(), verifierInput, 1, false, query, "verify", 0, verifierTimeout)
	if err != nil {
		if isSelectiveInterventionRequiredError(err) {
			return selectiveInterventionErrorMessage(err), nil, nil
		}
		verifierResp = "Verifier unavailable: " + err.Error()
	}
	verifierResp, verifierReflectionNotes := applyReflectionGate(
		client,
		modelCandidates,
		cognition.StageMAVerify,
		query,
		verifierResp,
		nil,
		knowledgeCtx,
		nil,
	)
	for _, note := range verifierReflectionNotes {
		fmt.Println(note)
	}

	sourceList := mapKeysSorted(allSources)
	synthInput := "Query:\n" + query +
		"\n\nEvidence:\n" + formatEvidenceForSynth(evidence, sourceList) +
		"\n\nAdversarial self-play:\n" + adversarialReport +
		"\n\nSymbolic consistency audit:\n" + symbolicAudit +
		"\n\nVerifier notes:\n" + verifierResp +
		"\n\nProvide final answer now."

	finalResp, _, err := runAgentLoopWithTimeout(client, tc, modelCandidates, synthesizerSystemPrompt(), synthInput, 1, false, query, "final", maFinalLatencyBudgetMS, synthTimeout)
	if err != nil {
		if isSelectiveInterventionRequiredError(err) {
			return selectiveInterventionErrorMessage(err), nil, nil
		}
		return fallbackSynthesisFromEvidence(query, evidence, sourceList), sourceList, nil
	}

	finalAnswer := sanitizeModelOutput(finalResp)
	if strings.TrimSpace(finalAnswer) == "" {
		finalAnswer = "I couldn't synthesize a final answer from the current evidence."
	}
	finalAnswer, finalReflectionNotes := applyReflectionGate(
		client,
		modelCandidates,
		cognition.StageMASynthesize,
		query,
		finalAnswer,
		sourceList,
		knowledgeCtx,
		nil,
	)
	for _, note := range finalReflectionNotes {
		fmt.Println(note)
	}
	decision, _ := cognition.RunSymbolicSupervision(cognition.SupervisionInput{
		Stage:        cognition.StageMultiAgentMerge,
		Query:        query,
		Candidate:    finalAnswer,
		ContextFacts: knowledgeCtx,
		SourceRefs:   sourceList,
	}, cognition.DefaultSupervisionPolicy("deep"))
	if decision.Outcome == cognition.SupervisionHardVeto {
		finalAnswer = fallbackSynthesisFromEvidence(query, evidence, sourceList)
	}
	return finalAnswer, sourceList, nil
}

func buildPlannerInput(mm *memory.MemoryManager, query string) ([]string, []string, string) {
	knowledgeCtx := []string{}
	historyCtx := []string{}
	if mm != nil {
		if anchored, err := mm.ResolveAnchoredContext(query, 3, 4); err == nil {
			knowledgeCtx = anchored.Knowledge
			historyCtx = anchored.History
		} else {
			knowledgeCtx, _ = mm.RetrieveKnowledge(query, 4)
			historyCtx, _ = mm.RetrieveContext(query, 3)
		}
	}

	plannerInput := "User query:\n" + query
	if len(knowledgeCtx) > 0 {
		plannerInput += "\n\nRelevant knowledge:\n- " + strings.Join(knowledgeCtx, "\n- ")
	}
	if len(historyCtx) > 0 {
		plannerInput += "\n\nRelevant history:\n- " + strings.Join(historyCtx, "\n- ")
	}
	return knowledgeCtx, historyCtx, plannerInput
}

func runPlanningMode(
	client *api.Client,
	mm *memory.MemoryManager,
	modelCandidates []string,
	query string,
) (string, []string, error) {
	_, _, plannerInput := buildPlannerInput(mm, query)
	plannerResp, _, err := runAgentLoopWithTimeout(client, nil, modelCandidates, plannerSystemPrompt(), plannerInput, 1, false, query, "plan", 0, 45*time.Second)
	if err != nil {
		return "", nil, fmt.Errorf("planner failed: %w", err)
	}
	steps, autoDecomposed := parsePlanStepsV2(plannerResp, query, maMaxPlanSteps)
	if len(steps) == 0 {
		steps = []string{query}
	}
	if autoDecomposed {
		fmt.Println("Goal Decomposition v2: recursive task tree expanded automatically.")
	}
	taskBoardLine := ""
	if store, err := taskBoardStoreFactory(); err == nil {
		if board, boardErr := store.CreateBoardFromSteps(currentRuntimeNamespace(), query, steps); boardErr == nil {
			_ = store.Save()
			taskBoardLine = board.ID
		}
	}

	var b strings.Builder
	b.WriteString("PLANNING MODE\n")
	b.WriteString("Objective:\n")
	b.WriteString(query)
	b.WriteString("\n\nPlan:\n")
	for i, step := range steps {
		fmt.Fprintf(&b, "%d. %s\n", i+1, strings.TrimSpace(step))
	}
	b.WriteString("\nExecution Handoff:\n")
	b.WriteString("- Run full pipeline: talos multi-agent \"")
	b.WriteString(query)
	b.WriteString("\"\n")
	b.WriteString("- Run selected sub-agents: talos multi-agent \"")
	b.WriteString(query)
	b.WriteString("\" --agents researcher,verifier,synthesizer")
	if strings.TrimSpace(taskBoardLine) != "" {
		b.WriteString("\n- Task board: ")
		b.WriteString(taskBoardLine)
		b.WriteString(" (inspect with: talos tasks show)")
	}
	return strings.TrimSpace(b.String()), nil, nil
}

func currentRuntimeNamespace() string {
	if ns := strings.ToLower(strings.TrimSpace(requestedNamespace)); ns != "" {
		return ns
	}
	sm, err := state.NewManager()
	if err != nil {
		return ""
	}
	return strings.ToLower(strings.TrimSpace(sm.ActiveNamespace()))
}

func runAgentLoop(
	client *api.Client,
	tc *tools.GLMToolClient,
	modelCandidates []string,
	systemPrompt string,
	userPrompt string,
	maxLoops int,
	allowTools bool,
	taskQuery string,
	stage string,
	maxLatencyMS int,
) (string, []string, error) {
	return runAgentLoopWithTimeout(
		client,
		tc,
		modelCandidates,
		systemPrompt,
		userPrompt,
		maxLoops,
		allowTools,
		taskQuery,
		stage,
		maxLatencyMS,
		0,
	)
}

func runAgentLoopWithTimeout(
	client *api.Client,
	tc *tools.GLMToolClient,
	modelCandidates []string,
	systemPrompt string,
	userPrompt string,
	maxLoops int,
	allowTools bool,
	taskQuery string,
	stage string,
	maxLatencyMS int,
	callTimeout time.Duration,
) (string, []string, error) {
	resolvedCandidates := modelCandidates
	if resolved, err := router.ResolveRemote(router.ResolveRequest{
		Query:        taskQuery,
		Stage:        stage,
		MaxLatencyMS: maxLatencyMS,
		Models:       modelCandidates,
	}); err == nil && strings.TrimSpace(resolved) != "" {
		resolvedCandidates = buildModelCandidates(modelCandidates, resolved)
	}

	messages := []api.Message{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: userPrompt},
	}
	var toolLogs []string

	for i := 0; i < maxInt(maxLoops, 1); i++ {
		resp, modelUsed, err := callLLMWithFallbackWithTimeout(client, resolvedCandidates, messages, callTimeout)
		if err != nil {
			return "", toolLogs, err
		}
		if maVerbose {
			fmt.Printf("DEBUG: Agent call %d used model %s\n", i+1, modelUsed)
		}

		clean := sanitizeModelOutput(resp)
		if !allowTools || tc == nil {
			return clean, toolLogs, nil
		}

		toolCalls, hasTools := parseToolCalls(resp)
		if toolflowV3Enabled() || toolflowShadowEvalEnabled() {
			if v3Calls, v3Has, dep, v3Err := parseToolCallsV3Aware(resp); v3Err != nil {
				if maVerbose {
					fmt.Printf("DEBUG: Toolflow parser warning: %v\n", v3Err)
				}
			} else if v3Has {
				toolCalls = v3Calls
				hasTools = true
				if maVerbose && toolflowDeprecationsEnabled() {
					for _, note := range dep {
						fmt.Printf("DEBUG: Toolflow deprecation: %s\n", note)
					}
				}
			}
		}
		if !hasTools {
			return clean, toolLogs, nil
		}
		if len(toolCalls) > maxToolCallsPerTurn {
			toolCalls = toolCalls[:maxToolCallsPerTurn]
		}

		messages = append(messages, api.Message{Role: "assistant", Content: resp})
		if toolflowV3Enabled() {
			sharedBuf := newSharedExecutionBuffer(256)
			var (
				collabHub      *collaborativeStreamHub
				collabWatchers []<-chan collaborativeStreamWatcherSummary
				collabUnsubs   []func()
			)
			if collaborativeToolStreamsEnabled() {
				collabHub = newCollaborativeStreamHub()
				for _, watcher := range []string{"planner", "verifier", "synthesizer"} {
					ch, unsub := collabHub.subscribe(watcher, 64)
					collabWatchers = append(collabWatchers, watchCollaborativeStream(watcher, ch, 2))
					collabUnsubs = append(collabUnsubs, unsub)
				}
			}
			pivotReason := ""
			items, status, depNotes, pivoted, runErr := runToolflowV3Observed(tc, taskQuery, toolCalls, fmt.Sprintf("ma-%s-%d", stage, i), "multi-agent", stage, func(ev toolflow.ExecEvent) bool {
				sharedBuf.append(ev)
				if collabHub != nil {
					collabHub.broadcast(ev)
				}
				if ev.Kind != "output" && ev.Kind != "error" {
					return false
				}
				if reason, ok := detectSmokingGunStreamSignal(ev.Chunk); ok {
					if strings.TrimSpace(pivotReason) == "" {
						pivotReason = reason
					}
					return true
				}
				return false
			})
			collabSummaries := make([]collaborativeStreamWatcherSummary, 0, len(collabWatchers))
			if collabHub != nil {
				for _, unsub := range collabUnsubs {
					if unsub != nil {
						unsub()
					}
				}
				for _, resultCh := range collabWatchers {
					if resultCh == nil {
						continue
					}
					summary, ok := <-resultCh
					if !ok {
						continue
					}
					summary.Dropped = collabHub.droppedCount(summary.Watcher)
					collabSummaries = append(collabSummaries, summary)
				}
			}
			if runErr != nil {
				if isSelectiveInterventionRequiredError(runErr) {
					return "", toolLogs, runErr
				}
				if maVerbose {
					fmt.Printf("DEBUG: Toolflow V3 failed (fallback to legacy): %v\n", runErr)
				}
			} else {
				if maVerbose && toolflowDeprecationsEnabled() {
					for _, note := range depNotes {
						fmt.Printf("DEBUG: Toolflow deprecation: %s\n", note)
					}
				}
				if maVerbose && toolflowTraceEnabled() {
					fmt.Print(status)
				}
				if len(collabSummaries) > 0 {
					collabStatus := formatCollaborativeStreamStatus(collabSummaries)
					if maVerbose {
						fmt.Print(collabStatus)
					}
					toolLogs = append(toolLogs, strings.TrimSpace(collabStatus))
				}
				if pivoted {
					latest := sharedBuf.latest(4)
					if maVerbose && len(latest) > 0 {
						for _, r := range latest {
							if strings.TrimSpace(r.Chunk) == "" {
								continue
							}
							fmt.Printf("DEBUG: Stream shard (%s/%s): %s\n", r.Tool, r.Kind, truncateForModel(r.Chunk))
						}
					}
					if strings.TrimSpace(pivotReason) == "" {
						pivotReason = "high-signal anomaly detected in streamed tool output"
					}
					if hitlStreamTriggerEnabled() {
						decision := promptHITL(
							fmt.Sprintf("stream-pivot-%s-%d", stage, i),
							"Collaborative stream detected a high-signal anomaly:\n"+pivotReason+"\nApprove pivot-and-continue or reject to halt this agent stage.",
							[]hitlDecision{hitlApprove, hitlReject, hitlDefer},
						)
						if decision == hitlReject {
							return "", toolLogs, fmt.Errorf("HITL rejected stream pivot for stage=%s reason=%s", stage, pivotReason)
						}
					}
					toolLogs = append(toolLogs, "Council stream pivot: "+pivotReason)
					messages = append(messages, api.Message{
						Role: "user",
						Content: "Streaming council alert: " + pivotReason + ". " +
							"Pivot the remaining research plan immediately. " +
							"Prioritize verification of this anomaly before further broad search.",
					})
				}
				for _, item := range items {
					out := item.Output
					if item.Err != nil {
						if isSelectiveInterventionRequiredError(item.Err) {
							return "", toolLogs, item.Err
						}
						out = "Error executing tool: " + item.Err.Error()
					}
					toolLogs = append(toolLogs, out)
					msg := "Tool result (" + item.Tool + "): " + truncateForModel(out) + "\nContinue."
					messages = append(messages, api.Message{Role: "user", Content: msg})
				}
				continue
			}
		}
		runToolflowShadowEval(taskQuery, toolCalls, fmt.Sprintf("ma-shadow-%s-%d", stage, i))

		for _, raw := range toolCalls {
			call, note := arbitrateToolCall(raw, taskQuery)
			if maVerbose && note != "" {
				fmt.Printf("DEBUG: Tool arbitration: %s\n", note)
			}
			out, err := executeToolCall(tc, call)
			if err != nil {
				if isSelectiveInterventionRequiredError(err) {
					return "", toolLogs, err
				}
				out = "Error executing tool: " + err.Error()
			}
			toolLogs = append(toolLogs, out)
			msg := "Tool result (" + call.Tool + "): " + truncateForModel(out) + "\nContinue."
			messages = append(messages, api.Message{Role: "user", Content: msg})
		}
	}

	return "Reached agent loop limit without final response.", toolLogs, nil
}

func callLLMWithFallback(client *api.Client, modelCandidates []string, messages []api.Message) (string, string, error) {
	return callLLMWithFallbackWithTimeout(client, modelCandidates, messages, 0)
}

func callLLMWithFallbackWithTimeout(client *api.Client, modelCandidates []string, messages []api.Message, callTimeout time.Duration) (string, string, error) {
	if len(modelCandidates) == 0 {
		return "", "", fmt.Errorf("no model candidates available")
	}

	var lastErr error
	for _, modelName := range modelCandidates {
		resp, err := callLLMOnce(client, modelName, messages, callTimeout)
		if err == nil {
			return resp, modelName, nil
		}
		lastErr = err
		if maVerbose {
			fmt.Printf("DEBUG: Model %s failed: %v\n", modelName, err)
		}
		if !isRetryableAgentError(err) {
			return "", "", err
		}
	}
	return "", "", fmt.Errorf("all model candidates failed: %w", lastErr)
}

func isRetryableAgentError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	if isTransientLLMError(err) {
		return true
	}
	return strings.Contains(strings.ToLower(err.Error()), "first-token timeout")
}

func callLLMOnce(client *api.Client, modelName string, messages []api.Message, callTimeout time.Duration) (string, error) {
	prompt := ""
	if len(messages) > 0 {
		prompt = messages[len(messages)-1].Content
	}
	opts, entropy := state.ResolveEntropyOptions(prompt)
	req := &api.ChatRequest{
		Model:    modelName,
		Options:  opts,
		Messages: messages,
	}
	if maVerbose {
		fmt.Printf("DEBUG: Entropy mode=%s temperature=%.2f top_p=%.2f\n", entropy.Mode, entropy.Temperature, entropy.TopP)
	}

	effectiveTimeout := callTimeout
	if effectiveTimeout <= 0 {
		effectiveTimeout = maChatTimeout
	}
	totalCtx, totalCancel := context.WithTimeout(context.Background(), effectiveTimeout)
	defer totalCancel()
	ctx, cancel := context.WithCancel(totalCtx)
	defer cancel()

	firstTokenTimeout := maFirstTokenTimeout
	if firstTokenTimeout > effectiveTimeout {
		firstTokenTimeout = boundedStageTimeout(effectiveTimeout/2, 10*time.Second, effectiveTimeout)
	}

	var out strings.Builder
	var sawFirstToken atomic.Bool
	var firstTokenTimerTriggered atomic.Bool

	timer := time.AfterFunc(firstTokenTimeout, func() {
		if !sawFirstToken.Load() {
			firstTokenTimerTriggered.Store(true)
			cancel()
		}
	})
	defer timer.Stop()

	err := client.Chat(ctx, req, func(resp api.ChatResponse) error {
		if !sawFirstToken.Load() {
			sawFirstToken.Store(true)
			timer.Stop()
		}
		out.WriteString(resp.Message.Content)
		return nil
	})
	if err != nil {
		if firstTokenTimerTriggered.Load() && errors.Is(ctx.Err(), context.Canceled) {
			return "", fmt.Errorf("first-token timeout after %s", firstTokenTimeout)
		}
		return "", err
	}

	return out.String(), nil
}

func boundedStageTimeout(candidate, minTimeout, maxTimeout time.Duration) time.Duration {
	if candidate <= 0 {
		candidate = minTimeout
	}
	if candidate < minTimeout {
		return minTimeout
	}
	if maxTimeout > 0 && candidate > maxTimeout {
		return maxTimeout
	}
	return candidate
}

func summarizePlanStep(step string, maxLen int) string {
	clean := strings.Join(strings.Fields(strings.TrimSpace(step)), " ")
	if clean == "" {
		return "n/a"
	}
	if maxLen <= 0 || len(clean) <= maxLen {
		return clean
	}
	return clean[:maxLen] + "..."
}

func parsePlanSteps(raw, fallback string, maxSteps int) []string {
	steps, _ := parsePlanStepsV2(raw, fallback, maxSteps)
	return steps
}

func parsePlanStepsV2(raw, fallback string, maxSteps int) ([]string, bool) {
	type plan struct {
		Steps []string `json:"steps"`
	}

	trimmed := strings.TrimSpace(stripMarkdownCodeFences(stripReasoningSections(raw)))
	var p plan
	if err := json.Unmarshal([]byte(trimmed), &p); err == nil && len(p.Steps) > 0 {
		return maybeApplyGoalDecompositionV2(fallback, p.Steps, maxSteps)
	}

	lines := strings.Split(trimmed, "\n")
	var steps []string
	for _, line := range lines {
		line = strings.TrimSpace(strings.TrimLeft(line, "-*0123456789. "))
		if line != "" {
			steps = append(steps, line)
		}
	}
	steps = clampSteps(steps, maxSteps)
	if len(steps) > 0 {
		return maybeApplyGoalDecompositionV2(fallback, steps, maxSteps)
	}
	return maybeApplyGoalDecompositionV2(fallback, []string{fallback}, maxSteps)
}

func clampSteps(steps []string, maxSteps int) []string {
	var out []string
	seen := make(map[string]bool)
	for _, s := range steps {
		s = strings.TrimSpace(s)
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
		if len(out) >= maxInt(maxSteps, 1) {
			break
		}
	}
	return out
}

func formatEvidenceForVerifier(ev []evidenceRecord) string {
	var b strings.Builder
	for i, e := range ev {
		fmt.Fprintf(&b, "%d) Step: %s\nSummary: %s\n", i+1, e.Step, e.Summary)
		if len(e.SourceURL) > 0 {
			fmt.Fprintf(&b, "Sources: %s\n", strings.Join(e.SourceURL, ", "))
		}
		b.WriteString("\n")
	}
	return b.String()
}

func formatEvidenceForSynth(ev []evidenceRecord, sources []string) string {
	index := make(map[string]int)
	for i, s := range sources {
		index[s] = i + 1
	}

	var b strings.Builder
	for i, e := range ev {
		fmt.Fprintf(&b, "%d) %s\n", i+1, e.Summary)
		if len(e.SourceURL) > 0 {
			var refs []string
			for _, s := range e.SourceURL {
				if n, ok := index[s]; ok {
					refs = append(refs, fmt.Sprintf("[%d]", n))
				}
			}
			if len(refs) > 0 {
				fmt.Fprintf(&b, "Refs: %s\n", strings.Join(uniqueStrings(refs), " "))
			}
		}
	}

	if len(sources) > 0 {
		b.WriteString("\nNumbered sources:\n")
		for i, s := range sources {
			fmt.Fprintf(&b, "[%d] %s\n", i+1, s)
		}
	}
	return b.String()
}

func truncateForModel(s string) string {
	if len(s) <= maxToolResultChars {
		return s
	}
	return s[:maxToolResultChars] + "\n...(truncated for context size)"
}

func uniqueStrings(in []string) []string {
	seen := make(map[string]bool)
	var out []string
	for _, v := range in {
		v = strings.TrimSpace(v)
		if v == "" || seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	return out
}

func mapKeysSorted(m map[string]bool) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func fallbackSynthesisFromEvidence(query string, ev []evidenceRecord, sources []string) string {
	var first string
	for _, e := range ev {
		if strings.TrimSpace(e.Summary) != "" {
			first = strings.TrimSpace(e.Summary)
			break
		}
	}
	if first == "" {
		first = "I could not fully synthesize the final answer due model latency, but research steps completed."
	}
	answer := "Query: " + query + "\n\nBest available answer:\n" + first
	if len(sources) > 0 {
		answer += "\n\nSources:\n"
		for i, s := range sources {
			answer += fmt.Sprintf("[%d] %s\n", i+1, s)
		}
	}
	return answer
}

func buildSymbolicSiblingAudit(query string, evidence []evidenceRecord, knowledgeCtx []string) string {
	_ = query
	var findings []string

	for i := 0; i < len(evidence); i++ {
		for j := i + 1; j < len(evidence); j++ {
			a := strings.TrimSpace(evidence[i].Summary)
			b := strings.TrimSpace(evidence[j].Summary)
			if a == "" || b == "" {
				continue
			}
			score := cognition.DetectContradiction(a, b)
			if score >= 0.7 {
				findings = append(findings, fmt.Sprintf(
					"- Potential contradiction (%.2f) between step %d and step %d",
					score, i+1, j+1,
				))
			}
		}
	}

	for i, ev := range evidence {
		if strings.TrimSpace(ev.Summary) == "" {
			continue
		}
		score := cognition.EvaluateLogic(ev.Summary, knowledgeCtx)
		if score >= 0.7 {
			findings = append(findings, fmt.Sprintf(
				"- Step %d appears to conflict with known context (%.2f)",
				i+1, score,
			))
		}
	}

	if len(findings) == 0 {
		return "No strong symbolic contradictions detected across sibling candidates."
	}
	return "Detected potential contradictions:\n" + strings.Join(findings, "\n")
}

func hardenEvidenceWithAdversarialSelfPlay(evidence []evidenceRecord, knowledgeCtx []string) ([]evidenceRecord, string) {
	if len(evidence) == 0 {
		return evidence, "No evidence shards available for adversarial review."
	}

	hardened := make([]evidenceRecord, 0, len(evidence))
	results := make([]cognition.SelfPlayResult, 0, len(evidence))
	for _, ev := range evidence {
		summary := strings.TrimSpace(ev.Summary)
		if summary == "" {
			hardened = append(hardened, ev)
			results = append(results, cognition.SelfPlayResult{})
			continue
		}

		ctx := make([]string, 0, len(knowledgeCtx)+len(ev.SourceURL))
		ctx = append(ctx, knowledgeCtx...)
		ctx = append(ctx, ev.SourceURL...)
		sp := cognition.ConductSelfPlay(summary, ctx)
		refined := strings.TrimSpace(sp.FinalCandidate)
		if refined != "" {
			ev.Summary = refined
		}
		hardened = append(hardened, ev)
		results = append(results, sp)
	}
	return hardened, buildAdversarialSelfPlayReport(results)
}

func buildAdversarialSelfPlayReport(results []cognition.SelfPlayResult) string {
	if len(results) == 0 {
		return "No adversarial self-play results recorded."
	}
	var b strings.Builder
	for i, r := range results {
		vector := strings.TrimSpace(r.WinningVector)
		if vector == "" {
			vector = "n/a"
		}
		finding := strings.TrimSpace(r.OpponentFinding)
		if finding == "" {
			finding = "none"
		}
		fmt.Fprintf(&b,
			"- shard %d: vector=%s flaw=%.2f contradictions=%d cycles=%d finding=%s\n",
			i+1, vector, r.MaxFlawScore, r.Contradictions, r.Cycles, summarizePlanStep(finding, 160),
		)
	}
	return strings.TrimSpace(b.String())
}

func detectSmokingGunStreamSignal(chunk string) (string, bool) {
	clean := strings.TrimSpace(chunk)
	if clean == "" {
		return "", false
	}
	l := strings.ToLower(clean)
	for _, token := range []string{
		"smoking gun", "critical contradiction", "forgery", "key leaked",
		"unauthorized", "exploit", "breach", "rce", "zero-day",
		"credential exposed", "private key", "secret", "token",
	} {
		if strings.Contains(l, token) {
			return "streaming evidence flagged: " + token, true
		}
	}
	return "", false
}

func init() {
	multiAgentCmd.Flags().IntVar(&maMaxPlanSteps, "max-plan-steps", 10, "Maximum planner decomposition steps")
	multiAgentCmd.Flags().IntVar(&maMaxResearchLoops, "max-research-loops", 3, "Maximum researcher tool loops per step")
	multiAgentCmd.Flags().BoolVar(&maVerbose, "verbose", false, "Enable verbose pipeline logs")
	multiAgentCmd.Flags().StringVar(&maMode, "mode", "pipeline", "Execution mode: pipeline|planning")
	multiAgentCmd.Flags().StringVar(&maAgents, "agents", "", "Comma-separated sub-agent order (planner,researcher,verifier,synthesizer or all)")
	multiAgentCmd.Flags().BoolVar(&maParallel, "parallel", false, "Run selected sub-agents in parallel (requires --agents)")
	rootCmd.AddCommand(multiAgentCmd)
}
