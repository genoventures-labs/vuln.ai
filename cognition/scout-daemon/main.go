package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"math"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/Thynaptic/P-LMv1/pkg/cognition"
	"github.com/Thynaptic/P-LMv1/pkg/envload"
	"github.com/Thynaptic/P-LMv1/pkg/orchestration"
	"github.com/Thynaptic/P-LMv1/pkg/skills"
	"github.com/Thynaptic/P-LMv1/pkg/tools"
)

const (
	defaultInboxDir          = ".memory/documentary/inbox"
	defaultMirrorBriefsPath  = ".memory/reasoning_mirror_briefs.jsonl"
	defaultScoutStatePath    = ".memory/scout_daemon/state.json"
	defaultScoutWorkLogPath  = ".memory/scout_daemon/work_orders.jsonl"
	defaultScoutOracleLog    = ".memory/scout_daemon/oracle_deadlocks.jsonl"
	defaultScoutTick         = 20 * time.Second
	defaultArchiveThreshold  = 0.68
	defaultEscalateThreshold = 0.76
)

var amountRE = regexp.MustCompile(`(?i)(?:\$|usd\s*)?\s*([0-9]{1,3}(?:,[0-9]{3})*(?:\.[0-9]{1,2})?|[0-9]+(?:\.[0-9]{1,2})?)`)

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

type scoutState struct {
	LastScanAt        time.Time              `json:"last_scan_at"`
	Primed            bool                   `json:"primed"`
	Seen              map[string]trackedFile `json:"seen"`
	ProcessedCount    int                    `json:"processed_count"`
	Escalations       int                    `json:"escalations"`
	ExtractorRequests int                    `json:"extractor_requests"`
}

type scoutDaemon struct {
	inboxDir        string
	watchDirs       []string
	tick            time.Duration
	mirrorBriefPath string
	statePath       string
	workLogPath     string
	oracleLogPath   string

	archiveThreshold  float64
	escalateThreshold float64

	mu            sync.Mutex
	state         scoutState
	lastBriefSent time.Time
}

type sourceConflict struct {
	CurrentKind  string
	CurrentValue float64
	CurrentYear  string
	RefKind      string
	RefValue     float64
	RefYear      string
	Discrepancy  float64
	Ref          orchestration.ReasoningMatch
}

type lowConfidenceSignal struct {
	Kind       string
	Confidence float64
	Label      string
}

func main() {
	if err := envload.Autoload(); err != nil {
		fmt.Fprintf(os.Stderr, "warning: env autoload failed: %v\n", err)
	}

	var (
		inboxDir          = flag.String("inbox", defaultInboxDir, "documentary inbox directory")
		watchDirsCSV      = flag.String("watch-dirs", ".", "comma-separated additional directories to tail for file changes")
		tick              = flag.Duration("tick", defaultScoutTick, "scan interval")
		mirrorPath        = flag.String("mirror-briefs", defaultMirrorBriefsPath, "mirror brief JSONL path")
		statePath         = flag.String("state-path", defaultScoutStatePath, "daemon state path")
		workLogPath       = flag.String("work-log", defaultScoutWorkLogPath, "tool request log path")
		archiveThreshold  = flag.Float64("archive-threshold", defaultArchiveThreshold, "minimum archivist similarity to cross-reference")
		escalateThreshold = flag.Float64("escalate-threshold", defaultEscalateThreshold, "contradiction threshold to escalate")
	)
	flag.Parse()

	d := &scoutDaemon{
		inboxDir:          strings.TrimSpace(*inboxDir),
		watchDirs:         splitCSVPaths(*watchDirsCSV),
		tick:              *tick,
		mirrorBriefPath:   strings.TrimSpace(*mirrorPath),
		statePath:         strings.TrimSpace(*statePath),
		workLogPath:       strings.TrimSpace(*workLogPath),
		oracleLogPath:     defaultScoutOracleLog,
		archiveThreshold:  clamp01(*archiveThreshold),
		escalateThreshold: clamp01(*escalateThreshold),
		lastBriefSent:     time.Time{},
	}
	if d.inboxDir == "" {
		d.inboxDir = defaultInboxDir
	}
	if len(d.watchDirs) == 0 {
		d.watchDirs = []string{"."}
	}
	if d.tick <= 0 {
		d.tick = defaultScoutTick
	}
	if err := d.loadState(); err != nil {
		fmt.Fprintf(os.Stderr, "scout state load warning: %v\n", err)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	fmt.Printf("glm-scoutd online. inbox=%s watch=%v\n", d.inboxDir, d.watchDirs)
	if err := d.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
		fmt.Fprintf(os.Stderr, "glm-scoutd exited with error: %v\n", err)
		os.Exit(1)
	}
}

func (d *scoutDaemon) Run(ctx context.Context) error {
	ticker := time.NewTicker(d.tick)
	defer ticker.Stop()

	for {
		if err := d.scanOnce(ctx); err != nil {
			fmt.Printf("glm-scoutd: scan warning: %v\n", err)
		}
		select {
		case <-ctx.Done():
			_ = d.persistState()
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func (d *scoutDaemon) scanOnce(ctx context.Context) error {
	paths, err := d.collectChangedFiles()
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

	// Prime baseline on first run, then start proactive actions.
	if !primed {
		_ = d.persistState()
		return nil
	}

	for _, p := range paths {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		d.processFile(p)
	}
	d.emitPeriodicBrief()
	return d.persistState()
}

func (d *scoutDaemon) collectChangedFiles() ([]string, error) {
	if d.state.Seen == nil {
		d.state.Seen = map[string]trackedFile{}
	}
	rootSet := map[string]bool{}
	rootSet[d.inboxDir] = true
	for _, w := range d.watchDirs {
		if w = strings.TrimSpace(w); w != "" {
			rootSet[w] = true
		}
	}
	var roots []string
	for r := range rootSet {
		roots = append(roots, r)
	}
	sort.Strings(roots)

	var changed []string
	for _, root := range roots {
		root = strings.TrimSpace(root)
		if root == "" {
			continue
		}
		info, err := os.Stat(root)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, err
		}
		if info.IsDir() {
			_ = filepath.WalkDir(root, func(path string, de os.DirEntry, walkErr error) error {
				if walkErr != nil {
					return nil
				}
				if de.IsDir() {
					if shouldSkipDir(path) {
						return filepath.SkipDir
					}
					return nil
				}
				if shouldIgnoreFile(path) {
					return nil
				}
				stat, err := de.Info()
				if err != nil {
					return nil
				}
				tf := trackedFile{Path: path, ModUnix: stat.ModTime().Unix(), Size: stat.Size()}
				prev, ok := d.state.Seen[path]
				if !ok || prev.ModUnix != tf.ModUnix || prev.Size != tf.Size {
					changed = append(changed, path)
				}
				d.state.Seen[path] = tf
				return nil
			})
		} else {
			tf := trackedFile{Path: root, ModUnix: info.ModTime().Unix(), Size: info.Size()}
			prev, ok := d.state.Seen[root]
			if !ok || prev.ModUnix != tf.ModUnix || prev.Size != tf.Size {
				changed = append(changed, root)
			}
			d.state.Seen[root] = tf
		}
	}
	sort.Strings(changed)
	return dedupeStrings(changed), nil
}

func (d *scoutDaemon) processFile(path string) {
	path = strings.TrimSpace(path)
	if path == "" {
		return
	}
	query := d.buildCrossRefQuery(path)
	matches, err := orchestration.QueryReasoningArchive(query, 4)
	if err != nil {
		return
	}

	claim := d.buildClaim(path)
	contradicted := false
	for _, m := range matches {
		if m.Similarity < d.archiveThreshold {
			continue
		}
		ref := strings.TrimSpace(m.Record.Reasoning)
		if ref == "" {
			ref = strings.TrimSpace(m.Record.Query)
		}
		if ref == "" {
			continue
		}
		score := cognition.DetectContradiction(claim, ref)
		if score >= d.escalateThreshold {
			contradicted = true
			d.raiseEscalationBrief(path, score, m)
			break
		}
	}

	needsExtractor := d.needsExtractor(path, claim, contradicted)
	if needsExtractor {
		d.dispatchExtractorWorkOrder(path)
	}
	if conflict, ok := d.detectCrossSourceConflict(path, matches); ok {
		d.handleCrossSourceConflict(path, conflict)
	}
	if signal, ok := d.detectLowConfidenceHighValue(path); ok {
		d.handleLowConfidenceHighValue(path, signal)
	}

	d.mu.Lock()
	d.state.ProcessedCount++
	d.mu.Unlock()
}

func (d *scoutDaemon) buildCrossRefQuery(path string) string {
	base := filepath.Base(path)
	ext := strings.ToLower(filepath.Ext(path))
	snippet := d.readSnippet(path, 1800)
	if snippet == "" {
		snippet = base
	}
	return strings.TrimSpace("file " + base + " " + ext + " " + snippet)
}

func (d *scoutDaemon) buildClaim(path string) string {
	base := filepath.Base(path)
	snippet := d.readSnippet(path, 800)
	if snippet == "" {
		snippet = "opaque or non-text artifact"
	}
	return fmt.Sprintf("New artifact %s indicates: %s", base, snippet)
}

func (d *scoutDaemon) needsExtractor(path, claim string, contradicted bool) bool {
	ext := strings.ToLower(filepath.Ext(path))
	supportedText := map[string]bool{
		".txt": true, ".md": true, ".json": true, ".yaml": true, ".yml": true, ".log": true, ".csv": true, ".go": true,
	}
	supportedDocVisual := map[string]bool{
		".pdf": true, ".png": true, ".jpg": true, ".jpeg": true, ".webp": true, ".bmp": true, ".tiff": true,
	}
	if supportedText[ext] {
		return false
	}
	if supportedDocVisual[ext] {
		// Try documentary parsing for inbox artifacts; if parsing fails, request extractor.
		if strings.HasPrefix(path, d.inboxDir) {
			dp := &skills.DocumentaryProcessor{}
			_, err := dp.ProcessArtifact(skills.DocumentaryArtifact{Path: path, SourceType: "scout_probe"})
			return err != nil
		}
		return false
	}
	if strings.TrimSpace(claim) == "" {
		return true
	}
	return contradicted || true
}

func (d *scoutDaemon) dispatchExtractorWorkOrder(path string) {
	status := "disabled"
	toolName := ""
	entry := map[string]interface{}{
		"timestamp": time.Now().UTC().Format(time.RFC3339),
		"path":      path,
		"status":    status,
		"tool":      toolName,
		"error":     "runtime local builder disabled; use glm-toolserver tool plugins",
	}
	b, _ := json.Marshal(entry)
	_ = appendJSONL(d.workLogPath, string(b))

	d.mu.Lock()
	d.state.ExtractorRequests++
	d.mu.Unlock()

	msg := fmt.Sprintf("Scout couldn't parse %s cleanly. Request a GLM toolserver plugin for extraction (%s).", baseName(path), status)
	_ = d.emitMirrorBrief(msg)
}

func (d *scoutDaemon) detectLowConfidenceHighValue(path string) (lowConfidenceSignal, bool) {
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".pdf", ".png", ".jpg", ".jpeg", ".webp", ".bmp", ".tiff":
	default:
		return lowConfidenceSignal{}, false
	}
	dp := &skills.DocumentaryProcessor{}
	res, err := dp.ProcessArtifact(skills.DocumentaryArtifact{Path: path, SourceType: "scout_quality_check"})
	if err != nil {
		return lowConfidenceSignal{}, false
	}

	best := lowConfidenceSignal{Confidence: 2.0}
	mark := func(kind, label string, conf float64) {
		if conf < best.Confidence {
			best = lowConfidenceSignal{
				Kind:       strings.TrimSpace(kind),
				Label:      strings.TrimSpace(label),
				Confidence: conf,
			}
		}
	}

	for _, hv := range res.HighValueEntities {
		k := strings.ToLower(strings.TrimSpace(hv.Kind))
		v := strings.ToLower(strings.TrimSpace(hv.Value))
		if strings.Contains(k, "signature") || strings.Contains(k, "stamp") ||
			strings.Contains(v, "signature") || strings.Contains(v, "bank") || strings.Contains(v, "total") {
			mark(hv.Kind, hv.Value, hv.Confidence)
		}
	}
	for _, vr := range res.VisualReasoning {
		for _, el := range vr.SpatialElements {
			lbl := strings.ToLower(strings.TrimSpace(el.Label))
			if strings.Contains(lbl, "signature") || strings.Contains(lbl, "bank") ||
				strings.Contains(lbl, "total") || strings.Contains(lbl, "$") {
				mark(el.Kind, el.Label, el.Confidence)
			}
		}
	}
	if best.Confidence < 0.80 {
		return best, true
	}
	return lowConfidenceSignal{}, false
}

func (d *scoutDaemon) handleLowConfidenceHighValue(path string, sig lowConfidenceSignal) {
	year := extractPrimaryYear(baseName(path) + " " + d.readSnippet(path, 1200))
	if year == "" {
		year = "latest"
	}
	ledgerOrDoc := "document scan"
	if strings.Contains(strings.ToLower(baseName(path)), "ledger") {
		ledgerOrDoc = "ledger scan"
	}
	status, _ := d.dispatchDenoiseWorkOrder(path, sig)
	msg := fmt.Sprintf("The %s %s is noisy. Runtime local builder is disabled; request a GLM toolserver denoise plugin before re-scan.", year, ledgerOrDoc)
	if strings.TrimSpace(status) != "" {
		msg += " (" + strings.ToLower(strings.TrimSpace(status)) + ")"
	}
	_ = d.emitMirrorBrief(msg)

	// Second-look pass after tool manufacturing request.
	dp := &skills.DocumentaryProcessor{}
	_, _ = dp.ProcessArtifact(skills.DocumentaryArtifact{
		Path:       path,
		SourceType: "scout_second_look",
		Metadata: map[string]string{
			"quality_signal": "low_confidence_high_value",
			"signal_kind":    strings.TrimSpace(sig.Kind),
			"signal_label":   strings.TrimSpace(sig.Label),
		},
	})
}

func (d *scoutDaemon) dispatchDenoiseWorkOrder(path string, sig lowConfidenceSignal) (string, error) {
	status := "disabled"
	toolName := ""
	entry := map[string]interface{}{
		"timestamp":    time.Now().UTC().Format(time.RFC3339),
		"path":         path,
		"status":       status,
		"tool":         toolName,
		"type":         "denoise_second_look",
		"signal_kind":  sig.Kind,
		"signal_label": sig.Label,
		"confidence":   sig.Confidence,
		"error":        "runtime local builder disabled; use glm-toolserver tool plugins",
	}
	_ = appendJSONL(d.workLogPath, mustJSON(entry))
	d.mu.Lock()
	d.state.ExtractorRequests++
	d.mu.Unlock()
	return status, nil
}

func (d *scoutDaemon) raiseEscalationBrief(path string, score float64, m orchestration.ReasoningMatch) {
	msg := fmt.Sprintf(
		"Sir, I've found a signature in %s that conflicts with archived reasoning (%.0f%% contradiction). Escalating to the Executive for analysis.",
		baseName(path),
		score*100,
	)
	_ = d.emitMirrorBrief(msg)
	d.mu.Lock()
	d.state.Escalations++
	d.mu.Unlock()
}

func (d *scoutDaemon) detectCrossSourceConflict(path string, matches []orchestration.ReasoningMatch) (sourceConflict, bool) {
	base := strings.ToLower(baseName(path))
	snippet := d.readSnippet(path, 2400)
	curKind := classifySourceKind(base + " " + snippet)
	curVal, ok := extractPrimaryAmount(snippet)
	if !ok {
		return sourceConflict{}, false
	}
	curYear := extractPrimaryYear(base + " " + snippet)

	var best sourceConflict
	found := false
	for _, m := range matches {
		if m.Similarity < d.archiveThreshold*0.85 {
			continue
		}
		refText := strings.TrimSpace(m.Record.Reasoning + " " + m.Record.Query)
		if refText == "" {
			continue
		}
		refKind := classifySourceKind(strings.ToLower(m.Record.Query + " " + m.Record.Reasoning))
		if !isCrossSourcePair(curKind, refKind) {
			continue
		}
		refVal, rok := extractPrimaryAmount(refText)
		if !rok {
			continue
		}
		discrepancy := calculateDiscrepancy(curVal, refVal)
		if discrepancy < 0.20 {
			continue
		}
		refYear := extractPrimaryYear(refText)
		c := sourceConflict{
			CurrentKind:  curKind,
			CurrentValue: curVal,
			CurrentYear:  curYear,
			RefKind:      refKind,
			RefValue:     refVal,
			RefYear:      refYear,
			Discrepancy:  discrepancy,
			Ref:          m,
		}
		if !found || c.Discrepancy > best.Discrepancy {
			best = c
			found = true
		}
	}
	return best, found
}

func (d *scoutDaemon) handleCrossSourceConflict(path string, c sourceConflict) {
	curYear := strings.TrimSpace(c.CurrentYear)
	if curYear == "" {
		curYear = strings.TrimSpace(c.RefYear)
	}
	if curYear == "" {
		curYear = "recent"
	}
	msg := fmt.Sprintf(
		"I've cross-referenced the %s %s with the %s %s. There's a %.0f%% discrepancy. Staging an 'Operation Deep Truth' report.",
		curYear, humanSourceKind(c.CurrentKind),
		fallbackYear(c.RefYear, curYear), humanSourceKind(c.RefKind),
		c.Discrepancy*100,
	)
	_ = d.emitMirrorBrief(msg)
	d.mu.Lock()
	d.state.Escalations++
	d.mu.Unlock()

	archTrace := buildArchivistDecisionTrace(c.Ref)
	conflictReport := map[string]interface{}{
		"path":              path,
		"current_kind":      c.CurrentKind,
		"current_value":     c.CurrentValue,
		"current_year":      c.CurrentYear,
		"reference_kind":    c.RefKind,
		"reference_value":   c.RefValue,
		"reference_year":    c.RefYear,
		"discrepancy_ratio": c.Discrepancy,
		"discrepancy_pct":   c.Discrepancy * 100,
	}
	proofID := sanitizeProofID(fmt.Sprintf("%s_%s_%d", baseName(path), strings.ReplaceAll(c.CurrentKind, " ", "_"), time.Now().Unix()))
	proof := skills.CaptureForensicProof(skills.ForensicProofRequest{
		Consent:             true,
		ProofID:             proofID,
		Mode:                "screen",
		Caption:             "Smoking Gun composite archival from Scout cross-source audit",
		ArchivistTrace:      archTrace,
		ScoutConflictReport: conflictReport,
		TimeoutS:            15,
	})
	if proof.ExitCode == 0 {
		_ = d.emitMirrorBrief("Smoking Gun archived. I've captured the discrepancy between the ledger and the bank log with the evidence web overlay. Packet stored in .memory/evidence_packets/.")
		_ = appendJSONL(d.oracleLogPath, mustJSON(map[string]interface{}{
			"timestamp":     time.Now().UTC().Format(time.RFC3339),
			"path":          path,
			"proof_id":      proof.ProofID,
			"proof_image":   proof.ImagePath,
			"proof_sidecar": proof.SidecarPath,
		}))
	}

	graphState := cognition.OracleGraphState{
		WorkOrder: cognition.OracleWorkOrder{
			ID:    "scout-conflict-" + strconv.FormatInt(time.Now().UnixNano(), 10),
			Goal:  "Resolve cross-source financial discrepancy to establish historical truth.",
			Phase: "conflict-audit",
			Owner: "glm-scoutd",
			Prompt: fmt.Sprintf(
				"Conflict in %s: %s reports %.2f, %s reports %.2f (%.0f%% discrepancy).",
				baseName(path), c.CurrentKind, c.CurrentValue, c.RefKind, c.RefValue, c.Discrepancy*100,
			),
			FailureNote: "Operation Deep Truth triggered due to conflicting financial records.",
			Metadata: map[string]interface{}{
				"path":          path,
				"current_kind":  c.CurrentKind,
				"current_value": c.CurrentValue,
				"ref_kind":      c.RefKind,
				"ref_value":     c.RefValue,
				"discrepancy":   c.Discrepancy,
			},
		},
		SymbolicVetoCount:  3,
		SymbolicVetoBlock:  baseName(path),
		SymbolicVetoReason: fmt.Sprintf("Cross-source discrepancy %.0f%% between %s and %s.", c.Discrepancy*100, c.CurrentKind, c.RefKind),
		MCTSConfidence:     0.25,
		MCTSExpansions:     3,
		MCTSMaxExpansions:  3,
		MaxRetriesHit:      true,
	}

	deadlockText, _ := cognition.PackageDeadlockContext(graphState)
	oraclePkg, _ := cognition.BuildOraclePackage(graphState)
	pkgPath, _ := cognition.PersistOraclePackage(oraclePkg, filepath.Join(".memory", "scout_daemon", "operation_deep_truth_last.json"))
	_ = appendJSONL(d.oracleLogPath, mustJSON(map[string]interface{}{
		"timestamp":        time.Now().UTC().Format(time.RFC3339),
		"path":             path,
		"discrepancy":      c.Discrepancy,
		"deadlock_context": deadlockText,
		"package_path":     pkgPath,
	}))

	if !cognition.ShouldCallOracle(graphState) {
		return
	}
	payload := tools.OraclePayload{
		FailedWorkOrder: map[string]interface{}{
			"id":    graphState.WorkOrder.ID,
			"goal":  graphState.WorkOrder.Goal,
			"phase": graphState.WorkOrder.Phase,
			"path":  path,
		},
		MirrorLines: []string{
			fmt.Sprintf("Detected %.0f%% discrepancy between %s and %s.", c.Discrepancy*100, c.CurrentKind, c.RefKind),
			"Operation Deep Truth staged for Oracle arbitration.",
		},
		VetoReason: graphState.SymbolicVetoReason,
		Metadata: map[string]interface{}{
			"mode":            "cross_source_audit",
			"operation":       "deep_truth",
			"discrepancy_pct": c.Discrepancy * 100,
			"context_path":    pkgPath,
		},
	}
	if strings.TrimSpace(deadlockText) != "" {
		payload.MirrorLines = append(payload.MirrorLines, deadlockText)
	}
	_, err := tools.DispatchToOracle(payload)
	if err != nil {
		_ = appendJSONL(d.oracleLogPath, mustJSON(map[string]interface{}{
			"timestamp": time.Now().UTC().Format(time.RFC3339),
			"path":      path,
			"status":    "oracle_dispatch_failed",
			"error":     err.Error(),
		}))
		return
	}
	_ = d.emitMirrorBrief("Operation Deep Truth escalated to Oracle for historical truth arbitration.")
}

func (d *scoutDaemon) emitPeriodicBrief() {
	d.mu.Lock()
	defer d.mu.Unlock()
	now := time.Now().UTC()
	if now.Sub(d.lastBriefSent) < 2*time.Minute {
		return
	}
	msg := fmt.Sprintf(
		"Scout status: scanned %d changes, escalations %d, extractor work orders %d.",
		d.state.ProcessedCount,
		d.state.Escalations,
		d.state.ExtractorRequests,
	)
	if err := d.emitMirrorBrief(msg); err == nil {
		d.lastBriefSent = now
	}
}

func (d *scoutDaemon) emitMirrorBrief(msg string) error {
	return appendJSONL(d.mirrorBriefPath, mustJSON(mirrorBrief{
		Timestamp: time.Now().UTC(),
		Source:    "glm-scoutd",
		Message:   strings.TrimSpace(msg),
	}))
}

func (d *scoutDaemon) readSnippet(path string, max int) string {
	ext := strings.ToLower(filepath.Ext(path))
	if !isTextLike(ext) {
		return ""
	}
	b, err := os.ReadFile(path)
	if err != nil || len(b) == 0 {
		return ""
	}
	s := string(b)
	if max > 0 && len(s) > max {
		s = s[:max]
	}
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.Join(strings.Fields(s), " ")
	return strings.TrimSpace(s)
}

func (d *scoutDaemon) loadState() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if strings.TrimSpace(d.statePath) == "" {
		d.statePath = defaultScoutStatePath
	}
	raw, err := os.ReadFile(d.statePath)
	if err != nil {
		if os.IsNotExist(err) {
			d.state = scoutState{Seen: map[string]trackedFile{}}
			return nil
		}
		return err
	}
	var s scoutState
	if err := json.Unmarshal(raw, &s); err != nil {
		return err
	}
	if s.Seen == nil {
		s.Seen = map[string]trackedFile{}
	}
	d.state = s
	return nil
}

func (d *scoutDaemon) persistState() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if strings.TrimSpace(d.statePath) == "" {
		d.statePath = defaultScoutStatePath
	}
	if err := os.MkdirAll(filepath.Dir(d.statePath), 0o755); err != nil {
		return err
	}
	b, _ := json.MarshalIndent(d.state, "", "  ")
	return os.WriteFile(d.statePath, b, 0o644)
}

func shouldSkipDir(path string) bool {
	p := filepath.ToSlash(strings.ToLower(strings.TrimSpace(path)))
	for _, s := range []string{"/.git", "/.memory/chromem-go", "/node_modules", "/bin/manufactured"} {
		if strings.Contains(p, s) {
			return true
		}
	}
	return false
}

func shouldIgnoreFile(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".tmp", ".swp", ".lock":
		return true
	default:
		return false
	}
}

func splitCSVPaths(v string) []string {
	var out []string
	for _, p := range strings.Split(v, ",") {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		out = append(out, p)
	}
	return dedupeStrings(out)
}

func dedupeStrings(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, v := range in {
		k := strings.TrimSpace(v)
		if k == "" || seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, k)
	}
	return out
}

func isTextLike(ext string) bool {
	switch strings.ToLower(strings.TrimSpace(ext)) {
	case ".txt", ".md", ".json", ".yaml", ".yml", ".log", ".csv", ".go", ".py", ".ts", ".js", ".svelte":
		return true
	default:
		return false
	}
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

func errString(err error) string {
	if err == nil {
		return ""
	}
	return strings.TrimSpace(err.Error())
}

func baseName(path string) string {
	return strings.TrimSpace(filepath.Base(path))
}

func clamp01(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

func classifySourceKind(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	switch {
	case strings.Contains(s, "ledger"):
		return "ledger"
	case strings.Contains(s, "bank"):
		return "bank logs"
	case strings.Contains(s, "payroll"):
		return "payroll files"
	case strings.Contains(s, "statement"):
		return "statement"
	default:
		return "record"
	}
}

func humanSourceKind(k string) string {
	k = strings.TrimSpace(k)
	if k == "" || k == "record" {
		return "records"
	}
	return k
}

func isCrossSourcePair(a, b string) bool {
	a = strings.TrimSpace(a)
	b = strings.TrimSpace(b)
	if a == "" || b == "" || a == b {
		return false
	}
	pairs := [][2]string{
		{"ledger", "bank logs"},
		{"ledger", "payroll files"},
		{"bank logs", "payroll files"},
		{"ledger", "statement"},
		{"bank logs", "statement"},
	}
	for _, p := range pairs {
		if (a == p[0] && b == p[1]) || (a == p[1] && b == p[0]) {
			return true
		}
	}
	return false
}

func extractPrimaryAmount(s string) (float64, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, false
	}
	matches := amountRE.FindAllStringSubmatch(s, -1)
	best := 0.0
	found := false
	for _, m := range matches {
		if len(m) < 2 {
			continue
		}
		raw := strings.ReplaceAll(strings.TrimSpace(m[1]), ",", "")
		v, err := strconv.ParseFloat(raw, 64)
		if err != nil || v <= 0 {
			continue
		}
		if !found || v > best {
			best = v
			found = true
		}
	}
	return best, found
}

func calculateDiscrepancy(a, b float64) float64 {
	if a <= 0 || b <= 0 {
		return 0
	}
	return math.Abs(a-b) / math.Max(a, b)
}

func extractPrimaryYear(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	re := regexp.MustCompile(`\b(19[7-9][0-9]|20[0-3][0-9])\b`)
	m := re.FindStringSubmatch(s)
	if len(m) == 2 {
		return m[1]
	}
	return ""
}

func fallbackYear(preferred, fallback string) string {
	preferred = strings.TrimSpace(preferred)
	if preferred != "" {
		return preferred
	}
	fallback = strings.TrimSpace(fallback)
	if fallback != "" {
		return fallback
	}
	return "recent"
}

func nonEmpty(v, fallback string) string {
	v = strings.TrimSpace(v)
	if v != "" {
		return v
	}
	return strings.TrimSpace(fallback)
}

func buildArchivistDecisionTrace(m orchestration.ReasoningMatch) []string {
	var out []string
	if q := strings.TrimSpace(m.Record.Query); q != "" {
		out = append(out, "Query: "+q)
	}
	if r := strings.TrimSpace(m.Record.Reasoning); r != "" {
		out = append(out, "Reasoning: "+r)
	}
	if p := strings.TrimSpace(m.Record.ChosenPath); p != "" {
		out = append(out, "ChosenPath: "+p)
	}
	if t := strings.TrimSpace(m.Record.Topology); t != "" {
		out = append(out, "Topology: "+t)
	}
	if len(m.Record.Alternatives) > 0 {
		out = append(out, "Alternatives: "+strings.Join(m.Record.Alternatives, ", "))
	}
	if len(out) == 0 {
		out = append(out, "No archivist trace available for this match.")
	}
	return out
}

func sanitizeProofID(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" {
		return "proof"
	}
	var b strings.Builder
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			b.WriteRune(r)
		} else {
			b.WriteRune('_')
		}
	}
	out := strings.Trim(b.String(), "_-")
	if out == "" {
		return "proof"
	}
	return out
}
