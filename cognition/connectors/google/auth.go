// Package google provides T.A.L.O.S. connectors for Google Workspace APIs.
// All connectors share a single GoogleAuth instance for token management.
package google

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/Thynaptic/P-LMv1/pkg/envload"
)

const googleTokenURL = "https://oauth2.googleapis.com/token"
const jwtGrantType = "urn:ietf:params:oauth:grant-type:jwt-bearer"

// envOnce ensures .env is loaded exactly once per process regardless of call path.
var envOnce sync.Once

func ensureEnv() {
	envOnce.Do(func() { _ = envload.Autoload() })
}

// serviceAccountJSON mirrors the fields of a Google service account key file.
type serviceAccountJSON struct {
	Type         string `json:"type"`
	ClientEmail  string `json:"client_email"`
	PrivateKeyID string `json:"private_key_id"`
	PrivateKey   string `json:"private_key"`
	TokenURI     string `json:"token_uri"`
}

// cachedToken holds a fetched access token and its expiry.
type cachedToken struct {
	value   string
	expires time.Time
}

// GoogleAuth manages service account credentials and token caching for Google APIs.
// Load it once via NewGoogleAuth and share across connectors.
type GoogleAuth struct {
	email      string
	privateKey *rsa.PrivateKey
	tokenURI   string
	impersonate string // GOOGLE_IMPERSONATE_USER — user email for domain-wide delegation

	mu     sync.Mutex
	tokens map[string]*cachedToken // keyed by scope string
	http   *http.Client
}

// NewGoogleAuth loads service account credentials from the file path in
// GOOGLE_SERVICE_ACCOUNT_JSON and reads GOOGLE_IMPERSONATE_USER for delegation.
// Credentials are read from shell exports and/or a .env file in the project root.
func NewGoogleAuth() (*GoogleAuth, error) {
	ensureEnv()
	path := strings.TrimSpace(os.Getenv("GOOGLE_SERVICE_ACCOUNT_JSON"))
	if path == "" {
		return nil, fmt.Errorf("GOOGLE_SERVICE_ACCOUNT_JSON is not set")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading service account file: %w", err)
	}
	var sa serviceAccountJSON
	if err := json.Unmarshal(data, &sa); err != nil {
		return nil, fmt.Errorf("parsing service account JSON: %w", err)
	}
	if sa.Type != "service_account" {
		return nil, fmt.Errorf("expected type=service_account, got %q", sa.Type)
	}
	pk, err := parseRSAPrivateKey(sa.PrivateKey)
	if err != nil {
		return nil, fmt.Errorf("parsing private key: %w", err)
	}
	tokenURI := sa.TokenURI
	if tokenURI == "" {
		tokenURI = googleTokenURL
	}
	return &GoogleAuth{
		email:       sa.ClientEmail,
		privateKey:  pk,
		tokenURI:    tokenURI,
		impersonate: strings.TrimSpace(os.Getenv("GOOGLE_IMPERSONATE_USER")),
		tokens:      make(map[string]*cachedToken),
		http:        &http.Client{Timeout: 15 * time.Second},
	}, nil
}

// Token returns a valid Bearer access token for the given scopes.
// It caches tokens and auto-refreshes when within 60 seconds of expiry.
func (a *GoogleAuth) Token(scopes ...string) (string, error) {
	key := strings.Join(scopes, " ")
	a.mu.Lock()
	defer a.mu.Unlock()
	if t, ok := a.tokens[key]; ok && time.Now().Before(t.expires.Add(-60*time.Second)) {
		return t.value, nil
	}
	tok, exp, err := a.fetchToken(scopes)
	if err != nil {
		return "", err
	}
	a.tokens[key] = &cachedToken{value: tok, expires: exp}
	return tok, nil
}

func (a *GoogleAuth) fetchToken(scopes []string) (string, time.Time, error) {
	jwt, err := a.buildJWT(scopes)
	if err != nil {
		return "", time.Time{}, err
	}
	form := url.Values{
		"grant_type": {jwtGrantType},
		"assertion":  {jwt},
	}
	resp, err := a.http.PostForm(a.tokenURI, form)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("token request: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return "", time.Time{}, fmt.Errorf("token request failed (%d): %s", resp.StatusCode, string(body))
	}
	var result struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
		TokenType   string `json:"token_type"`
		Error       string `json:"error"`
		ErrorDesc   string `json:"error_description"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return "", time.Time{}, fmt.Errorf("parsing token response: %w", err)
	}
	if result.Error != "" {
		return "", time.Time{}, fmt.Errorf("token error %s: %s", result.Error, result.ErrorDesc)
	}
	exp := time.Now().Add(time.Duration(result.ExpiresIn) * time.Second)
	return result.AccessToken, exp, nil
}

func (a *GoogleAuth) buildJWT(scopes []string) (string, error) {
	now := time.Now().Unix()
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"RS256","typ":"JWT"}`))

	claims := map[string]any{
		"iss":   a.email,
		"scope": strings.Join(scopes, " "),
		"aud":   a.tokenURI,
		"iat":   now,
		"exp":   now + 3600,
	}
	if a.impersonate != "" {
		claims["sub"] = a.impersonate
	}
	claimsJSON, err := json.Marshal(claims)
	if err != nil {
		return "", fmt.Errorf("marshaling JWT claims: %w", err)
	}
	claimsB64 := base64.RawURLEncoding.EncodeToString(claimsJSON)
	signingInput := header + "." + claimsB64

	h := sha256.New()
	h.Write([]byte(signingInput))
	sig, err := rsa.SignPKCS1v15(rand.Reader, a.privateKey, crypto.SHA256, h.Sum(nil))
	if err != nil {
		return "", fmt.Errorf("signing JWT: %w", err)
	}
	return signingInput + "." + base64.RawURLEncoding.EncodeToString(sig), nil
}

// doGet performs an authenticated GET request and returns the response body.
func (a *GoogleAuth) doGet(endpoint string, scopes []string) ([]byte, int, error) {
	token, err := a.Token(scopes...)
	if err != nil {
		return nil, 0, err
	}
	req, err := http.NewRequest(http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")

	client := a.http
	resp, err := client.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("GET %s: %w", endpoint, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return body, resp.StatusCode, nil
}

func parseRSAPrivateKey(pemStr string) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode([]byte(pemStr))
	if block == nil {
		return nil, fmt.Errorf("no PEM block found in private key")
	}
	switch block.Type {
	case "RSA PRIVATE KEY":
		return x509.ParsePKCS1PrivateKey(block.Bytes)
	case "PRIVATE KEY":
		key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
		if err != nil {
			return nil, err
		}
		rsaKey, ok := key.(*rsa.PrivateKey)
		if !ok {
			return nil, fmt.Errorf("PKCS8 key is not RSA")
		}
		return rsaKey, nil
	default:
		return nil, fmt.Errorf("unsupported PEM block type: %s", block.Type)
	}
}
