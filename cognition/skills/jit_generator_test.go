package skills

import (
	"os"
	"path/filepath"
	"testing"
)

func TestJITGeneratorGenerate(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	gen := NewJITGenerator(root)
	art, err := gen.Generate(JITSkillRequest{
		Name:          "reasoning_router",
		Description:   "route capability",
		Requirement:   "decide tool vs skill",
		ReasoningTier: "t2",
		TaskType:      "architecture",
	})
	if err != nil {
		t.Fatalf("Generate failed: %v", err)
	}
	if art.SkillID == "" || art.RootDir == "" || art.SourcePath == "" || art.ManifestPath == "" {
		t.Fatalf("artifact missing required fields: %+v", art)
	}
	if _, err := os.Stat(art.SourcePath); err != nil {
		t.Fatalf("source file missing: %v", err)
	}
	if _, err := os.Stat(art.ManifestPath); err != nil {
		t.Fatalf("manifest file missing: %v", err)
	}
	if filepath.Dir(art.SourcePath) != art.RootDir {
		t.Fatalf("source path not inside root dir")
	}
}
