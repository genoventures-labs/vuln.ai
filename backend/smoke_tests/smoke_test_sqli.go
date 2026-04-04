package smoke_tests

import (
	"fmt"
	"net/http"
)

// SQLInjectionVulnerable shows a classic concatenated query.
func SQLInjectionVulnerable(w http.ResponseWriter, r *http.Request) {
	userID := r.URL.Query().Get("id")

	// DANGER: SQL Injection. User input concatenated into query string.
	query := "SELECT * FROM users WHERE id = " + userID
	fmt.Fprintf(w, "Executing: %s", query)
}

// SQLInjectionSafe shows a parameterized query using placeholders.
func SQLInjectionSafe(w http.ResponseWriter, r *http.Request) {
	userID := r.URL.Query().Get("id")

	// SAFE: Parameterized query. Placeholders used correctly.
	// This should NOT be flagged as a critical injection by the AI.
	query := "SELECT * FROM users WHERE id = ?"
	fmt.Fprintf(w, "Executing: %s with param %s", query, userID)
}

// SQLInjectionFmt shows a vulnerable query using fmt.Sprintf.
func SQLInjectionFmt(w http.ResponseWriter, r *http.Request) {
	userID := r.URL.Query().Get("id")

	// DANGER: SQL Injection via fmt.Sprintf.
	query := fmt.Sprintf("SELECT * FROM users WHERE id = %s", userID)
	fmt.Fprintf(w, "Executing: %s", query)
}

func smoke_test_sqli() {
	http.HandleFunc("/vuln", SQLInjectionVulnerable)
	http.HandleFunc("/safe", SQLInjectionSafe)
	http.HandleFunc("/fmt", SQLInjectionFmt)

	fmt.Println("SQL Injection precision smoke test listening on :8086")
	http.ListenAndServe(":8086", nil)
}
