package smoke_tests

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"os/exec"
	"text/template"
)

// CommandInjectionDemonstration shows how user input touches os/exec.
func CommandInjectionDemonstration(w http.ResponseWriter, r *http.Request) {
	cmdParam := r.URL.Query().Get("cmd")

	// DANGER: Command Injection. User input directly passed to shell.
	cmd := exec.Command("sh", "-c", "echo "+cmdParam)
	output, _ := cmd.CombinedOutput()

	fmt.Fprintf(w, "Output: %s", output)
}

// SSRFDemonstration shows how user input touches http.Get.
func SSRFDemonstration(w http.ResponseWriter, r *http.Request) {
	targetURL := r.URL.Query().Get("url")

	// DANGER: SSRF. User input used as target for backend request.
	resp, _ := http.Get(targetURL)
	body, _ := ioutil.ReadAll(resp.Body)

	fmt.Fprintf(w, "Fetched: %s", string(body))
}

// PathTraversalDemonstration shows how user input touches os.Open.
func PathTraversalDemonstration(w http.ResponseWriter, r *http.Request) {
	filePath := r.URL.Query().Get("file")

	// DANGER: Path Traversal. User input used to open files on disk.
	data, _ := ioutil.ReadFile("/app/data/" + filePath)

	w.Write(data)
}

// SSTIDemonstration shows how user input touches template parsing.
func SSTIDemonstration(w http.ResponseWriter, r *http.Request) {
	userInput := r.FormValue("template")

	// DANGER: Server-Side Template Injection. User input parsed as template.
	tmpl, _ := template.New("test").Parse(userInput)
	tmpl.Execute(w, nil)
}

// InsecureDeserializationDemonstration shows how user input is directly unmarshaled.
func InsecureDeserializationDemonstration(w http.ResponseWriter, r *http.Request) {
	var data interface{}
	body, _ := ioutil.ReadAll(r.Body)

	// DANGER: Insecure Deserialization. Unmarshaling untrusted data into generic interface.
	json.Unmarshal(body, &data)

	fmt.Fprintf(w, "Processed data: %v", data)
}

// FileUploadDemonstration shows how user input touches multipart form handling.
func FileUploadDemonstration(w http.ResponseWriter, r *http.Request) {
	// DANGER: Insecure File Upload. Direct handling of uploaded files without validation.
	file, header, _ := r.FormFile("upload")
	fmt.Fprintf(w, "Uploaded: %s", header.Filename)
	_ = file
}

func smoke_test_data_flow() {
	http.HandleFunc("/exec", CommandInjectionDemonstration)
	http.HandleFunc("/proxy", SSRFDemonstration)
	http.HandleFunc("/read", PathTraversalDemonstration)
	http.HandleFunc("/render", SSTIDemonstration)
	http.HandleFunc("/parse", InsecureDeserializationDemonstration)
	http.HandleFunc("/upload", FileUploadDemonstration)

	fmt.Println("Data Flow smoke test server listening on :8083")
	http.ListenAndServe(":8083", nil)
}
