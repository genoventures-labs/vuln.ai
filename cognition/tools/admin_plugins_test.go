package tools

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestGLMAdminClient_PluginAndToolgenEndpoints(t *testing.T) {
	t.Parallel()

	var gotPaths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPaths = append(gotPaths, r.Method+" "+r.URL.Path)
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/admin/plugins":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"plugins": []map[string]interface{}{
					{"id": "p1", "name": "plugin-1", "enabled": false},
				},
			})
		case r.Method == http.MethodPost && r.URL.Path == "/admin/plugins/install":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"plugin_id": "p1",
				"status":    "installed",
			})
		case r.Method == http.MethodPost && r.URL.Path == "/admin/plugins/p1/enable":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"status": "enabled"})
		case r.Method == http.MethodPost && r.URL.Path == "/admin/plugins/p1/disable":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"status": "disabled"})
		case r.Method == http.MethodPost && r.URL.Path == "/admin/plugins":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"id":        "j1",
				"plugin_id": "p1",
				"name":      "plugin-1",
				"publisher": "thynaptic",
				"status":    "queued",
			})
		case r.Method == http.MethodGet && r.URL.Path == "/admin/plugins/jobs/j1":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"id":        "j1",
				"status":    "ready",
				"plugin_id": "p1",
				"name":      "plugin-1",
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	ac := &GLMAdminClient{
		BaseURL:    srv.URL,
		AdminToken: "x",
		HTTP:       srv.Client(),
	}

	plugins, err := ac.ListPlugins()
	if err != nil || len(plugins) != 1 {
		t.Fatalf("ListPlugins failed: %v len=%d", err, len(plugins))
	}
	if _, err := ac.InstallPlugin(AdminPluginInstallRequest{PluginID: "p1"}); err != nil {
		t.Fatalf("InstallPlugin failed: %v", err)
	}
	if err := ac.EnablePlugin("p1"); err != nil {
		t.Fatalf("EnablePlugin failed: %v", err)
	}
	if err := ac.DisablePlugin("p1"); err != nil {
		t.Fatalf("DisablePlugin failed: %v", err)
	}
	if _, err := ac.GenerateToolPlugin(AdminToolgenRequest{Name: "x"}); err != nil {
		t.Fatalf("GenerateToolPlugin failed: %v", err)
	}
	if _, err := ac.GetToolgenJob("j1"); err != nil {
		t.Fatalf("GetToolgenJob failed: %v", err)
	}

	required := []string{
		"GET /admin/plugins",
		"POST /admin/plugins/install",
		"POST /admin/plugins/p1/enable",
		"POST /admin/plugins/p1/disable",
		"POST /admin/plugins",
		"GET /admin/plugins/jobs/j1",
	}
	have := strings.Join(gotPaths, "|")
	for _, want := range required {
		if !strings.Contains(have, want) {
			t.Fatalf("missing call %s; got %v", want, gotPaths)
		}
	}
}
