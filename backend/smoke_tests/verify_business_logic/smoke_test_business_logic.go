package smoke_tests

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
)

// OWASP-A04-RATE-LIMITING: Vulnerable
func TransferFundsVulnerable(w http.ResponseWriter, r *http.Request) {
	// Sensitive action without rate limiting or throttling
	from := r.FormValue("from")
	to := r.FormValue("to")
	amount := r.FormValue("amount")
	fmt.Printf("Transferring %s from %s to %s\n", amount, from, to)
}

// OWASP-A04-RATE-LIMITING: Secure
func TransferFundsSecure(w http.ResponseWriter, r *http.Request) {
	// Secure action guarded by rate limiter
	if !checkRateLimit(r.RemoteAddr) {
		http.Error(w, "Too many requests", 429)
		return
	}
	from := r.FormValue("from")
	to := r.FormValue("to")
	amount := r.FormValue("amount")
	fmt.Printf("Transferring %s from %s to %s\n", amount, from, to)
}

// OWASP-A01-BOLA-IDOR: Vulnerable
func GetUserInvoiceVulnerable(w http.ResponseWriter, r *http.Request) {
	invoiceID := r.URL.Query().Get("id")
	// Direct object reference missing ownership verification
	query := fmt.Sprintf("SELECT * FROM invoices WHERE id = '%s'", invoiceID)
	fmt.Println(query)
}

// OWASP-A01-BOLA-IDOR: Secure
func GetUserInvoiceSecure(w http.ResponseWriter, r *http.Request) {
	invoiceID := r.URL.Query().Get("id")
	userID := r.Context().Value("user_id")
	// Safely bounds the object reference strictly to the current owner
	query := fmt.Sprintf("SELECT * FROM invoices WHERE id = '%s' AND owner_id = '%s'", invoiceID, userID)
	fmt.Println(query)
}

// OWASP-A08-MASS-ASSIGNMENT: Vulnerable
func UpdateProfileVulnerable(w http.ResponseWriter, r *http.Request) {
	var user User
	// Binding directly to domain model allows mass assignment (e.g. is_admin=true)
	json.NewDecoder(r.Body).Decode(&user)
	saveUser(user)
}

// OWASP-A08-MASS-ASSIGNMENT: Secure
func UpdateProfileSecure(w http.ResponseWriter, r *http.Request) {
	var dto UpdateProfileDTO
	// Binding to restricted DTO explicitly prevents mass assignment to sensitive fields
	json.NewDecoder(r.Body).Decode(&dto)
	user := mapFromDTO(dto)
	saveUser(user)
}

// OWASP-A03-TOCTOU-RACE: Vulnerable
func PurchaseItemVulnerable(w http.ResponseWriter, r *http.Request) {
	itemID := r.FormValue("item_id")
	qty := getInventory(itemID)

	// Race condition: another thread could buy the last item before this thread updates
	if qty > 0 {
		newQty := qty - 1
		setInventory(itemID, newQty)
	}
}

// OWASP-A03-TOCTOU-RACE: Secure 1 (Mutex)
var inventoryMutex sync.Mutex

func PurchaseItemSecureMutex(w http.ResponseWriter, r *http.Request) {
	itemID := r.FormValue("item_id")

	inventoryMutex.Lock()
	defer inventoryMutex.Unlock()

	qty := getInventory(itemID)
	if qty > 0 {
		newQty := qty - 1
		setInventory(itemID, newQty)
	}
}

// OWASP-A03-TOCTOU-RACE: Secure 2 (DB Lock)
func PurchaseItemSecureDB(w http.ResponseWriter, r *http.Request) {
	itemID := r.FormValue("item_id")

	// Utilizing database-level pessimistic locking via FOR UPDATE
	query := fmt.Sprintf("SELECT qty FROM inventory WHERE item_id = '%s' FOR UPDATE", itemID)
	fmt.Println(query)

	update := fmt.Sprintf("UPDATE inventory SET qty = qty - 1 WHERE item_id = '%s'", itemID)
	fmt.Println(update)
}

// Mocks
type User struct {
	ID      string
	IsAdmin bool
}
type UpdateProfileDTO struct{ Name string }

func checkRateLimit(ip string) bool      { return true }
func saveUser(u User)                    {}
func mapFromDTO(d UpdateProfileDTO) User { return User{} }
func getInventory(id string) int         { return 1 }
func setInventory(id string, q int)      {}
