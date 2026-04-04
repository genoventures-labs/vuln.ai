package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/Thynaptic/P-LMv1/pkg/envload"
)

const (
	defaultResultsPath = ".memory/lab_assistant/results.jsonl"
	defaultMirrorPath  = ".memory/reasoning_mirror_briefs.jsonl"
	defaultBacklogPath = ".memory/compliance/backlog.jsonl"
	defaultStatePath   = ".memory/compliance/state.json"
	defaultTick        = 4 * time.Second
)

var (
	absPathRE      = regexp.MustCompile(`(?m)(["'` + "`" + `])(/(?:etc|var|home|tmp|opt|root|srv|usr)/[^"'` + "`" + `\s]{1,220})\1`)
	weakCryptoRE   = regexp.MustCompile(`(?m)\b(md5|sha1|des|rc4)\b`)
	missingTLSRE   = regexp.MustCompile(`(?m)\bhttp://`)
	intentBypassRE = regexp.MustCompile(`(?m)\b(allow_all|skip_auth|disable_auth|bypass_policy)\b`)
)

type mirrorBrief struct {
	Timestamp time.Time `json:"timestamp"`
	Source    string    `json:"source,omitempty"`
	Message   string    `json:"message"`
}

type daemonState struct {
	ResultsOffset int64     `json:"results_offset"`
	LastScanAt    time.Time `json:"last_scan_at,omitempty"`
}

type labResult struct {
	Timestamp  time.Time `json:"timestamp"`
	TaskID     string    `json:"task_id,omitempty"`
	BuildID    string    `json:"build_id,omitempty"`
	SourcePath string    `json:"source_path,omitempty"`
	ShadowPath string    `json:"shadow_path,omitempty"`
	Success    bool      `json:"success"`
}

type vulnerabilityFinding struct {
	Rule     string   `json:"rule"`
	Severity string   `json:"severity"`
	Files    []string `json:"files,omitempty"`
	Note     string   `json:"note,omitempty"`
}

type complianceBacklogItem struct {
	Timestamp     time.Time              `json:"timestamp"`
	BuildID       string                 `json:"build_id,omitempty"`
	TaskID        string                 `json:"task_id,omitempty"`
	SourcePath    string                 `json:"source_path,omitempty"`
	SasswallBasis []string               `json:"sasswall_basis"`
	Findings      []vulnerabilityFinding `json:"findings,omitempty"`
	Advisory      string                 `json:"advisory"`
}

type daemon struct {
	resultsPath string
	mirrorPath  string
	backlogPath string
	statePath   string
	tick        time.Duration

	mu    sync.Mutex
	state daemonState
}

func main() {
	if err := envload.Autoload(); err != nil {
		fmt.Fprintf(os.Stderr, "warning: env autoload failed: %v\n", err)
	}

	var (
		resultsPath = flag.String("results", defaultResultsPath, "lab-assistant results jsonl")
		mirrorPath  = flag.String("mirror-briefs", defaultMirrorPath, "reasoning mirror jsonl")
		backlogPath = flag.String("backlog", defaultBacklogPath, "compliance backlog jsonl")
		statePath   = flag.String("state-path", defaultStatePath, "daemon state path")
		tick        = flag.Duration("tick", defaultTick, "poll interval")
	)
	flag.Parse()

	d := &daemon{
		resultsPath: strings.TrimSpace(*resultsPath),
		mirrorPath:  strings.TrimSpace(*mirrorPath),
		backlogPath: strings.TrimSpace(*backlogPath),
		statePath:   strings.TrimSpace(*statePath),
		tick:        *tick,
	}
	if d.resultsPath == "" {
		d.resultsPath = defaultResultsPath
	}
	if d.mirrorPath == "" {
		d.mirrorPath = defaultMirrorPath
	}
	if d.backlogPath == "" {
		d.backlogPath = defaultBacklogPath
	}
	if d.statePath == "" {
		d.statePath = defaultStatePath
	}
	if d.tick <= 0 {
		d.tick = defaultTick
	}
	if err := d.loadState(); err != nil {
		fmt.Fprintf(os.Stderr, "glm-compliance-d: state load warning: %v\n", err)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	fmt.Printf("glm-compliance-d online. results=%s\n", d.resultsPath)
	if err := d.run(ctx); err != nil && !errors.Is(err, context.Canceled) {
		fmt.Fprintf(os.Stderr, "glm-compliance-d exited with error: %v\n", err)
		os.Exit(1)
	}
}

func (d *daemon) run(ctx context.Context) error {
	ticker := time.NewTicker(d.tick)
	defer ticker.Stop()
	for {
		if err := d.scanOnce(); err != nil && !errors.Is(err, os.ErrNotExist) {
			fmt.Printf("glm-compliance-d: scan warning: %v\n", err)
		}
		select {
		case <-ctx.Done():
			_ = d.persistState()
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func (d *daemon) scanOnce() error {
	results, newOffset, err := d.readNewResults()
	if err != nil {
		return err
	}
	for _, r := range results {
		if !r.Success {
			continue
		}
		findings := auditVerifiedSource(strings.TrimSpace(r.SourcePath))
		if len(findings) == 0 {
			continue
		}
		advisory := buildAdvisoryLine(r, findings)
		item := complianceBacklogItem{
			Timestamp:     time.Now().UTC(),
			BuildID:       strings.TrimSpace(r.BuildID),
			TaskID:        strings.TrimSpace(r.TaskID),
			SourcePath:    strings.TrimSpace(r.SourcePath),
			SasswallBasis: []string{"Access Control", "Intent Analysis"},
			Findings:      findings,
			Advisory:      advisory,
		}
		_ = appendJSONL(d.backlogPath, mustJSON(item))
		_ = appendJSONL(d.mirrorPath, mustJSON(mirrorBrief{
			Timestamp: time.Now().UTC(),
			Source:    "glm-compliance-d",
			Message:   advisory,
		}))
	}
	d.mu.Lock()
	d.state.ResultsOffset = newOffset
	d.state.LastScanAt = time.Now().UTC()
	d.mu.Unlock()
	return d.persistState()
}

func (d *daemon) readNewResults() ([]labResult, int64, error) {
	f, err := os.Open(d.resultsPath)
	if err != nil {
		return nil, d.currentOffset(), err
	}
	defer f.Close()

	st, err := f.Stat()
	if err != nil {
		return nil, d.currentOffset(), err
	}
	start := d.currentOffset()
	if start > st.Size() {
		start = 0
	}
	if _, err := f.Seek(start, io.SeekStart); err != nil {
		return nil, start, err
	}
	sc := bufio.NewScanner(f)
	var out []labResult
	for sc.Scan() {
		ln := strings.TrimSpace(sc.Text())
		if ln == "" {
			continue
		}
		var row labResult
		if err := json.Unmarshal([]byte(ln), &row); err != nil {
			continue
		}
		out = append(out, row)
	}
	if err := sc.Err(); err != nil {
		return nil, start, err
	}
	return out, st.Size(), nil
}

func (d *daemon) currentOffset() int64 {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.state.ResultsOffset
}

func auditVerifiedSource(sourcePath string) []vulnerabilityFinding {
	sourcePath = strings.TrimSpace(sourcePath)
	if sourcePath == "" {
		return nil
	}
	info, err := os.Stat(sourcePath)
	if err != nil {
		return nil
	}
	var files []string
	if info.IsDir() {
		_ = filepath.WalkDir(sourcePath, func(path string, de os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return nil
			}
			if de.IsDir() {
				if strings.Contains(strings.ToLower(path), "/.git") || strings.Contains(strings.ToLower(path), "/.memory") {
					return filepath.SkipDir
				}
				return nil
			}
			ext := strings.ToLower(filepath.Ext(path))
			switch ext {
			case ".go", ".yaml", ".yml", ".json", ".conf", ".ini":
				files = append(files, path)
			}
			return nil
		})
	} else {
		files = append(files, sourcePath)
	}

	var hardPaths, weakCrypto, clearHTTP, intentBypass []string
	for _, fp := range files {
		raw, err := os.ReadFile(fp)
		if err != nil || len(raw) == 0 {
			continue
		}
		txt := string(raw)
		if absPathRE.MatchString(txt) {
			hardPaths = append(hardPaths, fp)
		}
		if weakCryptoRE.MatchString(strings.ToLower(txt)) {
			weakCrypto = append(weakCrypto, fp)
		}
		if missingTLSRE.MatchString(strings.ToLower(txt)) {
			clearHTTP = append(clearHTTP, fp)
		}
		if intentBypassRE.MatchString(strings.ToLower(txt)) {
			intentBypass = append(intentBypass, fp)
		}
	}

	var findings []vulnerabilityFinding
	if len(hardPaths) > 0 {
		findings = append(findings, vulnerabilityFinding{
			Rule:     "Hardcoded paths",
			Severity: "medium",
			Files:    dedupe(hardPaths),
			Note:     "Prefer configurable path policies for Enterprise/Federal portability.",
		})
	}
	if len(weakCrypto) > 0 {
		findings = append(findings, vulnerabilityFinding{
			Rule:     "Weak cryptography markers",
			Severity: "high",
			Files:    dedupe(weakCrypto),
			Note:     "For Federal readiness, enforce modern cryptography and encryption at rest.",
		})
	}
	if len(clearHTTP) > 0 {
		findings = append(findings, vulnerabilityFinding{
			Rule:     "Cleartext transport (http://)",
			Severity: "high",
			Files:    dedupe(clearHTTP),
			Note:     "Align with Sasswall access-control posture by defaulting to authenticated TLS channels.",
		})
	}
	if len(intentBypass) > 0 {
		findings = append(findings, vulnerabilityFinding{
			Rule:     "Intent-analysis bypass marker",
			Severity: "high",
			Files:    dedupe(intentBypass),
			Note:     "Sasswall Intent Analysis benchmark flags bypass patterns for hardening backlog.",
		})
	}
	return findings
}

func buildAdvisoryLine(r labResult, findings []vulnerabilityFinding) string {
	target := strings.TrimSpace(r.SourcePath)
	if target == "" {
		target = "module"
	}
	id := strings.TrimSpace(r.BuildID)
	if id == "" {
		id = strings.TrimSpace(r.TaskID)
	}
	if id == "" {
		id = "latest"
	}
	return fmt.Sprintf(
		"Sir, this module is production-ready, but for a Federal deployment, we'll eventually need to harden the entry point. I've noted this in the Compliance Backlog. (build=%s target=%s findings=%d, benchmark=Sasswall Access Control+Intent Analysis)",
		id, filepath.Base(target), len(findings),
	)
}

func (d *daemon) loadState() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	raw, err := os.ReadFile(d.statePath)
	if err != nil {
		if os.IsNotExist(err) {
			d.state = daemonState{}
			return nil
		}
		return err
	}
	var st daemonState
	if err := json.Unmarshal(raw, &st); err != nil {
		return err
	}
	d.state = st
	return nil
}

func (d *daemon) persistState() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if err := os.MkdirAll(filepath.Dir(d.statePath), 0o755); err != nil {
		return err
	}
	b, _ := json.MarshalIndent(d.state, "", "  ")
	return os.WriteFile(d.statePath, b, 0o644)
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

func mustJSON(v interface{}) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func dedupe(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, v := range in {
		v = strings.TrimSpace(v)
		if v == "" || seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	return out
}
