package scanner

import (
	"bufio"
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	sitter "github.com/smacker/go-tree-sitter"
	"github.com/smacker/go-tree-sitter/golang"
	"github.com/smacker/go-tree-sitter/java"
	"github.com/smacker/go-tree-sitter/javascript"
	"github.com/smacker/go-tree-sitter/python"
)

type Finding struct {
	ID          string `json:"id"`
	File        string `json:"file"`
	Line        int    `json:"line"`
	VulnType    string `json:"vuln_type"`
	Severity    string `json:"severity"`
	Description string `json:"description"`
	Snippet     string `json:"snippet"`
}

type Scanner struct {
	Rules []Rule
}

type Rule struct {
	ID          string
	Type        string
	Description string
	Severity    string
	Pattern     *regexp.Regexp
	TSQuery     string // Tree-sitter query string
}

func NewScanner() *Scanner {
	return &Scanner{
		Rules: []Rule{
			{
				ID:          "OWASP-A01-IDOR",
				Type:        "Broken Access Control / IDOR",
				Description: "Potential IDOR: Resource fetched by ID. Verify ownership filters.",
				Severity:    "Critical",
				Pattern:     regexp.MustCompile(`(?i)(db|repo|model).*(First|Find|Get|Where).*\bid\b`),
			},
			{
				ID:          "OWASP-A01-OWNERSHIP",
				Type:        "Broken Access Control / Ownership",
				Description: "Potential missing ownership check: Resource modification by ID.",
				Severity:    "Critical",
				Pattern:     regexp.MustCompile(`(?i)(db|repo|model).*(Update|Delete|Save).*\bid\b`),
			},
			{
				ID:          "OWASP-A01-ESCALATION",
				Type:        "Broken Access Control / Escalation",
				Description: "Potential privilege escalation: Sensitive fields (role, is_admin) updated from untrusted input.",
				Severity:    "Critical",
				Pattern:     regexp.MustCompile(`(?i)(role|is_admin|admin|permission|group).*=.*(r\.(URL|Form|Body)|req\.)`),
			},
			{
				ID:          "OWASP-A01-RBAC",
				Type:        "Broken Access Control / RBAC",
				Description: "Potential RBAC failure: User role or permissions assigned from untrusted input.",
				Severity:    "Critical",
				Pattern:     regexp.MustCompile(`(?i)(role|permission|is_admin|group).*=.*(req\.body|req\.query|r\.Form|params|r\.URL\.Query|r\.FormValue)`),
			},
			{
				ID:          "OWASP-A07-AUTH",
				Type:        "Identification and Authentication Failures",
				Description: "Potential insecure authentication: Credentials handled in cleartext or session tokens from input.",
				Severity:    "Critical",
				Pattern:     regexp.MustCompile(`(?i)(password|token|session_id|auth_key).*=.*(req\.body|req\.query|r\.FormValue)`),
			},
			{
				ID:          "OWASP-A03-COMMAND",
				Type:        "Injection / Command Injection",
				Description: "Potential command injection: User-controlled data might touch os/exec sinks.",
				Severity:    "Critical",
				Pattern:     regexp.MustCompile(`(?i)(exec|os)\..*(Command|Start|Run|Output|CombinedOutput)`),
			},
			{
				ID:          "OWASP-A10-SSRF",
				Type:        "Server-Side Request Forgery (SSRF)",
				Description: "Potential SSRF: User-controlled data might touch network sinks.",
				Severity:    "Critical",
				Pattern:     regexp.MustCompile(`(?i)(http|client|net)\.(Get|Post|Head|Do|NewRequest|Dial)`),
			},
			{
				ID:          "OWASP-A01-TRAVERSAL",
				Type:        "Broken Access Control / Path Traversal",
				Description: "Potential path traversal: User-controlled data might touch file system sinks.",
				Severity:    "High",
				Pattern:     regexp.MustCompile(`(?i)(os|ioutil|os\.File|filepath)\.(Open|OpenFile|ReadFile|WriteFile|Remove|Create|Stat|Join|Abs)`),
			},
			{
				ID:          "OWASP-A03-TEMPLATE",
				Type:        "Injection / Template Injection",
				Description: "Potential server-side template injection: User-controlled data might touch template parsing sinks.",
				Severity:    "High",
				Pattern:     regexp.MustCompile(`(?i)template\..*(Parse|ParseFiles|ParseGlob|Execute)`),
			},
			{
				ID:          "OWASP-A08-DESERIALIZATION",
				Type:        "Insecure Deserialization",
				Description: "Potential insecure deserialization: Untrusted data unmarshaled.",
				Severity:    "High",
				Pattern:     regexp.MustCompile(`(?i)(json|xml|gob|yaml)\..*(Unmarshal|Decode)`),
			},
			{
				ID:          "FILE-UPLOAD",
				Type:        "Insecure File Upload",
				Description: "Potential insecure file upload: Verify extension and content-type validation.",
				Severity:    "High",
				Pattern:     regexp.MustCompile(`(?i)\b(r|req)\.(FormFile|MultipartForm)`),
			},
			{
				ID:          "OWASP-A03-XSS",
				Type:        "Injection / Cross-Site Scripting (XSS)",
				Description: "Potential XSS: User-controlled data reflected in response.",
				Severity:    "High",
				Pattern:     regexp.MustCompile(`(?i)(w\.Write|fmt\.Fprint|fmt\.Fprintf|fmt\.Sprint|fmt\.Sprintf)`),
			},
			{
				ID:          "OWASP-A02-SEED",
				Type:        "Cryptographic Failure / Weak Seed",
				Description: "Potential weak random seed: Avoid using Unix timestamp without additional entropy.",
				Severity:    "Medium",
				Pattern:     regexp.MustCompile(`(?i)rand\.Seed\(time\.Now\(\)\.Unix\(\)\)`),
			},
			{
				ID:          "OWASP-A06-VULN-COMP",
				Type:        "Vulnerable and Outdated Components",
				Description: "Potential use of vulnerable or outdated components. Verify dependency versions in go.mod.",
				Severity:    "Medium",
				Pattern:     regexp.MustCompile(`(?i)(github\.com/gin-gonic/gin\s+v1\.[0-9]+|github\.com/beego/beego\s+v1\.[0-9]+|golang\.org/x/crypto\s+v0\.[0-3]\.[0-9]+)`),
			},
			{
				ID:          "OWASP-A04-PII",
				Type:        "Sensitive Data Exposure / PII",
				Description: "Potential sensitive data exposure: PII or secrets processed by output/logging sinks.",
				Severity:    "High",
				Pattern:     regexp.MustCompile(`(?i)(w\.Write|fmt\.Fprint|fmt\.Fprintf|log\.|fmt\.Print|json\.Marshal|json\.NewEncoder)`),
			},
			{
				ID:          "OWASP-A05-HEADERS",
				Type:        "Security Misconfiguration / Headers",
				Description: "Potential missing security headers: Verify CSP, HSTS, and Frame options.",
				Severity:    "Medium",
				Pattern:     regexp.MustCompile(`(?i)\b(w\.Header\(\)\.Set|w\.Header\(\)\.Add)\b`),
			},
			{
				ID:          "OWASP-A05-TLS",
				Type:        "Security Misconfiguration / TLS",
				Description: "Potential insecure TLS configuration: Use of weak versions.",
				Severity:    "High",
				Pattern:     regexp.MustCompile(`(?i)\btls\.VersionTLS(10|11)\b`),
			},
			{
				ID:          "OWASP-A05-DEBUG",
				Type:        "Security Misconfiguration / Debug",
				Description: "Potential debug mode enabled: Verify production settings.",
				Severity:    "High",
				Pattern:     regexp.MustCompile(`(?i)(gin\.SetMode\(gin\.DebugMode\)|debug\s*[:=]\s*true|pprof\.)`),
			},
			{
				ID:          "OWASP-A05-CORS",
				Type:        "Security Misconfiguration / CORS",
				Description: "Potential permissive CORS policy: overly broad Access-Control-Allow-Origin.",
				Severity:    "High",
				Pattern:     regexp.MustCompile(`(?i)Access-Control-Allow-Origin.*(\*|http://\*)`),
			},
			{
				ID:          "OWASP-A05-CREDS",
				Type:        "Security Misconfiguration / Credentials",
				Description: "Potential hardcoded credentials or default settings.",
				Severity:    "Critical",
				Pattern:     regexp.MustCompile(`(?i)(password|admin|root|secret|key|token)\s*(?::=|=)\s*["'](admin|password|123456|root|guest)["']`),
			},
			{
				ID:          "OWASP-A05-ERROR",
				Type:        "Security Misconfiguration / Information Disclosure",
				Description: "Potential information disclosure: Error details written to response.",
				Severity:    "Medium",
				Pattern:     regexp.MustCompile(`(?i)(http\.Error|w\.Write|fmt\.Fprintf).*err`),
			},
			{
				ID:          "OWASP-A02-HASH-MD5",
				Type:        "Cryptographic Failure / Weak Hash",
				Description: "Use of MD5: MD5 is cryptographically broken and should not be used for security-sensitive operations.",
				Severity:    "High",
				Pattern:     regexp.MustCompile(`(?i)(crypto/md5|md5\.New|md5\.Sum)`),
			},
			{
				ID:          "OWASP-A02-HASH-SHA1",
				Type:        "Cryptographic Failure / Weak Hash",
				Description: "Use of SHA1: SHA1 is considered weak and should be replaced with SHA256 or better.",
				Severity:    "Medium",
				Pattern:     regexp.MustCompile(`(?i)(crypto/sha1|sha1\.New|sha1\.Sum)`),
			},
			{
				ID:          "OWASP-A02-RANDOM",
				Type:        "Cryptographic Failure / Insecure Randomness",
				Description: "Potential insecure randomness: Verify if math/rand is used for security-sensitive tokens.",
				Severity:    "High",
				Pattern:     regexp.MustCompile(`(?i)(math/rand)`),
			},
			{
				ID:          "OWASP-A07-RESET-FLOW",
				Type:        "Auth Logic / Password Reset",
				Description: "Password reset flow detected. Verify token invalidation and expiration logic.",
				Severity:    "Critical",
				Pattern:     regexp.MustCompile(`(?i)(reset.*password|forgot.*password|recover.*account|password.*recovery|token.*reset)`),
			},
			{
				ID:          "OWASP-A07-SESSION-FIXATION",
				Type:        "Auth Logic / Session Fixation",
				Description: "Login flow detected. Verify session IDs are regenerated upon successful authentication.",
				Severity:    "Critical",
				Pattern:     regexp.MustCompile(`(?i)(login|authenticate|signin).*(session|SetCookie)`),
			},
			{
				ID:          "OWASP-A07-TOKEN-ROTATION",
				Type:        "Auth Logic / Token Rotation",
				Description: "Token refresh flow detected. Verify refresh tokens are rotated and old tokens invalidated.",
				Severity:    "High",
				Pattern:     regexp.MustCompile(`(?i)(refresh.*token|token.*refresh|jwt.*renew|renew.*token)`),
			},
			{
				ID:          "OWASP-A01-TENANT-ISOLATION",
				Type:        "Auth Logic / Tenant Isolation",
				Description: "Multi-tenant query detected. Verify tenant_id/org_id is strictly enforced in all data access.",
				Severity:    "Critical",
				Pattern:     regexp.MustCompile(`(?i)(tenant_id|org_id|organization_id|workspace_id).*=`),
			},
			{
				ID:          "OWASP-A07-OAUTH-MISCONFIG",
				Type:        "Auth Logic / OAuth Misconfiguration",
				Description: "OAuth flow detected. Verify strict 'state' validation and exact redirect_uri matching.",
				Severity:    "High",
				Pattern:     regexp.MustCompile(`(?i)(oauth|authorize|callback|client_id|redirect_uri|state=)`),
			},
			{
				ID:          "OWASP-A04-RATE-LIMITING",
				Type:        "Business Logic / Rate Limiting",
				Description: "Sensitive action detected without obvious rate limiting. Check for logic abuse (e.g., credential stuffing, brute force).",
				Severity:    "High",
				Pattern:     regexp.MustCompile(`(?i)(change.*password|reset.*password|transfer.*funds|checkout|submit.*payment)`),
			},
			{
				ID:          "OWASP-A01-BOLA-IDOR",
				Type:        "Business Logic / BOLA",
				Description: "Resource access detected. Verify ownership logic (IDOR/BOLA) prevents accessing records belonging to others.",
				Severity:    "High",
				Pattern:     regexp.MustCompile(`(?i)(r\.URL\.Query|r\.FormValue|c\.Param|req\.Param).*\(\s*["']id["']\s*\)`),
			},
			{
				ID:          "OWASP-A08-MASS-ASSIGNMENT",
				Type:        "Business Logic / Mass Assignment",
				Description: "Mass assignment detected. Raw structured inputs should not be bound directly to sensitive domain models.",
				Severity:    "High",
				Pattern:     regexp.MustCompile(`(?i)(json\.NewDecoder|c\.Bind|req\.Decode).*\(&[a-zA-Z]+\)`),
			},
			{
				ID:          "OWASP-A03-TOCTOU-RACE",
				Type:        "Business Logic / Race Condition",
				Description: "Read-Modify-Write pattern detected. Verify atomic transactions, mutual exclusion, or 'FOR UPDATE' locks are used to prevent TOCTOU race conditions.",
				Severity:    "Critical",
				Pattern:     regexp.MustCompile(`(?i)(purchase.*item|buy.*item|inventory|balance)`),
			},
			{
				ID:          "OWASP-A06-VULN-DEP",
				Type:        "Supply Chain / Known CVE",
				Description: "Package dependency defined. Verify against known CVE databases for vulnerable components.",
				Severity:    "High",
				Pattern:     regexp.MustCompile(`(?i)(require\s+[a-zA-Z0-9\.\-/]+\s+v[0-9]+|"[a-zA-Z0-9\.\-/]+"\s*:\s*"[\^~]?[0-9]+|(?m)^[a-zA-Z0-9\.\-]+==[0-9]+|[a-zA-Z0-9\.\-/]+\s+v[0-9]+)`),
			},
			{
				ID:          "OWASP-A06-OUTDATED-DEP",
				Type:        "Supply Chain / Outdated Library",
				Description: "Package dependency defined. Verify component is not severely outdated or abandoned.",
				Severity:    "Medium",
				Pattern:     regexp.MustCompile(`(?i)(require\s+[a-zA-Z0-9\.\-/]+\s+v[0-9]+|"[a-zA-Z0-9\.\-/]+"\s*:\s*"[\^~]?[0-9]+|(?m)^[a-zA-Z0-9\.\-]+==[0-9]+|[a-zA-Z0-9\.\-/]+\s+v[0-9]+)`),
			},
			{
				ID:          "OWASP-A02-KEY",
				Type:        "Cryptographic Failure / Hardcoded Key",
				Description: "Potential hardcoded cryptographic key or sensitive buffer.",
				Severity:    "Critical",
				Pattern:     regexp.MustCompile(`(?i)(key|iv|secret|token|salt|nonce)\s*[:=]\s*\[\]byte\s*\{`),
			},
			{
				ID:          "OWASP-A03-SQLI",
				Type:        "Injection / SQL Injection",
				Description: "Potential SQL injection: String concatenation or formatting in query.",
				Severity:    "Critical",
				Pattern:     regexp.MustCompile(`(?i)(SELECT|INSERT|UPDATE|DELETE|FROM).*(?:\+|fmt\.Sprintf|%).*`),
			},
			{
				ID:          "OWASP-A03-SQLI-STRUCT",
				Type:        "Injection / SQL Injection (Structural)",
				Description: "Logic-aware SQL injection: Detects string concatenation inside SQL query calls.",
				Severity:    "Critical",
				TSQuery: `
					(call_expression
						function: (selector_expression field: (field_identifier) @method (#match? @method "^(Exec|Query|QueryRow)$"))
						arguments: (argument_list (binary_expression left: (string) right: (_) @var))
					) @vuln
				`,
			},
			{
				ID:          "XSS",
				Type:        "Cross-Site Scripting",
				Description: "Potential XSS via unescaped output.",
				Severity:    "Medium",
				Pattern:     regexp.MustCompile(`(?i)(innerHTML|document\.write|alert\(|<\s*script\s*>)`),
			},
			{
				ID:          "OWASP-A04-DOS",
				Type:        "Unrestricted Resource Consumption",
				Description: "Potential DoS: Reading entire request body into memory. Use io.LimitReader.",
				Severity:    "High",
				Pattern:     regexp.MustCompile(`(?i)(io|ioutil)\.ReadAll\((r|req)\.Body\)`),
			},
			{
				ID:          "OWASP-A01-OPEN-REDIRECT",
				Type:        "Broken Access Control / Open Redirect",
				Description: "Potential Open Redirect: Using unvalidated input in http.Redirect.",
				Severity:    "Medium",
				Pattern:     regexp.MustCompile(`(?i)http\.Redirect\([^,]+,\s*[^,]+,\s*(req\.URL\.Query|r\.URL\.Query|req\.FormValue|r\.FormValue)`),
			},
			{
				ID:          "OWASP-A02-JWT-HARDCODE",
				Type:        "Cryptographic Failure / Hardcoded JWT Secret",
				Description: "Potential hardcoded JWT secret in Parse or Sign methods.",
				Severity:    "Critical",
				Pattern:     regexp.MustCompile(`(?i)(jwt\.ParseWithClaims|SignedString).*\[\]byte\(".*"\)`),
			},
			{
				ID:          "OWASP-A02-WEAK-CIPHER",
				Type:        "Cryptographic Failure / Weak Cipher",
				Description: "Use of weak or broken cryptographic algorithms (DES, RC4).",
				Severity:    "High",
				Pattern:     regexp.MustCompile(`(?i)(crypto/des|des\.New|crypto/rc4|rc4\.New)`),
			},
			{
				ID:          "OWASP-A09-LOGGING",
				Type:        "Security Logging and Monitoring Failures",
				Description: "Empty error handling block. Errors should be logged or handled properly.",
				Severity:    "Medium",
				TSQuery: `
					(if_statement
						condition: (binary_expression
							operator: "!="
							right: (nil)
						)
						consequence: (block) @empty_block
					) @vuln
				`,
			},
		},
	}
}

func (s *Scanner) Scan(root string) ([]Finding, error) {
	var findings []Finding

	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		if info.IsDir() {
			if strings.HasPrefix(info.Name(), ".") || info.Name() == "node_modules" || info.Name() == "vendor" {
				return filepath.SkipDir
			}
			return nil
		}

		// Only scan text-based files
		ext := strings.ToLower(filepath.Ext(path))
		base := filepath.Base(path)
		if !isSupportedExtension(ext, base) {
			return nil
		}

		fileFindings, err := s.scanFile(path)
		if err != nil {
			return err
		}
		findings = append(findings, fileFindings...)

		return nil
	})

	return findings, err
}

func (s *Scanner) scanFile(path string) ([]Finding, error) {
	ext := strings.ToLower(filepath.Ext(path))
	var findings []Finding

	// Structural Scanning (Tree-Sitter)
	structuralFindings, err := s.scanStructural(path, ext)
	if err == nil {
		findings = append(findings, structuralFindings...)
	}

	// Regex-based Scanning (Fallback/Complement)
	file, err := os.Open(path)
	if err != nil {
		return findings, err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	lineNum := 1
	for scanner.Scan() {
		line := scanner.Text()
		for _, rule := range s.Rules {
			if rule.Pattern != nil && rule.Pattern.MatchString(line) {
				findings = append(findings, Finding{
					ID:          rule.ID,
					File:        path,
					Line:        lineNum,
					VulnType:    rule.Type,
					Severity:    rule.Severity,
					Description: rule.Description,
					Snippet:     strings.TrimSpace(line),
				})
			}
		}
		lineNum++
	}

	// Perform Auth Flow Logic, Business Logic, and Supply Chain Analysis on regex findings
	var authFindings []Finding
	var busLogicFindings []Finding
	var supplyChainFindings []Finding
	for _, f := range findings {
		if strings.HasPrefix(f.VulnType, "Auth Logic") {
			authFindings = append(authFindings, f)
		} else if strings.HasPrefix(f.VulnType, "Business Logic") {
			busLogicFindings = append(busLogicFindings, f)
		} else if strings.HasPrefix(f.VulnType, "Supply Chain") {
			supplyChainFindings = append(supplyChainFindings, f)
		}
	}

	if len(authFindings) > 0 || len(busLogicFindings) > 0 || len(supplyChainFindings) > 0 {
		fileContent, err := os.ReadFile(path)
		if err == nil {
			var lang *sitter.Language
			switch ext {
			case ".go":
				lang = golang.GetLanguage()
			case ".js", ".jsx", ".ts", ".tsx":
				lang = javascript.GetLanguage()
			case ".py":
				lang = python.GetLanguage()
			case ".java":
				lang = java.GetLanguage()
			}

			var tree *sitter.Tree
			if lang != nil {
				parser := sitter.NewParser()
				parser.SetLanguage(lang)
				tree, _ = parser.ParseCtx(context.Background(), nil, fileContent)
			}

			// Even if tree/lang is nil, Analyzers fall back to whole-file checks
			var verifiedAuthFindings []Finding
			if len(authFindings) > 0 {
				authAnalyzer := NewAuthFlowAnalyzer(lang)
				verifiedAuthFindings = authAnalyzer.AnalyzeAuthFlows(context.Background(), fileContent, tree, authFindings)
			}

			var verifiedBusLogicFindings []Finding
			if len(busLogicFindings) > 0 {
				busAnalyzer := NewBusinessLogicAnalyzer(lang)
				verifiedBusLogicFindings = busAnalyzer.AnalyzeBusinessLogic(context.Background(), fileContent, tree, busLogicFindings)
			}

			var verifiedSupplyChainFindings []Finding
			if len(supplyChainFindings) > 0 {
				supplyAnalyzer := NewSupplyChainAnalyzer(lang)
				verifiedSupplyChainFindings = supplyAnalyzer.AnalyzeSupplyChain(context.Background(), fileContent, tree, supplyChainFindings)
			}

			// Rebuild findings list: keep non-analyzed findings + verified findings
			var finalFindings []Finding
			for _, f := range findings {
				if !strings.HasPrefix(f.VulnType, "Auth Logic") && !strings.HasPrefix(f.VulnType, "Business Logic") && !strings.HasPrefix(f.VulnType, "Supply Chain") {
					finalFindings = append(finalFindings, f)
				}
			}
			finalFindings = append(finalFindings, verifiedAuthFindings...)
			finalFindings = append(finalFindings, verifiedBusLogicFindings...)
			finalFindings = append(finalFindings, verifiedSupplyChainFindings...)
			findings = finalFindings
		}
	}

	return findings, scanner.Err()
}

func (s *Scanner) scanStructural(path string, ext string) ([]Finding, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var lang *sitter.Language
	switch ext {
	case ".go":
		lang = golang.GetLanguage()
	case ".js", ".jsx", ".ts", ".tsx":
		lang = javascript.GetLanguage()
	case ".py":
		lang = python.GetLanguage()
	case ".java":
		lang = java.GetLanguage()
	default:
		return nil, nil // No structural scanning for this extension
	}

	parser := sitter.NewParser()
	parser.SetLanguage(lang)
	tree, err := parser.ParseCtx(context.Background(), nil, content)
	if err != nil {
		return nil, err
	}

	var structuralFindings []Finding
	for _, rule := range s.Rules {
		if rule.TSQuery == "" {
			continue
		}

		q, err := sitter.NewQuery([]byte(rule.TSQuery), lang)
		if err != nil {
			continue
		}

		qc := sitter.NewQueryCursor()
		qc.Exec(q, tree.RootNode())

		for {
			m, ok := qc.NextMatch()
			if !ok {
				break
			}

			// Custom logic: if any capture is named "empty_block", ensure it has zero named children.
			emptyMatch := true
			hasVulnCapture := false
			for _, cap := range m.Captures {
				capName := q.CaptureNameForId(cap.Index)
				if capName == "empty_block" && cap.Node.NamedChildCount() > 0 {
					emptyMatch = false
					break
				}
				if capName == "vuln" {
					hasVulnCapture = true
				}
			}
			if !emptyMatch {
				continue
			}

			for _, cap := range m.Captures {
				capName := q.CaptureNameForId(cap.Index)
				// If a @vuln capture is defined, ONLY report on that capture to avoid duplicates.
				if hasVulnCapture && capName != "vuln" {
					continue
				}

				node := cap.Node
				startPoint := node.StartPoint()
				structuralFindings = append(structuralFindings, Finding{
					ID:          rule.ID,
					File:        path,
					Line:        int(startPoint.Row) + 1,
					VulnType:    rule.Type,
					Severity:    rule.Severity,
					Description: rule.Description,
					Snippet:     string(content[node.StartByte():node.EndByte()]),
				})
			}
		}
	}

	return structuralFindings, nil
}

func isSupportedExtension(ext string, base string) bool {
	supported := []string{".go", ".mod", ".sum", ".js", ".ts", ".tsx", ".jsx", ".py", ".php", ".java", ".html", ".css", ".md", ".txt", ".json"}

	isSup := false
	for _, s := range supported {
		if s == ext {
			isSup = true
			break
		}
	}

	if !isSup {
		return false
	}

	// Fast filter for manifests
	if ext == ".json" && base != "package.json" && base != "package-lock.json" {
		return false
	}

	return true
}
