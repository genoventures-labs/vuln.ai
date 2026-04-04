package taloscli

import (
	"strings"
	"testing"
)

func TestRenderLearnDryRunPlanNoInput(t *testing.T) {
	learnFile = ""
	learnDir = ""
	learnFromResearch = ""
	learnURLs = nil
	learnURLFile = ""
	learnHFDatasets = nil
	if _, err := renderLearnDryRunPlan(nil); err == nil {
		t.Fatal("expected error for missing learn input")
	}
}

func TestRenderResearchDryRunPlan(t *testing.T) {
	out := renderResearchDryRunPlan("run", "test query", researchExecutionContext{})
	if !strings.Contains(out, "TALOS RESEARCH DRY RUN") || !strings.Contains(out, "skipped: true") {
		t.Fatalf("unexpected dry-run output: %s", out)
	}
}

func TestRenderLearnDryRunPlanShowsTitleConfig(t *testing.T) {
	learnFile = ""
	learnDir = ""
	learnFromResearch = ""
	learnURLs = nil
	learnURLFile = ""
	learnHFDatasets = nil
	learnTitleChunks = true
	learnTitleMaxChars = 88
	learnTitleModel = "qwen"
	learnIncremental = true
	learnIncrementalManifest = "/tmp/inc.json"

	out, err := renderLearnDryRunPlan([]string{"hello world"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "title_chunks: true") {
		t.Fatalf("expected title_chunks in dry run output, got: %s", out)
	}
	if !strings.Contains(out, "title_max_chars: 88") {
		t.Fatalf("expected title_max_chars in dry run output, got: %s", out)
	}
	if !strings.Contains(out, "title_model: qwen") {
		t.Fatalf("expected title_model in dry run output, got: %s", out)
	}
	if !strings.Contains(out, "incremental: true") {
		t.Fatalf("expected incremental in dry run output, got: %s", out)
	}
}
