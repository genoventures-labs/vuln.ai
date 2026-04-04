package cognition

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

const (
	defaultSkillsTempDir = ".skills/temp"
	defaultSkillsPermDir = ".skills/permanent"
	skillExecTimeout     = 5 * time.Second
)

var capabilityGapMarkers = []string{
	"no tool", "missing tool", "cannot parse", "can't parse",
	"unsupported format", "proprietary binary", "need parser",
	"no capability", "capability gap", "unable to process",
}

var dangerousPattern = regexp.MustCompile(`(?i)\b(rm\s+-rf|wget\b|curl\b|nc\b|netcat\b|ssh\b|scp\b)\b`)

// CapabilityGap represents a detected missing capability in reasoning output.
type CapabilityGap struct {
	Detected    bool
	Description string
}

// SkillPrimitive is a JIT-compiled mini capability.
type SkillPrimitive struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Language    string `json:"language"`
	Description string `json:"description"`
	RootDir     string `json:"root_dir"`
	ScriptPath  string `json:"script_path"`
	UseCount    int    `json:"use_count"`
}

// SkillCompiler generates and dispatches constrained skill primitives.
type SkillCompiler struct {
	TempDir      string
	PermanentDir string
}

// NewSkillCompiler returns a compiler with default skill roots.
func NewSkillCompiler() *SkillCompiler {
	return &SkillCompiler{
		TempDir:      defaultSkillsTempDir,
		PermanentDir: defaultSkillsPermDir,
	}
}

// IdentifyCapabilityGap scans text for likely missing-tool statements.
func IdentifyCapabilityGap(text string) CapabilityGap {
	t := strings.ToLower(strings.TrimSpace(text))
	if t == "" {
		return CapabilityGap{}
	}
	for _, marker := range capabilityGapMarkers {
		if strings.Contains(t, marker) {
			return CapabilityGap{
				Detected:    true,
				Description: marker,
			}
		}
	}
	return CapabilityGap{}
}

// CompileSkillPrimitive JIT-generates a constrained script for the detected capability gap.
func (sc *SkillCompiler) CompileSkillPrimitive(gap CapabilityGap, query string) (SkillPrimitive, error) {
	if !gap.Detected {
		return SkillPrimitive{}, fmt.Errorf("no capability gap detected")
	}
	if sc == nil {
		sc = NewSkillCompiler()
	}
	if err := os.MkdirAll(sc.TempDir, 0o755); err != nil {
		return SkillPrimitive{}, err
	}
	if err := os.MkdirAll(sc.PermanentDir, 0o755); err != nil {
		return SkillPrimitive{}, err
	}

	id := fmt.Sprintf("skill_%d", time.Now().UnixNano())
	root := filepath.Join(sc.TempDir, id)
	if err := os.MkdirAll(root, 0o755); err != nil {
		return SkillPrimitive{}, err
	}

	skill := SkillPrimitive{
		ID:          id,
		Name:        "capability_" + sanitizeName(gap.Description),
		Language:    "bash",
		Description: "JIT capability primitive for gap: " + gap.Description,
		RootDir:     root,
		ScriptPath:  filepath.Join(root, "skill.sh"),
		UseCount:    0,
	}

	content := buildSkillScript(gap, query)
	if dangerousPattern.MatchString(content) {
		return SkillPrimitive{}, fmt.Errorf("generated script failed safety policy")
	}
	if err := os.WriteFile(skill.ScriptPath, []byte(content), 0o755); err != nil {
		return SkillPrimitive{}, err
	}
	if err := sc.persistMetadata(skill); err != nil {
		return SkillPrimitive{}, err
	}
	return skill, nil
}

// DispatchSandbox executes the compiled skill in a constrained environment.
func (sc *SkillCompiler) DispatchSandbox(skill SkillPrimitive, input string) (string, error) {
	if strings.TrimSpace(skill.ScriptPath) == "" {
		return "", fmt.Errorf("skill has no script path")
	}
	scriptPath := strings.TrimSpace(skill.ScriptPath)
	if !filepath.IsAbs(scriptPath) {
		if abs, err := filepath.Abs(scriptPath); err == nil {
			scriptPath = abs
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), skillExecTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "/bin/bash", scriptPath, input)
	cmd.Dir = skill.RootDir
	cmd.Env = []string{
		"PATH=/usr/bin:/bin",
		"HOME=/tmp",
		"LANG=C",
	}

	out, err := cmd.CombinedOutput()
	result := strings.TrimSpace(string(out))
	if result == "" {
		result = "(no output)"
	}
	AppendSkillExecutionLog(SkillExecutionLog{
		Timestamp: time.Now().UTC(),
		SkillID:   skill.ID,
		SkillName: skill.Name,
		SkillPath: skill.RootDir,
		Success:   err == nil,
	})
	if err != nil {
		return result, err
	}
	if sc != nil {
		_ = sc.bumpUseCount(skill)
	}
	return result, nil
}

func buildSkillScript(gap CapabilityGap, query string) string {
	base := `#!/usr/bin/env bash
set -euo pipefail
INPUT="${1:-}"
if [[ "${INPUT}" == "--self-test" ]]; then
  echo "skill-ok"
  exit 0
fi
`
	lg := strings.ToLower(gap.Description + " " + query)
	if strings.Contains(lg, "binary") || strings.Contains(lg, "parse") {
		base += `
echo "Primitive parser helper:"
echo "- Use 'xxd -g 1 <file>' for byte-level inspection."
echo "- Use 'strings <file>' to extract printable segments."
echo "- Use 'file <file>' for quick format hints."
echo "Input: ${INPUT}"
`
	} else {
		base += `
echo "Capability primitive activated for gap remediation."
echo "Focus: ` + escapeForBash(gap.Description) + `"
echo "Input: ${INPUT}"
`
	}
	return base
}

func (sc *SkillCompiler) bumpUseCount(skill SkillPrimitive) error {
	metaPath := filepath.Join(skill.RootDir, "metadata.json")
	var current SkillPrimitive
	if b, err := os.ReadFile(metaPath); err == nil {
		_ = json.Unmarshal(b, &current)
	}
	if strings.TrimSpace(current.ID) == "" {
		current = skill
	}
	current.UseCount++
	return sc.persistMetadata(current)
}

func (sc *SkillCompiler) persistMetadata(skill SkillPrimitive) error {
	metaPath := filepath.Join(skill.RootDir, "metadata.json")
	payload, err := json.MarshalIndent(skill, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(metaPath, payload, 0o644)
}

func sanitizeName(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	repl := strings.NewReplacer(" ", "_", "-", "_", "/", "_", "\\", "_", ":", "_")
	s = repl.Replace(s)
	if s == "" {
		return "generic"
	}
	return s
}

func escapeForBash(s string) string {
	return strings.ReplaceAll(s, `"`, `\"`)
}
