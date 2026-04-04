package benchmark

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/Thynaptic/P-LMv1/pkg/capability"
	"github.com/Thynaptic/P-LMv1/pkg/skills"
	"github.com/Thynaptic/P-LMv1/pkg/tools"
)

type TALOSCheckStatus string

const (
	TALOSCheckPass TALOSCheckStatus = "pass"
	TALOSCheckFail TALOSCheckStatus = "fail"
	TALOSCheckSkip TALOSCheckStatus = "skip"
)

type TALOSCheckResult struct {
	Name        string           `json:"name"`
	Status      TALOSCheckStatus `json:"status"`
	DurationMS  int64            `json:"duration_ms"`
	Detail      string           `json:"detail,omitempty"`
	Remediation string           `json:"remediation,omitempty"`
}

type TALOSAuditOptions struct {
	RequirePreflight bool
	CreateSkill      bool
	RunAdminWrites   bool
	ReportPath       string
}

type TALOSAuditReport struct {
	TimestampUTC string             `json:"timestamp_utc"`
	Score        float64            `json:"score"`
	Pass         int                `json:"pass"`
	Fail         int                `json:"fail"`
	Skip         int                `json:"skip"`
	Checks       []TALOSCheckResult `json:"checks"`
}

const defaultAuditToolserverBaseURL = "https://chat.thynaptic.com"

func RunTALOSAudit(ctx context.Context, opt TALOSAuditOptions) TALOSAuditReport {
	if strings.TrimSpace(opt.ReportPath) == "" {
		opt.ReportPath = "talos_benchmark_report.json"
	}
	if !opt.RequirePreflight {
		opt.RequirePreflight = true
	}

	var checks []TALOSCheckResult
	add := func(name string, fn func() (TALOSCheckStatus, string, string)) {
		start := time.Now()
		status, detail, remediation := fn()
		checks = append(checks, TALOSCheckResult{
			Name:        name,
			Status:      status,
			DurationMS:  time.Since(start).Milliseconds(),
			Detail:      strings.TrimSpace(detail),
			Remediation: strings.TrimSpace(remediation),
		})
	}

	add("env.toolserver_base_url", func() (TALOSCheckStatus, string, string) {
		base := resolvedAuditToolserverBaseURL()
		source := "env"
		if strings.TrimSpace(os.Getenv("GLM_TOOLSERVER_BASE_URL")) == "" && strings.TrimSpace(os.Getenv("GLM_TOOL_BASE_URL")) == "" {
			source = "default"
		}
		return TALOSCheckPass, "base_url=" + base + " source=" + source, ""
	})

	add("http.healthz", func() (TALOSCheckStatus, string, string) {
		base := strings.TrimRight(resolvedAuditToolserverBaseURL(), "/")
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, base+"/healthz", nil)
		resp, err := (&http.Client{Timeout: 8 * time.Second}).Do(req)
		if err != nil {
			return TALOSCheckFail, err.Error(), "verify network path and TLS/DNS to toolserver"
		}
		defer resp.Body.Close()
		if resp.StatusCode >= 400 {
			return TALOSCheckFail, fmt.Sprintf("status %d", resp.StatusCode), "verify external access and reverse proxy routing"
		}
		return TALOSCheckPass, fmt.Sprintf("status %d", resp.StatusCode), ""
	})

	var tc *tools.GLMToolClient
	add("tool.client.init", func() (TALOSCheckStatus, string, string) {
		c, err := tools.NewGLMToolClient()
		if err != nil {
			return TALOSCheckFail, err.Error(), "set GLM_CLIENT_ID and GLM_API_KEY in environment"
		}
		tc = c
		return TALOSCheckPass, "initialized", ""
	})

	add("skills.preflight", func() (TALOSCheckStatus, string, string) {
		if tc == nil {
			return TALOSCheckSkip, "tool client unavailable", "fix tool.client.init first"
		}
		resp, err := tc.SkillPreflight(tools.SkillPreflightRequest{
			SkillName: "talos_audit_probe",
			Intent:    "probe TALOS skill preflight pipeline",
		})
		if err != nil {
			if opt.RequirePreflight {
				return TALOSCheckFail, err.Error(), "ensure /skills/preflight is exposed and credentials are valid"
			}
			return TALOSCheckSkip, err.Error(), "preflight not required in this run"
		}
		decision := strings.ToLower(strings.TrimSpace(resp.Decision))
		if decision != "allow" {
			return TALOSCheckFail, "decision=" + decision + " reason=" + strings.TrimSpace(resp.Reason), "adjust skill preflight policy to allow intended TALOS skill actions"
		}
		return TALOSCheckPass, "decision=allow", ""
	})

	add("skills.user_create", func() (TALOSCheckStatus, string, string) {
		if !opt.CreateSkill {
			return TALOSCheckSkip, "disabled by flag", ""
		}
		if tc == nil {
			return TALOSCheckSkip, "tool client unavailable", "fix tool.client.init first"
		}
		creator := skills.NewUserSkillCreator(tc)
		creator.RequirePreflight = opt.RequirePreflight
		res, err := creator.Create(ctx, skills.UserSkillCreateRequest{
			Name:          fmt.Sprintf("talos_audit_%d", time.Now().UnixNano()),
			Intent:        "Create an audit probe self-skill for TALOS",
			Description:   "benchmark probe",
			ReasoningTier: "t2",
			TaskType:      "benchmark",
		})
		if err != nil {
			return TALOSCheckFail, err.Error(), "fix preflight policy or skill generation path permissions"
		}
		return TALOSCheckPass, "artifact=" + strings.TrimSpace(res.Artifact.RootDir), ""
	})

	add("skills.registry", func() (TALOSCheckStatus, string, string) {
		reg := skills.NewSkillRegistry(skills.PermanentSkillsRoot())
		recs, err := reg.ListEnabled()
		if err != nil {
			return TALOSCheckFail, err.Error(), "verify .skills/permanent/index.json permissions and JSON integrity"
		}
		return TALOSCheckPass, fmt.Sprintf("enabled_skills=%d", len(recs)), ""
	})

	var ac *tools.GLMAdminClient
	add("admin.client.init", func() (TALOSCheckStatus, string, string) {
		a, err := tools.NewGLMAdminClientFromEnv()
		if err != nil {
			return TALOSCheckSkip, err.Error(), "set GLM_ADMIN_TOKEN for admin lifecycle checks"
		}
		ac = a
		return TALOSCheckPass, "initialized", ""
	})

	add("admin.plugins.list", func() (TALOSCheckStatus, string, string) {
		if ac == nil {
			return TALOSCheckSkip, "admin client unavailable", "set GLM_ADMIN_TOKEN and remote policy"
		}
		plugins, err := ac.ListPlugins()
		if err != nil {
			return TALOSCheckFail, err.Error(), "verify /admin/plugins route and admin token permissions"
		}
		return TALOSCheckPass, fmt.Sprintf("plugins=%d", len(plugins)), ""
	})

	add("admin.toolgen.generate", func() (TALOSCheckStatus, string, string) {
		if !opt.RunAdminWrites {
			return TALOSCheckSkip, "disabled by flag", "run with --admin-writes to exercise generation pipeline"
		}
		if ac == nil {
			return TALOSCheckSkip, "admin client unavailable", "set GLM_ADMIN_TOKEN and remote policy"
		}
		resp, err := ac.GenerateToolPlugin(tools.AdminToolgenRequest{
			Name:        fmt.Sprintf("talos-audit-%d", time.Now().Unix()),
			Description: "Audit probe plugin",
			Publisher:   "talos",
			Goal:        "verify toolgen generation path",
			Requirement: "Input: {} Output: {\"ok\":true}",
		})
		if err != nil {
			return TALOSCheckFail, err.Error(), "verify /admin/plugins async generation endpoint availability and model config"
		}
		return TALOSCheckPass, "plugin_id=" + strings.TrimSpace(resp.PluginID) + " job_id=" + strings.TrimSpace(resp.JobID), ""
	})

	add("capability.router.policy", func() (TALOSCheckStatus, string, string) {
		orch := capability.NewOrchestrator(nil, skills.NewJITGenerator(""), capability.KeywordLLMClassifier{})
		dec := orch.Decide(ctx, capability.IntentSignal{
			Query:          "build internal reasoning strategy",
			GapDescription: "missing self reasoning pattern",
			TaskType:       "forensic",
			ReasoningTier:  "t2",
		})
		if dec.Kind != capability.CapabilityKindSkill {
			return TALOSCheckFail, "expected skill route, got " + string(dec.Kind), "tune policy markers in capability router"
		}
		return TALOSCheckPass, "kind=" + string(dec.Kind), ""
	})

	pass, fail, skip := tally(checks)
	score := 0.0
	total := pass + fail
	if total > 0 {
		score = float64(pass) / float64(total)
	}

	return TALOSAuditReport{
		TimestampUTC: time.Now().UTC().Format(time.RFC3339),
		Score:        score,
		Pass:         pass,
		Fail:         fail,
		Skip:         skip,
		Checks:       checks,
	}
}

func tally(checks []TALOSCheckResult) (pass, fail, skip int) {
	for _, c := range checks {
		switch c.Status {
		case TALOSCheckPass:
			pass++
		case TALOSCheckFail:
			fail++
		default:
			skip++
		}
	}
	return pass, fail, skip
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func resolvedAuditToolserverBaseURL() string {
	base := strings.TrimSpace(os.Getenv("GLM_TOOLSERVER_BASE_URL"))
	if base == "" {
		base = strings.TrimSpace(os.Getenv("GLM_TOOL_BASE_URL"))
	}
	if base == "" {
		base = defaultAuditToolserverBaseURL
	}
	return strings.TrimRight(base, "/")
}
