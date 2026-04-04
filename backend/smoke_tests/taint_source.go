package smoke_tests

import (
	"net/http"
)

func TaintHandler(w http.ResponseWriter, r *http.Request) {
	userInput := r.FormValue("query")
	// Passing tainted data to a sink in another file
	ExecuteInSink(userInput)
}
