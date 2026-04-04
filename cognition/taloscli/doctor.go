package taloscli

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

type doctorCheck struct {
	Name   string
	Status string
	Detail string
	Action string
}

var (
	doctorTimeout   time.Duration
	doctorNoNetwork bool
)

var doctorCmd = &cobra.Command{
	Use:   "doctor",
	Short: "Run TALOS runtime diagnostics.",
	Long:  "Runs local environment and optional network probes, then prints a professional ASCII health report.",
	RunE: func(cmd *cobra.Command, args []string) error {
		checks := runDoctorChecks(doctorTimeout, doctorNoNetwork)
		writeDoctorReport(cmd.OutOrStdout(), checks)
		return nil
	},
}

func runDoctorChecks(timeout time.Duration, noNetwork bool) []doctorCheck {
	checks := []doctorCheck{
		checkTalosInPath(),
		checkWorkingDirectory(),
		checkDirectoryWritable(".memory", "Create and grant write access to .memory for state/telemetry."),
		checkDirectoryWritable(".skills/permanent", "Create and grant write access to .skills/permanent for skill persistence."),
		checkOllamaHost(),
		checkEnvVar("GLM_TOOLSERVER_BASE_URL", "Set GLM_TOOLSERVER_BASE_URL to enable tool substrate routing."),
		checkEnvVar("URLSCAN_API_KEY", "Set URLSCAN_API_KEY to enable URL safety verdict checks for learn --url."),
	}

	if !noNetwork {
		checks = append(checks, probeURL("Ollama Endpoint", ollamaProbeURL(), timeout,
			"Ensure Ollama is running and reachable at OLLAMA_HOST (or AI_API_Guide default: http://85.31.233.157:11434)."))
		if base := strings.TrimSpace(os.Getenv("GLM_TOOLSERVER_BASE_URL")); base != "" {
			checks = append(checks, probeURL("GLM Toolserver Endpoint", strings.TrimRight(base, "/")+"/healthz", timeout,
				"Ensure toolserver is reachable and health endpoint is available."))
		} else {
			checks = append(checks, doctorCheck{
				Name:   "GLM Toolserver Endpoint",
				Status: "WARN",
				Detail: "GLM_TOOLSERVER_BASE_URL not set; endpoint probe skipped.",
				Action: "Set GLM_TOOLSERVER_BASE_URL to enable connectivity checks.",
			})
		}
		checks = append(checks, probeURLReachability("URLScan Endpoint", urlscanProbeURL(), timeout,
			"Ensure urlscan.io is reachable, or set URLSCAN_API_BASE_URL if using a proxy/base override."))
	}

	return checks
}

func writeDoctorReport(w io.Writer, checks []doctorCheck) {
	pass, warn, fail := 0, 0, 0
	for _, c := range checks {
		switch c.Status {
		case "PASS":
			pass++
		case "WARN":
			warn++
		case "FAIL":
			fail++
		}
	}

	status := "SUCCESS"
	if fail > 0 {
		status = "FAILED"
	} else if warn > 0 {
		status = "PARTIAL"
	}

	_, _ = fmt.Fprintf(w, "TALOS DOCTOR\n\nCOMMAND\n  talos doctor\n\nSTATUS\n  %s\n\nSUMMARY\n  PASS: %d\n  WARN: %d\n  FAIL: %d\n\nCHECKS\n", status, pass, warn, fail)
	for _, c := range checks {
		_, _ = fmt.Fprintf(w, "  [%s] %s\n", c.Status, c.Name)
		if strings.TrimSpace(c.Detail) != "" {
			_, _ = fmt.Fprintf(w, "    Detail: %s\n", c.Detail)
		}
		if strings.TrimSpace(c.Action) != "" {
			_, _ = fmt.Fprintf(w, "    Action: %s\n", c.Action)
		}
	}
	_, _ = io.WriteString(w, "\nNEXT\n")
	if fail > 0 {
		_, _ = io.WriteString(w, "  Address FAIL actions first, then rerun: talos doctor\n")
	} else if warn > 0 {
		_, _ = io.WriteString(w, "  Address WARN actions as needed, then rerun: talos doctor\n")
	} else {
		_, _ = io.WriteString(w, "  Environment looks healthy. Continue with your TALOS workflow.\n")
	}
}

func checkTalosInPath() doctorCheck {
	path, err := exec.LookPath("talos")
	if err != nil {
		return doctorCheck{
			Name:   "CLI Binary Discovery",
			Status: "WARN",
			Detail: "talos not found in PATH.",
			Action: "Run: go run ./cmd/talos monitor install",
		}
	}
	return doctorCheck{
		Name:   "CLI Binary Discovery",
		Status: "PASS",
		Detail: "Resolved talos binary: " + path,
	}
}

func checkWorkingDirectory() doctorCheck {
	wd, err := os.Getwd()
	if err != nil {
		return doctorCheck{
			Name:   "Working Directory",
			Status: "WARN",
			Detail: "Could not determine current working directory.",
			Action: "Run TALOS from within your project repository.",
		}
	}
	if fileExists(filepath.Join(wd, "go.mod")) {
		return doctorCheck{
			Name:   "Working Directory",
			Status: "PASS",
			Detail: "go.mod detected in current directory.",
		}
	}
	return doctorCheck{
		Name:   "Working Directory",
		Status: "WARN",
		Detail: "go.mod not found in current directory.",
		Action: "Run TALOS from the repository root for full local tooling behavior.",
	}
}

func checkFileExists(path string, action string) doctorCheck {
	if fileExists(path) {
		return doctorCheck{
			Name:   "File Presence: " + path,
			Status: "PASS",
			Detail: "Required file found.",
		}
	}
	return doctorCheck{
		Name:   "File Presence: " + path,
		Status: "WARN",
		Detail: "File not found.",
		Action: action,
	}
}

func checkDirectoryWritable(path string, action string) doctorCheck {
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return doctorCheck{
				Name:   "Directory Writable: " + path,
				Status: "WARN",
				Detail: "Directory does not exist.",
				Action: action,
			}
		}
		return doctorCheck{
			Name:   "Directory Writable: " + path,
			Status: "FAIL",
			Detail: "Could not inspect directory: " + err.Error(),
			Action: action,
		}
	}
	if !info.IsDir() {
		return doctorCheck{
			Name:   "Directory Writable: " + path,
			Status: "FAIL",
			Detail: "Path exists but is not a directory.",
			Action: action,
		}
	}
	testPath := filepath.Join(path, ".talos_doctor_tmp")
	if err := os.WriteFile(testPath, []byte("ok"), 0o644); err != nil {
		return doctorCheck{
			Name:   "Directory Writable: " + path,
			Status: "FAIL",
			Detail: "Directory is not writable: " + err.Error(),
			Action: action,
		}
	}
	_ = os.Remove(testPath)
	return doctorCheck{
		Name:   "Directory Writable: " + path,
		Status: "PASS",
		Detail: "Write test succeeded.",
	}
}

func checkEnvVar(name string, action string) doctorCheck {
	val := strings.TrimSpace(os.Getenv(name))
	if val == "" {
		return doctorCheck{
			Name:   "Environment Variable: " + name,
			Status: "WARN",
			Detail: "Not set.",
			Action: action,
		}
	}
	return doctorCheck{
		Name:   "Environment Variable: " + name,
		Status: "PASS",
		Detail: "Set to: " + val,
	}
}

func checkOllamaHost() doctorCheck {
	val := strings.TrimSpace(os.Getenv("OLLAMA_HOST"))
	if val == "" {
		return doctorCheck{
			Name:   "Environment Variable: OLLAMA_HOST",
			Status: "PASS",
			Detail: "Not set. Using AI_API_Guide default: http://85.31.233.157:11434",
		}
	}
	return doctorCheck{
		Name:   "Environment Variable: OLLAMA_HOST",
		Status: "PASS",
		Detail: "Set to: " + val,
	}
}

func probeURL(name, url string, timeout time.Duration, action string) doctorCheck {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return doctorCheck{
			Name:   name,
			Status: "FAIL",
			Detail: "Invalid probe URL: " + err.Error(),
			Action: action,
		}
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return doctorCheck{
			Name:   name,
			Status: "WARN",
			Detail: "Probe failed: " + err.Error(),
			Action: action,
		}
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 200 && resp.StatusCode < 400 {
		return doctorCheck{
			Name:   name,
			Status: "PASS",
			Detail: fmt.Sprintf("Endpoint reachable (%s).", resp.Status),
		}
	}
	return doctorCheck{
		Name:   name,
		Status: "WARN",
		Detail: fmt.Sprintf("Endpoint returned non-success status (%s).", resp.Status),
		Action: action,
	}
}

func probeURLReachability(name, url string, timeout time.Duration, action string) doctorCheck {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return doctorCheck{
			Name:   name,
			Status: "FAIL",
			Detail: "Invalid probe URL: " + err.Error(),
			Action: action,
		}
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return doctorCheck{
			Name:   name,
			Status: "WARN",
			Detail: "Reachability probe failed: " + err.Error(),
			Action: action,
		}
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 500 {
		return doctorCheck{
			Name:   name,
			Status: "WARN",
			Detail: fmt.Sprintf("Endpoint reachable but unhealthy status (%s).", resp.Status),
			Action: action,
		}
	}
	return doctorCheck{
		Name:   name,
		Status: "PASS",
		Detail: fmt.Sprintf("Endpoint reachable (%s).", resp.Status),
	}
}

func ollamaProbeURL() string {
	host := strings.TrimSpace(os.Getenv("OLLAMA_HOST"))
	if host == "" {
		host = "http://85.31.233.157:11434"
	}
	return strings.TrimRight(host, "/") + "/api/tags"
}

func urlscanProbeURL() string {
	base := strings.TrimSpace(os.Getenv("URLSCAN_API_BASE_URL"))
	if base == "" {
		base = "https://urlscan.io"
	}
	return strings.TrimRight(base, "/") + "/"
}

func init() {
	doctorCmd.Flags().DurationVar(&doctorTimeout, "timeout", 2*time.Second, "network probe timeout")
	doctorCmd.Flags().BoolVar(&doctorNoNetwork, "no-network", false, "skip network probes")
	rootCmd.AddCommand(doctorCmd)
}
