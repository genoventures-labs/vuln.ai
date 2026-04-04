package skills

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

var (
	goModModuleRE  = regexp.MustCompile(`(?m)^\s*module\s+([^\s]+)\s*$`)
	goModVersionRE = regexp.MustCompile(`(?m)^\s*go\s+([0-9.]+)\s*$`)
	goModReqRE     = regexp.MustCompile(`(?m)^\s*require\s+([^\s]+)\s+([^\s]+)\s*$`)
	goModReqBlkRE  = regexp.MustCompile(`(?ms)^\s*require\s*\((.*?)\)`)
)

// WorkspaceContext provides a project "mental map" for IDE-aware reasoning.
type WorkspaceContext struct {
	Root             string    `json:"root"`
	ActiveFile       string    `json:"active_file,omitempty"`
	ModulePath       string    `json:"module_path,omitempty"`
	GoVersion        string    `json:"go_version,omitempty"`
	RequireCount     int       `json:"require_count"`
	Requires         []string  `json:"requires,omitempty"`
	DirectoryEntries []string  `json:"directory_entries,omitempty"`
	IndexedAt        time.Time `json:"indexed_at"`
}

// LintMirrorInsight is a plain-English explanation of compiler/lint output.
type LintMirrorInsight struct {
	Detected       bool    `json:"detected"`
	Severity       float64 `json:"severity"`
	Headline       string  `json:"headline"`
	Why            string  `json:"why"`
	SuggestedFix   string  `json:"suggested_fix"`
	SourceFragment string  `json:"source_fragment,omitempty"`
}

// BuildWorkspaceContext indexes directory structure and go.mod for developer context.
func BuildWorkspaceContext(root, activeFile string, maxEntries int) (WorkspaceContext, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		root = "."
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return WorkspaceContext{}, err
	}
	if maxEntries <= 0 {
		maxEntries = 220
	}
	ctx := WorkspaceContext{
		Root:       absRoot,
		ActiveFile: strings.TrimSpace(activeFile),
		IndexedAt:  time.Now().UTC(),
	}
	modPath := filepath.Join(absRoot, "go.mod")
	if raw, err := os.ReadFile(modPath); err == nil {
		mod := string(raw)
		if m := goModModuleRE.FindStringSubmatch(mod); len(m) == 2 {
			ctx.ModulePath = strings.TrimSpace(m[1])
		}
		if m := goModVersionRE.FindStringSubmatch(mod); len(m) == 2 {
			ctx.GoVersion = strings.TrimSpace(m[1])
		}
		reqs := parseGoModRequires(mod)
		ctx.RequireCount = len(reqs)
		if len(reqs) > 40 {
			reqs = reqs[:40]
		}
		ctx.Requires = reqs
	}

	var entries []string
	_ = filepath.WalkDir(absRoot, func(path string, de os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		rel, err := filepath.Rel(absRoot, path)
		if err != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		if rel == "." {
			return nil
		}
		if de.IsDir() {
			if shouldSkipWorkspaceDir(rel) {
				return filepath.SkipDir
			}
			return nil
		}
		if shouldIgnoreWorkspaceFile(rel) {
			return nil
		}
		entries = append(entries, rel)
		if len(entries) >= maxEntries {
			return filepath.SkipDir
		}
		return nil
	})
	sort.Strings(entries)
	ctx.DirectoryEntries = entries
	return ctx, nil
}

// BuildLintMirrorFromWatch inspects watch_terminal frames and explains compiler issues.
func BuildLintMirrorFromWatch(res WatchTerminalResult) (LintMirrorInsight, bool) {
	if len(res.Frames) == 0 {
		return LintMirrorInsight{}, false
	}
	var blob strings.Builder
	if strings.TrimSpace(res.Stderr) != "" {
		blob.WriteString(strings.TrimSpace(res.Stderr))
		blob.WriteString("\n")
	}
	start := len(res.Frames) - 4
	if start < 0 {
		start = 0
	}
	for i := start; i < len(res.Frames); i++ {
		f := res.Frames[i]
		if s := strings.TrimSpace(f.Stderr); s != "" {
			blob.WriteString(s)
			blob.WriteString("\n")
		}
		if s := strings.TrimSpace(f.OCRText); s != "" {
			blob.WriteString(s)
			blob.WriteString("\n")
		}
	}
	return ExplainCompilerOutput(blob.String())
}

// ExplainCompilerOutput turns compiler/linter stderr into plain-English guidance.
func ExplainCompilerOutput(raw string) (LintMirrorInsight, bool) {
	s := strings.ToLower(strings.TrimSpace(raw))
	if s == "" {
		return LintMirrorInsight{}, false
	}
	out := LintMirrorInsight{
		Detected:       true,
		Severity:       0.62,
		SourceFragment: trimFragment(raw, 220),
	}
	switch {
	case strings.Contains(s, "borrow checker") || strings.Contains(s, "cannot move out of") || strings.Contains(s, "use of moved value"):
		out.Severity = 0.88
		out.Headline = "Borrow checker conflict"
		out.Why = "A value was moved or mutably borrowed, and the code tries to use it again in an incompatible way."
		out.SuggestedFix = "Prefer borrowing (`&`/`&mut`) or clone before the move; narrow mutable borrow scope around the exact lines."
	case strings.Contains(s, "undefined:"):
		out.Severity = 0.82
		out.Headline = "Undefined symbol"
		out.Why = "The compiler cannot find a function/type/variable referenced in the file."
		out.SuggestedFix = "Check spelling/imports, ensure the symbol is in scope, and verify the package exposes it."
	case strings.Contains(s, "cannot use") && strings.Contains(s, "as type"):
		out.Severity = 0.78
		out.Headline = "Type mismatch"
		out.Why = "An expression type does not match the type expected by the function/assignment."
		out.SuggestedFix = "Convert the value explicitly or update the function signature so both sides use compatible types."
	case strings.Contains(s, "no required module provides package"):
		out.Severity = 0.80
		out.Headline = "Missing module dependency"
		out.Why = "A package import is not declared in go.mod."
		out.SuggestedFix = "Add/replace the dependency in go.mod or switch to an existing local package utility."
	case strings.Contains(s, "import cycle not allowed"):
		out.Severity = 0.79
		out.Headline = "Import cycle"
		out.Why = "Two or more packages depend on each other, which Go forbids."
		out.SuggestedFix = "Extract shared logic to a lower-level package and remove the circular dependency."
	case strings.Contains(s, "syntax error"):
		out.Severity = 0.76
		out.Headline = "Syntax error"
		out.Why = "The parser hit invalid code structure (often brackets, commas, or keywords)."
		out.SuggestedFix = "Start at the first reported file:line and fix delimiters/keywords before later errors."
	default:
		return LintMirrorInsight{
			Detected:       true,
			Severity:       0.45,
			Headline:       "Compiler friction detected",
			Why:            "Build output indicates an error, but it does not match a known high-confidence pattern.",
			SuggestedFix:   "Inspect the first compiler-reported file/line and rerun the check after one focused fix.",
			SourceFragment: trimFragment(raw, 220),
		}, true
	}
	return out, true
}

// FormatLintMirrorBrief formats a casual mirror line for runtime narration.
func FormatLintMirrorBrief(ins LintMirrorInsight) string {
	if !ins.Detected {
		return ""
	}
	parts := []string{
		ins.Headline + ": " + ins.Why,
		"Fix: " + ins.SuggestedFix,
	}
	if strings.TrimSpace(ins.SourceFragment) != "" {
		parts = append(parts, "Signal: "+ins.SourceFragment)
	}
	return strings.Join(parts, " ")
}

func MarshalWorkspaceContext(ctx WorkspaceContext) string {
	b, err := json.Marshal(ctx)
	if err != nil {
		return "{}"
	}
	return string(b)
}

func parseGoModRequires(raw string) []string {
	seen := map[string]bool{}
	var out []string
	for _, m := range goModReqRE.FindAllStringSubmatch(raw, -1) {
		if len(m) < 3 {
			continue
		}
		dep := strings.TrimSpace(m[1] + "@" + m[2])
		if dep == "" || seen[dep] {
			continue
		}
		seen[dep] = true
		out = append(out, dep)
	}
	if blk := goModReqBlkRE.FindStringSubmatch(raw); len(blk) == 2 {
		for _, ln := range strings.Split(blk[1], "\n") {
			ln = strings.TrimSpace(strings.SplitN(ln, "//", 2)[0])
			if ln == "" {
				continue
			}
			f := strings.Fields(ln)
			if len(f) < 2 {
				continue
			}
			dep := strings.TrimSpace(f[0] + "@" + f[1])
			if dep == "" || seen[dep] {
				continue
			}
			seen[dep] = true
			out = append(out, dep)
		}
	}
	sort.Strings(out)
	return out
}

func shouldSkipWorkspaceDir(rel string) bool {
	rel = strings.ToLower(strings.TrimSpace(filepath.ToSlash(rel)))
	for _, skip := range []string{
		".git", ".memory", ".codelab", ".skills", "node_modules", "vendor", "bin", "dist", "build", "target",
	} {
		if rel == skip || strings.HasPrefix(rel, skip+"/") {
			return true
		}
	}
	return false
}

func shouldIgnoreWorkspaceFile(rel string) bool {
	rel = strings.ToLower(strings.TrimSpace(filepath.ToSlash(rel)))
	for _, ext := range []string{".png", ".jpg", ".jpeg", ".gif", ".svg", ".pdf", ".zip", ".tar", ".gz", ".bin"} {
		if strings.HasSuffix(rel, ext) {
			return true
		}
	}
	return false
}

func trimFragment(s string, max int) string {
	s = strings.TrimSpace(strings.Join(strings.Fields(s), " "))
	if max <= 0 || len(s) <= max {
		return s
	}
	return s[:max] + "...(truncated)"
}

func BuildDeveloperHandoverLine(ctx WorkspaceContext, scoutCount, capabilityCount int) string {
	module := strings.TrimSpace(ctx.ModulePath)
	if module == "" {
		module = "current workspace"
	}
	return fmt.Sprintf(
		"Developer handover: indexed %d files across %s (Go %s, %d deps). Scout posted %d intuition brief(s), capability pipeline published %d new manifest(s).",
		len(ctx.DirectoryEntries),
		module,
		fallbackText(ctx.GoVersion, "n/a"),
		ctx.RequireCount,
		scoutCount,
		capabilityCount,
	)
}

func fallbackText(v, fb string) string {
	if strings.TrimSpace(v) == "" {
		return fb
	}
	return strings.TrimSpace(v)
}
