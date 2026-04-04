package smoke_tests

import (
	"fmt"
	"net/http"
)

// VulnerableRBAC simulates a role assignment from untrusted input
func VulnerableRBAC(w http.ResponseWriter, r *http.Request) {
	// DANGER: User role is assigned directly from query parameters
	userRole := r.URL.Query().Get("role")
	currentUser := r.URL.Query().Get("user")

	fmt.Printf("Updating user %s to role: %s\n", currentUser, userRole)
	w.Write([]byte("Role updated successfully"))
}

// InsecureAuth simulates handling passwords from untrusted input in an insecure way
func InsecureAuth(w http.ResponseWriter, r *http.Request) {
	// DANGER: Password from form is handled directly
	password := r.FormValue("password")
	username := r.FormValue("username")

	if username == "admin" && password == "admin123" {
		w.Write([]byte("Login successful"))
	} else {
		w.Write([]byte("Login failed"))
	}
}

func smoke_test_auth_rbac() {
	http.HandleFunc("/update-role", VulnerableRBAC)
	http.HandleFunc("/login", InsecureAuth)
	fmt.Println("Auth/RBAC smoke test server listening on :8081")
	http.ListenAndServe(":8081", nil)
}
