package taloscli

import (
	"strings"
	"testing"

	"github.com/Thynaptic/P-LMv1/pkg/cognition"
)

func TestBuildAdversarialSelfPlayReport(t *testing.T) {
	report := buildAdversarialSelfPlayReport([]cognition.SelfPlayResult{
		{
			WinningVector:   "security_exploitability",
			MaxFlawScore:    0.27,
			Contradictions:  0,
			Cycles:          1,
			OpponentFinding: "missed boundary check",
		},
	})
	if !strings.Contains(report, "security_exploitability") {
		t.Fatalf("expected report to include winning vector, got: %s", report)
	}
	if !strings.Contains(report, "flaw=0.27") {
		t.Fatalf("expected report to include flaw score, got: %s", report)
	}
}

func TestHardenEvidenceWithAdversarialSelfPlayEmpty(t *testing.T) {
	hardened, report := hardenEvidenceWithAdversarialSelfPlay(nil, nil)
	if len(hardened) != 0 {
		t.Fatalf("expected no hardened evidence, got %d", len(hardened))
	}
	if !strings.Contains(report, "No evidence shards") {
		t.Fatalf("unexpected report: %s", report)
	}
}
