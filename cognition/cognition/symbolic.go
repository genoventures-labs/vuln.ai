package cognition

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/Thynaptic/P-LMv1/pkg/memory"
	"github.com/Thynaptic/P-LMv1/pkg/state"
	"github.com/ollama/ollama/api"
)

const symbolicTimeout = 8 * time.Second

var symbolicModels = []string{
	"llama3.2:1b",
	"qwen2.5:3b-instruct",
}

var splitSentenceRE = regexp.MustCompile(`[.!?]\s+`)
var fileClaimRE = regexp.MustCompile(`(?i)\b(?:file|path|directory)\s+([~/.\w-][\w./-]*)\s+(exists|is present|is available|is missing|does not exist)\b`)
var pathClaimRE = regexp.MustCompile(`(?i)\b([~/.][\w./-]+)\s+(exists|is missing|does not exist)\b`)
var metricClaimRE = regexp.MustCompile(`(?i)\b(confidence|frustration|analytical mode|urgency)\s+(?:is|=)\s*(high|low|medium|balanced|[0-9]*\.?[0-9]+)\b`)
var commandClaimRE = regexp.MustCompile(`(?i)\b(command|ls|cat|grep|find|stat)\s+([^\n,;]+?)\s+(failed|succeeded|passed)\b`)
var secretKeyPairRE = regexp.MustCompile(`(?i)\b(GLM_API_KEY|GLM_ADMIN_TOKEN|api[_-]?key|secret|token)\b\s*[:=]\s*['"]?[A-Za-z0-9_\-]{16,}['"]?`)
var bearerLikeRE = regexp.MustCompile(`(?i)\bBearer\s+[A-Za-z0-9_\-]{16,}\b`)

// DetectSecretLeaks checks whether output contains credential-like values.
func DetectSecretLeaks(text string) []string {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}
	var out []string
	if secretKeyPairRE.MatchString(text) {
		out = append(out, "credential assignment pattern detected")
	}
	if bearerLikeRE.MatchString(text) {
		out = append(out, "bearer token pattern detected")
	}
	return dedupeViolations(out)
}

// LogicalClaim is a parsed assertion extracted from generated draft text.
type LogicalClaim struct {
	Raw       string  `json:"raw"`
	Type      string  `json:"type"`
	Subject   string  `json:"subject"`
	Predicate string  `json:"predicate"`
	Object    string  `json:"object"`
	ScoreHint float64 `json:"score_hint"`
}

// SymbolicCheckResult captures deterministic veto checks against ground truth.
type SymbolicCheckResult struct {
	Claims      []LogicalClaim `json:"claims"`
	GroundTruth []string       `json:"ground_truth"`
	Violations  []string       `json:"violations"`
	Veto        bool           `json:"veto"`
}

// SelfPlayResult captures adversarial review/refinement outcomes.
type SelfPlayResult struct {
	FinalCandidate   string
	MaxFlawScore     float64
	Cycles           int
	Contradictions   int
	OpponentFinding  string
	WinningVector    string
	WinningRationale string
	AttackVectors    []SelfPlayAttackResult
}

// SelfPlayAttackResult captures one adversarial attack vector run.
type SelfPlayAttackResult struct {
	VectorName      string
	FinalCandidate  string
	MaxFlawScore    float64
	Contradictions  int
	Cycles          int
	OpponentFinding string
	BranchScore     float64
}

// CodeAuditOptions carries symbolic code-audit inputs.
type CodeAuditOptions struct {
	Query               string
	Code                string
	ProjectPackages     []string
	GoModule            string
	AllowedGoModules    []string
	AllowedNodePackages []string
}

// AuditGoCodeSymbolic runs deterministic code-policy checks used by final gating.
func AuditGoCodeSymbolic(opts CodeAuditOptions) []string {
	code := strings.TrimSpace(opts.Code)
	if code == "" {
		return nil
	}

	pkgSet := map[string]bool{}
	for _, p := range opts.ProjectPackages {
		pkgSet[strings.TrimSpace(p)] = true
	}
	allowedGo := map[string]bool{}
	for _, m := range opts.AllowedGoModules {
		allowedGo[strings.TrimSpace(m)] = true
	}
	if strings.TrimSpace(opts.GoModule) != "" {
		allowedGo[strings.TrimSpace(opts.GoModule)] = true
	}

	var violations []string
	if v := auditForbiddenPatterns(code, pkgSet); len(v) > 0 {
		violations = append(violations, v...)
	}
	if v := auditArchitectureAlignment(opts.Query, code); len(v) > 0 {
		violations = append(violations, v...)
	}
	if v := auditGoDependencies(code, strings.TrimSpace(opts.GoModule), allowedGo); len(v) > 0 {
		violations = append(violations, v...)
	}
	return dedupeViolations(violations)
}

// DetectContradiction estimates whether two claims contradict each other.
// Returns 0.0 (consistent) to 1.0 (hard contradiction).
func DetectContradiction(claimA, claimB string) float64 {
	claimA = strings.TrimSpace(claimA)
	claimB = strings.TrimSpace(claimB)
	if claimA == "" || claimB == "" {
		return 0
	}
	if strings.EqualFold(claimA, claimB) {
		return 0
	}

	client, err := api.ClientFromEnvironment()
	if err != nil {
		return heuristicContradiction(claimA, claimB)
	}

	system := `You are a strict contradiction detector.
Score whether two claims contradict each other.
Return JSON only:
{"contradiction":0.0}
Rules:
- 0.0 = fully consistent or unrelated
- 1.0 = direct factual contradiction`

	user := `{"claim_a":` + quoteJSON(claimA) + `,"claim_b":` + quoteJSON(claimB) + `}`
	messages := []api.Message{
		{Role: "system", Content: system},
		{Role: "user", Content: user},
	}

	for _, model := range symbolicModels {
		opts, _ := state.ResolveEntropyOptions(user)
		req := &api.ChatRequest{
			Model:    model,
			Options:  opts,
			Messages: messages,
		}
		ctx, cancel := context.WithTimeout(context.Background(), symbolicTimeout)
		var out strings.Builder
		err := client.Chat(ctx, req, func(resp api.ChatResponse) error {
			out.WriteString(resp.Message.Content)
			return nil
		})
		cancel()
		if err != nil {
			continue
		}
		if score, ok := parseContradictionScore(out.String()); ok {
			return clampScore(score)
		}
	}

	return heuristicContradiction(claimA, claimB)
}

func auditForbiddenPatterns(code string, projectPkgs map[string]bool) []string {
	var out []string
	if hasHardcodedSecret(code) {
		out = append(out, "forbidden pattern: possible hardcoded secret/token detected")
	}

	importsFmt := regexp.MustCompile(`(?m)^\s*"fmt"\s*$`).MatchString(code) || strings.Contains(code, "fmt.Print")
	usesLogger := strings.Contains(code, "pkg/logger") || strings.Contains(code, "logger.")
	if importsFmt && !usesLogger && projectPkgs["pkg/logger"] {
		out = append(out, "forbidden pattern: use pkg/logger instead of fmt.Print* for runtime logging")
	}

	dropErrPattern := regexp.MustCompile(`(?m)^\s*[^/\n]*,\s*_\s*:?=\s*[^=\n]+\(`)
	if dropErrPattern.MatchString(code) {
		out = append(out, "forbidden pattern: discarded error value ('_, _' style assignment)")
	}
	singleDropErr := regexp.MustCompile(`(?m)^\s*_\s*=\s*[^=\n]+\(`)
	if singleDropErr.MatchString(code) {
		out = append(out, "forbidden pattern: unhandled return value assigned to blank identifier")
	}
	return out
}

func auditArchitectureAlignment(query, code string) []string {
	q := strings.ToLower(strings.TrimSpace(query))
	pkgName := "unknown"
	if m := regexp.MustCompile(`(?m)^\s*package\s+([A-Za-z_]\w*)`).FindStringSubmatch(code); len(m) == 2 {
		pkgName = strings.TrimSpace(m[1])
	}

	isCLIIntent := symbolicHasAny(q, "cmd/", "cli", "command", "cobra", "terminal command")
	isPkgIntent := symbolicHasAny(q, "pkg/", "library", "utility", "reusable", "module")

	var out []string
	if isCLIIntent && pkgName != "main" {
		out = append(out, "architecture mismatch: CLI command code must use package main under cmd/")
	}
	if isPkgIntent && pkgName == "main" {
		out = append(out, "architecture mismatch: reusable logic should live in pkg/ (non-main package)")
	}

	if pkgName == "main" && !isCLIIntent {
		reFunc := regexp.MustCompile(`(?m)^\s*func\s+([A-Za-z_]\w*)\s*\(`)
		funcs := reFunc.FindAllStringSubmatch(code, -1)
		if len(funcs) > 2 {
			out = append(out, "architecture mismatch: business logic detected in package main; move logic into pkg/")
		}
	}
	return out
}

func auditGoDependencies(code, module string, allowed map[string]bool) []string {
	imps := extractGoImports(code)
	if len(imps) == 0 {
		return nil
	}
	var out []string
	for _, imp := range imps {
		imp = strings.TrimSpace(imp)
		if imp == "" {
			continue
		}
		if isStdGoImport(imp) {
			continue
		}
		if module != "" && (imp == module || strings.HasPrefix(imp, module+"/")) {
			continue
		}
		if strings.HasPrefix(imp, "pkg/") || strings.HasPrefix(imp, "cmd/") {
			continue
		}
		if isAllowedExternalImport(imp, allowed) {
			continue
		}
		out = append(out, "external dependency not approved by go.mod: "+imp)
	}
	return out
}

func extractGoImports(code string) []string {
	var out []string
	seen := map[string]bool{}

	blockRe := regexp.MustCompile(`(?s)import\s*\((.*?)\)`)
	qRe := regexp.MustCompile(`"([^"]+)"`)
	for _, m := range blockRe.FindAllStringSubmatch(code, -1) {
		if len(m) != 2 {
			continue
		}
		for _, qm := range qRe.FindAllStringSubmatch(m[1], -1) {
			if len(qm) != 2 {
				continue
			}
			v := strings.TrimSpace(qm[1])
			if v == "" || seen[v] {
				continue
			}
			seen[v] = true
			out = append(out, v)
		}
	}

	singleRe := regexp.MustCompile(`(?m)^\s*import\s+(?:[A-Za-z_]\w+\s+)?"([^"]+)"\s*$`)
	for _, m := range singleRe.FindAllStringSubmatch(code, -1) {
		if len(m) != 2 {
			continue
		}
		v := strings.TrimSpace(m[1])
		if v == "" || seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	return out
}

func isStdGoImport(imp string) bool {
	if imp == "" {
		return true
	}
	head := imp
	if i := strings.Index(head, "/"); i >= 0 {
		head = head[:i]
	}
	return !strings.Contains(head, ".")
}

func isAllowedExternalImport(imp string, allowed map[string]bool) bool {
	for mod := range allowed {
		mod = strings.TrimSpace(mod)
		if mod == "" {
			continue
		}
		if imp == mod || strings.HasPrefix(imp, mod+"/") || strings.HasPrefix(mod, imp+"/") {
			return true
		}
	}
	return false
}

func hasHardcodedSecret(code string) bool {
	re := regexp.MustCompile(`(?i)(api[_-]?key|secret|token|password)\s*[:=]\s*["'][^"']{10,}["']`)
	return re.MatchString(code)
}

func dedupeViolations(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	seen := map[string]bool{}
	var out []string
	for _, v := range in {
		v = strings.TrimSpace(v)
		if v == "" || seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	return out
}

// LoadDependencyAllowlist builds allowlists from go.mod and package.json.
func LoadDependencyAllowlist(root string) (goModule string, goAllowed []string, nodeAllowed []string) {
	root = strings.TrimSpace(root)
	if root == "" {
		root = "."
	}
	goModule, goAllowed = parseGoModAllowlist(filepath.Join(root, "go.mod"))
	nodeAllowed = parsePackageJSONAllowlist(filepath.Join(root, "package.json"))
	return goModule, goAllowed, nodeAllowed
}

func parseGoModAllowlist(path string) (string, []string) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", nil
	}
	lines := strings.Split(string(raw), "\n")
	module := ""
	allowed := map[string]bool{}
	inRequire := false
	for _, line := range lines {
		l := strings.TrimSpace(line)
		if l == "" || strings.HasPrefix(l, "//") {
			continue
		}
		if strings.HasPrefix(l, "module ") {
			module = strings.TrimSpace(strings.TrimPrefix(l, "module "))
			continue
		}
		if strings.HasPrefix(l, "require (") {
			inRequire = true
			continue
		}
		if inRequire && l == ")" {
			inRequire = false
			continue
		}
		if strings.HasPrefix(l, "require ") {
			parts := strings.Fields(strings.TrimSpace(strings.TrimPrefix(l, "require ")))
			if len(parts) > 0 {
				allowed[parts[0]] = true
			}
			continue
		}
		if inRequire {
			parts := strings.Fields(l)
			if len(parts) > 0 {
				allowed[parts[0]] = true
			}
		}
	}
	out := make([]string, 0, len(allowed))
	for k := range allowed {
		out = append(out, k)
	}
	return strings.TrimSpace(module), out
}

func parsePackageJSONAllowlist(path string) []string {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var parsed struct {
		Dependencies    map[string]string `json:"dependencies"`
		DevDependencies map[string]string `json:"devDependencies"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil
	}
	allowed := map[string]bool{}
	for k := range parsed.Dependencies {
		allowed[k] = true
	}
	for k := range parsed.DevDependencies {
		allowed[k] = true
	}
	out := make([]string, 0, len(allowed))
	for k := range allowed {
		out = append(out, k)
	}
	return out
}

// EvaluateLogic checks an output candidate against itself and external context.
// Returns 0.0 (logically consistent) to 1.0 (hard contradiction present).
func EvaluateLogic(output string, context []string) float64 {
	output = strings.TrimSpace(output)
	if output == "" {
		return 0
	}

	maxScore := 0.0
	for _, fact := range context {
		score := DetectContradiction(output, fact)
		if score > maxScore {
			maxScore = score
		}
		if maxScore >= 0.92 {
			return 1.0
		}
	}

	// Self-consistency: compare sentence pairs for internal contradiction.
	sentences := splitIntoSentences(output)
	for i := 0; i < len(sentences); i++ {
		for j := i + 1; j < len(sentences); j++ {
			score := DetectContradiction(sentences[i], sentences[j])
			if score > maxScore {
				maxScore = score
			}
			if maxScore >= 0.92 {
				return 1.0
			}
		}
	}
	return clampScore(maxScore)
}

// ExtractClaims parses explicit factual assertions from a draft answer.
func ExtractClaims(draft string) []LogicalClaim {
	draft = strings.TrimSpace(draft)
	if draft == "" {
		return nil
	}
	sentences := splitIntoSentences(draft)
	out := make([]LogicalClaim, 0, len(sentences))
	for _, s := range sentences {
		line := strings.TrimSpace(s)
		if line == "" {
			continue
		}

		if m := fileClaimRE.FindStringSubmatch(line); len(m) == 3 {
			out = append(out, LogicalClaim{
				Raw:       line,
				Type:      "filesystem",
				Subject:   strings.TrimSpace(m[1]),
				Predicate: strings.ToLower(strings.TrimSpace(m[2])),
				ScoreHint: 0.9,
			})
			continue
		}
		if m := pathClaimRE.FindStringSubmatch(line); len(m) == 3 {
			out = append(out, LogicalClaim{
				Raw:       line,
				Type:      "filesystem",
				Subject:   strings.TrimSpace(m[1]),
				Predicate: strings.ToLower(strings.TrimSpace(m[2])),
				ScoreHint: 0.85,
			})
			continue
		}
		if m := metricClaimRE.FindStringSubmatch(line); len(m) == 3 {
			out = append(out, LogicalClaim{
				Raw:       line,
				Type:      "state_metric",
				Subject:   strings.ToLower(strings.TrimSpace(m[1])),
				Object:    strings.ToLower(strings.TrimSpace(m[2])),
				ScoreHint: 0.8,
			})
			continue
		}
		if m := commandClaimRE.FindStringSubmatch(line); len(m) == 4 {
			out = append(out, LogicalClaim{
				Raw:       line,
				Type:      "command_status",
				Subject:   strings.ToLower(strings.TrimSpace(m[1])),
				Object:    strings.TrimSpace(m[2]),
				Predicate: strings.ToLower(strings.TrimSpace(m[3])),
				ScoreHint: 0.85,
			})
			continue
		}
		if looksAssertive(line) {
			out = append(out, LogicalClaim{
				Raw:       line,
				Type:      "generic",
				ScoreHint: 0.45,
			})
		}
	}
	if len(out) > 24 {
		out = out[:24]
	}
	return out
}

// CollectLiveShellFacts extracts shell-ground-truth snippets from memory.
func CollectLiveShellFacts(mm *memory.MemoryManager, query string, k int) []string {
	if mm == nil {
		return nil
	}
	if k <= 0 {
		k = 10
	}
	segs, err := mm.RetrieveKnowledgeSegments("live shell terminal stdout stderr cli ls stat "+strings.TrimSpace(query), k*3)
	if err != nil || len(segs) == 0 {
		return nil
	}
	var out []string
	seen := map[string]bool{}
	for _, s := range segs {
		src := strings.ToLower(strings.TrimSpace(s.Metadata["source_type"] + " " + s.Metadata["source_path"]))
		content := strings.TrimSpace(s.Content)
		if content == "" {
			continue
		}
		if !symbolicHasAny(src, "shell", "terminal", "stdout", "stderr", "cli", "command", "log", "runtime") &&
			!symbolicHasAny(strings.ToLower(content), "no such file", "cannot access", "permission denied", "not found", "$ ", "ls:", "stat:") {
			continue
		}
		line := truncateSymbolic(strings.ReplaceAll(content, "\n", " "), 260)
		if seen[line] {
			continue
		}
		seen[line] = true
		out = append(out, line)
		if len(out) >= k {
			break
		}
	}
	return out
}

// CheckSymbolicAssertions verifies draft claims against session state + live shell facts.
// If a formal mismatch is found, it returns Veto=true.
func CheckSymbolicAssertions(draft string, session state.SessionState, liveShellFacts []string) SymbolicCheckResult {
	claims := ExtractClaims(draft)
	result := SymbolicCheckResult{
		Claims:      claims,
		GroundTruth: buildSymbolicGroundTruth(session, liveShellFacts),
	}
	if len(claims) == 0 {
		return result
	}

	for _, c := range claims {
		switch c.Type {
		case "filesystem":
			path := normalizePathToken(c.Subject)
			if path == "" {
				continue
			}
			wantExists := !symbolicHasAny(c.Predicate, "missing", "does not exist")
			if contradiction := contradictsFilesystemClaim(path, wantExists, liveShellFacts); contradiction != "" {
				result.Violations = append(result.Violations, contradiction)
			}

		case "state_metric":
			if violation := checkStateMetricClaim(c, session); violation != "" {
				result.Violations = append(result.Violations, violation)
			}

		case "command_status":
			if violation := checkCommandStatusClaim(c, liveShellFacts); violation != "" {
				result.Violations = append(result.Violations, violation)
			}
		}
	}

	result.Veto = len(result.Violations) > 0
	return result
}

func splitIntoSentences(s string) []string {
	raw := splitSentenceRE.Split(strings.TrimSpace(s), -1)
	out := make([]string, 0, len(raw))
	for _, r := range raw {
		r = strings.TrimSpace(r)
		if r == "" {
			continue
		}
		out = append(out, r)
	}
	return out
}

func parseContradictionScore(raw string) (float64, bool) {
	type payload struct {
		Contradiction float64 `json:"contradiction"`
		Score         float64 `json:"score"`
	}
	clean := strings.TrimSpace(stripMarkdownCodeFences(raw))

	var p payload
	if err := json.Unmarshal([]byte(clean), &p); err == nil {
		if !math.IsNaN(p.Contradiction) && !math.IsInf(p.Contradiction, 0) {
			return p.Contradiction, true
		}
		if !math.IsNaN(p.Score) && !math.IsInf(p.Score, 0) {
			return p.Score, true
		}
	}
	start := strings.Index(clean, "{")
	end := strings.LastIndex(clean, "}")
	if start >= 0 && end > start {
		if err := json.Unmarshal([]byte(clean[start:end+1]), &p); err == nil {
			if !math.IsNaN(p.Contradiction) && !math.IsInf(p.Contradiction, 0) {
				return p.Contradiction, true
			}
			if !math.IsNaN(p.Score) && !math.IsInf(p.Score, 0) {
				return p.Score, true
			}
		}
	}
	return 0, false
}

func heuristicContradiction(a, b string) float64 {
	la := strings.ToLower(a)
	lb := strings.ToLower(b)
	if la == lb {
		return 0
	}

	negA := containsNegation(la)
	negB := containsNegation(lb)
	shared := lexicalOverlap(la, lb)

	if shared >= 0.45 && negA != negB {
		return 0.85
	}
	if shared >= 0.6 {
		return 0.1
	}
	if shared < 0.1 {
		return 0.05
	}
	return 0.25
}

func containsNegation(s string) bool {
	for _, t := range []string{" not ", " no ", " never ", " cannot ", " can't ", " isn't ", " won't ", " doesn't "} {
		if strings.Contains(" "+s+" ", t) {
			return true
		}
	}
	return false
}

func lexicalOverlap(a, b string) float64 {
	ta := tokenize(a)
	tb := tokenize(b)
	if len(ta) == 0 || len(tb) == 0 {
		return 0
	}
	setA := make(map[string]bool, len(ta))
	for _, t := range ta {
		setA[t] = true
	}
	shared := 0
	for _, t := range tb {
		if setA[t] {
			shared++
		}
	}
	den := len(ta)
	if len(tb) > den {
		den = len(tb)
	}
	return float64(shared) / float64(den)
}

func tokenize(s string) []string {
	s = strings.ToLower(strings.TrimSpace(s))
	repl := strings.NewReplacer(",", " ", ".", " ", ";", " ", ":", " ", "(", " ", ")", " ", "[", " ", "]", " ", "{", " ", "}", " ", "\"", " ")
	s = repl.Replace(s)
	var out []string
	for _, w := range strings.Fields(s) {
		if len(w) < 3 {
			continue
		}
		out = append(out, w)
	}
	return out
}

func quoteJSON(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

func clampScore(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

func looksAssertive(s string) bool {
	l := strings.ToLower(strings.TrimSpace(s))
	if l == "" {
		return false
	}
	if strings.HasSuffix(l, "?") {
		return false
	}
	return symbolicHasAny(l, " is ", " are ", " was ", " were ", " has ", " have ", " runs ", " uses ", " exists ", " failed ", " succeeded ")
}

func buildSymbolicGroundTruth(session state.SessionState, shellFacts []string) []string {
	truth := []string{
		fmt.Sprintf("state.confidence=%.2f", session.Confidence),
		fmt.Sprintf("state.urgency=%.2f", session.Urgency),
		fmt.Sprintf("state.analytical_mode=%.2f", session.AnalyticalMode),
		fmt.Sprintf("state.frustration=%.2f", session.Frustration),
	}
	for _, f := range shellFacts {
		if strings.TrimSpace(f) == "" {
			continue
		}
		truth = append(truth, "shell: "+truncateSymbolic(f, 220))
	}
	if len(truth) > 28 {
		truth = truth[:28]
	}
	return truth
}

func contradictsFilesystemClaim(path string, wantExists bool, facts []string) string {
	if len(facts) == 0 {
		return ""
	}
	lp := strings.ToLower(path)
	for _, f := range facts {
		lf := strings.ToLower(f)
		if !strings.Contains(lf, lp) && !strings.Contains(lp, strings.TrimSpace(filepathBase(lp))) {
			continue
		}
		missing := symbolicHasAny(lf, "no such file or directory", "cannot access", "not found", "missing", "does not exist")
		present := symbolicHasAny(lf, "drwx", "-rw", "exists", "present", "found", "total ")
		if wantExists && missing {
			return "symbolic veto: claim says path exists but live shell evidence reports missing: " + path
		}
		if !wantExists && present && !missing {
			return "symbolic veto: claim says path missing but live shell evidence indicates presence: " + path
		}
	}
	return ""
}

func checkCommandStatusClaim(c LogicalClaim, facts []string) string {
	if len(facts) == 0 {
		return ""
	}
	wantSuccess := c.Predicate == "succeeded" || c.Predicate == "passed"
	needle := strings.ToLower(strings.TrimSpace(c.Subject + " " + c.Object))
	for _, f := range facts {
		lf := strings.ToLower(f)
		if needle != "" && !strings.Contains(lf, strings.Fields(needle)[0]) {
			continue
		}
		failed := symbolicHasAny(lf, "error", "failed", "exit status", "no such file", "cannot access", "permission denied")
		if wantSuccess && failed {
			return "symbolic veto: claim says command succeeded but shell evidence records failure"
		}
		if !wantSuccess && symbolicHasAny(lf, "succeeded", "ok", "completed") {
			return "symbolic veto: claim says command failed but shell evidence records success"
		}
	}
	return ""
}

func checkStateMetricClaim(c LogicalClaim, s state.SessionState) string {
	metric := metricValue(c.Subject, s)
	if metric < 0 {
		return ""
	}
	label := strings.TrimSpace(c.Object)
	if label == "" {
		return ""
	}
	if n, err := strconv.ParseFloat(label, 64); err == nil {
		if math.Abs(n-metric) > 0.16 {
			return fmt.Sprintf("symbolic veto: claim says %s=%.2f but state model is %.2f", c.Subject, n, metric)
		}
		return ""
	}

	switch label {
	case "high":
		if metric < 0.67 {
			return fmt.Sprintf("symbolic veto: claim says %s is high but state model is %.2f", c.Subject, metric)
		}
	case "low":
		if metric > 0.33 {
			return fmt.Sprintf("symbolic veto: claim says %s is low but state model is %.2f", c.Subject, metric)
		}
	case "medium", "balanced":
		if metric < 0.30 || metric > 0.70 {
			return fmt.Sprintf("symbolic veto: claim says %s is balanced but state model is %.2f", c.Subject, metric)
		}
	}
	return ""
}

func metricValue(metric string, s state.SessionState) float64 {
	switch strings.ToLower(strings.TrimSpace(metric)) {
	case "confidence":
		return s.Confidence
	case "urgency":
		return s.Urgency
	case "analytical mode":
		return s.AnalyticalMode
	case "frustration":
		return s.Frustration
	default:
		return -1
	}
}

func normalizePathToken(p string) string {
	p = strings.TrimSpace(strings.Trim(p, ".,;:()[]{}\"'"))
	if p == "" {
		return ""
	}
	// keep only plausible path-like tokens
	if strings.ContainsAny(p, "/~.") {
		return p
	}
	return ""
}

func symbolicHasAny(s string, needles ...string) bool {
	for _, n := range needles {
		if strings.Contains(s, n) {
			return true
		}
	}
	return false
}

func truncateSymbolic(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	if n < 4 {
		return s[:n]
	}
	return s[:n-3] + "..."
}

func filepathBase(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	parts := strings.Split(path, "/")
	if len(parts) == 0 {
		return path
	}
	return parts[len(parts)-1]
}

// ConductSelfPlay runs an internal opponent-vs-author loop to harden a candidate.
// It performs up to 2 recursive rebuttal/refinement cycles when flaw score > 0.7.
func ConductSelfPlay(candidate string, refs []string) SelfPlayResult {
	current := strings.TrimSpace(candidate)
	if current == "" {
		return SelfPlayResult{}
	}

	result := SelfPlayResult{
		FinalCandidate: current,
	}

	client, err := api.ClientFromEnvironment()
	if err != nil {
		// Fallback: no LLM available, only run symbolic contradiction checks.
		score := EvaluateLogic(current, refs)
		result.MaxFlawScore = score
		if score >= 0.7 {
			result.Contradictions = 1
		}
		result.WinningVector = "symbolic_fallback"
		result.WinningRationale = "LLM unavailable; selected symbolic consistency path."
		result.AttackVectors = []SelfPlayAttackResult{
			{
				VectorName:     result.WinningVector,
				FinalCandidate: current,
				MaxFlawScore:   score,
				Contradictions: result.Contradictions,
				BranchScore:    scoreSelfPlayBranch(score, result.Contradictions, 0),
			},
		}
		return result
	}

	maxCycles := 2
	vectors := buildSelfPlayVectors()
	branches := make([]selfPlayBranch, 0, len(vectors))
	for _, vector := range vectors {
		branchCandidate := current
		branchCycles := 0
		branchContradictions := 0
		branchMaxFlaw := 0.0
		branchCritique := ""

		for cycle := 0; cycle < maxCycles; cycle++ {
			flawScore, critique := runOpponentCritiqueWithVector(client, branchCandidate, refs, vector)
			if flawScore > branchMaxFlaw {
				branchMaxFlaw = flawScore
			}
			branchCritique = critique

			contradiction := EvaluateLogic(branchCandidate, refs)
			if contradiction >= 0.7 {
				branchContradictions++
			}
			if flawScore <= 0.7 {
				break
			}

			refined, ok := runAuthorRebuttalWithVector(client, branchCandidate, critique, refs, vector)
			if !ok || strings.TrimSpace(refined) == "" {
				break
			}

			branchCycles = cycle + 1
			branchCandidate = strings.TrimSpace(refined)
		}

		branches = append(branches, selfPlayBranch{
			vectorName:      vector.Name,
			finalCandidate:  strings.TrimSpace(branchCandidate),
			maxFlawScore:    branchMaxFlaw,
			contradictions:  branchContradictions,
			cycles:          branchCycles,
			opponentFinding: strings.TrimSpace(branchCritique),
			branchScore:     scoreSelfPlayBranch(branchMaxFlaw, branchContradictions, branchCycles),
		})
	}

	best, ok := selectStrongestSelfPlayBranch(branches)
	if !ok {
		return result
	}
	result.FinalCandidate = best.finalCandidate
	result.MaxFlawScore = best.maxFlawScore
	result.Cycles = best.cycles
	result.Contradictions = best.contradictions
	result.OpponentFinding = best.opponentFinding
	result.WinningVector = best.vectorName
	result.WinningRationale = "Selected branch with minimum adversarial flaw score after vectorized internal attack."
	result.AttackVectors = make([]SelfPlayAttackResult, 0, len(branches))
	for _, b := range branches {
		result.AttackVectors = append(result.AttackVectors, SelfPlayAttackResult{
			VectorName:      b.vectorName,
			FinalCandidate:  b.finalCandidate,
			MaxFlawScore:    b.maxFlawScore,
			Contradictions:  b.contradictions,
			Cycles:          b.cycles,
			OpponentFinding: b.opponentFinding,
			BranchScore:     b.branchScore,
		})
	}

	return result
}

func runOpponentCritique(client *api.Client, candidate string, refs []string) (float64, string) {
	return runOpponentCritiqueWithVector(client, candidate, refs, selfPlayVector{})
}

type selfPlayVector struct {
	Name   string
	Prompt string
}

type selfPlayBranch struct {
	vectorName      string
	finalCandidate  string
	maxFlawScore    float64
	contradictions  int
	cycles          int
	opponentFinding string
	branchScore     float64
}

func buildSelfPlayVectors() []selfPlayVector {
	return []selfPlayVector{
		{Name: "logic_contradiction", Prompt: "Attack logical consistency and contradiction handling."},
		{Name: "evidence_coverage", Prompt: "Attack missing evidence, citation weakness, and unsupported claims."},
		{Name: "security_exploitability", Prompt: "Attack security assumptions, exploit paths, and risk under adversarial conditions."},
		{Name: "implementation_feasibility", Prompt: "Attack operational feasibility, hidden dependencies, and rollout risk."},
	}
}

func scoreSelfPlayBranch(maxFlaw float64, contradictions int, cycles int) float64 {
	return clampScore(maxFlaw) + (0.12 * float64(maxSelfPlayInt(contradictions, 0))) + (0.03 * float64(maxSelfPlayInt(cycles, 0)))
}

func selectStrongestSelfPlayBranch(branches []selfPlayBranch) (selfPlayBranch, bool) {
	if len(branches) == 0 {
		return selfPlayBranch{}, false
	}
	best := branches[0]
	for i := 1; i < len(branches); i++ {
		b := branches[i]
		switch {
		case b.branchScore < best.branchScore:
			best = b
		case b.branchScore == best.branchScore && b.maxFlawScore < best.maxFlawScore:
			best = b
		case b.branchScore == best.branchScore && b.maxFlawScore == best.maxFlawScore && b.contradictions < best.contradictions:
			best = b
		}
	}
	return best, true
}

func maxSelfPlayInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func runOpponentCritiqueWithVector(client *api.Client, candidate string, refs []string, vector selfPlayVector) (float64, string) {
	system := `You are an adversarial evaluator.
Find logical gaps, missing edge cases, or factual inconsistencies.
Return JSON only:
{"flaw_score":0.0,"critique":"..."}
Where flaw_score in [0,1] and >0.7 means significant flaw.`

	user := "Candidate answer:\n" + candidate + "\n\nReference context:\n- " + strings.Join(refs, "\n- ")
	if strings.TrimSpace(vector.Name) != "" {
		system += "\nUse the declared attack vector to maximize pressure."
		user += "\n\nAttack vector: " + vector.Name + "\nAttack objective: " + strings.TrimSpace(vector.Prompt)
	}
	messages := []api.Message{
		{Role: "system", Content: system},
		{Role: "user", Content: user},
	}

	for _, model := range symbolicModels {
		opts, _ := state.ResolveEntropyOptions(user)
		req := &api.ChatRequest{
			Model:    model,
			Options:  opts,
			Messages: messages,
		}
		ctx, cancel := context.WithTimeout(context.Background(), symbolicTimeout)
		var out strings.Builder
		err := client.Chat(ctx, req, func(resp api.ChatResponse) error {
			out.WriteString(resp.Message.Content)
			return nil
		})
		cancel()
		if err != nil {
			continue
		}
		score, critique, ok := parseCritiquePayload(out.String())
		if ok {
			return clampScore(score), strings.TrimSpace(critique)
		}
	}

	// Heuristic fallback
	score := EvaluateLogic(candidate, refs)
	critique := "Potential inconsistency found by symbolic heuristic."
	if strings.TrimSpace(vector.Name) != "" {
		critique = vector.Name + ": " + critique
	}
	return score, critique
}

func runAuthorRebuttal(client *api.Client, candidate string, critique string, refs []string) (string, bool) {
	return runAuthorRebuttalWithVector(client, candidate, critique, refs, selfPlayVector{})
}

func runAuthorRebuttalWithVector(client *api.Client, candidate string, critique string, refs []string, vector selfPlayVector) (string, bool) {
	system := `You are the original author improving an answer under critique.
Address the criticism directly, correct factual issues, and keep the answer concise.
Return only the revised answer text.`
	user := "Original candidate:\n" + candidate +
		"\n\nOpponent critique:\n" + critique +
		"\n\nReference context:\n- " + strings.Join(refs, "\n- ")
	if strings.TrimSpace(vector.Name) != "" {
		user += "\n\nTarget attack vector to satisfy: " + vector.Name
	}
	messages := []api.Message{
		{Role: "system", Content: system},
		{Role: "user", Content: user},
	}

	for _, model := range symbolicModels {
		opts, _ := state.ResolveEntropyOptions(user)
		req := &api.ChatRequest{
			Model:    model,
			Options:  opts,
			Messages: messages,
		}
		ctx, cancel := context.WithTimeout(context.Background(), symbolicTimeout)
		var out strings.Builder
		err := client.Chat(ctx, req, func(resp api.ChatResponse) error {
			out.WriteString(resp.Message.Content)
			return nil
		})
		cancel()
		if err != nil {
			continue
		}
		refined := strings.TrimSpace(stripMarkdownCodeFences(out.String()))
		if refined != "" {
			return refined, true
		}
	}
	return "", false
}

func parseCritiquePayload(raw string) (float64, string, bool) {
	type payload struct {
		FlawScore float64 `json:"flaw_score"`
		Score     float64 `json:"score"`
		Critique  string  `json:"critique"`
	}

	clean := strings.TrimSpace(stripMarkdownCodeFences(raw))
	var p payload
	if err := json.Unmarshal([]byte(clean), &p); err == nil {
		score := p.FlawScore
		if math.IsNaN(score) || math.IsInf(score, 0) {
			score = p.Score
		}
		if !math.IsNaN(score) && !math.IsInf(score, 0) {
			return score, p.Critique, true
		}
	}
	start := strings.Index(clean, "{")
	end := strings.LastIndex(clean, "}")
	if start >= 0 && end > start {
		if err := json.Unmarshal([]byte(clean[start:end+1]), &p); err == nil {
			score := p.FlawScore
			if math.IsNaN(score) || math.IsInf(score, 0) {
				score = p.Score
			}
			if !math.IsNaN(score) && !math.IsInf(score, 0) {
				return score, p.Critique, true
			}
		}
	}
	return 0, "", false
}
