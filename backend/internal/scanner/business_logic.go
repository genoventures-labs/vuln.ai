package scanner

import (
	"context"
	"log"
	"strings"

	sitter "github.com/smacker/go-tree-sitter"
)

// BusinessLogicAnalyzer coordinates deep contextual checks for abuse flaws
type BusinessLogicAnalyzer struct {
	Language *sitter.Language
}

func NewBusinessLogicAnalyzer(lang *sitter.Language) *BusinessLogicAnalyzer {
	return &BusinessLogicAnalyzer{Language: lang}
}

// AnalyzeBusinessLogic intercepts findings related to rate limiting, race conditions, and IDOR
func (a *BusinessLogicAnalyzer) AnalyzeBusinessLogic(ctx context.Context, fileContent []byte, tree *sitter.Tree, findings []Finding) []Finding {
	if a.Language == nil || tree == nil {
		return findings // Skip if structural parsing unsupported
	}

	var verifiedFindings []Finding

	for _, f := range findings {
		// Only run deep logic checks on Business Logic findings
		if !strings.HasPrefix(f.VulnType, "Business Logic") {
			verifiedFindings = append(verifiedFindings, f)
			continue
		}

		log.Printf("BusinessLogic: Tracing logic for %s in %s at line %d", f.ID, f.File, f.Line)

		isVulnerable := true

		nodeContent := extractNodeContentAtLine(tree.RootNode(), fileContent, f.Line)
		if len(nodeContent) == 0 {
			nodeContent = fileContent // fallback
		}

		switch f.ID {
		case "OWASP-A04-RATE-LIMITING":
			isVulnerable = a.checkRateLimitingLogic(nodeContent, f)
		case "OWASP-A01-BOLA-IDOR":
			isVulnerable = a.checkBOLALogic(nodeContent, f)
		case "OWASP-A08-MASS-ASSIGNMENT":
			isVulnerable = a.checkMassAssignmentLogic(nodeContent, f)
		case "OWASP-A03-TOCTOU-RACE":
			isVulnerable = a.checkRaceConditionLogic(nodeContent, f)
		}

		if isVulnerable {
			verifiedFindings = append(verifiedFindings, f)
		} else {
			log.Printf("BusinessLogic: Safely handled %s in %s (False Positive mitigated by structural logic)", f.ID, f.File)
		}
	}

	return verifiedFindings
}

func (a *BusinessLogicAnalyzer) checkRateLimitingLogic(content []byte, finding Finding) bool {
	// A sensible business action should have throttling, jitter, or explicit rate limiting wrapped around it
	strContent := strings.ToLower(string(content))
	return !strings.Contains(strContent, "ratelimit") && !strings.Contains(strContent, "throttle") && !strings.Contains(strContent, "limiter")
}

func (a *BusinessLogicAnalyzer) checkBOLALogic(content []byte, finding Finding) bool {
	// If accessing a resource by ID, it should check ownership (e.g., checking against user_id)
	strContent := strings.ToLower(string(content))
	return !strings.Contains(strContent, "user_id") && !strings.Contains(strContent, "owner_id") && !strings.Contains(strContent, "checkownership")
}

func (a *BusinessLogicAnalyzer) checkMassAssignmentLogic(content []byte, finding Finding) bool {
	// Directly binding to a struct is risky if it lacks explicit allowlists/denylists (DTOs)
	// Secure versions usually map a specifically crafted DTO/form instead of the domain model directly.
	strContent := strings.ToLower(string(content))
	// Example: In Go, using `binding:"-"` or specific DTO mapping prevents this.
	// As a naive string check on the function, we look for explicit mapping routines.
	return !strings.Contains(strContent, "tomodel") && !strings.Contains(strContent, "mapfrom") && !strings.Contains(strContent, "dto") && !strings.Contains(strContent, "binding:\"-\"")
}

func (a *BusinessLogicAnalyzer) checkRaceConditionLogic(content []byte, finding Finding) bool {
	// Read-Modify-Write needs locks. Expecting Mutex, atomic actions, or DB FOR UPDATE locks.
	strContent := strings.ToLower(string(content))
	return !strings.Contains(strContent, "for update") && !strings.Contains(strContent, "mutex.lock") && !strings.Contains(strContent, "sync.rwmutex") && !strings.Contains(strContent, "atomic.")
}
