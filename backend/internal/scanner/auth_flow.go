package scanner

import (
	"context"
	"log"
	"strings"

	sitter "github.com/smacker/go-tree-sitter"
)

// AuthFlowAnalyzer coordinates deep logic checks for authentication flaws
type AuthFlowAnalyzer struct {
	Language *sitter.Language
}

func NewAuthFlowAnalyzer(lang *sitter.Language) *AuthFlowAnalyzer {
	return &AuthFlowAnalyzer{Language: lang}
}

// AnalyzeAuthFlows intercepts auth-related findings and attempts to prove/disprove them structurally
func (a *AuthFlowAnalyzer) AnalyzeAuthFlows(ctx context.Context, fileContent []byte, tree *sitter.Tree, findings []Finding) []Finding {
	if a.Language == nil || tree == nil {
		return findings // Skip if structural parsing unsupported
	}

	var verifiedFindings []Finding

	for _, f := range findings {
		// Only run deep logic checks on Auth Flow findings
		if !strings.HasPrefix(f.VulnType, "Auth Logic") {
			verifiedFindings = append(verifiedFindings, f)
			continue
		}

		log.Printf("AuthFlow: Tracing logic for %s in %s at line %d", f.ID, f.File, f.Line)

		isVulnerable := true

		// Extract the specific function/method node containing this finding to prevent cross-contamination
		nodeContent := extractNodeContentAtLine(tree.RootNode(), fileContent, f.Line)
		if nodeContent == nil {
			nodeContent = fileContent // fallback to whole file if node extraction fails
		}

		switch f.ID {
		case "OWASP-A07-RESET-FLOW":
			isVulnerable = a.checkPasswordResetLogic(nodeContent, f)
		case "OWASP-A07-SESSION-FIXATION":
			isVulnerable = a.checkSessionFixationLogic(nodeContent, f)
		case "OWASP-A07-TOKEN-ROTATION":
			isVulnerable = a.checkTokenRotationLogic(nodeContent, f)
		case "OWASP-A01-TENANT-ISOLATION":
			isVulnerable = a.checkTenantIsolationLogic(nodeContent, f)
		case "OWASP-A07-OAUTH-MISCONFIG":
			isVulnerable = a.checkOAuthMisconfigLogic(nodeContent, f)
		}

		// If the logic check confirms it's missing the expected security sequence
		if isVulnerable {
			verifiedFindings = append(verifiedFindings, f)
		} else {
			log.Printf("AuthFlow: Safely handled %s in %s (False Positive mitigated by structural logic)", f.ID, f.File)
		}
	}

	return verifiedFindings
}

func (a *AuthFlowAnalyzer) checkPasswordResetLogic(content []byte, finding Finding) bool {
	// Look for token invalidation queries (e.g. UPDATE ... SET used_at = NOW() or DELETE FROM reset_tokens)
	// If missing, it's vulnerable
	return !strings.Contains(strings.ToLower(string(content)), "used_at") && !strings.Contains(strings.ToLower(string(content)), "delete")
}

func (a *AuthFlowAnalyzer) checkSessionFixationLogic(content []byte, finding Finding) bool {
	// Vulnerable if login does not regenerate session ID
	strContent := strings.ToLower(string(content))
	return !strings.Contains(strContent, "regenerate") && !strings.Contains(strContent, "newsession") && !strings.Contains(strContent, "clear")
}

func (a *AuthFlowAnalyzer) checkTokenRotationLogic(content []byte, finding Finding) bool {
	// Vulnerable if refresh token is reused without rotating family tokens
	strContent := strings.ToLower(string(content))
	return !strings.Contains(strContent, "revoke") && !strings.Contains(strContent, "rotate") && !strings.Contains(strContent, "blacklist") && !strings.Contains(strContent, "invalidate")
}

func (a *AuthFlowAnalyzer) checkTenantIsolationLogic(content []byte, finding Finding) bool {
	// Vulnerable if query builder doesn't enforce tenant_id strictly via middleware or scopes
	strContent := strings.ToLower(string(content))

	// If it's a direct SQL string, ensure tenant_id is in the WHERE clause
	if strings.Contains(strContent, "select ") || strings.Contains(strContent, "update ") || strings.Contains(strContent, "delete ") {
		return !strings.Contains(strContent, "tenant_id") && !strings.Contains(strContent, "org_id")
	}

	return !strings.Contains(strContent, "scopetenant")
}

func (a *AuthFlowAnalyzer) checkOAuthMisconfigLogic(content []byte, finding Finding) bool {
	// Vulnerable if OAuth callback does not strictly validate the 'state' parameter
	strContent := strings.ToLower(string(content))
	return !strings.Contains(strContent, "verify_state") && !strings.Contains(strContent, "state == ") && !strings.Contains(strContent, "checkstate")
}

// extractNodeContentAtLine finds the function or method node that contains the given line number
func extractNodeContentAtLine(node *sitter.Node, content []byte, line int) []byte {
	// Line numbers in Finding are 1-indexed, sitter is 0-indexed
	targetLine := uint32(line - 1)

	// Traverse the AST to find the closest wrapper (e.g. function_declaration or method_declaration)
	var targetNode *sitter.Node

	// Quick depth-first search for the deepest node that contains the line and is a function/method
	var findNode func(n *sitter.Node)
	findNode = func(n *sitter.Node) {
		if n == nil {
			return
		}

		startRow := n.StartPoint().Row
		endRow := n.EndPoint().Row

		if targetLine >= startRow && targetLine <= endRow {
			nodeType := n.Type()
			if nodeType == "function_declaration" || nodeType == "method_declaration" || nodeType == "class_definition" || nodeType == "function_definition" {
				// We prefer the tightest scope, so continue traversing children to see if there's a smaller nested function
				targetNode = n
			}

			for i := 0; i < int(n.ChildCount()); i++ {
				findNode(n.Child(i))
			}
		}
	}

	findNode(node)

	if targetNode != nil {
		start := targetNode.StartByte()
		end := targetNode.EndByte()
		if start < uint32(len(content)) && end <= uint32(len(content)) {
			return content[start:end]
		}
	}

	// Fallback to nil if no specific function wrapping the line is found
	return nil
}
