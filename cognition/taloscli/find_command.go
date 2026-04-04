package taloscli

import (
	"fmt"
	"sort"
	"strings"

	"github.com/spf13/cobra"
)

type findMatch struct {
	Path    string
	Use     string
	Summary string
}

var findCmd = &cobra.Command{
	Use:   "find <keyword>",
	Short: "Find TALOS commands by keyword.",
	Args:  cobra.MinimumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		query := strings.ToLower(strings.TrimSpace(strings.Join(args, " ")))
		if query == "" {
			fmt.Fprintln(cmd.OutOrStdout(), "Keyword cannot be empty.")
			return
		}
		matches := findCommandsByKeyword(rootCmd, query)
		fmt.Fprintln(cmd.OutOrStdout(), renderFindResults(query, matches))
	},
}

func findCommandsByKeyword(root *cobra.Command, query string) []findMatch {
	query = strings.ToLower(strings.TrimSpace(query))
	if root == nil || query == "" {
		return nil
	}
	seen := map[string]bool{}
	matches := make([]findMatch, 0)
	var walk func(c *cobra.Command)
	walk = func(c *cobra.Command) {
		if c == nil || c.Hidden {
			return
		}
		path := strings.TrimSpace(c.CommandPath())
		if path != "" && !seen[path] && commandMatchesQuery(c, query) {
			seen[path] = true
			matches = append(matches, findMatch{
				Path:    path,
				Use:     strings.TrimSpace(c.UseLine()),
				Summary: strings.TrimSpace(c.Short),
			})
		}
		for _, child := range c.Commands() {
			walk(child)
		}
	}
	walk(root)
	sort.SliceStable(matches, func(i, j int) bool {
		return matches[i].Path < matches[j].Path
	})
	return matches
}

func commandMatchesQuery(c *cobra.Command, query string) bool {
	if c == nil {
		return false
	}
	haystacks := []string{
		strings.ToLower(strings.TrimSpace(c.Name())),
		strings.ToLower(strings.TrimSpace(c.Use)),
		strings.ToLower(strings.TrimSpace(c.UseLine())),
		strings.ToLower(strings.TrimSpace(c.Short)),
		strings.ToLower(strings.TrimSpace(c.Long)),
	}
	for _, a := range c.Aliases {
		haystacks = append(haystacks, strings.ToLower(strings.TrimSpace(a)))
	}
	for _, h := range haystacks {
		if strings.Contains(h, query) {
			return true
		}
	}
	return false
}

func renderFindResults(query string, matches []findMatch) string {
	var b strings.Builder
	b.WriteString("TALOS COMMAND FIND\n\n")
	b.WriteString("COMMAND\n")
	b.WriteString("  talos find " + strings.TrimSpace(query) + "\n\n")
	b.WriteString("STATUS\n")
	if len(matches) == 0 {
		b.WriteString("  PARTIAL\n\n")
	} else {
		b.WriteString("  SUCCESS\n\n")
	}
	b.WriteString("QUERY\n")
	b.WriteString("  " + strings.TrimSpace(query) + "\n\n")
	b.WriteString("RESULTS\n")
	b.WriteString(fmt.Sprintf("  matches: %d\n\n", len(matches)))
	if len(matches) == 0 {
		b.WriteString("COMMANDS\n")
		b.WriteString("  No matching commands found.\n")
		b.WriteString("\nNEXT\n")
		b.WriteString("  Try: talos --help\n")
		return strings.TrimRight(b.String(), "\n")
	}

	pathWidth := len("COMMAND PATH")
	for _, m := range matches {
		if len(m.Path) > pathWidth {
			pathWidth = len(m.Path)
		}
	}

	b.WriteString("COMMANDS\n")
	b.WriteString(fmt.Sprintf("  %-*s | %s\n", pathWidth, "COMMAND PATH", "SUMMARY"))
	b.WriteString(fmt.Sprintf("  %s-+-%s\n", strings.Repeat("-", pathWidth), strings.Repeat("-", 60)))
	for _, m := range matches {
		summary := m.Summary
		if summary == "" {
			summary = "n/a"
		}
		b.WriteString(fmt.Sprintf("  %-*s | %s\n", pathWidth, m.Path, summary))
	}
	b.WriteString("\nNEXT\n")
	b.WriteString("  Use: talos <command> --help\n")
	return strings.TrimRight(b.String(), "\n")
}

func init() {
	rootCmd.AddCommand(findCmd)
}
