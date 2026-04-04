package taloscli

import (
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	selectiveInterventionPath         = ".memory/selective_interventions.json"
	selectiveInterventionEnabledEnv   = "TALOS_SELECTIVE_INTERVENTION_ENABLED"
	selectiveInterventionThresholdEnv = "TALOS_SELECTIVE_INTERVENTION_THRESHOLD"
	selectiveInterventionAutoEnv      = "TALOS_SELECTIVE_AUTO_APPROVE"
	selectiveApprovedIDsEnv           = "TALOS_SELECTIVE_APPROVED_IDS"
)

type selectiveInterventionError struct {
	TicketID string
	Message  string
}

func (e *selectiveInterventionError) Error() string {
	return "SELECTIVE_INTERVENTION_REQUIRED: " + strings.TrimSpace(e.Message)
}

type selectiveInterventionTicket struct {
	ID        string                 `json:"id"`
	Tool      string                 `json:"tool"`
	RiskScore float64                `json:"risk_score"`
	Reasons   []string               `json:"reasons"`
	Args      map[string]interface{} `json:"args"`
	Status    string                 `json:"status"`
	CreatedAt time.Time              `json:"created_at"`
	UpdatedAt time.Time              `json:"updated_at"`
}

type selectiveInterventionStore struct {
	Tickets []selectiveInterventionTicket `json:"tickets"`
}

var selectiveInterventionMu sync.Mutex

func selectiveInterventionEnabled() bool {
	return envBoolDefault(selectiveInterventionEnabledEnv, true)
}

func selectiveInterventionThreshold() float64 {
	raw := strings.TrimSpace(os.Getenv(selectiveInterventionThresholdEnv))
	if raw == "" {
		return 0.78
	}
	v, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return 0.78
	}
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

func isSelectiveInterventionRequiredError(err error) bool {
	if err == nil {
		return false
	}
	var si *selectiveInterventionError
	if ok := errors.As(err, &si); ok {
		return true
	}
	return strings.Contains(strings.ToUpper(err.Error()), "SELECTIVE_INTERVENTION_REQUIRED")
}

func selectiveInterventionErrorMessage(err error) string {
	if err == nil {
		return ""
	}
	var si *selectiveInterventionError
	if ok := errors.As(err, &si); ok && strings.TrimSpace(si.Message) != "" {
		return strings.TrimSpace(si.Message)
	}
	msg := strings.TrimSpace(err.Error())
	msg = strings.TrimPrefix(msg, "SELECTIVE_INTERVENTION_REQUIRED:")
	return strings.TrimSpace(msg)
}

func maybeRequireSelectiveIntervention(call toolInvocation) error {
	if !selectiveInterventionEnabled() {
		return nil
	}
	score, reasons := scoreSelectiveInterventionRisk(call)
	if score < selectiveInterventionThreshold() {
		return nil
	}

	ticketID := buildSelectiveInterventionID(call)
	if shouldBypassSelectiveIntervention(ticketID) {
		return nil
	}

	selectiveInterventionMu.Lock()
	defer selectiveInterventionMu.Unlock()

	store, _ := loadSelectiveInterventionStore(selectiveInterventionPath)
	ticket, idx := findSelectiveTicket(store, ticketID)
	now := time.Now().UTC()
	if idx >= 0 && strings.EqualFold(strings.TrimSpace(ticket.Status), "approved") {
		return nil
	}
	if idx >= 0 && strings.EqualFold(strings.TrimSpace(ticket.Status), "rejected") {
		msg := fmt.Sprintf("High-risk request for tool '%s' was previously rejected (ticket %s). Use 'approve %s' to continue anyway.", call.Tool, ticketID, ticketID)
		return &selectiveInterventionError{TicketID: ticketID, Message: msg}
	}

	newTicket := selectiveInterventionTicket{
		ID:        ticketID,
		Tool:      call.Tool,
		RiskScore: score,
		Reasons:   reasons,
		Args:      call.Args,
		Status:    "pending",
		CreatedAt: now,
		UpdatedAt: now,
	}
	if idx >= 0 {
		newTicket.CreatedAt = store.Tickets[idx].CreatedAt
		store.Tickets[idx] = newTicket
	} else {
		store.Tickets = append(store.Tickets, newTicket)
	}
	_ = saveSelectiveInterventionStore(selectiveInterventionPath, store)

	if decision := promptHITL(
		ticketID,
		fmt.Sprintf("High-risk tool call blocked: tool=%s score=%.2f reasons=%s", call.Tool, score, strings.Join(reasons, ", ")),
		[]hitlDecision{hitlApprove, hitlReject, hitlDefer},
	); decision != hitlDefer {
		status := "approved"
		if decision == hitlReject {
			status = "rejected"
		}
		store, _ = loadSelectiveInterventionStore(selectiveInterventionPath)
		if ticket, idx := findSelectiveTicket(store, ticketID); idx >= 0 {
			ticket.Status = status
			ticket.UpdatedAt = time.Now().UTC()
			store.Tickets[idx] = ticket
			_ = saveSelectiveInterventionStore(selectiveInterventionPath, store)
		}
		if decision == hitlApprove {
			return nil
		}
		msg := fmt.Sprintf("HITL rejected high-risk tool '%s' (ticket %s).", call.Tool, ticketID)
		return &selectiveInterventionError{TicketID: ticketID, Message: msg}
	}

	msg := fmt.Sprintf(
		"I have the tool and plan, but this is high-risk (score=%.2f; reasons=%s). Manual sign-off required before execution.\nApprove with: approve %s\nDecline with: reject %s",
		score, strings.Join(reasons, ", "), ticketID, ticketID,
	)
	return &selectiveInterventionError{TicketID: ticketID, Message: msg}
}

func scoreSelectiveInterventionRisk(call toolInvocation) (float64, []string) {
	tool := strings.ToLower(strings.TrimSpace(call.Tool))
	reasons := []string{}
	score := 0.0

	switch tool {
	case "revoke_client", "admin_delete_client":
		score = 0.96
		reasons = append(reasons, "client deletion is destructive")
	case "rotate_client_key", "admin_rotate_client":
		score = 0.84
		reasons = append(reasons, "credential rotation impacts active integrations")
	case "provision_client", "admin_create_client":
		score = 0.82
		reasons = append(reasons, "identity provisioning affects security boundary")
	case "sys_exec":
		score = 0.88
		reasons = append(reasons, "shell execution can alter host state")
	case "execute_code":
		score = 0.82
		reasons = append(reasons, "arbitrary code execution")
	case "http_request":
		method := strings.ToUpper(getArgString(call.Args, "method", "GET"))
		if method != "GET" && method != "HEAD" {
			score = maxFloat(score, 0.84)
			reasons = append(reasons, "stateful HTTP method")
		} else {
			score = maxFloat(score, 0.62)
		}
	case "capture_screen", "watch_terminal":
		score = maxFloat(score, 0.80)
		reasons = append(reasons, "sensitive capture scope")
	}

	if tool == "sys_exec" {
		cmd := strings.ToLower(getArgString(call.Args, "command", ""))
		for _, token := range []string{"rm -rf", "shutdown", "reboot", "mkfs", "dd if=", "chmod -r", "chown -r", "truncate -s 0"} {
			if strings.Contains(cmd, token) {
				score = maxFloat(score, 0.97)
				reasons = append(reasons, "destructive command pattern: "+token)
				break
			}
		}
		if getArgBool(call.Args, "override", false) {
			score = maxFloat(score, 0.92)
			reasons = append(reasons, "override flag requested")
		}
	}

	if tool == "http_request" {
		body := strings.TrimSpace(getArgString(call.Args, "body", ""))
		if body != "" {
			score = maxFloat(score, 0.86)
			reasons = append(reasons, "non-empty request body")
		}
	}

	if (tool == "capture_screen" || tool == "watch_terminal" || tool == "draw_box" || tool == "draw_war_room") &&
		!getArgBool(call.Args, "consent", false) {
		score = maxFloat(score, 0.89)
		reasons = append(reasons, "missing explicit consent")
	}

	if len(reasons) == 0 {
		reasons = append(reasons, "high-impact operation")
	}
	return clampFloat(score), uniqueStrings(reasons)
}

func buildSelectiveInterventionID(call toolInvocation) string {
	payload := map[string]interface{}{
		"tool": strings.ToLower(strings.TrimSpace(call.Tool)),
		"args": call.Args,
	}
	raw, _ := json.Marshal(payload)
	sum := sha1.Sum(raw)
	return "si-" + hex.EncodeToString(sum[:])[:12]
}

func shouldBypassSelectiveIntervention(ticketID string) bool {
	if envBoolDefault(selectiveInterventionAutoEnv, false) {
		return true
	}
	raw := strings.TrimSpace(os.Getenv(selectiveApprovedIDsEnv))
	if raw == "" {
		return false
	}
	for _, part := range strings.Split(raw, ",") {
		if strings.EqualFold(strings.TrimSpace(part), ticketID) {
			return true
		}
	}
	return false
}

func TryHandleSelectiveInterventionApproval(input string) (string, bool) {
	in := strings.TrimSpace(strings.ToLower(input))
	if in == "" {
		return "", false
	}
	if !strings.HasPrefix(in, "approve") && !strings.HasPrefix(in, "reject") {
		return "", false
	}

	fields := strings.Fields(in)
	action := fields[0]
	ticketID := ""
	for _, f := range fields[1:] {
		if strings.HasPrefix(f, "si-") {
			ticketID = f
			break
		}
	}

	selectiveInterventionMu.Lock()
	defer selectiveInterventionMu.Unlock()

	store, err := loadSelectiveInterventionStore(selectiveInterventionPath)
	if err != nil {
		return fmt.Sprintf("Selective Intervention: unable to load pending approvals: %v", err), true
	}
	idx := -1
	if ticketID != "" {
		_, idx = findSelectiveTicket(store, ticketID)
	}
	if idx < 0 {
		idx = findLatestPendingTicketIndex(store)
		if idx >= 0 {
			ticketID = store.Tickets[idx].ID
		}
	}
	if idx < 0 {
		return "Selective Intervention: no pending high-risk actions found.", true
	}

	now := time.Now().UTC()
	status := "approved"
	if action == "reject" {
		status = "rejected"
	}
	store.Tickets[idx].Status = status
	store.Tickets[idx].UpdatedAt = now
	if err := saveSelectiveInterventionStore(selectiveInterventionPath, store); err != nil {
		return fmt.Sprintf("Selective Intervention: failed to update %s: %v", ticketID, err), true
	}
	if status == "approved" {
		return fmt.Sprintf("Selective Intervention: approved %s. Re-run the request to continue execution.", ticketID), true
	}
	return fmt.Sprintf("Selective Intervention: rejected %s. Execution remains paused.", ticketID), true
}

func findSelectiveTicket(store selectiveInterventionStore, id string) (selectiveInterventionTicket, int) {
	for i := range store.Tickets {
		if strings.EqualFold(strings.TrimSpace(store.Tickets[i].ID), strings.TrimSpace(id)) {
			return store.Tickets[i], i
		}
	}
	return selectiveInterventionTicket{}, -1
}

func findLatestPendingTicketIndex(store selectiveInterventionStore) int {
	type candidate struct {
		idx int
		ts  time.Time
	}
	var best candidate
	best.idx = -1
	for i := range store.Tickets {
		if !strings.EqualFold(strings.TrimSpace(store.Tickets[i].Status), "pending") {
			continue
		}
		ts := store.Tickets[i].UpdatedAt
		if ts.IsZero() {
			ts = store.Tickets[i].CreatedAt
		}
		if best.idx < 0 || ts.After(best.ts) {
			best = candidate{idx: i, ts: ts}
		}
	}
	return best.idx
}

func loadSelectiveInterventionStore(path string) (selectiveInterventionStore, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return selectiveInterventionStore{}, nil
		}
		return selectiveInterventionStore{}, err
	}
	var store selectiveInterventionStore
	if err := json.Unmarshal(raw, &store); err != nil {
		return selectiveInterventionStore{}, err
	}
	return store, nil
}

func saveSelectiveInterventionStore(path string, store selectiveInterventionStore) error {
	for i := range store.Tickets {
		sort.Strings(store.Tickets[i].Reasons)
	}
	raw, err := json.MarshalIndent(store, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, raw, 0o644)
}

func maxFloat(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}

func clampFloat(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}
