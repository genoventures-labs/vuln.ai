package taloscli

import (
	"bytes"
	"strings"
	"testing"
)

func TestWriteDoctorReport(t *testing.T) {
	checks := []doctorCheck{
		{Name: "A", Status: "PASS", Detail: "ok"},
		{Name: "B", Status: "WARN", Detail: "warn", Action: "fix"},
		{Name: "C", Status: "FAIL", Detail: "fail", Action: "repair"},
	}

	var out bytes.Buffer
	writeDoctorReport(&out, checks)
	rendered := out.String()

	required := []string{
		"TALOS DOCTOR",
		"COMMAND",
		"STATUS",
		"SUMMARY",
		"PASS: 1",
		"WARN: 1",
		"FAIL: 1",
		"CHECKS",
		"[PASS] A",
		"[WARN] B",
		"[FAIL] C",
		"NEXT",
	}
	for _, token := range required {
		if !strings.Contains(rendered, token) {
			t.Fatalf("expected report to contain %q", token)
		}
	}
}
