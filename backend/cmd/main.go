package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"
	"github.com/user/azimuthal-belt/backend/internal/ai"
	"github.com/user/azimuthal-belt/backend/internal/db"
	"github.com/user/azimuthal-belt/backend/internal/learner"
	"github.com/user/azimuthal-belt/backend/internal/orchestrator"
	"github.com/user/azimuthal-belt/backend/internal/scanner"
	"github.com/user/azimuthal-belt/backend/internal/scraper"
	"github.com/user/azimuthal-belt/cognition/router"
	"github.com/user/azimuthal-belt/cognition/toolflow"
)

type ScanRequest struct {
	Path     string `json:"path"`
	EnableAI bool   `json:"enable_ai"`
}

type FindingWithAI struct {
	scanner.Finding
	AIAnalysis *ai.AIAnalysisResult `json:"ai_analysis,omitempty"`
	TaintTrace string               `json:"taint_trace,omitempty"`
	Verified   bool                 `json:"verified"`
}

type ScanResponse struct {
	Success  bool            `json:"success"`
	Findings []FindingWithAI `json:"findings"`
	Count    int             `json:"count"`
	Error    string          `json:"error,omitempty"`
}

type LearnRequest struct {
	URL   string `json:"url,omitempty"`
	Topic string `json:"topic,omitempty"`
}

func main() {
	r := chi.NewRouter()

	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	r.Use(cors.Handler(cors.Options{
		AllowedOrigins:   []string{"https://*", "http://*"},
		AllowedMethods:   []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
		AllowedHeaders:   []string{"Accept", "Authorization", "Content-Type", "X-CSRF-Token"},
		ExposedHeaders:   []string{"Link"},
		AllowCredentials: true,
		MaxAge:           300,
	}))

	s := scanner.NewScanner()

	// AI Client Initialization
	aiBaseURL := os.Getenv("AI_BASE_URL")
	if aiBaseURL == "" {
		aiBaseURL = "http://localhost:8089"
	}

	aiKey := os.Getenv("AI_API_KEY")
	toolKey := os.Getenv("GLM_API_KEY")
	clientID := os.Getenv("GLM_CLIENT_ID")

	cogRouter, err := router.NewRouter()
	if err != nil {
		log.Printf("Warning: Failed to initialize cognitive router: %v", err)
	}

	aiClient := ai.NewAIClient(
		aiBaseURL,
		aiKey,
		toolKey,
		clientID,
		cogRouter,
	)

	// Pocketbase Client Initialization
	pbURL := os.Getenv("POCKETBASE_URL")
	if pbURL == "" {
		pbURL = "https://pocketbase.thynaptic.com"
	}

	pbClient := db.NewPocketbaseClient(
		pbURL,
		os.Getenv("POCKETBASE_IDENTITY"),
		os.Getenv("POCKETBASE_PASSWORD"),
		os.Getenv("POCKETBASE_AUTH_COLLECTION"), // e.g., service_accounts
	)

	// Authenticate with Pocketbase if credentials are provided
	if pbClient.Identity != "" && pbClient.Password != "" {
		if pbClient.Collection == "" {
			pbClient.Collection = "service_accounts"
		}
		if err := pbClient.Authenticate(); err != nil {
			log.Printf("Warning: Pocketbase authentication failed: %v", err)
		} else {
			log.Println("Successfully authenticated with Pocketbase")
		}
	}

	learnerStore, err := learner.NewStore("./data")
	if err != nil {
		log.Printf("Warning: Failed to init learner store: %v", err)
	}

	curriculumStore, err := learner.NewCurriculumStore("./data")
	if err != nil {
		log.Printf("Warning: Failed to init curriculum store: %v", err)
	} else {
		// Seed default syllabus for demonstrations
		_ = curriculumStore.SaveLesson(learner.CurriculumLesson{
			ID: "cwe_89_baseline_1", Topic: "SQL Injection", Type: learner.LessonTypeBaseline,
			Content: "Always try terminating the string sequence and appending a truthy statement: `' OR 1=1 --`",
		})
		_ = curriculumStore.SaveLesson(learner.CurriculumLesson{
			ID: "cwe_89_clean_1", Topic: "SQL Injection", Type: learner.LessonTypeClean,
			Content: "Never concatenate strings. Use prepared statements like `db.QueryRow(\"SELECT * FROM users WHERE username = ?\", u)`",
		})
		_ = curriculumStore.SaveLesson(learner.CurriculumLesson{
			ID: "cwe_285_edge_1", Topic: "Broken Object Level Authorization", Type: learner.LessonTypeEdge,
			Content: "If the API uses sequential IDs, try enumerating the ID parameter: `/api/users/1` -> `/api/users/2`. If using UUIDs, look for leaked UUIDs in other API responses.",
		})
	}

	indexer := scanner.NewIndexer(aiClient, pbClient, learnerStore)
	taintAnalyzer := scanner.NewTaintAnalyzer(pbClient)

	// Initialize Execution Toolflow for Agents
	toolRegistry := toolflow.NewRegistry()
	if err := toolRegistry.Register("patch_file", scanner.ApplyPatch); err != nil {
		log.Printf("Warning: Failed to register patch_file tool: %v", err)
	}
	if err := toolRegistry.Register("recon_subagent", scanner.ReconNode(aiClient)); err != nil {
		log.Printf("Warning: Failed to register recon_subagent tool: %v", err)
	}
	if err := toolRegistry.Register("payload_subagent", scanner.PayloadNode(aiClient)); err != nil {
		log.Printf("Warning: Failed to register payload_subagent tool: %v", err)
	}

	if err := toolRegistry.Register("verdict_subagent", scanner.VerdictNode(aiClient)); err != nil {
		log.Printf("Warning: Failed to register verdict_subagent tool: %v", err)
	}

	if err := toolRegistry.Register("auditor_subagent", scanner.AuditorNode(aiClient)); err != nil {
		log.Printf("Warning: Failed to register auditor_subagent tool: %v", err)
	}
	if err := toolRegistry.Register("web_probe", scanner.ExecuteProbe); err != nil {
		log.Printf("Warning: Failed to register web_probe tool: %v", err)
	}

	missionController := orchestrator.NewMissionController(pbClient, aiClient, toolRegistry, learnerStore, curriculumStore)

	// Initialize Scrapers
	scraper.StartH1ScraperCron()
	scraper.StartCWEScraperCron()

	r.Post("/api/index", func(w http.ResponseWriter, r *http.Request) {
		var req ScanRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		if req.Path == "" {
			http.Error(w, "path is required", http.StatusBadRequest)
			return
		}

		go func() {
			log.Printf("Starting indexing for path: %s", req.Path)
			if err := indexer.IndexDirectory(req.Path); err != nil {
				log.Printf("Indexing failed: %v", err)
			} else {
				log.Printf("Indexing completed for path: %s", req.Path)
			}
		}()

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": true,
			"message": "Indexing started in background",
		})
	})

	r.Post("/api/learn", func(w http.ResponseWriter, r *http.Request) {
		var req LearnRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		if req.URL == "" && req.Topic == "" {
			http.Error(w, "url or topic is required", http.StatusBadRequest)
			return
		}

		go func() {
			if req.URL != "" {
				log.Printf("API: Starting learning from URL: %s", req.URL)
				if err := indexer.LearnURL(req.URL); err != nil {
					log.Printf("API: LearnURL failed: %v", err)
				} else {
					log.Printf("API: Learning completed for URL: %s", req.URL)
				}
			}
			if req.Topic != "" {
				log.Printf("API: Starting learning from topic: %s", req.Topic)
				if err := indexer.LearnTopic(req.Topic); err != nil {
					log.Printf("API: LearnTopic failed: %v", err)
				} else {
					log.Printf("API: Learning completed for topic: %s", req.Topic)
				}
			}
		}()

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": true,
			"message": "Learning started in background",
		})
	})

	r.Post("/api/crawl", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			URL         string                 `json:"url"`
			MaxPages    int                    `json:"max_pages"`
			UseHeadless bool                   `json:"use_headless"`
			Headers     map[string]interface{} `json:"headers"`
			Cookies     map[string]interface{} `json:"cookies"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		if req.URL == "" {
			http.Error(w, "url is required", http.StatusBadRequest)
			return
		}
		if req.MaxPages <= 0 {
			req.MaxPages = 10 // default
		}

		go func() {
			log.Printf("API: Starting domain crawl from URL: %s (Max: %d, Headless: %v)", req.URL, req.MaxPages, req.UseHeadless)

			var err error
			if req.UseHeadless {
				err = indexer.CrawlWithRod(req.URL, req.MaxPages, req.Headers, req.Cookies)
			} else {
				err = indexer.CrawlAndLearn(req.URL, req.MaxPages, req.Headers, req.Cookies)
			}

			if err != nil {
				log.Printf("API: Crawl failed: %v", err)
			} else {
				log.Printf("API: Crawling completed for URL: %s", req.URL)
			}
		}()

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": true,
			"message": "Domain crawling started in background",
		})
	})

	r.Post("/api/scan", func(w http.ResponseWriter, r *http.Request) {
		var req ScanRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		if req.Path == "" {
			http.Error(w, "path is required", http.StatusBadRequest)
			return
		}

		findings, err := s.Scan(req.Path)
		if err != nil {
			resp := ScanResponse{
				Success: false,
				Error:   err.Error(),
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(resp)
			return
		}

		var shards []db.CogShard
		if pbClient.BaseURL != "" && pbClient.Token != "" {
			shards, _ = pbClient.ListShards()
		}

		var findingsWithAI []FindingWithAI
		for _, f := range findings {
			fwai := FindingWithAI{Finding: f}

			// Taint Analysis Verification
			if len(shards) > 0 {
				verified, trace := taintAnalyzer.TraceTaint(f, shards)
				fwai.Verified = verified
				fwai.TaintTrace = trace
			}

			// AI Enhancement
			if req.EnableAI && aiClient.BaseURL != "" {
				// Retrieve context from codebase and past results
				contextQuery := fmt.Sprintf("%s %s %s", f.VulnType, f.Snippet, f.Description)
				ctx, err := aiClient.RetrieveContext(contextQuery)
				if err != nil {
					log.Printf("Warning: Retrieval failed: %v", err)
				}

				if learnerStore != nil {
					feedbacks := learnerStore.GetRelevantFeedback("AnalyzeFinding", "")
					for _, fb := range feedbacks {
						ctx = append(ctx, fmt.Sprintf("[AGENT_FEEDBACK] Failed Assumption: %s | Corrected Logic: %s", fb.FailedPath, fb.CorrectedPath))
					}
				}

				analysis, err := aiClient.AnalyzeFinding(f.File, f.VulnType, f.Snippet, f.Description, ctx)
				if err != nil {
					log.Printf("Scan: AI Analysis failed for %s:%d: %v", f.File, f.Line, err)
					fwai.AIAnalysis = nil
				} else {
					fwai.AIAnalysis = analysis

					// Final Agentic Consensus Phase (Phase 15)
					var consensusFeedback []string
					if learnerStore != nil {
						cb := learnerStore.GetRelevantFeedback("ConsensusVerify", "")
						for _, f := range cb {
							consensusFeedback = append(consensusFeedback, fmt.Sprintf("- Failed Assumption: %s | Corrected Logic: %s", f.FailedPath, f.CorrectedPath))
						}
					}

					verified, judgeVerdict, cerr := aiClient.ConsensusVerify(f.File, f.VulnType, f.Snippet, analysis.TechnicalAnalysis, strings.Join(ctx, "\n"), consensusFeedback)
					if cerr == nil {
						fwai.Verified = verified
						if fwai.AIAnalysis != nil {
							fwai.AIAnalysis.TechnicalAnalysis = fmt.Sprintf("%s\n\n--- CONSENSUS VERDICT ---\n%s", fwai.AIAnalysis.TechnicalAnalysis, judgeVerdict)
						}
					} else {
						log.Printf("Scan: Consensus Verify failed for %s:%d: %v", f.File, f.Line, cerr)
					}

					// Save analysis to Pocketbase memory_nodes
					if pbClient.BaseURL != "" && pbClient.Token != "" {
						analysisBytes, _ := json.Marshal(analysis)
						_ = pbClient.SaveMemory(db.MemoryNode{
							Key:     fmt.Sprintf("finding_%s_%d", f.ID, f.Line),
							Content: string(analysisBytes),
							Metadata: map[string]interface{}{
								"file":      f.File,
								"line":      f.Line,
								"vuln_type": f.VulnType,
							},
						})
					}
				}
			}

			// Pocketbase Persistence (Finding record)
			if pbClient.BaseURL != "" && pbClient.Token != "" {
				_ = pbClient.SaveFinding("findings", fwai)
			}

			findingsWithAI = append(findingsWithAI, fwai)
		}

		resp := ScanResponse{
			Success:  true,
			Findings: findingsWithAI,
			Count:    len(findingsWithAI),
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	})

	r.Post("/api/feedback", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Hash     string `json:"hash"`
			Feedback string `json:"feedback"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if req.Hash == "" || req.Feedback == "" {
			http.Error(w, "hash and feedback are required", http.StatusBadRequest)
			return
		}

		shard, id, err := pbClient.GetShardByHash(req.Hash)
		if err != nil {
			http.Error(w, "failed to fetch shard: "+err.Error(), http.StatusNotFound)
			return
		}

		oldExp, _ := shard.Metadata["explanation_v1"].(string)
		code := ""
		parts := strings.SplitN(shard.Content, "\n\nCode:\n", 2)
		if len(parts) > 1 {
			code = parts[1]
		}

		var refineFeedback []string
		if learnerStore != nil {
			fb := learnerStore.GetRelevantFeedback("RefineExplanation", "")
			for _, f := range fb {
				refineFeedback = append(refineFeedback, fmt.Sprintf("- Failed Assumption: %s | Corrected Logic: %s", f.FailedPath, f.CorrectedPath))
			}
		}

		refined, err := aiClient.RefineExplanation(oldExp, code, req.Feedback, refineFeedback)
		if err != nil {
			http.Error(w, "failed to refine explanation: "+err.Error(), http.StatusInternalServerError)
			return
		}

		if shard.Metadata == nil {
			shard.Metadata = make(map[string]interface{})
		}
		shard.Metadata["explanation_v2"] = refined
		if len(parts) > 1 {
			shard.Content = fmt.Sprintf("Explanation:\n%s\n\nCode:\n%s", refined, code)
		}

		if pbClient.BaseURL != "" && pbClient.Token != "" {
			if err := pbClient.UpdateShard(id, *shard); err != nil {
				http.Error(w, "failed to update shard: "+err.Error(), http.StatusInternalServerError)
				return
			}
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": true,
			"message": "Feedback applied and learning updated",
			"refined": refined,
		})
	})

	r.Post("/api/agent-feedback", func(w http.ResponseWriter, r *http.Request) {
		var req learner.AgentFeedback
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if req.FailedPath == "" || req.CorrectedPath == "" {
			http.Error(w, "failed_path and corrected_path are required", http.StatusBadRequest)
			return
		}

		if req.ID == "" {
			req.ID = fmt.Sprintf("fb_%d", time.Now().UnixNano())
		}
		if req.Timestamp == "" {
			req.Timestamp = time.Now().Format(time.RFC3339)
		}
		if req.TaskType == "" {
			req.TaskType = "AnalyzeFinding" // Default
		}

		if learnerStore != nil {
			if err := learnerStore.SaveFeedback(req); err != nil {
				http.Error(w, "Failed to save feedback: "+err.Error(), http.StatusInternalServerError)
				return
			}
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success":  true,
			"message":  "Agent Feedback saved successfully",
			"feedback": req,
		})
	})

	r.Post("/api/patch", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			FilePath        string `json:"file_path"`
			TargetSnippet   string `json:"target_snippet"`
			ReplacementCode string `json:"replacement_code"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		if req.FilePath == "" || req.TargetSnippet == "" || req.ReplacementCode == "" {
			http.Error(w, "file_path, target_snippet, and replacement_code are required", http.StatusBadRequest)
			return
		}

		ctx := r.Context()

		// Create a dynamic plan with a single execution node for the requested patch
		plan := toolflow.Plan{
			Nodes: []toolflow.PlanNode{
				{
					ID:   "patch_node_1",
					Tool: "patch_file",
					Args: map[string]interface{}{
						"file_path":        req.FilePath,
						"target_snippet":   req.TargetSnippet,
						"replacement_code": req.ReplacementCode,
					},
					TimeoutMS: 5000,
				},
			},
			DeterministicHash: fmt.Sprintf("patch_%d", time.Now().UnixNano()),
			OrderedIDs:        []string{"patch_node_1"},
		}

		results, _, err := toolflow.ExecutePlanV2(ctx, plan, toolRegistry, toolflow.ExecOptions{
			MaxWorkers: 1,
			FailClosed: true,
		})

		if err != nil || (len(results) > 0 && results[0].Err != nil) {
			errMsg := ""
			if err != nil {
				errMsg = err.Error()
			} else if len(results) > 0 {
				errMsg = results[0].Err.Error()
			}

			// Auto-Log Failure to Agent Feedback Loop
			if learnerStore != nil {
				_ = learnerStore.SaveFeedback(learner.AgentFeedback{
					ID:            fmt.Sprintf("vuln_patch_%d", time.Now().UnixNano()),
					TaskType:      "AutoPatch",
					FailedPath:    fmt.Sprintf("Attempted to replace '%s' in %s", req.TargetSnippet, req.FilePath),
					CorrectedPath: fmt.Sprintf("Patch failed: %v", errMsg),
					Timestamp:     time.Now().Format(time.RFC3339),
				})
			}

			http.Error(w, "failed to apply patch: "+errMsg, http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": true,
			"message": "Patch applied successfully",
		})
	})

	r.Post("/api/strike", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Target  learner.ProbingTarget  `json:"target"`
			Headers map[string]interface{} `json:"headers"`
			Cookies map[string]interface{} `json:"cookies"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		// Give the full strike sequence up to 3 minutes before cancelling
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Minute)
		defer cancel()
		targetJSON, _ := json.Marshal(req.Target)

		var strikeFeedback []string
		if learnerStore != nil {
			fb := learnerStore.GetRelevantFeedback("WebStrike", req.Target.Action)
			// only fetch up to top 5 to keep context small
			for i, f := range fb {
				if i >= 5 {
					break
				}
				strikeFeedback = append(strikeFeedback, fmt.Sprintf("- Target: %s | Blocked/Failed Payload: %s", f.FailedPath, f.CorrectedPath))
			}
		}

		var errMsg string

		// 1. Run Recon Step
		reconPlan := toolflow.Plan{
			Nodes: []toolflow.PlanNode{
				{
					ID:   "recon_step",
					Tool: "recon_subagent",
					Args: map[string]interface{}{
						"target_json": string(targetJSON),
					},
					TimeoutMS: 60000,
				},
			},
			DeterministicHash: fmt.Sprintf("strike_recon_%d", time.Now().UnixNano()),
			OrderedIDs:        []string{"recon_step"},
		}
		reconRes, _, err := toolflow.ExecutePlanV2(ctx, reconPlan, toolRegistry, toolflow.ExecOptions{
			MaxWorkers: 1,
			FailClosed: true,
		})

		if err != nil {
			errMsg = err.Error()
		} else if len(reconRes) > 0 && reconRes[0].Err != nil {
			errMsg = reconRes[0].Err.Error()
		}

		if errMsg != "" {
			if learnerStore != nil {
				_ = learnerStore.SaveFeedback(learner.AgentFeedback{
					ID:            fmt.Sprintf("recon_err_%d", time.Now().UnixNano()),
					TaskType:      "WebStrike",
					ContextID:     req.Target.Action,
					FailedPath:    "Recon Step Failure",
					CorrectedPath: errMsg,
					Timestamp:     time.Now().Format(time.RFC3339),
				})
			}
			http.Error(w, "Recon failed: "+errMsg, http.StatusInternalServerError)
			return
		}
		reconContext := ""
		if len(reconRes) > 0 {
			reconContext = reconRes[0].Output
		}

		// 2. Run Payload Step
		payloadPlan := toolflow.Plan{
			Nodes: []toolflow.PlanNode{
				{
					ID:   "payload_step",
					Tool: "payload_subagent",
					Args: map[string]interface{}{
						"target_json":    string(targetJSON),
						"agent_feedback": strikeFeedback,
						"recon_context":  reconContext,
					},
					TimeoutMS: 120000,
				},
			},
			DeterministicHash: fmt.Sprintf("strike_payload_%d", time.Now().UnixNano()),
			OrderedIDs:        []string{"payload_step"},
		}
		payloadRes, _, err := toolflow.ExecutePlanV2(ctx, payloadPlan, toolRegistry, toolflow.ExecOptions{
			MaxWorkers: 1,
			FailClosed: true,
		})

		if err != nil {
			errMsg = err.Error()
		} else if len(payloadRes) > 0 && payloadRes[0].Err != nil {
			errMsg = payloadRes[0].Err.Error()
		}
		if errMsg != "" {
			if learnerStore != nil {
				_ = learnerStore.SaveFeedback(learner.AgentFeedback{
					ID:            fmt.Sprintf("payload_err_%d", time.Now().UnixNano()),
					TaskType:      "WebStrike",
					ContextID:     req.Target.Action,
					FailedPath:    "Payload Step Failure",
					CorrectedPath: errMsg,
					Timestamp:     time.Now().Format(time.RFC3339),
				})
			}
			http.Error(w, "Payload generation failed: "+errMsg, http.StatusInternalServerError)
			return
		}

		payloadRawStr := ""
		if len(payloadRes) > 0 {
			payloadRawStr = payloadRes[0].Output
		}

		var generatedPayload struct {
			Method  string `json:"method"`
			Payload string `json:"payload"`
		}
		if err := json.Unmarshal([]byte(payloadRawStr), &generatedPayload); err != nil {
			// fallback handling if AI returns a raw string
			generatedPayload.Payload = payloadRawStr
			generatedPayload.Method = "POST"
		}

		// 3. Run Strike Step and Verdict Step
		strikePlan := toolflow.Plan{
			Nodes: []toolflow.PlanNode{
				{
					ID:   "strike_step",
					Tool: "web_probe",
					Args: map[string]interface{}{
						"url":          req.Target.Action,
						"method":       generatedPayload.Method,
						"payload":      generatedPayload.Payload,
						"content_type": "application/json",
						"headers":      req.Headers,
						"cookies":      req.Cookies,
					},
					TimeoutMS: 15000,
				},
				{
					ID:   "verdict_step",
					Tool: "verdict_subagent",
					Args: map[string]interface{}{
						"payload":       generatedPayload.Payload,
						"strike_output": "{{" + "strike_step.output" + "}}", // Reference previous node's output
					},
					TimeoutMS: 60000,
				},
			},
			DeterministicHash: fmt.Sprintf("strike_exec_%d", time.Now().UnixNano()),
			OrderedIDs:        []string{"strike_step", "verdict_step"},
		}

		results, _, err := toolflow.ExecutePlanV2(ctx, strikePlan, toolRegistry, toolflow.ExecOptions{
			MaxWorkers: 1,
			FailClosed: true,
		})

		if err != nil {
			errMsg = err.Error()
		} else if len(results) > 0 && results[0].Err != nil {
			errMsg = results[0].Err.Error()
		}

		if errMsg != "" {
			// Save Feedback on Failure (e.g., WAF block, 403, network drop)
			if learnerStore != nil {
				_ = learnerStore.SaveFeedback(learner.AgentFeedback{
					ID:            fmt.Sprintf("web_strike_fail_%d", time.Now().UnixNano()),
					TaskType:      "WebStrike",
					ContextID:     req.Target.Action,
					FailedPath:    fmt.Sprintf("Target: %s", req.Target.Action),
					CorrectedPath: fmt.Sprintf("DAG failed: %v", errMsg),
					Timestamp:     time.Now().Format(time.RFC3339),
				})
			}
			http.Error(w, "Strike failed: "+errMsg, http.StatusInternalServerError)
			return
		}

		var output string
		if len(results) > 0 {
			// The final node is the verdict_step, grab its output
			for _, r := range results {
				if r.NodeID == "verdict_step" {
					output = r.Output
				}
			}
		}

		var verdict ai.ExploitVerdict
		if err := json.Unmarshal([]byte(output), &verdict); err == nil {
			if !verdict.IsExploited && learnerStore != nil {
				_ = learnerStore.SaveFeedback(learner.AgentFeedback{
					ID:            fmt.Sprintf("web_strike_feedback_%d", time.Now().UnixNano()),
					TaskType:      "WebStrike",
					ContextID:     req.Target.Action,
					FailedPath:    fmt.Sprintf("Payload used: %s", generatedPayload.Payload),
					CorrectedPath: fmt.Sprintf("Verdict: Not an exploit. Reason: %s", verdict.Reasoning),
					Timestamp:     time.Now().Format(time.RFC3339),
				})
			}
		}

		// Save a Finding in memory to represent the successful test and its output
		// We don't have block-level variables for the payload here because it was generated inside the DAG.
		// We'll log the fact that the DAG succeeded.
		if pbClient.BaseURL != "" && pbClient.Token != "" {
			_ = pbClient.SaveMemory(db.MemoryNode{
				Key:     fmt.Sprintf("strike_success_%d", time.Now().Unix()),
				Content: output,
				Metadata: map[string]interface{}{
					"action": req.Target.Action,
					"type":   "strike_result",
				},
			})
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": true,
			"message": "Strike applied successfully",
			"output":  output,
		})
	})

	r.Get("/api/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	})

	r.Post("/api/missions/start", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			TargetURL         string `json:"target_url"`
			VulnerabilityType string `json:"vulnerability_type"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		if req.TargetURL == "" {
			http.Error(w, "target_url is required", http.StatusBadRequest)
			return
		}

		missionID := fmt.Sprintf("mission_%d", time.Now().UnixNano())
		mission := db.SecurityMission{
			ID:                missionID,
			TargetURL:         req.TargetURL,
			VulnerabilityType: req.VulnerabilityType,
			Status:            "Pending",
			History:           []string{fmt.Sprintf("[%s] Mission created for target: %s", time.Now().Format(time.RFC3339), req.TargetURL)},
		}

		if pbClient.BaseURL != "" && pbClient.Token != "" {
			if err := pbClient.SaveMission(mission); err != nil {
				log.Printf("Warning: Failed to persist mission start: %v", err)
			}
		}

		// Kick off the state machine asynchronously
		go missionController.ExecuteMission(context.Background(), mission)

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success":    true,
			"mission_id": mission.ID,
			"status":     mission.Status,
		})
	})

	r.Get("/api/missions/{id}", func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		if pbClient.BaseURL == "" || pbClient.Token == "" {
			http.Error(w, "Database not configured for mission tracking.", http.StatusServiceUnavailable)
			return
		}

		mission, _, err := pbClient.GetMission(id)
		if err != nil {
			http.Error(w, "Mission not found: "+err.Error(), http.StatusNotFound)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(mission)
	})

	r.Get("/api/report", func(w http.ResponseWriter, r *http.Request) {
		// Give the AI enough time to write a full markdown report
		ctx, cancel := context.WithTimeout(r.Context(), 4*time.Minute)
		defer cancel()

		// 1. Gather context (Mocked for now, in a real app this pulls from DB)
		// We'll simulate having a few successful strikes and intelligence objects.
		strikesJSON := `[{"target": "/login", "vulnerability": "SQL Injection", "payload": "' OR 1=1 --"}]`
		intelJSON := `[{"cwe": "CWE-89", "description": "Improper Neutralization of Special Elements used in an SQL Command"}]`

		// 2. Run Auditor Step
		reportPlan := toolflow.Plan{
			Nodes: []toolflow.PlanNode{
				{
					ID:   "auditor_step",
					Tool: "auditor_subagent",
					Args: map[string]interface{}{
						"strikes_json":      strikesJSON,
						"intelligence_json": intelJSON,
					},
					TimeoutMS: 180000,
				},
			},
			DeterministicHash: fmt.Sprintf("report_exec_%d", time.Now().UnixNano()),
			OrderedIDs:        []string{"auditor_step"},
		}

		results, _, err := toolflow.ExecutePlanV2(ctx, reportPlan, toolRegistry, toolflow.ExecOptions{
			MaxWorkers:   1,
			FailClosed:   true,
			GlobalBudget: 4 * time.Minute,
		})

		if err != nil {
			http.Error(w, "Report generation failed: "+err.Error(), http.StatusInternalServerError)
			return
		}

		var output string
		if len(results) > 0 {
			if results[0].Err != nil {
				http.Error(w, "Auditor failed: "+results[0].Err.Error(), http.StatusInternalServerError)
				return
			}
			output = results[0].Output
		}

		// Return raw markdown text
		w.Header().Set("Content-Type", "text/markdown")
		w.Write([]byte(output))
	})

	r.Get("/api/curriculum", func(w http.ResponseWriter, r *http.Request) {
		topic := r.URL.Query().Get("topic")
		lType := r.URL.Query().Get("type")

		var lessons []learner.CurriculumLesson
		if curriculumStore != nil {
			lessons = curriculumStore.GetLessons(topic, learner.LessonType(lType))
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(lessons)
	})

	r.Post("/api/curriculum", func(w http.ResponseWriter, r *http.Request) {
		var req learner.CurriculumLesson
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		if req.ID == "" {
			req.ID = fmt.Sprintf("lesson_%d", time.Now().UnixNano())
		}

		if curriculumStore != nil {
			_ = curriculumStore.SaveLesson(req)
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": true,
			"lesson":  req,
		})
	})

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	log.Printf("Server starting on port %s", port)
	if err := http.ListenAndServe(":"+port, r); err != nil {
		log.Fatal(err)
	}
}
