package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Thynaptic/P-LMv1/pkg/state"
	chromem "github.com/philippgille/chromem-go"
)

const (
	defaultReindexInterval       = 30 * time.Minute
	defaultArchiveAfterDays      = 30
	defaultArchiveMinImportance  = 0.20
	defaultGoalAlignmentWeight   = 0.35
	defaultImportanceCarryWeight = 0.55
	defaultReplayWeight          = 0.10
)

// Reindexer re-ranks and clusters long-term memory in background.
type Reindexer struct {
	mm *MemoryManager
	sm *state.Manager

	Interval             time.Duration
	ArchiveAfterDays     int
	ArchiveMinImportance float64

	stop chan struct{}
	wg   sync.WaitGroup
}

// NewReindexer creates a reindexer bound to memory + session state.
func NewReindexer(mm *MemoryManager, sm *state.Manager) *Reindexer {
	return &Reindexer{
		mm:                   mm,
		sm:                   sm,
		Interval:             defaultReindexInterval,
		ArchiveAfterDays:     defaultArchiveAfterDays,
		ArchiveMinImportance: defaultArchiveMinImportance,
	}
}

// Start begins periodic contextual re-indexing.
func (r *Reindexer) Start() {
	if r == nil || r.mm == nil {
		return
	}
	if r.Interval <= 0 {
		r.Interval = defaultReindexInterval
	}
	if r.stop != nil {
		return
	}
	r.stop = make(chan struct{})
	r.wg.Add(1)
	go func() {
		defer r.wg.Done()
		t := time.NewTicker(r.Interval)
		defer t.Stop()
		for {
			select {
			case <-t.C:
				_ = r.RunOnce()
			case <-r.stop:
				return
			}
		}
	}()
}

// Stop ends periodic re-indexing.
func (r *Reindexer) Stop() {
	if r == nil || r.stop == nil {
		return
	}
	close(r.stop)
	r.stop = nil
	r.wg.Wait()
}

// RunOnce executes one full contextual re-index pass.
func (r *Reindexer) RunOnce() error {
	if r == nil || r.mm == nil {
		return fmt.Errorf("reindexer missing memory manager")
	}
	now := time.Now().UTC()
	goal := ""
	if r.sm != nil {
		goal = strings.TrimSpace(r.sm.GetSnapshot().PrimaryGoal)
	}

	if err := r.reindexCollection(r.mm.historyCollection, goal, now); err != nil {
		return err
	}
	if err := r.reindexCollection(r.mm.knowledgeCollection, goal, now); err != nil {
		return err
	}
	_ = r.promoteLearnedSkills()
	return nil
}

func (r *Reindexer) reindexCollection(col *chromem.Collection, primaryGoal string, now time.Time) error {
	docs, err := enumerateCollection(col)
	if err != nil {
		return err
	}
	if len(docs) == 0 {
		return nil
	}

	type docPatch struct {
		doc          chromem.Document
		clusterID    string
		clusterLabel string
	}
	patches := make([]docPatch, 0, len(docs))
	clusterSize := make(map[string]int)

	for _, d := range docs {
		doc := d
		if doc.Metadata == nil {
			doc.Metadata = make(map[string]string)
		}

		currentImportance := parseMetaFloat(doc.Metadata, metaBaseImportance, defaultBaseImportance)
		goalAlign := scoreGoalAlignment(primaryGoal, doc.Content, doc.Metadata)
		retrievalBoost := math.Min(0.30, math.Log1p(float64(parseMetaInt(doc.Metadata, metaRetrievalCount, 0)))*0.08)

		newImportance := clamp01(
			(defaultImportanceCarryWeight * currentImportance) +
				(defaultGoalAlignmentWeight * goalAlign) +
				(defaultReplayWeight * retrievalBoost),
		)
		doc.Metadata[metaBaseImportance] = formatFloat(newImportance)
		doc.Metadata[metaLastReindexedAt] = now.Format(time.RFC3339)
		if _, ok := doc.Metadata[metaTimestamp]; !ok {
			doc.Metadata[metaTimestamp] = now.Format(time.RFC3339)
		}

		// Cluster assignment.
		label := inferClusterLabel(doc, primaryGoal)
		cid := clusterIDForLabel(label)
		doc.Metadata[metaClusterID] = cid
		doc.Metadata[metaClusterLabel] = label
		clusterSize[cid]++

		// Archival marking.
		staleDays := stalenessDays(doc.Metadata, now)
		archive := newImportance <= r.archiveMinImportance() && staleDays >= r.archiveAfterDays()
		if archive {
			doc.Metadata[metaArchived] = "true"
			doc.Metadata[metaArchiveReason] = fmt.Sprintf("stale_%dd_low_importance", staleDays)
		} else {
			doc.Metadata[metaArchived] = "false"
			delete(doc.Metadata, metaArchiveReason)
		}

		patches = append(patches, docPatch{
			doc:          doc,
			clusterID:    cid,
			clusterLabel: label,
		})
	}

	// Finalize cluster sizes and persist.
	for _, p := range patches {
		size := clusterSize[p.clusterID]
		p.doc.Metadata[metaClusterSize] = fmt.Sprintf("%d", size)
		if err := col.AddDocument(context.Background(), p.doc); err != nil {
			continue
		}
	}
	return nil
}

func enumerateCollection(col *chromem.Collection) ([]chromem.Document, error) {
	count := col.Count()
	if count == 0 {
		return nil, nil
	}
	res, err := col.Query(context.Background(), " ", count, nil, nil)
	if err != nil {
		return nil, err
	}
	out := make([]chromem.Document, 0, len(res))
	for _, r := range res {
		d, err := col.GetByID(context.Background(), r.ID)
		if err != nil {
			continue
		}
		out = append(out, d)
	}
	return out, nil
}

func scoreGoalAlignment(goal, content string, metadata map[string]string) float64 {
	goal = strings.TrimSpace(goal)
	if goal == "" {
		return 0.5
	}
	tGoal := tokenizeGoal(goal)
	if len(tGoal) == 0 {
		return 0.5
	}

	haystack := strings.ToLower(content)
	for _, k := range []string{"source_path", "source_type", "cluster_label"} {
		if v := strings.TrimSpace(metadata[k]); v != "" {
			haystack += " " + strings.ToLower(v)
		}
	}

	match := 0
	for _, t := range tGoal {
		if strings.Contains(haystack, t) {
			match++
		}
	}
	return clamp01(float64(match) / float64(len(tGoal)))
}

func tokenizeGoal(s string) []string {
	s = strings.ToLower(strings.TrimSpace(s))
	repl := strings.NewReplacer(",", " ", ".", " ", ":", " ", ";", " ", "/", " ", "-", " ", "_", " ")
	s = repl.Replace(s)
	stop := map[string]bool{
		"the": true, "and": true, "for": true, "with": true, "into": true, "from": true,
		"this": true, "that": true, "your": true, "our": true, "project": true,
	}
	seen := make(map[string]bool)
	var out []string
	for _, p := range strings.Fields(s) {
		if len(p) < 3 || stop[p] || seen[p] {
			continue
		}
		seen[p] = true
		out = append(out, p)
	}
	return out
}

func inferClusterLabel(doc chromem.Document, primaryGoal string) string {
	if p := strings.TrimSpace(doc.Metadata["source_path"]); p != "" {
		return "src:" + p
	}
	if p := strings.TrimSpace(doc.Metadata[metaClusterLabel]); p != "" {
		return p
	}

	toks := topTokens(doc.Content, 3)
	if len(toks) > 0 {
		return "topic:" + strings.Join(toks, "_")
	}
	if g := strings.TrimSpace(primaryGoal); g != "" {
		gt := tokenizeGoal(g)
		if len(gt) > 0 {
			return "goal:" + strings.Join(gt[:minInt(2, len(gt))], "_")
		}
	}
	return "general"
}

func topTokens(s string, n int) []string {
	s = strings.ToLower(s)
	repl := strings.NewReplacer(",", " ", ".", " ", ":", " ", ";", " ", "(", " ", ")", " ", "[", " ", "]", " ", "{", " ", "}", " ")
	s = repl.Replace(s)
	stop := map[string]bool{
		"the": true, "and": true, "for": true, "with": true, "that": true, "this": true, "from": true, "have": true, "been": true,
	}
	counts := make(map[string]int)
	for _, t := range strings.Fields(s) {
		if len(t) < 4 || stop[t] {
			continue
		}
		counts[t]++
	}
	type kv struct {
		K string
		V int
	}
	var arr []kv
	for k, v := range counts {
		arr = append(arr, kv{K: k, V: v})
	}
	sort.Slice(arr, func(i, j int) bool {
		if arr[i].V == arr[j].V {
			return arr[i].K < arr[j].K
		}
		return arr[i].V > arr[j].V
	})
	if len(arr) > n {
		arr = arr[:n]
	}
	out := make([]string, 0, len(arr))
	for _, e := range arr {
		out = append(out, e.K)
	}
	return out
}

func clusterIDForLabel(label string) string {
	h := fnv.New64a()
	_, _ = h.Write([]byte(strings.TrimSpace(strings.ToLower(label))))
	return fmt.Sprintf("c_%x", h.Sum64())
}

func stalenessDays(meta map[string]string, now time.Time) int {
	ref := parseMetaTime(meta, metaLastRetrievedAt)
	if ref == nil {
		ref = parseMetaTime(meta, metaTimestamp)
	}
	if ref == nil || ref.IsZero() || now.Before(*ref) {
		return 0
	}
	return int(now.Sub(*ref).Hours() / 24)
}

func (r *Reindexer) archiveAfterDays() int {
	if r.ArchiveAfterDays <= 0 {
		return defaultArchiveAfterDays
	}
	return r.ArchiveAfterDays
}

func (r *Reindexer) archiveMinImportance() float64 {
	if r.ArchiveMinImportance <= 0 {
		return defaultArchiveMinImportance
	}
	return clamp01(r.ArchiveMinImportance)
}

func (r *Reindexer) promoteLearnedSkills() error {
	tempRoot := filepath.Clean(".skills/temp")
	permRoot := filepath.Clean(".skills/permanent")
	if err := os.MkdirAll(tempRoot, 0o755); err != nil {
		return err
	}
	if err := os.MkdirAll(permRoot, 0o755); err != nil {
		return err
	}

	entries, err := os.ReadDir(tempRoot)
	if err != nil {
		return err
	}
	type skillMeta struct {
		ID       string `json:"id"`
		Name     string `json:"name"`
		UseCount int    `json:"use_count"`
	}

	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		srcDir := filepath.Join(tempRoot, e.Name())
		metaPath := filepath.Join(srcDir, "metadata.json")
		b, err := os.ReadFile(metaPath)
		if err != nil {
			continue
		}
		var m skillMeta
		if err := json.Unmarshal(b, &m); err != nil {
			continue
		}
		if m.UseCount <= 3 {
			continue
		}
		dstDir := filepath.Join(permRoot, e.Name())
		if _, err := os.Stat(dstDir); err == nil {
			_ = os.RemoveAll(srcDir)
			continue
		}
		if err := os.Rename(srcDir, dstDir); err != nil {
			continue
		}
	}
	return nil
}
