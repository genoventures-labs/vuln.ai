package taloscli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Thynaptic/P-LMv1/pkg/toolflow"
)

func TestApplyArtifactDrivenInputsResearchArtifact(t *testing.T) {
	tmp := t.TempDir()
	origArtifactsDir := researchArtifactsDir
	researchArtifactsDir = tmp
	t.Cleanup(func() { researchArtifactsDir = origArtifactsDir })

	artifact := ResearchArtifact{
		SessionID:        "research-1",
		Query:            "incident triage",
		ExecutiveSummary: "Auth service timeout spike",
		Findings:         []researchFinding{{Text: "Timeouts increased after deploy", Verified: true}},
		EvidenceNotes:    []string{"p99 latency regressed from 200ms to 1.8s"},
		Sources:          []string{"https://status.example.com/incident/42"},
	}
	b, _ := json.Marshal(artifact)
	if err := os.WriteFile(filepath.Join(tmp, "research-1.json"), b, 0o644); err != nil {
		t.Fatalf("write artifact: %v", err)
	}

	in := []toolflow.Invocation{
		{Tool: "fetch_url", Args: map[string]interface{}{"artifact_id": "research-1"}},
		{Tool: "web_search", Args: map[string]interface{}{"query": "root cause", "artifact_id": "research-1"}},
	}
	out, notes, warns := applyArtifactDrivenInputs(in)
	if len(warns) != 0 {
		t.Fatalf("expected no warnings, got %v", warns)
	}
	if got := strings.TrimSpace(getArgString(out[0].Args, "url", "")); got != "https://status.example.com/incident/42" {
		t.Fatalf("expected url from artifact source, got %q", got)
	}
	q := strings.TrimSpace(getArgString(out[1].Args, "query", ""))
	if !strings.Contains(strings.ToLower(q), "root cause") || !strings.Contains(strings.ToLower(q), "timeout") {
		t.Fatalf("expected merged artifact context in query, got %q", q)
	}
	if len(notes) == 0 {
		t.Fatal("expected artifact application notes")
	}
}

func TestApplyArtifactDrivenInputsReflectionAudit(t *testing.T) {
	tmp := t.TempDir()
	auditPath := filepath.Join(tmp, "reflection_audit.jsonl")
	line := `{"timestamp":"2026-02-18T12:00:00Z","stage":"research_synthesis","outcome":"steer","risk_score":0.82,"violations":["missing citation markers while sources are available"]}`
	if err := os.WriteFile(auditPath, []byte(line+"\n"), 0o644); err != nil {
		t.Fatalf("write audit: %v", err)
	}

	in := []toolflow.Invocation{
		{Tool: "vector_retrieve", Args: map[string]interface{}{"reflection_audit_path": auditPath}},
	}
	out, notes, warns := applyArtifactDrivenInputs(in)
	if len(warns) != 0 {
		t.Fatalf("expected no warnings, got %v", warns)
	}
	q := strings.TrimSpace(getArgString(out[0].Args, "query", ""))
	if q == "" {
		t.Fatal("expected reflection-derived query to be injected")
	}
	if !strings.Contains(strings.ToLower(q), "missing citation") {
		t.Fatalf("expected reflection evidence in query, got %q", q)
	}
	if len(notes) == 0 {
		t.Fatal("expected artifact notes for reflection ingest")
	}
}
