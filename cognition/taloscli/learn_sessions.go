package taloscli

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const defaultLearnSessionsPath = ".memory/learn_sessions.jsonl"
const defaultNativeGroundingPath = ".memory/native_text_grounding.jsonl"

var learnSessionsPath = defaultLearnSessionsPath
var nativeGroundingPath = defaultNativeGroundingPath

type LearnSessionRecord struct {
	SessionID      string            `json:"session_id"`
	StartedAt      string            `json:"started_at"`
	FinishedAt     string            `json:"finished_at"`
	Status         string            `json:"status"`
	Mode           string            `json:"mode"`
	QueryOrTarget  string            `json:"query_or_target,omitempty"`
	Sources        []string          `json:"sources,omitempty"`
	Metrics        map[string]int64  `json:"metrics"`
	Summary        string            `json:"summary"`
	FailureReason  string            `json:"failure_reason,omitempty"`
	ConfigSnapshot map[string]string `json:"config,omitempty"`
}

type NativeGroundingRecord struct {
	SessionID     string   `json:"session_id"`
	Mode          string   `json:"mode"`
	Status        string   `json:"status"`
	QueryOrTarget string   `json:"query_or_target,omitempty"`
	Summary       string   `json:"summary"`
	Sources       []string `json:"sources,omitempty"`
	CapturedAt    string   `json:"captured_at"`
}

func newLearnSession(mode, target string, sources []string, config map[string]string) LearnSessionRecord {
	now := time.Now().UTC()
	if config == nil {
		config = map[string]string{}
	}
	return LearnSessionRecord{
		SessionID:      fmt.Sprintf("learn-%d", now.UnixNano()),
		StartedAt:      now.Format(time.RFC3339),
		Status:         "FAILED",
		Mode:           strings.ToUpper(strings.TrimSpace(mode)),
		QueryOrTarget:  strings.TrimSpace(target),
		Sources:        uniqueStrings(sources),
		Metrics:        map[string]int64{},
		ConfigSnapshot: config,
		Summary:        "No summary recorded.",
	}
}

func (r *LearnSessionRecord) finish(status, summary, failureReason string, metrics map[string]int64) {
	if r == nil {
		return
	}
	r.FinishedAt = time.Now().UTC().Format(time.RFC3339)
	r.Status = strings.ToUpper(strings.TrimSpace(status))
	r.Summary = strings.TrimSpace(summary)
	if r.Summary == "" {
		r.Summary = "No summary recorded."
	}
	r.FailureReason = strings.TrimSpace(failureReason)
	if metrics != nil {
		r.Metrics = metrics
	}
}

func appendLearnSessionRecord(rec LearnSessionRecord) error {
	if err := os.MkdirAll(filepath.Dir(learnSessionsPath), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(learnSessionsPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	if err := enc.Encode(rec); err != nil {
		return err
	}
	_ = appendNativeGroundingRecord(rec)
	return nil
}

func readLearnSessionRecords() ([]LearnSessionRecord, int, error) {
	f, err := os.Open(learnSessionsPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, 0, nil
		}
		return nil, 0, err
	}
	defer f.Close()

	var out []LearnSessionRecord
	corrupt := 0
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var rec LearnSessionRecord
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			corrupt++
			continue
		}
		out = append(out, rec)
	}
	if err := sc.Err(); err != nil {
		return nil, 0, err
	}
	return out, corrupt, nil
}

func appendNativeGroundingRecord(rec LearnSessionRecord) error {
	gr := NativeGroundingRecord{
		SessionID:     strings.TrimSpace(rec.SessionID),
		Mode:          strings.TrimSpace(rec.Mode),
		Status:        strings.TrimSpace(rec.Status),
		QueryOrTarget: strings.TrimSpace(rec.QueryOrTarget),
		Summary:       strings.TrimSpace(rec.Summary),
		Sources:       uniqueStrings(rec.Sources),
		CapturedAt:    time.Now().UTC().Format(time.RFC3339),
	}
	if gr.Summary == "" {
		gr.Summary = "No summary recorded."
	}
	if err := os.MkdirAll(filepath.Dir(nativeGroundingPath), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(nativeGroundingPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	return enc.Encode(gr)
}

func readNativeGroundingRecords() ([]NativeGroundingRecord, int, error) {
	f, err := os.Open(nativeGroundingPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, 0, nil
		}
		return nil, 0, err
	}
	defer f.Close()

	var out []NativeGroundingRecord
	corrupt := 0
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var rec NativeGroundingRecord
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			corrupt++
			continue
		}
		out = append(out, rec)
	}
	if err := sc.Err(); err != nil {
		return nil, 0, err
	}
	return out, corrupt, nil
}

func filterLearnSessionRecords(in []LearnSessionRecord, status string, modes map[string]bool, last int) []LearnSessionRecord {
	status = strings.ToUpper(strings.TrimSpace(status))
	out := make([]LearnSessionRecord, 0, len(in))
	for _, r := range in {
		if status != "" && status != "ALL" && strings.ToUpper(r.Status) != status {
			continue
		}
		if len(modes) > 0 && !modes[strings.ToUpper(strings.TrimSpace(r.Mode))] {
			continue
		}
		out = append(out, r)
	}

	sort.SliceStable(out, func(i, j int) bool {
		return out[i].StartedAt > out[j].StartedAt
	})

	if last > 0 && len(out) > last {
		out = out[:last]
	}
	return out
}

func renderLearnedReport(records []LearnSessionRecord, requestedLast int, corruptCount int) string {
	var b strings.Builder
	b.WriteString("TALOS LEARNED REPORT\n\n")
	b.WriteString("COMMAND\n")
	b.WriteString("  talos learned\n\n")
	b.WriteString("WINDOW\n")
	b.WriteString(fmt.Sprintf("  requested_last: %d\n", requestedLast))
	b.WriteString(fmt.Sprintf("  returned_sessions: %d\n", len(records)))
	b.WriteString(fmt.Sprintf("  corrupt_records_skipped: %d\n\n", corruptCount))

	if len(records) == 0 {
		b.WriteString("STATUS\n")
		b.WriteString("  SUCCESS\n\n")
		b.WriteString("SESSION SNAPSHOT\n")
		b.WriteString("  No learn sessions recorded yet.\n\n")
		b.WriteString("NEXT ACTION\n")
		b.WriteString("  Run `talos learn` or `talos learn-image` to create session history.")
		return b.String()
	}

	b.WriteString("SESSION SNAPSHOT\n")
	total := map[string]int64{}
	sourceFreq := map[string]int{}
	for i, r := range records {
		b.WriteString(fmt.Sprintf("  %d) %s | %s | %s\n", i+1, r.StartedAt, strings.ToUpper(r.Status), strings.ToUpper(r.Mode)))
		if strings.TrimSpace(r.QueryOrTarget) != "" {
			b.WriteString("     target: " + strings.TrimSpace(r.QueryOrTarget) + "\n")
		}
		if strings.TrimSpace(r.Summary) != "" {
			b.WriteString("     summary: " + strings.TrimSpace(r.Summary) + "\n")
		}
		if strings.TrimSpace(r.FailureReason) != "" {
			b.WriteString("     failure_reason: " + strings.TrimSpace(r.FailureReason) + "\n")
		}
		if len(r.Metrics) > 0 {
			metricKeys := make([]string, 0, len(r.Metrics))
			for k := range r.Metrics {
				metricKeys = append(metricKeys, k)
			}
			sort.Strings(metricKeys)
			for _, k := range metricKeys {
				v := r.Metrics[k]
				total[k] += v
				b.WriteString(fmt.Sprintf("     %s: %d\n", k, v))
			}
		}
		for _, s := range r.Sources {
			s = strings.TrimSpace(s)
			if s != "" {
				sourceFreq[s]++
			}
		}
	}

	b.WriteString("\nAGGREGATE METRICS\n")
	if len(total) == 0 {
		b.WriteString("  No aggregate metrics available.\n")
	} else {
		keys := make([]string, 0, len(total))
		for k := range total {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			b.WriteString(fmt.Sprintf("  %s: %d\n", k, total[k]))
		}
	}

	b.WriteString("\nTOP SOURCES\n")
	if len(sourceFreq) == 0 {
		b.WriteString("  No sources recorded.\n")
	} else {
		type kv struct {
			K string
			V int
		}
		var items []kv
		for k, v := range sourceFreq {
			items = append(items, kv{K: k, V: v})
		}
		sort.SliceStable(items, func(i, j int) bool {
			if items[i].V == items[j].V {
				return items[i].K < items[j].K
			}
			return items[i].V > items[j].V
		})
		limit := 5
		if len(items) < limit {
			limit = len(items)
		}
		for i := 0; i < limit; i++ {
			b.WriteString(fmt.Sprintf("  - %s (sessions: %d)\n", items[i].K, items[i].V))
		}
	}

	b.WriteString("\nOBSERVATIONS\n")
	failed := 0
	partial := 0
	for _, r := range records {
		switch strings.ToUpper(r.Status) {
		case "FAILED":
			failed++
		case "PARTIAL":
			partial++
		}
	}
	b.WriteString("\nSTATUS\n")
	if failed > 0 {
		b.WriteString("  FAILED\n")
	} else if partial > 0 {
		b.WriteString("  PARTIAL\n")
	} else {
		b.WriteString("  SUCCESS\n")
	}
	b.WriteString(fmt.Sprintf("  Processed %d session(s); failed=%d, partial=%d.\n", len(records), failed, partial))
	if failed > 0 {
		b.WriteString("  Investigate repeated failures before the next large training run.\n")
	}
	return b.String()
}
