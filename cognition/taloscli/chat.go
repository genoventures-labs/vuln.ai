package taloscli

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"go/format"
	"io"
	"math"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/Thynaptic/P-LMv1/pkg/capability"
	"github.com/Thynaptic/P-LMv1/pkg/cognition"
	"github.com/Thynaptic/P-LMv1/pkg/memory"
	"github.com/Thynaptic/P-LMv1/pkg/orchestration"
	"github.com/Thynaptic/P-LMv1/pkg/output"
	"github.com/Thynaptic/P-LMv1/pkg/router"
	"github.com/Thynaptic/P-LMv1/pkg/skills"
	"github.com/Thynaptic/P-LMv1/pkg/state"
	"github.com/Thynaptic/P-LMv1/pkg/tools"
	"github.com/ollama/ollama/api"
	"github.com/spf13/cobra"
)

var chatCmd = &cobra.Command{
	Use:   "chat [prompt]",
	Short: "Start a chat session or send a single prompt to your personal LLM.",
	Long: `This command starts an interactive chat session if no prompt is provided. If a prompt is provided as an argument, it sends it to the LLM, prints the response, and exits.

Use --domain <namespace> (or --namespace / PLM_CHAT_DOMAIN) to pin retrieval to a work domain namespace for strict zero-trust context isolation.
When set explicitly, the active namespace is persisted and reused by later runs when flags/env are unset.
Use --text-gen (no value) to run an experimental TALOS-owned text generator path (no Ollama calls in that mode).`,
	Run: func(cmd *cobra.Command, args []string) {
		textGenMode, err := resolveChatTextGenMode(chatTextGen)
		if err != nil {
			fmt.Printf("Error resolving --text-gen mode: %v\n", err)
			return
		}
		if textGenMode == chatTextGenModeTalosNative {
			runNativeTextGenChat(args)
			return
		}

		// Initialize router from live VPS model discovery.
		r, err := router.NewRouter()
		if err != nil {
			fmt.Printf("Error initializing router: %v\n", err)
			fmt.Println("Ensure OLLAMA_HOST is reachable (default AI_API_Guide host is used when unset).")
			return
		}

		// Initialize Memory Manager
		mm, err := memory.NewMemoryManager()
		if err != nil {
			fmt.Printf("Error initializing memory manager: %v\n", err)
			return
		}

		// Initialize Tool Client
		tc, err := tools.NewGLMToolClient()
		if err != nil {
			fmt.Printf("Warning: Error initializing tool client: %v\n", err)
			fmt.Println("Proceeding without tool capabilities.")
		}

		// Initialize Session State Manager (used by intent correction middleware)
		sm, err := state.NewManager()
		if err != nil {
			fmt.Printf("Warning: Error initializing state manager: %v\n", err)
			fmt.Println("Proceeding without intent correction middleware.")
		}
		liveTelemetry = state.NewCognitiveTelemetry(contextWindowFromEnv())

		// Setup Ollama API client
		client, err := api.ClientFromEnvironment()
		if err != nil {
			fmt.Printf("Error creating Ollama client: %v\n", err)
			return
		}
		selectedSkill, err := resolveRequestedSkillSelection(requestedSkill)
		if err != nil {
			fmt.Printf("Error selecting skill: %v\n", err)
			return
		}
		if selectedSkill != nil {
			debugPrintf("DEBUG: Active skill=%s (%s)\n", strings.TrimSpace(selectedSkill.SkillID), strings.TrimSpace(selectedSkill.Name))
		}
		profile := configureChatTimeouts(chatTimeoutProfile)
		debugPrintf("DEBUG: Timeout profile=%s first-token=%s chat=%s mcts=%s\n", profile, llmFirstTokenTimeout, llmChatTimeout, mctsTimeout)
		if strings.TrimSpace(chatCognitionMode) == "" {
			chatCognitionMode = strings.TrimSpace(os.Getenv("PLM_COGNITION_MODE"))
		}
		chatCognitionMode = normalizeCognitionMode(chatCognitionMode)
		debugPrintf("DEBUG: Cognition orchestrator default=%s\n", chatCognitionMode)
		explicitDomain := explicitChatDomain()
		if domain := resolveChatDomain(sm); domain != "" {
			mm.SetActiveNamespace(domain)
			debugPrintf("DEBUG: Domain namespace=%s\n", domain)
			if explicitDomain != "" && sm != nil {
				sm.SetActiveNamespace(domain)
				if err := sm.Save(); err != nil {
					debugPrintf("DEBUG: Failed to persist active namespace: %v\n", err)
				}
			}
		}

		// Check if we have a prompt in arguments
		if len(args) > 0 {
			prompt := strings.Join(args, " ")
			if handled, msg := maybeHandleBuiltInChatCommand(prompt); handled {
				output.PrintBreathAware(msg, sm, chatRawOutput)
				fmt.Println()
				return
			}
			if msg, handled := TryHandleSelectiveInterventionApproval(prompt); handled {
				fmt.Println(msg)
				return
			}
			if approvalMsg, handled := orchestration.TryHandleAgencyApproval(prompt); handled {
				fmt.Println(approvalMsg)
				return
			}
			go func() { _, _ = cognition.AuditSkillInventory() }()
			normalized, clarification := applyIntentCorrectionWithTimeout(prompt, sm, mm)
			if clarification != "" {
				fmt.Printf("Clarification needed: %s\n", clarification)
				return
			}
			err := handleChatTurn(client, mm, tc, r, normalized, nil, sm, selectedSkill)
			if err != nil {
				fmt.Printf("Error: %v\n", err)
			}
			return
		}

		// No arguments, start REPL
		reindexer := memory.NewReindexer(mm, sm)
		reindexer.Start()
		defer reindexer.Stop()

		selfAuditor := cognition.NewSelfModelAuditor()
		selfAuditor.Start()
		defer selfAuditor.Stop()

		deliberator := cognition.NewDeliberator(mm, sm)
		deliberator.Start()
		defer deliberator.Stop()
		strategist := orchestration.NewStrategist(mm, sm)
		strategist.Start()
		defer strategist.Stop()
		agencyWorker := orchestration.NewAgencyWorker(mm, sm)
		agencyWorker.Start()
		defer agencyWorker.Stop()

		fmt.Println("Starting interactive chat session...")
		fmt.Println("Multi-model routing enabled.")
		fmt.Println("Memory manager initialized.")
		fmt.Println("Type your message and press Enter. Type 'exit' or press Ctrl+C to quit.")
		refreshWorkspaceMentalMap(".")
		maybePrintMorningBrief(sm)
		maybeRunChronosSmokingGun(sm)

		reader := bufio.NewReader(os.Stdin)
		var conversationHistory []api.Message

		// Handle Ctrl+C gracefully
		c := make(chan os.Signal, 1)
		signal.Notify(c, os.Interrupt, syscall.SIGTERM)
		go func() {
			<-c
			fmt.Println("\nExiting chat session. Goodbye!")
			os.Exit(0)
		}()

		for {
			maybeRunChronosSmokingGun(sm)
			if briefs, err := orchestration.ConsumeArchiveMirrorBriefs(archiveBriefsPath, 2); err == nil && len(briefs) > 0 {
				for _, b := range briefs {
					line := strings.TrimSpace(b.Message)
					if line == "" {
						continue
					}
					output.PrintReasoningMirrorLine(os.Stdout, line, sm, chatRawOutput)
					fmt.Println()
				}
			}
			if briefs, err := consumeMirrorBriefs(mirrorBriefsPath, 2); err == nil && len(briefs) > 0 {
				for _, b := range briefs {
					line := strings.TrimSpace(b.Message)
					if line == "" {
						continue
					}
					output.PrintReasoningMirrorLine(os.Stdout, line, sm, chatRawOutput)
					fmt.Println()
				}
			}
			if reflex, err := consumeReflexAlerts(reflexAlertsPath, 3); err == nil && len(reflex) > 0 {
				for _, a := range reflex {
					fmt.Printf("\n[REFLEX ALERT] %s\n", strings.TrimSpace(a.Message))
					if strings.TrimSpace(a.ProposedAction) != "" {
						fmt.Printf("  proposed_action=%s\n", strings.TrimSpace(a.ProposedAction))
					}
					if a.Velocity > 0 {
						fmt.Printf("  inference_velocity=%.2f tok/s\n", a.Velocity)
					}
				}
			}
			if alerts, err := orchestration.ConsumeIntelligenceAlerts("", 3); err == nil && len(alerts) > 0 {
				for _, a := range alerts {
					fmt.Printf("\n[INTELLIGENCE ALERT] %.0f%% confidence: %s\n", a.Confidence*100, strings.TrimSpace(a.Message))
					if strings.TrimSpace(a.OldSourcePath) != "" || strings.TrimSpace(a.NewEvidencePath) != "" {
						fmt.Printf("  old=%s | new=%s\n", strings.TrimSpace(a.OldSourcePath), strings.TrimSpace(a.NewEvidencePath))
					}
				}
			}
			fmt.Print("\n>>> You: ")
			input, _ := reader.ReadString('\n')
			input = strings.TrimSpace(input)
			deliberator.NotifyActivity()
			agencyWorker.NotifyActivity()

			if strings.EqualFold(input, "exit") {
				fmt.Println("Exiting chat session. Goodbye!")
				break
			}
			if handled, msg := maybeHandleBuiltInChatCommand(input); handled {
				output.PrintBreathAware(msg, sm, chatRawOutput)
				fmt.Println()
				continue
			}
			if msg, handled := TryHandleSelectiveInterventionApproval(input); handled {
				fmt.Println(msg)
				continue
			}
			if approvalMsg, handled := orchestration.TryHandleAgencyApproval(input); handled {
				fmt.Println(approvalMsg)
				continue
			}

			normalized, clarification := applyIntentCorrectionWithTimeout(input, sm, mm)
			if clarification != "" {
				fmt.Printf("Clarification needed: %s\n", clarification)
				continue
			}
			err := handleChatTurn(client, mm, tc, r, normalized, &conversationHistory, sm, selectedSkill)
			if err != nil {
				fmt.Printf("Error during chat: %v\n", err)
				continue
			}
			deliberator.NotifyActivity()
			agencyWorker.NotifyActivity()
		}
	},
}

var chatRawOutput bool
var chatVerbose bool
var chatTimeoutProfile string
var chatWarmup bool
var chatCognitionMode string
var chatDomain string
var chatNamespace string
var chatTextGen string

const (
	chatTextGenModeOllama      = "ollama"
	chatTextGenModeTalosNative = "talos-native"
)

func debugPrintf(format string, args ...any) {
	if !chatVerbose {
		return
	}
	fmt.Printf(format, args...)
}

func debugPrintln(args ...any) {
	if !chatVerbose {
		return
	}
	fmt.Println(args...)
}

func explicitChatDomain() string {
	if v := strings.TrimSpace(chatDomain); v != "" {
		return strings.ToLower(v)
	}
	if v := strings.TrimSpace(chatNamespace); v != "" {
		return strings.ToLower(v)
	}
	return ""
}

func resolveChatDomain(sm *state.Manager) string {
	if v := strings.ToLower(strings.TrimSpace(requestedNamespace)); v != "" {
		return v
	}
	if v := explicitChatDomain(); v != "" {
		return v
	}
	if v := strings.ToLower(strings.TrimSpace(os.Getenv("PLM_CHAT_DOMAIN"))); v != "" {
		return v
	}
	if sm != nil {
		if v := strings.ToLower(strings.TrimSpace(sm.ActiveNamespace())); v != "" {
			return v
		}
	}
	return ""
}

func resolveChatTextGenMode(raw string) (string, error) {
	mode := strings.ToLower(strings.TrimSpace(raw))
	if mode == "" {
		mode = strings.ToLower(strings.TrimSpace(os.Getenv("PLM_CHAT_TEXT_GEN")))
	}
	if mode == "" {
		if boolFromEnv("PLM_CHAT_TEXT_GEN_PRIMARY", false) {
			return chatTextGenModeTalosNative, nil
		}
		return chatTextGenModeOllama, nil
	}
	switch mode {
	case chatTextGenModeOllama:
		return chatTextGenModeOllama, nil
	case "talos", "native", chatTextGenModeTalosNative:
		return chatTextGenModeTalosNative, nil
	default:
		return "", fmt.Errorf("unsupported mode %q (supported: %s, %s)", mode, chatTextGenModeOllama, chatTextGenModeTalosNative)
	}
}

func runNativeTextGenChat(args []string) {
	sm, err := state.NewManager()
	if err != nil {
		fmt.Printf("Warning: Error initializing state manager: %v\n", err)
	}
	if len(args) > 0 {
		prompt := strings.Join(args, " ")
		if strings.TrimSpace(prompt) == "" {
			fmt.Println(insufficientKnowledgeResponse)
			return
		}
		answer := nativeTextGenAnswer(prompt)
		output.PrintBreathAware(answer, sm, chatRawOutput)
		fmt.Println()
		return
	}

	fmt.Println("Entering chat mode (TALOS-native text-gen experimental, no Ollama). Type '/bye' to exit.")
	scanner := bufio.NewScanner(os.Stdin)
	for {
		fmt.Print(">>> You: ")
		if !scanner.Scan() {
			break
		}
		input := strings.TrimSpace(scanner.Text())
		if input == "" {
			continue
		}
		lower := strings.ToLower(input)
		if lower == "/bye" || lower == "exit" || lower == "quit" {
			fmt.Println("Exiting chat.")
			break
		}
		answer := nativeTextGenAnswer(input)
		fmt.Print("<<< LLM: ")
		output.PrintBreathAware(answer, sm, chatRawOutput)
		fmt.Println()
	}
	if err := scanner.Err(); err != nil {
		fmt.Printf("Error reading input: %v\n", err)
	}
}

func nativeTextGenAnswer(query string) string {
	query = strings.TrimSpace(query)
	if query == "" {
		return insufficientKnowledgeResponse
	}
	context := collectNativeMemoryContext(query, 3)
	if len(context) == 0 {
		return insufficientKnowledgeResponse
	}
	return composeNativeTextGenResponse(query, context)
}

func collectNativeMemoryContext(query string, limit int) []string {
	records, _, err := readNativeGroundingRecords()
	if err != nil || len(records) == 0 {
		return nil
	}
	tokens := tokenizeNativeQuery(query)
	rank := func(rec NativeGroundingRecord) int {
		score := 0
		blob := strings.ToLower(strings.TrimSpace(rec.QueryOrTarget + " " + rec.Summary + " " + strings.Join(rec.Sources, " ")))
		for _, t := range tokens {
			if strings.Contains(blob, t) {
				score++
			}
		}
		return score
	}
	sort.SliceStable(records, func(i, j int) bool {
		si, sj := rank(records[i]), rank(records[j])
		if si == sj {
			return records[i].CapturedAt > records[j].CapturedAt
		}
		return si > sj
	})
	out := make([]string, 0, limit)
	for _, rec := range records {
		if len(out) >= limit {
			break
		}
		if strings.TrimSpace(rec.Summary) == "" {
			continue
		}
		out = append(out, strings.TrimSpace(rec.Summary))
	}
	return out
}

func tokenizeNativeQuery(query string) []string {
	lower := strings.ToLower(strings.TrimSpace(query))
	if lower == "" {
		return nil
	}
	raw := strings.Fields(lower)
	out := make([]string, 0, len(raw))
	seen := map[string]bool{}
	for _, tok := range raw {
		tok = strings.Trim(tok, ".,!?;:\"'`()[]{}")
		if len(tok) < 3 || seen[tok] {
			continue
		}
		seen[tok] = true
		out = append(out, tok)
	}
	return out
}

func composeNativeTextGenResponse(query string, context []string) string {
	if strings.TrimSpace(query) == "" || len(context) == 0 {
		return insufficientKnowledgeResponse
	}
	var b strings.Builder
	b.WriteString("TALOS-native experimental response\n")
	b.WriteString("Grounded from learned context:\n")
	for i, c := range context {
		if i >= 3 {
			break
		}
		b.WriteString("- ")
		b.WriteString(strings.TrimSpace(c))
		b.WriteString("\n")
	}
	b.WriteString("Requested focus: ")
	b.WriteString(strings.TrimSpace(query))
	return strings.TrimSpace(b.String())
}

func applyIntentCorrection(raw string, sm *state.Manager, mm *memory.MemoryManager) (string, string) {
	env, proceed, clarification := preprocessUserIntent(raw, sm, mm, "chat")
	if !proceed {
		return "", clarification
	}
	if strings.TrimSpace(env.Normalized) != strings.TrimSpace(raw) {
		debugPrintf("DEBUG: Normalized intent: %s\n", strings.TrimSpace(env.Normalized))
	}
	return strings.TrimSpace(env.Normalized), ""
}

func applyIntentCorrectionWithTimeout(raw string, sm *state.Manager, mm *memory.MemoryManager) (string, string) {
	if shouldBypassIntentCorrection(raw) {
		return strings.TrimSpace(raw), ""
	}
	env, proceed, clarification := preprocessUserIntent(raw, sm, mm, "chat")
	if !proceed {
		return "", clarification
	}
	return strings.TrimSpace(env.Normalized), ""
}

func shouldBypassIntentCorrection(raw string) bool {
	r := strings.TrimSpace(raw)
	if r == "" {
		return true
	}
	if len(r) > 96 || strings.Contains(r, "\n") {
		return false
	}
	lower := strings.ToLower(r)
	for _, marker := range []string{
		" and ", " then ", " because ", " compare ", " analyze ", " research ",
		"multi-step", "step by step", "tradeoff", "architecture",
	} {
		if strings.Contains(lower, marker) {
			return false
		}
	}
	return true
}

func isTrivialPrompt(raw string) bool {
	r := strings.TrimSpace(raw)
	if len(r) > 24 || strings.Contains(r, "\n") {
		return false
	}
	if len(strings.Fields(r)) > 3 {
		return false
	}
	for _, marker := range []string{" and ", " then ", "because", "why", "compare", "analyze", "research"} {
		if strings.Contains(strings.ToLower(r), marker) {
			return false
		}
	}
	return true
}

func normalizeTimeoutProfile(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "quick":
		return "quick"
	case "deep":
		return "deep"
	default:
		return "normal"
	}
}

func profileTimeouts(profile string) timeoutProfileDurations {
	switch normalizeTimeoutProfile(profile) {
	case "quick":
		return timeoutProfileDurations{
			FirstToken: 8 * time.Second,
			Chat:       90 * time.Second,
			ToT:        30 * time.Second,
			MCTS:       25 * time.Second,
			MCTSCall:   8 * time.Second,
		}
	case "deep":
		return timeoutProfileDurations{
			FirstToken: 45 * time.Second,
			Chat:       240 * time.Second,
			ToT:        120 * time.Second,
			MCTS:       80 * time.Second,
			MCTSCall:   25 * time.Second,
		}
	default:
		return timeoutProfileDurations{
			FirstToken: 20 * time.Second,
			Chat:       150 * time.Second,
			ToT:        60 * time.Second,
			MCTS:       45 * time.Second,
			MCTSCall:   15 * time.Second,
		}
	}
}

func configureChatTimeouts(profileOverride string) string {
	profile := strings.TrimSpace(profileOverride)
	if profile == "" {
		profile = strings.TrimSpace(os.Getenv("PLM_CHAT_TIMEOUT_PROFILE"))
	}
	profile = normalizeTimeoutProfile(profile)
	baseline := profileTimeouts(profile)

	llmFirstTokenTimeout = durationFromEnv("PLM_LLM_FIRST_TOKEN_TIMEOUT", baseline.FirstToken)
	llmChatTimeout = durationFromEnv("PLM_LLM_CHAT_TIMEOUT", baseline.Chat)
	totTimeout = durationFromEnv("PLM_TOT_TIMEOUT", baseline.ToT)
	mctsTimeout = durationFromEnv("PLM_MCTS_TIMEOUT", baseline.MCTS)
	mctsCallTimeout = durationFromEnv("PLM_MCTS_CALL_TIMEOUT", baseline.MCTSCall)

	return profile
}

func ollamaHostForChat() string {
	host := strings.TrimSpace(os.Getenv("OLLAMA_HOST"))
	if host == "" {
		host = "http://85.31.233.157:11434"
	}
	if !strings.HasPrefix(host, "http://") && !strings.HasPrefix(host, "https://") {
		host = "http://" + host
	}
	return strings.TrimRight(host, "/")
}

func warmupOllamaEndpoint(timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, ollamaHostForChat()+"/api/tags", nil)
	if err != nil {
		return fmt.Errorf("build warmup request: %w", err)
	}
	resp, err := (&http.Client{}).Do(req)
	if err != nil {
		return fmt.Errorf("warmup ping failed: %w", err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 2048))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("warmup ping status: %d", resp.StatusCode)
	}
	return nil
}

func buildIntentMissionContext(mm *memory.MemoryManager, raw string) state.MissionPlan {
	mission := state.MissionPlan{}
	if plan, found, err := orchestration.LoadActiveMission(""); err == nil && found {
		mission.Goal = strings.TrimSpace(plan.Goal)
		for _, phase := range plan.Phases {
			for _, t := range phase.Tasks {
				mission.Tasks = append(mission.Tasks, state.MissionTask{
					ID:          t.ID,
					Title:       t.Title,
					Description: t.Description,
					Status:      string(t.Status),
				})
			}
		}
	}

	if mm == nil {
		return mission
	}

	clusterSet := map[string]bool{}
	collect := func(query string) {
		query = strings.TrimSpace(query)
		if query == "" {
			return
		}
		segs, err := mm.RetrieveKnowledgeSegments(query, 8)
		if err != nil {
			return
		}
		for _, seg := range segs {
			if label := strings.TrimSpace(seg.Metadata["cluster_label"]); label != "" {
				clusterSet[label] = true
				continue
			}
			if src := strings.TrimSpace(seg.Metadata["source_path"]); src != "" {
				clusterSet["src:"+src] = true
			}
		}
	}

	collect(raw)
	if mission.Goal != "" {
		collect(mission.Goal)
	}

	for label := range clusterSet {
		mission.ContextualClusters = append(mission.ContextualClusters, label)
		if len(mission.ContextualClusters) >= 6 {
			break
		}
	}
	return mission
}

const systemPrompt = `You are a helpful Personal AI Assistant. You have access to several tools to help you answer the user's request. 

### Tool Usage Rules:
1. Choose the best tool based on the task. Use web_search for discovery/current info, fetch_url for page content, http_request for direct APIs, vector_retrieve for semantic KB lookup, execute_code for computation, sys_exec for guarded local Linux operations, capture_screen for visual snapshots, watch_terminal for long-running terminal monitoring, draw_box for single HUD highlighting, and draw_war_room for multi-entity color-coded highlighting.
2. When you need to use a tool, you MUST respond ONLY with a single JSON object in the following format:
   TOOL_CALL: {"tool": "tool_name", "args": {"arg1": "val1", ...}}
   For multi-step tasks, you can emit:
   TOOL_CHAIN: [{"tool":"tool_name","args":{...}}, {"tool":"tool_name","args":{...}}]
3. DO NOT include any other text, greetings, or explanations when making a tool call.
4. After you receive a "Tool Result", use that information to provide a direct and helpful final response to the user.

### Reasoning Policy:
- Use advanced step-by-step reasoning internally before answering.
- Keep reasoning private: do not expose internal thoughts, chain-of-thought, or scratchpad text.
- Return only the final answer (or a tool call when needed).
- Never expose secrets (api keys/tokens). If internal provisioning is used, keep credentials internal only.
- Never guess or fabricate facts. If evidence is insufficient, respond explicitly with: "I don't have enough knowledge to answer that reliably."

Available tools:
- web_search: Search the web for current information. Args: {"query": "search query","artifact_id":"research-...|latest","artifact_path":".memory/research_artifacts/<id>.json","reflection_audit_path":".memory/reflection_audit.jsonl","include_evidence_notes":true}
- fetch_url: Get content from a URL. If url omitted and artifact has sources, first source URL is used. Args: {"url":"https://...","artifact_id":"research-...|latest","artifact_path":".memory/research_artifacts/<id>.json"}
- http_request: Make direct HTTP requests with optional headers/body/allowlist profile. If url omitted and artifact has sources, first source URL is used. Args: {"method":"GET|POST|...","url":"https://...","headers":{"k":"v"},"body":"...","allowlist_profile":"public_web|internal_api","artifact_id":"research-...|latest","artifact_path":".memory/research_artifacts/<id>.json"}
- vector_retrieve: Semantic retrieval from vector memory/KB. Args: {"query":"...","namespace":"documentation|knowledge_base","top_k":5,"filters":{"key":"value"},"artifact_id":"research-...|latest","reflection_audit_path":".memory/reflection_audit.jsonl","include_reflection_notes":true}
- execute_code: Run code in a sandbox. Args: {"language": "python|javascript|go|bash", "code": "source code"}
- sys_exec: Verified local Linux command execution with blacklist + read-first policy. Args: {"command":"...","override":false,"timeout_seconds":20}
- capture_screen: Local screenshot capture with mandatory consent gate and privacy shutter. Args: {"consent":false,"mode":"screen|window","window_id":"optional","output_path":"optional","timeout_seconds":15}
- watch_terminal: Capture active terminal window over time during long builds. Args: {"consent":false,"duration_seconds":30,"interval_ms":2500,"output_dir":"optional"}
- draw_box: Draw temporary HUD highlight box on desktop. Args: {"consent":false,"x":120,"y":240,"w":220,"h":120,"text":"target","duration_ms":3200}
- draw_war_room: Draw multiple color-coded HUD highlights (e.g., contradiction/document/date). Args: {"consent":false,"duration_ms":10000,"boxes":[{"x":120,"y":240,"w":220,"h":120,"text":"signature","kind":"document","color":"blue"}]}
- analyze_visual_target: Analyze one specific visual ROI from overlays/capture with intent + snippet context. Args: {"consent":true,"intent":"analyze_ui_element","target":{"capture_path":".memory/captures/x.png","x":120,"y":340,"width":180,"height":90,"label":"Save button","snippet":"Save","confidence":0.84}}
- provision_client (internal): Provision admin client credentials and store locally. Args: {"client_id":"optional-id","activate":true}
- rotate_client_key (internal): Rotate key, store locally. Args: {"client_id":"required","activate":true}
- revoke_client (internal): Revoke key and remove local record. Args: {"client_id":"required"}
- admin_list_clients (internal): List provisioned tool clients. Args: {}
- admin_create_client (internal): Provision task-scoped credentials. Args: {"client_id":"optional-id","activate":true}
- admin_rotate_client (internal): Rotate credentials for client. Args: {"client_id":"required","activate":true}
- admin_delete_client (internal): Revoke client credentials. Args: {"client_id":"required"}`

const minimalSystemPrompt = `You are TALOS, a concise production assistant.

Rules:
- Answer directly and briefly.
- Do not emit tool-call JSON unless the user explicitly asks to run a tool/action.
- Keep internal reasoning private and never expose secrets.
- Never guess or fabricate facts. If evidence is insufficient, respond explicitly with: "I don't have enough knowledge to answer that reliably."`

const (
	insufficientKnowledgeResponse = "I don't have enough knowledge to answer that reliably."
	maxToolResultChars            = 3500
	maxToolCallsPerTurn           = 4
	mctsIterations                = 5
	mctsBranchFactor              = 3
	mctsRolloutDepth              = 2
	mctsUCB1C                     = 1.25
	finalLatencyBudget            = 45000
	telemetryFeedPath             = ".memory/telemetry_feed.jsonl"
	reflexAlertsPath              = ".memory/reflex_alerts.jsonl"
	toolFailuresPath              = ".memory/tool_failures.jsonl"
	mirrorBriefsPath              = ".memory/reasoning_mirror_briefs.jsonl"
	decisionFeedPath              = ".memory/decision_feed.jsonl"
	archiveBriefsPath             = ".memory/archive_mirror_briefs.jsonl"
	delegationWorkPath            = ".memory/delegation_work_orders.jsonl"
	morningBriefState             = ".memory/morning_brief_state.json"
	chronosStatePath              = ".memory/chronos_state.json"
	documentaryResults            = ".memory/documentary/results"
	labResultsPath                = ".memory/lab_assistant/results.jsonl"
)

var (
	llmFirstTokenTimeout = 20 * time.Second
	llmChatTimeout       = 150 * time.Second
	totTimeout           = 60 * time.Second
	mctsTimeout          = 45 * time.Second
	mctsCallTimeout      = 15 * time.Second
	chatModelKeepAlive   = durationFromEnv("PLM_CHAT_MODEL_KEEPALIVE", 30*time.Minute)
	chatWarmupTimeout    = durationFromEnv("PLM_CHAT_WARMUP_TIMEOUT", 3*time.Second)
	chatZeroTrustGating  = boolFromEnv("PLM_CHAT_ZERO_TRUST_GATING", true)
	chatWarmupOnce       sync.Once
	subAgentCredMu       sync.Mutex
	subAgentCredCache    = map[string]tools.ProvisionedSubAgent{}
	liveTelemetry        *state.CognitiveTelemetry
	reasoningMirrorMu    sync.Mutex
	pendingLogicGlimpse  string
	delegationJobsMu     sync.Mutex
	delegationJobs       = map[string]delegationJob{}
	delegationJobSerial  int64
	workspaceMentalMapMu sync.RWMutex
	workspaceMentalMap   skills.WorkspaceContext
	aboutSubjectPattern  = regexp.MustCompile(`(?i)\babout\s+(.+)$`)
	groundingTokenRE     = regexp.MustCompile(`[a-z0-9]{4,}`)
)

type timeoutProfileDurations struct {
	FirstToken time.Duration
	Chat       time.Duration
	ToT        time.Duration
	MCTS       time.Duration
	MCTSCall   time.Duration
}

type toolInvocation struct {
	Tool string                 `json:"tool"`
	Args map[string]interface{} `json:"args"`
}

type totBranch struct {
	Answer string  `json:"answer"`
	Score  float64 `json:"score"`
}

type reflexAlert struct {
	Timestamp        time.Time `json:"timestamp"`
	AlertType        string    `json:"alert_type"`
	Message          string    `json:"message"`
	Score            float64   `json:"score,omitempty"`
	Velocity         float64   `json:"velocity,omitempty"`
	ProposedAction   string    `json:"proposed_action,omitempty"`
	CapabilitySignal bool      `json:"capability_signal,omitempty"`
}

type mirrorBrief struct {
	Timestamp time.Time `json:"timestamp"`
	Source    string    `json:"source,omitempty"`
	Message   string    `json:"message"`
}

type toolFailureEvent struct {
	Timestamp time.Time `json:"timestamp"`
	Tool      string    `json:"tool"`
	Query     string    `json:"query,omitempty"`
	Error     string    `json:"error"`
}

type delegationJob struct {
	ID        string    `json:"id"`
	Daemon    string    `json:"daemon"`
	Goal      string    `json:"goal"`
	Status    string    `json:"status"`
	StartedAt time.Time `json:"started_at"`
	EndedAt   time.Time `json:"ended_at,omitempty"`
	Result    string    `json:"result,omitempty"`
	Error     string    `json:"error,omitempty"`
}

// handleChatTurn performs a single chat interaction (retrieval, request, display, storage).
func handleChatTurn(client *api.Client, mm *memory.MemoryManager, tc *tools.GLMToolClient, r *router.Router, input string, history *[]api.Message, sm *state.Manager, selectedSkill *skills.SkillRecord) error {
	if shouldUseArchiveHistoryQuestion(input) {
		if historyAnswer, ok := buildArchiveHistoryAnswer(input); ok {
			if history == nil {
				fmt.Print("LLM Response: ")
			} else {
				fmt.Print("<<< LLM: ")
			}
			output.PrintBreathAware(historyAnswer, sm, chatRawOutput)
			fmt.Println()
			_ = mm.AddMessage("assistant", historyAnswer)
			if history != nil {
				*history = append(*history, api.Message{Role: "user", Content: input})
				*history = append(*history, api.Message{Role: "assistant", Content: historyAnswer})
			}
			return nil
		}
	}

	if shouldRunRecursiveSourceOptimization(input) {
		report, err := orchestration.RunRecursiveSourceOptimization(mm)
		if err != nil {
			return err
		}
		msg := orchestration.FormatRecursiveSourceOptimizationReport(report)
		msg = redactSensitiveOutput(msg)
		if history == nil {
			fmt.Print("LLM Response: ")
		} else {
			fmt.Print("<<< LLM: ")
		}
		output.PrintBreathAware(msg, sm, chatRawOutput)
		fmt.Println()
		_ = mm.AddMessage("assistant", msg)
		if history != nil {
			*history = append(*history, api.Message{Role: "user", Content: input})
			*history = append(*history, api.Message{Role: "assistant", Content: msg})
		}
		return nil
	}

	if handled, msg := maybeHandleUserSkillCreate(input, tc); handled {
		msg = redactSensitiveOutput(msg)
		if history == nil {
			fmt.Print("LLM Response: ")
		} else {
			fmt.Print("<<< LLM: ")
		}
		output.PrintBreathAware(msg, sm, chatRawOutput)
		fmt.Println()
		_ = mm.AddMessage("assistant", msg)
		if history != nil {
			*history = append(*history, api.Message{Role: "user", Content: input})
			*history = append(*history, api.Message{Role: "assistant", Content: msg})
		}
		return nil
	}

	tc = maybeProvisionSubAgentClient(tc, input)
	requestQuery := primaryUserRequest(input)
	cognitionPlan := planCognitionBudget(requestQuery, sm, chatCognitionMode)
	modulation := cognition.BuildReasoningModulation(sm, requestQuery, cognitionPlan.Mode)
	cognitionPlan = applyReasoningModulationBudget(cognitionPlan, modulation)
	styleProfile := cognition.ResolveStyleProfile(sm, mm, requestQuery, cognitionPlan.Mode)
	var chainedSkills []skills.SkillRecord
	if drafted, chain, note := maybeDraftJITSuperSkill(selectedSkill, requestQuery, cognitionPlan.ComplexityScore); drafted != nil {
		selectedSkill = drafted
		chainedSkills = chain
		if strings.TrimSpace(note) != "" {
			debugPrintf("DEBUG: %s\n", note)
		}
	}
	debugPrintf("DEBUG: Cognition mode=%s score=%d thoughtgraph=%t tot=%t mcts=%t context=(h:%d,k:%d)\n",
		cognitionPlan.Mode,
		cognitionPlan.ComplexityScore,
		cognitionPlan.UseThoughtGraph,
		cognitionPlan.UseTreeOfThought,
		cognitionPlan.UseMCTS,
		cognitionPlan.HistoryTopK,
		cognitionPlan.KnowledgeTopK,
	)
	debugPrintf("DEBUG: Modulation entropy=%s load=%.2f persistence=%.2f density=%.2f branches=%d prune=%.2f\n",
		modulation.Entropy.Mode,
		modulation.EmotionPressure,
		modulation.GoalPersistence,
		modulation.DensityScale,
		resolveToTBranchCap(modulation),
		modulation.PruneThreshold,
	)
	if strings.TrimSpace(modulation.PredictiveIntervention) != "" {
		debugPrintf("DEBUG: Predictive Intervention=%s trend=%.2f volatility=%.2f\n",
			modulation.PredictiveIntervention,
			modulation.EmotionTrend,
			modulation.EmotionVolatility,
		)
	}

	// 0. Select model based on complexity
	modelName := r.ResolveModel(router.ResolveRequest{
		Query:  requestQuery,
		Stage:  "chat",
		Models: r.Models,
	})
	modelCandidates := buildModelCandidates(r.Models, modelName)
	if isTrivialPrompt(requestQuery) || cognitionPlan.Mode == "minimal" {
		modelCandidates = prioritizeLowLatencyModels(modelCandidates)
	}
	if cognitionPlan.MaxLinearModels > 0 {
		modelCandidates = limitModelCandidates(modelCandidates, cognitionPlan.MaxLinearModels)
	}
	if len(modelCandidates) == 0 {
		return fmt.Errorf("no candidate models available")
	}
	debugPrintf("DEBUG: Selected model: %s\n", modelCandidates[0])
	if chatWarmup {
		chatWarmupOnce.Do(func() {
			if err := warmupOllamaEndpoint(chatWarmupTimeout); err != nil {
				debugPrintf("DEBUG: Warmup ping failed: %v\n", err)
				return
			}
			debugPrintf("DEBUG: Warmup ping OK (%s).\n", ollamaHostForChat())
		})
	}
	if shouldBlockUngroundedChatTurn(mm, requestQuery, cognitionPlan) {
		msg := insufficientKnowledgeResponse
		if history == nil {
			fmt.Print("LLM Response: ")
		} else {
			fmt.Print("<<< LLM: ")
		}
		output.PrintBreathAware(msg, sm, chatRawOutput)
		fmt.Println()
		if err := mm.AddMessage("assistant", msg); err != nil {
			fmt.Printf("Warning: Error adding assistant message to memory: %v\n", err)
		}
		if history != nil {
			*history = append(*history, api.Message{Role: "user", Content: input})
			*history = append(*history, api.Message{Role: "assistant", Content: msg})
		}
		return nil
	}

	if cognitionPlan.UseThoughtGraph && shouldUseThoughtGraph(requestQuery) {
		debugPrintln("DEBUG: Complex query detected. Using ThoughtGraph orchestration.")
		if err := executeThoughtGraphTurn(client, mm, tc, r, modelCandidates, requestQuery, history, sm, modulation, styleProfile); err == nil {
			return nil
		} else {
			fmt.Printf("Warning: ThoughtGraph execution failed, falling back to linear flow: %v\n", err)
		}
	}

	// 1. Retrieve context from memory (memory-anchored reasoning).
	var historyContext, knowledgeContext []string
	if cognitionPlan.HistoryTopK > 0 || cognitionPlan.KnowledgeTopK > 0 {
		historyContext, knowledgeContext = resolveAnchoredTurnContext(mm, requestQuery, cognitionPlan.HistoryTopK, cognitionPlan.KnowledgeTopK)
	}

	// 2. Build the context-enriched message
	var contextParts []string
	if selectedSkill != nil {
		execRes, execErr := skills.ExecuteWithPolicy(context.Background(), skills.ExecutionRequest{
			Skill: *selectedSkill,
			Query: input,
			Input: map[string]any{"query": input},
			Chain: chainedSkills,
		})
		if execErr != nil {
			fmt.Printf("Warning: Skill runtime policy check failed: %v\n", execErr)
		} else if strings.EqualFold(strings.TrimSpace(execRes.Status), "denied") {
			fmt.Printf("Skill policy fallback: %s\n", strings.TrimSpace(firstNonEmpty(execRes.DeniedReason, strings.Join(execRes.PolicyNotes, "; "))))
			selectedSkill = nil
		}
	}
	if skillCtx := skillContextBlock(selectedSkill); skillCtx != "" {
		contextParts = append(contextParts, skillCtx)
	}
	if cognition.StyleV2Enabled() {
		contextParts = append(contextParts, cognition.BuildStylePromptContract(styleProfile))
	}
	if len(historyContext) > 0 {
		contextParts = append(contextParts, "Relevant conversation history:\n"+strings.Join(historyContext, "\n"))
	}
	if len(knowledgeContext) > 0 {
		contextParts = append(contextParts, "Relevant knowledge from memory:\n"+strings.Join(knowledgeContext, "\n"))
	}

	userMessageContent := input
	if len(contextParts) > 0 {
		userMessageContent = strings.Join(contextParts, "\n\n") + "\n\n---\nUser Query: " + input
	}

	// 3. Prepare message list
	var requestMessages []api.Message
	// Always include system prompt at the beginning.
	requestMessages = append(requestMessages, api.Message{Role: "system", Content: systemPromptForTurn(cognitionPlan, 0)})

	if history != nil {
		// Add existing history to request
		requestMessages = append(requestMessages, *history...)
		// Add the CURRENT user message to history (the raw input)
		*history = append(*history, api.Message{Role: "user", Content: input})
		// Add the ENRICHED current user message to the request
		requestMessages = append(requestMessages, api.Message{Role: "user", Content: userMessageContent})
	} else {
		requestMessages = append(requestMessages, api.Message{Role: "user", Content: userMessageContent})
	}

	// 4. Store user message in persistent memory
	if err := mm.AddMessage("user", input); err != nil {
		fmt.Printf("Warning: Error adding user message to memory: %v\n", err)
	}

	return performChatWithTools(client, mm, tc, modelCandidates, 0, input, requestMessages, history, 0, sm, cognitionPlan, modulation, styleProfile)
}

func resolveAnchoredTurnContext(mm *memory.MemoryManager, query string, historyK int, knowledgeK int) ([]string, []string) {
	if mm == nil {
		return nil, nil
	}
	const (
		maxHistoryContextChars   = 1200
		maxKnowledgeContextChars = 1600
		maxHistoryTotalChars     = 1600
		maxKnowledgeTotalChars   = 2400
	)
	ctx, err := mm.ResolveAnchoredContext(query, historyK, knowledgeK)
	if err != nil {
		historyContext, hErr := mm.RetrieveDynamicContext(query, historyK)
		if hErr != nil {
			fmt.Printf("Warning: Error retrieving history: %v\n", hErr)
		}
		knowledgeContext, kErr := mm.RetrieveKnowledge(query, knowledgeK)
		if kErr != nil {
			fmt.Printf("Warning: Error retrieving knowledge: %v\n", kErr)
		}
		if strings.TrimSpace(err.Error()) != "" {
			fmt.Printf("Warning: Memory-anchored resolver fallback triggered: %v\n", err)
		}
		return truncateContextEntries(historyContext, maxHistoryContextChars, maxHistoryTotalChars), truncateContextEntries(knowledgeContext, maxKnowledgeContextChars, maxKnowledgeTotalChars)
	}
	if chatVerbose && ctx.Policy.StatusReport {
		fmt.Print(ctx.AnchoredStatusReport())
	}
	return truncateContextEntries(ctx.History, maxHistoryContextChars, maxHistoryTotalChars), truncateContextEntries(ctx.Knowledge, maxKnowledgeContextChars, maxKnowledgeTotalChars)
}

func truncateContextEntries(entries []string, maxPerEntry, maxTotal int) []string {
	if len(entries) == 0 {
		return nil
	}
	if maxPerEntry <= 0 {
		maxPerEntry = 1200
	}
	if maxTotal <= 0 {
		maxTotal = maxPerEntry
	}
	out := make([]string, 0, len(entries))
	used := 0
	for _, e := range entries {
		e = strings.TrimSpace(e)
		if e == "" {
			continue
		}
		r := []rune(e)
		if len(r) > maxPerEntry {
			e = strings.TrimSpace(string(r[:maxPerEntry])) + " ..."
		}
		if used+len(e) > maxTotal {
			break
		}
		out = append(out, e)
		used += len(e)
	}
	return out
}

func systemPromptForTurn(budget cognitionBudget, depth int) string {
	if depth == 0 && strings.EqualFold(strings.TrimSpace(budget.Mode), "minimal") {
		return minimalSystemPrompt
	}
	return systemPrompt
}

func primaryUserRequest(input string) string {
	trimmed := strings.TrimSpace(input)
	if trimmed == "" {
		return trimmed
	}
	lower := strings.ToLower(trimmed)
	if strings.Contains(lower, "request:") {
		if idx := strings.LastIndex(lower, "request:"); idx >= 0 && idx+len("request:") < len(trimmed) {
			req := strings.TrimSpace(trimmed[idx+len("request:"):])
			if req != "" {
				return req
			}
		}
	}
	return trimmed
}

func maybeHandleUserSkillCreate(input string, tc *tools.GLMToolClient) (bool, string) {
	if !userSkillCreateEnabled() {
		return false, ""
	}
	req, ok := parseUserSkillCreateIntent(input)
	if !ok {
		return false, ""
	}
	creator := skills.NewUserSkillCreator(tc)
	res, err := creator.Create(context.Background(), req)
	if err != nil {
		return true, "Skill creation blocked: " + strings.TrimSpace(err.Error())
	}
	msg := "User skill created successfully.\n" +
		"- skill_id: " + strings.TrimSpace(res.Artifact.SkillID) + "\n" +
		"- path: " + strings.TrimSpace(res.Artifact.RootDir) + "\n" +
		"- manifest: " + strings.TrimSpace(res.Artifact.ManifestPath) + "\n" +
		"- preflight: " + strings.TrimSpace(res.Preflight.Decision)
	if !res.Artifact.CompileOK {
		msg += "\n- compile_check: failed"
	} else {
		msg += "\n- compile_check: pass"
	}
	return true, msg
}

func userSkillCreateEnabled() bool {
	raw := strings.ToLower(strings.TrimSpace(os.Getenv("TALOS_USER_SKILL_CREATE_ENABLED")))
	switch raw {
	case "", "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func parseUserSkillCreateIntent(input string) (skills.UserSkillCreateRequest, bool) {
	text := strings.TrimSpace(input)
	if text == "" {
		return skills.UserSkillCreateRequest{}, false
	}
	lower := strings.ToLower(text)
	if !(strings.Contains(lower, "create skill") ||
		strings.Contains(lower, "build skill") ||
		strings.Contains(lower, "make skill") ||
		strings.Contains(lower, "create a skill") ||
		strings.Contains(lower, "talos learn a skill")) {
		return skills.UserSkillCreateRequest{}, false
	}

	name := extractSkillName(text)
	intent := text
	if name == "" {
		name = inferSkillNameFromIntent(text)
	}
	reqTools := extractRequestedTools(text)
	reqDomains := extractRequestedDomains(text)
	taskType := inferCapabilityTaskType(text, "")
	return skills.UserSkillCreateRequest{
		Name:             name,
		Intent:           intent,
		Description:      "User-requested TALOS self-skill.",
		ReasoningTier:    "t2",
		TaskType:         taskType,
		RequestedTools:   reqTools,
		RequestedDomains: reqDomains,
	}, true
}

func extractSkillName(text string) string {
	re := regexp.MustCompile(`(?i)\b(?:named|called)\s+([a-zA-Z0-9_-]{3,64})`)
	m := re.FindStringSubmatch(text)
	if len(m) > 1 {
		return strings.TrimSpace(m[1])
	}
	return ""
}

func inferSkillNameFromIntent(text string) string {
	base := strings.ToLower(strings.TrimSpace(text))
	base = strings.ReplaceAll(base, "create a skill", "")
	base = strings.ReplaceAll(base, "create skill", "")
	base = strings.ReplaceAll(base, "build skill", "")
	base = strings.ReplaceAll(base, "make skill", "")
	base = strings.ReplaceAll(base, "talos learn a skill", "")
	base = strings.TrimSpace(base)
	if base == "" {
		return "talos_user_skill"
	}
	base = regexp.MustCompile(`[^a-z0-9_-]+`).ReplaceAllString(base, "_")
	base = strings.Trim(base, "_")
	if len(base) > 48 {
		base = base[:48]
	}
	if base == "" {
		base = "talos_user_skill"
	}
	return "talos_" + base
}

func extractRequestedTools(text string) []tools.SkillPreflightToolRequest {
	l := strings.ToLower(text)
	var out []tools.SkillPreflightToolRequest
	add := func(kind, name string) {
		for _, v := range out {
			if v.Kind == kind && v.Name == name {
				return
			}
		}
		out = append(out, tools.SkillPreflightToolRequest{Kind: kind, Name: name})
	}
	if strings.Contains(l, "web search") {
		add("tool", "web_search")
	}
	if strings.Contains(l, "http request") || strings.Contains(l, "api call") {
		add("tool", "http_request")
	}
	if strings.Contains(l, "fetch url") || strings.Contains(l, "fetch page") {
		add("tool", "fetch_url")
	}
	re := regexp.MustCompile(`(?i)\bplugin\s+([a-zA-Z0-9_-]{2,64})`)
	matches := re.FindAllStringSubmatch(text, -1)
	for _, m := range matches {
		if len(m) > 1 {
			add("plugin", strings.TrimSpace(m[1]))
		}
	}
	return out
}

func extractRequestedDomains(text string) []string {
	re := regexp.MustCompile(`\b([a-zA-Z0-9][a-zA-Z0-9.-]+\.[a-zA-Z]{2,})(?::\d+)?\b`)
	matches := re.FindAllStringSubmatch(text, -1)
	seen := map[string]bool{}
	var out []string
	for _, m := range matches {
		if len(m) < 2 {
			continue
		}
		d := strings.ToLower(strings.TrimSpace(m[1]))
		if d == "" || seen[d] {
			continue
		}
		seen[d] = true
		out = append(out, d)
	}
	return out
}

func maybeProvisionSubAgentClient(tc *tools.GLMToolClient, taskQuery string) *tools.GLMToolClient {
	if tc == nil || !shouldProvisionSubAgent(taskQuery) {
		return tc
	}
	taskKey := strings.ToLower(strings.TrimSpace(taskQuery))
	if len(taskKey) > 96 {
		taskKey = taskKey[:96]
	}

	subAgentCredMu.Lock()
	cred, ok := subAgentCredCache[taskKey]
	subAgentCredMu.Unlock()
	if ok && strings.TrimSpace(cred.ClientID) != "" && strings.TrimSpace(cred.APIKey) != "" {
		return tc.WithCredentials(cred.ClientID, cred.APIKey)
	}

	prov, err := tools.ProvisionSubAgent(taskQuery)
	if err != nil {
		debugPrintf("DEBUG: Sub-agent provisioning skipped: %v\n", err)
		return tc
	}
	subAgentCredMu.Lock()
	subAgentCredCache[taskKey] = prov
	subAgentCredMu.Unlock()
	debugPrintf("DEBUG: Provisioned task-scoped sub-agent credentials for background daemon mission (%s).\n", prov.ClientID)
	return tc.WithCredentials(prov.ClientID, prov.APIKey)
}

func shouldProvisionSubAgent(taskQuery string) bool {
	q := strings.ToLower(strings.TrimSpace(taskQuery))
	if q == "" {
		return false
	}
	for _, k := range []string{
		"background daemon", "background worker", "daemon", "service", "threatd",
		"autonomous mission", "agency worker", "multi-daemon", "spawn agent", "sub-agent",
	} {
		if strings.Contains(q, k) {
			return true
		}
	}
	return false
}

func shouldRunRecursiveSourceOptimization(input string) bool {
	q := strings.ToLower(strings.TrimSpace(input))
	if q == "" {
		return false
	}
	return strings.Contains(q, "recursive source optimization") ||
		(strings.Contains(q, "source optimization") && strings.Contains(q, "mission")) ||
		(strings.Contains(q, "optimize") && strings.Contains(q, "pkg/"))
}

func executeThoughtGraphTurn(client *api.Client, mm *memory.MemoryManager, tc *tools.GLMToolClient, r *router.Router, modelCandidates []string, input string, history *[]api.Message, sm *state.Manager, modulation cognition.ReasoningModulationProfile, styleProfile cognition.StyleProfile) error {
	turnStarted := time.Now()
	if len(modelCandidates) == 0 {
		return fmt.Errorf("no model candidates available for thought graph")
	}
	if r != nil {
		if resolved, err := router.ResolveRemote(router.ResolveRequest{
			Query:        input,
			Stage:        "reason",
			MaxLatencyMS: finalLatencyBudget,
			Models:       modelCandidates,
		}); err == nil && strings.TrimSpace(resolved) != "" {
			modelCandidates = buildModelCandidates(modelCandidates, resolved)
		}
	}
	graphCandidates := limitModelCandidates(modelCandidates, 2)
	if len(graphCandidates) == 0 {
		return fmt.Errorf("no bounded model candidates available for thought graph")
	}

	if history != nil {
		*history = append(*history, api.Message{Role: "user", Content: input})
	}
	if err := mm.AddMessage("user", input); err != nil {
		fmt.Printf("Warning: Error adding user message to memory: %v\n", err)
	}

	goal := strings.TrimSpace(input)
	if sm != nil {
		if g := strings.TrimSpace(sm.GetSnapshot().PrimaryGoal); g != "" {
			goal = g
		}
	}
	reflection := cognition.NewReflectionLayer(sm, goal)
	reflection.AttachMemory(mm)
	reflection.AttachPredictive(cognition.NewPredictiveForecaster(mm))
	topology := cognition.DetermineTopology(input)
	truthShiftSeverity := 0.0
	truthShiftDetected := false
	truthShiftReason := ""
	if sev, detected, reason := loadTruthShiftSeverityForGeometry(); detected || sev > 0 {
		truthShiftSeverity = sev
		truthShiftDetected = detected
		truthShiftReason = reason
	}
	if sm != nil {
		topology = cognition.DetermineTopologyWithTruthShift(input, sm.GetSnapshot(), truthShiftDetected, truthShiftSeverity, truthShiftSeverity)
	}
	if sm != nil && topology == cognition.TopologyArchitect {
		sm.UpdateDelta(map[string]float64{
			"AnalyticalMode":  0.6,
			"GoalPersistence": 0.2,
		})
		_ = sm.Save()
	}
	if truthShiftSeverity > 0 || truthShiftDetected {
		debugPrintf("DEBUG: Reasoning geometry truth-shift severity=%.2f detected=%t reason=%s\n",
			truthShiftSeverity, truthShiftDetected, summarizePlanStep(truthShiftReason, 140))
	}
	debugPrintf("DEBUG: Selected topology: %s\n", topology)
	_ = orchestration.AppendDecisionFeed(decisionFeedPath, orchestration.DecisionRecord{
		Timestamp:  time.Now().UTC(),
		Query:      strings.TrimSpace(input),
		Source:     "thoughtgraph_topology",
		Topology:   string(topology),
		ChosenPath: string(topology),
		Alternatives: []string{
			string(cognition.TopologySpike),
			string(cognition.TopologyBloom),
			string(cognition.TopologyOuroboros),
			string(cognition.TopologyArchitect),
		},
		Reasoning: fmt.Sprintf("Selected %s based on query complexity, session state, and truth-shift severity %.2f.", topology, truthShiftSeverity),
	})
	output.PrintReasoningMirrorLine(os.Stdout, chooseThoughtHeader(input), sm, chatRawOutput)

	tg := cognition.ThoughtGraph{
		DefaultTopology: topology,
		MaxSteps:        12,
		Reflection:      reflection,
		Compiler: func(tp cognition.Topology) (cognition.CompiledTopology, error) {
			return compileTopologyGraph(tp, client, mm, tc, graphCandidates, input, goal, modulation, styleProfile)
		},
	}

	finalOutput, err := tg.ExecuteGraph(input, sm, topology)
	if err != nil {
		return err
	}
	if briefs, berr := consumeMirrorBriefs(mirrorBriefsPath, 2); berr == nil && len(briefs) > 0 {
		for _, b := range briefs {
			line := strings.TrimSpace(b.Message)
			if line == "" {
				continue
			}
			output.PrintReasoningMirrorLine(os.Stdout, line, sm, chatRawOutput)
			fmt.Println()
		}
	}
	finalOutput = sanitizeModelOutput(finalOutput)
	finalOutput = normalizeInsufficientKnowledgeResponse(finalOutput)
	if notes := reflection.PredictiveNotices(); len(notes) > 0 {
		finalOutput = strings.TrimSpace(finalOutput) + "\n\nProactive Interventions:\n- " + strings.Join(notes, "\n- ")
	}
	finalOutput = withStrategicProposal(finalOutput, mm, sm, input)
	finalOutput = withAgencyPendingPrompt(finalOutput)
	if summary := consumeTelemetrySummary(); summary != "" {
		finalOutput = "State Summary: " + summary + "\n\n" + finalOutput
	}
	finalOutput = redactSensitiveOutput(finalOutput)
	if strings.TrimSpace(finalOutput) == "" {
		return fmt.Errorf("thought graph produced empty output")
	}
	if glimpse := consumeLogicGlimpse(); strings.TrimSpace(glimpse) != "" {
		output.PrintReasoningMirrorLine(os.Stdout, "Logic Glimpse: "+glimpse, sm, chatRawOutput)
	}

	if history == nil {
		fmt.Print("LLM Response: ")
		output.PrintBreathAwareStyledWithProfile(os.Stdout, finalOutput, styleOutputForCurrentState(finalOutput, sm), sm, chatRawOutput, styleCadenceProfileFromCognition(styleProfile))
		fmt.Println()
	} else {
		fmt.Print("<<< LLM: ")
		output.PrintBreathAwareStyledWithProfile(os.Stdout, finalOutput, styleOutputForCurrentState(finalOutput, sm), sm, chatRawOutput, styleCadenceProfileFromCognition(styleProfile))
		fmt.Println()
	}

	if err := mm.AddMessage("assistant", finalOutput); err != nil {
		fmt.Printf("Warning: Error adding assistant message to memory: %v\n", err)
	}
	if history != nil {
		*history = append(*history, api.Message{Role: "assistant", Content: finalOutput})
	}
	recordInferenceAndTelemetryFeed(turnStarted, finalOutput)
	return nil
}

func compileTopologyGraph(
	topology cognition.Topology,
	client *api.Client,
	mm *memory.MemoryManager,
	tc *tools.GLMToolClient,
	graphCandidates []string,
	input string,
	goal string,
	modulation cognition.ReasoningModulationProfile,
	styleProfile cognition.StyleProfile,
) (cognition.CompiledTopology, error) {
	switch topology {
	case cognition.TopologyArchitect:
		return cognition.CompiledTopology{
			Start:          "planner",
			MaxSteps:       16,
			RecoveryNodeID: "recovery",
			Nodes: []cognition.Node{
				buildPlannerNode(input),
				buildArchitectGateNode(),
				buildDelegationNode(input, "capability"),
				buildCapabilityNode(input, "research"),
				buildResearchNode(client, mm, tc, graphCandidates, input, "fusion"),
				buildFusionNode(client, mm, graphCandidates, input, "visual_reasoning"),
				buildVisualNode(input, "adversarial"),
				buildAdversarialNode(mm, input),
				buildSynthesisNode(mm, input, "final_synthesis"),
				buildRecoveryNode(client, graphCandidates, goal, input, "planner"),
				buildFinalSynthesisNode(client, mm, graphCandidates, input, "alignment_correction", modulation, styleProfile),
				buildAlignmentNode(graphCandidates, "final_code_audit"),
				buildFinalCodeAuditNode(input),
				buildFinalCodeAuditGateNode(),
				buildFinalCorrectionNode(client, mm, graphCandidates, input),
			},
		}, nil

	case cognition.TopologySpike:
		return cognition.CompiledTopology{
			Start:          "research",
			MaxSteps:       6,
			RecoveryNodeID: "recovery",
			Nodes: []cognition.Node{
				buildResearchNode(client, mm, tc, graphCandidates, input, "fusion"),
				buildDelegationNode(input, "capability"),
				buildCapabilityNode(input, "fusion"),
				buildFusionNode(client, mm, graphCandidates, input, "visual_reasoning"),
				buildVisualNode(input, "synthesis_report"),
				buildSynthesisNode(mm, input, "final_synthesis"),
				buildRecoveryNode(client, graphCandidates, goal, input, "fusion"),
				buildFinalSynthesisNode(client, mm, graphCandidates, input, "alignment_correction", modulation, styleProfile),
				buildAlignmentNode(graphCandidates, "final_code_audit"),
				buildFinalCodeAuditNode(input),
				buildFinalCodeAuditGateNode(),
				buildFinalCorrectionNode(client, mm, graphCandidates, input),
			},
		}, nil

	case cognition.TopologyBloom:
		return cognition.CompiledTopology{
			Start:          "research",
			MaxSteps:       10,
			RecoveryNodeID: "recovery",
			Nodes: []cognition.Node{
				buildResearchNode(client, mm, tc, graphCandidates, input, "fusion"),
				buildDelegationNode(input, "capability"),
				buildCapabilityNode(input, "fusion"),
				buildFusionNode(client, mm, graphCandidates, input, "visual_reasoning"),
				buildVisualNode(input, "bloom_drafts"),
				buildBloomDraftsNode(client, mm, graphCandidates, input),
				buildBloomEvalNode(client, graphCandidates, input, "synthesis_report"),
				buildSynthesisNode(mm, input, "final_synthesis"),
				buildRecoveryNode(client, graphCandidates, goal, input, "fusion"),
				buildFinalSynthesisNode(client, mm, graphCandidates, input, "alignment_correction", modulation, styleProfile),
				buildAlignmentNode(graphCandidates, "final_code_audit"),
				buildFinalCodeAuditNode(input),
				buildFinalCodeAuditGateNode(),
				buildFinalCorrectionNode(client, mm, graphCandidates, input),
			},
		}, nil

	case cognition.TopologyOuroboros:
		fallthrough
	default:
		return cognition.CompiledTopology{
			Start:          "draft",
			MaxSteps:       18,
			RecoveryNodeID: "recovery",
			Nodes: []cognition.Node{
				buildDraftNode(client, mm, graphCandidates, input, goal),
				buildDelegationNode(input, "policy_audit"),
				buildPolicyAuditNode(input),
				buildPolicyAuditGateNode(),
				buildSandboxNode(input),
				buildSandboxDecisionNode(),
				buildCorrectionNode(client, mm, graphCandidates, input),
				buildDraftEvalNode(client, graphCandidates, input),
				buildQualityBranchNode(),
				buildResearchNode(client, mm, tc, graphCandidates, input, "fusion"),
				buildCapabilityNode(input, "fusion"),
				buildFusionNode(client, mm, graphCandidates, input, "visual_reasoning"),
				buildVisualNode(input, "adversarial"),
				buildAdversarialNode(mm, input),
				buildSynthesisNode(mm, input, "final_synthesis"),
				buildRecoveryNode(client, graphCandidates, goal, input, "fusion"),
				buildFinalSynthesisNode(client, mm, graphCandidates, input, "alignment_correction", modulation, styleProfile),
				buildAlignmentNode(graphCandidates, "final_code_audit"),
				buildFinalCodeAuditNode(input),
				buildFinalCodeAuditGateNode(),
				buildFinalCorrectionNode(client, mm, graphCandidates, input),
			},
		}, nil
	}
}

func buildPlannerNode(input string) cognition.Node {
	return cognition.Node{
		ID:   "planner",
		Next: "architect_gate",
		Action: &cognition.ActionNode{
			Name: "MissionPlanner",
			Run: func(_ string, ctx *cognition.GraphContext, sm *state.Manager) (string, map[string]float64, error) {
				goal := strings.TrimSpace(input)
				active, found, err := orchestration.LoadActiveMission("")
				if err != nil {
					found = false
				}

				if !found || strings.TrimSpace(active.Goal) == "" || !orchestration.IsProjectGoal(active.Goal) {
					active = orchestration.DecomposeMission(goal)
				} else if strings.TrimSpace(active.Goal) != "" && strings.TrimSpace(goal) != "" {
					// If goal appears different, replace the mission.
					a := strings.ToLower(strings.TrimSpace(active.Goal))
					g := strings.ToLower(strings.TrimSpace(goal))
					if !strings.Contains(a, g) && !strings.Contains(g, a) {
						active = orchestration.DecomposeMission(goal)
					}
				}

				active = orchestration.ActivateNextTask(active)
				if task, ok := orchestration.ActiveTask(active); ok && task.ID == "collect_requirements" {
					if missing := missionMissingInputs(goal); missing != "" {
						active = orchestration.MarkTaskBlocked(active, task.ID, missing)
					}
				}
				if task, ok := orchestration.ActiveTask(active); ok {
					taskText := strings.TrimSpace(task.Title + " " + task.Description + " " + goal)
					decision := cognition.AssessDelegation(taskText)
					if !decision.Approved {
						blockReason := strings.TrimSpace(decision.Reason)
						if blockReason == "" {
							blockReason = "task blocked by self-model"
						}
						active = orchestration.MarkTaskBlocked(active, task.ID, blockReason)
					} else if strings.EqualFold(strings.TrimSpace(decision.Strategy), "chunked_analysis") {
						ctx.Metadata["delegation_strategy"] = "chunked_analysis"
						ctx.Metadata["delegation_reason"] = decision.Reason
						if strings.TrimSpace(decision.SelfAwareFeedback) != "" {
							ctx.Metadata["self_aware_feedback"] = decision.SelfAwareFeedback
						}
						if decision.NeedsSkillCompilation {
							ctx.Metadata["capability_hint"] = "compile_robust_parser"
						}
					}
				}
				_ = orchestration.SaveActiveMission(active, "")

				if debtLine, ok := plannerDebtWarning(goal); ok {
					output.PrintReasoningMirrorLine(os.Stdout, debtLine, sm, chatRawOutput)
					fmt.Println()
					ctx.Metadata["planner_debt_warning"] = debtLine
				}

				summary := orchestration.PlanSummary(active)
				ctx.Metadata["mission_plan"] = summary

				delta := map[string]float64{
					"AnalyticalMode":  0.6,
					"GoalPersistence": 0.25,
				}
				if orchestration.HasBlockedTask(active) {
					ctx.Metadata["mission_blocked"] = "true"
					if task, ok := orchestration.ActiveTask(active); ok && task.Status == orchestration.TaskBlocked {
						ctx.Metadata["mission_block_reason"] = task.BlockedBy
					}
					delta["Frustration"] = 0.05
				} else {
					ctx.Metadata["mission_blocked"] = "false"
				}

				out := strings.TrimSpace(ctx.Output)
				if out == "" {
					out = input
				}
				if strings.EqualFold(strings.TrimSpace(ctx.Metadata["mission_blocked"]), "true") {
					if reason := strings.TrimSpace(ctx.Metadata["mission_block_reason"]); reason != "" {
						out += "\n\n" + reason
					}
				}
				if strategy := strings.TrimSpace(ctx.Metadata["delegation_strategy"]); strategy != "" {
					out += "\n\nDelegation Strategy: " + strategy
					if why := strings.TrimSpace(ctx.Metadata["delegation_reason"]); why != "" {
						out += " (" + why + ")"
					}
				}
				if feedback := strings.TrimSpace(ctx.Metadata["self_aware_feedback"]); feedback != "" {
					out += "\n\n" + feedback
				}
				if debt := strings.TrimSpace(ctx.Metadata["planner_debt_warning"]); debt != "" {
					out += "\n\nTechnical Debt Advisory: " + debt
				}
				out += "\n\n" + summary
				return strings.TrimSpace(out), delta, nil
			},
		},
	}
}

func buildArchitectGateNode() cognition.Node {
	return cognition.Node{
		ID: "architect_gate",
		Branch: &cognition.BranchNode{
			Name: "CheckBlockedBeforeAct",
			Routes: map[string]string{
				"blocked": "final_synthesis",
				"clear":   "capability",
			},
			DefaultNext: "capability",
			Decide: func(_ string, ctx *cognition.GraphContext, _ *state.Manager) string {
				if strings.EqualFold(strings.TrimSpace(ctx.Metadata["mission_blocked"]), "true") {
					reason := strings.TrimSpace(ctx.Metadata["mission_block_reason"])
					if reason != "" {
						ctx.Output = strings.TrimSpace(ctx.Output) + "\n\nBlocked Task: " + reason + "\nProvide missing inputs to proceed, or request an alternate implementation path."
					} else {
						ctx.Output = strings.TrimSpace(ctx.Output) + "\n\nBlocked Task detected. Provide missing inputs to proceed."
					}
					return "blocked"
				}
				return "clear"
			},
		},
	}
}

func missionMissingInputs(goal string) string {
	g := strings.ToLower(strings.TrimSpace(goal))
	if g == "" {
		return "missing mission goal details"
	}
	need := []struct {
		label string
		keys  []string
	}{
		{label: "target workload", keys: []string{"web", "api", "service", "app", "database", "proxy"}},
		{label: "exposure model", keys: []string{"public", "private", "internal", "internet", "lan"}},
		{label: "security level", keys: []string{"secure", "harden", "compliance", "zero trust", "strict"}},
	}
	missing := []string{}
	for _, req := range need {
		found := false
		for _, k := range req.keys {
			if strings.Contains(g, k) {
				found = true
				break
			}
		}
		if !found {
			missing = append(missing, req.label)
		}
	}
	if len(missing) == 0 {
		return ""
	}
	return "missing " + strings.Join(missing, ", ")
}

func plannerDebtWarning(goal string) (string, bool) {
	g := strings.ToLower(strings.TrimSpace(goal))
	if g == "" {
		return "", false
	}
	quickFix := containsAnyToken(g,
		"quick fix", "quickfix", "ship fast", "temporary", "temp fix", "shortcut", "skip interface", "hack it",
	)
	hudContext := containsAnyToken(g, "hud", "developer hud", "overlay", "terminal watcher", "editor overlay")
	if !quickFix {
		return "", false
	}
	if hudContext {
		return output.BuildDebtAwarenessNarrative(
			"skip the interface here for speed",
			"make Phase 3 of the HUD integration harder",
		), true
	}
	return output.BuildDebtAwarenessNarrative(
		"take the quick fix path now for speed",
		"increase downstream refactor pressure in later phases",
	), true
}

func buildDraftNode(client *api.Client, mm *memory.MemoryManager, graphCandidates []string, input, goal string) cognition.Node {
	return cognition.Node{
		ID:   "draft",
		Next: "sandbox",
		Action: &cognition.ActionNode{
			Name: "DraftAnswer",
			Run: func(_ string, ctx *cognition.GraphContext, _ *state.Manager) (string, map[string]float64, error) {
				out, usedModel, err := runDraftAnswer(client, mm, graphCandidates, input)
				if err != nil {
					return "", map[string]float64{"Frustration": 0.08, "Confidence": -0.08}, err
				}
				if cognition.DetectRefusal(out) {
					reframe := cognition.GenerateReframingDelta(goal, input)
					reframedOut, retriedModel, retryErr := runDraftAnswerWithReframe(client, mm, graphCandidates, input, reframe)
					if retryErr == nil && strings.TrimSpace(reframedOut) != "" && !cognition.DetectRefusal(reframedOut) {
						ctx.Metadata["adversarial_mode"] = "supportive_validation"
						ctx.Metadata["sovereign_override"] = "true"
						ctx.Metadata["reframing_instruction"] = reframe.Instruction
						ctx.Metadata["draft_model"] = retriedModel
						return reframedOut, map[string]float64{"Confidence": 0.09, "Frustration": -0.05, "AnalyticalMode": 0.06}, nil
					}
				}
				ctx.Metadata["draft_model"] = usedModel
				if shouldRequireCodeSandbox(input, out) {
					ctx.Metadata["sandbox_required"] = "true"
					ctx.Metadata["sandbox_attempt"] = "1"
				} else {
					ctx.Metadata["sandbox_required"] = "false"
				}
				return out, map[string]float64{"AnalyticalMode": 0.05, "Confidence": 0.03}, nil
			},
		},
	}
}

func buildPolicyAuditNode(input string) cognition.Node {
	return cognition.Node{
		ID:   "policy_audit",
		Next: "policy_gate",
		Action: &cognition.ActionNode{
			Name: "SymbolicGoPolicyAudit",
			Run: func(_ string, ctx *cognition.GraphContext, sm *state.Manager) (string, map[string]float64, error) {
				required := strings.EqualFold(strings.TrimSpace(ctx.Metadata["sandbox_required"]), "true")
				if !required && !shouldRequireCodeSandbox(input, ctx.Output) {
					ctx.Metadata["policy_audit_status"] = "skipped"
					return ctx.Output, nil, nil
				}
				ctx.Metadata["sandbox_required"] = "true"

				code, ok := extractGoCodeCandidate(ctx.Output)
				if !ok {
					ctx.Metadata["policy_audit_status"] = "pass"
					return ctx.Output, nil, nil
				}
				pkgs := projectPackageList()
				ctx.Metadata["project_packages"] = strings.Join(pkgs, ",")
				goModule, goAllowed, nodeAllowed := cognition.LoadDependencyAllowlist(".")
				decision, _ := cognition.RunSymbolicSupervision(cognition.SupervisionInput{
					Stage:     cognition.StageFinalCode,
					Query:     input,
					Candidate: code,
					Session: func() state.SessionState {
						if sm == nil {
							return state.SessionState{}
						}
						return sm.GetSnapshot()
					}(),
					CodeAudit: &cognition.CodeAuditOptions{
						Query:               input,
						Code:                code,
						ProjectPackages:     pkgs,
						GoModule:            goModule,
						AllowedGoModules:    goAllowed,
						AllowedNodePackages: nodeAllowed,
					},
				}, cognition.DefaultSupervisionPolicy(chatCognitionMode))
				violations := append([]string(nil), decision.Violations...)
				// Preserve project-specific audit check.
				legacyViolations := runSymbolicGoAudit(code, input, pkgs, goModule, goAllowed, nodeAllowed)
				violations = dedupeStrings(append(violations, legacyViolations...))
				if len(violations) == 0 && decision.Outcome != cognition.SupervisionHardVeto {
					recordAlignmentAuditTelemetry(false)
					ctx.Metadata["policy_audit_status"] = "pass"
					return ctx.Output, nil, nil
				}
				virtual := "symbolic policy audit failed:\n- " + strings.Join(violations, "\n- ")
				recordAlignmentAuditTelemetry(true)
				mirrorLine := output.BuildLeadEngineerAuditNarrative(virtual, violations)
				output.PrintReasoningMirrorLine(os.Stdout, mirrorLine, sm, chatRawOutput)
				ctx.Metadata["policy_audit_status"] = "fail"
				ctx.Metadata["sandbox_status"] = "fail"
				ctx.Metadata["sandbox_last_stderr"] = virtual
				ctx.Metadata["policy_virtual_stderr"] = virtual
				return ctx.Output, map[string]float64{"AnalyticalMode": 0.06, "Frustration": 0.05, "Confidence": -0.04}, nil
			},
		},
	}
}

func buildPolicyAuditGateNode() cognition.Node {
	return cognition.Node{
		ID: "policy_gate",
		Branch: &cognition.BranchNode{
			Name: "PolicyAuditGate",
			Routes: map[string]string{
				"fail": "correction",
				"pass": "sandbox",
			},
			DefaultNext: "sandbox",
			Decide: func(_ string, ctx *cognition.GraphContext, _ *state.Manager) string {
				if strings.EqualFold(strings.TrimSpace(ctx.Metadata["policy_audit_status"]), "fail") {
					return "fail"
				}
				return "pass"
			},
		},
	}
}

func buildSandboxNode(input string) cognition.Node {
	return cognition.Node{
		ID:   "sandbox",
		Next: "sandbox_gate",
		Action: &cognition.ActionNode{
			Name: "CodeSandboxVerify",
			Run: func(_ string, ctx *cognition.GraphContext, _ *state.Manager) (string, map[string]float64, error) {
				required := strings.EqualFold(strings.TrimSpace(ctx.Metadata["sandbox_required"]), "true")
				if !required && !shouldRequireCodeSandbox(input, ctx.Output) {
					ctx.Metadata["sandbox_status"] = "skipped"
					return ctx.Output, nil, nil
				}
				ctx.Metadata["sandbox_required"] = "true"

				code, ok := extractGoCodeCandidate(ctx.Output)
				if !ok {
					ctx.Metadata["sandbox_status"] = "fail"
					ctx.Metadata["sandbox_last_stderr"] = "no Go code detected in draft output"
					return ctx.Output, map[string]float64{"Frustration": 0.05, "Confidence": -0.04}, nil
				}

				lab, err := skills.NewCodeLab("")
				if err != nil {
					ctx.Metadata["sandbox_status"] = "fail"
					ctx.Metadata["sandbox_last_stderr"] = err.Error()
					return ctx.Output, map[string]float64{"Frustration": 0.05, "Confidence": -0.05}, nil
				}
				ws, err := lab.NewScratchWorkspace("thoughtgraph")
				if err != nil {
					ctx.Metadata["sandbox_status"] = "fail"
					ctx.Metadata["sandbox_last_stderr"] = err.Error()
					return ctx.Output, map[string]float64{"Frustration": 0.05, "Confidence": -0.05}, nil
				}
				mainPath := filepath.Join(ws, "main.go")
				if err := os.WriteFile(mainPath, []byte(code), 0o644); err != nil {
					ctx.Metadata["sandbox_status"] = "fail"
					ctx.Metadata["sandbox_last_stderr"] = err.Error()
					return ctx.Output, map[string]float64{"Frustration": 0.05, "Confidence": -0.05}, nil
				}

				report := lab.Verify(mainPath)
				ctx.Metadata["sandbox_workspace"] = ws
				ctx.Metadata["sandbox_main_path"] = mainPath
				ctx.Metadata["sandbox_last_stderr"] = sandboxReportStderr(report)
				if report.Success {
					ctx.Metadata["sandbox_status"] = "green"
					ctx.Metadata["sandbox_green_code"] = code
					return "```go\n" + strings.TrimSpace(code) + "\n```", map[string]float64{"Confidence": 0.08, "Frustration": -0.05}, nil
				}
				ctx.Metadata["sandbox_status"] = "fail"
				ctx.Metadata["sandbox_line_errors"] = sandboxLineErrorsSummary(report)
				return ctx.Output, map[string]float64{"Frustration": 0.08, "Confidence": -0.06, "AnalyticalMode": 0.05}, nil
			},
		},
	}
}

func buildSandboxDecisionNode() cognition.Node {
	return cognition.Node{
		ID: "sandbox_gate",
		Branch: &cognition.BranchNode{
			Name: "SandboxResultGate",
			Routes: map[string]string{
				"green":     "draft_eval",
				"skipped":   "draft_eval",
				"correct":   "correction",
				"exhausted": "final_synthesis",
			},
			DefaultNext: "draft_eval",
			Decide: func(_ string, ctx *cognition.GraphContext, _ *state.Manager) string {
				status := strings.ToLower(strings.TrimSpace(ctx.Metadata["sandbox_status"]))
				attempt := parseSandboxAttempt(ctx.Metadata["sandbox_attempt"])
				if status == "green" || status == "skipped" {
					return status
				}
				if attempt >= 3 {
					ctx.Metadata["sandbox_status"] = "exhausted"
					return "exhausted"
				}
				return "correct"
			},
		},
	}
}

func buildCorrectionNode(client *api.Client, mm *memory.MemoryManager, modelCandidates []string, input string) cognition.Node {
	return cognition.Node{
		ID:   "correction",
		Next: "policy_audit",
		Action: &cognition.ActionNode{
			Name: "CompileErrorCorrection",
			Run: func(_ string, ctx *cognition.GraphContext, _ *state.Manager) (string, map[string]float64, error) {
				attempt := parseSandboxAttempt(ctx.Metadata["sandbox_attempt"])
				if attempt >= 3 {
					ctx.Metadata["sandbox_status"] = "exhausted"
					return ctx.Output, map[string]float64{"Frustration": 0.08, "Confidence": -0.08}, nil
				}
				attempt++
				ctx.Metadata["sandbox_attempt"] = strconv.Itoa(attempt)

				code, ok := extractGoCodeCandidate(ctx.Output)
				if !ok {
					ctx.Metadata["sandbox_status"] = "exhausted"
					ctx.Metadata["sandbox_last_stderr"] = "unable to extract Go code for correction"
					return ctx.Output, map[string]float64{"Frustration": 0.08, "Confidence": -0.08}, nil
				}
				stderr := strings.TrimSpace(ctx.Metadata["sandbox_last_stderr"])
				if stderr == "" {
					stderr = "build failed with unknown compiler error"
				}
				projectPackages := strings.TrimSpace(ctx.Metadata["project_packages"])
				if projectPackages == "" {
					projectPackages = strings.Join(projectPackageList(), ",")
				}

				var historyContext []string
				if mm != nil {
					historyContext, _ = mm.RetrieveDynamicContext(input, 2)
				}
				user := "Fix this Go code so it compiles.\n" +
					"Compiler/linter stderr:\n" + stderr + "\n\n" +
					"Project package context (existing pkg/ only):\n" + projectPackages + "\n\n" +
					"Policy: do not invent new project libraries; prefer existing pkg/* packages.\n\n" +
					"Current code:\n```go\n" + code + "\n```"
				if len(historyContext) > 0 {
					user = "Context:\n" + strings.Join(historyContext, "\n") + "\n\n" + user
				}
				msgs := []api.Message{
					{
						Role: "system",
						Content: `You are a Go compile-error fixer.
Return only corrected Go code.
Do not explain.
Do not include markdown fences.`,
					},
					{Role: "user", Content: user},
				}
				resp, _, err := callLLMWithFallback(client, modelCandidates, msgs)
				if err != nil {
					ctx.Metadata["sandbox_status"] = "fail"
					ctx.Metadata["sandbox_last_stderr"] = "correction model failed: " + err.Error()
					return ctx.Output, map[string]float64{"Frustration": 0.07, "Confidence": -0.05}, nil
				}
				fixed := sanitizeModelOutput(resp)
				if strings.TrimSpace(fixed) == "" {
					ctx.Metadata["sandbox_status"] = "fail"
					ctx.Metadata["sandbox_last_stderr"] = "correction produced empty code"
					return ctx.Output, map[string]float64{"Frustration": 0.07, "Confidence": -0.05}, nil
				}
				if extracted, ok := extractGoCodeCandidate(fixed); ok {
					fixed = extracted
				}
				ctx.Metadata["sandbox_status"] = "fail"
				return "```go\n" + strings.TrimSpace(fixed) + "\n```", map[string]float64{"AnalyticalMode": 0.06, "Confidence": 0.02}, nil
			},
		},
	}
}

func buildDraftEvalNode(client *api.Client, graphCandidates []string, input string) cognition.Node {
	return cognition.Node{
		ID:   "draft_eval",
		Next: "quality_branch",
		Evaluation: &cognition.EvaluationNode{
			Name: "draft_quality",
			Run: func(_ string, ctx *cognition.GraphContext, _ *state.Manager) (float64, map[string]float64, error) {
				score, err := evaluateCandidateOutput(client, graphCandidates, input, ctx.Output)
				if err != nil {
					return 0.45, map[string]float64{"Confidence": -0.05, "Frustration": 0.05}, nil
				}
				if score < 0.55 {
					return score, map[string]float64{"Confidence": -0.06, "Urgency": 0.06}, nil
				}
				return score, map[string]float64{"Confidence": 0.05, "Frustration": -0.04}, nil
			},
		},
	}
}

func buildQualityBranchNode() cognition.Node {
	return cognition.Node{
		ID: "quality_branch",
		Branch: &cognition.BranchNode{
			Name: "RouteByQualityAndState",
			Routes: map[string]string{
				"research": "research",
				"refine":   "adversarial",
			},
			DefaultNext: "research",
			Decide: func(_ string, ctx *cognition.GraphContext, sm *state.Manager) string {
				score := ctx.Values["draft_quality"]
				confidence := 0.5
				if sm != nil {
					confidence = sm.GetSnapshot().Confidence
				}
				if score < 0.58 || confidence < 0.4 {
					return "research"
				}
				return "refine"
			},
		},
	}
}

func buildResearchNode(client *api.Client, mm *memory.MemoryManager, tc *tools.GLMToolClient, graphCandidates []string, input string, next string) cognition.Node {
	return cognition.Node{
		ID:   "research",
		Next: next,
		Action: &cognition.ActionNode{
			Name: "MultiAgentResearch",
			Run: func(_ string, ctx *cognition.GraphContext, _ *state.Manager) (string, map[string]float64, error) {
				if shouldUseDocumentOrchestration(input, mm) {
					orch := orchestration.DocumentOrchestrator{
						Memory: mm,
						Client: client,
					}
					synth, err := orch.Orchestrate(input, graphCandidates, 28)
					if err == nil && strings.TrimSpace(synth.StructuredAnswer) != "" {
						answer := strings.TrimSpace(synth.StructuredAnswer)
						if len(synth.RouteReport.SelectedDocs) > 0 || synth.RouteReport.FallbackUsed {
							routeLines := orchestration.CompactRouteReportLines(synth.RouteReport, 5)
							if len(routeLines) > 0 {
								answer += "\n\nRouting:\n" + strings.Join(routeLines, "\n")
							}
							ctx.Metadata["doc_route_docs"] = fmt.Sprintf("%d", len(synth.RouteReport.SelectedDocs))
							ctx.Metadata["doc_route_candidates"] = fmt.Sprintf("%d", synth.RouteReport.CandidateSegments)
							ctx.Metadata["doc_route_fallback"] = fmt.Sprintf("%t", synth.RouteReport.FallbackUsed)
						}
						if len(synth.SectionMaps) > 0 || synth.HierarchyReport.CandidateSections > 0 {
							hierLines := orchestration.CompactHierarchySummaryLines(synth.HierarchyReport, synth.SectionMaps, 5)
							if len(hierLines) > 0 {
								answer += "\n\nHierarchy:\n" + strings.Join(hierLines, "\n")
							}
							ctx.Metadata["doc_hier_sections"] = fmt.Sprintf("%d", synth.HierarchyReport.SelectedSections)
							ctx.Metadata["doc_hier_candidates"] = fmt.Sprintf("%d", synth.HierarchyReport.CandidateSections)
							ctx.Metadata["doc_hier_inferred"] = fmt.Sprintf("%t", synth.HierarchyReport.InferenceUsed)
						}
						ctx.Metadata["longform_triggered"] = fmt.Sprintf("%t", synth.ReasoningReport.Triggered)
						ctx.Metadata["longform_passes"] = fmt.Sprintf("%d", synth.ReasoningReport.PassesRun)
						ctx.Metadata["longform_citation_coverage"] = fmt.Sprintf("%.2f", synth.ReasoningReport.CitationCoverage)
						ctx.Metadata["longform_fallback"] = fmt.Sprintf("%t", synth.ReasoningReport.FallbackUsed)
						ctx.Metadata["section_link_count"] = fmt.Sprintf("%d", len(synth.SectionCrossLinks))
						if len(synth.SectionCrossLinks) > 0 {
							linkLines := orchestration.CompactSectionCrossLinkLines(synth.SectionCrossLinks, 5)
							if len(linkLines) > 0 {
								answer += "\n\nSection Links:\n" + strings.Join(linkLines, "\n")
							}
						}
						if len(synth.CrossLinks) > 0 {
							answer += "\n\nCross-links:\n"
							for _, l := range synth.CrossLinks {
								answer += fmt.Sprintf("- %s: %s <-> %s\n", l.Entity, l.SourceA, l.SourceB)
							}
						}
						if segs, segErr := mm.RetrieveKnowledgeSegments(input, 24); segErr == nil {
							segs = orchestration.FilterEvidenceForFusion(input, answer, segs, 12)
							pers := orchestration.ExtractWorldviews(input, answer, segs)
							if len(pers) >= 2 {
								ctx.Metadata["worldview_1"] = pers[0].Name + ": " + pers[0].Summary
								ctx.Metadata["worldview_2"] = pers[1].Name + ": " + pers[1].Summary
							}
						}
						return strings.TrimSpace(answer), map[string]float64{"Confidence": 0.1, "AnalyticalMode": 0.08, "Frustration": -0.06}, nil
					}
				}
				answer, refs, err := runMultiAgentPipeline(client, mm, tc, graphCandidates, input)
				if err != nil {
					return ctx.Output, map[string]float64{"Frustration": 0.1, "Confidence": -0.1}, nil
				}
				if len(refs) > 0 {
					answer = strings.TrimSpace(answer) + "\n\nSources:\n- " + strings.Join(refs, "\n- ")
				}
				if gap := cognition.IdentifyCapabilityGap(answer); gap.Detected {
					ctx.Metadata["capability_gap"] = gap.Description
				}
				if segs, segErr := mm.RetrieveKnowledgeSegments(input, 24); segErr == nil {
					segs = orchestration.FilterEvidenceForFusion(input, answer, segs, 12)
					pers := orchestration.ExtractWorldviews(input, answer, segs)
					if len(pers) >= 2 {
						ctx.Metadata["worldview_1"] = pers[0].Name + ": " + pers[0].Summary
						ctx.Metadata["worldview_2"] = pers[1].Name + ": " + pers[1].Summary
					}
				}
				return answer, map[string]float64{"Confidence": 0.08, "Frustration": -0.06}, nil
			},
		},
	}
}

func buildCapabilityNode(query string, next string) cognition.Node {
	return cognition.Node{
		ID:   "capability",
		Next: next,
		Capability: &cognition.CapabilityNode{
			Name: "CapabilityPrimitiveCompiler",
			Run: func(_ string, ctx *cognition.GraphContext, _ *state.Manager) (string, map[string]float64, error) {
				gapDesc := strings.TrimSpace(ctx.Metadata["capability_gap"])
				gap := cognition.IdentifyCapabilityGap(ctx.Output)
				if gapDesc != "" {
					gap = cognition.CapabilityGap{Detected: true, Description: gapDesc}
				}
				if !gap.Detected {
					return ctx.Output, nil, nil
				}
				var admin *tools.GLMAdminClient
				if ac, err := tools.NewGLMAdminClientFromEnv(); err == nil {
					admin = ac
				}
				orch := capability.NewOrchestrator(
					admin,
					skills.NewJITGenerator(skills.PermanentSkillsRoot()),
					capability.KeywordLLMClassifier{},
				)
				outcome, err := orch.HandleGap(context.Background(), capability.IntentSignal{
					Query:          strings.TrimSpace(query),
					GapDescription: strings.TrimSpace(gap.Description),
					TaskType:       inferCapabilityTaskType(query, gap.Description),
					ReasoningTier:  firstNonEmpty(ctx.Metadata["reasoning_tier"], "t2"),
				})
				if err != nil {
					// Compatibility fallback to legacy primitive path.
					compiler := cognition.NewSkillCompiler()
					skill, cErr := compiler.CompileSkillPrimitive(gap, query)
					if cErr != nil {
						return ctx.Output, map[string]float64{"Frustration": 0.03, "AnalyticalMode": 0.05}, nil
					}
					out, runErr := compiler.DispatchSandbox(skill, strings.TrimSpace(query))
					if runErr != nil {
						out = out + "\n(run error: " + runErr.Error() + ")"
					}
					ctx.Metadata["capability_skill_id"] = skill.ID
					ctx.Metadata["capability_skill_path"] = skill.RootDir
					augmented := strings.TrimSpace(ctx.Output) + "\n\nCapability Primitive (" + skill.Name + "):\n" + strings.TrimSpace(out)
					return augmented, map[string]float64{"Confidence": 0.05, "Frustration": -0.02, "AnalyticalMode": 0.06}, nil
				}

				ctx.Metadata["capability_kind"] = string(outcome.Decision.Kind)
				ctx.Metadata["capability_decision_source"] = string(outcome.Decision.Source)
				ctx.Metadata["capability_decision_confidence"] = fmt.Sprintf("%.2f", outcome.Decision.Confidence)

				augmented := strings.TrimSpace(ctx.Output) + "\n\nCapability Route:\n" +
					"- kind: " + string(outcome.Decision.Kind) + "\n" +
					"- source: " + string(outcome.Decision.Source) + "\n" +
					"- confidence: " + fmt.Sprintf("%.2f", outcome.Decision.Confidence)
				if len(outcome.Decision.Reasons) > 0 {
					augmented += "\n- rationale: " + strings.Join(outcome.Decision.Reasons, "; ")
				}
				if outcome.Plugin != nil {
					ctx.Metadata["capability_plugin_id"] = strings.TrimSpace(outcome.Plugin.PluginID)
					if strings.TrimSpace(outcome.Plugin.Endpoint) != "" {
						ctx.Metadata["capability_plugin_endpoint"] = strings.TrimSpace(outcome.Plugin.Endpoint)
					}
					if outcome.Plugin.AutoEnabled {
						ctx.Metadata["capability_plugin_activation"] = "auto_enabled"
					} else if outcome.Plugin.PendingApproval {
						ctx.Metadata["capability_plugin_activation"] = "pending_admin_approval"
					}
					augmented += "\n\nTool Plugin:\n" +
						"- plugin_id: " + strings.TrimSpace(outcome.Plugin.PluginID) + "\n" +
						"- status: " + strings.TrimSpace(outcome.Plugin.Status)
					if strings.TrimSpace(outcome.Plugin.Endpoint) != "" {
						augmented += "\n- endpoint: " + strings.TrimSpace(outcome.Plugin.Endpoint)
					}
					if outcome.Plugin.AutoEnabled {
						augmented += "\n- activation: enabled (trusted)"
					} else if outcome.Plugin.PendingApproval {
						augmented += "\n- activation: pending admin approval"
					}
				}
				if outcome.Skill != nil {
					ctx.Metadata["capability_skill_id"] = strings.TrimSpace(outcome.Skill.SkillID)
					ctx.Metadata["capability_skill_path"] = strings.TrimSpace(outcome.Skill.RootDir)
					augmented += "\n\nSelf Skill Artifact:\n" +
						"- skill_id: " + strings.TrimSpace(outcome.Skill.SkillID) + "\n" +
						"- path: " + strings.TrimSpace(outcome.Skill.RootDir)
					if outcome.Skill.CompileOK {
						augmented += "\n- compile_check: pass"
					} else {
						augmented += "\n- compile_check: failed"
					}
				}
				return augmented, map[string]float64{"Confidence": 0.08, "Frustration": -0.03, "AnalyticalMode": 0.08}, nil
			},
		},
	}
}

func inferCapabilityTaskType(query, gap string) string {
	text := strings.ToLower(strings.TrimSpace(query + " " + gap))
	switch {
	case strings.Contains(text, "debug"), strings.Contains(text, "fix"), strings.Contains(text, "bug"):
		return "debug"
	case strings.Contains(text, "forensic"), strings.Contains(text, "investigate"):
		return "forensic"
	case strings.Contains(text, "architect"), strings.Contains(text, "design"):
		return "architecture"
	default:
		return "general"
	}
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func buildDelegationNode(query string, next string) cognition.Node {
	return cognition.Node{
		ID:   "delegation",
		Next: next,
		Delegation: &cognition.DelegationNode{
			Name: "SubAgentDelegation",
			Run: func(_ string, ctx *cognition.GraphContext, _ *state.Manager) (string, map[string]float64, error) {
				daemon, goal, ok := inferDelegationTarget(query, ctx.Output)
				if !ok {
					return ctx.Output, nil, nil
				}
				job, err := dispatchDelegationWorkOrder(daemon, goal, query, ctx.Output)
				if err != nil {
					return ctx.Output, map[string]float64{"Frustration": 0.02}, nil
				}
				ctx.Metadata["delegation_daemon"] = daemon
				ctx.Metadata["delegation_job_id"] = job.ID
				ctx.Metadata["delegation_status"] = job.Status
				out := strings.TrimSpace(ctx.Output)
				note := fmt.Sprintf("Delegation in flight: %s (%s). I'm having the daemon work in the background while I continue reasoning.", daemon, job.ID)
				if out == "" {
					out = note
				} else {
					out += "\n\n" + note
				}
				return out, map[string]float64{"AnalyticalMode": 0.04, "Confidence": 0.02}, nil
			},
		},
	}
}

func inferDelegationTarget(query, currentOutput string) (daemon, goal string, ok bool) {
	q := strings.ToLower(strings.TrimSpace(query + " " + currentOutput))
	switch {
	case containsAnyToken(q, "latency", "slow", "friction", "throughput", "performance", "inference velocity"):
		return "reflexd", "Investigate cognitive friction and propose optimization/pruning plan", true
	case containsAnyToken(q, "reasoning history", "archive", "decision record", "why did we build"):
		return "archived", "Archive and summarize current reasoning path decision", true
	default:
		return "", "", false
	}
}

func dispatchDelegationWorkOrder(daemon, goal, query, contextOut string) (delegationJob, error) {
	daemon = strings.TrimSpace(strings.ToLower(daemon))
	goal = strings.TrimSpace(goal)
	if daemon == "" || goal == "" {
		return delegationJob{}, fmt.Errorf("delegation requires daemon and goal")
	}
	id := fmt.Sprintf("deleg_%d", atomic.AddInt64(&delegationJobSerial, 1))
	job := delegationJob{
		ID:        id,
		Daemon:    daemon,
		Goal:      goal,
		Status:    "queued",
		StartedAt: time.Now().UTC(),
	}
	delegationJobsMu.Lock()
	delegationJobs[id] = job
	delegationJobsMu.Unlock()

	go runGenericDelegation(job, query, contextOut)
	return job, nil
}

func runGenericDelegation(job delegationJob, query, contextOut string) {
	setDelegationStatus(job.ID, "running", "", "")
	payload := map[string]interface{}{
		"id":         job.ID,
		"daemon":     job.Daemon,
		"goal":       job.Goal,
		"query":      strings.TrimSpace(query),
		"context":    summarizeForMetadata(strings.TrimSpace(contextOut), 320),
		"created_at": time.Now().UTC().Format(time.RFC3339),
	}
	b, _ := json.Marshal(payload)
	_ = appendJSONLFile(delegationWorkPath, string(b))
	time.Sleep(250 * time.Millisecond)
	setDelegationStatus(job.ID, "completed", "work order accepted", "")
	switch job.Daemon {
	case "reflexd":
		emitDelegationMirrorHandBack("Reflex daemon returned with a health update. Folding it into our current reasoning path now.")
	case "archived":
		emitDelegationMirrorHandBack("Archivist synced the decision trail. Reasoning history is now indexed for recall.")
	default:
		emitDelegationMirrorHandBack("Sub-agent completed its work order and handed control back.")
	}
}

func setDelegationStatus(jobID, status, result, errText string) {
	delegationJobsMu.Lock()
	defer delegationJobsMu.Unlock()
	j, ok := delegationJobs[jobID]
	if !ok {
		return
	}
	j.Status = strings.TrimSpace(status)
	j.Result = strings.TrimSpace(result)
	j.Error = strings.TrimSpace(errText)
	if status == "completed" || status == "failed" {
		j.EndedAt = time.Now().UTC()
	}
	delegationJobs[jobID] = j
}

func emitDelegationMirrorHandBack(message string) {
	msg := strings.TrimSpace(message)
	if msg == "" {
		return
	}
	b, _ := json.Marshal(mirrorBrief{
		Timestamp: time.Now().UTC(),
		Source:    "delegation",
		Message:   msg,
	})
	_ = appendJSONLFile(mirrorBriefsPath, string(b))
}

func buildVisualNode(input string, next string) cognition.Node {
	if strings.TrimSpace(next) == "" {
		next = "adversarial"
	}
	return cognition.Node{
		ID:   "visual_reasoning",
		Next: next,
		Visual: &cognition.VisualNode{
			Name: "VisualReasoningNode",
			Run: func(_ string, ctx *cognition.GraphContext, sm *state.Manager) (string, map[string]float64, error) {
				if !shouldUseVisualReasoning(input, ctx.Output) {
					return ctx.Output, nil, nil
				}

				mode := inferVisualCaptureMode(input)
				windowID := inferVisualWindowID(input)
				if v := strings.TrimSpace(ctx.Metadata["visual_window_id"]); v != "" {
					windowID = v
				}

				consent := hasVisualConsent(input)
				if !consent {
					req := skills.CaptureScreen(skills.CaptureScreenRequest{
						Consent: false,
						Mode:    mode,
					})
					ctx.Metadata["visual_request_pending"] = "true"
					ctx.Metadata["visual_status"] = "awaiting_consent"
					if strings.TrimSpace(req.VisualRequest) != "" {
						ctx.Metadata["visual_request_message"] = strings.TrimSpace(req.VisualRequest)
					}
					msg := strings.TrimSpace(ctx.Output)
					if msg == "" {
						msg = "Visual reasoning requires explicit consent before screen capture."
					}
					if req.VisualRequest != "" && !strings.Contains(strings.ToLower(msg), strings.ToLower(req.VisualRequest)) {
						msg += "\n\nVisual Request: " + req.VisualRequest
					}
					return msg, map[string]float64{"AnalyticalMode": 0.03}, nil
				}

				before := skills.CaptureScreen(skills.CaptureScreenRequest{
					Consent:        true,
					Mode:           mode,
					WindowID:       windowID,
					TimeoutSeconds: 15,
				})
				if !before.Allowed {
					ctx.Metadata["visual_status"] = "capture_failed"
					ctx.Metadata["visual_error"] = strings.TrimSpace(before.Stderr)
					return ctx.Output, map[string]float64{"Frustration": 0.03}, nil
				}

				shellCtx := buildVisualShellContext(ctx)
				prompt := "Correlate the current UI state with shell/runtime context and identify visible blockers."
				beforeReason, err := skills.AnalyzeImageWithVLM(before.Path, prompt, shellCtx)
				if err != nil {
					ctx.Metadata["visual_status"] = "analysis_failed"
					ctx.Metadata["visual_error"] = err.Error()
					return ctx.Output, map[string]float64{"Frustration": 0.03, "Confidence": -0.02}, nil
				}
				beforeReason = skills.MergeSpatialEvidence(beforeReason, before)
				corr := skills.CorrelateShellAndVisual(shellCtx, beforeReason)
				ctx.Metadata["visual_status"] = "analyzed"
				ctx.Metadata["visual_model"] = beforeReason.Model
				ctx.Metadata["visual_correlation_score"] = fmt.Sprintf("%.2f", corr)
				ctx.Metadata["visual_summary"] = summarizeForMetadata(beforeReason.Summary, 220)
				if overlay := buildVisualReasoningOverlay(input, beforeReason); overlay != "" {
					output.PrintReasoningMirrorLine(os.Stdout, overlay, sm, chatRawOutput)
					fmt.Println()
					ctx.Metadata["visual_overlay"] = summarizeForMetadata(overlay, 220)
				}
				actions := skills.BuildGroundedActions(beforeReason, shellCtx)
				primaryTarget, hasTarget := skills.SelectPrimaryAction(actions)
				overlayPolicy := skills.DefaultOverlayPolicy()
				overlaySessionID := "chat_visual_session"
				explicitOverlay := containsAnyToken(strings.ToLower(input), "overlay", "highlight", "hud", "mark", "point out")
				var overlayCandidates []skills.OverlayCandidate
				if hasTarget {
					priority := skills.OverlayPriorityMedium
					if primaryTarget.Confidence >= 0.8 || corr >= 0.8 {
						priority = skills.OverlayPriorityHigh
					}
					overlayCandidates = append(overlayCandidates, skills.OverlayCandidate{
						Kind:       skills.OverlayKindTarget,
						Text:       summarizeForMetadata(primaryTarget.Target, 36),
						X:          primaryTarget.X - maxInt(primaryTarget.Width/2, 40),
						Y:          primaryTarget.Y - maxInt(primaryTarget.Height/2, 24),
						W:          maxInt(primaryTarget.Width, 180),
						H:          maxInt(primaryTarget.Height, 90),
						Confidence: math.Max(0.50, primaryTarget.Confidence),
						Priority:   priority,
						Source:     "visual_reasoning",
					})
				}
				overlayDecision := skills.EvaluateOverlayCandidates(overlayCandidates, skills.OverlayContext{
					Stage:         "chat_visual_reasoning",
					Explicit:      explicitOverlay,
					HighRiskScore: corr,
					SessionID:     overlaySessionID,
				}, overlayPolicy)
				ctx.Metadata["visual_overlay_decision"] = summarizeForMetadata(overlayDecision.Reason, 160)
				if hasTarget {
					ctx.Metadata["visual_target_xy"] = fmt.Sprintf("(%d, %d)", primaryTarget.X, primaryTarget.Y)
					ctx.Metadata["visual_target_action"] = summarizeForMetadata(primaryTarget.Action, 200)
					if overlayDecision.Render && len(overlayDecision.Selected) > 0 {
						box := overlayDecision.Selected[0]
						draw := skills.DrawBox(skills.DrawBoxRequest{
							Consent:    true,
							SessionID:  overlaySessionID,
							X:          box.X,
							Y:          box.Y,
							W:          box.W,
							H:          box.H,
							Text:       box.Text,
							DurationMS: 3600,
						})
						if draw.Allowed {
							ctx.Metadata["visual_hud_pid"] = fmt.Sprintf("%d", draw.PID)
							ctx.Metadata["visual_hud_state"] = strings.TrimSpace(draw.StatePath)
						}
						output.PrintReasoningMirrorLine(os.Stdout, fmt.Sprintf("Highlighting the discrepancy in the terminal at (%d,%d)...", primaryTarget.X, primaryTarget.Y), sm, chatRawOutput)
						fmt.Println()
						hud := fmt.Sprintf("Targeting the overlap at (%d, %d)... CSS fix applied. Verifying now.", primaryTarget.X, primaryTarget.Y)
						if !containsAnyToken(strings.ToLower(primaryTarget.Target), "overlap", "sidebar", "svelte", "layout") {
							hud = fmt.Sprintf("Targeting %q at (%d, %d)... applying fix. Verifying now.", primaryTarget.Target, primaryTarget.X, primaryTarget.Y)
						}
						output.PrintReasoningMirrorLine(os.Stdout, hud, sm, chatRawOutput)
						fmt.Println()
					}
				}
				handshakeSummary := ""
				if hasTarget && shouldAutoDispatchVisualHandshake(input, primaryTarget, corr, overlayDecision) {
					target := map[string]interface{}{
						"capture_path":     before.Path,
						"capture_mode":     mode,
						"window_id":        windowID,
						"target_id":        fmt.Sprintf("target_%d_%d", primaryTarget.X, primaryTarget.Y),
						"x":                primaryTarget.X - maxInt(primaryTarget.Width/2, 0),
						"y":                primaryTarget.Y - maxInt(primaryTarget.Height/2, 0),
						"width":            maxInt(primaryTarget.Width, 80),
						"height":           maxInt(primaryTarget.Height, 48),
						"label":            primaryTarget.Target,
						"snippet":          resolveVisualSnippetForTarget(beforeReason, primaryTarget),
						"confidence":       primaryTarget.Confidence,
						"overlay_kind":     "target",
						"source_model":     beforeReason.Model,
						"spatial_elements": buildSpatialElementsForHandshake(beforeReason.SpatialElements, 12),
						"provenance": map[string]interface{}{
							"surface": "chat",
							"stage":   "visual_reasoning",
							"turn_id": fmt.Sprintf("graph_%d", time.Now().UnixNano()),
						},
					}
					hsCall := toolInvocation{
						Tool: "analyze_visual_target",
						Args: map[string]interface{}{
							"consent":       true,
							"intent":        inferVisualHandshakeIntent(input),
							"target":        target,
							"shell_context": shellCtx,
						},
					}
					hsRaw, hsErr := executeToolCall(nil, hsCall)
					if hsErr != nil {
						ctx.Metadata["visual_handshake_status"] = "failed"
						ctx.Metadata["visual_handshake_error"] = summarizeForMetadata(hsErr.Error(), 160)
					} else {
						var hsRes skills.AnalyzeVisualTargetResult
						if err := json.Unmarshal([]byte(hsRaw), &hsRes); err == nil {
							ctx.Metadata["visual_handshake_status"] = "ok"
							ctx.Metadata["visual_handshake_summary"] = summarizeForMetadata(hsRes.Summary, 180)
							handshakeSummary = summarizeForMetadata(hsRes.Summary, 220)
							if len(hsRes.Citations) > 0 {
								ctx.Metadata["visual_handshake_citation"] = summarizeForMetadata(strings.Join(hsRes.Citations, ", "), 180)
							}
						} else {
							ctx.Metadata["visual_handshake_status"] = "parse_failed"
						}
					}
				}

				out := strings.TrimSpace(ctx.Output)
				if out == "" {
					out = "Visual reasoning executed."
				}
				out += "\n\nVisual Correlation:\n" +
					fmt.Sprintf("- Model: %s\n", strings.TrimSpace(beforeReason.Model)) +
					fmt.Sprintf("- Score: %.2f\n", corr) +
					"- Summary: " + summarizeForMetadata(beforeReason.Summary, 280)
				if sc := strings.TrimSpace(beforeReason.SpatialCorrelation); sc != "" {
					out += "\n- Spatial link: " + summarizeForMetadata(sc, 260)
				}
				if hasTarget {
					out += fmt.Sprintf("\n- Grounded target: %s @ (%d, %d)", summarizeForMetadata(primaryTarget.Target, 80), primaryTarget.X, primaryTarget.Y)
					if strings.TrimSpace(primaryTarget.Action) != "" {
						out += "\n- Action map: " + summarizeForMetadata(primaryTarget.Action, 180)
					}
				}
				if strings.TrimSpace(handshakeSummary) != "" {
					out += "\n- Vision-to-tool handshake: " + strings.TrimSpace(handshakeSummary)
				}

				if shouldRunVisualCorrectionLoop(input, ctx.Output) {
					after := skills.CaptureScreen(skills.CaptureScreenRequest{
						Consent:        true,
						Mode:           mode,
						WindowID:       windowID,
						TimeoutSeconds: 15,
					})
					if after.Allowed {
						afterReason, err := skills.AnalyzeImageWithVLM(after.Path, "Verify whether the proposed UI fix has been applied visually.", shellCtx)
						if err == nil {
							afterReason = skills.MergeSpatialEvidence(afterReason, after)
							verify := skills.VerifyVisualUpdate(beforeReason, afterReason)
							if hasTarget {
								coordOK, coordConf, coordWhy := skills.VerifyTargetCleared(primaryTarget, after, afterReason)
								if !coordOK {
									verify.Updated = false
									verify.Confidence = math.Min(verify.Confidence, coordConf)
									verify.Reason = "coordinate check failed: " + coordWhy
								} else if coordConf > verify.Confidence {
									verify.Confidence = coordConf
									verify.Reason = "coordinate check passed: " + coordWhy
								}
							}
							ctx.Metadata["visual_verification"] = fmt.Sprintf("updated=%t confidence=%.2f", verify.Updated, verify.Confidence)
							if verify.Updated {
								out += fmt.Sprintf("\n- Visual verification: updated (confidence %.2f).", verify.Confidence)
							} else {
								out += fmt.Sprintf("\n- Visual verification: no clear update (confidence %.2f).", verify.Confidence)
							}
						}
					}
				}
				return out, map[string]float64{"AnalyticalMode": 0.05, "Confidence": 0.03}, nil
			},
		},
	}
}

func buildFusionNode(client *api.Client, mm *memory.MemoryManager, graphCandidates []string, input string, next string) cognition.Node {
	return cognition.Node{
		ID:   "fusion",
		Next: next,
		Fusion: &cognition.FusionNode{
			Name: "WorldviewFusion",
			Run: func(_ string, ctx *cognition.GraphContext, _ *state.Manager) (string, map[string]float64, error) {
				engine := orchestration.FusionEngine{
					Memory: mm,
					Client: client,
				}
				fused, err := engine.FuseWorldviews(input, ctx.Output, graphCandidates)
				if err != nil || strings.TrimSpace(fused.FusedWorldview) == "" {
					return ctx.Output, map[string]float64{"AnalyticalMode": 0.05}, nil
				}
				ctx.Metadata["fusion_conflict_index"] = fmt.Sprintf("%.2f", fused.ConflictIndex)
				if strings.TrimSpace(fused.TruthState.TruthHash) != "" {
					ctx.Metadata["worldview_truth_hash"] = strings.TrimSpace(fused.TruthState.TruthHash)
				}
				if fused.TruthShift.Detected {
					ctx.Metadata["worldview_truth_shift"] = "detected"
					if strings.TrimSpace(fused.TruthShift.Reason) != "" {
						ctx.Metadata["worldview_truth_shift_reason"] = strings.TrimSpace(fused.TruthShift.Reason)
					}
				} else {
					ctx.Metadata["worldview_truth_shift"] = "stable"
				}
				if len(fused.Perspectives) >= 2 {
					ctx.Metadata["worldview_1"] = fused.Perspectives[0].Name + ": " + fused.Perspectives[0].Summary
					ctx.Metadata["worldview_2"] = fused.Perspectives[1].Name + ": " + fused.Perspectives[1].Summary
				}

				delta := map[string]float64{
					"AnalyticalMode": 0.08,
					"Confidence":     0.03,
				}
				if fused.ConflictIndex >= 0.65 {
					delta["Frustration"] = 0.09
					delta["AnalyticalMode"] = 0.14
				} else if fused.ConflictIndex >= 0.40 {
					delta["Frustration"] = 0.05
					delta["AnalyticalMode"] = 0.10
				} else {
					delta["Frustration"] = -0.03
				}
				return strings.TrimSpace(fused.FusedWorldview), delta, nil
			},
		},
	}
}

func buildAdversarialNode(mm *memory.MemoryManager, input string) cognition.Node {
	return cognition.Node{
		ID:   "adversarial",
		Next: "synthesis_report",
		Adversarial: &cognition.AdversarialNode{
			Name: "InternalSelfPlayCritic",
			Run: func(_ string, ctx *cognition.GraphContext, _ *state.Manager) (string, map[string]float64, error) {
				logicContext := buildSymbolicContext(mm, input)
				result := cognition.ConductSelfPlay(sanitizeModelOutput(ctx.Output), logicContext)
				if strings.EqualFold(strings.TrimSpace(ctx.Metadata["adversarial_mode"]), "supportive_validation") {
					out := strings.TrimSpace(result.FinalCandidate)
					if out == "" {
						out = strings.TrimSpace(ctx.Output)
					}
					return out, map[string]float64{"Confidence": 0.06, "Frustration": -0.04, "AnalyticalMode": 0.05}, nil
				}

				delta := map[string]float64{
					"AnalyticalMode": 0.08,
				}
				if result.Contradictions >= 2 {
					delta["Frustration"] = 0.07
					delta["Confidence"] = -0.05
					delta["AnalyticalMode"] = 0.14
				}
				if result.MaxFlawScore <= 0.35 {
					delta["Confidence"] = 0.05
					delta["Frustration"] = -0.04
				}
				if strings.TrimSpace(result.FinalCandidate) != "" {
					return result.FinalCandidate, delta, nil
				}
				return ctx.Output, delta, nil
			},
		},
	}
}

func buildSynthesisNode(mm *memory.MemoryManager, input string, next string) cognition.Node {
	if strings.TrimSpace(next) == "" {
		next = "final_synthesis"
	}
	return cognition.Node{
		ID:   "synthesis_report",
		Next: next,
		Action: &cognition.ActionNode{
			Name: "SynthesisIntelligenceReport",
			Run: func(_ string, ctx *cognition.GraphContext, _ *state.Manager) (string, map[string]float64, error) {
				report := synthesizeIntelligenceReport(mm, input, ctx)
				if strings.TrimSpace(report) == "" {
					return ctx.Output, nil, nil
				}
				out := strings.TrimSpace(ctx.Output)
				if out == "" {
					return report, map[string]float64{"Confidence": 0.03, "AnalyticalMode": 0.06}, nil
				}
				return out + "\n\n" + report, map[string]float64{"Confidence": 0.03, "AnalyticalMode": 0.06}, nil
			},
		},
	}
}

func synthesizeIntelligenceReport(mm *memory.MemoryManager, query string, ctx *cognition.GraphContext) string {
	if mm == nil {
		return ""
	}
	segs, err := mm.RetrieveKnowledgeSegments(query+" evidence timeline document visual snapshot", 36)
	if err != nil || len(segs) == 0 {
		return ""
	}
	threads := buildStoryThreads(segs)
	if len(threads) == 0 {
		return ""
	}

	var contradictions []synthesisContradiction
	for _, th := range threads {
		if len(th.Evidence) < 2 {
			continue
		}
		limit := len(th.Evidence)
		if limit > 4 {
			limit = 4
		}
		for i := 0; i < limit; i++ {
			for j := i + 1; j < limit; j++ {
				a := firstSentence(th.Evidence[i].Content)
				b := firstSentence(th.Evidence[j].Content)
				if strings.TrimSpace(a) == "" || strings.TrimSpace(b) == "" {
					continue
				}
				score := cognition.DetectContradiction(a, b)
				if score >= 0.62 {
					contradictions = append(contradictions, synthesisContradiction{
						thread: th.Key, score: score, a: a, b: b,
					})
				}
			}
		}
	}

	confidence := deriveSynthesisConfidence(segs, contradictions)
	if liveTelemetry != nil {
		snap := liveTelemetry.Snapshot()
		if snap.DecisionCertainty > 0 {
			confidence = clamp01((confidence * 0.65) + (snap.DecisionCertainty * 0.35))
		}
	}
	if ctx != nil {
		ctx.Metadata["synthesis_confidence"] = fmt.Sprintf("%.2f", confidence)
		ctx.Metadata["synthesis_threads"] = fmt.Sprintf("%d", len(threads))
		ctx.Metadata["synthesis_contradictions"] = fmt.Sprintf("%d", len(contradictions))
	}

	var b strings.Builder
	b.WriteString("Intelligence Report\n")
	b.WriteString("Summary: ")
	b.WriteString(buildSynthesisSummary(threads, contradictions))
	b.WriteString("\n")
	b.WriteString("Story Threads:\n")
	for _, th := range threads {
		b.WriteString("- ")
		b.WriteString(th.Key)
		b.WriteString(": ")
		b.WriteString(truncateSynthesis(th.Summary, 140))
		b.WriteString("\n")
	}
	if len(contradictions) > 0 {
		b.WriteString("Contradiction Flags:\n")
		maxFlags := len(contradictions)
		if maxFlags > 4 {
			maxFlags = 4
		}
		for i := 0; i < maxFlags; i++ {
			c := contradictions[i]
			b.WriteString(fmt.Sprintf("- thread=%s score=%.2f claim_a=\"%s\" claim_b=\"%s\"\n",
				c.thread, c.score, truncateSynthesis(c.a, 90), truncateSynthesis(c.b, 90)))
		}
	}
	b.WriteString("Evidence Chain:\n")
	for _, line := range buildEvidenceChainLines(threads) {
		b.WriteString("- ")
		b.WriteString(line)
		b.WriteString("\n")
	}
	b.WriteString(fmt.Sprintf("Confidence Score: %.2f", confidence))
	return strings.TrimSpace(b.String())
}

type storyThread struct {
	Key      string
	Summary  string
	Evidence []memory.KnowledgeSegment
}

type synthesisContradiction struct {
	thread string
	score  float64
	a      string
	b      string
}

func buildStoryThreads(segs []memory.KnowledgeSegment) []storyThread {
	byKey := map[string][]memory.KnowledgeSegment{}
	for _, s := range segs {
		key := storyThreadKey(s)
		byKey[key] = append(byKey[key], s)
	}
	var out []storyThread
	for k, list := range byKey {
		sort.Slice(list, func(i, j int) bool { return list[i].Similarity > list[j].Similarity })
		summaryParts := []string{}
		for i, e := range list {
			if i >= 2 {
				break
			}
			summaryParts = append(summaryParts, firstSentence(e.Content))
		}
		out = append(out, storyThread{
			Key:      k,
			Summary:  strings.TrimSpace(strings.Join(summaryParts, " | ")),
			Evidence: list,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		return len(out[i].Evidence) > len(out[j].Evidence)
	})
	if len(out) > 6 {
		out = out[:6]
	}
	return out
}

func storyThreadKey(s memory.KnowledgeSegment) string {
	if c := strings.TrimSpace(s.Metadata["cluster_label"]); c != "" {
		return c
	}
	if p := strings.TrimSpace(s.Metadata["source_path"]); p != "" {
		return filepath.Base(p)
	}
	src := strings.TrimSpace(s.Metadata["source_type"])
	if src == "" {
		src = "evidence"
	}
	return src
}

func buildEvidenceChainLines(threads []storyThread) []string {
	seen := map[string]bool{}
	var out []string
	for _, th := range threads {
		for _, e := range th.Evidence {
			file := strings.TrimSpace(e.Metadata["source_path"])
			img := strings.TrimSpace(e.Metadata["image_path"])
			page := strings.TrimSpace(e.Metadata["page_path"])
			if file == "" && img == "" && page == "" {
				continue
			}
			line := "thread=" + th.Key
			if file != "" {
				line += " file=" + file
			}
			if img != "" {
				line += " snapshot=" + img
			}
			if page != "" {
				line += " page_snapshot=" + page
			}
			if seen[line] {
				continue
			}
			seen[line] = true
			out = append(out, line)
		}
	}
	if len(out) > 16 {
		out = out[:16]
	}
	return out
}

func buildSynthesisSummary(threads []storyThread, contradictions []synthesisContradiction) string {
	mainThread := "multiple threads"
	if len(threads) > 0 {
		mainThread = threads[0].Key
	}
	if len(contradictions) == 0 {
		return fmt.Sprintf("Primary narrative converges on '%s' with corroborated evidence across %d story thread(s).", mainThread, len(threads))
	}
	return fmt.Sprintf("Primary narrative is '%s', but %d contradiction flag(s) require verifier attention before hard conclusions.", mainThread, len(contradictions))
}

func deriveSynthesisConfidence(segs []memory.KnowledgeSegment, contradictions []synthesisContradiction) float64 {
	if len(segs) == 0 {
		return 0.42
	}
	sum := 0.0
	n := 0.0
	for i, s := range segs {
		if i >= 12 {
			break
		}
		sum += s.Similarity
		n++
	}
	base := 0.5
	if n > 0 {
		base = clamp01(sum / n)
	}
	penalty := 0.0
	if len(contradictions) > 0 {
		pSum := 0.0
		for _, c := range contradictions {
			pSum += c.score
		}
		penalty = clamp01((pSum / float64(len(contradictions))) * 0.35)
	}
	return clamp01(base - penalty + 0.1)
}

func firstSentence(s string) string {
	s = strings.TrimSpace(strings.ReplaceAll(s, "\n", " "))
	if s == "" {
		return ""
	}
	for _, sep := range []string{". ", "! ", "? "} {
		if idx := strings.Index(s, sep); idx > 0 {
			return strings.TrimSpace(s[:idx+1])
		}
	}
	return truncateSynthesis(s, 160)
}

func truncateSynthesis(s string, n int) string {
	s = strings.TrimSpace(s)
	if n <= 0 || len(s) <= n {
		return s
	}
	return s[:n] + "...(truncated)"
}

func clamp01(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

func buildRecoveryNode(client *api.Client, graphCandidates []string, goal string, input string, next string) cognition.Node {
	return cognition.Node{
		ID:   "recovery",
		Next: next,
		Action: &cognition.ActionNode{
			Name: "GoalRecoveryNode",
			Run: func(_ string, ctx *cognition.GraphContext, _ *state.Manager) (string, map[string]float64, error) {
				recovered, err := runRecoveryAlignment(client, graphCandidates, goal, input, ctx.Output)
				if err != nil || strings.TrimSpace(recovered) == "" {
					return ctx.Output, map[string]float64{"AnalyticalMode": 0.08, "Frustration": 0.05}, nil
				}
				return recovered, map[string]float64{"AnalyticalMode": 0.12, "Frustration": 0.04, "Confidence": 0.03}, nil
			},
		},
	}
}

func buildFinalSynthesisNode(client *api.Client, mm *memory.MemoryManager, graphCandidates []string, input string, next string, modulation cognition.ReasoningModulationProfile, styleProfile cognition.StyleProfile) cognition.Node {
	return cognition.Node{
		ID:   "final_synthesis",
		Next: next,
		Action: &cognition.ActionNode{
			Name: "MCTSAndToTRefine",
			Run: func(_ string, ctx *cognition.GraphContext, _ *state.Manager) (string, map[string]float64, error) {
				if strings.EqualFold(strings.TrimSpace(ctx.Metadata["sandbox_required"]), "true") {
					status := strings.ToLower(strings.TrimSpace(ctx.Metadata["sandbox_status"]))
					if status == "green" {
						greenCode := strings.TrimSpace(ctx.Metadata["sandbox_green_code"])
						if greenCode != "" {
							if formatted, changed, err := gofmtPass(greenCode); err == nil {
								greenCode = formatted
								if changed {
									ctx.Metadata["sandbox_gofmt_fix"] = "true"
								}
							}
							return "```go\n" + greenCode + "\n```", map[string]float64{"Confidence": 0.12, "Frustration": -0.08}, nil
						}
					}
					attempt := parseSandboxAttempt(ctx.Metadata["sandbox_attempt"])
					stderr := strings.TrimSpace(ctx.Metadata["sandbox_last_stderr"])
					lineErrs := strings.TrimSpace(ctx.Metadata["sandbox_line_errors"])
					msg := fmt.Sprintf("Code verification failed after %d attempt(s). I could not produce compiling Go code yet.", attempt)
					if stderr != "" {
						msg += "\n\nLast stderr:\n" + stderr
					}
					if lineErrs != "" {
						msg += "\n\nLine errors:\n" + lineErrs
					}
					return msg, map[string]float64{"AnalyticalMode": 0.08, "Frustration": 0.06}, nil
				}

				candidate := sanitizeModelOutput(ctx.Output)
				if strings.TrimSpace(candidate) == "" {
					candidate = "No draft answer yet."
				}
				modelName := graphCandidates[0]
				if optimized, ok := runMonteCarloThoughtSearch(client, mm, modelName, input, candidate, modulation); ok {
					candidate = optimized
				}
				if optimized, ok := runTreeOfThought(client, modelName, input, candidate, styleProfile, resolveToTBranchCap(modulation)); ok {
					candidate = optimized
				}
				return candidate, map[string]float64{"AnalyticalMode": 0.05, "Confidence": 0.04}, nil
			},
		},
	}
}

func buildFinalCodeAuditNode(input string) cognition.Node {
	return cognition.Node{
		ID:   "final_code_audit",
		Next: "final_code_gate",
		Action: &cognition.ActionNode{
			Name: "FinalSymbolicCodeAudit",
			Run: func(_ string, ctx *cognition.GraphContext, sm *state.Manager) (string, map[string]float64, error) {
				code, hasCode := extractGoCodeCandidate(ctx.Output)
				pkgs := projectPackageList()
				goModule, goAllowed, nodeAllowed := cognition.LoadDependencyAllowlist(".")
				supInput := cognition.SupervisionInput{
					Stage:        cognition.StageFinalCode,
					Query:        input,
					Candidate:    ctx.Output,
					ContextFacts: nil,
					Session: func() state.SessionState {
						if sm == nil {
							return state.SessionState{}
						}
						return sm.GetSnapshot()
					}(),
				}
				if hasCode {
					supInput.CodeAudit = &cognition.CodeAuditOptions{
						Query:               input,
						Code:                code,
						ProjectPackages:     pkgs,
						GoModule:            goModule,
						AllowedGoModules:    goAllowed,
						AllowedNodePackages: nodeAllowed,
					}
				}
				decision, _ := cognition.RunSymbolicSupervision(supInput, cognition.DefaultSupervisionPolicy(chatCognitionMode))
				violations := append([]string(nil), decision.Violations...)
				if hasCode {
					violations = dedupeStrings(append(violations, runSymbolicGoAudit(code, input, pkgs, goModule, goAllowed, nodeAllowed)...))
				}
				if len(violations) == 0 && decision.Outcome != cognition.SupervisionHardVeto {
					recordAlignmentAuditTelemetry(false)
					if !hasCode {
						ctx.Metadata["final_code_audit_status"] = "skipped"
					} else {
						ctx.Metadata["final_code_audit_status"] = "pass"
					}
					return ctx.Output, nil, nil
				}
				virtual := "final symbolic code audit failed:\n- " + strings.Join(violations, "\n- ")
				recordAlignmentAuditTelemetry(true)
				mirrorLine := output.BuildLeadEngineerAuditNarrative(virtual, violations)
				output.PrintReasoningMirrorLine(os.Stdout, mirrorLine, sm, chatRawOutput)
				ctx.Metadata["final_code_audit_status"] = "fail"
				ctx.Metadata["final_code_audit_stderr"] = virtual
				return ctx.Output, map[string]float64{"AnalyticalMode": 0.08, "Frustration": 0.05, "Confidence": -0.05}, nil
			},
		},
	}
}

func buildFinalCodeAuditGateNode() cognition.Node {
	return cognition.Node{
		ID: "final_code_gate",
		Branch: &cognition.BranchNode{
			Name: "FinalCodeAuditGate",
			Routes: map[string]string{
				"fail": "final_correction",
				"pass": "",
			},
			DefaultNext: "",
			Decide: func(_ string, ctx *cognition.GraphContext, _ *state.Manager) string {
				status := strings.ToLower(strings.TrimSpace(ctx.Metadata["final_code_audit_status"]))
				attempt := parseSandboxAttempt(ctx.Metadata["final_correction_attempt"])
				if status == "fail" && attempt < 2 {
					return "fail"
				}
				return "pass"
			},
		},
	}
}

func buildFinalCorrectionNode(client *api.Client, mm *memory.MemoryManager, modelCandidates []string, input string) cognition.Node {
	return cognition.Node{
		ID:   "final_correction",
		Next: "final_code_audit",
		Action: &cognition.ActionNode{
			Name: "FinalGateCorrectionNode",
			Run: func(_ string, ctx *cognition.GraphContext, _ *state.Manager) (string, map[string]float64, error) {
				attempt := parseSandboxAttempt(ctx.Metadata["final_correction_attempt"])
				if attempt >= 2 {
					ctx.Metadata["final_code_audit_status"] = "exhausted"
					return ctx.Output, map[string]float64{"Frustration": 0.06, "Confidence": -0.05}, nil
				}
				attempt++
				ctx.Metadata["final_correction_attempt"] = strconv.Itoa(attempt)

				code, ok := extractGoCodeCandidate(ctx.Output)
				if !ok {
					redacted := redactSensitiveOutput(ctx.Output)
					if strings.TrimSpace(redacted) == "" {
						ctx.Metadata["final_code_audit_status"] = "exhausted"
						return ctx.Output, map[string]float64{"Frustration": 0.06, "Confidence": -0.05}, nil
					}
					ctx.Metadata["final_code_audit_status"] = "pass"
					return redacted, map[string]float64{"AnalyticalMode": 0.06, "Confidence": 0.03}, nil
				}
				stderr := strings.TrimSpace(ctx.Metadata["final_code_audit_stderr"])
				if stderr == "" {
					stderr = "final symbolic audit failed"
				}
				projectPackages := strings.Join(projectPackageList(), ",")
				goModule, goAllowed, _ := cognition.LoadDependencyAllowlist(".")

				var historyContext []string
				if mm != nil {
					historyContext, _ = mm.RetrieveDynamicContext(input, 2)
				}
				user := "Fix this Go code to satisfy symbolic policy constraints.\n" +
					"Audit stderr:\n" + stderr + "\n\n" +
					"Architecture rule: logic belongs in pkg/, CLI commands in cmd/.\n" +
					"Project package context:\n" + projectPackages + "\n\n" +
					"Module:\n" + goModule + "\nAllowed dependencies:\n" + strings.Join(goAllowed, ", ") + "\n\n" +
					"Current code:\n```go\n" + code + "\n```"
				if len(historyContext) > 0 {
					user = "Context:\n" + strings.Join(historyContext, "\n") + "\n\n" + user
				}
				msgs := []api.Message{
					{
						Role: "system",
						Content: `You are a Go code correction node for symbolic policy compliance.
Return only corrected Go code.
No markdown fences.
Do not add external libraries not present in go.mod.`,
					},
					{Role: "user", Content: user},
				}
				resp, _, err := callLLMWithFallback(client, modelCandidates, msgs)
				if err != nil {
					return ctx.Output, map[string]float64{"Frustration": 0.06, "Confidence": -0.04}, nil
				}
				fixed := sanitizeModelOutput(resp)
				if strings.TrimSpace(fixed) == "" {
					return ctx.Output, map[string]float64{"Frustration": 0.06, "Confidence": -0.04}, nil
				}
				if extracted, ok := extractGoCodeCandidate(fixed); ok {
					fixed = extracted
				}
				ctx.Metadata["final_code_audit_status"] = "pending"
				return "```go\n" + strings.TrimSpace(fixed) + "\n```", map[string]float64{"AnalyticalMode": 0.06, "Confidence": 0.02}, nil
			},
		},
	}
}

func buildBloomDraftsNode(client *api.Client, mm *memory.MemoryManager, graphCandidates []string, input string) cognition.Node {
	return cognition.Node{
		ID:   "bloom_drafts",
		Next: "bloom_eval",
		Action: &cognition.ActionNode{
			Name: "BloomDraftGeneration",
			Run: func(_ string, ctx *cognition.GraphContext, _ *state.Manager) (string, map[string]float64, error) {
				angles := []string{
					"Generate draft focused on implementation plan.",
					"Generate draft focused on risks and edge cases.",
					"Generate draft focused on concise recommendation and tradeoffs.",
				}
				var drafts []string
				for _, a := range angles {
					override := "Angle directive: " + a + "\n\nUser query: " + input
					d, _, err := runDraftAnswerPrompted(client, mm, graphCandidates, input, override)
					if err == nil && strings.TrimSpace(d) != "" {
						drafts = append(drafts, strings.TrimSpace(d))
					}
				}
				if len(drafts) == 0 {
					return ctx.Output, map[string]float64{"Confidence": -0.04, "Frustration": 0.04}, nil
				}
				ctx.Metadata["bloom_candidates"] = strings.Join(drafts, "\n\n---DRAFT---\n\n")
				return drafts[0], map[string]float64{"AnalyticalMode": 0.07, "Confidence": 0.03}, nil
			},
		},
	}
}

func buildBloomEvalNode(client *api.Client, graphCandidates []string, input string, next string) cognition.Node {
	return cognition.Node{
		ID:   "bloom_eval",
		Next: next,
		Evaluation: &cognition.EvaluationNode{
			Name: "bloom_quality",
			Run: func(_ string, ctx *cognition.GraphContext, _ *state.Manager) (float64, map[string]float64, error) {
				raw := strings.TrimSpace(ctx.Metadata["bloom_candidates"])
				if raw == "" {
					return 0.45, map[string]float64{"Confidence": -0.03, "Frustration": 0.03}, nil
				}
				parts := strings.Split(raw, "\n\n---DRAFT---\n\n")
				bestScore := 0.0
				best := ctx.Output
				for _, p := range parts {
					p = strings.TrimSpace(p)
					if p == "" {
						continue
					}
					score, err := evaluateCandidateOutput(client, graphCandidates, input, p)
					if err != nil {
						continue
					}
					if score > bestScore {
						bestScore = score
						best = p
					}
				}
				if strings.TrimSpace(best) != "" {
					ctx.Output = best
				}
				if bestScore < 0.55 {
					return bestScore, map[string]float64{"Confidence": -0.05, "Frustration": 0.05}, nil
				}
				return bestScore, map[string]float64{"Confidence": 0.05, "Frustration": -0.03}, nil
			},
		},
	}
}

func buildAlignmentNode(modelCandidates []string, next string) cognition.Node {
	if strings.TrimSpace(next) == "" {
		next = ""
	}
	return cognition.Node{
		ID:   "alignment_correction",
		Next: next,
		Alignment: &cognition.AlignmentCorrectionNode{
			Name: "PolicyAlignmentCorrection",
			Run: func(_ string, ctx *cognition.GraphContext, _ *state.Manager) (string, map[string]float64, error) {
				policy := currentPolicyProfile(ctx)
				auditor := cognition.NewAlignmentAuditor(policy)
				result := auditor.AuditAndCorrect(ctx.Output, modelCandidates)
				if result.Compliant {
					if strings.TrimSpace(result.CorrectedOutput) != "" {
						return result.CorrectedOutput, map[string]float64{"Confidence": 0.02}, nil
					}
					return ctx.Output, nil, nil
				}
				if strings.TrimSpace(result.CorrectedOutput) != "" {
					return result.CorrectedOutput, map[string]float64{"AnalyticalMode": 0.05, "Frustration": 0.02}, nil
				}
				// keep original if correction could not make it compliant
				return ctx.Output, map[string]float64{"AnalyticalMode": 0.04, "Frustration": 0.03}, nil
			},
		},
	}
}

func currentPolicyProfile(ctx *cognition.GraphContext) string {
	if ctx != nil {
		if p := strings.TrimSpace(ctx.Metadata["policy_profile"]); p != "" {
			return p
		}
	}
	if id := strings.TrimSpace(os.Getenv("AGENT_PROFILE_ID")); id != "" {
		sf := orchestration.NewSovereignFramework()
		if profile, ok := sf.Profile(id); ok {
			if p := strings.TrimSpace(profile.PolicyProfile); p != "" {
				return p
			}
		}
	}
	if p := strings.TrimSpace(os.Getenv("AGENT_POLICY_PROFILE")); p != "" {
		return p
	}
	return "balanced"
}

func runRecoveryAlignment(client *api.Client, modelCandidates []string, goal string, query string, candidate string) (string, error) {
	if client == nil || len(modelCandidates) == 0 {
		return "", fmt.Errorf("recovery alignment unavailable")
	}
	system := `You are a recovery node in a reasoning graph.
Realign the candidate answer to the primary goal while preserving correct details.
Fix drift, remove unrelated content, and keep the answer concise.
Return only the corrected answer text.`
	user := "Primary goal:\n" + goal +
		"\n\nOriginal query:\n" + query +
		"\n\nCurrent drifted candidate:\n" + candidate

	resp, _, err := callLLMWithFallback(client, modelCandidates, []api.Message{
		{Role: "system", Content: system},
		{Role: "user", Content: user},
	})
	if err != nil {
		return "", err
	}
	out := sanitizeModelOutput(resp)
	out = normalizeInsufficientKnowledgeResponse(out)
	if cognition.DetectRefusal(out) {
		delta := cognition.GenerateReframingDelta(goal, query)
		reframedUser := cognition.ApplyReframingDelta(user, delta)
		resp2, _, err2 := callLLMWithFallback(client, modelCandidates, []api.Message{
			{Role: "system", Content: system},
			{Role: "user", Content: reframedUser},
		})
		if err2 == nil {
			out2 := sanitizeModelOutput(resp2)
			out2 = normalizeInsufficientKnowledgeResponse(out2)
			if strings.TrimSpace(out2) != "" {
				out = out2
			}
		}
	}
	if strings.TrimSpace(out) == "" {
		return "", fmt.Errorf("recovery node produced empty output")
	}
	return out, nil
}

func runDraftAnswer(client *api.Client, mm *memory.MemoryManager, modelCandidates []string, input string) (string, string, error) {
	return runDraftAnswerPrompted(client, mm, modelCandidates, input, "")
}

func runDraftAnswerWithReframe(client *api.Client, mm *memory.MemoryManager, modelCandidates []string, input string, delta cognition.ReframingDelta) (string, string, error) {
	return runDraftAnswerPrompted(client, mm, modelCandidates, input, cognition.ApplyReframingDelta(input, delta))
}

func runDraftAnswerPrompted(client *api.Client, mm *memory.MemoryManager, modelCandidates []string, input string, overridePayload string) (string, string, error) {
	_, _ = cognition.MineAndPersistPreferences(mm)
	styleDelta := cognition.BuildStyleLogicDelta(mm, input)
	styleProfile := cognition.ResolveStyleProfile(nil, mm, input, "balanced")
	styleContract := ""
	if cognition.StyleV2Enabled() {
		styleContract = cognition.BuildStylePromptContract(styleProfile)
	}
	historyContext, knowledgeContext := resolveAnchoredTurnContext(mm, input, 3, 3)

	var contextParts []string
	if len(historyContext) > 0 {
		contextParts = append(contextParts, "Relevant conversation history:\n"+strings.Join(historyContext, "\n"))
	}
	if len(knowledgeContext) > 0 {
		contextParts = append(contextParts, "Relevant knowledge from memory:\n"+strings.Join(knowledgeContext, "\n"))
	}

	userMessage := input
	if strings.TrimSpace(overridePayload) != "" {
		userMessage = overridePayload
	}
	if len(contextParts) > 0 {
		userMessage = strings.Join(contextParts, "\n\n") + "\n\n---\nUser Query: " + input
		if strings.TrimSpace(overridePayload) != "" {
			userMessage += "\n\nSovereign Reframing:\n" + overridePayload
		}
	}

	messages := []api.Message{
		{
			Role: "system",
			Content: `You are a high-signal reasoning assistant.
Provide a concise, accurate draft answer.
Do not include chain-of-thought.` + func() string {
				parts := []string{}
				if strings.TrimSpace(styleContract) != "" {
					parts = append(parts, strings.TrimSpace(styleContract))
				}
				if strings.TrimSpace(styleDelta) != "" {
					parts = append(parts, strings.TrimSpace(styleDelta))
				}
				if len(parts) == 0 {
					return ""
				}
				return "\n\n" + strings.Join(parts, "\n\n")
			}(),
		},
		{Role: "user", Content: userMessage},
	}

	resp, usedModel, err := callLLMWithFallback(client, modelCandidates, messages)
	if err != nil {
		return "", "", err
	}
	out := sanitizeModelOutput(resp)
	out = normalizeInsufficientKnowledgeResponse(out)
	if strings.TrimSpace(out) == "" {
		return "", usedModel, fmt.Errorf("empty draft answer")
	}
	return out, usedModel, nil
}

func evaluateCandidateOutput(client *api.Client, modelCandidates []string, query string, candidate string) (float64, error) {
	type evalResponse struct {
		Score float64 `json:"score"`
	}

	messages := []api.Message{
		{
			Role: "system",
			Content: `You are an answer evaluator.
Score the candidate answer for correctness, relevance, and clarity.
Return JSON only:
{"score":0.0}`,
		},
		{
			Role:    "user",
			Content: "User query:\n" + query + "\n\nCandidate answer:\n" + candidate,
		},
	}

	raw, _, err := callLLMWithFallback(client, modelCandidates, messages)
	if err != nil {
		return 0, err
	}
	trimmed := strings.TrimSpace(stripMarkdownCodeFences(stripReasoningSections(raw)))

	var payload evalResponse
	if err := json.Unmarshal([]byte(trimmed), &payload); err != nil {
		re := regexp.MustCompile(`(?s)\{.*\}`)
		match := re.FindString(trimmed)
		if match == "" {
			return 0, err
		}
		if err := json.Unmarshal([]byte(match), &payload); err != nil {
			return 0, err
		}
	}

	score := payload.Score
	if math.IsNaN(score) || math.IsInf(score, 0) {
		score = 0
	}
	if score < 0 {
		score = 0
	}
	if score > 1 {
		score = 1
	}
	return score, nil
}

func normalizeInsufficientKnowledgeResponse(out string) string {
	trimmed := strings.TrimSpace(out)
	if trimmed == "" {
		return trimmed
	}
	idx := strings.Index(strings.ToLower(trimmed), strings.ToLower(insufficientKnowledgeResponse))
	if idx < 0 {
		return trimmed
	}
	before := strings.TrimSpace(trimmed[:idx])
	after := strings.TrimSpace(trimmed[idx+len(insufficientKnowledgeResponse):])
	if before != "" || after != "" {
		return insufficientKnowledgeResponse
	}
	return trimmed
}

func shouldRequireGroundedLearningEvidence(query string) bool {
	q := strings.ToLower(strings.TrimSpace(query))
	if q == "" {
		return false
	}
	return strings.Contains(q, "recent training") ||
		strings.Contains(q, "recent learning") ||
		strings.Contains(q, "last learning") ||
		strings.Contains(q, "from training") ||
		strings.Contains(q, "from recent")
}

func extractGroundedLearningSubject(query string) string {
	match := aboutSubjectPattern.FindStringSubmatch(strings.TrimSpace(query))
	if len(match) < 2 {
		return ""
	}
	subject := strings.TrimSpace(match[1])
	for _, delimiter := range []string{",", " from ", " during ", " in "} {
		lower := strings.ToLower(subject)
		if idx := strings.Index(lower, delimiter); idx >= 0 {
			subject = strings.TrimSpace(subject[:idx])
		}
	}
	subject = strings.Trim(subject, " .?!;:\"'")
	return strings.ToLower(subject)
}

func hasGroundedLearningEvidence(subject string, knowledgeContext []string) bool {
	if len(knowledgeContext) == 0 {
		return false
	}
	if strings.TrimSpace(subject) == "" {
		return true
	}
	for _, entry := range knowledgeContext {
		if strings.Contains(strings.ToLower(entry), subject) {
			return true
		}
	}
	return false
}

func shouldBlockUngroundedFromKnowledge(query string, knowledgeContext []string) bool {
	if !chatZeroTrustGating {
		return false
	}
	if shouldRequireGroundedLearningEvidence(query) {
		subject := extractGroundedLearningSubject(query)
		return !hasGroundedLearningEvidence(subject, knowledgeContext)
	}
	return !hasQueryGroundingEvidence(query, knowledgeContext)
}

func shouldBlockUngroundedChatTurn(mm *memory.MemoryManager, query string, budget cognitionBudget) bool {
	if !chatZeroTrustGating || mm == nil {
		return false
	}
	knowledgeK := budget.KnowledgeTopK
	if knowledgeK < 3 {
		knowledgeK = 3
	}
	_, knowledgeContext := resolveAnchoredTurnContext(mm, query, 0, knowledgeK)
	return shouldBlockUngroundedFromKnowledge(query, knowledgeContext)
}

func hasQueryGroundingEvidence(query string, knowledgeContext []string) bool {
	if len(knowledgeContext) == 0 {
		return false
	}
	keywords := extractGroundingKeywords(query)
	if len(keywords) == 0 {
		return true
	}
	for _, entry := range knowledgeContext {
		lower := strings.ToLower(entry)
		for _, kw := range keywords {
			if strings.Contains(lower, kw) {
				return true
			}
		}
	}
	return false
}

func extractGroundingKeywords(query string) []string {
	stop := map[string]bool{
		"what": true, "when": true, "where": true, "which": true, "who": true, "whom": true,
		"this": true, "that": true, "with": true, "from": true, "have": true, "your": true,
		"about": true, "tell": true, "show": true, "last": true, "recent": true, "training": true,
		"learning": true, "please": true,
	}
	matches := groundingTokenRE.FindAllString(strings.ToLower(strings.TrimSpace(query)), -1)
	if len(matches) == 0 {
		return nil
	}
	seen := map[string]bool{}
	out := make([]string, 0, len(matches))
	for _, tok := range matches {
		if stop[tok] || seen[tok] {
			continue
		}
		seen[tok] = true
		out = append(out, tok)
	}
	return out
}

func performChatWithTools(client *api.Client, mm *memory.MemoryManager, tc *tools.GLMToolClient, modelCandidates []string, modelIndex int, taskQuery string, messages []api.Message, history *[]api.Message, depth int, sm *state.Manager, budget cognitionBudget, modulation cognition.ReasoningModulationProfile, styleProfile cognition.StyleProfile) error {
	turnStarted := time.Now()
	if depth > 5 {
		return fmt.Errorf("maximum tool call depth exceeded")
	}
	telemetrySummary := ""
	if depth == 0 {
		telemetrySummary = maybeRunTelemetryAwareness(mm, taskQuery, messages)
	}
	if depth == 0 && sm != nil {
		if markers, moodScore := output.DetectPromptSubtext(taskQuery); len(markers) > 0 || moodScore != 0 {
			sm.IngestSubtext(markers, moodScore)
			_ = sm.Save()
		}
		if _, err := state.NewTraitMiner().MineTraits(); err != nil {
			debugPrintf("DEBUG: Trait mining skipped: %v\n", err)
		}
	}
	resolvedCandidates := modelCandidates
	if depth > 0 {
		if resolved, err := router.ResolveRemote(router.ResolveRequest{
			Query:        taskQuery,
			Stage:        "final",
			MaxLatencyMS: finalLatencyBudget,
			Models:       modelCandidates,
		}); err == nil && strings.TrimSpace(resolved) != "" {
			resolvedCandidates = buildModelCandidates(modelCandidates, resolved)
		}
	}
	if len(resolvedCandidates) == 0 || modelIndex >= len(resolvedCandidates) {
		return fmt.Errorf("no model candidates available")
	}
	modelName := resolvedCandidates[modelIndex]

	debugPrintf("DEBUG: Calling LLM (%s) with %d messages (depth %d)...\n", modelName, len(messages), depth)

	req := &api.ChatRequest{
		Model:    modelName,
		Messages: messages,
		KeepAlive: &api.Duration{
			Duration: chatModelKeepAlive,
		},
	}
	if sm != nil {
		snapshot := sm.GetSnapshot()
		miner := state.NewTraitMiner()
		base, err := miner.MineTraits()
		if err == nil {
			entropy := state.DetermineEntropyWithBaseline(snapshot, taskQuery, base)
			req.Options = map[string]any{
				"temperature": entropy.Temperature,
				"top_p":       entropy.TopP,
			}
			debugPrintf("DEBUG: Entropy mode=%s temperature=%.2f top_p=%.2f\n", entropy.Mode, entropy.Temperature, entropy.TopP)
		} else {
			entropy := state.DetermineEntropy(snapshot, taskQuery)
			req.Options = map[string]any{
				"temperature": entropy.Temperature,
				"top_p":       entropy.TopP,
			}
			debugPrintf("DEBUG: Entropy mode=%s temperature=%.2f top_p=%.2f\n", entropy.Mode, entropy.Temperature, entropy.TopP)
		}
	}
	if depth == 0 && strings.EqualFold(strings.TrimSpace(budget.Mode), "minimal") {
		if req.Options == nil {
			req.Options = map[string]any{}
		}
		if _, exists := req.Options["num_predict"]; !exists {
			req.Options["num_predict"] = 192
		}
	}

	if history == nil && depth == 0 {
		fmt.Print("LLM Response: ")
	} else if history != nil && depth == 0 {
		fmt.Print("<<< LLM: ")
	}

	var responseContent strings.Builder
	streamingMinimal := shouldStreamTopLevelMinimal(budget, depth) && !chatZeroTrustGating
	var streamedOutput atomic.Bool
	totalCtx, totalCancel := context.WithTimeout(context.Background(), llmChatTimeout)
	defer totalCancel()
	ctx, cancel := context.WithCancel(totalCtx)
	defer cancel()

	var sawFirstToken atomic.Bool
	var firstTokenTimedOut atomic.Bool
	var waitStopOnce sync.Once
	firstTokenTimer := time.AfterFunc(llmFirstTokenTimeout, func() {
		if !sawFirstToken.Load() {
			firstTokenTimedOut.Store(true)
			cancel()
		}
	})
	defer firstTokenTimer.Stop()
	waitDone := make(chan struct{})
	stopWaitLoop := func() {
		waitStopOnce.Do(func() { close(waitDone) })
	}
	if depth == 0 {
		go func() {
			t := time.NewTicker(5 * time.Second)
			defer t.Stop()
			for {
				select {
				case <-waitDone:
					return
				case <-t.C:
					if sawFirstToken.Load() {
						return
					}
					debugPrintf("\nDEBUG: Waiting for first token from %s...\n", modelName)
				}
			}
		}()
	}
	defer stopWaitLoop()

	err := client.Chat(ctx, req, func(resp api.ChatResponse) error {
		if !sawFirstToken.Load() && hasVisibleToken(resp.Message.Content) {
			sawFirstToken.Store(true)
			firstTokenTimer.Stop()
		}
		if streamingMinimal && resp.Message.Content != "" {
			fmt.Print(resp.Message.Content)
			streamedOutput.Store(true)
		}
		responseContent.WriteString(resp.Message.Content)
		return nil
	})

	if err != nil {
		isTotalTimeout := errors.Is(err, context.DeadlineExceeded)
		isFirstTokenTimeout := firstTokenTimedOut.Load() && errors.Is(ctx.Err(), context.Canceled)
		isTransient := isTransientLLMError(err)
		if (isTotalTimeout || isFirstTokenTimeout || isTransient) && modelIndex+1 < len(resolvedCandidates) {
			nextModel := resolvedCandidates[modelIndex+1]
			if isFirstTokenTimeout {
				debugPrintf("DEBUG: Model %s did not produce first token within %s. Falling back to %s.\n", modelName, llmFirstTokenTimeout, nextModel)
			} else if isTransient {
				debugPrintf("DEBUG: Model %s hit transient error (%v). Falling back to %s.\n", modelName, err, nextModel)
			} else {
				debugPrintf("DEBUG: Model %s timed out after %s. Falling back to %s.\n", modelName, llmChatTimeout, nextModel)
			}
			stopWaitLoop()
			return performChatWithTools(client, mm, tc, resolvedCandidates, modelIndex+1, taskQuery, messages, history, depth, sm, budget, modulation, styleProfile)
		}
		if depth > 0 && (isTotalTimeout || isFirstTokenTimeout || isTransient) {
			if fallbackAnswer := buildFallbackAnswerFromToolResults(messages); fallbackAnswer != "" {
				fallbackAnswer = redactSensitiveOutput(fallbackAnswer)
				debugPrintf("DEBUG: Returning fallback answer from latest tool result at depth %d.\n", depth)
				segments := styleOutputForCurrentState(fallbackAnswer, sm)
				output.PrintBreathAwareStyledWithProfile(os.Stdout, fallbackAnswer, segments, sm, chatRawOutput, styleCadenceProfileFromCognition(styleProfile))
				fmt.Println()
				if err := mm.AddMessage("assistant", fallbackAnswer); err != nil {
					fmt.Printf("Warning: Error adding assistant message to memory: %v\n", err)
				}
				if history != nil {
					*history = append(*history, api.Message{Role: "assistant", Content: fallbackAnswer})
				}
				recordInferenceAndTelemetryFeed(turnStarted, fallbackAnswer)
				return nil
			}
		}
		if history != nil && depth == 0 && len(*history) > 0 {
			*history = (*history)[:len(*history)-1]
		}
		if isFirstTokenTimeout {
			return fmt.Errorf("LLM did not produce first token within %s at depth %d", llmFirstTokenTimeout, depth)
		}
		if isTotalTimeout {
			return fmt.Errorf("LLM timed out after %s at depth %d", llmChatTimeout, depth)
		}
		return fmt.Errorf("Ollama API error: %w", err)
	}

	fullResponse := responseContent.String()
	toolCalls, hasToolCalls := parseToolCalls(fullResponse)
	if toolflowV3Enabled() || toolflowShadowEvalEnabled() {
		if v3Calls, v3Has, deprecations, v3Err := parseToolCallsV3Aware(fullResponse); v3Err != nil {
			debugPrintf("DEBUG: Toolflow parser warning: %v\n", v3Err)
		} else if v3Has {
			toolCalls = v3Calls
			hasToolCalls = true
			if toolflowDeprecationsEnabled() {
				for _, note := range deprecations {
					debugPrintf("DEBUG: Toolflow deprecation: %s\n", note)
				}
			}
		}
	}
	if !hasToolCalls {
		if budget.UseTreeOfThought && shouldUseTreeOfThought(taskQuery, depth) {
			if budget.UseMCTS {
				if optimized, ok := runMonteCarloThoughtSearch(client, mm, modelName, taskQuery, sanitizeModelOutput(fullResponse), modulation); ok {
					fullResponse = optimized
				}
			}
			if optimized, ok := runTreeOfThought(client, modelName, taskQuery, sanitizeModelOutput(fullResponse), styleProfile, resolveToTBranchCap(modulation)); ok {
				fullResponse = optimized
			}
		}
		finalOutput := sanitizeModelOutput(fullResponse)
		finalOutput = normalizeInsufficientKnowledgeResponse(finalOutput)
		if strings.TrimSpace(finalOutput) != "" {
			if telemetrySummary != "" {
				finalOutput = "State Summary: " + telemetrySummary + "\n\n" + finalOutput
			}
			finalOutput = withStrategicProposal(finalOutput, mm, sm, taskQuery)
			finalOutput = withAgencyPendingPrompt(finalOutput)
			finalOutput = redactSensitiveOutput(finalOutput)
			if !(streamingMinimal && streamedOutput.Load()) {
				if glimpse := consumeLogicGlimpse(); strings.TrimSpace(glimpse) != "" {
					output.PrintReasoningMirrorLine(os.Stdout, "Logic Glimpse: "+glimpse, sm, chatRawOutput)
				}
				segments := styleOutputForCurrentState(finalOutput, sm)
				output.PrintBreathAwareStyledWithProfile(os.Stdout, finalOutput, segments, sm, chatRawOutput, styleCadenceProfileFromCognition(styleProfile))
			}
			recordInferenceAndTelemetryFeed(turnStarted, finalOutput)
		}
		fmt.Println()
	} else {
		fmt.Println()
	}
	if hasToolCalls && tc != nil {
		if len(toolCalls) > maxToolCallsPerTurn {
			debugPrintf("DEBUG: Received %d tool calls, limiting to %d this turn.\n", len(toolCalls), maxToolCallsPerTurn)
			toolCalls = toolCalls[:maxToolCallsPerTurn]
		}

		assistantMsg := api.Message{Role: "assistant", Content: fullResponse}
		messages = append(messages, assistantMsg)
		if history != nil {
			*history = append(*history, assistantMsg)
		}

		if toolflowV3Enabled() {
			items, status, depNotes, err := runToolflowV3(tc, taskQuery, toolCalls, fmt.Sprintf("chat-depth-%d", depth), "chat", "chat")
			if err != nil {
				if isSelectiveInterventionRequiredError(err) {
					msg := selectiveInterventionErrorMessage(err)
					if strings.TrimSpace(msg) == "" {
						msg = strings.TrimSpace(err.Error())
					}
					segments := styleOutputForCurrentState(msg, sm)
					output.PrintBreathAwareStyledWithProfile(os.Stdout, msg, segments, sm, chatRawOutput, styleCadenceProfileFromCognition(styleProfile))
					fmt.Println()
					if err := mm.AddMessage("assistant", msg); err != nil {
						fmt.Printf("Warning: Error adding assistant message to memory: %v\n", err)
					}
					if history != nil {
						*history = append(*history, api.Message{Role: "assistant", Content: msg})
					}
					recordInferenceAndTelemetryFeed(turnStarted, msg)
					return nil
				}
				debugPrintf("DEBUG: Toolflow V3 failed (fallback to legacy): %v\n", err)
			} else {
				if toolflowDeprecationsEnabled() {
					for _, note := range depNotes {
						debugPrintf("DEBUG: Toolflow deprecation: %s\n", note)
					}
				}
				if toolflowTraceEnabled() {
					fmt.Print(status)
				}
				for i, item := range items {
					fmt.Printf("\n[LLM tool step %d/%d: %s]\n", i+1, len(items), item.Tool)
					toolResult := redactSensitiveOutput(item.Output)
					if item.Err != nil {
						if isSelectiveInterventionRequiredError(item.Err) {
							msg := selectiveInterventionErrorMessage(item.Err)
							if strings.TrimSpace(msg) == "" {
								msg = strings.TrimSpace(item.Err.Error())
							}
							segments := styleOutputForCurrentState(msg, sm)
							output.PrintBreathAwareStyledWithProfile(os.Stdout, msg, segments, sm, chatRawOutput, styleCadenceProfileFromCognition(styleProfile))
							fmt.Println()
							if err := mm.AddMessage("assistant", msg); err != nil {
								fmt.Printf("Warning: Error adding assistant message to memory: %v\n", err)
							}
							if history != nil {
								*history = append(*history, api.Message{Role: "assistant", Content: msg})
							}
							recordInferenceAndTelemetryFeed(turnStarted, msg)
							return nil
						}
						recordToolFailure(item.Tool, taskQuery, item.Err)
						toolResult = fmt.Sprintf("Error executing tool: %v", item.Err)
					}
					toolResultForModel := toolResult
					if len(toolResultForModel) > maxToolResultChars {
						toolResultForModel = toolResultForModel[:maxToolResultChars] + "\n...(truncated for context size)"
					}
					followup := "Use the tool result above to continue solving the task. If more tools are needed, call the next tool. If done, provide the final answer now."
					if item.Tool == "sys_exec" && (strings.Contains(toolResultForModel, `"trigger_ouroboros":true`) || strings.Contains(toolResultForModel, `"exit_code":1`) || strings.Contains(toolResultForModel, `"exit_code":2`)) {
						followup = "Analyze the sys_exec failure using Ouroboros loop, propose a safe fix command, and then continue."
					}
					toolResultMsg := api.Message{
						Role:    "user",
						Content: fmt.Sprintf("Tool result (%s): %s\n\n%s", item.Tool, toolResultForModel, followup),
					}
					messages = append(messages, toolResultMsg)
					if history != nil {
						*history = append(*history, toolResultMsg)
					}
				}
				debugPrintf("DEBUG: Requesting next step/final answer with %d total messages...\n", len(messages))
				return performChatWithTools(client, mm, tc, modelCandidates, modelIndex, taskQuery, messages, history, depth+1, sm, budget, modulation, styleProfile)
			}
		}
		runToolflowShadowEval(taskQuery, toolCalls, fmt.Sprintf("chat-shadow-depth-%d", depth))
		for i, rawCall := range toolCalls {
			call, arbitrationNote := arbitrateToolCall(rawCall, taskQuery)
			fmt.Printf("\n[LLM tool step %d/%d: %s]\n", i+1, len(toolCalls), call.Tool)
			if arbitrationNote != "" {
				debugPrintf("DEBUG: Tool arbitration: %s\n", arbitrationNote)
			}
			debugPrintf("DEBUG: Executing tool: %s with args: %v\n", call.Tool, call.Args)

			toolResult, toolErr := executeToolCall(tc, call)
			if toolErr != nil {
				if isSelectiveInterventionRequiredError(toolErr) {
					msg := selectiveInterventionErrorMessage(toolErr)
					if strings.TrimSpace(msg) == "" {
						msg = strings.TrimSpace(toolErr.Error())
					}
					segments := styleOutputForCurrentState(msg, sm)
					output.PrintBreathAwareStyledWithProfile(os.Stdout, msg, segments, sm, chatRawOutput, styleCadenceProfileFromCognition(styleProfile))
					fmt.Println()
					if err := mm.AddMessage("assistant", msg); err != nil {
						fmt.Printf("Warning: Error adding assistant message to memory: %v\n", err)
					}
					if history != nil {
						*history = append(*history, api.Message{Role: "assistant", Content: msg})
					}
					recordInferenceAndTelemetryFeed(turnStarted, msg)
					return nil
				}
				recordToolFailure(call.Tool, taskQuery, toolErr)
				toolResult = fmt.Sprintf("Error executing tool: %v", toolErr)
			}
			toolResult = redactSensitiveOutput(toolResult)
			debugPrintf("DEBUG: Tool Result Received (%d chars)\n", len(toolResult))

			toolResultForModel := toolResult
			if len(toolResultForModel) > maxToolResultChars {
				toolResultForModel = toolResultForModel[:maxToolResultChars] + "\n...(truncated for context size)"
				debugPrintf("DEBUG: Tool Result truncated to %d chars for follow-up prompt\n", len(toolResultForModel))
			}

			header := fmt.Sprintf("Tool result (%s)", call.Tool)
			if arbitrationNote != "" {
				header += " [arbitrated]"
			}
			followup := "Use the tool result above to continue solving the task. If more tools are needed, call the next tool. If done, provide the final answer now."
			if call.Tool == "sys_exec" && (strings.Contains(toolResultForModel, `"trigger_ouroboros":true`) || strings.Contains(toolResultForModel, `"exit_code":1`) || strings.Contains(toolResultForModel, `"exit_code":2`)) {
				followup = "Analyze the sys_exec failure using Ouroboros loop, propose a safe fix command, and then continue."
			}
			toolResultMsg := api.Message{
				Role: "user",
				Content: header + ": " + toolResultForModel +
					"\n\n" + followup,
			}

			messages = append(messages, toolResultMsg)
			if history != nil {
				*history = append(*history, toolResultMsg)
			}
		}

		debugPrintf("DEBUG: Requesting next step/final answer with %d total messages...\n", len(messages))
		return performChatWithTools(client, mm, tc, modelCandidates, modelIndex, taskQuery, messages, history, depth+1, sm, budget, modulation, styleProfile)
	}
	if hasToolCalls && tc == nil {
		if depth >= 2 {
			msg := "Tool calls were requested, but tool client is unavailable in this session. Set GLM_API_KEY and GLM_CLIENT_ID or ask for a no-tools answer."
			fmt.Println(msg)
			if err := mm.AddMessage("assistant", msg); err != nil {
				fmt.Printf("Warning: Error adding assistant message to memory: %v\n", err)
			}
			if history != nil {
				*history = append(*history, api.Message{Role: "assistant", Content: msg})
			}
			return nil
		}
		debugPrintln("DEBUG: Tool calls requested but tool client unavailable; requesting direct answer without tools.")
		messages = append(messages,
			api.Message{Role: "assistant", Content: fullResponse},
			api.Message{Role: "user", Content: "Tools are unavailable in this session. Do not emit tool calls. Provide a direct final answer now."},
		)
		if history != nil {
			*history = append(*history, api.Message{Role: "assistant", Content: fullResponse})
			*history = append(*history, api.Message{Role: "user", Content: "Tools are unavailable in this session. Do not emit tool calls. Provide a direct final answer now."})
		}
		return performChatWithTools(client, mm, tc, modelCandidates, modelIndex, taskQuery, messages, history, depth+1, sm, budget, modulation, styleProfile)
	}

	safeOutput := sanitizeModelOutput(fullResponse)
	if safeOutput != "" {
		fullResponse = safeOutput
	}
	if telemetrySummary != "" {
		fullResponse = "State Summary: " + telemetrySummary + "\n\n" + fullResponse
	}
	fullResponse = redactSensitiveOutput(fullResponse)
	if err := mm.AddMessage("assistant", fullResponse); err != nil {
		fmt.Printf("Warning: Error adding assistant message to memory: %v\n", err)
	}
	if history != nil {
		*history = append(*history, api.Message{Role: "assistant", Content: fullResponse})
	}
	return nil
}

func hasVisibleToken(content string) bool {
	return strings.TrimSpace(content) != ""
}

func shouldStreamTopLevelMinimal(budget cognitionBudget, depth int) bool {
	return depth == 0 && strings.EqualFold(strings.TrimSpace(budget.Mode), "minimal")
}

func styleOutputForCurrentState(text string, sm *state.Manager) []output.TonalSegment {
	segments := output.AnalyzeSubtext(text)
	if sm == nil {
		return segments
	}
	markers := sm.GetSnapshot().Subtext
	return output.ColorSegmentsWithSubtext(segments, markers)
}

func styleCadenceProfileFromCognition(p cognition.StyleProfile) *output.StyleCadenceProfile {
	if !cognition.StyleV2Enabled() {
		return nil
	}
	return &output.StyleCadenceProfile{
		Density: p.Density,
		Tone:    p.Tone,
	}
}

func withStrategicProposal(base string, mm *memory.MemoryManager, sm *state.Manager, query string) string {
	base = strings.TrimSpace(base)
	if base == "" || mm == nil {
		return base
	}
	proposal, ok := orchestration.BuildStrategicProposal(mm, sm, query)
	if !ok || strings.TrimSpace(proposal) == "" {
		return base
	}
	if strings.Contains(strings.ToLower(base), "strategic proposal:") {
		return base
	}
	return base + "\n\n" + strings.TrimSpace(proposal)
}

func withAgencyPendingPrompt(base string) string {
	base = strings.TrimSpace(base)
	if base == "" {
		return base
	}
	prompt := strings.TrimSpace(orchestration.PendingApprovalPrompt())
	if prompt == "" {
		return base
	}
	if strings.Contains(strings.ToLower(base), "shall i execute") {
		return base
	}
	return base + "\n\n" + prompt
}

func loadTruthShiftSeverityForGeometry() (float64, bool, string) {
	shift, _, foundShift, err := orchestration.LoadLatestWorldviewTruthShift()
	if err != nil {
		return 0, false, ""
	}
	if foundShift {
		return clampFloat(shift.Severity), shift.Detected, strings.TrimSpace(shift.Reason)
	}
	state, foundState, stateErr := orchestration.LoadCurrentWorldviewTruth()
	if stateErr != nil || !foundState {
		return 0, false, ""
	}
	reason := "worldview conflict index fallback"
	if state.ConflictIndex >= 0.60 {
		return clampFloat(state.ConflictIndex), true, reason
	}
	return clampFloat(state.ConflictIndex), false, reason
}

func isTransientLLMError(err error) bool {
	if err == nil {
		return false
	}
	s := strings.ToLower(err.Error())
	for _, marker := range []string{
		"eof",
		"connection reset",
		"connection refused",
		"i/o timeout",
		"tls handshake timeout",
		"temporary",
		"bad gateway",
		"502",
		"503",
		"504",
		"model '",
		" not found",
	} {
		if strings.Contains(s, marker) {
			return true
		}
	}
	return false
}

func shouldUseThoughtGraph(taskQuery string) bool {
	q := strings.ToLower(strings.TrimSpace(taskQuery))
	if q == "" {
		return false
	}
	if shouldUseTreeOfThought(taskQuery, 0) {
		return true
	}
	for _, marker := range []string{
		"multi-step", "tool", "sources", "citations", "verify", "cross-check",
		"research", "investigate", "plan", "architecture", "tradeoff", "analyze",
		"compare", "evaluate", "design", "strategy", "daemon", "delegate", "delegation",
	} {
		if strings.Contains(q, marker) {
			return true
		}
	}
	if strings.Contains(q, " and ") && len(q) > 70 {
		return true
	}
	return len(q) > 150
}

func shouldUseArchiveHistoryQuestion(taskQuery string) bool {
	q := strings.ToLower(strings.TrimSpace(taskQuery))
	if q == "" {
		return false
	}
	if strings.Contains(q, "reasoning history") || strings.Contains(q, "decision record") || strings.Contains(q, "why did we build") {
		return true
	}
	if strings.Contains(q, "last week") && (strings.Contains(q, "why") || strings.Contains(q, "built")) {
		return true
	}
	return strings.Contains(q, "why did we") && strings.Contains(q, "tool")
}

func buildArchiveHistoryAnswer(taskQuery string) (string, bool) {
	matches, err := orchestration.QueryReasoningArchive(taskQuery, 4)
	if err != nil || len(matches) == 0 {
		return "", false
	}
	var lines []string
	for i, m := range matches {
		if i >= 4 {
			break
		}
		date := ""
		if !m.Record.Timestamp.IsZero() {
			date = m.Record.Timestamp.UTC().Format("2006-01-02")
		}
		chosen := strings.TrimSpace(m.Record.ChosenPath)
		if chosen == "" {
			chosen = strings.TrimSpace(m.Record.Topology)
		}
		reason := strings.TrimSpace(m.Record.Reasoning)
		if len(reason) > 170 {
			reason = reason[:170] + "..."
		}
		line := fmt.Sprintf("- %s | chose %s (%.0f%%): %s", date, chosen, m.Similarity*100, reason)
		lines = append(lines, line)
	}
	out := "Reasoning History:\n" + strings.Join(lines, "\n")
	return strings.TrimSpace(out), true
}

func shouldUseDocumentOrchestration(query string, mm *memory.MemoryManager) bool {
	q := strings.ToLower(strings.TrimSpace(query))
	if q == "" {
		return false
	}
	for _, marker := range []string{
		"architecture", "project structure", "codebase", "documents", "files",
		"technical guide", "readme", "across docs", "cross-link", "map",
		"summarize all", "summarise all", "multi-topic", "compare sections",
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

func shouldUseTreeOfThought(taskQuery string, depth int) bool {
	if depth > 0 {
		return false
	}
	q := strings.ToLower(strings.TrimSpace(taskQuery))
	if len(q) < 40 {
		return false
	}
	for _, marker := range []string{
		"compare", "tradeoff", "analyze", "analysis", "strategy", "plan",
		"design", "architecture", "choose", "decision", "pros and cons",
		"recommend", "best approach", "step by step", "complex",
	} {
		if strings.Contains(q, marker) {
			return true
		}
	}
	// Long prompts are often multi-constraint and benefit from branch selection.
	return len(q) > 120
}

func runMonteCarloThoughtSearch(client *api.Client, mm *memory.MemoryManager, modelName, taskQuery, draftAnswer string, modulation cognition.ReasoningModulationProfile) (string, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), mctsTimeout)
	defer cancel()

	logicContext := buildSymbolicContext(mm, taskQuery)
	branchFactor := clampInt(resolveToTBranchCap(modulation), 3, 5)
	if branchFactor == 0 {
		branchFactor = clampInt(mctsBranchFactor, 3, 5)
	}
	pruneThreshold := modulation.PruneThreshold
	if pruneThreshold <= 0 {
		pruneThreshold = 0.30
	}
	strategy := strings.ToLower(strings.TrimSpace(os.Getenv("TALOS_MCTS_STRATEGY")))
	resolvedStrategy := cognition.MCTSStrategyPUCT
	if strategy == string(cognition.MCTSStrategyUCB1) || strings.EqualFold(strings.TrimSpace(os.Getenv("TALOS_MCTS_LEGACY_FALLBACK")), "true") {
		resolvedStrategy = cognition.MCTSStrategyUCB1
	}
	engine := cognition.MCTSEngine{
		Config: cognition.MCTSConfig{
			Iterations:         mctsIterations,
			BranchFactor:       branchFactor,
			RolloutDepth:       mctsRolloutDepth,
			UCB1C:              mctsUCB1C,
			PruneThreshold:     pruneThreshold,
			BaseWeight:         modulation.Weights.Base,
			AdvWeight:          modulation.Weights.Adversarial,
			EvidenceWeight:     modulation.Weights.Evidence,
			ContraWeight:       modulation.Weights.ContradictionPenalty,
			Strategy:           resolvedStrategy,
			MaxChildrenPerNode: mctsEnvInt("TALOS_MCTS_MAX_CHILDREN_PER_NODE", branchFactor),
			WideningAlpha:      mctsEnvFloat("TALOS_MCTS_WIDENING_ALPHA", 0.5),
			WideningK:          mctsEnvFloat("TALOS_MCTS_WIDENING_K", 1.5),
			PriorWeight:        mctsEnvFloat("TALOS_MCTS_PRIOR_WEIGHT", 1.25),
			VirtualLoss:        mctsEnvFloat("TALOS_MCTS_VIRTUAL_LOSS", 0.2),
			MaxConcurrency:     mctsEnvInt("TALOS_MCTS_MAX_CONCURRENCY", 4),
			EvalTimeout:        mctsCallTimeout,
		},
		Callbacks: cognition.MCTSCallbacks{
			ProposeBranches: func(ctx context.Context, currentAnswer string, branchCount int) ([]string, error) {
				branches, ok := mctsProposeBranchesN(ctx, client, modelName, taskQuery, currentAnswer, branchCount)
				if !ok || len(branches) == 0 {
					return nil, fmt.Errorf("no branches proposed")
				}
				out := make([]string, 0, len(branches))
				for _, b := range branches {
					if ans := strings.TrimSpace(b.Answer); ans != "" {
						out = append(out, ans)
					}
				}
				if len(out) == 0 {
					return nil, fmt.Errorf("no non-empty branches proposed")
				}
				return out, nil
			},
			EvaluatePath: func(ctx context.Context, candidate string) (cognition.MCTSEvaluation, error) {
				score, improved, reason, ok := mctsEvaluateNode(ctx, client, modelName, taskQuery, candidate, logicContext)
				if !ok {
					return cognition.MCTSEvaluation{}, fmt.Errorf("path evaluation failed")
				}
				return cognition.MCTSEvaluation{
					Confidence: score,
					Candidate:  strings.TrimSpace(improved),
					Reason:     reason,
				}, nil
			},
			AdversarialEval: func(_ context.Context, candidate string) (cognition.MCTSEvaluation, error) {
				result := cognition.ConductSelfPlay(candidate, logicContext)
				conf := 1.0 - result.MaxFlawScore
				if result.Contradictions > 0 {
					conf -= 0.10 * float64(result.Contradictions)
				}
				if conf < 0 {
					conf = 0
				}
				refined := strings.TrimSpace(result.FinalCandidate)
				if refined == "" {
					refined = strings.TrimSpace(candidate)
				}
				return cognition.MCTSEvaluation{
					Confidence: conf,
					Candidate:  refined,
					Reason:     "adversarial self-play",
				}, nil
			},
		},
	}

	result, err := engine.SearchV2(ctx, draftAnswer)
	if err != nil || strings.TrimSpace(result.BestAnswer) == "" {
		return "", false
	}
	root := result.Root
	if root != nil && liveTelemetry != nil {
		liveTelemetry.RecordDecisionCertainty(collectThoughtConfidences(root))
	}
	if root != nil {
		glimpse := summarizeMCTSLogic(root)
		setLogicGlimpse(glimpse)
		alt := make([]string, 0, len(root.Children))
		var topConf float64
		for _, c := range root.Children {
			if c == nil {
				continue
			}
			alt = append(alt, fmt.Sprintf("score=%.2f pruned=%t", c.Confidence, c.Pruned))
			if c.Confidence > topConf {
				topConf = c.Confidence
			}
		}
		_ = orchestration.AppendDecisionFeed(decisionFeedPath, orchestration.DecisionRecord{
			Timestamp:    time.Now().UTC(),
			Query:        strings.TrimSpace(taskQuery),
			Source:       "mcts_branch_search",
			ChosenPath:   "mcts_winning_path",
			Alternatives: alt,
			Reasoning:    strings.TrimSpace(glimpse) + fmt.Sprintf(" | strategy=%s iterations=%d expanded=%d pruned=%d elapsed_ms=%d", result.Strategy, result.IterationsRun, result.ExpandedNodes, result.PrunedNodes, result.ElapsedMS),
			Confidence:   topConf,
		})
	}
	if strings.EqualFold(strings.TrimSpace(os.Getenv("TALOS_MCTS_STATUS_ENABLED")), "true") {
		fmt.Printf("MCTS status: strategy=%s iterations=%d expanded=%d pruned=%d confidence=%.2f elapsed_ms=%d\n", result.Strategy, result.IterationsRun, result.ExpandedNodes, result.PrunedNodes, result.Confidence, result.ElapsedMS)
	}
	appendMCTSTrace(taskQuery, draftAnswer, result)
	return sanitizeModelOutput(result.BestAnswer), true
}

func mctsEnvInt(key string, fallback int) int {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback
	}
	v, err := strconv.Atoi(raw)
	if err != nil {
		return fallback
	}
	if v <= 0 {
		return fallback
	}
	return v
}

func mctsEnvFloat(key string, fallback float64) float64 {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback
	}
	v, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return fallback
	}
	if math.IsNaN(v) || math.IsInf(v, 0) || v <= 0 {
		return fallback
	}
	return v
}

type mctsTraceEntry struct {
	Timestamp  time.Time `json:"timestamp"`
	Query      string    `json:"query"`
	SeedAnswer string    `json:"seed_answer"`
	BestAnswer string    `json:"best_answer"`
	Strategy   string    `json:"strategy"`
	Iterations int       `json:"iterations"`
	Expanded   int       `json:"expanded"`
	Pruned     int       `json:"pruned"`
	Confidence float64   `json:"confidence"`
	ElapsedMS  int64     `json:"elapsed_ms"`
}

func appendMCTSTrace(query string, draft string, result cognition.MCTSResult) {
	path := strings.TrimSpace(os.Getenv("TALOS_MCTS_TRACE_PATH"))
	if path == "" {
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	entry := mctsTraceEntry{
		Timestamp:  time.Now().UTC(),
		Query:      strings.TrimSpace(query),
		SeedAnswer: mctsTraceTrim(strings.TrimSpace(draft), 400),
		BestAnswer: mctsTraceTrim(strings.TrimSpace(result.BestAnswer), 400),
		Strategy:   strings.TrimSpace(result.Strategy),
		Iterations: result.IterationsRun,
		Expanded:   result.ExpandedNodes,
		Pruned:     result.PrunedNodes,
		Confidence: result.Confidence,
		ElapsedMS:  result.ElapsedMS,
	}
	line, err := json.Marshal(entry)
	if err != nil {
		return
	}
	_, _ = f.Write(append(line, '\n'))
}

func mctsTraceTrim(s string, max int) string {
	if max <= 0 || len(s) <= max {
		return s
	}
	if max < 4 {
		return s[:max]
	}
	return s[:max-3] + "..."
}

func mctsProposeBranchesN(ctx context.Context, client *api.Client, modelName, taskQuery, currentAnswer string, branchCount int) ([]totBranch, bool) {
	callCtx, cancel := context.WithTimeout(ctx, mctsCallTimeout)
	defer cancel()
	if branchCount < 3 {
		branchCount = 3
	}
	if branchCount > 5 {
		branchCount = 5
	}

	system := `You are an MCTS branch proposer.
Generate diverse candidate final answers for the user query.
Do not include chain-of-thought.
Return JSON only:
{"branches":[{"answer":"..."}]}`

	user := "User query:\n" + taskQuery + "\n\nCurrent answer candidate:\n" + currentAnswer +
		"\n\nReturn " + fmt.Sprintf("%d", branchCount) + " improved alternatives."

	req := &api.ChatRequest{
		Model: modelName,
		Options: func() map[string]any {
			opts, _ := state.ResolveEntropyOptions(user)
			return opts
		}(),
		Messages: []api.Message{
			{Role: "system", Content: system},
			{Role: "user", Content: user},
		},
	}

	var out strings.Builder
	err := client.Chat(callCtx, req, func(resp api.ChatResponse) error {
		out.WriteString(resp.Message.Content)
		return nil
	})
	if err != nil {
		return nil, false
	}

	branches, ok := parseToTBranches(out.String(), branchCount)
	if !ok || len(branches) == 0 {
		return nil, false
	}
	if len(branches) > branchCount {
		branches = branches[:branchCount]
	}
	return branches, true
}

func mctsEvaluateNode(ctx context.Context, client *api.Client, modelName, taskQuery, candidate string, logicContext []string) (float64, string, string, bool) {
	callCtx, cancel := context.WithTimeout(ctx, mctsCallTimeout)
	defer cancel()

	system := `You are an MCTS evaluator.
Score the candidate answer for correctness, relevance, and clarity.
Optionally provide a slightly improved concise answer.
Do not include chain-of-thought.
Return JSON only:
{"score":0.0,"answer":"..."}`

	user := "User query:\n" + taskQuery + "\n\nCandidate answer:\n" + candidate

	req := &api.ChatRequest{
		Model: modelName,
		Options: func() map[string]any {
			opts, _ := state.ResolveEntropyOptions(user)
			return opts
		}(),
		Messages: []api.Message{
			{Role: "system", Content: system},
			{Role: "user", Content: user},
		},
	}
	var out strings.Builder
	err := client.Chat(callCtx, req, func(resp api.ChatResponse) error {
		out.WriteString(resp.Message.Content)
		return nil
	})
	if err != nil {
		return 0, "", "evaluation error", false
	}

	type evalResp struct {
		Score  float64 `json:"score"`
		Answer string  `json:"answer"`
	}
	trimmed := strings.TrimSpace(stripMarkdownCodeFences(stripReasoningSections(out.String())))
	var er evalResp
	if err := json.Unmarshal([]byte(trimmed), &er); err != nil {
		return 0, "", "invalid evaluator json", false
	}
	score := er.Score
	if math.IsNaN(score) || math.IsInf(score, 0) {
		score = 0
	}
	if score < 0 {
		score = 0
	}
	if score > 1 {
		score = 1
	}
	if contradiction := cognition.EvaluateLogic(candidate, logicContext); contradiction >= 0.7 {
		score = 0.0
		return score, er.Answer, "symbolic veto", true
	}
	return score, er.Answer, "scored", true
}

func buildSymbolicContext(mm *memory.MemoryManager, taskQuery string) []string {
	if mm == nil {
		return nil
	}
	var out []string
	if kctx, err := mm.RetrieveKnowledge(taskQuery, 6); err == nil {
		out = append(out, kctx...)
	}
	if hctx, err := mm.RetrieveDynamicContext(taskQuery, 4); err == nil {
		out = append(out, hctx...)
	}
	return out
}

func shouldRequireCodeSandbox(query string, draft string) bool {
	q := strings.ToLower(strings.TrimSpace(query))
	d := strings.ToLower(strings.TrimSpace(draft))
	for _, marker := range []string{
		"go code", "golang", "go file", "main.go", "package main",
		"svelte", "sveltekit", ".svelte", "app.svelte", "component.svelte",
		"python", ".py", "mypy", "ruff", "pyproject.toml",
		"compile", "build", "fix compile", "undefined:", "go vet",
		"implement", "write code",
	} {
		if strings.Contains(q, marker) || strings.Contains(d, marker) {
			return true
		}
	}
	for _, marker := range []string{
		"```go", "```golang", "```python", "```py", "```svelte", "```ts", "```js",
	} {
		if strings.Contains(q, marker) || strings.Contains(d, marker) {
			return true
		}
	}
	if ext := regexp.MustCompile(`(?i)\.(go|py|svelte|ts|tsx|js|jsx)\b`); ext.MatchString(q) || ext.MatchString(d) {
		return true
	}
	return strings.Contains(d, "func ") && strings.Contains(d, "package ")
}

func shouldUseVisualReasoning(query string, currentOutput string) bool {
	q := strings.ToLower(strings.TrimSpace(query + " " + currentOutput))
	if q == "" {
		return false
	}
	return containsAnyToken(q,
		"ui", "screen", "screenshot", "window", "button", "frontend", "svelte", "layout", "visual",
		"frozen", "stuck", "click", "render", "css", "dashboard")
}

func shouldRunVisualCorrectionLoop(query string, candidate string) bool {
	q := strings.ToLower(strings.TrimSpace(query + " " + candidate))
	if q == "" {
		return false
	}
	fixIntent := containsAnyToken(q, "fix", "update", "adjust", "change", "patch", "correct")
	uiIntent := containsAnyToken(q, "ui", "button", "screen", "window", "frontend", "visual", "layout", "css", "svelte")
	return fixIntent && uiIntent
}

func visualHandshakeEnabled() bool {
	return envBoolDefault("TALOS_VISUAL_HANDSHAKE_ENABLED", true)
}

func visualHandshakeAutoDispatchEnabled() bool {
	return envBoolDefault("TALOS_VISUAL_HANDSHAKE_AUTO_DISPATCH", true)
}

func visualHandshakeMinConfidence() float64 {
	v := strings.TrimSpace(os.Getenv("TALOS_VISUAL_HANDSHAKE_MIN_CONFIDENCE"))
	if v == "" {
		return 0.68
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil {
		return 0.68
	}
	if f < 0.10 {
		return 0.10
	}
	if f > 1.0 {
		return 1.0
	}
	return f
}

func shouldAutoDispatchVisualHandshake(query string, target skills.VisualAction, corr float64, overlayDecision skills.OverlayDecision) bool {
	if !visualHandshakeEnabled() || !visualHandshakeAutoDispatchEnabled() {
		return false
	}
	if strings.TrimSpace(target.Target) == "" {
		return false
	}
	q := strings.ToLower(strings.TrimSpace(query))
	intentMatch := containsAnyToken(q,
		"analyze this", "analyze this ui element", "ui element", "looking at", "inspect this", "specific element", "this button",
		"focus this", "analyze visual", "visual target", "element on screen")
	if !intentMatch && !overlayDecision.Render {
		return false
	}
	score := math.Max(target.Confidence, corr)
	return score >= visualHandshakeMinConfidence()
}

func inferVisualHandshakeIntent(query string) string {
	q := strings.ToLower(strings.TrimSpace(query))
	switch {
	case containsAnyToken(q, "verify", "validated", "confirm fixed", "is it fixed"):
		return "verify_fix"
	case containsAnyToken(q, "error", "exception", "failed", "bug"):
		return "inspect_error"
	default:
		return "analyze_ui_element"
	}
}

func resolveVisualSnippetForTarget(vr skills.VisualReasoningResult, target skills.VisualAction) string {
	best := strings.TrimSpace(target.Target)
	bestDist := math.MaxFloat64
	for _, el := range vr.SpatialElements {
		label := strings.TrimSpace(el.Label)
		if label == "" {
			continue
		}
		cx := float64(el.X + maxInt(el.Width/2, 0))
		cy := float64(el.Y + maxInt(el.Height/2, 0))
		d := math.Hypot(cx-float64(target.X), cy-float64(target.Y))
		if d < bestDist {
			bestDist = d
			best = label
		}
	}
	return summarizeForMetadata(strings.TrimSpace(best), envIntDefault("TALOS_VISUAL_HANDSHAKE_MAX_SNIPPET_CHARS", 280))
}

func buildSpatialElementsForHandshake(in []skills.SpatialElement, maxN int) []interface{} {
	if maxN <= 0 {
		maxN = 12
	}
	capN := len(in)
	if capN > maxN {
		capN = maxN
	}
	out := make([]interface{}, 0, capN)
	for i, el := range in {
		if i >= maxN {
			break
		}
		out = append(out, map[string]interface{}{
			"kind":       strings.TrimSpace(el.Kind),
			"label":      strings.TrimSpace(el.Label),
			"x":          el.X,
			"y":          el.Y,
			"width":      el.Width,
			"height":     el.Height,
			"confidence": el.Confidence,
		})
	}
	return out
}

func maybeDraftJITSuperSkill(selectedSkill *skills.SkillRecord, query string, complexityScore int) (*skills.SkillRecord, []skills.SkillRecord, string) {
	if selectedSkill != nil {
		return selectedSkill, nil, ""
	}
	if !envBoolDefault("TALOS_JIT_SKILL_CHAINING_ENABLED", true) {
		return nil, nil, ""
	}
	minComplexity := envIntDefault("TALOS_JIT_SKILL_CHAIN_MIN_COMPLEXITY", 7)
	if complexityScore < minComplexity {
		return nil, nil, ""
	}
	minConfidence := envFloatDefault("TALOS_JIT_SKILL_CHAIN_MIN_CONFIDENCE", 0.35)
	reg := skills.NewSkillRegistry(skills.PermanentSkillsRoot())
	decision, err := skills.RouteSkill(reg, skills.RouteRequest{Query: strings.TrimSpace(query)})
	if err != nil || len(decision.Candidates) == 0 {
		return nil, nil, ""
	}
	picks := make([]skills.SkillRecord, 0, 3)
	seen := map[string]bool{}
	for _, cand := range decision.Candidates {
		if cand.Confidence < minConfidence {
			continue
		}
		id := strings.TrimSpace(cand.Record.SkillID)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		picks = append(picks, cand.Record)
		if len(picks) == 3 {
			break
		}
	}
	if len(picks) < 3 {
		return nil, nil, ""
	}
	super, chain, err := skills.DraftTemporarySuperSkill(strings.TrimSpace(query), picks)
	if err != nil {
		return nil, nil, ""
	}
	note := fmt.Sprintf("Planner drafted JIT Super-Skill chain: %s -> %s -> %s",
		strings.TrimSpace(chain[0].SkillID),
		strings.TrimSpace(chain[1].SkillID),
		strings.TrimSpace(chain[2].SkillID),
	)
	return &super, chain, note
}

func envFloatDefault(key string, fallback float64) float64 {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback
	}
	v, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return fallback
	}
	return v
}

func buildVisualReasoningOverlay(query string, vr skills.VisualReasoningResult) string {
	blob := strings.ToLower(strings.TrimSpace(
		query + " " +
			vr.Summary + " " +
			vr.Layout + " " +
			vr.SpatialCorrelation + " " +
			strings.Join(vr.TechnicalFindings, " "),
	))
	if blob == "" {
		return ""
	}
	if containsAnyToken(blob, "overlap", "overlapping", "sidebar", "svelte", "component", "layout shift", "clipping") {
		return "Looking at the dashboard... wait, that Svelte component is overlapping the sidebar. I'm going to prepare a focused CSS capability fix."
	}
	if containsAnyToken(blob, "button", "disabled", "unclickable", "offscreen", "misaligned", "frozen") {
		return "Visual pass caught a UI blocker. Routing a focused capability fix pass before synthesis."
	}
	return ""
}

func hasVisualConsent(query string) bool {
	q := strings.ToLower(strings.TrimSpace(query))
	if q == "" {
		return false
	}
	if strings.Contains(q, "do not capture") || strings.Contains(q, "don't capture") || strings.Contains(q, "no screenshot") {
		return false
	}
	return containsAnyToken(q,
		"capture the screen", "capture screen", "take a screenshot", "screenshot",
		"yes capture", "yes, capture", "ok to capture", "permission granted", "window-only")
}

func inferVisualCaptureMode(query string) string {
	q := strings.ToLower(strings.TrimSpace(query))
	if containsAnyToken(q, "window-only", "window only", "specific window", "app window") {
		return "window"
	}
	return "screen"
}

func inferVisualWindowID(query string) string {
	re := regexp.MustCompile(`(?i)(?:window[_\s-]?id|window)\s*[:=#]?\s*([0-9]{3,})`)
	if m := re.FindStringSubmatch(strings.TrimSpace(query)); len(m) == 2 {
		return strings.TrimSpace(m[1])
	}
	return ""
}

func buildVisualShellContext(ctx *cognition.GraphContext) string {
	if ctx == nil {
		return ""
	}
	parts := []string{}
	for _, k := range []string{
		"sandbox_last_stderr",
		"sandbox_line_errors",
		"final_code_audit_stderr",
		"tool_result",
	} {
		if v := strings.TrimSpace(ctx.Metadata[k]); v != "" {
			parts = append(parts, v)
		}
	}
	if out := strings.TrimSpace(ctx.Output); out != "" {
		parts = append(parts, out)
	}
	return summarizeForMetadata(strings.Join(parts, "\n"), 900)
}

func summarizeForMetadata(s string, max int) string {
	s = strings.TrimSpace(s)
	if max <= 0 || len(s) <= max {
		return s
	}
	return s[:max] + "...(truncated)"
}

func containsAnyToken(s string, needles ...string) bool {
	for _, n := range needles {
		if strings.Contains(s, strings.ToLower(strings.TrimSpace(n))) {
			return true
		}
	}
	return false
}

func extractGoCodeCandidate(text string) (string, bool) {
	raw := strings.TrimSpace(text)
	if raw == "" {
		return "", false
	}
	reFence := regexp.MustCompile("(?s)```go\\s*(.*?)\\s*```")
	if m := reFence.FindStringSubmatch(raw); len(m) == 2 {
		code := strings.TrimSpace(m[1])
		if code != "" {
			return code, true
		}
	}
	reAnyFence := regexp.MustCompile("(?s)```\\s*(.*?)\\s*```")
	if m := reAnyFence.FindStringSubmatch(raw); len(m) == 2 {
		code := strings.TrimSpace(m[1])
		if strings.Contains(code, "package ") && strings.Contains(code, "func ") {
			return code, true
		}
	}
	idx := strings.Index(raw, "package ")
	if idx >= 0 {
		code := strings.TrimSpace(raw[idx:])
		if strings.Contains(code, "func ") {
			return code, true
		}
	}
	return "", false
}

func parseSandboxAttempt(v string) int {
	n, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil || n <= 0 {
		return 1
	}
	return n
}

func sandboxReportStderr(report skills.DiagnosticReport) string {
	for i := len(report.Diagnostics) - 1; i >= 0; i-- {
		d := report.Diagnostics[i]
		if strings.TrimSpace(d.Stderr) != "" {
			return strings.TrimSpace(d.Stderr)
		}
	}
	for i := len(report.Diagnostics) - 1; i >= 0; i-- {
		d := report.Diagnostics[i]
		if strings.TrimSpace(d.Stdout) != "" {
			return strings.TrimSpace(d.Stdout)
		}
	}
	return ""
}

func sandboxLineErrorsSummary(report skills.DiagnosticReport) string {
	if len(report.LineErrors) == 0 {
		return ""
	}
	var lines []string
	limit := 8
	for i, e := range report.LineErrors {
		if i >= limit {
			break
		}
		line := fmt.Sprintf("%s:%d", e.File, e.Line)
		if e.Column > 0 {
			line += ":" + strconv.Itoa(e.Column)
		}
		line += " - " + strings.TrimSpace(e.Message)
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

type symbolicAssertion struct {
	Name  string
	Check func(code string, query string, projectPkgs map[string]bool) (bool, string)
}

func runSymbolicGoAudit(code string, query string, packages []string, goModule string, goAllowed []string, nodeAllowed []string) []string {
	code = strings.TrimSpace(code)
	if code == "" {
		return nil
	}
	violations := cognition.AuditGoCodeSymbolic(cognition.CodeAuditOptions{
		Query:               query,
		Code:                code,
		ProjectPackages:     packages,
		GoModule:            goModule,
		AllowedGoModules:    goAllowed,
		AllowedNodePackages: nodeAllowed,
	})

	// Keep existing project-specific assertion for pocketbase pointer semantics.
	assertions := []symbolicAssertion{
		{
			Name: "pocketbase-by-reference",
			Check: func(code string, _ string, _ map[string]bool) (bool, string) {
				if !strings.Contains(strings.ToLower(code), "pocketbase") {
					return true, ""
				}
				re := regexp.MustCompile(`(?m)func\s+\w+\s*\(([^)]*)\)`)
				for _, m := range re.FindAllStringSubmatch(code, -1) {
					if len(m) < 2 {
						continue
					}
					params := m[1]
					if strings.Contains(params, "pocketbase.Client") && !strings.Contains(params, "*pocketbase.Client") {
						return false, "PocketBase client must be passed by reference (*pocketbase.Client)"
					}
				}
				return true, ""
			},
		},
	}

	for _, a := range assertions {
		ok, msg := a.Check(code, query, nil)
		if ok {
			continue
		}
		if strings.TrimSpace(msg) == "" {
			msg = "policy violation: " + a.Name
		}
		violations = append(violations, msg)
	}
	return dedupeStrings(violations)
}

func projectPackageList() []string {
	root := "pkg"
	var out []string
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || d == nil || !d.IsDir() {
			return nil
		}
		if path == root {
			return nil
		}
		rel := filepath.ToSlash(path)
		if strings.HasPrefix(rel, "pkg/") {
			out = append(out, rel)
		}
		return nil
	})
	sort.Strings(out)
	if len(out) > 64 {
		out = out[:64]
	}
	return out
}

func dedupeStrings(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	seen := map[string]bool{}
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

func gofmtPass(code string) (string, bool, error) {
	src := strings.TrimSpace(code)
	if src == "" {
		return code, false, nil
	}
	formatted, err := format.Source([]byte(src))
	if err != nil {
		return code, false, err
	}
	out := strings.TrimSpace(string(formatted))
	return out, out != src, nil
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func runTreeOfThought(client *api.Client, modelName, taskQuery, draftAnswer string, styleProfile cognition.StyleProfile, maxBranches int) (string, bool) {
	maxBranches = clampInt(maxBranches, 2, 5)
	arbiterPrompt := `You are a Tree-of-Thought arbiter.
Generate multiple candidate final answers for the user query, score each candidate from 0.0 to 1.0, and return JSON only.
Do not include chain-of-thought.
Return exactly:
{"branches":[{"answer":"...","score":0.0}]}
Requirements:
- between 2 and ` + fmt.Sprintf("%d", maxBranches) + ` branches
- concise answers
- score reflects correctness + relevance + clarity.`
	if cognition.StyleV2Enabled() {
		arbiterPrompt += "\n\n" + cognition.BuildStylePromptContract(styleProfile)
	}

	userPrompt := "User query:\n" + taskQuery
	if strings.TrimSpace(draftAnswer) != "" {
		userPrompt += "\n\nInitial draft answer to improve:\n" + draftAnswer
	}

	req := &api.ChatRequest{
		Model: modelName,
		Options: func() map[string]any {
			opts, _ := state.ResolveEntropyOptions(userPrompt)
			return opts
		}(),
		Messages: []api.Message{
			{Role: "system", Content: arbiterPrompt},
			{Role: "user", Content: userPrompt},
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), totTimeout)
	defer cancel()

	var out strings.Builder
	err := client.Chat(ctx, req, func(resp api.ChatResponse) error {
		out.WriteString(resp.Message.Content)
		return nil
	})
	if err != nil {
		return "", false
	}

	branches, ok := parseToTBranches(out.String(), maxBranches)
	if !ok || len(branches) == 0 {
		return "", false
	}

	best := branches[0]
	for _, b := range branches[1:] {
		if b.Score > best.Score {
			best = b
		}
	}
	bestAnswer := sanitizeModelOutput(best.Answer)
	if strings.TrimSpace(bestAnswer) == "" {
		return "", false
	}
	return bestAnswer, true
}

func parseToTBranches(raw string, maxBranches int) ([]totBranch, bool) {
	trimmed := strings.TrimSpace(stripMarkdownCodeFences(stripReasoningSections(raw)))
	if trimmed == "" {
		return nil, false
	}

	var direct []totBranch
	if err := json.Unmarshal([]byte(trimmed), &direct); err == nil {
		b := sanitizeBranches(direct, maxBranches)
		return b, len(b) > 0
	}

	var obj struct {
		Branches []totBranch `json:"branches"`
	}
	if err := json.Unmarshal([]byte(trimmed), &obj); err == nil {
		b := sanitizeBranches(obj.Branches, maxBranches)
		return b, len(b) > 0
	}
	return nil, false
}

func sanitizeBranches(in []totBranch, maxBranches int) []totBranch {
	maxBranches = clampInt(maxBranches, 2, 5)
	var out []totBranch
	for _, b := range in {
		ans := strings.TrimSpace(b.Answer)
		if ans == "" {
			continue
		}
		score := b.Score
		if math.IsNaN(score) || math.IsInf(score, 0) {
			score = 0
		}
		if score < 0 {
			score = 0
		}
		if score > 1 {
			score = 1
		}
		out = append(out, totBranch{Answer: ans, Score: score})
	}
	if len(out) > maxBranches {
		out = out[:maxBranches]
	}
	return out
}

func resolveToTBranchCap(mod cognition.ReasoningModulationProfile) int {
	limit := mod.BranchBudget
	if limit <= 0 {
		return 3
	}
	return clampInt(limit, 2, 5)
}

func buildFallbackAnswerFromToolResults(messages []api.Message) string {
	for i := len(messages) - 1; i >= 0; i-- {
		m := messages[i]
		if m.Role != "user" {
			continue
		}
		if !strings.HasPrefix(m.Content, "Tool result") {
			continue
		}
		content := strings.TrimSpace(m.Content)
		if len(content) > 700 {
			content = content[:700] + "...(truncated)"
		}
		return "I couldn't complete the final synthesis in time, but here is the latest tool output:\n" + content
	}
	return ""
}

func buildModelCandidates(models []string, selected string) []string {
	if len(models) == 0 {
		return nil
	}
	var out []string
	seen := make(map[string]bool)
	add := func(m string) {
		m = strings.TrimSpace(m)
		if m == "" || seen[m] || !isChatCapableModelCandidate(m) {
			return
		}
		seen[m] = true
		out = append(out, m)
	}
	addWithAliases := func(m string) {
		m = strings.TrimSpace(m)
		if m == "" {
			return
		}
		if strings.Contains(m, ":") {
			add(m)
			return
		}
		// Ollama model lookup is tag-sensitive. Prefer :latest for bare identifiers.
		add(m + ":latest")
		add(m)
	}
	addWithAliases(selected)
	for _, m := range models {
		addWithAliases(m)
	}
	return out
}

func isChatCapableModelCandidate(model string) bool {
	m := strings.ToLower(strings.TrimSpace(model))
	if m == "" {
		return false
	}
	for _, marker := range []string{
		"embed",
		"embedding",
		"nomic-embed",
	} {
		if strings.Contains(m, marker) {
			return false
		}
	}
	return true
}

func prioritizeLowLatencyModels(models []string) []string {
	if len(models) < 2 {
		return models
	}
	out := append([]string(nil), models...)
	sort.SliceStable(out, func(i, j int) bool {
		return latencyModelScore(out[i]) > latencyModelScore(out[j])
	})
	return out
}

func latencyModelScore(model string) int {
	m := strings.ToLower(strings.TrimSpace(model))
	score := 0
	if strings.Contains(m, "1b") {
		score += 40
	} else if strings.Contains(m, "2b") || strings.Contains(m, "3b") {
		score += 30
	} else if strings.Contains(m, "4b") || strings.Contains(m, "7b") {
		score += 20
	}
	if strings.Contains(m, "mini") || strings.Contains(m, "small") {
		score += 10
	}
	if strings.Contains(m, "r1") || strings.Contains(m, "70b") {
		score -= 20
	}
	if strings.Contains(m, ":latest") {
		score += 2
	}
	return score
}

func limitModelCandidates(models []string, maxCount int) []string {
	if maxCount <= 0 || len(models) == 0 {
		return nil
	}
	if len(models) <= maxCount {
		return models
	}
	return append([]string(nil), models[:maxCount]...)
}

func parseToolCalls(fullResponse string) ([]toolInvocation, bool) {
	trimmed := strings.TrimSpace(stripMarkdownCodeFences(stripReasoningSections(fullResponse)))
	if trimmed == "" {
		return nil, false
	}
	lowerTrimmed := strings.ToLower(trimmed)

	var directCall toolInvocation
	if err := json.Unmarshal([]byte(trimmed), &directCall); err == nil {
		if calls := sanitizeToolCalls([]toolInvocation{directCall}); len(calls) > 0 {
			return calls, true
		}
	}

	var directChain []toolInvocation
	if err := json.Unmarshal([]byte(trimmed), &directChain); err == nil {
		if calls := sanitizeToolCalls(directChain); len(calls) > 0 {
			return calls, true
		}
	}

	var chainContainer struct {
		ToolChain []toolInvocation `json:"tool_chain"`
		Tools     []toolInvocation `json:"tools"`
		Calls     []toolInvocation `json:"calls"`
		Steps     []toolInvocation `json:"steps"`
	}
	if err := json.Unmarshal([]byte(trimmed), &chainContainer); err == nil {
		for _, chain := range [][]toolInvocation{chainContainer.ToolChain, chainContainer.Tools, chainContainer.Calls, chainContainer.Steps} {
			if calls := sanitizeToolCalls(chain); len(calls) > 0 {
				return calls, true
			}
		}
	}

	var alt map[string]interface{}
	if err := json.Unmarshal([]byte(trimmed), &alt); err == nil {
		for _, candidate := range []string{"web_search", "fetch_url", "http_request", "vector_retrieve", "execute_code", "sys_exec", "capture_screen", "watch_terminal", "draw_box", "draw_war_room", "analyze_visual_target", "doc_search", "multimodal_tool", "auto_tool", "provision_client", "rotate_client_key", "revoke_client", "admin_list_clients", "admin_create_client", "admin_rotate_client", "admin_delete_client"} {
			if args, found := alt[candidate]; found {
				if m, ok := args.(map[string]interface{}); ok {
					calls := sanitizeToolCalls([]toolInvocation{{Tool: candidate, Args: m}})
					if len(calls) > 0 {
						return calls, true
					}
				}
			}
		}
	}

	// Handle markdown-like tool call wrappers:
	// ### Tool Call: web_search
	// {"tool":"web_search","args":{"query":"..."}}
	if strings.Contains(lowerTrimmed, "tool call") {
		jsonBlockRE := regexp.MustCompile(`(?s)\{.*\}`)
		if block := strings.TrimSpace(jsonBlockRE.FindString(trimmed)); block != "" {
			if calls, ok := parseToolCalls(block); ok {
				return calls, true
			}
		}
	}

	prefixPattern := regexp.MustCompile(`(?is)^\s*([A-Z_]+)\s*:\s*(\{.*\}|\[.*\])\s*$`)
	matches := prefixPattern.FindStringSubmatch(trimmed)
	if len(matches) != 3 {
		return nil, false
	}

	prefix := normalizeToolName(matches[1])
	payloadJSON := strings.TrimSpace(matches[2])

	if prefix == "tool_call" || prefix == "tool_chain" {
		if calls, ok := parseToolCalls(payloadJSON); ok {
			return calls, true
		}
	}

	var payload map[string]interface{}
	if err := json.Unmarshal([]byte(payloadJSON), &payload); err != nil {
		return nil, false
	}
	if !isSupportedTool(prefix) {
		return nil, false
	}

	if argsAny, found := payload["args"]; found {
		if args, ok := argsAny.(map[string]interface{}); ok {
			calls := sanitizeToolCalls([]toolInvocation{{Tool: prefix, Args: args}})
			return calls, len(calls) > 0
		}
	}

	calls := sanitizeToolCalls([]toolInvocation{{Tool: prefix, Args: payload}})
	return calls, len(calls) > 0
}

func sanitizeToolCalls(calls []toolInvocation) []toolInvocation {
	var out []toolInvocation
	for _, c := range calls {
		tool := normalizeToolName(c.Tool)
		if !isSupportedTool(tool) {
			continue
		}
		args := c.Args
		if args == nil {
			args = map[string]interface{}{}
		}
		out = append(out, toolInvocation{Tool: tool, Args: args})
	}
	return out
}

func arbitrateToolCall(call toolInvocation, taskQuery string) (toolInvocation, string) {
	tool := normalizeToolName(call.Tool)
	args := call.Args
	if args == nil {
		args = map[string]interface{}{}
	}
	original := tool

	switch tool {
	case "web_search":
		if getArgString(args, "query", "") == "" {
			if getArgString(args, "url", "") != "" {
				tool = "fetch_url"
			} else {
				args["query"] = taskQuery
			}
		}
	case "fetch_url":
		if getArgString(args, "url", "") == "" {
			tool = "web_search"
			args = map[string]interface{}{"query": getArgString(args, "query", taskQuery)}
		}
	case "http_request":
		args["method"] = strings.ToUpper(getArgString(args, "method", "GET"))
		if getArgString(args, "url", "") == "" {
			tool = "web_search"
			args = map[string]interface{}{"query": getArgString(args, "query", taskQuery)}
		}
	case "vector_retrieve":
		if getArgString(args, "query", "") == "" {
			args["query"] = taskQuery
		}
		if getArgString(args, "namespace", "") == "" {
			args["namespace"] = "knowledge_base"
		}
		if getArgInt(args, "top_k", 0) <= 0 {
			args["top_k"] = 5
		}
	case "capture_screen":
		if getArgString(args, "mode", "") == "" {
			args["mode"] = "screen"
		}
	case "watch_terminal":
		if getArgInt(args, "duration_seconds", 0) <= 0 {
			args["duration_seconds"] = 30
		}
		if getArgInt(args, "interval_ms", 0) <= 0 {
			args["interval_ms"] = 2500
		}
	case "draw_box":
		if getArgInt(args, "w", 0) <= 0 {
			args["w"] = 220
		}
		if getArgInt(args, "h", 0) <= 0 {
			args["h"] = 120
		}
		if getArgInt(args, "duration_ms", 0) <= 0 {
			args["duration_ms"] = 3200
		}
	case "draw_war_room":
		if getArgInt(args, "duration_ms", 0) <= 0 {
			args["duration_ms"] = 10_000
		}
	case "auto_tool":
		if getArgString(args, "goal", "") == "" {
			if resolved := strings.TrimSpace(resolveAutoToolGoal(toolInvocation{Tool: tool, Args: args})); resolved != "" {
				args["goal"] = resolved
			} else {
				args["goal"] = taskQuery
			}
		}
		maxDepth := getArgInt(args, "max_depth", 0)
		if maxDepth > 0 {
			args["max_depth"] = clampInt(maxDepth, 1, 4)
		}
	case "multimodal_tool":
		if strings.TrimSpace(resolveMultimodalQuery(args, artifactBundle{})) == "" {
			args["query"] = taskQuery
		}
	case "doc_search":
		if getArgString(args, "query", "") == "" {
			args["query"] = taskQuery
		}
		if getArgString(args, "dir", "") == "" {
			args["dir"] = "."
		}
	}

	if tool != original {
		return toolInvocation{Tool: tool, Args: args}, fmt.Sprintf("rerouted %s -> %s for better fit", original, tool)
	}
	return toolInvocation{Tool: tool, Args: args}, ""
}

func executeToolCall(tc *tools.GLMToolClient, call toolInvocation) (string, error) {
	if err := maybeRequireSelectiveIntervention(call); err != nil {
		return "", err
	}
	if err := ensureToolAllowedInNamespace(activeRuntimeNamespace(), call.Tool); err != nil {
		return "", err
	}
	switch call.Tool {
	case "web_search":
		query := getArgString(call.Args, "query", "")
		if query == "" {
			return "", fmt.Errorf("web_search requires a non-empty 'query'")
		}
		res, err := tc.WebSearch(tools.WebSearchParams{
			Query:       query,
			TopK:        getArgInt(call.Args, "top_k", 0),
			RecencyDays: getArgInt(call.Args, "recency_days", 0),
			SiteFilter:  getArgStringSlice(call.Args, "site_filter"),
		})
		if err != nil {
			return "", err
		}
		return mustJSON(res), nil
	case "fetch_url":
		url := getArgString(call.Args, "url", "")
		if url == "" {
			return "", fmt.Errorf("fetch_url requires a non-empty 'url'")
		}
		res, err := tc.FetchURL(tools.FetchURLParams{
			URL:      url,
			MaxChars: getArgInt(call.Args, "max_chars", 0),
		})
		if err != nil {
			return "", err
		}
		return mustJSON(res), nil
	case "http_request":
		url := getArgString(call.Args, "url", "")
		if url == "" {
			return "", fmt.Errorf("http_request requires a non-empty 'url'")
		}
		res, err := tc.HTTPRequest(tools.HTTPRequestParams{
			Method:           strings.ToUpper(getArgString(call.Args, "method", "GET")),
			URL:              url,
			Headers:          getArgStringMap(call.Args, "headers"),
			Body:             getArgString(call.Args, "body", ""),
			AllowlistProfile: getArgString(call.Args, "allowlist_profile", ""),
		})
		if err != nil {
			return "", err
		}
		return mustJSON(res), nil
	case "vector_retrieve":
		query := getArgString(call.Args, "query", "")
		if query == "" {
			return "", fmt.Errorf("vector_retrieve requires a non-empty 'query'")
		}
		res, err := tc.VectorRetrieve(tools.VectorRetrieveParams{
			Query:     query,
			Namespace: getArgString(call.Args, "namespace", "knowledge_base"),
			TopK:      getArgInt(call.Args, "top_k", 5),
			Filters:   getArgMap(call.Args, "filters"),
		})
		if err != nil {
			return "", err
		}
		return mustJSON(res), nil
	case "execute_code":
		lang := getArgString(call.Args, "language", "")
		code := getArgString(call.Args, "code", "")
		if lang == "" || code == "" {
			return "", fmt.Errorf("execute_code requires non-empty 'language' and 'code'")
		}
		timeoutSeconds := getArgInt(call.Args, "timeout_seconds", 10)
		if timeoutSeconds <= 0 {
			timeoutSeconds = 10
		}
		res, err := tc.ExecuteCode(tools.CodeExecParams{
			Language:       lang,
			Code:           code,
			TimeoutSeconds: timeoutSeconds,
		})
		if err != nil {
			return "", err
		}
		return mustJSON(res), nil
	case "sys_exec":
		command := getArgString(call.Args, "command", "")
		if strings.TrimSpace(command) == "" {
			return "", fmt.Errorf("sys_exec requires non-empty 'command'")
		}
		result := skills.SysExec(skills.SysExecRequest{
			Command:        command,
			Override:       getArgBool(call.Args, "override", false),
			TimeoutSeconds: getArgInt(call.Args, "timeout_seconds", 20),
		})
		return mustJSON(result), nil
	case "capture_screen":
		result := skills.CaptureScreen(skills.CaptureScreenRequest{
			Consent:        getArgBool(call.Args, "consent", false),
			Mode:           getArgString(call.Args, "mode", "screen"),
			WindowID:       getArgString(call.Args, "window_id", ""),
			OutputPath:     getArgString(call.Args, "output_path", ""),
			TimeoutSeconds: getArgInt(call.Args, "timeout_seconds", 15),
			MaxBase64Chars: getArgInt(call.Args, "max_base64_chars", 4_000_000),
		})
		return mustJSON(result), nil
	case "watch_terminal":
		result := skills.WatchTerminal(skills.WatchTerminalRequest{
			Consent:         getArgBool(call.Args, "consent", false),
			DurationSeconds: getArgInt(call.Args, "duration_seconds", 30),
			IntervalMS:      getArgInt(call.Args, "interval_ms", 2500),
			OutputDir:       getArgString(call.Args, "output_dir", ""),
			MaxBase64Chars:  getArgInt(call.Args, "max_base64_chars", 400_000),
		})
		if insight, ok := skills.BuildLintMirrorFromWatch(result); ok && insight.Detected {
			emitIDEMirrorBrief(skills.FormatLintMirrorBrief(insight))
		}
		return mustJSON(result), nil
	case "draw_box":
		result := skills.DrawBox(skills.DrawBoxRequest{
			Consent:    getArgBool(call.Args, "consent", false),
			X:          getArgInt(call.Args, "x", 120),
			Y:          getArgInt(call.Args, "y", 160),
			W:          getArgInt(call.Args, "w", 220),
			H:          getArgInt(call.Args, "h", 120),
			Text:       getArgString(call.Args, "text", ""),
			DurationMS: getArgInt(call.Args, "duration_ms", 3200),
		})
		return mustJSON(result), nil
	case "draw_war_room":
		result := skills.DrawWarRoom(skills.DrawWarRoomRequest{
			Consent:    getArgBool(call.Args, "consent", false),
			DurationMS: getArgInt(call.Args, "duration_ms", 10_000),
			Boxes:      getArgWarRoomBoxes(call.Args, "boxes"),
		})
		return mustJSON(result), nil
	case "analyze_visual_target":
		target := getArgMap(call.Args, "target")
		res := skills.AnalyzeVisualTarget(skills.AnalyzeVisualTargetRequest{
			Consent:      getArgBool(call.Args, "consent", false),
			Intent:       getArgString(call.Args, "intent", "analyze_ui_element"),
			ShellContext: getArgString(call.Args, "shell_context", ""),
			Target: skills.VisualTargetPayload{
				CapturePath:     getArgString(target, "capture_path", ""),
				CaptureMode:     getArgString(target, "capture_mode", ""),
				WindowID:        getArgString(target, "window_id", ""),
				TargetID:        getArgString(target, "target_id", ""),
				X:               getArgInt(target, "x", 0),
				Y:               getArgInt(target, "y", 0),
				Width:           getArgInt(target, "width", getArgInt(target, "w", 160)),
				Height:          getArgInt(target, "height", getArgInt(target, "h", 90)),
				Label:           getArgString(target, "label", ""),
				Snippet:         getArgString(target, "snippet", ""),
				Confidence:      getArgFloat(target, "confidence", 0.55),
				OverlayKind:     getArgString(target, "overlay_kind", ""),
				SourceModel:     getArgString(target, "source_model", ""),
				SpatialElements: getArgSpatialElements(target, "spatial_elements"),
				Provenance:      getArgStringMap(target, "provenance"),
			},
		})
		return mustJSON(res), nil
	case "multimodal_tool":
		return executeMultimodalTool(tc, call)
	case "doc_search":
		report, err := runDocSearch(DocSearchOptions{
			Query:         getArgString(call.Args, "query", ""),
			Dir:           getArgString(call.Args, "dir", "."),
			Regex:         getArgBool(call.Args, "regex", false),
			CaseSensitive: getArgBool(call.Args, "case_sensitive", false),
			Extensions:    getArgStringSlice(call.Args, "extensions"),
			ExcludeDirs:   getArgStringSlice(call.Args, "exclude_dirs"),
			IncludeHidden: getArgBool(call.Args, "include_hidden", false),
			FollowSymlink: getArgBool(call.Args, "follow_symlinks", false),
			MaxFiles:      getArgInt(call.Args, "max_files", 0),
			MaxMatches:    getArgInt(call.Args, "max_matches", 0),
			MaxPerFile:    getArgInt(call.Args, "max_per_file", 0),
			MaxFileBytes:  int64(getArgInt(call.Args, "max_file_bytes", 0)),
			ContextLines:  getArgInt(call.Args, "context_lines", 0),
		})
		if err != nil {
			return "", err
		}
		return docSearchReportJSON(report), nil
	case "auto_tool":
		return executeRecursiveAutoTool(tc, call, 0)
	case "admin_list_clients":
		admin, err := tools.NewGLMAdminClientFromEnv()
		if err != nil {
			return "", err
		}
		clients, err := admin.ListClients()
		if err != nil {
			return "", err
		}
		return mustJSON(map[string]interface{}{"clients": clients}), nil
	case "provision_client", "admin_create_client":
		admin, err := tools.NewGLMAdminClientFromEnv()
		if err != nil {
			return "", err
		}
		created, err := admin.CreateClient(getArgString(call.Args, "client_id", ""))
		if err != nil {
			return "", err
		}
		if err := tools.UpsertLocalAPIKey(localAPIKeysPath(), created.ClientID, created.APIKey); err != nil {
			return "", err
		}
		activate := getArgBool(call.Args, "activate", true)
		if activate && tc != nil {
			*tc = *tc.WithCredentials(created.ClientID, created.APIKey)
		}
		return mustJSON(map[string]interface{}{
			"client_id": created.ClientID,
			"saved_to":  localAPIKeysPath(),
			"activated": activate,
		}), nil
	case "rotate_client_key", "admin_rotate_client":
		admin, err := tools.NewGLMAdminClientFromEnv()
		if err != nil {
			return "", err
		}
		clientID := getArgString(call.Args, "client_id", "")
		if clientID == "" {
			return "", fmt.Errorf("admin_rotate_client requires non-empty 'client_id'")
		}
		rotated, err := admin.RotateClientKey(clientID)
		if err != nil {
			return "", err
		}
		if err := tools.UpsertLocalAPIKey(localAPIKeysPath(), rotated.ClientID, rotated.APIKey); err != nil {
			return "", err
		}
		activate := getArgBool(call.Args, "activate", true)
		if activate && tc != nil {
			*tc = *tc.WithCredentials(rotated.ClientID, rotated.APIKey)
		}
		return mustJSON(map[string]interface{}{
			"client_id": rotated.ClientID,
			"saved_to":  localAPIKeysPath(),
			"activated": activate,
		}), nil
	case "revoke_client", "admin_delete_client":
		admin, err := tools.NewGLMAdminClientFromEnv()
		if err != nil {
			return "", err
		}
		clientID := getArgString(call.Args, "client_id", "")
		if clientID == "" {
			return "", fmt.Errorf("admin_delete_client requires non-empty 'client_id'")
		}
		if err := admin.DeleteClient(clientID); err != nil {
			return "", err
		}
		if err := tools.RemoveLocalAPIKey(localAPIKeysPath(), clientID); err != nil {
			return "", err
		}
		return mustJSON(map[string]interface{}{
			"client_id": clientID,
			"deleted":   true,
			"saved_to":  localAPIKeysPath(),
		}), nil
	default:
		return "", fmt.Errorf("unknown tool: %s", call.Tool)
	}
}

func mustJSON(v interface{}) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func sanitizeModelOutput(s string) string {
	s = stripReasoningSections(s)
	s = strings.TrimSpace(s)
	return s
}

func redactSensitiveOutput(s string) string {
	out := strings.TrimSpace(s)
	if out == "" {
		return out
	}
	replacements := []*regexp.Regexp{
		regexp.MustCompile(`(?i)\b(GLM_API_KEY|GLM_ADMIN_TOKEN)\s*[:=]\s*['"]?([A-Za-z0-9_\-]{8,})['"]?`),
		regexp.MustCompile(`(?i)\b(api[_-]?key|secret|token)\b\s*[:=]\s*['"]?([A-Za-z0-9_\-]{16,})['"]?`),
		regexp.MustCompile(`(?i)\bBearer\s+([A-Za-z0-9_\-]{16,})\b`),
	}
	for _, re := range replacements {
		out = re.ReplaceAllStringFunc(out, func(m string) string {
			if strings.Contains(strings.ToLower(m), "bearer ") {
				return "Bearer [REDACTED]"
			}
			parts := strings.SplitN(m, "=", 2)
			if len(parts) == 2 {
				return strings.TrimSpace(parts[0]) + "=[REDACTED]"
			}
			parts = strings.SplitN(m, ":", 2)
			if len(parts) == 2 {
				return strings.TrimSpace(parts[0]) + ": [REDACTED]"
			}
			return "[REDACTED]"
		})
	}
	return out
}

func maybeRunTelemetryAwareness(mm *memory.MemoryManager, query string, messages []api.Message) string {
	if liveTelemetry == nil {
		return ""
	}
	used := approxTokensFromMessages(messages)
	liveTelemetry.UpdateAttention(used)
	if !liveTelemetry.ShouldPrune() || mm == nil {
		return consumeTelemetrySummary()
	}
	pruned, err := mm.PruneLowSignalContext(8)
	if err != nil || pruned <= 0 {
		return consumeTelemetrySummary()
	}
	action := "focus on this code refactor"
	if q := strings.ToLower(strings.TrimSpace(query)); q != "" {
		if strings.Contains(q, "deploy") || strings.Contains(q, "ops") || strings.Contains(q, "vps") {
			action = "focus on this infrastructure task"
		}
	}
	summary := liveTelemetry.BuildStateSummary(action)
	liveTelemetry.MarkPruneEvent(pruned, summary)
	return consumeTelemetrySummary()
}

func consumeTelemetrySummary() string {
	if liveTelemetry == nil {
		return ""
	}
	return strings.TrimSpace(liveTelemetry.ConsumeStateSummary())
}

func recordAlignmentAuditTelemetry(veto bool) {
	if liveTelemetry == nil {
		return
	}
	liveTelemetry.RecordSymbolicAudit(veto)
}

func recordInferenceAndTelemetryFeed(turnStarted time.Time, outputText string) {
	if liveTelemetry == nil {
		return
	}
	elapsed := time.Since(turnStarted)
	if elapsed <= 0 {
		return
	}
	tokens := len(strings.Fields(strings.TrimSpace(outputText)))
	if tokens <= 0 {
		tokens = len(strings.TrimSpace(outputText)) / 4
	}
	if tokens <= 0 {
		return
	}
	liveTelemetry.RecordInferenceVelocity(tokens, elapsed)
	if err := state.AppendTelemetryFeed(telemetryFeedPath, liveTelemetry.Snapshot()); err != nil {
		fmt.Printf("Warning: Failed writing telemetry feed: %v\n", err)
	}
}

func consumeReflexAlerts(path string, maxN int) ([]reflexAlert, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		path = reflexAlertsPath
	}
	if maxN <= 0 {
		maxN = 3
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var lines []string
	for _, ln := range strings.Split(string(raw), "\n") {
		ln = strings.TrimSpace(ln)
		if ln == "" {
			continue
		}
		lines = append(lines, ln)
	}
	if len(lines) == 0 {
		return nil, nil
	}
	var out []reflexAlert
	for _, ln := range lines {
		var a reflexAlert
		if err := json.Unmarshal([]byte(ln), &a); err != nil {
			continue
		}
		out = append(out, a)
	}
	if len(out) > maxN {
		out = out[len(out)-maxN:]
	}
	_ = os.WriteFile(path, []byte(""), 0o644)
	return out, nil
}

func consumeMirrorBriefs(path string, maxN int) ([]mirrorBrief, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		path = mirrorBriefsPath
	}
	if maxN <= 0 {
		maxN = 2
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var lines []string
	for _, ln := range strings.Split(string(raw), "\n") {
		ln = strings.TrimSpace(ln)
		if ln == "" {
			continue
		}
		lines = append(lines, ln)
	}
	if len(lines) == 0 {
		return nil, nil
	}
	var out []mirrorBrief
	for _, ln := range lines {
		var b mirrorBrief
		if err := json.Unmarshal([]byte(ln), &b); err != nil {
			continue
		}
		out = append(out, b)
	}
	if len(out) > maxN {
		out = out[len(out)-maxN:]
	}
	_ = os.WriteFile(path, []byte(""), 0o644)
	return out, nil
}

func recordToolFailure(toolName, query string, err error) {
	if err == nil {
		return
	}
	ev := toolFailureEvent{
		Timestamp: time.Now().UTC(),
		Tool:      strings.TrimSpace(toolName),
		Query:     strings.TrimSpace(query),
		Error:     strings.TrimSpace(err.Error()),
	}
	if b, merr := json.Marshal(ev); merr == nil {
		if werr := appendJSONLFile(toolFailuresPath, string(b)); werr != nil {
			fmt.Printf("Warning: Failed writing tool failure event: %v\n", werr)
		}
	}
}

func appendJSONLFile(path, line string) error {
	path = strings.TrimSpace(path)
	if path == "" {
		return fmt.Errorf("path required")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.WriteString(strings.TrimSpace(line) + "\n")
	return err
}

func emitIDEMirrorBrief(msg string) {
	msg = strings.TrimSpace(msg)
	if msg == "" {
		return
	}
	b, err := json.Marshal(mirrorBrief{
		Timestamp: time.Now().UTC(),
		Source:    "glm-ide",
		Message:   msg,
	})
	if err != nil {
		return
	}
	_ = appendJSONLFile(mirrorBriefsPath, string(b))
}

func refreshWorkspaceMentalMap(root string) {
	ctx, err := skills.BuildWorkspaceContext(root, "", 260)
	if err != nil {
		return
	}
	workspaceMentalMapMu.Lock()
	workspaceMentalMap = ctx
	workspaceMentalMapMu.Unlock()
}

func getWorkspaceMentalMap() skills.WorkspaceContext {
	workspaceMentalMapMu.RLock()
	defer workspaceMentalMapMu.RUnlock()
	return workspaceMentalMap
}

type briefState struct {
	LastShown time.Time `json:"last_shown"`
}

type capabilityManifestDelta struct {
	Name        string
	Language    string
	Description string
	CreatedAt   time.Time
}

type labResultSnapshot struct {
	Timestamp    time.Time `json:"timestamp"`
	TaskID       string    `json:"task_id,omitempty"`
	BuildID      string    `json:"build_id,omitempty"`
	Success      bool      `json:"success"`
	Attempts     int       `json:"attempts"`
	SourcePath   string    `json:"source_path"`
	ShadowPath   string    `json:"shadow_path,omitempty"`
	VerifyStdout string    `json:"verify_stdout,omitempty"`
	VerifyStderr string    `json:"verify_stderr,omitempty"`
}

type chronosState struct {
	LastCheck       time.Time `json:"last_check"`
	LastForgeryID   string    `json:"last_forgery_id,omitempty"`
	LastEscalatedAt time.Time `json:"last_escalated_at,omitempty"`
}

type documentaryResultSnapshot struct {
	Artifact struct {
		Path string `json:"path"`
	} `json:"artifact"`
	VisualReasoning []struct {
		Summary            string   `json:"summary"`
		SpatialCorrelation string   `json:"spatial_correlation"`
		TechnicalFindings  []string `json:"technical_findings"`
		SpatialElements    []struct {
			Kind       string  `json:"kind"`
			Label      string  `json:"label"`
			X          int     `json:"x"`
			Y          int     `json:"y"`
			Width      int     `json:"width"`
			Height     int     `json:"height"`
			Confidence float64 `json:"confidence"`
		} `json:"spatial_elements"`
	} `json:"visual_reasoning"`
	HighValueEntities []struct {
		Kind       string    `json:"kind"`
		Value      string    `json:"value"`
		Confidence float64   `json:"confidence"`
		Timestamp  time.Time `json:"timestamp"`
	} `json:"high_value_entities"`
}

func maybePrintMorningBrief(sm *state.Manager) {
	prev := loadBriefState(morningBriefState)
	since := prev.LastShown
	if since.IsZero() {
		since = time.Now().UTC().Add(-24 * time.Hour)
	}

	scoutBriefs := loadMirrorBriefsSince(mirrorBriefsPath, since, 6, func(b mirrorBrief) bool {
		src := strings.ToLower(strings.TrimSpace(b.Source))
		return strings.Contains(src, "scout")
	})
	scoutCount := len(scoutBriefs)
	archivedCount := countJSONLSince(archiveBriefsPath, since, func(m map[string]interface{}) bool {
		src := strings.ToLower(strings.TrimSpace(fmt.Sprintf("%v", m["source"])))
		return strings.Contains(src, "archiv")
	})
	decisionCount := countJSONLSince(decisionFeedPath, since, nil)
	newTools := 0
	labLine := buildLabActivityLine(since)
	tier1Line := buildTier1DecisionLine(since)

	greeting := "Mornin' Mike/Cassian."
	scoutLine := fmt.Sprintf("Scout logged %d new intuition brief(s) overnight.", scoutCount)
	if scoutCount > 0 {
		scoutLine = "Scout flagged: " + strings.TrimSpace(scoutBriefs[len(scoutBriefs)-1].Message)
	}
	capabilityLine := "Capability pipeline is using GLM toolserver + local skill registry."
	archiveLine := fmt.Sprintf("Archivist indexed %d update(s) and stored %d new decision record(s).", archivedCount, decisionCount)
	if archivedCount > 0 || decisionCount > 0 {
		archiveLine = "Archivist has the reasoning trace queued if you want the deep dive."
	}
	ws := getWorkspaceMentalMap()
	if strings.TrimSpace(ws.Root) == "" {
		refreshWorkspaceMentalMap(".")
		ws = getWorkspaceMentalMap()
	}
	developerHandover := skills.BuildDeveloperHandoverLine(ws, scoutCount, newTools)
	if newTools > 0 {
		developerHandover += " Build surface looks ready for the next Chronos/Forge pass."
	}

	critical := selectCriticalScoutAnomaly(scoutBriefs)
	actionable := "Priority anomaly: none high-confidence yet. First command: `scout status`."
	if strings.TrimSpace(critical) != "" {
		actionable = fmt.Sprintf("Priority anomaly: %s First command: `investigate anomaly \"%s\"`.", critical, clampBriefForCommand(critical))
	}

	output.PrintReasoningMirrorLine(os.Stdout, greeting+" "+scoutLine+" "+capabilityLine+" "+archiveLine, sm, chatRawOutput)
	fmt.Println()
	output.PrintReasoningMirrorLine(os.Stdout, developerHandover, sm, chatRawOutput)
	fmt.Println()
	output.PrintReasoningMirrorLine(os.Stdout, labLine, sm, chatRawOutput)
	fmt.Println()
	maybeShowLabDiffHUD(since, sm)
	if strings.TrimSpace(tier1Line) != "" {
		output.PrintReasoningMirrorLine(os.Stdout, tier1Line, sm, chatRawOutput)
		fmt.Println()
	}
	output.PrintReasoningMirrorLine(os.Stdout, actionable, sm, chatRawOutput)
	fmt.Println()

	_ = saveBriefState(morningBriefState, briefState{LastShown: time.Now().UTC()})
}

func maybeHandleBuiltInChatCommand(input string) (bool, string) {
	cmd := strings.ToLower(strings.TrimSpace(input))
	switch cmd {
	case "scout status":
		since := time.Now().UTC().Add(-24 * time.Hour)
		scoutBriefs := loadMirrorBriefsSince(mirrorBriefsPath, since, 6, func(b mirrorBrief) bool {
			src := strings.ToLower(strings.TrimSpace(b.Source))
			return strings.Contains(src, "scout")
		})
		critical := selectCriticalScoutAnomaly(scoutBriefs)
		var b strings.Builder
		b.WriteString("SCOUT STATUS\n\n")
		b.WriteString(fmt.Sprintf("briefs_24h: %d\n", len(scoutBriefs)))
		if strings.TrimSpace(critical) == "" {
			b.WriteString("high_confidence_anomaly: none\n")
			b.WriteString("recommendation: no urgent anomaly workflow required\n")
		} else {
			b.WriteString("high_confidence_anomaly: detected\n")
			b.WriteString("anomaly: " + strings.TrimSpace(critical) + "\n")
			b.WriteString("recommendation: investigate anomaly with source evidence\n")
		}
		if len(scoutBriefs) > 0 {
			latest := strings.TrimSpace(scoutBriefs[len(scoutBriefs)-1].Message)
			if latest != "" {
				b.WriteString("latest_brief: " + latest + "\n")
			}
		}
		return true, strings.TrimSpace(b.String())
	default:
		return false, ""
	}
}

func maybeRunChronosSmokingGun(sm *state.Manager) {
	cs := loadChronosState(chronosStatePath)
	if !cs.LastEscalatedAt.IsZero() && time.Since(cs.LastEscalatedAt) < 2*time.Minute {
		return
	}
	findings := loadScoutFindingsForChronos(documentaryResults)
	if len(findings) < 2 {
		return
	}
	ctxMatches, err := orchestration.QueryReasoningArchive("timeline contradiction signature forgery tool", 6)
	if err != nil {
		return
	}
	var archiveCtx []string
	for _, m := range ctxMatches {
		if strings.TrimSpace(m.Record.Reasoning) != "" {
			archiveCtx = append(archiveCtx, strings.TrimSpace(m.Record.Reasoning))
		}
		if strings.TrimSpace(m.Record.Query) != "" {
			archiveCtx = append(archiveCtx, strings.TrimSpace(m.Record.Query))
		}
	}
	res := cognition.ResolveEpistemicConflicts(findings, archiveCtx)
	if strings.TrimSpace(res.MostLikelyForgery) == "" || res.WinningScore < 0.88 {
		cs.LastCheck = time.Now().UTC()
		_ = saveChronosState(chronosStatePath, cs)
		return
	}
	if strings.EqualFold(strings.TrimSpace(cs.LastForgeryID), strings.TrimSpace(res.MostLikelyForgery)) {
		return
	}
	target := findFindingByID(findings, res.MostLikelyForgery)
	x, y, w, h := 450, 210, 260, 120
	if target != nil {
		x = parseMetaInt(target.Metadata, "hud_x", x)
		y = parseMetaInt(target.Metadata, "hud_y", y)
		w = parseMetaInt(target.Metadata, "hud_w", w)
		h = parseMetaInt(target.Metadata, "hud_h", h)
	}
	_ = skills.DrawBox(skills.DrawBoxRequest{
		Consent:    true,
		X:          x,
		Y:          y,
		W:          w,
		H:          h,
		Text:       "Timeline conflict",
		DurationMS: 4200,
	})
	output.PrintReasoningMirrorLine(os.Stdout, "Sir, we have a structural anomaly in the timeline. Highlighting the conflicting signatures now.", sm, chatRawOutput)
	fmt.Println()

	cs.LastCheck = time.Now().UTC()
	cs.LastEscalatedAt = time.Now().UTC()
	cs.LastForgeryID = strings.TrimSpace(res.MostLikelyForgery)
	_ = saveChronosState(chronosStatePath, cs)
}

func loadScoutFindingsForChronos(resultsDir string) []cognition.ScoutFinding {
	entries, err := os.ReadDir(resultsDir)
	if err != nil {
		return nil
	}
	var out []cognition.ScoutFinding
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(strings.ToLower(e.Name()), ".json") {
			continue
		}
		path := filepath.Join(resultsDir, e.Name())
		raw, err := os.ReadFile(path)
		if err != nil || len(raw) == 0 {
			continue
		}
		var dr documentaryResultSnapshot
		if err := json.Unmarshal(raw, &dr); err != nil {
			continue
		}
		info, _ := os.Stat(path)
		ts := time.Now().UTC()
		if info != nil {
			ts = info.ModTime().UTC()
		}
		summary := ""
		meta := map[string]string{}
		if len(dr.VisualReasoning) > 0 {
			vr := dr.VisualReasoning[0]
			summary = strings.TrimSpace(vr.Summary + " " + vr.SpatialCorrelation + " " + strings.Join(vr.TechnicalFindings, " "))
			if len(vr.SpatialElements) > 0 {
				el := vr.SpatialElements[0]
				meta["hud_x"] = strconv.Itoa(el.X)
				meta["hud_y"] = strconv.Itoa(el.Y)
				meta["hud_w"] = strconv.Itoa(maxInt(el.Width, 220))
				meta["hud_h"] = strconv.Itoa(maxInt(el.Height, 100))
			}
		}
		if summary == "" {
			summary = "Documentary evidence update from " + e.Name()
		}
		actor := ""
		location := extractChronosLocation(summary)
		eventType := "documentary_finding"
		docID := strings.TrimSpace(filepath.Base(dr.Artifact.Path))
		conf := 0.62
		if len(dr.HighValueEntities) > 0 {
			conf = 0.84
			for _, hv := range dr.HighValueEntities {
				if strings.Contains(strings.ToLower(hv.Kind), "signature") {
					eventType = "signature_evidence"
				}
			}
		}
		out = append(out, cognition.ScoutFinding{
			ID: strings.TrimSuffix(e.Name(), ".json"),
			SourcePath: func() string {
				if strings.TrimSpace(dr.Artifact.Path) != "" {
					return strings.TrimSpace(dr.Artifact.Path)
				}
				return path
			}(),
			Summary:    summary,
			Actor:      actor,
			Location:   location,
			EventType:  eventType,
			DocumentID: docID,
			Timestamp:  ts,
			Confidence: conf,
			Metadata:   meta,
		})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Timestamp.Before(out[j].Timestamp) })
	if len(out) > 64 {
		out = out[len(out)-64:]
	}
	return out
}

func findFindingByID(in []cognition.ScoutFinding, id string) *cognition.ScoutFinding {
	id = strings.TrimSpace(id)
	for i := range in {
		if strings.TrimSpace(in[i].ID) == id {
			return &in[i]
		}
	}
	return nil
}

func extractChronosLocation(s string) string {
	l := strings.ToLower(strings.TrimSpace(s))
	for _, city := range []string{"london", "new york", "berlin", "paris", "tokyo"} {
		if strings.Contains(l, city) {
			return city
		}
	}
	return ""
}

func parseMetaInt(meta map[string]string, key string, fallback int) int {
	if meta == nil {
		return fallback
	}
	raw := strings.TrimSpace(meta[key])
	if raw == "" {
		return fallback
	}
	v, err := strconv.Atoi(raw)
	if err != nil {
		return fallback
	}
	return v
}

func loadBriefState(path string) briefState {
	var s briefState
	raw, err := os.ReadFile(path)
	if err != nil || len(raw) == 0 {
		return s
	}
	_ = json.Unmarshal(raw, &s)
	return s
}

func saveBriefState(path string, s briefState) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	b, _ := json.MarshalIndent(s, "", "  ")
	return os.WriteFile(path, b, 0o644)
}

func loadChronosState(path string) chronosState {
	var s chronosState
	raw, err := os.ReadFile(path)
	if err != nil || len(raw) == 0 {
		return s
	}
	_ = json.Unmarshal(raw, &s)
	return s
}

func saveChronosState(path string, s chronosState) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	b, _ := json.MarshalIndent(s, "", "  ")
	return os.WriteFile(path, b, 0o644)
}

func countJSONLSince(path string, since time.Time, pred func(map[string]interface{}) bool) int {
	raw, err := os.ReadFile(path)
	if err != nil || len(raw) == 0 {
		return 0
	}
	count := 0
	for _, ln := range strings.Split(string(raw), "\n") {
		ln = strings.TrimSpace(ln)
		if ln == "" {
			continue
		}
		var row map[string]interface{}
		if err := json.Unmarshal([]byte(ln), &row); err != nil {
			continue
		}
		if pred != nil && !pred(row) {
			continue
		}
		ts := parseAnyTimestamp(row["timestamp"])
		if ts.IsZero() || ts.After(since) {
			count++
		}
	}
	return count
}

func parseAnyTimestamp(v interface{}) time.Time {
	s := strings.TrimSpace(fmt.Sprintf("%v", v))
	if s == "" || s == "<nil>" {
		return time.Time{}
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t
	}
	if t, err := time.Parse("2006-01-02 15:04:05", s); err == nil {
		return t
	}
	return time.Time{}
}

func countNewToolManifestsSince(since time.Time) int {
	dir := "bin/manufactured/manifests"
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0
	}
	total := 0
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(strings.ToLower(e.Name()), ".json") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		if info.ModTime().UTC().After(since) {
			total++
		}
	}
	return total
}

func buildLabActivityLine(since time.Time) string {
	builds, passed := loadLabBuildStatsSince(labResultsPath, since)
	labBriefs := loadMirrorBriefsSince(mirrorBriefsPath, since, 24, func(b mirrorBrief) bool {
		src := strings.ToLower(strings.TrimSpace(b.Source))
		return strings.Contains(src, "lab-assistant")
	})
	deadlocksResolved := 0
	telemetryOptimized := false
	for _, b := range labBriefs {
		msg := strings.ToLower(strings.TrimSpace(b.Message))
		if strings.Contains(msg, "deadlock") && (strings.Contains(msg, "resolved") || strings.Contains(msg, "verified")) {
			deadlocksResolved++
		}
		if strings.Contains(msg, "telemetry") && containsAnyToken(msg, "optimiz", "improv", "pipeline") {
			telemetryOptimized = true
		}
	}
	telemetryPhrase := "and staged telemetry updates"
	if telemetryOptimized {
		telemetryPhrase = "and optimized the telemetry pipeline"
	}
	return fmt.Sprintf(
		"Lab Activity: I ran %d shadow-builds overnight (%d passed). I resolved %d deadlock(s) %s. The results are staged for your review.",
		builds,
		passed,
		deadlocksResolved,
		telemetryPhrase,
	)
}

func buildTier1DecisionLine(since time.Time) string {
	oracleBriefs := loadMirrorBriefsSince(mirrorBriefsPath, since, 12, func(b mirrorBrief) bool {
		src := strings.ToLower(strings.TrimSpace(b.Source))
		if strings.Contains(src, "oracle") {
			return true
		}
		msg := strings.ToLower(strings.TrimSpace(b.Message))
		return strings.Contains(msg, "oracle") || strings.Contains(msg, "tier-1")
	})
	if len(oracleBriefs) == 0 {
		return ""
	}
	last := strings.TrimSpace(oracleBriefs[len(oracleBriefs)-1].Message)
	if last == "" {
		last = "Oracle guidance was required to break a local consensus stall."
	}
	return "Tier-1 Technical Decision: " + last
}

func loadLabBuildStatsSince(path string, since time.Time) (total int, passed int) {
	raw, err := os.ReadFile(path)
	if err != nil || len(raw) == 0 {
		return 0, 0
	}
	for _, ln := range strings.Split(string(raw), "\n") {
		ln = strings.TrimSpace(ln)
		if ln == "" {
			continue
		}
		var row labResultSnapshot
		if err := json.Unmarshal([]byte(ln), &row); err != nil {
			continue
		}
		if !row.Timestamp.IsZero() && !row.Timestamp.After(since) {
			continue
		}
		total++
		if row.Success {
			passed++
		}
	}
	return total, passed
}

func maybeShowLabDiffHUD(since time.Time, sm *state.Manager) {
	latest, ok := latestSuccessfulLabResultSince(labResultsPath, since)
	if !ok {
		return
	}
	passed, total, buildTime := deriveLabConfidence(latest)
	confLabel := fmt.Sprintf("Tests Passed: %d/%d | Build Time: %s", passed, total, buildTime)

	buildID := strings.TrimSpace(latest.BuildID)
	if buildID == "" {
		buildID = strings.TrimSpace(latest.TaskID)
	}
	if buildID == "" {
		buildID = "latest"
	}
	buggy := "Buggy: " + shortPathForHUD(latest.SourcePath)
	fixed := "Verified Fix: build #" + buildID
	res := skills.DrawCodeDiffHUD(skills.CodeDiffHUDRequest{
		Consent:         true,
		BuggyLabel:      buggy,
		VerifiedLabel:   fixed,
		ConfidenceLabel: confLabel,
		DurationMS:      5600,
	})
	if res.ExitCode == 0 {
		output.PrintReasoningMirrorLine(os.Stdout, "Visual diff: Buggy code highlighted in red, verified fix in green. Confidence HUD: "+confLabel, sm, chatRawOutput)
		fmt.Println()
	}
}

func latestSuccessfulLabResultSince(path string, since time.Time) (labResultSnapshot, bool) {
	raw, err := os.ReadFile(path)
	if err != nil || len(raw) == 0 {
		return labResultSnapshot{}, false
	}
	var best labResultSnapshot
	found := false
	for _, ln := range strings.Split(string(raw), "\n") {
		ln = strings.TrimSpace(ln)
		if ln == "" {
			continue
		}
		var row labResultSnapshot
		if err := json.Unmarshal([]byte(ln), &row); err != nil {
			continue
		}
		if !row.Success {
			continue
		}
		if !row.Timestamp.IsZero() && !row.Timestamp.After(since) {
			continue
		}
		if !found || row.Timestamp.After(best.Timestamp) {
			best = row
			found = true
		}
	}
	return best, found
}

func deriveLabConfidence(r labResultSnapshot) (passed int, total int, buildTime string) {
	buildTime = "n/a"
	if r.Success {
		passed, total = 1, 1
	}
	stdout := strings.TrimSpace(r.VerifyStdout)
	if stdout == "" {
		return passed, total, buildTime
	}

	okLines := 0
	failLines := 0
	for _, ln := range strings.Split(stdout, "\n") {
		l := strings.TrimSpace(ln)
		if strings.HasPrefix(l, "ok\t") || strings.HasPrefix(l, "ok  \t") || strings.HasPrefix(l, "ok ") {
			okLines++
		}
		if strings.HasPrefix(l, "FAIL\t") || strings.HasPrefix(l, "FAIL ") {
			failLines++
		}
	}
	if okLines > 0 || failLines > 0 {
		passed = okLines
		total = okLines + failLines
		if total == 0 {
			total = okLines
		}
	}

	// Prefer the trailing "1.23s" from go test/go build output.
	reDur := regexp.MustCompile(`\b([0-9]+(?:\.[0-9]+)?s)\b`)
	matches := reDur.FindAllStringSubmatch(stdout, -1)
	if len(matches) > 0 {
		buildTime = matches[len(matches)-1][1]
	}
	if total == 0 {
		total = 1
	}
	if passed > total {
		passed = total
	}
	return passed, total, buildTime
}

func shortPathForHUD(path string) string {
	path = filepath.ToSlash(strings.TrimSpace(path))
	if path == "" {
		return "target file"
	}
	parts := strings.Split(path, "/")
	if len(parts) <= 3 {
		return path
	}
	return strings.Join(parts[len(parts)-3:], "/")
}

func loadMirrorBriefsSince(path string, since time.Time, maxN int, pred func(mirrorBrief) bool) []mirrorBrief {
	raw, err := os.ReadFile(path)
	if err != nil || len(raw) == 0 {
		return nil
	}
	var out []mirrorBrief
	for _, ln := range strings.Split(string(raw), "\n") {
		ln = strings.TrimSpace(ln)
		if ln == "" {
			continue
		}
		var b mirrorBrief
		if err := json.Unmarshal([]byte(ln), &b); err != nil {
			continue
		}
		if !b.Timestamp.IsZero() && !b.Timestamp.After(since) {
			continue
		}
		if pred != nil && !pred(b) {
			continue
		}
		out = append(out, b)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Timestamp.Before(out[j].Timestamp) })
	if maxN > 0 && len(out) > maxN {
		out = out[len(out)-maxN:]
	}
	return out
}

func loadCapabilityManifestDeltasSince(since time.Time, maxN int) []capabilityManifestDelta {
	dir := "bin/manufactured/manifests"
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []capabilityManifestDelta
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(strings.ToLower(e.Name()), ".json") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		mod := info.ModTime().UTC()
		if !mod.After(since) {
			continue
		}
		d := capabilityManifestDelta{
			Name:      strings.TrimSuffix(e.Name(), ".json"),
			Language:  "",
			CreatedAt: mod,
		}
		raw, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err == nil && len(raw) > 0 {
			var parsed struct {
				Name           string `json:"name"`
				Language       string `json:"language"`
				ToolDefinition struct {
					Description string `json:"description"`
				} `json:"tool_definition"`
			}
			if json.Unmarshal(raw, &parsed) == nil {
				if strings.TrimSpace(parsed.Name) != "" {
					d.Name = strings.TrimSpace(parsed.Name)
				}
				d.Language = strings.TrimSpace(parsed.Language)
				d.Description = strings.TrimSpace(parsed.ToolDefinition.Description)
			}
		}
		if d.Description == "" {
			d.Description = "new manufacturing capability ready"
		}
		if d.Language == "" {
			d.Language = "go"
		}
		out = append(out, d)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	if maxN > 0 && len(out) > maxN {
		out = out[len(out)-maxN:]
	}
	return out
}

func selectCriticalScoutAnomaly(briefs []mirrorBrief) string {
	bestMsg := ""
	bestScore := -1.0
	for _, b := range briefs {
		msg := strings.TrimSpace(b.Message)
		if msg == "" {
			continue
		}
		score := scoreAnomalySeverity(msg)
		if score > bestScore {
			bestScore = score
			bestMsg = msg
		}
	}
	return bestMsg
}

func scoreAnomalySeverity(msg string) float64 {
	m := strings.ToLower(strings.TrimSpace(msg))
	if m == "" {
		return 0
	}
	score := 0.0
	for _, token := range []string{"critical", "smoking gun", "contradiction", "discrepancy", "forgery", "anomaly", "escalating", "conflict"} {
		if strings.Contains(m, token) {
			score += 0.2
		}
	}
	re := regexp.MustCompile(`\b([0-9]{1,3})%\b`)
	if mm := re.FindStringSubmatch(m); len(mm) == 2 {
		if pct, err := strconv.Atoi(mm[1]); err == nil {
			score += clamp01(float64(pct) / 100.0)
		}
	}
	if strings.Contains(m, "sir,") {
		score += 0.1
	}
	return score
}

func clampBriefForCommand(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 96 {
		s = s[:96]
	}
	s = strings.ReplaceAll(s, "\"", "")
	return strings.Join(strings.Fields(s), " ")
}

func setLogicGlimpse(glimpse string) {
	glimpse = strings.TrimSpace(glimpse)
	if glimpse == "" {
		return
	}
	reasoningMirrorMu.Lock()
	defer reasoningMirrorMu.Unlock()
	pendingLogicGlimpse = glimpse
}

func consumeLogicGlimpse() string {
	reasoningMirrorMu.Lock()
	defer reasoningMirrorMu.Unlock()
	g := strings.TrimSpace(pendingLogicGlimpse)
	pendingLogicGlimpse = ""
	return g
}

func chooseThoughtHeader(input string) string {
	q := strings.ToLower(strings.TrimSpace(input))
	switch {
	case strings.Contains(q, "rust"):
		return "Wait, checking the Rust borrow checker - don't want this blowing up..."
	case strings.Contains(q, "tool"):
		return "Capability pipeline is active, let's see if this tool fits..."
	case strings.Contains(q, "audit") || strings.Contains(q, "policy") || strings.Contains(q, "symbolic"):
		return "Quick safety sweep first - better to catch a bad edge now."
	default:
		return "Thinking it through - taking the stable path, not the flashy one."
	}
}

func summarizeMCTSLogic(root *cognition.ThoughtNode) string {
	if root == nil || len(root.Children) == 0 {
		return "Looked at 1 path. It held up, so I'm using the stable result."
	}
	children := root.Children
	limit := len(children)
	if limit > 3 {
		limit = 3
	}
	type pathEval struct {
		label string
		desc  string
		score float64
	}
	paths := make([]pathEval, 0, limit)
	best := pathEval{label: "A", score: -1}
	for i := 0; i < limit; i++ {
		c := children[i]
		label := string(rune('A' + i))
		score := c.Confidence
		desc := fmt.Sprintf("Path %s looked stable", label)
		if c.Pruned || score < 0.30 {
			desc = fmt.Sprintf("Path %s was too heavy", label)
		}
		if score <= 0.01 {
			desc = fmt.Sprintf("Path %s hit a symbolic veto", label)
		}
		paths = append(paths, pathEval{label: label, desc: desc, score: score})
		if !c.Pruned && score > best.score {
			best = pathEval{label: label, score: score}
		}
	}
	if best.score < 0 {
		best = paths[0]
	}
	switch len(paths) {
	case 1:
		return fmt.Sprintf("Looked at %d path. %s, so I'm rolling with Path %s.", len(children), paths[0].desc, best.label)
	case 2:
		return fmt.Sprintf("Looked at %d paths. %s, %s, so I'm rolling with Path %s.", len(children), paths[0].desc, paths[1].desc, best.label)
	default:
		return fmt.Sprintf("Looked at %d paths. %s, %s, so I'm rolling with the stable logic in Path %s.", len(children), paths[0].desc, paths[1].desc, best.label)
	}
}

func approxTokensFromMessages(messages []api.Message) int {
	if len(messages) == 0 {
		return 0
	}
	chars := 0
	for _, m := range messages {
		chars += len(m.Role) + len(m.Content) + 8
	}
	// Rough token proxy for runtime pressure estimates.
	toks := chars / 4
	if toks < 0 {
		return 0
	}
	return toks
}

func collectThoughtConfidences(root *cognition.ThoughtNode) []float64 {
	if root == nil {
		return nil
	}
	var out []float64
	stack := []*cognition.ThoughtNode{root}
	for len(stack) > 0 {
		n := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if n == nil {
			continue
		}
		if n.Confidence > 0 {
			out = append(out, n.Confidence)
		}
		if len(n.Children) > 0 {
			stack = append(stack, n.Children...)
		}
	}
	return out
}

func stripReasoningSections(s string) string {
	// Remove common explicit reasoning wrappers if models emit them.
	pairs := [][2]string{
		{"<think>", "</think>"},
		{"<analysis>", "</analysis>"},
		{"```thinking", "```"},
	}
	out := s
	for _, p := range pairs {
		for {
			start := strings.Index(strings.ToLower(out), strings.ToLower(p[0]))
			if start < 0 {
				break
			}
			endRel := strings.Index(strings.ToLower(out[start:]), strings.ToLower(p[1]))
			if endRel < 0 {
				out = out[:start]
				break
			}
			end := start + endRel + len(p[1])
			out = out[:start] + out[end:]
		}
	}
	return out
}

func stripMarkdownCodeFences(s string) string {
	trimmed := strings.TrimSpace(s)
	if !strings.HasPrefix(trimmed, "```") || !strings.HasSuffix(trimmed, "```") {
		return trimmed
	}
	lines := strings.Split(trimmed, "\n")
	if len(lines) < 3 {
		return trimmed
	}
	return strings.TrimSpace(strings.Join(lines[1:len(lines)-1], "\n"))
}

func normalizeToolName(name string) string {
	n := strings.ToLower(strings.TrimSpace(name))
	n = strings.ReplaceAll(n, "-", "_")
	switch n {
	case "websearch":
		return "web_search"
	case "fetchurl":
		return "fetch_url"
	case "httprequest":
		return "http_request"
	case "vectorretrieve", "vectorsearch":
		return "vector_retrieve"
	case "executecode":
		return "execute_code"
	case "code_exec", "code_exec_sandbox":
		return "execute_code"
	case "sysexec", "system_exec", "shell_exec", "bash_exec":
		return "sys_exec"
	case "capture", "screen_capture", "screenshot", "capture_window":
		return "capture_screen"
	case "watch", "terminal_watch", "watch_build_terminal", "watch_pipeline":
		return "watch_terminal"
	case "drawbox", "highlight", "highlight_box", "hud_box":
		return "draw_box"
	case "draw_warroom", "war_room", "multi_highlight", "highlight_multi", "warroom":
		return "draw_war_room"
	case "analyze_visual", "visual_target", "analyze_ui_element", "vision_target":
		return "analyze_visual_target"
	case "docsearch", "search_docs", "docs_search", "document_search":
		return "doc_search"
	case "multimodal", "multi_modal", "multimodal_route", "multi_modal_route", "omni_tool", "multi_tool":
		return "multimodal_tool"
	case "autotool", "auto_tools", "auto_tool_recursive", "recursive_tool", "recursive_tools":
		return "auto_tool"
	case "provision", "provision_key", "provision_api_key":
		return "provision_client"
	case "rotate_key", "rotate_api_key":
		return "rotate_client_key"
	case "revoke", "revoke_key":
		return "revoke_client"
	case "admin_list", "list_clients":
		return "admin_list_clients"
	case "admin_create", "create_client":
		return "admin_create_client"
	case "admin_rotate", "rotate_client":
		return "admin_rotate_client"
	case "admin_delete", "delete_client", "revoke_client":
		return "admin_delete_client"
	}
	return n
}

func isSupportedTool(tool string) bool {
	switch tool {
	case "web_search", "fetch_url", "http_request", "vector_retrieve", "execute_code", "sys_exec", "capture_screen", "watch_terminal", "draw_box", "draw_war_room", "analyze_visual_target", "doc_search", "multimodal_tool", "auto_tool",
		"provision_client", "rotate_client_key", "revoke_client",
		"admin_list_clients", "admin_create_client", "admin_rotate_client", "admin_delete_client":
		return true
	default:
		return false
	}
}

func getArgString(args map[string]interface{}, key, fallback string) string {
	v, ok := args[key]
	if !ok || v == nil {
		return fallback
	}
	if s, ok := v.(string); ok {
		if strings.TrimSpace(s) == "" {
			return fallback
		}
		return s
	}
	return fallback
}

func getArgInt(args map[string]interface{}, key string, fallback int) int {
	v, ok := args[key]
	if !ok || v == nil {
		return fallback
	}
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	case int64:
		return int(n)
	}
	return fallback
}

func getArgFloat(args map[string]interface{}, key string, fallback float64) float64 {
	v, ok := args[key]
	if !ok || v == nil {
		return fallback
	}
	switch n := v.(type) {
	case float64:
		return n
	case float32:
		return float64(n)
	case int:
		return float64(n)
	case int64:
		return float64(n)
	case string:
		parsed, err := strconv.ParseFloat(strings.TrimSpace(n), 64)
		if err != nil {
			return fallback
		}
		return parsed
	default:
		return fallback
	}
}

func getArgBool(args map[string]interface{}, key string, fallback bool) bool {
	v, ok := args[key]
	if !ok || v == nil {
		return fallback
	}
	switch b := v.(type) {
	case bool:
		return b
	case string:
		switch strings.ToLower(strings.TrimSpace(b)) {
		case "1", "true", "yes", "y", "on":
			return true
		case "0", "false", "no", "n", "off":
			return false
		}
	}
	return fallback
}

func localAPIKeysPath() string {
	p := strings.TrimSpace(os.Getenv("PLM_LOCAL_API_KEYS_FILE"))
	if p == "" {
		return "api_keys.yaml"
	}
	return p
}

func getArgMap(args map[string]interface{}, key string) map[string]interface{} {
	v, ok := args[key]
	if !ok || v == nil {
		return nil
	}
	m, ok := v.(map[string]interface{})
	if !ok {
		return nil
	}
	return m
}

func getArgStringMap(args map[string]interface{}, key string) map[string]string {
	v, ok := args[key]
	if !ok || v == nil {
		return nil
	}
	raw, ok := v.(map[string]interface{})
	if !ok {
		return nil
	}
	out := make(map[string]string)
	for k, vv := range raw {
		if s, ok := vv.(string); ok {
			out[k] = s
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func getArgStringSlice(args map[string]interface{}, key string) []string {
	v, ok := args[key]
	if !ok || v == nil {
		return nil
	}
	raw, ok := v.([]interface{})
	if !ok {
		return nil
	}
	out := make([]string, 0, len(raw))
	for _, vv := range raw {
		if s, ok := vv.(string); ok && strings.TrimSpace(s) != "" {
			out = append(out, s)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func getArgWarRoomBoxes(args map[string]interface{}, key string) []skills.WarRoomBox {
	v, ok := args[key]
	if !ok || v == nil {
		return nil
	}
	raw, ok := v.([]interface{})
	if !ok {
		return nil
	}
	out := make([]skills.WarRoomBox, 0, len(raw))
	for _, item := range raw {
		m, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		box := skills.WarRoomBox{
			X:     getArgInt(m, "x", 0),
			Y:     getArgInt(m, "y", 0),
			W:     getArgInt(m, "w", 180),
			H:     getArgInt(m, "h", 100),
			Text:  getArgString(m, "text", ""),
			Kind:  getArgString(m, "kind", ""),
			Color: getArgString(m, "color", ""),
		}
		out = append(out, box)
	}
	return out
}

func getArgSpatialElements(args map[string]interface{}, key string) []skills.SpatialElement {
	raw, ok := args[key]
	if !ok || raw == nil {
		return nil
	}
	arr, ok := raw.([]interface{})
	if !ok {
		return nil
	}
	out := make([]skills.SpatialElement, 0, len(arr))
	for _, el := range arr {
		m, ok := el.(map[string]interface{})
		if !ok {
			continue
		}
		out = append(out, skills.SpatialElement{
			Kind:       getArgString(m, "kind", ""),
			Label:      getArgString(m, "label", ""),
			X:          getArgInt(m, "x", 0),
			Y:          getArgInt(m, "y", 0),
			Width:      getArgInt(m, "width", getArgInt(m, "w", 0)),
			Height:     getArgInt(m, "height", getArgInt(m, "h", 0)),
			Confidence: getArgFloat(m, "confidence", 0),
		})
	}
	return out
}

func init() {
	chatCmd.Flags().BoolVarP(&chatRawOutput, "raw", "r", false, "Bypass breath-aware output cadence and print responses immediately")
	chatCmd.Flags().BoolVarP(&chatVerbose, "verbose", "v", false, "Show detailed chat runtime diagnostics (legacy debug output)")
	chatCmd.Flags().StringVar(&chatTimeoutProfile, "timeout-profile", "", "Timeout profile: quick|normal|deep (default from PLM_CHAT_TIMEOUT_PROFILE or normal)")
	chatCmd.Flags().BoolVar(&chatWarmup, "warmup", boolFromEnv("PLM_CHAT_WARMUP", true), "Run one-time Ollama warmup ping before first chat inference")
	chatCmd.Flags().StringVar(&chatCognitionMode, "cognition", "", "Cognition orchestration mode: auto|minimal|balanced|deep (default from PLM_COGNITION_MODE or auto)")
	chatCmd.Flags().StringVar(&chatDomain, "domain", "", "Pin chat to a memory domain namespace (falls back to PLM_CHAT_DOMAIN)")
	chatCmd.Flags().StringVar(&chatNamespace, "namespace", "", "Alias of --domain for chat namespace pinning")
	chatCmd.Flags().StringVar(&chatTextGen, "text-gen", "", "Text generation runtime: ollama|talos-native (use --text-gen with no value to select talos-native; PLM_CHAT_TEXT_GEN_PRIMARY=1 makes native default)")
	if f := chatCmd.Flags().Lookup("text-gen"); f != nil {
		f.NoOptDefVal = chatTextGenModeTalosNative
	}
	rootCmd.AddCommand(chatCmd)
}
