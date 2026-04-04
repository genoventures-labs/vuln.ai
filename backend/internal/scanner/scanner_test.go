package scanner

import (
	"os"
	"path/filepath"
	"testing"
)

func TestScanner(t *testing.T) {
	// Create a temporary directory for testing
	tmpDir, err := os.MkdirTemp("", "vuln-test")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	// Create a dummy file with a secret
	secretFile := filepath.Join(tmpDir, "secret.js")
	content := []byte("const password = 'admin';\nconsole.log(password);")
	if err := os.WriteFile(secretFile, content, 0644); err != nil {
		t.Fatal(err)
	}

	// Create a dummy file with SQL injection
	sqlFile := filepath.Join(tmpDir, "db.php")
	sqlContent := []byte("$query = 'SELECT * FROM users WHERE id = ' + $_GET['id'];")
	if err := os.WriteFile(sqlFile, sqlContent, 0644); err != nil {
		t.Fatal(err)
	}

	// Create a dummy file with missing OWASP vulns
	miscVulnFile := filepath.Join(tmpDir, "misc.go")
	miscContent := []byte(`
package main

import (
	"crypto/des"
	"io/ioutil"
	"net/http"
	"github.com/golang-jwt/jwt"
)

func handler(w http.ResponseWriter, r *http.Request) {
	// A04: DoS
	body, _ := ioutil.ReadAll(r.Body)

	// A01: Open Redirect
	http.Redirect(w, r, r.URL.Query().Get("url"), http.StatusMovedPermanently)

	// A02: JWT Hardcode
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{})
	token.SignedString([]byte("supersecret"))

	// A02: Weak Cipher
	c, err := des.NewCipher([]byte("12345678"))
	
	// A09: Logging Failure
	if err != nil {
	}
}
`)
	if err := os.WriteFile(miscVulnFile, miscContent, 0644); err != nil {
		t.Fatal(err)
	}

	s := NewScanner()
	findings, err := s.Scan(tmpDir)
	if err != nil {
		t.Errorf("Scan failed: %v", err)
	}

	// Verify findings
	expectedIDs := map[string]bool{
		"OWASP-A05-CREDS":         false,
		"OWASP-A03-SQLI":          false,
		"OWASP-A04-DOS":           false,
		"OWASP-A01-OPEN-REDIRECT": false,
		"OWASP-A02-JWT-HARDCODE":  false,
		"OWASP-A02-WEAK-CIPHER":   false,
		"OWASP-A09-LOGGING":       false,
	}

	for _, f := range findings {
		if _, ok := expectedIDs[f.ID]; ok {
			expectedIDs[f.ID] = true
		}
		// Also OWASP-A07-AUTH can be secrets
		if f.ID == "OWASP-A07-AUTH" {
			expectedIDs["OWASP-A05-CREDS"] = true
		}
	}

	for id, found := range expectedIDs {
		if !found {
			t.Errorf("Failed to find expected vulnerability: %s", id)
		}
	}
}
