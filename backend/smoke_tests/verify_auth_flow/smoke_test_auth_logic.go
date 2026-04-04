package smoke_tests

import (
	"fmt"
	"net/http"
)

// OWASP-A07-RESET-FLOW: Vulnerable (No Invalidation)
func ResetPasswordVulnerable(w http.ResponseWriter, r *http.Request) {
	token := r.FormValue("reset_token")
	newPassword := r.FormValue("new_password")

	// Sets new password but leaves token active in DB for reuse
	query := fmt.Sprintf("UPDATE users SET password = '%s' WHERE reset_token = '%s'", newPassword, token)
	fmt.Println(query)
}

// OWASP-A07-RESET-FLOW: Secure (Token Invalidated)
func ResetPasswordSecure(w http.ResponseWriter, r *http.Request) {
	token := r.FormValue("reset_token")
	newPassword := r.FormValue("new_password")

	query := fmt.Sprintf("UPDATE users SET password = '%s' WHERE reset_token = '%s'", newPassword, token)
	fmt.Println(query)
	// Properly invalidates token
	deleteQuery := fmt.Sprintf("DELETE FROM reset_tokens WHERE token = '%s'", token)
	fmt.Println(deleteQuery)
}

// OWASP-A07-SESSION-FIXATION: Vulnerable
func LoginVulnerable(w http.ResponseWriter, r *http.Request) {
	session := getSession(r)
	// Authenticates user but reuses existing session ID, allowing fixation
	session.Set("user_id", "123")
	session.Save()
}

// OWASP-A07-SESSION-FIXATION: Secure
func LoginSecure(w http.ResponseWriter, r *http.Request) {
	session := getSession(r)
	// Safely issues a brand new session cookie post-login
	session.Regenerate()
	session.Set("user_id", "123")
	session.Save()
}

// OWASP-A07-TOKEN-ROTATION: Vulnerable
func RefreshTokenVulnerable(w http.ResponseWriter, r *http.Request) {
	oldToken := r.FormValue("refresh_token")
	// Issues new JWT but doesn't track/revoke the old refresh token
	newToken := generateJWT()
	fmt.Println(oldToken, newToken)
}

// OWASP-A07-TOKEN-ROTATION: Secure
func RefreshTokenSecure(w http.ResponseWriter, r *http.Request) {
	oldToken := r.FormValue("refresh_token")
	// Safely revokes old token to prevent infinite reuse paths
	revokeTokenFamily(oldToken)
	newToken := generateJWT()
	fmt.Println(oldToken, newToken)
}

// OWASP-A01-TENANT-ISOLATION: Vulnerable
func GetUserDataVulnerable(w http.ResponseWriter, r *http.Request) {
	// Missing tenant ID scope, potential to read across boundaries
	query := "SELECT * FROM users WHERE status = 'active'"
	fmt.Println(query)
}

// OWASP-A01-TENANT-ISOLATION: Secure
func GetUserDataSecure(w http.ResponseWriter, r *http.Request) {
	tenantID := r.Context().Value("tenant_id")
	// Properly scopes query to the specific tenant
	query := fmt.Sprintf("SELECT * FROM users WHERE status = 'active' AND tenant_id = '%v'", tenantID)
	fmt.Println(query)
}

// OWASP-A07-OAUTH-MISCONFIG: Vulnerable
func OAuthCallbackVulnerable(w http.ResponseWriter, r *http.Request) {
	code := r.FormValue("code")
	// Ignores the 'state' parameter entirely, CSRF risk
	exchangeCodeForToken(code)
}

// OWASP-A07-OAUTH-MISCONFIG: Secure
func OAuthCallbackSecure(w http.ResponseWriter, r *http.Request) {
	code := r.FormValue("code")
	state := r.FormValue("state")

	// Strictly validates CSRF state
	if !verify_state_cookie(r, state) {
		http.Error(w, "Invalid state", 400)
		return
	}
	exchangeCodeForToken(code)
}

// Mock definitions
type Session interface {
	Set(k, v string)
	Save()
	Regenerate()
}

func getSession(r *http.Request) Session                 { return nil }
func generateJWT() string                                { return "" }
func revokeTokenFamily(t string)                         {}
func exchangeCodeForToken(c string)                      {}
func verify_state_cookie(r *http.Request, s string) bool { return true }
