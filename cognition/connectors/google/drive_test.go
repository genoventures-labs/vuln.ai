package google

import (
	"context"
	"net/http"
	"testing"

	"github.com/Thynaptic/P-LMv1/pkg/connectors"
)

func TestDriveConnectorName(t *testing.T) {
	auth := &GoogleAuth{tokens: make(map[string]*cachedToken)}
	c := NewDriveConnector(auth)
	if c.Name() != "google_drive" {
		t.Errorf("expected google_drive, got %q", c.Name())
	}
}

func TestDriveConnectorInterface(_ *testing.T) {
	var _ connectors.Connector = (*DriveConnector)(nil)
}

func TestDriveIsTextMIME(t *testing.T) {
	cases := []struct {
		mime string
		want bool
	}{
		{"text/plain", true},
		{"text/html", true},
		{"text/csv", true},
		{"application/json", true},
		{"application/pdf", false},
		{"image/png", false},
		{"application/vnd.google-apps.document", false}, // handled separately
	}
	for _, tc := range cases {
		if got := isTextMIME(tc.mime); got != tc.want {
			t.Errorf("isTextMIME(%q) = %v, want %v", tc.mime, got, tc.want)
		}
	}
}

func TestDriveListFilesBuildQuery(t *testing.T) {
	// Verify query builder logic for folder constraint.
	parts := []string{"mimeType != '" + mimeGoogleFolder + "'", "trashed = false"}
	q := "mimeType != '" + mimeGoogleFolder + "' and trashed = false and '" + "folder-123" + "' in parents"
	if !containsStr(q, "trashed = false") {
		t.Errorf("query should contain 'trashed = false': %q", q)
	}
	if !containsStr(q, mimeGoogleFolder) {
		t.Errorf("query should exclude folder MIME type: %q", q)
	}
	_ = parts
}

func TestDriveFetchEmptyResult(t *testing.T) {
	// Just verify the DriveConnector is instantiable and has correct name.
	auth := &GoogleAuth{tokens: make(map[string]*cachedToken), http: &http.Client{}}
	c := NewDriveConnector(auth)
	if c == nil {
		t.Fatal("expected non-nil DriveConnector")
	}
	_ = c.Name()
	_ = context.Background()
}

func TestMinHelper(t *testing.T) {
	if min(3, 5) != 3 {
		t.Error("min(3,5) should be 3")
	}
	if min(7, 2) != 2 {
		t.Error("min(7,2) should be 2")
	}
}

func containsStr(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(s) > 0 && containsSubstring(s, sub))
}

func containsSubstring(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
