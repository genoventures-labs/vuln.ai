package skills

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const (
	defaultCodeLabRoot    = ".codelab"
	defaultScratchSubdir  = "scratch"
	defaultVerifyTimeout  = 45 * time.Second
	defaultCommandTimeout = 20 * time.Second
)

var goLineErrorRE = regexp.MustCompile(`(?m)^(.+\.go):(\d+):(?:(\d+):)?\s*(.+)$`)
var genericLineErrorRE = regexp.MustCompile(`(?m)^(.+\.(?:go|py|svelte|ts|js|tsx|jsx)):(\d+):(?:(\d+):)?\s*(.+)$`)
var pyTraceLineErrorRE = regexp.MustCompile(`(?m)^\s*File "([^"]+)", line (\d+)(?:, in [^\n]+)?$`)
var npmErrorRE = regexp.MustCompile(`(?m)^npm ERR!\s+(.+)$`)
var rustSpanRE = regexp.MustCompile(`^\s*-->\s+(.+\.rs):(\d+):(\d+)`)
var rustHelpNoteRE = regexp.MustCompile(`^\s*(help|note):\s*(.+)$`)
var rustEqHelpNoteRE = regexp.MustCompile(`^\s*=\s*(help|note):\s*(.+)$`)
var rustErrWarnRE = regexp.MustCompile(`^(error|warning)(?:\[[A-Za-z0-9_]+\])?:\s*(.+)$`)

// LineError is a parsed line-specific compiler/linter issue.
type LineError struct {
	File     string `json:"file"`
	Line     int    `json:"line"`
	Column   int    `json:"column,omitempty"`
	Message  string `json:"message"`
	Severity string `json:"severity"`
	Tool     string `json:"tool"`
}

// Diagnostic captures one tool invocation and its output.
type Diagnostic struct {
	Tool      string      `json:"tool"`
	Command   []string    `json:"command"`
	ExitCode  int         `json:"exit_code"`
	Stdout    string      `json:"stdout"`
	Stderr    string      `json:"stderr"`
	Duration  int64       `json:"duration_ms"`
	LineError []LineError `json:"line_errors,omitempty"`
}

// DiagnosticReport is the structured output from Verify(path).
type DiagnosticReport struct {
	Path        string       `json:"path"`
	Language    string       `json:"language"`
	Workspace   string       `json:"workspace"`
	StartedAt   time.Time    `json:"started_at"`
	FinishedAt  time.Time    `json:"finished_at"`
	ExitCode    int          `json:"exit_code"`
	Success     bool         `json:"success"`
	Diagnostics []Diagnostic `json:"diagnostics"`
	LineErrors  []LineError  `json:"line_errors,omitempty"`
}

// CommandSpec defines one toolchain command invocation.
type CommandSpec struct {
	Tool string
	Args []string
}

// LanguageProfile describes how to verify code for a specific language.
type LanguageProfile interface {
	Name() string
	Commands(targetPath string) []CommandSpec
}

// GoProfile defines Go formatter/build/vet toolchain commands.
type GoProfile struct{}

func (GoProfile) Name() string {
	return "go"
}

// RustProfile defines Rust syntax/lint verification commands.
type RustProfile struct{}

func (RustProfile) Name() string {
	return "rust"
}

func (RustProfile) Commands(targetPath string) []CommandSpec {
	workspace := nearestCargoWorkspace(targetPath)
	if workspace == "" {
		return []CommandSpec{
			{Tool: "cargo check", Args: []string{"cargo", "check"}},
			{Tool: "cargo clippy", Args: []string{"cargo", "clippy", "--", "-D", "warnings"}},
		}
	}
	return []CommandSpec{
		{Tool: "cargo check", Args: []string{"cargo", "check"}},
		{Tool: "cargo clippy", Args: []string{"cargo", "clippy", "--", "-D", "warnings"}},
	}
}

// SvelteProfile defines Svelte/TypeScript verification commands.
type SvelteProfile struct{}

func (SvelteProfile) Name() string {
	return "svelte"
}

func (SvelteProfile) Commands(targetPath string) []CommandSpec {
	workspace := targetPath
	if st, err := os.Stat(targetPath); err == nil && !st.IsDir() {
		workspace = filepath.Dir(targetPath)
	}

	tscArgs := nodeToolOrNpx(workspace, "tsc")
	tscArgs = append(tscArgs, "--noEmit", "--pretty", "false")

	if hasNpmCheckScript(workspace) {
		return []CommandSpec{
			{Tool: "npm run check", Args: []string{"npm", "run", "check"}},
			{Tool: "tsc --noEmit", Args: tscArgs},
		}
	}

	if hasLocalNodeTool(workspace, "svelte-check") {
		return []CommandSpec{
			{Tool: "svelte-check", Args: []string{filepath.Join(".", "node_modules", ".bin", "svelte-check")}},
			{Tool: "tsc --noEmit", Args: tscArgs},
		}
	}

	return []CommandSpec{
		{Tool: "svelte-check", Args: []string{"npx", "--yes", "svelte-check"}},
		{Tool: "tsc --noEmit", Args: tscArgs},
	}
}

// PythonProfile defines Python lint/type-check commands.
type PythonProfile struct{}

func (PythonProfile) Name() string {
	return "python"
}

func (PythonProfile) Commands(targetPath string) []CommandSpec {
	target := strings.TrimSpace(targetPath)
	if target == "" {
		target = "."
	}
	ruff := commandOrModule("ruff", "ruff")
	mypy := commandOrModule("mypy", "mypy")
	return []CommandSpec{
		{Tool: "ruff check", Args: append(ruff, "check", target)},
		{Tool: "mypy", Args: append(mypy, target)},
	}
}

func (GoProfile) Commands(targetPath string) []CommandSpec {
	p := strings.TrimSpace(targetPath)
	if p == "" {
		return nil
	}
	if st, err := os.Stat(p); err == nil && !st.IsDir() {
		return []CommandSpec{
			{Tool: "go fmt", Args: []string{"gofmt", "-w", p}},
			{Tool: "go build", Args: []string{"go", "build", p}},
			{Tool: "go vet", Args: []string{"go", "vet", p}},
		}
	}
	return []CommandSpec{
		{Tool: "go fmt", Args: []string{"gofmt", "-w", "."}},
		{Tool: "go build", Args: []string{"go", "build", "./..."}},
		{Tool: "go vet", Args: []string{"go", "vet", "./..."}},
	}
}

// CodeLab provides an isolated workspace and verification routines.
type CodeLab struct {
	RootDir        string
	ScratchDir     string
	VerifyTimeout  time.Duration
	CommandTimeout time.Duration
}

// NewCodeLab initializes the codelab root and scratch directories.
func NewCodeLab(root string) (*CodeLab, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		root = defaultCodeLabRoot
	}
	scratch := filepath.Join(root, defaultScratchSubdir)
	if err := os.MkdirAll(scratch, 0o755); err != nil {
		return nil, fmt.Errorf("create codelab scratch dir: %w", err)
	}
	return &CodeLab{
		RootDir:        root,
		ScratchDir:     scratch,
		VerifyTimeout:  defaultVerifyTimeout,
		CommandTimeout: defaultCommandTimeout,
	}, nil
}

// NewScratchWorkspace creates a new isolated scratch workspace in .codelab/scratch.
func (c *CodeLab) NewScratchWorkspace(prefix string) (string, error) {
	if c == nil {
		return "", fmt.Errorf("codelab is nil")
	}
	name := sanitizeWorkspacePrefix(prefix)
	if name == "" {
		name = "session"
	}
	dir := filepath.Join(c.ScratchDir, fmt.Sprintf("%s_%d", name, time.Now().UnixNano()))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("create scratch workspace: %w", err)
	}
	return dir, nil
}

// Verify attempts to format/build/vet the target path and returns structured diagnostics.
func (c *CodeLab) Verify(path string) DiagnosticReport {
	report := DiagnosticReport{
		Path:      strings.TrimSpace(path),
		Language:  "go",
		StartedAt: time.Now().UTC(),
		ExitCode:  0,
		Success:   true,
	}
	defer func() { report.FinishedAt = time.Now().UTC() }()

	if c == nil {
		report.Success = false
		report.ExitCode = 1
		report.Diagnostics = append(report.Diagnostics, Diagnostic{
			Tool:     "verify",
			Command:  []string{},
			ExitCode: 1,
			Stderr:   "codelab is nil",
		})
		return report
	}

	target := strings.TrimSpace(path)
	if target == "" {
		report.Success = false
		report.ExitCode = 1
		report.Diagnostics = append(report.Diagnostics, Diagnostic{
			Tool:     "verify",
			ExitCode: 1,
			Stderr:   "path is required",
		})
		return report
	}
	absTarget, err := filepath.Abs(target)
	if err != nil {
		report.Success = false
		report.ExitCode = 1
		report.Diagnostics = append(report.Diagnostics, Diagnostic{
			Tool:     "verify",
			ExitCode: 1,
			Stderr:   "resolve path: " + err.Error(),
		})
		return report
	}
	st, err := os.Stat(absTarget)
	if err != nil {
		report.Success = false
		report.ExitCode = 1
		report.Diagnostics = append(report.Diagnostics, Diagnostic{
			Tool:     "verify",
			ExitCode: 1,
			Stderr:   "stat path: " + err.Error(),
		})
		return report
	}

	profile := detectLanguageProfile(absTarget)
	report.Language = profile.Name()

	report.Path = absTarget
	if st.IsDir() {
		report.Workspace = absTarget
	} else {
		report.Workspace = filepath.Dir(absTarget)
	}
	if report.Language == "rust" {
		if ws := nearestCargoWorkspace(absTarget); ws != "" {
			report.Workspace = ws
		}
	}

	timeout := c.VerifyTimeout
	if timeout <= 0 {
		timeout = defaultVerifyTimeout
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	cleanup := func() {}
	if report.Language == "svelte" {
		cleanup = prepareTempNodeEnvironment(report.Workspace)
	}
	defer cleanup()

	commands := profile.Commands(absTarget)
	for _, spec := range commands {
		diag := c.runCommand(ctx, spec, report.Workspace)
		report.Diagnostics = append(report.Diagnostics, diag)
		report.LineErrors = append(report.LineErrors, diag.LineError...)
		if diag.ExitCode != 0 {
			report.Success = false
			report.ExitCode = diag.ExitCode
		}
	}
	return report
}

func (c *CodeLab) runCommand(parent context.Context, spec CommandSpec, workdir string) Diagnostic {
	d := Diagnostic{
		Tool:    spec.Tool,
		Command: append([]string{}, spec.Args...),
	}
	if len(spec.Args) == 0 {
		d.ExitCode = 1
		d.Stderr = "no command provided"
		return d
	}

	timeout := c.CommandTimeout
	if timeout <= 0 {
		timeout = defaultCommandTimeout
	}
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()

	start := time.Now()
	cmd := exec.CommandContext(ctx, spec.Args[0], spec.Args[1:]...)
	cmd.Dir = workdir
	out, err := cmd.CombinedOutput()
	d.Duration = time.Since(start).Milliseconds()

	text := strings.TrimSpace(string(out))
	if err != nil {
		d.ExitCode = commandExitCode(err)
		d.Stderr = text
	} else {
		d.ExitCode = 0
		d.Stdout = text
	}

	combined := strings.TrimSpace(strings.Join([]string{d.Stdout, d.Stderr}, "\n"))
	d.LineError = parseLineErrors(spec.Tool, combined)
	return d
}

func parseLineErrors(tool, text string) []LineError {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}
	matches := goLineErrorRE.FindAllStringSubmatch(text, -1)
	if len(matches) == 0 {
		matches = genericLineErrorRE.FindAllStringSubmatch(text, -1)
	}
	out := make([]LineError, 0, len(matches))
	for _, m := range matches {
		if len(m) < 5 {
			continue
		}
		ln, _ := strconv.Atoi(m[2])
		col := 0
		if strings.TrimSpace(m[3]) != "" {
			col, _ = strconv.Atoi(strings.TrimSpace(m[3]))
		}
		out = append(out, LineError{
			File:     strings.TrimSpace(m[1]),
			Line:     ln,
			Column:   col,
			Message:  strings.TrimSpace(m[4]),
			Severity: severityFromTool(tool),
			Tool:     tool,
		})
	}
	out = append(out, parsePyTracebackErrors(tool, text)...)
	out = append(out, parseNpmErrors(tool, text)...)
	out = append(out, parseRustLineErrors(tool, text)...)
	return out
}

func detectLanguageProfile(path string) LanguageProfile {
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".py":
		return PythonProfile{}
	case ".rs":
		return RustProfile{}
	case ".svelte", ".ts", ".tsx", ".js", ".jsx":
		return SvelteProfile{}
	}
	if isDir(path) && hasCargoToml(path) {
		return RustProfile{}
	}
	if isDir(path) && looksLikeSvelteProject(path) {
		return SvelteProfile{}
	}
	return GoProfile{}
}

func looksLikeSvelteProject(path string) bool {
	workspace := path
	if st, err := os.Stat(path); err == nil && !st.IsDir() {
		workspace = filepath.Dir(path)
	}
	if _, err := os.Stat(filepath.Join(workspace, "svelte.config.js")); err == nil {
		return true
	}
	if _, err := os.Stat(filepath.Join(workspace, "svelte.config.ts")); err == nil {
		return true
	}
	pkgPath := filepath.Join(workspace, "package.json")
	raw, err := os.ReadFile(pkgPath)
	if err != nil {
		return false
	}
	var parsed struct {
		Dependencies    map[string]string `json:"dependencies"`
		DevDependencies map[string]string `json:"devDependencies"`
		Scripts         map[string]string `json:"scripts"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return false
	}
	if hasPkgEntry(parsed.Dependencies, "svelte") || hasPkgEntry(parsed.DevDependencies, "svelte") {
		return true
	}
	return hasPkgEntry(parsed.DevDependencies, "@sveltejs/kit")
}

func hasNpmCheckScript(workspace string) bool {
	raw, err := os.ReadFile(filepath.Join(workspace, "package.json"))
	if err != nil {
		return false
	}
	var parsed struct {
		Scripts map[string]string `json:"scripts"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return false
	}
	_, ok := parsed.Scripts["check"]
	return ok
}

func hasLocalNodeTool(workspace string, tool string) bool {
	p := filepath.Join(workspace, "node_modules", ".bin", tool)
	if _, err := os.Stat(p); err == nil {
		return true
	}
	return false
}

func hasPkgEntry(m map[string]string, key string) bool {
	if len(m) == 0 {
		return false
	}
	_, ok := m[key]
	return ok
}

func commandOrModule(bin string, module string) []string {
	if _, err := exec.LookPath(bin); err == nil {
		return []string{bin}
	}
	return []string{"python3", "-m", module}
}

func nodeToolOrNpx(workspace string, tool string) []string {
	local := filepath.Join(workspace, "node_modules", ".bin", tool)
	if _, err := os.Stat(local); err == nil {
		return []string{local}
	}
	if _, err := exec.LookPath(tool); err == nil {
		return []string{tool}
	}
	return []string{"npx", "--yes", tool}
}

func prepareTempNodeEnvironment(workspace string) func() {
	var cleanup []func()
	pkgPath := filepath.Join(workspace, "package.json")
	if _, err := os.Stat(pkgPath); err != nil {
		const tmpPkg = `{"name":"codelab-scratch","private":true,"version":"0.0.0","type":"module"}`
		if werr := os.WriteFile(pkgPath, []byte(tmpPkg), 0o644); werr == nil {
			cleanup = append(cleanup, func() { _ = os.Remove(pkgPath) })
		}
	}
	tsConfigPath := filepath.Join(workspace, "tsconfig.json")
	if _, err := os.Stat(tsConfigPath); err != nil {
		const tmpTSConfig = `{"compilerOptions":{"strict":true,"noEmit":true,"target":"ES2020","module":"ESNext","moduleResolution":"Bundler","skipLibCheck":true},"include":["**/*.ts","**/*.tsx","**/*.svelte"]}`
		if werr := os.WriteFile(tsConfigPath, []byte(tmpTSConfig), 0o644); werr == nil {
			cleanup = append(cleanup, func() { _ = os.Remove(tsConfigPath) })
		}
	}
	return func() {
		for i := len(cleanup) - 1; i >= 0; i-- {
			cleanup[i]()
		}
	}
}

func parsePyTracebackErrors(tool string, text string) []LineError {
	matches := pyTraceLineErrorRE.FindAllStringSubmatch(text, -1)
	if len(matches) == 0 {
		return nil
	}
	out := make([]LineError, 0, len(matches))
	for _, m := range matches {
		if len(m) < 3 {
			continue
		}
		ln, _ := strconv.Atoi(strings.TrimSpace(m[2]))
		out = append(out, LineError{
			File:     strings.TrimSpace(m[1]),
			Line:     ln,
			Message:  "python traceback",
			Severity: "error",
			Tool:     tool,
		})
	}
	return out
}

func parseNpmErrors(tool string, text string) []LineError {
	matches := npmErrorRE.FindAllStringSubmatch(text, -1)
	if len(matches) == 0 {
		return nil
	}
	out := make([]LineError, 0, len(matches))
	for _, m := range matches {
		if len(m) < 2 {
			continue
		}
		msg := strings.TrimSpace(m[1])
		if msg == "" {
			continue
		}
		out = append(out, LineError{
			File:     "",
			Line:     0,
			Message:  msg,
			Severity: "error",
			Tool:     tool,
		})
	}
	return out
}

func isDir(path string) bool {
	st, err := os.Stat(path)
	if err != nil {
		return false
	}
	return st.IsDir()
}

func hasCargoToml(path string) bool {
	workspace := path
	if st, err := os.Stat(path); err == nil && !st.IsDir() {
		workspace = filepath.Dir(path)
	}
	_, err := os.Stat(filepath.Join(workspace, "Cargo.toml"))
	return err == nil
}

func nearestCargoWorkspace(path string) string {
	p := strings.TrimSpace(path)
	if p == "" {
		return ""
	}
	if st, err := os.Stat(p); err == nil && !st.IsDir() {
		p = filepath.Dir(p)
	}
	cur := p
	for i := 0; i < 8; i++ {
		if cur == "" || cur == "/" || cur == "." {
			break
		}
		if _, err := os.Stat(filepath.Join(cur, "Cargo.toml")); err == nil {
			return cur
		}
		next := filepath.Dir(cur)
		if next == cur {
			break
		}
		cur = next
	}
	return ""
}

func parseRustLineErrors(tool string, text string) []LineError {
	if !strings.Contains(strings.ToLower(text), ".rs") &&
		!strings.Contains(strings.ToLower(text), "borrow") &&
		!strings.Contains(strings.ToLower(tool), "cargo") {
		return nil
	}
	scanner := bufio.NewScanner(strings.NewReader(text))
	var out []LineError
	curFile := ""
	curLine := 0
	curCol := 0
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		if m := rustSpanRE.FindStringSubmatch(line); len(m) == 4 {
			curFile = strings.TrimSpace(m[1])
			curLine, _ = strconv.Atoi(strings.TrimSpace(m[2]))
			curCol, _ = strconv.Atoi(strings.TrimSpace(m[3]))
			continue
		}
		if m := rustEqHelpNoteRE.FindStringSubmatch(line); len(m) == 3 {
			out = append(out, LineError{
				File:     curFile,
				Line:     curLine,
				Column:   curCol,
				Message:  strings.TrimSpace(m[2]),
				Severity: strings.ToLower(strings.TrimSpace(m[1])),
				Tool:     tool,
			})
			continue
		}
		if m := rustHelpNoteRE.FindStringSubmatch(line); len(m) == 3 {
			out = append(out, LineError{
				File:     curFile,
				Line:     curLine,
				Column:   curCol,
				Message:  strings.TrimSpace(m[2]),
				Severity: strings.ToLower(strings.TrimSpace(m[1])),
				Tool:     tool,
			})
			continue
		}
		if m := rustErrWarnRE.FindStringSubmatch(line); len(m) == 3 {
			out = append(out, LineError{
				File:     curFile,
				Line:     curLine,
				Column:   curCol,
				Message:  strings.TrimSpace(m[2]),
				Severity: strings.ToLower(strings.TrimSpace(m[1])),
				Tool:     tool,
			})
		}
	}
	return out
}

func severityFromTool(tool string) string {
	t := strings.ToLower(strings.TrimSpace(tool))
	switch {
	case strings.Contains(t, "vet"):
		return "warning"
	default:
		return "error"
	}
}

func commandExitCode(err error) int {
	if err == nil {
		return 0
	}
	var exitErr *exec.ExitError
	if ok := errorAs(err, &exitErr); ok {
		return exitErr.ExitCode()
	}
	return 1
}

func sanitizeWorkspacePrefix(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" {
		return ""
	}
	var b strings.Builder
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			b.WriteRune(r)
		}
	}
	return strings.Trim(b.String(), "-_")
}

// errorAs avoids importing errors in older/strict contexts where only one helper is needed.
func errorAs(err error, target interface{}) bool {
	switch t := target.(type) {
	case **exec.ExitError:
		if e, ok := err.(*exec.ExitError); ok {
			*t = e
			return true
		}
	}
	return false
}
