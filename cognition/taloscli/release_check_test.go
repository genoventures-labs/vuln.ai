package taloscli

import (
	"strings"
	"testing"
	"time"
)

func TestRunReleaseChecksStrictVsNonStrict(t *testing.T) {
	origRunner := releaseCheckTestRunner
	releaseCheckTestRunner = func(timeout time.Duration) error { return nil }
	defer func() { releaseCheckTestRunner = origRunner }()

	strict := runReleaseChecks(0, true, true)
	if strict.Ready {
		t.Fatal("expected strict release-check to fail when WARN checks exist")
	}
	nonstrict := runReleaseChecks(0, true, false)
	if !nonstrict.Ready {
		t.Fatal("expected non-strict release-check to pass when only WARN checks exist")
	}
}

func TestRunReleaseChecksTestFailure(t *testing.T) {
	origRunner := releaseCheckTestRunner
	releaseCheckTestRunner = func(timeout time.Duration) error { return releaseAssertErr("test failure") }
	defer func() { releaseCheckTestRunner = origRunner }()

	report := runReleaseChecks(0, true, false)
	if report.Ready {
		t.Fatal("expected release-check to fail when CLI tests fail")
	}
	found := false
	for _, c := range report.Checks {
		if c.Name == "CLI Package Tests" && strings.EqualFold(c.Status, "FAIL") {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("expected CLI Package Tests FAIL check")
	}
}

func TestRenderReleaseCheckReport(t *testing.T) {
	report := releaseCheckReport{
		Strict: true,
		Ready:  false,
		Pass:   1,
		Warn:   1,
		Fail:   1,
		Checks: []releaseCheck{{Name: "A", Status: "PASS"}},
	}
	out := renderReleaseCheckReport(report)
	required := []string{"TALOS RELEASE CHECK", "COMMAND", "STATUS", "NEXT"}
	for _, token := range required {
		if !strings.Contains(out, token) {
			t.Fatalf("expected token %q in report, got: %s", token, out)
		}
	}
	if !strings.Contains(out, "TALOS RELEASE CHECK") {
		t.Fatalf("expected release report header, got: %s", out)
	}
}

type releaseAssertErr string

func (e releaseAssertErr) Error() string { return string(e) }
