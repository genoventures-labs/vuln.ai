package taloscli

import (
	"fmt"
	"io"
	"strings"
)

func printCommandStatus(w io.Writer, command string, status string) {
	fmt.Fprintf(w, "%s\n\nCOMMAND\n  %s\n\nSTATUS\n  %s\n", strings.ToUpper(strings.TrimSpace(command)), strings.TrimSpace(command), strings.ToUpper(strings.TrimSpace(status)))
}

func printKV(w io.Writer, key, value string) {
	fmt.Fprintf(w, "  %s: %s\n", strings.TrimSpace(key), strings.TrimSpace(value))
}

func printSection(w io.Writer, title string) {
	fmt.Fprintf(w, "\n%s\n", strings.ToUpper(strings.TrimSpace(title)))
}
