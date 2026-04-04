package tools

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestGLMToolClientSkillPreflight(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/skills/preflight" || r.Method != http.MethodPost {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"decision": "allow",
			"reason":   "policy allow",
			"invoke": []map[string]interface{}{
				{"path": "/tools/web_search"},
			},
			"limits": map[string]interface{}{
				"timeout_sec": 10,
			},
		})
	}))
	defer srv.Close()

	c := &GLMToolClient{
		BaseURL:  srv.URL,
		APIKey:   "x",
		ClientID: "c1",
		HTTP:     srv.Client(),
	}
	resp, err := c.SkillPreflight(SkillPreflightRequest{
		SkillName: "jira_helper",
		Intent:    "lookup ticket",
	})
	if err != nil {
		t.Fatalf("SkillPreflight failed: %v", err)
	}
	if resp.Decision != "allow" {
		t.Fatalf("expected allow, got %q", resp.Decision)
	}
	if len(resp.Invoke) != 1 || resp.Invoke[0].Path != "/tools/web_search" {
		t.Fatalf("unexpected invoke response: %+v", resp.Invoke)
	}
}
