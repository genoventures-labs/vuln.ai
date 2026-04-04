package smoke_tests

import (
	"database/sql"
	"fmt"
	"net/http"
)

func VulnHandler(w http.ResponseWriter, r *http.Request) {
	db, _ := sql.Open("mysql", "user:pass@tcp(127.0.0.1:3306)/db")
	query := fmt.Sprintf("SELECT * FROM users WHERE id = %s", r.URL.Query().Get("id"))
	rows, _ := db.Query(query)
	_ = rows
}
