package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/Thynaptic/P-LMv1/pkg/envload"
	"github.com/Thynaptic/P-LMv1/pkg/orchestration"
)

type archiveDaemon struct {
	feedPath   string
	briefsPath string
	poll       time.Duration

	manifestDir string

	mu          sync.Mutex
	offset      int64
	lastBriefAt time.Time
	lastToolCnt int
	archivedRun int
}

func main() {
	if err := envload.Autoload(); err != nil {
		fmt.Fprintf(os.Stderr, "warning: env autoload failed: %v\n", err)
	}

	var (
		feedPath    = flag.String("decision-feed", ".memory/decision_feed.jsonl", "decision feed JSONL")
		briefsPath  = flag.String("briefs-file", ".memory/archive_mirror_briefs.jsonl", "mirror briefs JSONL")
		poll        = flag.Duration("poll", 2*time.Second, "poll interval")
		manifestDir = flag.String("manifests-dir", "bin/manufactured/manifests", "capability manifests directory")
	)
	flag.Parse()

	d := &archiveDaemon{
		feedPath:    strings.TrimSpace(*feedPath),
		briefsPath:  strings.TrimSpace(*briefsPath),
		poll:        *poll,
		manifestDir: strings.TrimSpace(*manifestDir),
	}
	d.lastToolCnt = d.countTools()

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	fmt.Printf("glm-archived online. feed=%s\n", d.feedPath)
	if err := d.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
		fmt.Fprintf(os.Stderr, "glm-archived exited with error: %v\n", err)
		os.Exit(1)
	}
}

func (d *archiveDaemon) Run(ctx context.Context) error {
	if d.poll <= 0 {
		d.poll = 2 * time.Second
	}
	tick := time.NewTicker(d.poll)
	defer tick.Stop()
	for {
		if err := d.processDecisionFeed(); err != nil && !errors.Is(err, os.ErrNotExist) {
			fmt.Printf("glm-archived: feed warning: %v\n", err)
		}
		d.maybeEmitStatusBrief()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-tick.C:
		}
	}
}

func (d *archiveDaemon) processDecisionFeed() error {
	records, err := d.readNewRecords()
	if err != nil {
		return err
	}
	if len(records) == 0 {
		return nil
	}
	for _, r := range records {
		if strings.TrimSpace(r.Query) == "" || strings.TrimSpace(r.Reasoning) == "" {
			continue
		}
		if err := orchestration.ArchiveDecisionRecord(r); err != nil {
			fmt.Printf("glm-archived: archive write failed: %v\n", err)
			continue
		}
		d.mu.Lock()
		d.archivedRun++
		d.mu.Unlock()
	}
	return nil
}

func (d *archiveDaemon) readNewRecords() ([]orchestration.DecisionRecord, error) {
	path := strings.TrimSpace(d.feedPath)
	if path == "" {
		path = ".memory/decision_feed.jsonl"
	}
	st, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	d.mu.Lock()
	if st.Size() < d.offset {
		d.offset = 0
	}
	start := d.offset
	d.mu.Unlock()

	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	if _, err := f.Seek(start, io.SeekStart); err != nil {
		return nil, err
	}
	chunk, err := io.ReadAll(f)
	if err != nil {
		return nil, err
	}
	d.mu.Lock()
	d.offset = start + int64(len(chunk))
	d.mu.Unlock()

	var out []orchestration.DecisionRecord
	for _, ln := range strings.Split(string(chunk), "\n") {
		ln = strings.TrimSpace(ln)
		if ln == "" {
			continue
		}
		var rec orchestration.DecisionRecord
		if err := json.Unmarshal([]byte(ln), &rec); err != nil {
			continue
		}
		out = append(out, rec)
	}
	return out, nil
}

func (d *archiveDaemon) maybeEmitStatusBrief() {
	d.mu.Lock()
	defer d.mu.Unlock()
	now := time.Now().UTC()
	if now.Sub(d.lastBriefAt) < 90*time.Second {
		return
	}
	currentTools := d.countTools()
	added := currentTools - d.lastToolCnt
	if added < 0 {
		added = 0
	}
	if d.archivedRun == 0 && added == 0 {
		return
	}
	msg := fmt.Sprintf("I've archived today's manufacturing runs; we've added %d new tools to the arsenal.", added)
	_ = orchestration.AppendArchiveMirrorBrief(d.briefsPath, orchestration.MirrorBrief{
		Timestamp: now,
		Source:    "glm-archived",
		Message:   msg,
	})
	d.lastBriefAt = now
	d.lastToolCnt = currentTools
	d.archivedRun = 0
}

func (d *archiveDaemon) countTools() int {
	base := strings.TrimSpace(d.manifestDir)
	if base == "" {
		base = "bin/manufactured/manifests"
	}
	entries, err := os.ReadDir(base)
	if err != nil {
		return 0
	}
	count := 0
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if strings.HasSuffix(strings.ToLower(e.Name()), ".json") {
			count++
		}
	}
	return count
}

func init() {
	_ = os.MkdirAll(filepath.Join(".memory"), 0o755)
}
