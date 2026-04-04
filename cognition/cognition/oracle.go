package cognition

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Thynaptic/P-LMv1/pkg/state"
)

const (
	defaultOracleMirrorPath = ".memory/reasoning_mirror_briefs.jsonl"
	defaultOracleQuotaPath  = ".memory/quota_usage.json"
)

// OracleWorkOrder is a compact snapshot of the currently failed unit of work.
type OracleWorkOrder struct {
	ID          string                 `json:"id,omitempty"`
	Goal        string                 `json:"goal,omitempty"`
	Phase       string                 `json:"phase,omitempty"`
	Owner       string                 `json:"owner,omitempty"`
	Prompt      string                 `json:"prompt,omitempty"`
	Metadata    map[string]interface{} `json:"metadata,omitempty"`
	FailureNote string                 `json:"failure_note,omitempty"`
}

// OracleGraphState captures the minimum state required for deadlock detection.
type OracleGraphState struct {
	WorkOrder OracleWorkOrder `json:"work_order"`

	// Symbolic deadlock inputs.
	SymbolicVetoCount  int    `json:"symbolic_veto_count"`
	SymbolicVetoBlock  string `json:"symbolic_veto_block,omitempty"`
	SymbolicVetoReason string `json:"symbolic_veto_reason,omitempty"`

	// MCTS deadlock inputs.
	MCTSConfidence    float64 `json:"mcts_confidence"`
	MCTSExpansions    int     `json:"mcts_expansions"`
	MCTSMaxExpansions int     `json:"mcts_max_expansions"`
	MaxRetriesHit     bool    `json:"max_retries_hit,omitempty"`
}

// OracleMirrorLine is one reasoning mirror line used for "why we're stuck" context.
type OracleMirrorLine struct {
	Timestamp time.Time `json:"timestamp,omitempty"`
	Source    string    `json:"source,omitempty"`
	Message   string    `json:"message"`
}

// OraclePackage is the context payload sent to external consensus systems.
type OraclePackage struct {
	Timestamp          time.Time          `json:"timestamp"`
	FailedWorkOrder    OracleWorkOrder    `json:"failed_work_order"`
	ReasoningMirror    []OracleMirrorLine `json:"reasoning_mirror"`
	SymbolicVetoReason string             `json:"symbolic_veto_reason,omitempty"`
	DeadlockSignals    map[string]bool    `json:"deadlock_signals,omitempty"`
}

// NeedsOracle is the lightweight trigger requested by the orchestration layer.
// It fires when confidence is critically low or retry budget is exhausted.
func NeedsOracle(confidence float64, retries int) bool {
	return confidence < 0.30 || retries > 2
}

// ShouldCallOracle returns true when deadlock conditions are met and quota safety allows escalation.
//
// Deadlock triggers:
// 1) Symbolic Auditor vetoed the same block >= 3 times.
// 2) MCTS confidence < 0.4 after max expansions.
//
// Quota gate:
// - Requires >5% daily quota remaining, using pkg/state/quota.go snapshot.
func ShouldCallOracle(graphState OracleGraphState) bool {
	return ShouldCallOracleWithQuotaPath(graphState, defaultOracleQuotaPath)
}

// ShouldCallOracleWithQuotaPath is the path-injectable variant of ShouldCallOracle.
func ShouldCallOracleWithQuotaPath(graphState OracleGraphState, quotaPath string) bool {
	hasSymbolicDeadlock := graphState.SymbolicVetoCount >= 3 && strings.TrimSpace(graphState.SymbolicVetoBlock) != ""
	hasMCTSDeadlock := graphState.MCTSMaxExpansions > 0 &&
		graphState.MCTSExpansions >= graphState.MCTSMaxExpansions &&
		graphState.MCTSConfidence < 0.40
	hasRetryDeadlock := graphState.MaxRetriesHit

	if !(hasSymbolicDeadlock || hasMCTSDeadlock || hasRetryDeadlock) {
		return false
	}
	return hasOracleQuota(quotaPath, 0.05)
}

// BuildOraclePackage aggregates the failed work order, mirror trace, and symbolic veto reason.
func BuildOraclePackage(graphState OracleGraphState) (OraclePackage, error) {
	return BuildOraclePackageWithMirrorPath(graphState, defaultOracleMirrorPath)
}

// BuildOraclePackageWithMirrorPath is the path-injectable variant of BuildOraclePackage.
func BuildOraclePackageWithMirrorPath(graphState OracleGraphState, mirrorPath string) (OraclePackage, error) {
	lines, err := readMirrorTail(mirrorPath, 10)
	if err != nil {
		return OraclePackage{}, err
	}
	pkg := OraclePackage{
		Timestamp:          time.Now().UTC(),
		FailedWorkOrder:    graphState.WorkOrder,
		ReasoningMirror:    lines,
		SymbolicVetoReason: strings.TrimSpace(graphState.SymbolicVetoReason),
		DeadlockSignals: map[string]bool{
			"symbolic_repeated_veto": graphState.SymbolicVetoCount >= 3 && strings.TrimSpace(graphState.SymbolicVetoBlock) != "",
			"mcts_low_confidence": graphState.MCTSMaxExpansions > 0 &&
				graphState.MCTSExpansions >= graphState.MCTSMaxExpansions &&
				graphState.MCTSConfidence < 0.40,
			"max_retries_hit": graphState.MaxRetriesHit,
		},
	}
	if pkg.SymbolicVetoReason == "" {
		pkg.SymbolicVetoReason = inferVetoReason(lines)
	}
	return pkg, nil
}

// PackageDeadlockContext returns a compact textual conflict package:
// - current work order
// - last 5 reasoning mirror lines
// - specific symbolic veto reason
func PackageDeadlockContext(graphState OracleGraphState) (string, error) {
	return PackageDeadlockContextWithMirrorPath(graphState, defaultOracleMirrorPath)
}

// PackageDeadlockContextWithMirrorPath is the path-injectable variant for tests/custom feeds.
func PackageDeadlockContextWithMirrorPath(graphState OracleGraphState, mirrorPath string) (string, error) {
	lines, err := readMirrorTail(mirrorPath, 5)
	if err != nil {
		return "", err
	}
	veto := strings.TrimSpace(graphState.SymbolicVetoReason)
	if veto == "" {
		veto = inferVetoReason(lines)
	}

	var b strings.Builder
	b.WriteString("Deadlock Context Package\n")
	b.WriteString("WorkOrder:\n")
	b.WriteString("- ID: " + strings.TrimSpace(graphState.WorkOrder.ID) + "\n")
	b.WriteString("- Goal: " + strings.TrimSpace(graphState.WorkOrder.Goal) + "\n")
	b.WriteString("- Phase: " + strings.TrimSpace(graphState.WorkOrder.Phase) + "\n")
	b.WriteString("- Owner: " + strings.TrimSpace(graphState.WorkOrder.Owner) + "\n")
	if p := strings.TrimSpace(graphState.WorkOrder.Prompt); p != "" {
		b.WriteString("- Prompt: " + p + "\n")
	}
	if n := strings.TrimSpace(graphState.WorkOrder.FailureNote); n != "" {
		b.WriteString("- FailureNote: " + n + "\n")
	}

	b.WriteString("ReasoningMirrorConflict (last 5):\n")
	if len(lines) == 0 {
		b.WriteString("- (no mirror lines available)\n")
	} else {
		for _, ln := range lines {
			msg := strings.TrimSpace(ln.Message)
			if msg == "" {
				continue
			}
			ts := ""
			if !ln.Timestamp.IsZero() {
				ts = ln.Timestamp.UTC().Format(time.RFC3339) + " "
			}
			src := strings.TrimSpace(ln.Source)
			if src != "" {
				src = "[" + src + "] "
			}
			b.WriteString("- " + ts + src + msg + "\n")
		}
	}

	b.WriteString("SymbolicVeto:\n")
	if veto == "" {
		b.WriteString("- (none)\n")
	} else {
		b.WriteString("- " + veto + "\n")
	}

	return strings.TrimSpace(b.String()), nil
}

// PersistOraclePackage writes a packaged payload to disk.
func PersistOraclePackage(p OraclePackage, path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		path = filepath.Join(".memory", "oracle", "last_package.json")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	b, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(path, b, 0o644); err != nil {
		return "", err
	}
	return path, nil
}

func hasOracleQuota(path string, minRemainingRatio float64) bool {
	snap, err := state.LoadQuotaSnapshot(path)
	if err != nil {
		// Fail-safe: do not escalate if quota state cannot be trusted.
		return false
	}
	if snap.DailyLimit <= 0 {
		return false
	}
	r := float64(snap.Remaining) / float64(snap.DailyLimit)
	return r > minRemainingRatio
}

func readMirrorTail(path string, n int) ([]OracleMirrorLine, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		path = defaultOracleMirrorPath
	}
	if n <= 0 {
		n = 10
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var rows []OracleMirrorLine
	for _, ln := range strings.Split(string(raw), "\n") {
		ln = strings.TrimSpace(ln)
		if ln == "" {
			continue
		}
		var row OracleMirrorLine
		if err := json.Unmarshal([]byte(ln), &row); err != nil {
			continue
		}
		row.Message = strings.TrimSpace(row.Message)
		if row.Message == "" {
			continue
		}
		rows = append(rows, row)
	}
	if len(rows) > n {
		rows = rows[len(rows)-n:]
	}
	return rows, nil
}

func inferVetoReason(lines []OracleMirrorLine) string {
	for i := len(lines) - 1; i >= 0; i-- {
		msg := strings.ToLower(strings.TrimSpace(lines[i].Message))
		if msg == "" {
			continue
		}
		if strings.Contains(msg, "veto") || strings.Contains(msg, "symbolic") {
			return strings.TrimSpace(lines[i].Message)
		}
	}
	if len(lines) == 0 {
		return ""
	}
	return fmt.Sprintf("No explicit symbolic veto line found. Last mirror signal: %s", strings.TrimSpace(lines[len(lines)-1].Message))
}
