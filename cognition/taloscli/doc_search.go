package taloscli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

var (
	docSearchDir           string
	docSearchTerm          string
	docSearchRegex         bool
	docSearchCaseSensitive bool
	docSearchExtensions    string
	docSearchExcludeDirs   string
	docSearchIncludeHidden bool
	docSearchFollowSymlink bool
	docSearchMaxFiles      int
	docSearchMaxMatches    int
	docSearchMaxPerFile    int
	docSearchMaxFileBytes  int64
	docSearchContextLines  int
	docSearchJSON          bool
)

var docSearchCmd = &cobra.Command{
	Use:     "doc-search [term]",
	Aliases: []string{"search-docs"},
	Short:   "Recursively search local workspace files for exact wording or terms.",
	Args:    cobra.ArbitraryArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		term := strings.TrimSpace(docSearchTerm)
		if term == "" && len(args) > 0 {
			term = strings.TrimSpace(strings.Join(args, " "))
		}
		opts := DocSearchOptions{
			Query:         term,
			Dir:           strings.TrimSpace(docSearchDir),
			Regex:         docSearchRegex,
			CaseSensitive: docSearchCaseSensitive,
			Extensions:    parseDocSearchCSV(docSearchExtensions),
			ExcludeDirs:   parseDocSearchCSV(docSearchExcludeDirs),
			IncludeHidden: docSearchIncludeHidden,
			FollowSymlink: docSearchFollowSymlink,
			MaxFiles:      docSearchMaxFiles,
			MaxMatches:    docSearchMaxMatches,
			MaxPerFile:    docSearchMaxPerFile,
			MaxFileBytes:  docSearchMaxFileBytes,
			ContextLines:  docSearchContextLines,
		}
		report, err := runDocSearch(opts)
		if err != nil {
			return err
		}
		if docSearchJSON {
			fmt.Fprintln(cmd.OutOrStdout(), docSearchReportJSON(report))
			return nil
		}
		fmt.Fprintln(cmd.OutOrStdout(), renderDocSearchReport(report))
		return nil
	},
}

func renderDocSearchReport(report DocSearchReport) string {
	var b strings.Builder
	b.WriteString("TALOS DOC SEARCH\n\n")
	b.WriteString("COMMAND\n")
	b.WriteString("  talos doc-search\n\n")
	status := "SUCCESS"
	if report.Truncated {
		status = "PARTIAL"
	}
	b.WriteString("STATUS\n")
	b.WriteString("  " + status + "\n\n")
	b.WriteString("QUERY\n")
	b.WriteString("  " + strings.TrimSpace(report.Query) + "\n\n")
	b.WriteString("SCOPE\n")
	b.WriteString("  root: " + strings.TrimSpace(report.Root) + "\n\n")
	b.WriteString("STATUS\n")
	b.WriteString(fmt.Sprintf("  scanned_files: %d\n", report.ScannedFiles))
	b.WriteString(fmt.Sprintf("  matched_files: %d\n", report.MatchedFiles))
	b.WriteString(fmt.Sprintf("  total_matches: %d\n", report.TotalMatches))
	b.WriteString(fmt.Sprintf("  skipped_binary: %d\n", report.SkippedBinary))
	b.WriteString(fmt.Sprintf("  skipped_hidden: %d\n", report.SkippedHidden))
	b.WriteString(fmt.Sprintf("  skipped_large: %d\n", report.SkippedLarge))
	b.WriteString(fmt.Sprintf("  skipped_by_type: %d\n", report.SkippedByType))
	b.WriteString(fmt.Sprintf("  skipped_by_rule: %d\n", report.SkippedByRule))
	b.WriteString(fmt.Sprintf("  truncated: %t\n\n", report.Truncated))
	b.WriteString("RESULTS\n")
	if len(report.Results) == 0 {
		b.WriteString("  no matches found.\n")
		b.WriteString("\nNEXT\n")
		b.WriteString("  Try: talos find <keyword>\n")
		return strings.TrimRight(b.String(), "\n")
	}
	for _, r := range report.Results {
		b.WriteString(fmt.Sprintf("  - %s (%d hits)\n", r.Path, r.MatchCount))
		for _, m := range r.Matches {
			b.WriteString(fmt.Sprintf("      L%d:C%d %s\n", m.Line, m.Column, m.Snippet))
		}
	}
	b.WriteString("\nNEXT\n")
	b.WriteString("  Refine scope with --dir/--extensions for faster targeted search.\n")
	return strings.TrimRight(b.String(), "\n")
}

func init() {
	fs := docSearchCmd.Flags()
	fs.StringVar(&docSearchDir, "dir", ".", "Root directory to recursively search")
	fs.StringVar(&docSearchTerm, "term", "", "Search term (optional when provided as positional argument)")
	fs.BoolVar(&docSearchRegex, "regex", false, "Use regex matching")
	fs.BoolVar(&docSearchCaseSensitive, "case-sensitive", false, "Enable case-sensitive matching")
	fs.StringVar(&docSearchExtensions, "extensions", strings.Join(defaultDocSearchExtensions, ","), "Comma-separated extension allowlist")
	fs.StringVar(&docSearchExcludeDirs, "exclude-dir", strings.Join(defaultDocSearchExcludedDirs, ","), "Comma-separated directory exclude list")
	fs.BoolVar(&docSearchIncludeHidden, "include-hidden", false, "Include hidden files/directories")
	fs.BoolVar(&docSearchFollowSymlink, "follow-symlinks", false, "Follow symbolic links")
	fs.IntVar(&docSearchMaxFiles, "max-files", docSearchDefaultMaxFiles, "Maximum number of files to scan")
	fs.IntVar(&docSearchMaxMatches, "max-matches", docSearchDefaultMaxMatches, "Maximum number of total matches to return")
	fs.IntVar(&docSearchMaxPerFile, "max-per-file", docSearchDefaultMaxPerFile, "Maximum number of matches to return per file")
	fs.Int64Var(&docSearchMaxFileBytes, "max-file-bytes", docSearchDefaultMaxFileBytes, "Skip files larger than this byte size")
	fs.IntVar(&docSearchContextLines, "context-lines", docSearchDefaultContextLines, "Number of surrounding lines to include around each hit")
	fs.BoolVar(&docSearchJSON, "json", false, "Render results as JSON")
	rootCmd.AddCommand(docSearchCmd)
}
