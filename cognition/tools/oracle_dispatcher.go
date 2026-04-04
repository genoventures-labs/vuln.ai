package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const (
	defaultOracleTimeout    = 90 * time.Second
	defaultMirrorBriefsPath = ".memory/reasoning_mirror_briefs.jsonl"
)

// OraclePayload is the payload sent to goracle for external consensus.
type OraclePayload struct {
	FailedWorkOrder map[string]interface{} `json:"failed_work_order,omitempty"`
	MirrorLines     []string               `json:"mirror_lines,omitempty"`
	ReflexStats     map[string]interface{} `json:"reflex_stats,omitempty"`
	VetoReason      string                 `json:"veto_reason,omitempty"`
	Metadata        map[string]interface{} `json:"metadata,omitempty"`
}

// OracleRequest is kept as a compatibility alias.
type OracleRequest = OraclePayload

// OracleDirective is a tagged high-priority directive returned from Oracle response.
type OracleDirective struct {
	Type       string `json:"type"`
	TrustLevel string `json:"trust_level"`
	Content    string `json:"content"`
}

// OracleDispatchResult captures oracle IO and downstream dispatch status.
type OracleDispatchResult struct {
	AliasCommand      string          `json:"alias_command"`
	OracleResponse    string          `json:"oracle_response"`
	Directive         OracleDirective `json:"directive"`
	DownstreamApplied bool            `json:"downstream_applied"`
	DispatchNote      string          `json:"dispatch_note,omitempty"`
}

type mirrorBrief struct {
	Timestamp time.Time `json:"timestamp"`
	Source    string    `json:"source,omitempty"`
	Message   string    `json:"message"`
}

// DispatchToOracle sends a packaged request to local `goracle`.
func DispatchToOracle(payload OraclePayload) (OracleDispatchResult, error) {
	_ = payload
	_ = AppendReasoningMirrorLine("Local consensus failed. Ascending to the Oracle for Tier-1 resolution...")

	stdout, stderr, err := callOracleAlias(payload)
	result := OracleDispatchResult{
		AliasCommand:   "bash -c \"source ~/.bashrc >/dev/null 2>&1; goracle\"",
		OracleResponse: strings.TrimSpace(stdout),
	}
	if err != nil {
		errText := strings.TrimSpace(stderr)
		if errText != "" {
			return result, fmt.Errorf("oracle alias failed: %w: %s", err, errText)
		}
		return result, fmt.Errorf("oracle alias failed: %w", err)
	}
	if strings.TrimSpace(result.OracleResponse) == "" {
		return result, fmt.Errorf("oracle alias returned empty response")
	}
	result.Directive = OracleDirective{
		Type:       "OracleDirective",
		TrustLevel: "Pre-Audited Truth",
		Content:    result.OracleResponse,
	}

	result.DownstreamApplied = false
	result.DispatchNote = "runtime local builder disabled; route via GLM toolserver policy"
	_ = AppendReasoningMirrorLine("Oracle directive received. Runtime local builder dispatch is disabled; route via GLM toolserver policy.")
	return result, nil
}

// AppendReasoningMirrorLine appends a short mirror line to the shared mirror feed.
func AppendReasoningMirrorLine(line string) error {
	line = strings.TrimSpace(line)
	if line == "" {
		return nil
	}
	msg := mirrorBrief{
		Timestamp: time.Now().UTC(),
		Source:    "glm-oracle",
		Message:   line,
	}
	b, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	return appendJSONL(defaultMirrorBriefsPath, string(b))
}

func callOracleAlias(payload OraclePayload) (string, string, error) {
	b, err := json.Marshal(payload)
	if err != nil {
		return "", "", err
	}
	// Explicitly call bash function `goracle` from ~/.bashrc.
	shell := "source ~/.bashrc >/dev/null 2>&1; goracle"
	ctx, cancel := context.WithTimeout(context.Background(), defaultOracleTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "bash", "-c", shell)
	cmd.Stdin = bytes.NewReader(b)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err = cmd.Run()
	return strings.TrimSpace(stdout.String()), strings.TrimSpace(stderr.String()), err
}

func appendJSONL(path, line string) error {
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
