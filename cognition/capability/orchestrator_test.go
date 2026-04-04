package capability

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Thynaptic/P-LMv1/pkg/skills"
	"github.com/Thynaptic/P-LMv1/pkg/tools"
)

func TestOrchestrator_ToolPathTrustedAutoEnable(t *testing.T) {
	t.Setenv("GLM_TRUSTED_PLUGIN_PUBLISHERS", "thynaptic")

	enableCalled := false
	jobPollCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/admin/plugins":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"id":        "job-1",
				"plugin_id": "talos-net",
				"name":      "talos-net",
				"publisher": "thynaptic",
				"status":    "queued",
				"endpoint":  "http://plugin/exec",
			})
		case "/admin/plugins/jobs/job-1":
			jobPollCount++
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"id":        "job-1",
				"plugin_id": "talos-net",
				"name":      "talos-net",
				"publisher": "thynaptic",
				"status":    "ready",
				"endpoint":  "http://plugin/exec",
			})
		case "/admin/plugins/talos-net/enable":
			enableCalled = true
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"status": "enabled"})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	orch := NewOrchestrator(&tools.GLMAdminClient{
		BaseURL:    srv.URL,
		AdminToken: "x",
		HTTP:       srv.Client(),
	}, skills.NewJITGenerator(t.TempDir()), KeywordLLMClassifier{})

	out, err := orch.HandleGap(context.Background(), IntentSignal{
		Query:          "call remote api plugin",
		GapDescription: "missing endpoint plugin",
		TaskType:       "debug",
		ReasoningTier:  "t2",
	})
	if err != nil {
		t.Fatalf("HandleGap failed: %v", err)
	}
	if out.Plugin == nil {
		t.Fatalf("expected plugin outcome")
	}
	if !out.Plugin.AutoEnabled || out.Plugin.PendingApproval {
		t.Fatalf("expected trusted auto-enable, got %+v", out.Plugin)
	}
	if !enableCalled {
		t.Fatalf("expected enable endpoint call")
	}
	if jobPollCount == 0 {
		t.Fatalf("expected async job polling")
	}
}

func TestOrchestrator_SkillPath(t *testing.T) {
	t.Parallel()
	skillSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/skills/preflight" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"decision": "allow",
			"reason":   "ok",
		})
	}))
	defer skillSrv.Close()
	orch := NewOrchestrator(nil, skills.NewJITGenerator(t.TempDir()), KeywordLLMClassifier{})
	orch.ToolClient = &tools.GLMToolClient{
		BaseURL:  skillSrv.URL,
		APIKey:   "k",
		ClientID: "c",
		HTTP:     skillSrv.Client(),
	}
	out, err := orch.HandleGap(context.Background(), IntentSignal{
		Query:          "improve reasoning strategy",
		GapDescription: "need internal reasoning pattern",
		TaskType:       "forensic",
		ReasoningTier:  "t2",
	})
	if err != nil {
		t.Fatalf("HandleGap failed: %v", err)
	}
	if out.Skill == nil {
		t.Fatalf("expected skill artifact")
	}
	if out.Decision.Kind != CapabilityKindSkill {
		t.Fatalf("expected skill decision, got %s", out.Decision.Kind)
	}
}
