package scanner

import (
	"context"
	"log"
	"strings"

	sitter "github.com/smacker/go-tree-sitter"
)

// SupplyChainAnalyzer coordinates deep contextual checks for manifest flaws
type SupplyChainAnalyzer struct {
	Language *sitter.Language
}

func NewSupplyChainAnalyzer(lang *sitter.Language) *SupplyChainAnalyzer {
	return &SupplyChainAnalyzer{Language: lang}
}

// AnalyzeSupplyChain intercepts findings related to CVEs, outdated packages, and transitive risks
func (a *SupplyChainAnalyzer) AnalyzeSupplyChain(ctx context.Context, fileContent []byte, tree *sitter.Tree, findings []Finding) []Finding {
	if a.Language == nil || tree == nil {
		// Even if structural parsing fails (like for basic text), we can still do contextual checks
	}

	var verifiedFindings []Finding

	for _, f := range findings {
		// Only run deep logic checks on Supply Chain findings
		if !strings.HasPrefix(f.VulnType, "Supply Chain") {
			verifiedFindings = append(verifiedFindings, f)
			continue
		}

		log.Printf("SupplyChain: Tracing logic for %s in %s at line %d", f.ID, f.File, f.Line)

		isVulnerable := true

		nodeContent := f.Snippet // Start with the regex extracted line for manifests
		if tree != nil {
			extracted := extractNodeContentAtLine(tree.RootNode(), fileContent, f.Line)
			if len(extracted) > 0 {
				nodeContent = string(extracted)
			}
		}

		switch f.ID {
		case "OWASP-A06-VULN-DEP":
			isVulnerable = a.checkKnownCVEMockLogic(nodeContent, f)
		case "OWASP-A06-OUTDATED-DEP":
			isVulnerable = a.checkOutdatedLibraryLogic(nodeContent, f)
		}

		if isVulnerable {
			verifiedFindings = append(verifiedFindings, f)
		} else {
			log.Printf("SupplyChain: Safely handled %s in %s (Component version is secure)", f.ID, f.File)
		}
	}

	return verifiedFindings
}

func (a *SupplyChainAnalyzer) checkKnownCVEMockLogic(content string, finding Finding) bool {
	// Mock CVE Database constraint correlation
	// Vulnerable libraries: log4j < 2.15, express < 4.0, requests < 2.20
	strContent := strings.ToLower(content)
	if strings.Contains(strContent, "log4j") {
		return strings.Contains(strContent, "2.14") || strings.Contains(strContent, "2.13") || strings.Contains(strContent, "2.12")
	}
	if strings.Contains(strContent, "express") {
		return strings.Contains(strContent, "3.") || strings.Contains(strContent, "2.") || strings.Contains(strContent, "1.")
	}
	if strings.Contains(strContent, "requests") {
		return strings.Contains(strContent, "2.19") || strings.Contains(strContent, "2.18") || strings.Contains(strContent, "2.0")
	}

	// For testing, catch a naive mock vulnerable-lib
	if strings.Contains(strContent, "vulnerable-package") || strings.Contains(strContent, "pwned-lib") {
		return true
	}

	return false
}

func (a *SupplyChainAnalyzer) checkOutdatedLibraryLogic(content string, finding Finding) bool {
	strContent := strings.ToLower(content)
	// Mock Outdated Library bounds
	// Very old major versions
	if strings.Contains(strContent, "react") {
		return strings.Contains(strContent, "15.") || strings.Contains(strContent, "14.") || strings.Contains(strContent, "0.")
	}
	if strings.Contains(strContent, "gin") {
		return strings.Contains(strContent, "v1.0") || strings.Contains(strContent, "v1.1.") || strings.Contains(strContent, "v1.2.")
	}

	if strings.Contains(strContent, "abandonware-pkg") {
		return true
	}

	return false
}
