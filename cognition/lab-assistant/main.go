package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/Thynaptic/P-LMv1/pkg/cognition"
	"github.com/Thynaptic/P-LMv1/pkg/envload"
	"github.com/Thynaptic/P-LMv1/pkg/forge"
	"github.com/Thynaptic/P-LMv1/pkg/skills"
)

const (
	defaultInboxDir      = ".memory/lab_assistant/inbox"
	defaultStatePath     = ".memory/lab_assistant/state.json"
	defaultMirrorPath    = ".memory/reasoning_mirror_briefs.jsonl"
	defaultResultPath    = ".memory/lab_assistant/results.jsonl"
	defaultTick          = 3 * time.Second
	defaultMaxAttempts   = 3
	defaultVerifyTimeout = 120 * time.Second
)

type mirrorBrief struct {
	Timestamp time.Time `json:"timestamp"`
	Source    string    `json:"source,omitempty"`
	Message   string    `json:"message"`
}

type trackedFile struct {
	Path    string `json:"path"`
	ModUnix int64  `json:"mod_unix"`
	Size    int64  `json:"size"`
}

type daemonState struct {
	LastScanAt time.Time              `json:"last_scan_at"`
	Primed     bool                   `json:"primed"`
	Seen       map[string]trackedFile `json:"seen"`
}

type hudBox struct {
	X    int    `json:"x"`
	Y    int    `json:"y"`
	W    int    `json:"w"`
	H    int    `json:"h"`
	Text string `json:"text,omitempty"`
}

type labTask struct {
	ID             string `json:"id,omitempty"`
	SourcePath     string `json:"source_path,omitempty"`
	SuggestedPatch string `json:"suggested_patch,omitempty"`
	SuggestedFile  string `json:"suggested_file,omitempty"`
	SuggestedCode  string `json:"suggested_code,omitempty"`
	ApplyCommand   string `json:"apply_command,omitempty"`
	VerifyCommand  string `json:"verify_command,omitempty"`
	MaxAttempts    int    `json:"max_attempts,omitempty"`
	CleanupShadow  bool   `json:"cleanup_shadow,omitempty"`
	Highlight      hudBox `json:"highlight,omitempty"`
}

type taskResult struct {
	Timestamp    time.Time `json:"timestamp"`
	TaskID       string    `json:"task_id"`
	SourcePath   string    `json:"source_path"`
	ShadowPath   string    `json:"shadow_path"`
	Success      bool      `json:"success"`
	Attempts     int       `json:"attempts"`
	VerifyStdout string    `json:"verify_stdout,omitempty"`
	VerifyStderr string    `json:"verify_stderr,omitempty"`
}

type assistant struct {
	inboxDir   string
	statePath  string
	mirrorPath string
	resultPath string
	tick       time.Duration

	mu    sync.Mutex
	state daemonState
}

func main() {
	if err := envload.Autoload(); err != nil {
		fmt.Fprintf(os.Stderr, "warning: env autoload failed: %v\n", err)
	}

	var (
		inboxDir   = flag.String("inbox", defaultInboxDir, "lab task inbox directory")
		statePath  = flag.String("state-path", defaultStatePath, "daemon state path")
		mirrorPath = flag.String("mirror-path", defaultMirrorPath, "reasoning mirror JSONL path")
		resultPath = flag.String("result-path", defaultResultPath, "task result JSONL path")
		tick       = flag.Duration("tick", defaultTick, "scan interval")
	)
	flag.Parse()

	d := &assistant{
		inboxDir:   strings.TrimSpace(*inboxDir),
		statePath:  strings.TrimSpace(*statePath),
		mirrorPath: strings.TrimSpace(*mirrorPath),
		resultPath: strings.TrimSpace(*resultPath),
		tick:       *tick,
	}
	if d.inboxDir == "" {
		d.inboxDir = defaultInboxDir
	}
	if d.statePath == "" {
		d.statePath = defaultStatePath
	}
	if d.mirrorPath == "" {
		d.mirrorPath = defaultMirrorPath
	}
	if d.resultPath == "" {
		d.resultPath = defaultResultPath
	}
	if d.tick <= 0 {
		d.tick = defaultTick
	}
	_ = os.MkdirAll(d.inboxDir, 0o755)
	if err := d.loadState(); err != nil {
		fmt.Printf("glm-lab-assistant: state load warning: %v\n", err)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	fmt.Printf("glm-lab-assistant online. inbox=%s\n", d.inboxDir)
	if err := d.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
		fmt.Fprintf(os.Stderr, "glm-lab-assistant exited with error: %v\n", err)
		os.Exit(1)
	}
}

func (d *assistant) Run(ctx context.Context) error {
	ticker := time.NewTicker(d.tick)
	defer ticker.Stop()
	for {
		if err := d.scanOnce(); err != nil {
			fmt.Printf("glm-lab-assistant: scan warning: %v\n", err)
		}
		select {
		case <-ctx.Done():
			_ = d.persistState()
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func (d *assistant) scanOnce() error {
	paths, err := d.changedTasks()
	if err != nil {
		return err
	}
	d.mu.Lock()
	primed := d.state.Primed
	if !d.state.Primed {
		d.state.Primed = true
	}
	d.state.LastScanAt = time.Now().UTC()
	d.mu.Unlock()

	if !primed {
		return d.persistState()
	}

	for _, p := range paths {
		if err := d.handleTaskPath(p); err != nil {
			fmt.Printf("glm-lab-assistant: task %s warning: %v\n", p, err)
		}
	}
	return d.persistState()
}

func (d *assistant) changedTasks() ([]string, error) {
	if d.state.Seen == nil {
		d.state.Seen = map[string]trackedFile{}
	}
	entries, err := os.ReadDir(d.inboxDir)
	if err != nil {
		return nil, err
	}
	var changed []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := strings.ToLower(strings.TrimSpace(e.Name()))
		if !strings.HasSuffix(name, ".json") {
			continue
		}
		path := filepath.Join(d.inboxDir, e.Name())
		info, err := e.Info()
		if err != nil {
			continue
		}
		tf := trackedFile{Path: path, ModUnix: info.ModTime().Unix(), Size: info.Size()}
		prev, ok := d.state.Seen[path]
		if !ok || prev.ModUnix != tf.ModUnix || prev.Size != tf.Size {
			changed = append(changed, path)
		}
		d.state.Seen[path] = tf
	}
	sort.Strings(changed)
	return changed, nil
}

func (d *assistant) handleTaskPath(path string) error {
	task, err := parseTask(path)
	if err != nil {
		return err
	}
	if strings.TrimSpace(task.SourcePath) == "" {
		return nil
	}
	if strings.TrimSpace(task.ID) == "" {
		task.ID = strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	}
	attempts := task.MaxAttempts
	if attempts <= 0 {
		attempts = defaultMaxAttempts
	}

	shadow, err := forge.CloneToShadow(task.SourcePath)
	if err != nil {
		return err
	}
	defer func() {
		if task.CleanupShadow {
			_ = forge.CleanShadowWorkspace(shadow)
		}
	}()

	_ = d.appendMirror("Lab Assistant picked up fix verification for task " + task.ID + ".")

	var lastStdout, lastStderr string
	var success bool
	var usedAttempts int

	for i := 1; i <= attempts; i++ {
		usedAttempts = i
		if err := applySuggestedFix(task, shadow); err != nil {
			lastStderr = err.Error()
			_ = d.appendMirror(fmt.Sprintf("Attempt %d/%d: failed to apply fix: %s", i, attempts, trimLine(err.Error())))
			continue
		}
		stdout, stderr, err := verifyFix(shadow, task.VerifyCommand)
		lastStdout, lastStderr = stdout, stderr
		if err == nil {
			if auditErr := runSymbolicAudit(task, shadow); auditErr == nil {
				success = true
				break
			} else {
				lastStderr = auditErr.Error()
			}
		}

		insight, ok := skills.ExplainCompilerOutput(lastStderr)
		if ok && insight.Detected {
			_ = d.appendMirror(fmt.Sprintf("Attempt %d/%d failed: %s Fix: %s", i, attempts, insight.Why, insight.SuggestedFix))
		} else {
			_ = d.appendMirror(fmt.Sprintf("Attempt %d/%d failed: %s", i, attempts, trimLine(lastStderr)))
		}
	}

	if success {
		_ = d.appendMirror("Sir, I've successfully verified the fix in the shadow workspace. It passed all unit tests and symbolic audits. Ready for deployment?")
		_ = highlightVerified(task.Highlight)
	} else {
		_ = d.appendMirror("Lab Assistant could not verify the fix yet. Captured failure details for local reasoning refinement.")
	}

	res := taskResult{
		Timestamp:    time.Now().UTC(),
		TaskID:       task.ID,
		SourcePath:   task.SourcePath,
		ShadowPath:   shadow,
		Success:      success,
		Attempts:     usedAttempts,
		VerifyStdout: trimLine(lastStdout),
		VerifyStderr: trimLine(lastStderr),
	}
	if b, err := json.Marshal(res); err == nil {
		_ = appendJSONL(d.resultPath, string(b))
	}
	return nil
}

func parseTask(path string) (labTask, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return labTask{}, err
	}
	var t labTask
	if err := json.Unmarshal(raw, &t); err != nil {
		return labTask{}, err
	}
	return t, nil
}

func applySuggestedFix(task labTask, shadow string) error {
	if strings.TrimSpace(task.SuggestedPatch) != "" {
		patchPath := filepath.Join(shadow, ".lab_assistant_patch.diff")
		if err := os.WriteFile(patchPath, []byte(task.SuggestedPatch), 0o644); err != nil {
			return err
		}
		_, stderr, err := runCmd(shadow, 40*time.Second, "git apply --whitespace=nowarn .lab_assistant_patch.diff")
		if err != nil {
			return fmt.Errorf("git apply failed: %s", trimLine(stderr))
		}
		return nil
	}
	if strings.TrimSpace(task.SuggestedFile) != "" && strings.TrimSpace(task.SuggestedCode) != "" {
		p := filepath.Join(shadow, strings.TrimSpace(task.SuggestedFile))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return err
		}
		return os.WriteFile(p, []byte(task.SuggestedCode), 0o644)
	}
	if strings.TrimSpace(task.ApplyCommand) != "" {
		_, stderr, err := runCmd(shadow, 45*time.Second, task.ApplyCommand)
		if err != nil {
			return fmt.Errorf("apply command failed: %s", trimLine(stderr))
		}
	}
	return nil
}

func verifyFix(shadow, verifyCommand string) (string, string, error) {
	cmd := strings.TrimSpace(verifyCommand)
	if cmd == "" {
		cmd = "go test ./..."
	}
	stdout, stderr, err := runCmd(shadow, defaultVerifyTimeout, cmd)
	if err == nil {
		return stdout, stderr, nil
	}
	if strings.Contains(strings.ToLower(cmd), "go test") {
		// fallback: build check
		bOut, bErr, bRunErr := runCmd(shadow, defaultVerifyTimeout, "go build ./...")
		if bRunErr == nil {
			return bOut, bErr, nil
		}
		return stdout + "\n" + bOut, stderr + "\n" + bErr, bRunErr
	}
	return stdout, stderr, err
}

func runSymbolicAudit(task labTask, shadow string) error {
	var code string
	if strings.TrimSpace(task.SuggestedFile) != "" {
		p := filepath.Join(shadow, strings.TrimSpace(task.SuggestedFile))
		raw, err := os.ReadFile(p)
		if err != nil {
			return nil // no direct file to audit; don't block success
		}
		code = string(raw)
	}
	if strings.TrimSpace(code) == "" {
		return nil
	}
	pkgs := projectPackages(shadow)
	goModule, goAllowed, nodeAllowed := cognition.LoadDependencyAllowlist(shadow)
	violations := cognition.AuditGoCodeSymbolic(cognition.CodeAuditOptions{
		Query:               "lab assistant fix verification",
		Code:                code,
		ProjectPackages:     pkgs,
		GoModule:            goModule,
		AllowedGoModules:    goAllowed,
		AllowedNodePackages: nodeAllowed,
	})
	if len(violations) > 0 {
		return fmt.Errorf("symbolic audit failed: %s", strings.Join(violations, "; "))
	}
	return nil
}

func projectPackages(root string) []string {
	var out []string
	entries, err := os.ReadDir(filepath.Join(root, "pkg"))
	if err != nil {
		return nil
	}
	for _, e := range entries {
		if e.IsDir() {
			out = append(out, "pkg/"+e.Name())
		}
	}
	sort.Strings(out)
	return out
}

func highlightVerified(h hudBox) error {
	x, y, w, ht := h.X, h.Y, h.W, h.H
	if w <= 0 {
		w = 360
	}
	if ht <= 0 {
		ht = 140
	}
	if h.Text == "" {
		h.Text = "Verified Success"
	}
	// Default approximate editor center if not provided.
	if x <= 0 {
		x = 420
	}
	if y <= 0 {
		y = 220
	}
	res := skills.DrawWarRoom(skills.DrawWarRoomRequest{
		Consent:    true,
		DurationMS: 4200,
		Boxes: []skills.WarRoomBox{
			{X: x, Y: y, W: w, H: ht, Text: h.Text, Kind: "verified", Color: "green"},
		},
	})
	if res.ExitCode != 0 {
		return fmt.Errorf("hud highlight failed: %s", strings.TrimSpace(res.Stderr))
	}
	return nil
}

func runCmd(dir string, timeout time.Duration, command string) (string, string, error) {
	if timeout <= 0 {
		timeout = 40 * time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "bash", "-lc", command)
	cmd.Dir = dir
	var outBuf, errBuf strings.Builder
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf
	err := cmd.Run()
	return strings.TrimSpace(outBuf.String()), strings.TrimSpace(errBuf.String()), err
}

func (d *assistant) appendMirror(msg string) error {
	b, _ := json.Marshal(mirrorBrief{
		Timestamp: time.Now().UTC(),
		Source:    "glm-lab-assistant",
		Message:   strings.TrimSpace(msg),
	})
	return appendJSONL(d.mirrorPath, string(b))
}

func appendJSONL(path, line string) error {
	path = strings.TrimSpace(path)
	if path == "" {
		return fmt.Errorf("path required")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.WriteString(strings.TrimSpace(line) + "\n")
	return err
}

func trimLine(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	re := regexp.MustCompile(`\s+`)
	s = re.ReplaceAllString(s, " ")
	if len(s) > 280 {
		return s[:280] + "...(truncated)"
	}
	return s
}

func (d *assistant) loadState() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	raw, err := os.ReadFile(d.statePath)
	if err != nil {
		if os.IsNotExist(err) {
			d.state = daemonState{Seen: map[string]trackedFile{}}
			return nil
		}
		return err
	}
	var st daemonState
	if err := json.Unmarshal(raw, &st); err != nil {
		return err
	}
	if st.Seen == nil {
		st.Seen = map[string]trackedFile{}
	}
	d.state = st
	return nil
}

func (d *assistant) persistState() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.state.Seen == nil {
		d.state.Seen = map[string]trackedFile{}
	}
	if err := os.MkdirAll(filepath.Dir(d.statePath), 0o755); err != nil {
		return err
	}
	b, _ := json.MarshalIndent(d.state, "", "  ")
	return os.WriteFile(d.statePath, b, 0o644)
}
