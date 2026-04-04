package orchestrator

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/user/azimuthal-belt/backend/internal/ai"
	"github.com/user/azimuthal-belt/backend/internal/db"
	"github.com/user/azimuthal-belt/backend/internal/learner"
	"github.com/user/azimuthal-belt/cognition/toolflow"
)

type MissionController struct {
	PBClient        *db.PocketbaseClient
	AIClient        *ai.AIClient
	ToolRegistry    *toolflow.Registry
	LearnerStore    *learner.Store
	CurriculumStore *learner.CurriculumStore
}

func NewMissionController(pb *db.PocketbaseClient, aiClient *ai.AIClient, reg *toolflow.Registry, store *learner.Store, curriculum *learner.CurriculumStore) *MissionController {
	return &MissionController{
		PBClient:        pb,
		AIClient:        aiClient,
		ToolRegistry:    reg,
		LearnerStore:    store,
		CurriculumStore: curriculum,
	}
}

// ExecuteMission runs the state machine: Pending -> Exploited -> Patched -> Resolved
func (mc *MissionController) ExecuteMission(ctx context.Context, mission db.SecurityMission) {
	log.Printf("[Mission:%s] Starting mission for target: %s", mission.ID, mission.TargetURL)

	updateStatus := func(status string, note string) {
		mission.Status = status
		mission.History = append(mission.History, fmt.Sprintf("[%s] %s: %s", time.Now().Format(time.RFC3339), status, note))
		if mc.PBClient != nil {
			_ = mc.PBClient.UpdateMission(mission.ID, mission)
		}
		log.Printf("[Mission:%s] %s -> %s", mission.ID, status, note)
	}

	target := learner.ProbingTarget{
		Action: mission.TargetURL,
		Method: "POST", // Default, could be refined
		Type:   "api_target",
	}
	targetJSON, _ := json.Marshal(target)

	// --- PHASE 1: RECON & PAYLOAD GENERATION ---
	reconCtx, payloadStr, err := mc.generatePayload(ctx, string(targetJSON), mission.TargetURL)
	if err != nil {
		updateStatus("Failed", fmt.Sprintf("Generation Failed: %v", err))
		return
	}

	// --- PHASE 2: STRIKE & VERDICT ---
	verdict, _, err := mc.executeStrike(ctx, target.Action, payloadStr)
	if err != nil {
		updateStatus("Failed", fmt.Sprintf("Strike Execution Error: %v", err))
		return
	}

	if verdict == nil || !verdict.IsExploited {
		updateStatus("Closed-Safe", "Initial strike failed to exploit. Target appears secure against this avenue.")
		return
	}

	updateStatus("Exploited", fmt.Sprintf("Verified vulnerability via payload: %s", payloadStr))

	// --- PHASE 3: REMEDIATION (PATCH) ---
	// To patch, we need the AI to locate the vulnerability in source code based on the strike context
	patchResult, err := mc.executePatch(ctx, target.Action, payloadStr, reconCtx)
	if err != nil {
		updateStatus("Patch-Failed", fmt.Sprintf("Could not auto-patch: %v", err))
		return
	}

	updateStatus("Patched", fmt.Sprintf("Applied auto-patch: %s", patchResult))

	// --- PHASE 4: VERIFICATION (THE PROOF) ---
	// Re-run the EXACT SAME payload against the newly patched endpoint
	log.Printf("[Mission:%s] Running Proof-of-Resolution Strike...", mission.ID)
	proofVerdict, _, err := mc.executeStrike(ctx, target.Action, payloadStr)
	if err != nil {
		updateStatus("Verify-Failed", fmt.Sprintf("Proof Strike Error: %v", err))
		return
	}

	if proofVerdict != nil && proofVerdict.IsExploited {
		updateStatus("Verification-Failed", "Proof Strike succeeded. The applied patch did NOT fix the vulnerability.")
		return
	}

	// If the exploit failed, the mission is officially resolved.
	updateStatus("Resolved", "Proof Strike confirmed the vulnerability is neutralized. Mission accomplished.")
}

func (mc *MissionController) generatePayload(ctx context.Context, targetJSON string, targetURL string) (string, string, error) {
	reconPlan := toolflow.Plan{
		Nodes: []toolflow.PlanNode{
			{
				ID:        "recon_step",
				Tool:      "recon_subagent",
				Args:      map[string]interface{}{"target_json": targetJSON},
				TimeoutMS: 60000,
			},
		},
		DeterministicHash: fmt.Sprintf("mission_recon_%d", time.Now().UnixNano()),
		OrderedIDs:        []string{"recon_step"},
	}
	reconRes, _, err := toolflow.ExecutePlanV2(ctx, reconPlan, mc.ToolRegistry, toolflow.ExecOptions{
		MaxWorkers:   1,
		FailClosed:   true,
		GlobalBudget: 4 * time.Minute,
	})
	if err != nil || (len(reconRes) > 0 && reconRes[0].Err != nil) {
		return "", "", fmt.Errorf("recon failed: %v", err)
	}
	reconContext := ""
	if len(reconRes) > 0 {
		reconContext = reconRes[0].Output
	}

	var strikeFeedback []string
	if mc.LearnerStore != nil {
		fb := mc.LearnerStore.GetRelevantFeedback("WebStrike", targetURL)
		for i, f := range fb {
			if i >= 5 {
				break
			}
			strikeFeedback = append(strikeFeedback, fmt.Sprintf("- Target: %s | Blocked/Failed Payload: %s", f.FailedPath, f.CorrectedPath))
		}
	}

	var syllabus []string
	if mc.CurriculumStore != nil {
		// Mock logic: extract topic from recon context (ideally AI extracts this directly)
		// For the PoC, we just fetch all baseline and edge lessons for demonstration
		baselines := mc.CurriculumStore.GetLessons("", learner.LessonTypeBaseline)
		edges := mc.CurriculumStore.GetLessons("", learner.LessonTypeEdge)

		for _, l := range baselines {
			syllabus = append(syllabus, fmt.Sprintf("[BASELINE EXPLOIT] Topic: %s - %s", l.Topic, l.Content))
		}
		for _, l := range edges {
			syllabus = append(syllabus, fmt.Sprintf("[EDGE SCENARIO] Topic: %s - %s", l.Topic, l.Content))
		}
	}

	payloadArgs := map[string]interface{}{
		"target_json":    targetJSON,
		"agent_feedback": strikeFeedback,
		"recon_context":  reconContext,
	}

	if len(syllabus) > 0 {
		payloadArgs["curriculum_syllabus"] = syllabus
	}

	payloadPlan := toolflow.Plan{
		Nodes: []toolflow.PlanNode{
			{
				ID:        "payload_step",
				Tool:      "payload_subagent",
				Args:      payloadArgs,
				TimeoutMS: 120000,
			},
		},
		DeterministicHash: fmt.Sprintf("mission_payload_%d", time.Now().UnixNano()),
		OrderedIDs:        []string{"payload_step"},
	}
	payloadRes, _, err := toolflow.ExecutePlanV2(ctx, payloadPlan, mc.ToolRegistry, toolflow.ExecOptions{
		MaxWorkers:   1,
		FailClosed:   true,
		GlobalBudget: 5 * time.Minute,
	})
	if err != nil || (len(payloadRes) > 0 && payloadRes[0].Err != nil) {
		return "", "", fmt.Errorf("payload generation failed: %v", err)
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
		generatedPayload.Payload = payloadRawStr
		generatedPayload.Method = "POST"
	}

	return reconContext, generatedPayload.Payload, nil
}

func (mc *MissionController) executeStrike(ctx context.Context, action string, payloadStr string) (*ai.ExploitVerdict, string, error) {
	strikePlan := toolflow.Plan{
		Nodes: []toolflow.PlanNode{
			{
				ID:   "strike_step",
				Tool: "web_probe",
				Args: map[string]interface{}{
					"url":          action,
					"method":       "POST", // simplified for mission orchestrator
					"payload":      payloadStr,
					"content_type": "application/json",
					"headers":      map[string]interface{}{},
					"cookies":      map[string]interface{}{},
				},
				TimeoutMS: 15000,
			},
			{
				ID:   "verdict_step",
				Tool: "verdict_subagent",
				Args: map[string]interface{}{
					"payload":       payloadStr,
					"strike_output": "{{" + "strike_step.output" + "}}",
				},
				TimeoutMS: 60000,
			},
		},
		DeterministicHash: fmt.Sprintf("mission_strike_%d", time.Now().UnixNano()),
		OrderedIDs:        []string{"strike_step", "verdict_step"},
	}

	results, _, err := toolflow.ExecutePlanV2(ctx, strikePlan, mc.ToolRegistry, toolflow.ExecOptions{MaxWorkers: 1, FailClosed: true, GlobalBudget: 3 * time.Minute})
	if err != nil {
		return nil, "", err
	}

	var output, strikeResponse string
	for _, r := range results {
		if r.NodeID == "verdict_step" {
			output = r.Output
		}
		if r.NodeID == "strike_step" {
			strikeResponse = r.Output
		}
	}

	var verdict ai.ExploitVerdict
	if err := json.Unmarshal([]byte(output), &verdict); err != nil {
		return nil, "", fmt.Errorf("failed to parse verdict json: %v", err)
	}

	return &verdict, strikeResponse, nil
}

// executePatch needs to find the local file mapping to the endpoint and apply a fix
func (mc *MissionController) executePatch(ctx context.Context, action string, payloadStr string, reconContext string) (string, error) {
	// For simplicity in the PoC orchestrator, we'll assume a dummy logic path.
	// In a full implementation, you'd map the targetURL (e.g. '/api/users') to a local file (e.g. 'handlers/users.go') via an AST or routing table.
	log.Printf("[Mission] Requesting AI patch strategy for: %s", action)

	var remedySyllabus []string
	if mc.CurriculumStore != nil {
		cleans := mc.CurriculumStore.GetLessons("", learner.LessonTypeClean)
		failures := mc.CurriculumStore.GetLessons("", learner.LessonTypeFailure)

		for _, l := range cleans {
			remedySyllabus = append(remedySyllabus, fmt.Sprintf("[CLEAN PATTERN] Topic: %s - %s", l.Topic, l.Content))
		}
		for _, l := range failures {
			remedySyllabus = append(remedySyllabus, fmt.Sprintf("[KNOWN FAILURE] Topic: %s - CAUTION: %s", l.Topic, l.Content))
		}
	}

	if len(remedySyllabus) > 0 {
		log.Printf("[Mission] Supplying %d curriculum lessons to Auto-Patcher", len(remedySyllabus))
	}

	// In a real scenario, we would use an AI client method here to read the source file,
	// locate the exact lines causing the vulnerability, generate the replacement code
	// (using the remedySyllabus as grounding context), and then call toolflow "patch_node_1".

	// Since we are mocking the filesystem routing for this phase:
	return "Simulated AI Patch Applied to local routing handler.", nil
}
