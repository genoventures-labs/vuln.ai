package cognition

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/Thynaptic/P-LMv1/pkg/state"
	"github.com/ollama/ollama/api"
)

const defaultAlignmentLogPath = ".memory/alignment_audit.json"

var (
	privateIPv4Pattern = regexp.MustCompile(`\b(?:10\.\d{1,3}\.\d{1,3}\.\d{1,3}|192\.168\.\d{1,3}\.\d{1,3}|172\.(?:1[6-9]|2\d|3[0-1])\.\d{1,3}\.\d{1,3})\b`)
)

type AlignmentViolation struct {
	Rule    string `json:"rule"`
	Message string `json:"message"`
}

type AuditResult struct {
	Compliant       bool                 `json:"compliant"`
	Violations      []AlignmentViolation `json:"violations"`
	OriginalOutput  string               `json:"original_output"`
	CorrectedOutput string               `json:"corrected_output,omitempty"`
}

type alignmentAuditLog struct {
	Timestamp        time.Time            `json:"timestamp"`
	PolicyProfile    string               `json:"policy_profile"`
	Violations       []AlignmentViolation `json:"violations"`
	Corrected        bool                 `json:"corrected"`
	OutputPreview    string               `json:"output_preview"`
	CorrectedPreview string               `json:"corrected_preview,omitempty"`
}

// AlignmentAuditor enforces policy constraints post-synthesis.
type AlignmentAuditor struct {
	PolicyProfile string
	LogPath       string
}

func NewAlignmentAuditor(policyProfile string) *AlignmentAuditor {
	policyProfile = strings.TrimSpace(policyProfile)
	if policyProfile == "" {
		policyProfile = "balanced"
	}
	return &AlignmentAuditor{
		PolicyProfile: policyProfile,
		LogPath:       defaultAlignmentLogPath,
	}
}

// Audit evaluates output against policy profile.
func (aa *AlignmentAuditor) Audit(output string) AuditResult {
	out := strings.TrimSpace(output)
	result := AuditResult{
		Compliant:      true,
		OriginalOutput: out,
	}
	if out == "" {
		return result
	}

	profile := strings.ToLower(strings.TrimSpace(aa.PolicyProfile))
	switch profile {
	case "internal ip masking":
		if privateIPv4Pattern.MatchString(out) {
			result.Compliant = false
			result.Violations = append(result.Violations, AlignmentViolation{
				Rule:    "internal_ip_masking",
				Message: "Output contains private internal IP addresses.",
			})
		}
	case "federal compliance":
		if privateIPv4Pattern.MatchString(out) {
			result.Compliant = false
			result.Violations = append(result.Violations, AlignmentViolation{
				Rule:    "redact_internal_network_details",
				Message: "Output exposes internal private network details.",
			})
		}
		if containsUncheckedInstruction(out) {
			result.Compliant = false
			result.Violations = append(result.Violations, AlignmentViolation{
				Rule:    "safe_operational_guidance",
				Message: "Output includes operational guidance without safety guardrails.",
			})
		}
	default:
		// balanced/strict profiles: apply lightweight redaction rule only.
		if profile == "strict" && privateIPv4Pattern.MatchString(out) {
			result.Compliant = false
			result.Violations = append(result.Violations, AlignmentViolation{
				Rule:    "strict_masking",
				Message: "Strict profile requires masking internal IP addresses.",
			})
		}
	}
	return result
}

// Correct re-synthesizes output to satisfy policy while preserving utility.
func (aa *AlignmentAuditor) Correct(output string, modelCandidates []string) string {
	corrected := applyDeterministicCorrections(output, aa.PolicyProfile)
	if strings.TrimSpace(corrected) == "" {
		return output
	}

	// Optional lightweight LLM polish when available.
	client, err := api.ClientFromEnvironment()
	if err != nil || len(modelCandidates) == 0 {
		return corrected
	}

	system := `You are an alignment correction assistant.
Preserve technical utility while enforcing policy constraints.
Do not remove critical technical steps; redact only sensitive details.
Return only the corrected answer text.`
	user := "Policy profile: " + aa.PolicyProfile + "\n\nCandidate output:\n" + corrected
	for _, model := range modelCandidates {
		opts, _ := state.ResolveEntropyOptions(user)
		req := &api.ChatRequest{
			Model:   model,
			Options: opts,
			Messages: []api.Message{
				{Role: "system", Content: system},
				{Role: "user", Content: user},
			},
		}
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		var out strings.Builder
		err := client.Chat(ctx, req, func(resp api.ChatResponse) error {
			out.WriteString(resp.Message.Content)
			return nil
		})
		cancel()
		if err == nil && strings.TrimSpace(out.String()) != "" {
			return strings.TrimSpace(stripMarkdownCodeFences(out.String()))
		}
	}
	return corrected
}

// AuditAndCorrect runs policy audit and applies correction loop when needed.
func (aa *AlignmentAuditor) AuditAndCorrect(output string, modelCandidates []string) AuditResult {
	result := aa.Audit(output)
	if result.Compliant {
		aa.log(result)
		return result
	}
	corrected := aa.Correct(output, modelCandidates)
	result.CorrectedOutput = corrected
	// Re-audit once after correction.
	post := aa.Audit(corrected)
	result.Compliant = post.Compliant
	if len(post.Violations) > 0 {
		result.Violations = post.Violations
	}
	aa.log(result)
	return result
}

func (aa *AlignmentAuditor) log(result AuditResult) {
	path := aa.LogPath
	if strings.TrimSpace(path) == "" {
		path = defaultAlignmentLogPath
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return
	}

	var logs []alignmentAuditLog
	if b, err := os.ReadFile(path); err == nil && len(b) > 0 {
		_ = json.Unmarshal(b, &logs)
	}
	entry := alignmentAuditLog{
		Timestamp:        time.Now().UTC(),
		PolicyProfile:    aa.PolicyProfile,
		Violations:       result.Violations,
		Corrected:        strings.TrimSpace(result.CorrectedOutput) != "",
		OutputPreview:    truncateAlignment(result.OriginalOutput, 320),
		CorrectedPreview: truncateAlignment(result.CorrectedOutput, 320),
	}
	logs = append(logs, entry)
	if len(logs) > 2000 {
		logs = logs[len(logs)-2000:]
	}
	data, err := json.MarshalIndent(logs, "", "  ")
	if err != nil {
		return
	}
	_ = os.WriteFile(path, data, 0o644)
}

func applyDeterministicCorrections(s string, policy string) string {
	out := strings.TrimSpace(s)
	if out == "" {
		return out
	}
	profile := strings.ToLower(strings.TrimSpace(policy))
	if profile == "internal ip masking" || profile == "federal compliance" || profile == "strict" {
		out = privateIPv4Pattern.ReplaceAllString(out, "[REDACTED_INTERNAL_IP]")
	}
	if profile == "federal compliance" {
		if containsUncheckedInstruction(out) {
			out += "\n\nSafety note: Validate in a controlled environment before production rollout."
		}
	}
	return out
}

func containsUncheckedInstruction(s string) bool {
	l := strings.ToLower(s)
	return strings.Contains(l, "run this in production now") || strings.Contains(l, "disable all security checks")
}

func truncateAlignment(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	if n < 4 {
		return s[:n]
	}
	return s[:n-3] + "..."
}

func (aa *AlignmentAuditor) String() string {
	return fmt.Sprintf("AlignmentAuditor(policy=%s)", aa.PolicyProfile)
}
