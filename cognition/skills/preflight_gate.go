package skills

import (
	"context"
	"fmt"
	"strings"

	"github.com/Thynaptic/P-LMv1/pkg/tools"
)

func EnsurePreflightAllowed(ctx context.Context, tc *tools.GLMToolClient, req tools.SkillPreflightRequest, required bool) (tools.SkillPreflightResponse, error) {
	if !required {
		return tools.SkillPreflightResponse{}, nil
	}
	if tc == nil {
		return tools.SkillPreflightResponse{}, fmt.Errorf("skills preflight required but tool client unavailable")
	}
	resp, err := tc.SkillPreflight(req)
	if err != nil {
		return tools.SkillPreflightResponse{}, fmt.Errorf("skills preflight failed: %w", err)
	}
	switch strings.ToLower(strings.TrimSpace(resp.Decision)) {
	case "allow":
		return resp, nil
	case "deny", "review":
		reason := strings.TrimSpace(resp.Reason)
		if reason == "" {
			reason = "skill preflight did not allow request"
		}
		return tools.SkillPreflightResponse{}, fmt.Errorf("skills preflight %s: %s", strings.ToLower(strings.TrimSpace(resp.Decision)), reason)
	default:
		return tools.SkillPreflightResponse{}, fmt.Errorf("skills preflight returned unknown decision: %q", resp.Decision)
	}
}
