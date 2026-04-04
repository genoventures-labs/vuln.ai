package smoke_tests

import (
	"fmt"
	"net/http"
)

type User struct {
	ID      string
	Name    string
	Role    string
	IsAdmin bool
}

type Order struct {
	ID      string
	UserID  string
	Product string
}

// Mock database for illustration
var db = struct {
	First  func(interface{}, ...interface{}) error
	Save   func(interface{}) error
	Delete func(interface{}, ...interface{}) error
}{
	First:  func(dst interface{}, args ...interface{}) error { return nil },
	Save:   func(m interface{}) error { return nil },
	Delete: func(m interface{}, args ...interface{}) error { return nil },
}

// GetOrderVulnerable demonstrates IDOR. It fetches an order by ID from input
// without verifying if the order belongs to the requesting user.
func GetOrderVulnerable(w http.ResponseWriter, r *http.Request) {
	orderID := r.URL.Query().Get("id")
	var order Order

	// DANGER: IDOR Vulnerability. Missing ownership check.
	// An attacker can fetch any order by changing the 'id' parameter.
	db.First(&order, "id = ?", orderID)

	fmt.Fprintf(w, "Order: %s, Product: %s", order.ID, order.Product)
}

// DeleteOrderVulnerable demonstrates missing ownership check.
func DeleteOrderVulnerable(w http.ResponseWriter, r *http.Request) {
	orderID := r.URL.Query().Get("id")

	// DANGER: Missing Ownership check.
	// An attacker can delete any order if they know the ID.
	db.Delete(&Order{}, "id = ?", orderID)

	w.Write([]byte("Order deleted"))
}

// UpdateProfileVulnerable demonstrates Vertical Privilege Escalation.
// It allows updating sensitive fields like 'is_admin' from the request body.
func UpdateProfileVulnerable(w http.ResponseWriter, r *http.Request) {
	userID := r.URL.Query().Get("id")
	var user User
	db.First(&user, "id = ?", userID)

	// DANGER: Privilege Escalation.
	// User can promote themselves to admin by passing is_admin=true in the form.
	user.Role = r.FormValue("role")
	user.IsAdmin = r.FormValue("is_admin") == "true"

	db.Save(&user)
	w.Write([]byte("Profile updated"))
}

// HorizontalEscalationVulnerable demonstrates horizontal escalation.
func HorizontalEscalationVulnerable(w http.ResponseWriter, r *http.Request) {
	targetUserID := r.FormValue("id") // User ID of another user
	newEmail := r.FormValue("email")

	var user User
	// DANGER: Missing check if the current user has permission to update targetUserID.
	db.First(&user, "id = ?", targetUserID)

	user.Name = newEmail // Simplified for example
	db.Save(&user)

	w.Write([]byte("User record updated"))
}

func smoke_test_idor_escalation() {
	http.HandleFunc("/order", GetOrderVulnerable)
	http.HandleFunc("/delete-order", DeleteOrderVulnerable)
	http.HandleFunc("/update-profile", UpdateProfileVulnerable)
	http.HandleFunc("/update-user", HorizontalEscalationVulnerable)

	fmt.Println("Advanced RBAC/IDOR smoke test server listening on :8082")
	http.ListenAndServe(":8082", nil)
}
