package taloscli

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/Thynaptic/P-LMv1/pkg/orchestration"
	"github.com/spf13/cobra"
)

var (
	beliefAuditSince string
	beliefAuditUntil string
	beliefAuditLast  int
)

var researchBeliefAuditCmd = &cobra.Command{
	Use:   "belief-audit [topic]",
	Short: "Audit how TALOS worldview has changed over time for a topic.",
	Args:  cobra.ArbitraryArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		topic := strings.TrimSpace(strings.Join(args, " "))
		since, err := parseAuditDateFlag(strings.TrimSpace(beliefAuditSince))
		if err != nil {
			return err
		}
		until, err := parseAuditDateFlag(strings.TrimSpace(beliefAuditUntil))
		if err != nil {
			return err
		}
		entries, err := orchestration.LoadWorldviewHistory()
		if err != nil {
			return err
		}
		report := renderBeliefAuditReport(topic, since, until, beliefAuditLast, entries)
		fmt.Fprint(cmd.OutOrStdout(), report)
		return nil
	},
}

func parseAuditDateFlag(raw string) (time.Time, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}, nil
	}
	layouts := []string{time.RFC3339, "2006-01-02", "2006"}
	for _, layout := range layouts {
		if t, err := time.Parse(layout, raw); err == nil {
			return t.UTC(), nil
		}
	}
	return time.Time{}, fmt.Errorf("invalid date %q (use YYYY, YYYY-MM-DD, or RFC3339)", raw)
}

func renderBeliefAuditReport(topic string, since, until time.Time, last int, in []orchestration.WorldviewTruthState) string {
	topic = strings.TrimSpace(topic)
	filtered := filterWorldviewHistory(topic, since, until, in)
	if last > 0 && len(filtered) > last {
		filtered = filtered[len(filtered)-last:]
	}
	var b strings.Builder
	b.WriteString("EPISTEMIC ALIGNMENT AUDIT\n\n")
	b.WriteString("WINDOW\n")
	if since.IsZero() {
		b.WriteString("  since: beginning\n")
	} else {
		b.WriteString("  since: " + since.Format("2006-01-02") + "\n")
	}
	if until.IsZero() {
		b.WriteString("  until: now\n")
	} else {
		b.WriteString("  until: " + until.Format("2006-01-02") + "\n")
	}
	if topic == "" {
		b.WriteString("  topic: all\n")
	} else {
		b.WriteString("  topic: " + topic + "\n")
	}
	b.WriteString(fmt.Sprintf("  matched_snapshots: %d\n\n", len(filtered)))

	if len(filtered) == 0 {
		b.WriteString("RESULT\n")
		b.WriteString("  No worldview snapshots matched this window/topic.\n")
		return b.String()
	}

	base := filtered[0]
	curr := filtered[len(filtered)-1]
	b.WriteString("BASELINE\n")
	b.WriteString("  date: " + safeDate(base.UpdatedAt) + "\n")
	b.WriteString("  truth_hash: " + strings.TrimSpace(base.TruthHash) + "\n")
	b.WriteString("  query: " + summarizeLine(base.Query, 120) + "\n\n")

	b.WriteString("CURRENT\n")
	b.WriteString("  date: " + safeDate(curr.UpdatedAt) + "\n")
	b.WriteString("  truth_hash: " + strings.TrimSpace(curr.TruthHash) + "\n")
	b.WriteString("  query: " + summarizeLine(curr.Query, 120) + "\n\n")

	added, removed, changed := diffWorldviewAnchors(base.ResolutionAnchors, curr.ResolutionAnchors)
	b.WriteString("DELTA\n")
	if strings.EqualFold(strings.TrimSpace(base.TruthHash), strings.TrimSpace(curr.TruthHash)) {
		b.WriteString("  worldview_shift: stable\n")
	} else {
		b.WriteString("  worldview_shift: changed\n")
	}
	b.WriteString(fmt.Sprintf("  conflict_index: %.2f -> %.2f\n", base.ConflictIndex, curr.ConflictIndex))
	b.WriteString(fmt.Sprintf("  anchor_changes: added=%d removed=%d winner_flips=%d\n", len(added), len(removed), len(changed)))
	for i, line := range changed {
		if i >= 6 {
			break
		}
		b.WriteString("  flip: " + line + "\n")
	}
	for i, line := range added {
		if i >= 3 {
			break
		}
		b.WriteString("  added: " + line + "\n")
	}
	for i, line := range removed {
		if i >= 3 {
			break
		}
		b.WriteString("  removed: " + line + "\n")
	}

	b.WriteString("\nTIMELINE\n")
	for i, e := range filtered {
		if i >= 10 && len(filtered) > 12 && i < len(filtered)-2 {
			continue
		}
		b.WriteString(fmt.Sprintf("  - %s | hash=%s | conflict=%.2f | query=%s\n",
			safeDate(e.UpdatedAt), strings.TrimSpace(e.TruthHash), e.ConflictIndex, summarizeLine(e.Query, 72)))
	}
	return b.String()
}

func filterWorldviewHistory(topic string, since, until time.Time, in []orchestration.WorldviewTruthState) []orchestration.WorldviewTruthState {
	out := make([]orchestration.WorldviewTruthState, 0, len(in))
	topic = strings.ToLower(strings.TrimSpace(topic))
	for _, e := range in {
		if !since.IsZero() && e.UpdatedAt.Before(since) {
			continue
		}
		if !until.IsZero() && e.UpdatedAt.After(until) {
			continue
		}
		if topic != "" {
			if !worldviewMatchesTopic(topic, e) {
				continue
			}
		}
		out = append(out, e)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].UpdatedAt.Before(out[j].UpdatedAt) })
	return out
}

func worldviewMatchesTopic(topic string, e orchestration.WorldviewTruthState) bool {
	lq := strings.ToLower(strings.TrimSpace(e.Query + " " + e.FusedWorldview))
	if strings.Contains(lq, topic) {
		return true
	}
	for _, a := range e.ResolutionAnchors {
		if strings.Contains(strings.ToLower(strings.TrimSpace(a.Issue+" "+a.Winner)), topic) {
			return true
		}
	}
	return false
}

func diffWorldviewAnchors(base, curr []orchestration.WorldviewTruthAnchor) (added []string, removed []string, changed []string) {
	bm := map[string]string{}
	for _, a := range base {
		issue := strings.TrimSpace(a.Issue)
		if issue == "" {
			continue
		}
		bm[issue] = strings.TrimSpace(a.Winner)
	}
	cm := map[string]string{}
	for _, a := range curr {
		issue := strings.TrimSpace(a.Issue)
		if issue == "" {
			continue
		}
		cm[issue] = strings.TrimSpace(a.Winner)
	}
	for issue, winner := range cm {
		bw, ok := bm[issue]
		if !ok {
			added = append(added, fmt.Sprintf("%s => %s", issue, winner))
			continue
		}
		if !strings.EqualFold(strings.TrimSpace(bw), strings.TrimSpace(winner)) {
			changed = append(changed, fmt.Sprintf("%s | %s -> %s", issue, bw, winner))
		}
	}
	for issue, winner := range bm {
		if _, ok := cm[issue]; !ok {
			removed = append(removed, fmt.Sprintf("%s => %s", issue, winner))
		}
	}
	sort.Strings(added)
	sort.Strings(removed)
	sort.Strings(changed)
	return added, removed, changed
}

func safeDate(t time.Time) string {
	if t.IsZero() {
		return "n/a"
	}
	return t.UTC().Format(time.RFC3339)
}

func summarizeLine(s string, n int) string {
	s = strings.Join(strings.Fields(strings.TrimSpace(s)), " ")
	if s == "" {
		return "n/a"
	}
	if len(s) <= n || n <= 0 {
		return s
	}
	if n <= 3 {
		return s[:n]
	}
	return s[:n-3] + "..."
}

func init() {
	researchBeliefAuditCmd.Flags().StringVar(&beliefAuditSince, "since", "", "Only include snapshots on/after date (YYYY, YYYY-MM-DD, or RFC3339)")
	researchBeliefAuditCmd.Flags().StringVar(&beliefAuditUntil, "until", "", "Only include snapshots on/before date (YYYY, YYYY-MM-DD, or RFC3339)")
	researchBeliefAuditCmd.Flags().IntVar(&beliefAuditLast, "last", 30, "Maximum snapshots to include after filtering")
	researchCmd.AddCommand(researchBeliefAuditCmd)
}
