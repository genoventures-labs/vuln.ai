package scanner

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/user/azimuthal-belt/backend/internal/ai"
)

// ReconNode is the first sub-agent that classifies a target and suggests flaws.
// Expects: "target_json" in args. Returns: JSON ReconAnalysis.
func ReconNode(aiClient *ai.AIClient) func(context.Context, map[string]interface{}) (string, error) {
	return func(ctx context.Context, args map[string]interface{}) (string, error) {
		targetJSON, ok := args["target_json"].(string)
		if !ok || targetJSON == "" {
			return "", fmt.Errorf("target_json argument missing or invalid")
		}

		fmt.Println("[Sub-Agent: Recon] Analyzing target structure...")
		analysis, err := aiClient.GenerateReconAnalysis(targetJSON)
		if err != nil {
			return "", err
		}

		fmt.Printf("[Sub-Agent: Recon] Classification: %s. Identified Flaws: %v\n", analysis.TargetType, analysis.LikelyFlaws)

		// Return as string so the next node can use it in prompt
		bytes, _ := json.Marshal(analysis)
		return string(bytes), nil
	}
}

// PayloadNode is the second sub-agent that generates the strike.
// Expects: "target_json", "recon_context", and optionally "agent_feedback" in args. Returns: StrikePayload.
func PayloadNode(aiClient *ai.AIClient) func(context.Context, map[string]interface{}) (string, error) {
	return func(ctx context.Context, args map[string]interface{}) (string, error) {
		targetJSON, ok := args["target_json"].(string)
		if !ok || targetJSON == "" {
			return "", fmt.Errorf("target_json argument missing or invalid")
		}

		reconContext, _ := args["recon_context"].(string)
		var feedback []string
		if fb, ok := args["agent_feedback"].([]string); ok {
			feedback = fb
		}

		var syllabus []string
		if syl, ok := args["curriculum_syllabus"].([]string); ok {
			syllabus = syl
		}

		// Phase 4: Intelligence Lookup
		intelligence := ""
		if reconContext != "" {
			var analysis ai.ReconAnalysis
			if err := json.Unmarshal([]byte(reconContext), &analysis); err == nil {
				for _, flaw := range analysis.LikelyFlaws {
					if strings.Contains(strings.ToUpper(flaw), "CWE-") {
						// Extract CWE ID (e.g., "CWE-89" -> "89")
						parts := strings.Split(strings.ToUpper(flaw), "CWE-")
						if len(parts) > 1 {
							cweID := strings.TrimSpace(strings.Split(parts[1], " ")[0])
							cweID = strings.TrimRight(cweID, ").,") // Clean up trailing punctuation
							fmt.Printf("[Sub-Agent: PayloadSpecialist] Fetching technical intelligence for CWE-%s...\n", cweID)
							if def, err := aiClient.FetchCWEPattern(cweID); err == nil {
								intelligence += fmt.Sprintf("\n--- CWE-%s Definition ---\n%s\nMitigations: %v\n", cweID, def.Description, def.Mitigations)
							}
						}
					}
				}
			}
		}

		fmt.Println("[Sub-Agent: PayloadSpecialist] Crafting weaponized request...")
		payload, err := aiClient.GenerateStrikePayload(targetJSON, reconContext, intelligence, feedback, syllabus)
		if err != nil {
			return "", err
		}

		fmt.Printf("[Sub-Agent: PayloadSpecialist] Generated %s payload targeting %s mode\n", payload.Method, "web form")
		bytes, _ := json.Marshal(payload)
		return string(bytes), nil
	}
}

// VerdictNode is the final sub-agent that analyzes the strike output to confirm or deny exploit success.
// Expects: "payload", "strike_output". Returns: JSON ExploitVerdict.
func VerdictNode(aiClient *ai.AIClient) func(context.Context, map[string]interface{}) (string, error) {
	return func(ctx context.Context, args map[string]interface{}) (string, error) {
		payload, ok := args["payload"].(string)
		if !ok || payload == "" {
			return "", fmt.Errorf("payload argument missing or invalid")
		}

		strikeOutput, ok := args["strike_output"].(string)
		if !ok || strikeOutput == "" {
			return "", fmt.Errorf("strike_output argument missing or invalid")
		}

		fmt.Println("[Sub-Agent: Auditor] Analyzing strike response for definitive exploit markers...")
		verdict, err := aiClient.VerifyExploit(payload, strikeOutput)
		if err != nil {
			return "", err
		}

		status := "FAILED"
		if verdict.IsExploited {
			status = "SUCCESS"
		}
		fmt.Printf("[Sub-Agent: Auditor] Verdict: %s (Confidence: %s). Reason: %s\n", status, verdict.Confidence, verdict.Reasoning)

		bytes, _ := json.Marshal(verdict)
		return string(bytes), nil
	}
}

// AuditorNode generates a comprehensive markdown report summarizing the security engagement.
// Expects: "strikes_json", "intelligence_json". Returns: Markdown string.
func AuditorNode(aiClient *ai.AIClient) func(context.Context, map[string]interface{}) (string, error) {
	return func(ctx context.Context, args map[string]interface{}) (string, error) {
		strikesJSON, _ := args["strikes_json"].(string)
		intelligenceJSON, _ := args["intelligence_json"].(string)

		fmt.Println("[Sub-Agent: Auditor] Generating comprehensive engagement report...")

		report, err := aiClient.GenerateAuditReport(strikesJSON, intelligenceJSON)
		if err != nil {
			return "", err
		}

		return report, nil
	}
}

// ExecuteProbe is a toolflow.ToolHandler that fires HTTP requests with dynamic payloads
// to test for vulnerabilities in web forms or API endpoints.
// Expects: "url", "method", "payload" (map or string depending on content type), "content_type" in args.
func ExecuteProbe(ctx context.Context, args map[string]interface{}) (string, error) {
	targetURL, ok := args["url"].(string)
	if !ok || targetURL == "" {
		return "", fmt.Errorf("url argument missing or invalid")
	}

	method, ok := args["method"].(string)
	if !ok || method == "" {
		return "", fmt.Errorf("method argument missing or invalid")
	}

	contentType, _ := args["content_type"].(string)
	if contentType == "" {
		contentType = "application/x-www-form-urlencoded" // Default for typical web forms
	}

	var reqBody io.Reader

	// Handle payload based on type
	if payload, ok := args["payload"]; ok && payload != nil {
		if method == "GET" || method == "HEAD" {
			// For GET, attach payload to URL query string
			u, err := url.Parse(targetURL)
			if err != nil {
				return "", fmt.Errorf("invalid url: %v", err)
			}
			q := u.Query()

			// If payload is a map, iterate and add to query
			if payloadMap, isMap := payload.(map[string]interface{}); isMap {
				for k, v := range payloadMap {
					q.Set(k, fmt.Sprintf("%v", v))
				}
			} else if payloadStr, isStr := payload.(string); isStr {
				// Naive append for raw strings
				if u.RawQuery != "" {
					u.RawQuery += "&" + payloadStr
				} else {
					u.RawQuery = payloadStr
				}
			}
			u.RawQuery = q.Encode() + u.RawQuery // Note: this might double encode if we append raw string. Good enough for simple proving.

			// Rebuild safe string
			baseQuery := q.Encode()
			if payloadStr, isStr := payload.(string); isStr {
				if baseQuery != "" {
					baseQuery += "&" + payloadStr
				} else {
					baseQuery = payloadStr
				}
			}
			u.RawQuery = baseQuery

			targetURL = u.String()
		} else {
			// For POST, PUT, etc. body
			if payloadStr, isStr := payload.(string); isStr {
				reqBody = strings.NewReader(payloadStr)
			} else if payloadMap, isMap := payload.(map[string]interface{}); isMap {
				// Encode map as form string
				formValues := url.Values{}
				for k, v := range payloadMap {
					formValues.Set(k, fmt.Sprintf("%v", v))
				}
				reqBody = strings.NewReader(formValues.Encode())
			} else {
				return "", fmt.Errorf("payload must be a string or map")
			}
		}
	}

	client := &http.Client{
		Timeout: 10 * time.Second,
	}

	req, err := http.NewRequestWithContext(ctx, method, targetURL, reqBody)
	if err != nil {
		return "", fmt.Errorf("failed to create request: %v", err)
	}

	if contentType != "" { // Set Content-Type if provided, regardless of reqBody presence
		req.Header.Set("Content-Type", contentType)
	}

	// Inject custom headers if provided
	if headersRaw, ok := args["headers"]; ok && headersRaw != nil {
		if headersMap, isMap := headersRaw.(map[string]interface{}); isMap {
			for k, v := range headersMap {
				req.Header.Set(k, fmt.Sprintf("%v", v))
			}
		}
	}

	// Inject custom cookies if provided
	if cookiesRaw, ok := args["cookies"]; ok && cookiesRaw != nil {
		if cookiesMap, isMap := cookiesRaw.(map[string]interface{}); isMap {
			for k, v := range cookiesMap {
				req.AddCookie(&http.Cookie{Name: k, Value: fmt.Sprintf("%v", v)})
			}
		}
	}

	// Default User-Agent if none provided (Stealth)
	if req.Header.Get("User-Agent") == "" {
		req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36")
	}

	// Stealth Jitter (500ms to 2000ms delay) to evade simple velocity rate limits
	jitter := time.Duration(rand.Intn(1500)+500) * time.Millisecond
	fmt.Printf("[Sub-Agent: Executor] Sleeping for %v (stealth jitter) before strike...\n", jitter)
	time.Sleep(jitter)

	resp, err := client.Do(req)
	if err != nil {
		// Return error so toolflow handles it as execution failure,
		// or return string so AI knows it crashed (e.g., DoS or WAF drop)
		return fmt.Sprintf("Request failed (Possible WAF drop or crash): %v", err), nil
	}
	defer resp.Body.Close()

	bodyBytes, _ := io.ReadAll(resp.Body)
	bodyStr := string(bodyBytes)

	// Truncate body if it's too huge so we don't blow up the AI context length
	if len(bodyStr) > 5000 {
		bodyStr = bodyStr[:5000] + "... [TRUNCATED]"
	}

	result := fmt.Sprintf("Status: %d\nHeaders: %v\nBody:\n%s", resp.StatusCode, resp.Header, bodyStr)
	return result, nil
}
