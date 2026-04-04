package google

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func generateTestRSAKey(t *testing.T) (*rsa.PrivateKey, string) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generating test RSA key: %v", err)
	}
	pkcs8, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatalf("marshaling PKCS8 key: %v", err)
	}
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: pkcs8})
	return key, string(pemBytes)
}

func newTestAuth(t *testing.T, tokenURL string) *GoogleAuth {
	t.Helper()
	_, pemStr := generateTestRSAKey(t)
	// Parse it back using the same path as production
	key, err := parseRSAPrivateKey(pemStr)
	if err != nil {
		t.Fatalf("parseRSAPrivateKey: %v", err)
	}
	return &GoogleAuth{
		email:      "test@example.iam.gserviceaccount.com",
		privateKey: key,
		tokenURI:   tokenURL,
		tokens:     make(map[string]*cachedToken),
		http:       &http.Client{},
	}
}

func TestParseRSAPrivateKeyPKCS8(t *testing.T) {
	_, pemStr := generateTestRSAKey(t)
	key, err := parseRSAPrivateKey(pemStr)
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if key == nil {
		t.Fatal("expected non-nil key")
	}
}

func TestParseRSAPrivateKeyInvalid(t *testing.T) {
	_, err := parseRSAPrivateKey("not a pem block")
	if err == nil {
		t.Fatal("expected error for invalid PEM")
	}
}

func TestGoogleAuthBuildJWT(t *testing.T) {
	auth := newTestAuth(t, "https://oauth2.googleapis.com/token")
	jwt, err := auth.buildJWT([]string{"https://www.googleapis.com/auth/gmail.readonly"})
	if err != nil {
		t.Fatalf("buildJWT: %v", err)
	}
	parts := strings.Split(jwt, ".")
	if len(parts) != 3 {
		t.Fatalf("expected 3 JWT parts, got %d", len(parts))
	}
}

func TestGoogleAuthTokenExchange(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "want POST", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"access_token":"test-token-123","expires_in":3600,"token_type":"Bearer"}`)
	}))
	defer srv.Close()

	auth := newTestAuth(t, srv.URL)
	token, err := auth.Token("https://www.googleapis.com/auth/gmail.readonly")
	if err != nil {
		t.Fatalf("Token: %v", err)
	}
	if token != "test-token-123" {
		t.Errorf("expected test-token-123, got %q", token)
	}
}

func TestGoogleAuthTokenCaching(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"access_token":"cached-token","expires_in":3600,"token_type":"Bearer"}`)
	}))
	defer srv.Close()

	auth := newTestAuth(t, srv.URL)
	scope := "https://www.googleapis.com/auth/drive.readonly"
	if _, err := auth.Token(scope); err != nil {
		t.Fatal(err)
	}
	if _, err := auth.Token(scope); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Errorf("expected 1 token fetch (cached), got %d", calls)
	}
}

func TestGoogleAuthTokenErrorResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{
			"error":             "invalid_grant",
			"error_description": "Invalid JWT",
		})
	}))
	defer srv.Close()

	auth := newTestAuth(t, srv.URL)
	_, err := auth.Token("https://www.googleapis.com/auth/gmail.readonly")
	if err == nil {
		t.Fatal("expected error for bad token response")
	}
}
