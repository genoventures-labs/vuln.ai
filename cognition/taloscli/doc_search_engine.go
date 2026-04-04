package taloscli

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

const (
	docSearchDefaultMaxFiles     = 5000
	docSearchDefaultMaxMatches   = 1000
	docSearchDefaultMaxPerFile   = 20
	docSearchDefaultMaxFileBytes = 2 * 1024 * 1024
	docSearchDefaultContextLines = 1
)

var defaultDocSearchExtensions = []string{
	".md", ".txt", ".rst", ".adoc", ".json", ".yaml", ".yml", ".toml", ".csv", ".tsv",
	".go", ".py", ".js", ".ts", ".tsx", ".jsx", ".java", ".c", ".cpp", ".rs", ".sh", ".sql",
}

var defaultDocSearchExcludedDirs = []string{
	".git", "node_modules", "vendor", "dist", "build", ".cache", ".memory",
}

type DocSearchOptions struct {
	Query         string
	Dir           string
	Regex         bool
	CaseSensitive bool
	Extensions    []string
	ExcludeDirs   []string
	IncludeHidden bool
	FollowSymlink bool
	MaxFiles      int
	MaxMatches    int
	MaxPerFile    int
	MaxFileBytes  int64
	ContextLines  int
}

type DocSearchMatch struct {
	Path    string `json:"path"`
	Line    int    `json:"line"`
	Column  int    `json:"column"`
	Snippet string `json:"snippet"`
}

type DocSearchFileResult struct {
	Path       string           `json:"path"`
	MatchCount int              `json:"match_count"`
	Matches    []DocSearchMatch `json:"matches,omitempty"`
}

type DocSearchReport struct {
	Mode          string                `json:"mode"`
	Query         string                `json:"query"`
	Root          string                `json:"root"`
	ScannedFiles  int                   `json:"scanned_files"`
	MatchedFiles  int                   `json:"matched_files"`
	TotalMatches  int                   `json:"total_matches"`
	SkippedBinary int                   `json:"skipped_binary"`
	SkippedHidden int                   `json:"skipped_hidden"`
	SkippedLarge  int                   `json:"skipped_large"`
	SkippedByType int                   `json:"skipped_by_type"`
	SkippedByRule int                   `json:"skipped_by_rule"`
	Truncated     bool                  `json:"truncated"`
	Citations     []string              `json:"citations,omitempty"`
	Results       []DocSearchFileResult `json:"results,omitempty"`
}

type lineMatcher struct {
	regex         *regexp.Regexp
	literal       string
	caseSensitive bool
}

func runDocSearch(opts DocSearchOptions) (DocSearchReport, error) {
	opts = normalizeDocSearchOptions(opts)
	rootAbs, allowedRoots, err := validateDocSearchPaths(opts.Dir)
	if err != nil {
		return DocSearchReport{}, err
	}
	matcher, err := newDocLineMatcher(opts.Query, opts.Regex, opts.CaseSensitive)
	if err != nil {
		return DocSearchReport{}, err
	}
	extAllow := buildDocSearchExtensionAllowlist(opts.Extensions)
	exclDirs := buildDocSearchDirSet(opts.ExcludeDirs)

	report := DocSearchReport{
		Mode:  "recursive_doc_search",
		Query: strings.TrimSpace(opts.Query),
		Root:  rootAbs,
	}
	results := make([]DocSearchFileResult, 0, 64)
	totalMatches := 0
	globalStop := false

	walkErr := filepath.WalkDir(rootAbs, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			report.SkippedByRule++
			return nil
		}
		if globalStop {
			return fs.SkipAll
		}
		if !opts.FollowSymlink && isSymlinkEntry(d) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			report.SkippedByRule++
			return nil
		}
		rel := docSearchRelPath(rootAbs, path)
		base := strings.ToLower(strings.TrimSpace(d.Name()))
		if d.IsDir() {
			if rel != "." {
				if !opts.IncludeHidden && isHiddenPathSegment(rel) {
					report.SkippedHidden++
					return filepath.SkipDir
				}
				if exclDirs[base] {
					report.SkippedByRule++
					return filepath.SkipDir
				}
			}
			return nil
		}
		if !pathWithinAllowedRoots(path, allowedRoots) {
			report.SkippedByRule++
			return nil
		}
		if !opts.IncludeHidden && isHiddenPathSegment(rel) {
			report.SkippedHidden++
			return nil
		}
		if !shouldSearchFileByExt(path, extAllow) {
			report.SkippedByType++
			return nil
		}
		info, err := d.Info()
		if err != nil {
			report.SkippedByRule++
			return nil
		}
		if info.Size() > opts.MaxFileBytes {
			report.SkippedLarge++
			return nil
		}
		if report.ScannedFiles >= opts.MaxFiles {
			report.Truncated = true
			globalStop = true
			return fs.SkipAll
		}
		report.ScannedFiles++

		content, err := os.ReadFile(path)
		if err != nil {
			report.SkippedByRule++
			return nil
		}
		if isBinaryContent(content) {
			report.SkippedBinary++
			return nil
		}
		lines := splitDocSearchLines(string(content))
		fileResult, matched := searchDocumentLines(path, rel, lines, matcher, opts.ContextLines, opts.MaxPerFile, opts.MaxMatches-totalMatches)
		if !matched {
			return nil
		}
		totalMatches += fileResult.MatchCount
		report.TotalMatches = totalMatches
		if fileResult.MatchCount > 0 {
			report.MatchedFiles++
			results = append(results, fileResult)
		}
		if totalMatches >= opts.MaxMatches {
			report.Truncated = true
			globalStop = true
			return fs.SkipAll
		}
		return nil
	})
	if walkErr != nil {
		return DocSearchReport{}, walkErr
	}

	sort.SliceStable(results, func(i, j int) bool { return results[i].Path < results[j].Path })
	report.Results = results
	report.Citations = buildDocSearchCitations(results, 256)
	return report, nil
}

func normalizeDocSearchOptions(in DocSearchOptions) DocSearchOptions {
	out := in
	out.Query = strings.TrimSpace(out.Query)
	out.Dir = strings.TrimSpace(out.Dir)
	if out.Dir == "" {
		out.Dir = "."
	}
	if len(out.Extensions) == 0 {
		out.Extensions = append([]string(nil), defaultDocSearchExtensions...)
	}
	if len(out.ExcludeDirs) == 0 {
		out.ExcludeDirs = append([]string(nil), defaultDocSearchExcludedDirs...)
	}
	out.MaxFiles = clampInt(out.MaxFiles, 1, 200000)
	if in.MaxFiles <= 0 {
		out.MaxFiles = docSearchDefaultMaxFiles
	}
	out.MaxMatches = clampInt(out.MaxMatches, 1, 200000)
	if in.MaxMatches <= 0 {
		out.MaxMatches = docSearchDefaultMaxMatches
	}
	out.MaxPerFile = clampInt(out.MaxPerFile, 1, 5000)
	if in.MaxPerFile <= 0 {
		out.MaxPerFile = docSearchDefaultMaxPerFile
	}
	if out.MaxFileBytes <= 0 {
		out.MaxFileBytes = docSearchDefaultMaxFileBytes
	}
	if out.MaxFileBytes > 64*1024*1024 {
		out.MaxFileBytes = 64 * 1024 * 1024
	}
	out.ContextLines = clampInt(out.ContextLines, 0, 6)
	if in.ContextLines < 0 {
		out.ContextLines = 0
	}
	if in.ContextLines == 0 {
		out.ContextLines = docSearchDefaultContextLines
	}
	return out
}

func validateDocSearchPaths(rawDir string) (string, []string, error) {
	target := strings.TrimSpace(rawDir)
	if target == "" {
		target = "."
	}
	absTarget, err := filepath.Abs(target)
	if err != nil {
		return "", nil, fmt.Errorf("invalid search directory: %w", err)
	}
	info, err := os.Stat(absTarget)
	if err != nil {
		return "", nil, fmt.Errorf("search directory unavailable: %w", err)
	}
	if !info.IsDir() {
		return "", nil, fmt.Errorf("search directory must be a directory: %s", absTarget)
	}
	roots := workspaceAllowedRoots()
	if !pathWithinAllowedRoots(absTarget, roots) {
		return "", nil, fmt.Errorf("search directory %s is outside workspace allowlist", absTarget)
	}
	return absTarget, roots, nil
}

func workspaceAllowedRoots() []string {
	out := make([]string, 0, 2)
	if wd, err := os.Getwd(); err == nil {
		if abs, absErr := filepath.Abs(wd); absErr == nil {
			out = append(out, filepath.Clean(abs))
		}
	}
	if repo, err := findRepoRoot(); err == nil {
		if abs, absErr := filepath.Abs(repo); absErr == nil {
			out = append(out, filepath.Clean(abs))
		}
	}
	if len(out) == 0 {
		return []string{"."}
	}
	return uniqueStrings(out)
}

func pathWithinAllowedRoots(path string, roots []string) bool {
	cleanPath := filepath.Clean(strings.TrimSpace(path))
	if cleanPath == "" {
		return false
	}
	for _, root := range roots {
		root = filepath.Clean(strings.TrimSpace(root))
		if root == "" {
			continue
		}
		if cleanPath == root {
			return true
		}
		prefix := root + string(os.PathSeparator)
		if strings.HasPrefix(cleanPath, prefix) {
			return true
		}
	}
	return false
}

func newDocLineMatcher(query string, useRegex bool, caseSensitive bool) (lineMatcher, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return lineMatcher{}, fmt.Errorf("doc_search requires non-empty query")
	}
	if !useRegex {
		return lineMatcher{literal: query, caseSensitive: caseSensitive}, nil
	}
	pat := query
	if !caseSensitive {
		pat = "(?i)" + pat
	}
	re, err := regexp.Compile(pat)
	if err != nil {
		return lineMatcher{}, fmt.Errorf("invalid regex: %w", err)
	}
	return lineMatcher{regex: re, caseSensitive: caseSensitive}, nil
}

func (m lineMatcher) match(line string) (int, bool) {
	if m.regex != nil {
		loc := m.regex.FindStringIndex(line)
		if len(loc) == 2 {
			return loc[0] + 1, true
		}
		return 0, false
	}
	if m.caseSensitive {
		idx := strings.Index(line, m.literal)
		if idx >= 0 {
			return idx + 1, true
		}
		return 0, false
	}
	idx := strings.Index(strings.ToLower(line), strings.ToLower(m.literal))
	if idx >= 0 {
		return idx + 1, true
	}
	return 0, false
}

func searchDocumentLines(path string, rel string, lines []string, matcher lineMatcher, ctxLines int, maxPerFile int, maxRemaining int) (DocSearchFileResult, bool) {
	out := DocSearchFileResult{Path: rel}
	if maxPerFile <= 0 || maxRemaining <= 0 {
		return out, false
	}
	hitsCap := maxPerFile
	if maxRemaining < hitsCap {
		hitsCap = maxRemaining
	}
	for i, line := range lines {
		col, ok := matcher.match(line)
		if !ok {
			continue
		}
		out.MatchCount++
		out.Matches = append(out.Matches, DocSearchMatch{
			Path:    rel,
			Line:    i + 1,
			Column:  col,
			Snippet: renderContextSnippet(lines, i, ctxLines),
		})
		if len(out.Matches) >= hitsCap {
			break
		}
	}
	if out.MatchCount == 0 {
		return out, false
	}
	if rel == "" {
		out.Path = path
	}
	return out, true
}

func renderContextSnippet(lines []string, idx int, contextLines int) string {
	if idx < 0 || idx >= len(lines) {
		return ""
	}
	if contextLines <= 0 {
		return strings.TrimSpace(lines[idx])
	}
	start := idx - contextLines
	if start < 0 {
		start = 0
	}
	end := idx + contextLines
	if end >= len(lines) {
		end = len(lines) - 1
	}
	parts := make([]string, 0, end-start+1)
	for i := start; i <= end; i++ {
		parts = append(parts, strings.TrimSpace(lines[i]))
	}
	return truncateText(strings.Join(parts, " | "), 320)
}

func buildDocSearchExtensionAllowlist(exts []string) map[string]bool {
	out := map[string]bool{}
	for _, e := range exts {
		e = strings.ToLower(strings.TrimSpace(e))
		if e == "" {
			continue
		}
		if !strings.HasPrefix(e, ".") {
			e = "." + e
		}
		out[e] = true
	}
	return out
}

func buildDocSearchDirSet(names []string) map[string]bool {
	out := map[string]bool{}
	for _, n := range names {
		n = strings.ToLower(strings.TrimSpace(n))
		if n == "" {
			continue
		}
		out[n] = true
	}
	return out
}

func shouldSearchFileByExt(path string, allow map[string]bool) bool {
	if len(allow) == 0 {
		return true
	}
	ext := strings.ToLower(strings.TrimSpace(filepath.Ext(path)))
	return allow[ext]
}

func isSymlinkEntry(d fs.DirEntry) bool {
	if d == nil {
		return false
	}
	return d.Type()&os.ModeSymlink != 0
}

func isHiddenPathSegment(rel string) bool {
	rel = strings.TrimSpace(filepath.ToSlash(rel))
	if rel == "" || rel == "." {
		return false
	}
	for _, seg := range strings.Split(rel, "/") {
		seg = strings.TrimSpace(seg)
		if seg == "" || seg == "." || seg == ".." {
			continue
		}
		if strings.HasPrefix(seg, ".") {
			return true
		}
	}
	return false
}

func isBinaryContent(content []byte) bool {
	if len(content) == 0 {
		return false
	}
	n := len(content)
	if n > 8192 {
		n = 8192
	}
	for i := 0; i < n; i++ {
		if content[i] == 0 {
			return true
		}
	}
	return false
}

func splitDocSearchLines(s string) []string {
	if s == "" {
		return []string{""}
	}
	lines := strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n")
	if len(lines) == 0 {
		return []string{""}
	}
	return lines
}

func buildDocSearchCitations(results []DocSearchFileResult, max int) []string {
	if max <= 0 {
		max = 128
	}
	out := make([]string, 0, len(results))
	for _, r := range results {
		p := strings.TrimSpace(r.Path)
		if p == "" {
			continue
		}
		out = append(out, p)
		if len(out) >= max {
			break
		}
	}
	return uniqueStrings(out)
}

func docSearchRelPath(rootAbs string, path string) string {
	rel, err := filepath.Rel(rootAbs, path)
	if err != nil {
		return filepath.ToSlash(strings.TrimSpace(path))
	}
	return filepath.ToSlash(strings.TrimSpace(rel))
}

func docSearchReportJSON(report DocSearchReport) string {
	b, _ := json.Marshal(report)
	return string(b)
}

func parseDocSearchCSV(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		out = append(out, p)
	}
	return out
}
