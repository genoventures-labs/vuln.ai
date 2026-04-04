package smoke_tests

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
)

type LeakUser struct {
	ID           string `json:"id"`
	Username     string `json:"username"`
	Email        string `json:"email"`
	PasswordHash string `json:"password_hash"` // DANGER: Sensitive data
}

// XSSDemonstration shows reflection of user input without escaping.
func XSSDemonstration(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("name")

	// DANGER: Reflected XSS. User-controlled 'name' is written directly to response.
	fmt.Fprintf(w, "<h1>Hello, %s</h1>", name)
}

// PIILeakageDemonstration shows exposure of sensitive user fields.
func PIILeakageDemonstration(w http.ResponseWriter, r *http.Request) {
	user := LeakUser{
		ID:           "1",
		Username:     "victim",
		Email:        "victim@example.com",
		PasswordHash: "$2a$12$R9h/cIPz0gi.URQHe83j6OaU/F3/RjXfL/fH6H6H6H6H6H6H6H6H6",
	}

	// DANGER: PII Exposure. PasswordHash and Email are leaked in the JSON response.
	json.Marshal(user)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(user)
}

// ErrorDisclosureDemonstration shows leakage of verbose error messages.
func ErrorDisclosureDemonstration(w http.ResponseWriter, r *http.Request) {
	_, err := http.Get("http://invalid-internal-service:9999")
	if err != nil {
		// DANGER: Information Disclosure. Verbose internal error written to response.
		http.Error(w, fmt.Sprintf("Service connection failed: %v", err), http.StatusInternalServerError)
		return
	}
}

// SecretLoggingDemonstration shows logging of sensitive tokens.
func SecretLoggingDemonstration(w http.ResponseWriter, r *http.Request) {
	apiKey := r.Header.Get("X-API-Key")

	// DANGER: Sensitive Logging. API key is written to server logs.
	log.Printf("Processing request with API Key: %s", apiKey)

	fmt.Fprint(w, "Request received")
}

func smoke_test_output_leakage() {
	http.HandleFunc("/xss", XSSDemonstration)
	http.HandleFunc("/user", PIILeakageDemonstration)
	http.HandleFunc("/error", ErrorDisclosureDemonstration)
	http.HandleFunc("/log", SecretLoggingDemonstration)

	fmt.Println("Output & Leakage smoke test server listening on :8084")
	http.ListenAndServe(":8084", nil)
}
