package skills

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Thynaptic/P-LMv1/pkg/tools"
)

func TestUserSkillCreator_CreateAllow(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/skills/preflight" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"decision": "allow",
			"invoke":   []map[string]interface{}{{"path": "/tools/web_search"}},
		})
	}))
	defer srv.Close()

	tc := &tools.GLMToolClient{
		BaseURL:  srv.URL,
		APIKey:   "k",
		ClientID: "c",
		HTTP:     srv.Client(),
	}
	creator := NewUserSkillCreator(tc)
	creator.Generator = NewJITGenerator(t.TempDir())
	res, err := creator.Create(context.Background(), UserSkillCreateRequest{
		Name:   "jira_helper",
		Intent: "summarize jira ticket",
	})
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	if strings.TrimSpace(res.Artifact.SkillID) == "" || strings.TrimSpace(res.Artifact.ManifestPath) == "" {
		t.Fatalf("artifact incomplete: %+v", res.Artifact)
	}
	if strings.ToLower(strings.TrimSpace(res.Preflight.Decision)) != "allow" {
		t.Fatalf("unexpected preflight decision: %+v", res.Preflight)
	}
}

func TestUserSkillCreator_DenyBlocked(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"decision": "deny",
			"reason":   "policy denied",
		})
	}))
	defer srv.Close()

	tc := &tools.GLMToolClient{
		BaseURL:  srv.URL,
		APIKey:   "k",
		ClientID: "c",
		HTTP:     srv.Client(),
	}
	creator := NewUserSkillCreator(tc)
	creator.Generator = NewJITGenerator(t.TempDir())
	_, err := creator.Create(context.Background(), UserSkillCreateRequest{
		Name:   "jira_helper",
		Intent: "summarize jira ticket",
	})
	if err == nil {
		t.Fatalf("expected deny error")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "deny") {
		t.Fatalf("expected deny in error, got %v", err)
	}
}

func TestUserSkillCreator_PreflightRequiredNoClient(t *testing.T) {
	t.Parallel()

	creator := NewUserSkillCreator(nil)
	creator.Generator = NewJITGenerator(t.TempDir())
	_, err := creator.Create(context.Background(), UserSkillCreateRequest{
		Name:   "x",
		Intent: "y",
	})
	if err == nil || !strings.Contains(strings.ToLower(err.Error()), "preflight") {
		t.Fatalf("expected preflight requirement error, got %v", err)
	}
}
