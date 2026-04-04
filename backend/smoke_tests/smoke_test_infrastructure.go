package smoke_tests

import (
	"crypto/tls"
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
)

// WeakTLSConfig shows an insecure TLS configuration.
func WeakTLSConfig() {
	// DANGER: Insecure TLS. MinVersion set to TLS 1.0.
	_ = &tls.Config{
		MinVersion: tls.VersionTLS10,
	}
}

// DebugModeExposure shows Gin in debug mode.
func DebugModeExposure() {
	// DANGER: Debug Mode. Setting Gin to debug mode in production-likely code.
	gin.SetMode(gin.DebugMode)
}

// PermissiveCORS shows an overly broad CORS policy.
func PermissiveCORS(w http.ResponseWriter, r *http.Request) {
	// DANGER: Permissive CORS. Allowing all origins.
	w.Header().Set("Access-Control-Allow-Origin", "*")
	fmt.Fprint(w, "CORS setup")
}

// MissingSecurityHeaders shows a response missing common protections.
func MissingSecurityHeaders(w http.ResponseWriter, r *http.Request) {
	// DANGER: Missing Headers. Only setting non-security headers.
	w.Header().Set("Content-Type", "text/plain")
	fmt.Fprint(w, "Hello World")
}

// HardcodedCredentials shows use of default/weak creds.
func HardcodedCredentials() {
	// DANGER: Hardcoded Creds. Using 'admin' as a default password.
	password := "admin"
	fmt.Println("Admin password is:", password)
}

func smoke_test_infrastructure() {
	WeakTLSConfig()
	DebugModeExposure()
	HardcodedCredentials()

	http.HandleFunc("/cors", PermissiveCORS)
	http.HandleFunc("/", MissingSecurityHeaders)

	fmt.Println("Infrastructure smoke test server listening on :8085")
	http.ListenAndServe(":8085", nil)
}
