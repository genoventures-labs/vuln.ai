package taloscli

import "testing"

func TestCompareSemver(t *testing.T) {
	tests := []struct {
		a, b string
		want int
	}{
		{"v1.2.3", "v1.2.3", 0},
		{"v1.2.4", "v1.2.3", 1},
		{"v1.2.2", "v1.2.3", -1},
		{"1.10.0", "1.9.9", 1},
		{"dev", "v1.0.0", -1},
	}
	for _, tc := range tests {
		got := compareSemver(tc.a, tc.b)
		if got != tc.want {
			t.Fatalf("compareSemver(%q,%q)=%d want %d", tc.a, tc.b, got, tc.want)
		}
	}
}

func TestParseSemver(t *testing.T) {
	got := parseSemver("v2.4.9-beta1")
	want := [3]int{2, 4, 9}
	if got != want {
		t.Fatalf("parseSemver mismatch: got=%v want=%v", got, want)
	}
}

func TestExtractLatestSemverTag(t *testing.T) {
	raw := "abc123\trefs/tags/v0.2.0\nfff999\trefs/tags/v0.10.0\nzzz111\trefs/tags/not-semver\n"
	got, err := extractLatestSemverTag(raw)
	if err != nil {
		t.Fatalf("extractLatestSemverTag error: %v", err)
	}
	if got != "v0.10.0" {
		t.Fatalf("expected v0.10.0, got %s", got)
	}
}
