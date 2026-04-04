package taloscli

import (
	"fmt"
	"io"
	"sort"
	"strings"
)

const (
	helpExampleDisplayLimit = 8
	helpDescriptionWidth    = 72
)

type helpCommandRow struct {
	Name        string
	Description string
}

func renderRootHelpPage(w io.Writer, _ bool) {
	rows := collectRootHelpRows()
	examples := collectRootHelpExamples()
	topRows := collectTopHelpRows(rows)

	fmt.Fprintln(w, "TALOS CLI HELP")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "USAGE")
	fmt.Fprintln(w, "  talos [command] [flags]")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "TOP COMMANDS")
	renderHelpCommandsTable(w, topRows)
	fmt.Fprintln(w)
	fmt.Fprintln(w, "ALL COMMANDS")
	renderHelpCommandsTable(w, rows)
	fmt.Fprintln(w)
	fmt.Fprintln(w, "EXAMPLES")
	if len(examples) == 0 {
		fmt.Fprintln(w, "  [none]")
	} else {
		n := minInt(len(examples), helpExampleDisplayLimit)
		for i, ex := range examples[:n] {
			fmt.Fprintf(w, "  %d. %s\n", i+1, ex)
		}
		if len(examples) > n {
			fmt.Fprintf(w, "  ... (%d more)\n", len(examples)-n)
		}
	}
	fmt.Fprintln(w)
	fmt.Fprintln(w, "DISCOVER MORE")
	fmt.Fprintln(w, "  talos find <keyword>")
	fmt.Fprintln(w, "  talos [command] --help")
}

func collectRootHelpRows() []helpCommandRow {
	cmds := rootCmd.Commands()
	rows := make([]helpCommandRow, 0, len(cmds))
	for _, c := range cmds {
		if c == nil || c.Hidden || !c.IsAvailableCommand() {
			continue
		}
		name := strings.TrimSpace(c.Name())
		if name == "" {
			continue
		}
		if name == "next" {
			name = "next (/next)"
		}
		rows = append(rows, helpCommandRow{
			Name:        name,
			Description: compactDescription(strings.TrimSpace(c.Short)),
		})
	}
	sort.SliceStable(rows, func(i, j int) bool {
		return rows[i].Name < rows[j].Name
	})
	return rows
}

func collectTopHelpRows(rows []helpCommandRow) []helpCommandRow {
	if len(rows) == 0 {
		return nil
	}
	priority := []string{"chat", "research", "learn", "multi-agent", "pipeline", "find", "doctor", "skills"}
	lookup := make(map[string]helpCommandRow, len(rows))
	for _, row := range rows {
		lookup[row.Name] = row
	}

	out := make([]helpCommandRow, 0, len(priority))
	for _, name := range priority {
		if row, ok := lookup[name]; ok {
			out = append(out, row)
		}
	}
	return out
}

func collectRootHelpExamples() []string {
	lines := strings.Split(rootCmd.Example, "\n")
	examples := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		examples = append(examples, line)
	}
	return examples
}

func renderHelpCommandsTable(w io.Writer, rows []helpCommandRow) {
	if len(rows) == 0 {
		fmt.Fprintln(w, "  [none]")
		return
	}
	nameWidth := len("COMMAND")
	for _, r := range rows {
		if len(r.Name) > nameWidth {
			nameWidth = len(r.Name)
		}
	}
	fmt.Fprintf(w, "  %-*s | %s\n", nameWidth, "COMMAND", "DESCRIPTION")
	fmt.Fprintf(w, "  %s-+-%s\n", strings.Repeat("-", nameWidth), strings.Repeat("-", helpDescriptionWidth))
	for _, r := range rows {
		desc := compactDescription(r.Description)
		if desc == "" {
			desc = "n/a"
		}
		fmt.Fprintf(w, "  %-*s | %-*s\n", nameWidth, r.Name, helpDescriptionWidth, desc)
	}
}

func compactDescription(desc string) string {
	desc = strings.Join(strings.Fields(strings.TrimSpace(desc)), " ")
	if desc == "" {
		return ""
	}
	if len(desc) <= helpDescriptionWidth {
		return desc
	}
	return desc[:helpDescriptionWidth-3] + "..."
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
