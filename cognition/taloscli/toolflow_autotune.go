package taloscli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Thynaptic/P-LMv1/pkg/toolflow"
)

const (
	toolflowAutotuneEnabledEnv = "TALOS_TOOLFLOW_AUTOTUNE_ENABLED"
	toolflowAutotunePathEnv    = "TALOS_TOOLFLOW_AUTOTUNE_PATH"
	defaultAutotunePath        = ".memory/toolflow_autotune.json"
	autotuneProfileVersion     = 1
)

type autotuneBandProfile struct {
	Samples         int            `json:"samples"`
	Successes       int            `json:"successes"`
	AvgDurationMS   float64        `json:"avg_duration_ms"`
	RecommendedArgs map[string]int `json:"recommended_args,omitempty"`
}

type autotuneProfile struct {
	Version   int                                       `json:"version"`
	UpdatedAt time.Time                                 `json:"updated_at"`
	Tools     map[string]map[string]autotuneBandProfile `json:"tools"`
}

type toolflowParamOptimizer struct {
	mu      sync.Mutex
	path    string
	profile autotuneProfile
}

var (
	toolflowAutotuneOnce sync.Once
	toolflowAutotuneInst *toolflowParamOptimizer
	toolflowAutotuneErr  error
)

func toolflowAutotuneEnabled() bool {
	return envBoolDefault(toolflowAutotuneEnabledEnv, true)
}

func toolflowAutotunePath() string {
	raw := strings.TrimSpace(os.Getenv(toolflowAutotunePathEnv))
	if raw == "" {
		return defaultAutotunePath
	}
	return raw
}

func globalToolflowParamOptimizer() (*toolflowParamOptimizer, error) {
	if !toolflowAutotuneEnabled() {
		return nil, nil
	}
	toolflowAutotuneOnce.Do(func() {
		toolflowAutotuneInst, toolflowAutotuneErr = newToolflowParamOptimizer(toolflowAutotunePath())
	})
	return toolflowAutotuneInst, toolflowAutotuneErr
}

func newToolflowParamOptimizer(path string) (*toolflowParamOptimizer, error) {
	if strings.TrimSpace(path) == "" {
		return nil, fmt.Errorf("autotune path is required")
	}
	o := &toolflowParamOptimizer{
		path: path,
		profile: autotuneProfile{
			Version:   autotuneProfileVersion,
			UpdatedAt: time.Now().UTC(),
			Tools:     map[string]map[string]autotuneBandProfile{},
		},
	}
	if err := o.load(); err != nil {
		return nil, err
	}
	return o, nil
}

func (o *toolflowParamOptimizer) Apply(invocations []toolflow.Invocation, loadRatio float64) ([]toolflow.Invocation, []string, error) {
	if o == nil {
		return invocations, nil, nil
	}
	o.mu.Lock()
	defer o.mu.Unlock()

	band := loadBand(loadRatio)
	out := make([]toolflow.Invocation, 0, len(invocations))
	notes := make([]string, 0, len(invocations))
	for _, in := range invocations {
		args := cloneArgs(in.Args)
		changed := false
		localNotes := make([]string, 0, 3)

		for key, v := range o.recommendedArgs(in.Tool, band) {
			if getIntArg(args, key) > 0 {
				continue
			}
			args[key] = v
			changed = true
			localNotes = append(localNotes, fmt.Sprintf("%s.%s=%d", in.Tool, key, v))
		}
		for key, v := range defaultsByBand(in.Tool, band) {
			if getIntArg(args, key) > 0 {
				continue
			}
			args[key] = v
			changed = true
			localNotes = append(localNotes, fmt.Sprintf("%s.%s=%d", in.Tool, key, v))
		}
		capNotes, capped := capArgsByBand(in.Tool, args, band)
		if capped {
			changed = true
			localNotes = append(localNotes, capNotes...)
		}
		depthNotes, depthCapped := capDepthArgsByBand(args, band)
		if depthCapped {
			changed = true
			localNotes = append(localNotes, prefixNotes(in.Tool, depthNotes)...)
		}
		if changed {
			notes = append(notes, localNotes...)
		}
		out = append(out, toolflow.Invocation{
			Tool:   in.Tool,
			Args:   args,
			Source: in.Source,
			Raw:    in.Raw,
		})
	}
	return out, trimNotes(notes, 8), nil
}

func (o *toolflowParamOptimizer) Learn(plan toolflow.Plan, results []toolflow.ToolResult, loadRatio float64) ([]string, error) {
	if o == nil {
		return nil, nil
	}
	o.mu.Lock()
	defer o.mu.Unlock()

	nodeArgs := make(map[string]map[string]interface{}, len(plan.Nodes))
	for _, n := range plan.Nodes {
		nodeArgs[n.ID] = n.Args
	}
	band := loadBand(loadRatio)
	notes := make([]string, 0, len(results))
	changed := false
	for _, r := range results {
		args, ok := nodeArgs[r.NodeID]
		if !ok || len(args) == 0 {
			continue
		}
		prof := o.ensureBandProfile(r.Tool, band)
		prof.Samples++
		if r.Err == nil {
			prof.Successes++
		}
		if r.DurationMS > 0 {
			if prof.AvgDurationMS <= 0 {
				prof.AvgDurationMS = float64(r.DurationMS)
			} else {
				prof.AvgDurationMS = (0.75 * prof.AvgDurationMS) + (0.25 * float64(r.DurationMS))
			}
		}
		if prof.RecommendedArgs == nil {
			prof.RecommendedArgs = map[string]int{}
		}
		if updateRecommendedTimeout(prof.RecommendedArgs, args, r) {
			changed = true
			notes = append(notes, fmt.Sprintf("%s.timeout_seconds=%d", r.Tool, prof.RecommendedArgs["timeout_seconds"]))
		}
		if updateRecommendedTopK(prof.RecommendedArgs, args, r, band) {
			changed = true
			notes = append(notes, fmt.Sprintf("%s.top_k=%d", r.Tool, prof.RecommendedArgs["top_k"]))
		}
		if updateRecommendedMaxChars(prof.RecommendedArgs, args, r, band) {
			changed = true
			notes = append(notes, fmt.Sprintf("%s.max_chars=%d", r.Tool, prof.RecommendedArgs["max_chars"]))
		}
		if depthUpdates := updateRecommendedDepthArgs(prof.RecommendedArgs, args, r, band); len(depthUpdates) > 0 {
			changed = true
			notes = append(notes, prefixNotes(r.Tool, depthUpdates)...)
		}
		o.profile.Tools[r.Tool][band] = *prof
	}
	if changed {
		o.profile.UpdatedAt = time.Now().UTC()
		if err := o.save(); err != nil {
			return nil, err
		}
	}
	return trimNotes(notes, 8), nil
}

func (o *toolflowParamOptimizer) load() error {
	b, err := os.ReadFile(o.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("toolflow autotune: read profile: %w", err)
	}
	var p autotuneProfile
	if err := json.Unmarshal(b, &p); err != nil {
		return fmt.Errorf("toolflow autotune: decode profile: %w", err)
	}
	if p.Tools == nil {
		p.Tools = map[string]map[string]autotuneBandProfile{}
	}
	if p.Version == 0 {
		p.Version = autotuneProfileVersion
	}
	o.profile = p
	return nil
}

func (o *toolflowParamOptimizer) save() error {
	if err := os.MkdirAll(filepath.Dir(o.path), 0o755); err != nil {
		return fmt.Errorf("toolflow autotune: create dir: %w", err)
	}
	b, err := json.MarshalIndent(o.profile, "", "  ")
	if err != nil {
		return fmt.Errorf("toolflow autotune: encode profile: %w", err)
	}
	if err := os.WriteFile(o.path, b, 0o644); err != nil {
		return fmt.Errorf("toolflow autotune: write profile: %w", err)
	}
	return nil
}

func (o *toolflowParamOptimizer) ensureBandProfile(toolName, band string) *autotuneBandProfile {
	if o.profile.Tools == nil {
		o.profile.Tools = map[string]map[string]autotuneBandProfile{}
	}
	toolBands := o.profile.Tools[toolName]
	if toolBands == nil {
		toolBands = map[string]autotuneBandProfile{}
		o.profile.Tools[toolName] = toolBands
	}
	prof := toolBands[band]
	if prof.RecommendedArgs == nil {
		prof.RecommendedArgs = map[string]int{}
	}
	return &prof
}

func (o *toolflowParamOptimizer) recommendedArgs(toolName, band string) map[string]int {
	toolBands := o.profile.Tools[toolName]
	if toolBands == nil {
		return nil
	}
	prof, ok := toolBands[band]
	if !ok || len(prof.RecommendedArgs) == 0 {
		return nil
	}
	out := make(map[string]int, len(prof.RecommendedArgs))
	for k, v := range prof.RecommendedArgs {
		out[k] = v
	}
	return out
}

func loadBand(load float64) string {
	if load < 0.55 {
		return "low"
	}
	if load < 0.85 {
		return "medium"
	}
	return "high"
}

func defaultsByBand(toolName, band string) map[string]int {
	switch toolName {
	case "web_search":
		switch band {
		case "high":
			return map[string]int{"top_k": 4, "recency_days": 7}
		case "medium":
			return map[string]int{"top_k": 6, "recency_days": 14}
		default:
			return map[string]int{"top_k": 8, "recency_days": 21}
		}
	case "vector_retrieve":
		switch band {
		case "high":
			return map[string]int{"top_k": 4}
		case "medium":
			return map[string]int{"top_k": 6}
		default:
			return map[string]int{"top_k": 8}
		}
	case "fetch_url":
		switch band {
		case "high":
			return map[string]int{"max_chars": 8000}
		case "medium":
			return map[string]int{"max_chars": 12000}
		default:
			return map[string]int{"max_chars": 20000}
		}
	case "execute_code", "sys_exec", "capture_screen":
		switch band {
		case "high":
			return map[string]int{"timeout_seconds": 12}
		case "medium":
			return map[string]int{"timeout_seconds": 18}
		default:
			return map[string]int{"timeout_seconds": 24}
		}
	default:
		return nil
	}
}

func capArgsByBand(toolName string, args map[string]interface{}, band string) ([]string, bool) {
	notes := make([]string, 0, 2)
	changed := false

	switch toolName {
	case "web_search", "vector_retrieve":
		maxTopK := 10
		if band == "medium" {
			maxTopK = 6
		}
		if band == "high" {
			maxTopK = 4
		}
		if v := getIntArg(args, "top_k"); v > maxTopK {
			args["top_k"] = maxTopK
			changed = true
			notes = append(notes, fmt.Sprintf("%s.top_k=%d", toolName, maxTopK))
		}
	case "fetch_url":
		maxChars := 24000
		if band == "medium" {
			maxChars = 12000
		}
		if band == "high" {
			maxChars = 8000
		}
		if v := getIntArg(args, "max_chars"); v > maxChars {
			args["max_chars"] = maxChars
			changed = true
			notes = append(notes, fmt.Sprintf("%s.max_chars=%d", toolName, maxChars))
		}
	case "execute_code", "sys_exec", "capture_screen":
		maxTimeout := 30
		if band == "medium" {
			maxTimeout = 20
		}
		if band == "high" {
			maxTimeout = 12
		}
		if v := getIntArg(args, "timeout_seconds"); v > maxTimeout {
			args["timeout_seconds"] = maxTimeout
			changed = true
			notes = append(notes, fmt.Sprintf("%s.timeout_seconds=%d", toolName, maxTimeout))
		}
	}
	return notes, changed
}

func capDepthArgsByBand(args map[string]interface{}, band string) ([]string, bool) {
	notes := make([]string, 0, 2)
	changed := false
	depthMax, pagesMax := depthCapsByBand(band)
	for _, key := range []string{"depth", "max_depth", "crawl_depth", "search_depth"} {
		v := getIntArg(args, key)
		if v <= 0 {
			continue
		}
		capped := clampIntAuto(v, 1, depthMax)
		if capped != v {
			args[key] = capped
			changed = true
			notes = append(notes, fmt.Sprintf("%s=%d", key, capped))
		}
	}
	if v := getIntArg(args, "max_pages"); v > 0 {
		capped := clampIntAuto(v, 5, pagesMax)
		if capped != v {
			args["max_pages"] = capped
			changed = true
			notes = append(notes, fmt.Sprintf("max_pages=%d", capped))
		}
	}
	return notes, changed
}

func updateRecommendedTimeout(reco map[string]int, args map[string]interface{}, r toolflow.ToolResult) bool {
	current := getIntArg(args, "timeout_seconds")
	if current <= 0 {
		current = reco["timeout_seconds"]
	}
	if current <= 0 {
		return false
	}
	next := current
	if isTimeoutResult(r) {
		next = minIntAuto(60, current+2)
	} else if r.Err == nil && r.DurationMS > 0 && r.DurationMS < int64(current*450) && current > 6 {
		next = current - 1
	}
	if next <= 0 || reco["timeout_seconds"] == next {
		return false
	}
	reco["timeout_seconds"] = next
	return true
}

func updateRecommendedTopK(reco map[string]int, args map[string]interface{}, r toolflow.ToolResult, band string) bool {
	current := getIntArg(args, "top_k")
	if current <= 0 {
		current = reco["top_k"]
	}
	if current <= 0 {
		return false
	}
	next := current
	if band == "high" && next > 4 {
		next = 4
	}
	if r.Err == nil && r.DurationMS > 0 {
		if r.DurationMS < 1200 && next < 12 {
			next++
		}
		if r.DurationMS > 7000 && next > 3 {
			next--
		}
	}
	if isTimeoutResult(r) && next > 3 {
		next--
	}
	next = clampIntAuto(next, 3, 12)
	if reco["top_k"] == next {
		return false
	}
	reco["top_k"] = next
	return true
}

func updateRecommendedMaxChars(reco map[string]int, args map[string]interface{}, r toolflow.ToolResult, band string) bool {
	current := getIntArg(args, "max_chars")
	if current <= 0 {
		current = reco["max_chars"]
	}
	if current <= 0 {
		return false
	}
	next := current
	if band == "high" && next > 8000 {
		next = 8000
	}
	if r.Err == nil && r.DurationMS > 0 {
		if r.DurationMS < 1200 && next < 32000 {
			next += 1000
		}
		if r.DurationMS > 7000 && next > 6000 {
			next -= 1000
		}
	}
	next = clampIntAuto(next, 4000, 32000)
	if reco["max_chars"] == next {
		return false
	}
	reco["max_chars"] = next
	return true
}

func updateRecommendedDepthArgs(reco map[string]int, args map[string]interface{}, r toolflow.ToolResult, band string) []string {
	updates := make([]string, 0, 2)
	depthMax, pagesMax := depthCapsByBand(band)

	for _, key := range []string{"depth", "max_depth", "crawl_depth", "search_depth"} {
		current := getIntArg(args, key)
		if current <= 0 {
			current = reco[key]
		}
		if current <= 0 {
			continue
		}
		next := current
		if isTimeoutResult(r) || r.DurationMS > 7000 {
			next--
		} else if r.Err == nil && r.DurationMS > 0 && r.DurationMS < 1500 {
			next++
		}
		next = clampIntAuto(next, 1, depthMax)
		if reco[key] == next {
			continue
		}
		reco[key] = next
		updates = append(updates, fmt.Sprintf("%s=%d", key, next))
	}

	currentPages := getIntArg(args, "max_pages")
	if currentPages <= 0 {
		currentPages = reco["max_pages"]
	}
	if currentPages > 0 {
		next := currentPages
		if isTimeoutResult(r) || r.DurationMS > 7000 {
			next -= 10
		} else if r.Err == nil && r.DurationMS > 0 && r.DurationMS < 1500 {
			next += 10
		}
		next = clampIntAuto(next, 5, pagesMax)
		if reco["max_pages"] != next {
			reco["max_pages"] = next
			updates = append(updates, fmt.Sprintf("max_pages=%d", next))
		}
	}

	return updates
}

func depthCapsByBand(band string) (depthMax int, maxPages int) {
	switch band {
	case "high":
		return 2, 30
	case "medium":
		return 3, 60
	default:
		return 5, 120
	}
}

func isTimeoutResult(r toolflow.ToolResult) bool {
	msg := strings.ToLower(strings.TrimSpace(r.ErrMessage))
	if r.Err != nil {
		msg = strings.ToLower(strings.TrimSpace(r.Err.Error() + " " + msg))
	}
	return strings.Contains(msg, "timeout") || strings.Contains(msg, "deadline exceeded")
}

func detectToolflowCPULoadRatio() (float64, error) {
	b, err := os.ReadFile("/proc/loadavg")
	if err != nil {
		return 0.25, fmt.Errorf("read /proc/loadavg: %w", err)
	}
	fields := strings.Fields(strings.TrimSpace(string(b)))
	if len(fields) == 0 {
		return 0.25, fmt.Errorf("loadavg format invalid")
	}
	load1, err := strconv.ParseFloat(fields[0], 64)
	if err != nil {
		return 0.25, fmt.Errorf("parse loadavg: %w", err)
	}
	cores := float64(runtime.NumCPU())
	if cores <= 0 {
		cores = 1
	}
	return clampFloatAuto(load1 / cores), nil
}

func formatAutotuneStatus(loadRatio float64, applyNotes []string, learnNotes []string, warnings []string) string {
	b := strings.Builder{}
	b.WriteString(fmt.Sprintf("AUTOTUNE: load=%d%% applied=%d learned=%d warnings=%d\n", int(loadRatio*100), len(applyNotes), len(learnNotes), len(warnings)))
	if len(applyNotes) > 0 {
		b.WriteString("AUTOTUNE_APPLIED: " + strings.Join(applyNotes, ", ") + "\n")
	}
	if len(learnNotes) > 0 {
		b.WriteString("AUTOTUNE_LEARNED: " + strings.Join(learnNotes, ", ") + "\n")
	}
	if len(warnings) > 0 {
		b.WriteString("AUTOTUNE_WARN: " + strings.Join(warnings, " | ") + "\n")
	}
	return b.String()
}

func cloneArgs(args map[string]interface{}) map[string]interface{} {
	if len(args) == 0 {
		return map[string]interface{}{}
	}
	out := make(map[string]interface{}, len(args))
	for k, v := range args {
		out[k] = v
	}
	return out
}

func getIntArg(args map[string]interface{}, key string) int {
	if len(args) == 0 {
		return 0
	}
	v, ok := args[key]
	if !ok {
		return 0
	}
	switch n := v.(type) {
	case int:
		return n
	case int32:
		return int(n)
	case int64:
		return int(n)
	case float32:
		return int(n)
	case float64:
		return int(n)
	case string:
		n = strings.TrimSpace(n)
		if n == "" {
			return 0
		}
		p, err := strconv.Atoi(n)
		if err != nil {
			return 0
		}
		return p
	default:
		return 0
	}
}

func clampFloatAuto(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

func clampIntAuto(v, minV, maxV int) int {
	if v < minV {
		return minV
	}
	if v > maxV {
		return maxV
	}
	return v
}

func minIntAuto(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func trimNotes(in []string, limit int) []string {
	if len(in) == 0 || limit <= 0 {
		return nil
	}
	seen := map[string]bool{}
	out := make([]string, 0, minIntAuto(len(in), limit))
	for _, n := range in {
		n = strings.TrimSpace(n)
		if n == "" || seen[n] {
			continue
		}
		seen[n] = true
		out = append(out, n)
		if len(out) >= limit {
			break
		}
	}
	return out
}

func prefixNotes(prefix string, notes []string) []string {
	if len(notes) == 0 {
		return nil
	}
	out := make([]string, 0, len(notes))
	for _, n := range notes {
		n = strings.TrimSpace(n)
		if n == "" {
			continue
		}
		out = append(out, fmt.Sprintf("%s.%s", prefix, n))
	}
	return out
}
