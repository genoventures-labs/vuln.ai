package taloscli

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

var (
	learnedLast   int
	learnedStatus string
	learnedModes  []string
	learnedJSON   bool
)

var learnedCmd = &cobra.Command{
	Use:   "learned",
	Short: "Report what TALOS learned in recent learn/training sessions.",
	Run: func(cmd *cobra.Command, args []string) {
		records, corrupt, err := readLearnSessionRecords()
		if err != nil {
			fmt.Printf("Error reading learn sessions: %v\n", err)
			return
		}
		modeFilter := make(map[string]bool)
		for _, m := range learnedModes {
			m = strings.ToUpper(strings.TrimSpace(m))
			if m != "" {
				modeFilter[m] = true
			}
		}
		filtered := filterLearnSessionRecords(records, learnedStatus, modeFilter, learnedLast)
		if learnedJSON {
			payload := map[string]interface{}{
				"requested_last":          learnedLast,
				"status_filter":           learnedStatus,
				"mode_filter":             learnedModes,
				"corrupt_records_skipped": corrupt,
				"sessions":                filtered,
			}
			out, err := json.MarshalIndent(payload, "", "  ")
			if err != nil {
				fmt.Printf("Error rendering JSON output: %v\n", err)
				return
			}
			fmt.Println(string(out))
			return
		}
		fmt.Println(renderLearnedReport(filtered, learnedLast, corrupt))
	},
}

func init() {
	learnedCmd.Flags().IntVar(&learnedLast, "last", 5, "Number of most recent sessions to report")
	learnedCmd.Flags().StringVar(&learnedStatus, "status", "all", "Filter by status: all|success|failed|partial")
	learnedCmd.Flags().StringSliceVar(&learnedModes, "mode", nil, "Filter by mode (repeatable): INLINE_TEXT|FILE|DIRECTORY|REMOTE|LEARN_IMAGE")
	learnedCmd.Flags().BoolVar(&learnedJSON, "json", false, "Render learned report as JSON")
	rootCmd.AddCommand(learnedCmd)
}
