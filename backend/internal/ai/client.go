package ai

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/user/azimuthal-belt/backend/internal/learner"
	"github.com/user/azimuthal-belt/cognition/router"
)

type AIClient struct {
	BaseURL  string
	APIKey   string // OpenWebUI/Ollama key
	ToolKey  string // GLM Tool Server key
	ClientID string
	Router   *router.Router
}

type AIAnalysisResult struct {
	TechnicalAnalysis string  `json:"technical_analysis"`
	RecommendedFix    string  `json:"recommended_fix"`
	Confidence        float64 `json:"confidence"`
	ConfidenceSource  string  `json:"confidence_source"`
	RiskLevel         string  `json:"risk_level"`
	ReasoningTraceID  string  `json:"reasoning_trace_id"`
}

type ChatRequest struct {
	Model    string    `json:"model"`
	Messages []Message `json:"messages"`
	Stream   bool      `json:"stream"`
}

type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type ChatResponse struct {
	Choices []struct {
		Message Message `json:"message"`
	} `json:"choices"`
}

type EmbeddingRequest struct {
	Model string `json:"model"`
	Input string `json:"input"`
}

type EmbeddingResponse struct {
	Data []struct {
		Embedding []float32 `json:"embedding"`
	} `json:"data"`
}

type CogSearchRequest struct {
	Query     string   `json:"query"`
	AllowMesh bool     `json:"allow_mesh"`
	TopK      int      `json:"top_k"`
	Types     []string `json:"types,omitempty"`
}

type CogSearchResponse struct {
	Matches []Match `json:"matches"`
}

type Match struct {
	ID       string                 `json:"id"`
	Text     string                 `json:"text"`
	Score    float64                `json:"score"`
	Source   string                 `json:"source"`
	Metadata map[string]interface{} `json:"metadata"`
}

type ToolSearchRequest struct {
	Query    string `json:"query"`
	AllowJIT bool   `json:"allow_jit"`
}

type ToolSearchResponse struct {
	Matches []Match `json:"matches"`
	Source  string  `json:"source"`
}

type FetchURLRequest struct {
	URL      string `json:"url"`
	MaxChars int    `json:"max_chars"`
}

type FetchURLOutput struct {
	ContentText string `json:"content_text"`
}

type WebSearchRequest struct {
	Query string `json:"query"`
}

type WebSearchOutput struct {
	Results []struct {
		URL     string `json:"url"`
		Snippet string `json:"snippet"`
	} `json:"results"`
}

func NewAIClient(baseURL string, apiKey string, toolKey string, clientID string, cogRouter *router.Router) *AIClient {
	return &AIClient{
		BaseURL:  baseURL,
		APIKey:   apiKey,
		ToolKey:  toolKey,
		ClientID: clientID,
		Router:   cogRouter,
	}
}

func (c *AIClient) ConsensusVerify(file, vulnType, snippet, explanation, context string, agentFeedback []string) (bool, string, error) {
	proponentPrompt := fmt.Sprintf(`You are the "Security Proponent". Your goal is to prove that the following vulnerability is REAL and EXPLOITABLE.
File: %s
Type: %s
Snippet: %s
Initial Explanation: %s
Context: %s

Provide a short technical justification for why this is a high-risk true positive.`, file, vulnType, snippet, explanation, context)

	skepticPrompt := fmt.Sprintf(`You are the "Security Skeptic". Your goal is to prove that the following vulnerability is a FALSE POSITIVE or is already handled by safety guards.
File: %s
Type: %s
Snippet: %s
Initial Explanation: %s
Context: %s

Provide a short technical justification for why this might be a false positive.`, file, vulnType, snippet, explanation, context)

	propoResp, err := c.ExplainCode(proponentPrompt, "", agentFeedback)
	if err != nil {
		return false, "", err
	}
	skepticResp, err := c.ExplainCode(skepticPrompt, "", agentFeedback)
	if err != nil {
		return false, "", err
	}

	finalPrompt := fmt.Sprintf(`You are the "Consensus Judge". Review the following two arguments and decide if the vulnerability is a VERIFIED TRUE POSITIVE.
--- Proponent Argument ---
%s
--- Skeptic Argument ---
%s

If you are >80%% sure it is a true positive, start your response with "VERIFIED: TRUE". Otherwise, start with "VERIFIED: FALSE".
Follow with a 1-sentence summary of the final verdict.`, propoResp, skepticResp)

	log.Printf("AI: Running Consensus Judge for %s", file)
	judgeResp, err := c.ExplainCode(finalPrompt, "", agentFeedback)
	if err != nil {
		log.Printf("AI: Consensus Judge failed for %s: %v", file, err)
		return false, "", err
	}
	log.Printf("AI: Consensus Judge raw response for %s: %s", file, judgeResp)

	// Improved consensus parsing with case-insensitive check and trimming
	verified := strings.Contains(strings.ToUpper(strings.TrimSpace(judgeResp)), "VERIFIED: TRUE")
	log.Printf("AI: Consensus Judge for %s - Verified: %v", file, verified)
	return verified, judgeResp, nil
}

func (c *AIClient) CreateEmbedding(input string) ([]float32, error) {
	if c.BaseURL == "" {
		return nil, fmt.Errorf("AI base URL not configured")
	}

	reqBody := EmbeddingRequest{
		Model: "nomic-embed-text:latest",
		Input: input,
	}

	body, _ := json.Marshal(reqBody)

	url := fmt.Sprintf("%s/v1/embeddings", c.BaseURL)
	if !bytes.Contains([]byte(c.BaseURL), []byte(":11434")) && !bytes.Contains([]byte(c.BaseURL), []byte("v1")) {
		url = fmt.Sprintf("%s/api/v1/embeddings", c.BaseURL)
	}

	req, err := http.NewRequest("POST", url, bytes.NewBuffer(body))
	if err != nil {
		return nil, err
	}

	req.Header.Set("Content-Type", "application/json")
	if c.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.APIKey)
	}
	if c.ClientID != "" {
		req.Header.Set("X-Client-Id", c.ClientID)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("AI embedding failed with status: %d", resp.StatusCode)
	}

	var embResp EmbeddingResponse
	if err := json.NewDecoder(resp.Body).Decode(&embResp); err != nil {
		return nil, err
	}

	if len(embResp.Data) == 0 {
		return nil, fmt.Errorf("AI returned no embeddings")
	}

	return embResp.Data[0].Embedding, nil
}

func (c *AIClient) FetchCWEPattern(id string) (*learner.CWEDefinition, error) {
	log.Printf("AI: Fetching CWE Pattern: %s", id)
	prompt := fmt.Sprintf("Provide a structured technical definition for CWE-%s, including common mitigations and structural flaws.", id)
	resp, err := c.ExplainCode(prompt, "", nil)
	if err != nil {
		return nil, err
	}

	var def learner.CWEDefinition
	start := strings.Index(resp, "{")
	end := strings.LastIndex(resp, "}")
	if start != -1 && end != -1 && end > start {
		jsonStr := resp[start : end+1]
		if err := json.Unmarshal([]byte(jsonStr), &def); err == nil {
			return &def, nil
		}
	}

	return &learner.CWEDefinition{
		ID:          "CWE-" + id,
		Name:        "CWE Definition (" + id + ")",
		Description: resp,
		Source:      "AI Intelligence",
	}, nil
}

func (c *AIClient) FetchH1Report(id string) (*learner.VulnerabilityReport, error) {
	log.Printf("AI: Fetching H1 Report: %s", id)
	prompt := fmt.Sprintf("Provide a structured technical summary of HackerOne report #%s, focusing on the weaponized PoC and site structure.", id)
	resp, err := c.ExplainCode(prompt, "", nil)
	if err != nil {
		return nil, err
	}

	var report learner.VulnerabilityReport
	start := strings.Index(resp, "{")
	end := strings.LastIndex(resp, "}")
	if start != -1 && end != -1 && end > start {
		jsonStr := resp[start : end+1]
		if err := json.Unmarshal([]byte(jsonStr), &report); err == nil {
			return &report, nil
		}
	}

	return &learner.VulnerabilityReport{
		ID:     "H1-" + id,
		Title:  "HackerOne Report Summary (" + id + ")",
		PoC:    resp,
		Source: "AI Intelligence",
	}, nil
}

func (c *AIClient) RetrieveContext(query string) ([]string, error) {
	// Reverted to return empty context because local module imports broken
	return []string{}, nil
}

func (c *AIClient) ExplainCode(code string, testCode string, agentFeedback []string) (string, error) {
	prompt := fmt.Sprintf(`Briefly explain what this Go code does. 
Focus on security relevance, data flow, and side effects. 
Keep it under 3 sentences. Provide ONLY the explanation.
Code:
%s`, code)

	if testCode != "" {
		prompt = fmt.Sprintf(`Briefly explain what this Go code does, specifically matching its implementation against its expected test behaviors. 
Focus on security relevance, data flow, side effects, and any deviations from expected test behavior. 
Keep it under 4 sentences. Provide ONLY the explanation.
Code:
%s

Test Behaviors:
%s`, code, testCode)
	}

	if len(agentFeedback) > 0 {
		prompt += "\n\nPAST MISTAKES TO AVOID:\n" + strings.Join(agentFeedback, "\n")
	}

	model := "llama3.2:latest"
	if c.Router != nil {
		selected := c.Router.SelectModel(prompt)
		if selected != "" && !strings.Contains(selected, "embed") {
			model = selected
		}
	}

	reqBody := ChatRequest{
		Model: model,
		Messages: []Message{
			{Role: "system", Content: "You are a senior security engineer. Explain code concisely."},
			{Role: "user", Content: prompt},
		},
		Stream: false,
	}

	body, _ := json.Marshal(reqBody)

	url := fmt.Sprintf("%s/v1/chat/completions", c.BaseURL)
	if !strings.Contains(c.BaseURL, ":11434") && !strings.Contains(c.BaseURL, "v1") {
		url = fmt.Sprintf("%s/api/chat", c.BaseURL)
	}

	req, err := http.NewRequest("POST", url, bytes.NewBuffer(body))
	if err != nil {
		return "", err
	}

	req.Header.Set("Content-Type", "application/json")
	if c.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.APIKey)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("AI API returned status: %d, body: %s", resp.StatusCode, string(bodyBytes))
	}

	var chatResp ChatResponse
	if err := json.NewDecoder(resp.Body).Decode(&chatResp); err != nil {
		return "", err
	}

	if len(chatResp.Choices) > 0 {
		return strings.TrimSpace(chatResp.Choices[0].Message.Content), nil
	}
	return "", fmt.Errorf("no explanation generated")
}

func (c *AIClient) RefineExplanation(oldExp, code, feedback string, agentFeedback []string) (string, error) {
	prompt := fmt.Sprintf(`You are a senior security engineer.
The system previously generated this explanation for the code snippet below:
--- Old Explanation ---
%s
--- End Old Explanation ---

However, a human operator provided the following feedback/correction:
"%s"

Code Snippet:
%s

Rewrite the explanation to cleanly incorporate the human feedback. Keep it under 3 sentences and professional. Provide ONLY the new explanation.`, oldExp, feedback, code)

	if len(agentFeedback) > 0 {
		prompt += "\n\nPAST MISTAKES TO AVOID:\n" + strings.Join(agentFeedback, "\n")
	}

	model := "llama3.2:latest"
	if c.Router != nil {
		selected := c.Router.SelectModel(prompt)
		if selected != "" && !strings.Contains(selected, "embed") {
			model = selected
		}
	}

	reqBody := ChatRequest{
		Model: model,
		Messages: []Message{
			{Role: "system", Content: "You are a senior security engineer. Refine the explanation."},
			{Role: "user", Content: prompt},
		},
		Stream: false,
	}

	body, _ := json.Marshal(reqBody)
	url := fmt.Sprintf("%s/v1/chat/completions", c.BaseURL)
	if !strings.Contains(c.BaseURL, ":11434") && !strings.Contains(c.BaseURL, "v1") {
		url = fmt.Sprintf("%s/api/chat", c.BaseURL)
	}

	req, err := http.NewRequest("POST", url, bytes.NewBuffer(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.APIKey)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("AI API returned status: %d, body: %s", resp.StatusCode, string(bodyBytes))
	}

	var chatResp ChatResponse
	if err := json.NewDecoder(resp.Body).Decode(&chatResp); err != nil {
		return "", err
	}
	if len(chatResp.Choices) > 0 {
		return strings.TrimSpace(chatResp.Choices[0].Message.Content), nil
	}
	return "", fmt.Errorf("no refined explanation generated")
}

func (c *AIClient) SummarizeFile(fileName string, functionSummaries []string, agentFeedback []string) (string, error) {
	prompt := fmt.Sprintf(`You are a senior architect analyzing a Go codebase.
Summarize the purpose and responsibilities of the file '%s'.
Here are the summaries of the individual functions contained in this file:

%s

Provide a cohesive paragraph explaining what this file does overall, its role in the system, and any major security boundaries it handles. Keep it under 4 sentences.`, fileName, strings.Join(functionSummaries, "\n- "))

	if len(agentFeedback) > 0 {
		prompt += "\n\nPAST MISTAKES TO AVOID:\n" + strings.Join(agentFeedback, "\n")
	}

	model := "llama3.2:latest"
	if c.Router != nil {
		selected := c.Router.SelectModel(prompt)
		if selected != "" && !strings.Contains(selected, "embed") {
			model = selected
		}
	}

	reqBody := ChatRequest{
		Model: model,
		Messages: []Message{
			{Role: "system", Content: "You are a senior security architect. Summarize file purposes concisely."},
			{Role: "user", Content: prompt},
		},
		Stream: false,
	}

	body, _ := json.Marshal(reqBody)
	url := fmt.Sprintf("%s/v1/chat/completions", c.BaseURL)
	if !strings.Contains(c.BaseURL, ":11434") && !strings.Contains(c.BaseURL, "v1") {
		url = fmt.Sprintf("%s/api/chat", c.BaseURL)
	}

	req, err := http.NewRequest("POST", url, bytes.NewBuffer(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.APIKey)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	var chatResp ChatResponse
	if err := json.NewDecoder(resp.Body).Decode(&chatResp); err != nil {
		return "", err
	}
	if len(chatResp.Choices) > 0 {
		return strings.TrimSpace(chatResp.Choices[0].Message.Content), nil
	}
	return "", fmt.Errorf("no file summary generated")
}

func (c *AIClient) SummarizePackage(packageName string, fileSummaries []string, agentFeedback []string) (string, error) {
	prompt := fmt.Sprintf(`You are a senior architect analyzing a Go codebase.
Summarize the purpose and responsibilities of the package/directory '%s'.
Here are the summaries of the individual files contained in this package:

%s

Provide a cohesive paragraph explaining the architectural role of this entire package. Keep it under 4 sentences.`, packageName, strings.Join(fileSummaries, "\n- "))

	if len(agentFeedback) > 0 {
		prompt += "\n\nPAST MISTAKES TO AVOID:\n" + strings.Join(agentFeedback, "\n")
	}

	model := "llama3.2:latest"
	if c.Router != nil {
		selected := c.Router.SelectModel(prompt)
		if selected != "" && !strings.Contains(selected, "embed") {
			model = selected
		}
	}

	reqBody := ChatRequest{
		Model: model,
		Messages: []Message{
			{Role: "system", Content: "You are a senior security architect. Summarize package architecture concisely."},
			{Role: "user", Content: prompt},
		},
		Stream: false,
	}

	body, _ := json.Marshal(reqBody)
	url := fmt.Sprintf("%s/v1/chat/completions", c.BaseURL)
	if !strings.Contains(c.BaseURL, ":11434") && !strings.Contains(c.BaseURL, "v1") {
		url = fmt.Sprintf("%s/api/chat", c.BaseURL)
	}

	req, err := http.NewRequest("POST", url, bytes.NewBuffer(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.APIKey)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	var chatResp ChatResponse
	if err := json.NewDecoder(resp.Body).Decode(&chatResp); err != nil {
		return "", err
	}
	if len(chatResp.Choices) > 0 {
		return strings.TrimSpace(chatResp.Choices[0].Message.Content), nil
	}
	return "", fmt.Errorf("no package summary generated")
}

func (c *AIClient) FetchURL(url string) (string, error) {
	reqBody := FetchURLRequest{URL: url, MaxChars: 10000}
	body, _ := json.Marshal(reqBody)
	targetURL := "https://chat.thynaptic.com/tools/fetch_url"

	req, err := http.NewRequest("POST", targetURL, bytes.NewBuffer(body))
	if err != nil {
		return "", err
	}

	req.Header.Set("Content-Type", "application/json")
	if c.ToolKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.ToolKey)
	}
	if c.ClientID != "" {
		req.Header.Set("X-Client-Id", c.ClientID)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("Fetch API returned status: %d", resp.StatusCode)
	}

	var fetchResp FetchURLOutput
	if err := json.NewDecoder(resp.Body).Decode(&fetchResp); err != nil {
		return "", err
	}

	return fetchResp.ContentText, nil
}

func (c *AIClient) WebSearch(query string) ([]string, error) {
	reqBody := WebSearchRequest{Query: query}
	body, _ := json.Marshal(reqBody)
	targetURL := "https://chat.thynaptic.com/tools/web_search"

	req, err := http.NewRequest("POST", targetURL, bytes.NewBuffer(body))
	if err != nil {
		return nil, err
	}

	req.Header.Set("Content-Type", "application/json")
	if c.ToolKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.ToolKey)
	}
	if c.ClientID != "" {
		req.Header.Set("X-Client-Id", c.ClientID)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("WebSearch API returned status: %d", resp.StatusCode)
	}

	var searchResp WebSearchOutput
	if err := json.NewDecoder(resp.Body).Decode(&searchResp); err != nil {
		return nil, err
	}

	var urls []string
	for _, res := range searchResp.Results {
		urls = append(urls, res.URL)
	}

	return urls, nil
}

type ReconAnalysis struct {
	TargetType  string   `json:"target_type"`
	LikelyFlaws []string `json:"likely_flaws"`
	Notes       string   `json:"notes"`
}

func (c *AIClient) GenerateReconAnalysis(targetJSON string) (*ReconAnalysis, error) {
	if c.BaseURL == "" {
		return nil, fmt.Errorf("AI base URL not configured")
	}

	prompt := fmt.Sprintf(`You are a reconnaissance sub-agent for an automated penetration testing framework.
Analyze the following JSON, which represents a web target (e.g., a form, an HTML page, or an API documentation/endpoint).
Your goal is to classify the target and identify the most likely vulnerabilities to hunt for.

### Target Classification Rules:
1. If the input contains a "swagger", "openapi", or "api-docs" path, or if 'type' is "api_target", classify as "API Documentation".
2. If it is a login or signup form, classify as "Authentication Form".
3. If it has many inputs, classify as "Data Entry Form".

### Flaw Identification:
- For **API Documentation**: Look for "Excessive Data Exposure (CWE-213)", "Broken Object Level Authorization (CWE-285)", "Improper Assets Management (CWE-1120)", and "Injection (CWE-89)".
- For **Forms**: Look for "SQL Injection (CWE-89)", "Cross-Site Scripting (CWE-79)", "CSRF (CWE-352)", and "Insecure Direct Object References (CWE-639)".
- ALWAYS include the CWE ID in parentheses if known (e.g., "SQL Injection (CWE-89)").

Target JSON:
%s

STRICT OUTPUT FORMAT:
Return ONLY a raw JSON object with no markdown wrapping. The JSON must exactly match this schema:
{
  "target_type": "API Documentation",
  "likely_flaws": ["Broken Object Level Authorization (CWE-285)", "Excessive Data Exposure (CWE-213)"],
  "notes": "Target is a Swagger UI. Likely exposes internal API endpoints without proper auth checks."
}`, targetJSON)

	model := "llama3.2:latest"
	if c.Router != nil {
		selected := c.Router.SelectModel(prompt)
		if selected != "" && !strings.Contains(selected, "embed") {
			model = selected
		}
	}

	sysPrompt := "You are a specialized JSON generator. Return ONLY the raw JSON object matching the requested schema. DO NOT include markdown code blocks, DO NOT include conversational text, DO NOT include any text before or after the JSON. If you include any non-JSON characters, the system will fail."

	reqBody := ChatRequest{
		Model: model,
		Messages: []Message{
			{Role: "system", Content: sysPrompt},
			{Role: "user", Content: prompt},
		},
		Stream: false,
	}

	body, _ := json.Marshal(reqBody)
	url := fmt.Sprintf("%s/v1/chat/completions", c.BaseURL)
	if !strings.Contains(c.BaseURL, ":11434") && !strings.Contains(c.BaseURL, "v1") {
		url = fmt.Sprintf("%s/api/chat", c.BaseURL)
	}

	req, err := http.NewRequest("POST", url, bytes.NewBuffer(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.APIKey)
	}
	if c.ClientID != "" {
		req.Header.Set("X-Client-Id", c.ClientID)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("AI API returned status: %d", resp.StatusCode)
	}

	var chatResp ChatResponse
	if err := json.NewDecoder(resp.Body).Decode(&chatResp); err != nil {
		return nil, err
	}

	if len(chatResp.Choices) > 0 {
		content := chatResp.Choices[0].Message.Content

		// Clean up markdown block if present
		content = strings.TrimSpace(content)
		if strings.HasPrefix(content, "```json") {
			content = strings.TrimPrefix(content, "```json")
			content = strings.TrimSuffix(content, "```")
			content = strings.TrimSpace(content)
		} else if strings.HasPrefix(content, "```") {
			content = strings.TrimPrefix(content, "```")
			content = strings.TrimSuffix(content, "```")
			content = strings.TrimSpace(content)
		}

		first := strings.Index(content, "{")
		last := strings.LastIndex(content, "}")
		if first != -1 && last != -1 && last > first {
			content = content[first : last+1]
		}

		var result ReconAnalysis
		if err := json.Unmarshal([]byte(content), &result); err != nil {
			return nil, fmt.Errorf("failed to parse AI JSON response for ReconAnalysis: %v, raw content: %s", err, content)
		}
		return &result, nil
	}

	return nil, fmt.Errorf("no recon analysis generated")
}

type StrikePayload struct {
	Payload string `json:"payload"`
	Method  string `json:"method"`
}

func (c *AIClient) GenerateStrikePayload(targetJSON string, reconContext string, intelligence string, agentFeedback []string, syllabus []string) (*StrikePayload, error) {
	if c.BaseURL == "" {
		return nil, fmt.Errorf("AI base URL not configured")
	}

	log.Printf("AI: Generating strike payload. ReconContext length: %d, Intelligence length: %d", len(reconContext), len(intelligence))

	prompt := fmt.Sprintf(`The target JSON represents a request body or a data structure that will be sent to a server. 
Your task is to craft a payload that exploit potential vulnerabilities by modifying the values within the JSON structure.

### Context Provided:
**Reconnaissance Analysis:**
%s

**Security Intelligence (CWE/H1):**
%s

### Target JSON:
%s

### Instructions:
1. Use the **Security Intelligence** context to understand the structural flaw (CWE) or reference weaponized patterns from existing reports (H1).
2. Craft a payload that specifically targets the weaknesses identified in the **Reconnaissance Analysis**.
3. Return a complete, valid JSON object that represents the malicious request body.

STRICT OUTPUT FORMAT:
Return ONLY a raw JSON object with no markdown wrapping. The JSON must exactly match this schema:
{
  "payload": "{\"cmd\": \"id\", \"args\": \"; injection\"}", // Escaped JSON string for the body
  "method": "POST"                                         // The HTTP method
}
DO NOT include any text other than the JSON object.`, reconContext, intelligence, targetJSON)

	if len(agentFeedback) > 0 {
		prompt += "\n\nPAST MISTAKES TO AVOID:\n" + strings.Join(agentFeedback, "\n")
	}

	if len(syllabus) > 0 {
		prompt += "\n\nCURRICULUM SYLLABUS (LESSONS TO APPLY):\n" + strings.Join(syllabus, "\n")
	}

	model := "llama3.2:latest"
	if c.Router != nil {
		selected := c.Router.SelectModel(prompt)
		if selected != "" && !strings.Contains(selected, "embed") {
			model = selected
		}
	}

	sysPrompt := "You are a specialized JSON generator. Return ONLY the raw JSON object matching the requested schema. DO NOT include markdown code blocks, DO NOT include conversational text, DO NOT include any text before or after the JSON. If you include any non-JSON characters, the system will fail. The 'payload' field must contain an escaped string representation of the malicious body."

	reqBody := ChatRequest{
		Model: model,
		Messages: []Message{
			{Role: "system", Content: sysPrompt},
			{Role: "user", Content: prompt},
		},
		Stream: false,
	}

	body, _ := json.Marshal(reqBody)
	url := fmt.Sprintf("%s/v1/chat/completions", c.BaseURL)
	if !strings.Contains(c.BaseURL, ":11434") && !strings.Contains(c.BaseURL, "v1") {
		url = fmt.Sprintf("%s/api/chat", c.BaseURL)
	}

	req, err := http.NewRequest("POST", url, bytes.NewBuffer(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.APIKey)
	}
	if c.ClientID != "" {
		req.Header.Set("X-Client-Id", c.ClientID)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("AI API returned status: %d", resp.StatusCode)
	}

	var chatResp ChatResponse
	if err := json.NewDecoder(resp.Body).Decode(&chatResp); err != nil {
		return nil, err
	}

	if len(chatResp.Choices) > 0 {
		content := chatResp.Choices[0].Message.Content

		// Clean up markdown block if present
		content = strings.TrimSpace(content)
		if strings.HasPrefix(content, "```json") {
			content = strings.TrimPrefix(content, "```json")
			content = strings.TrimSuffix(content, "```")
			content = strings.TrimSpace(content)
		} else if strings.HasPrefix(content, "```") {
			content = strings.TrimPrefix(content, "```")
			content = strings.TrimSuffix(content, "```")
			content = strings.TrimSpace(content)
		}

		first := strings.Index(content, "{")
		last := strings.LastIndex(content, "}")
		if first != -1 && last != -1 && last > first {
			content = content[first : last+1]
		}

		var result StrikePayload
		if err := json.Unmarshal([]byte(content), &result); err != nil {
			escapedContent := strings.ReplaceAll(content, "\n", "\\n")
			if err2 := json.Unmarshal([]byte(escapedContent), &result); err2 == nil {
				return &result, nil
			}
			return nil, fmt.Errorf("failed to parse AI JSON response for StrikePayload: %v, raw content: %s", err, content)
		}

		return &result, nil
	}

	return nil, fmt.Errorf("no strike payload generated")
}

type ExploitVerdict struct {
	IsExploited bool   `json:"is_exploited"`
	Confidence  string `json:"confidence"`
	Reasoning   string `json:"reasoning"`
}

func (c *AIClient) VerifyExploit(payload string, httpResponse string) (*ExploitVerdict, error) {
	if c.BaseURL == "" {
		return nil, fmt.Errorf("AI base URL not configured")
	}

	prompt := fmt.Sprintf(`You are a senior security auditor analyzing the results of a penetration testing strike.
Your goal is to definitively determine if the injected payload successfully exploited the target based on the HTTP response.

### Injected Payload:
%s

### Target HTTP Response:
%s

### Instructions:
1. Look for definitive signs of compromise in the response (e.g., leaked database errors, execution of system commands, unexpected sensitive data dumps, or bypassed authentication states).
2. Distinguish between a generic server error (500) and an actual successful exploit. A 500 error alone does not mean an exploit succeeded unless the response reveals sensitive internal state.
3. Determine if the target is 'Exploited' (true/false), state your 'Confidence' (High/Medium/Low), and provide brief 'Reasoning'.

STRICT OUTPUT FORMAT:
Return ONLY a raw JSON object with no markdown wrapping. The JSON must exactly match this schema:
{
  "is_exploited": true,
  "confidence": "High",
  "reasoning": "The response contains a MySQL syntax error, indicating the SQL injection payload successfully altered the query structure."
}
DO NOT include any text other than the JSON object.`, payload, httpResponse)

	model := "llama3.2:latest"
	if c.Router != nil {
		selected := c.Router.SelectModel(prompt)
		if selected != "" && !strings.Contains(selected, "embed") {
			model = selected
		}
	}

	sysPrompt := "You are a specialized JSON generator. Return ONLY the raw JSON object matching the requested schema. DO NOT include markdown code blocks, DO NOT include conversational text, DO NOT include any text before or after the JSON. If you include any non-JSON characters, the system will fail."

	reqBody := ChatRequest{
		Model: model,
		Messages: []Message{
			{Role: "system", Content: sysPrompt},
			{Role: "user", Content: prompt},
		},
		Stream: false,
	}

	body, _ := json.Marshal(reqBody)
	url := fmt.Sprintf("%s/v1/chat/completions", c.BaseURL)
	if !strings.Contains(c.BaseURL, ":11434") && !strings.Contains(c.BaseURL, "v1") {
		url = fmt.Sprintf("%s/api/chat", c.BaseURL)
	}

	req, err := http.NewRequest("POST", url, bytes.NewBuffer(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.APIKey)
	}
	if c.ClientID != "" {
		req.Header.Set("X-Client-Id", c.ClientID)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("AI API returned status: %d", resp.StatusCode)
	}

	var chatResp ChatResponse
	if err := json.NewDecoder(resp.Body).Decode(&chatResp); err != nil {
		return nil, err
	}

	if len(chatResp.Choices) > 0 {
		content := chatResp.Choices[0].Message.Content

		// Clean up markdown block if present
		content = strings.TrimSpace(content)
		if strings.HasPrefix(content, "```json") {
			content = strings.TrimPrefix(content, "```json")
			content = strings.TrimSuffix(content, "```")
			content = strings.TrimSpace(content)
		} else if strings.HasPrefix(content, "```") {
			content = strings.TrimPrefix(content, "```")
			content = strings.TrimSuffix(content, "```")
			content = strings.TrimSpace(content)
		}

		first := strings.Index(content, "{")
		last := strings.LastIndex(content, "}")
		if first != -1 && last != -1 && last > first {
			content = content[first : last+1]
		}

		var result ExploitVerdict
		if err := json.Unmarshal([]byte(content), &result); err != nil {
			return nil, fmt.Errorf("failed to parse AI JSON response for ExploitVerdict: %v, raw content: %s", err, content)
		}

		return &result, nil
	}

	return nil, fmt.Errorf("no expliot verdict generated")
}

func (c *AIClient) MapSwaggerSchema(swaggerJSON string) ([]learner.ProbingTarget, error) {
	if c.BaseURL == "" {
		return nil, fmt.Errorf("AI base URL not configured")
	}

	prompt := fmt.Sprintf(`You are an expert security engineer building an attack surface map. 
Analyze the provided Swagger/OpenAPI JSON and extract all functional endpoints as an array of probing targets.

### Swagger Document:
%s

### Instructions:
1. For every path and method combination (e.g., POST /api/users), create a target object.
2. Determine the full URL using the host/servers list if available, otherwise just use the relative path as the action.
3. Extract any expected query parameters or body fields and list them in the "fields" array.
4. Set the 'type' to "api_target".

STRICT OUTPUT FORMAT:
Return ONLY a raw JSON array of objects. No markdown, no conversational text. The JSON must match this structure exactly:
[
  {
    "action": "/api/users",
    "method": "POST",
    "type": "api_target",
    "fields": [
      {"name": "username", "type": "string"},
      {"name": "email", "type": "string"}
    ]
  }
]`, swaggerJSON)

	model := "llama3.2:latest"
	if c.Router != nil {
		selected := c.Router.SelectModel(prompt)
		if selected != "" && !strings.Contains(selected, "embed") {
			model = selected
		}
	}

	sysPrompt := "You are a specialized JSON generator. Return ONLY a raw JSON array matching the requested schema. DO NOT include markdown code blocks. DO NOT include any text before or after the JSON array."

	reqBody := ChatRequest{
		Model: model,
		Messages: []Message{
			{Role: "system", Content: sysPrompt},
			{Role: "user", Content: prompt},
		},
		Stream: false,
	}

	body, _ := json.Marshal(reqBody)
	url := fmt.Sprintf("%s/v1/chat/completions", c.BaseURL)
	if !strings.Contains(c.BaseURL, ":11434") && !strings.Contains(c.BaseURL, "v1") {
		url = fmt.Sprintf("%s/api/chat", c.BaseURL)
	}

	req, err := http.NewRequest("POST", url, bytes.NewBuffer(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.APIKey)
	}
	if c.ClientID != "" {
		req.Header.Set("X-Client-Id", c.ClientID)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("AI API returned status: %d", resp.StatusCode)
	}

	var chatResp ChatResponse
	if err := json.NewDecoder(resp.Body).Decode(&chatResp); err != nil {
		return nil, err
	}

	if len(chatResp.Choices) > 0 {
		content := chatResp.Choices[0].Message.Content

		content = strings.TrimSpace(content)
		if strings.HasPrefix(content, "```json") {
			content = strings.TrimPrefix(content, "```json")
			content = strings.TrimSuffix(content, "```")
			content = strings.TrimSpace(content)
		} else if strings.HasPrefix(content, "```") {
			content = strings.TrimPrefix(content, "```")
			content = strings.TrimSuffix(content, "```")
			content = strings.TrimSpace(content)
		}

		first := strings.Index(content, "[")
		last := strings.LastIndex(content, "]")
		if first != -1 && last != -1 && last > first {
			content = content[first : last+1]
		}

		var result []learner.ProbingTarget
		if err := json.Unmarshal([]byte(content), &result); err != nil {
			return nil, fmt.Errorf("failed to parse AI JSON array for Swagger targets: %v, raw content: %s", err, content)
		}

		return result, nil
	}

	return nil, fmt.Errorf("no swagger targets generated")
}

func (c *AIClient) GenerateAuditReport(strikesJSON string, intelligenceJSON string) (string, error) {
	if c.BaseURL == "" {
		return "", fmt.Errorf("AI base URL not configured")
	}

	prompt := fmt.Sprintf(`You are a lead security consultant writing an executive summary and technical breakdown of a recent penetration test.
Review the provided successful strikes and security intelligence, and synthesize a cohesive Markdown report.

### Successful Strikes (JSON):
%s

### Security Intelligence Context (JSON):
%s

### Instructions:
1. **Executive Summary**: Provide a high-level overview of the risks discovered.
2. **Technical Findings**: For each successful strike, explain the vulnerability, the payload used, and the impact.
3. **Remediation**: Provide actionable advice based on the intelligence context (e.g., CWE mitigations).
4. **Format**: Use clean, professional GitHub-flavored Markdown. Do not include any JSON wrapping.

STRICT OUTPUT FORMAT:
Return ONLY the raw Markdown text. Do not wrap it in a code block or JSON object.`, strikesJSON, intelligenceJSON)

	model := "llama3.2:latest"
	if c.Router != nil {
		selected := c.Router.SelectModel(prompt)
		if selected != "" && !strings.Contains(selected, "embed") {
			model = selected
		}
	}

	reqBody := ChatRequest{
		Model: model,
		Messages: []Message{
			{Role: "system", Content: "You are a senior security consultant. Output only professional Markdown."},
			{Role: "user", Content: prompt},
		},
		Stream: false,
	}

	body, _ := json.Marshal(reqBody)
	url := fmt.Sprintf("%s/v1/chat/completions", c.BaseURL)
	if !strings.Contains(c.BaseURL, ":11434") && !strings.Contains(c.BaseURL, "v1") {
		url = fmt.Sprintf("%s/api/chat", c.BaseURL)
	}

	req, err := http.NewRequest("POST", url, bytes.NewBuffer(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.APIKey)
	}
	if c.ClientID != "" {
		req.Header.Set("X-Client-Id", c.ClientID)
	}

	client := &http.Client{
		Timeout: 3 * time.Minute,
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("AI API returned status: %d", resp.StatusCode)
	}

	var chatResp ChatResponse
	if err := json.NewDecoder(resp.Body).Decode(&chatResp); err != nil {
		return "", err
	}

	if len(chatResp.Choices) > 0 {
		return strings.TrimSpace(chatResp.Choices[0].Message.Content), nil
	}

	return "", fmt.Errorf("no audit report generated")
}

func (c *AIClient) AnalyzeFinding(fileName, vulnType, snippet, description string, context []string) (*AIAnalysisResult, error) {
	if c.BaseURL == "" {
		return nil, fmt.Errorf("AI base URL not configured")
	}

	codeContext := ""
	refContext := ""
	feedbackContext := ""

	for _, ctx := range context {
		if strings.HasPrefix(ctx, "[AGENT_FEEDBACK]") {
			feedbackContext += strings.TrimPrefix(ctx, "[AGENT_FEEDBACK] ") + "\n"
		} else if strings.Contains(ctx, "external_reference") || strings.Contains(ctx, "http") {
			refContext += ctx + "\n---\n"
		} else {
			codeContext += ctx + "\n---\n"
		}
	}

	prompt := fmt.Sprintf("Analyze this potential vulnerability in file: %s\nType: %s\nSnippet: %s\nDescription: %s\n", fileName, vulnType, snippet, description)

	if feedbackContext != "" {
		prompt += "\nPAST MISTAKES TO AVOID:\n" + feedbackContext + "\n"
	}
	if refContext != "" {
		prompt += "\nPRIORITY REFERENCE KNOWLEDGE (Deterministic):\n" + refContext
	}
	if codeContext != "" {
		prompt += "\nCODEBASE CONTEXT (Verify sinks/sources):\n" + codeContext
	}

	prompt += "\n\nSTRICT OUTPUT FORMAT:\n" +
		"Return ONLY a raw JSON object with no markdown wrapping. The JSON must exactly match this schema:\n" +
		"{\n" +
		"  \"technical_analysis\": \"[Technical explanation...]\",\n" +
		"  \"recommended_fix\": \"[Corrected code snippet...]\",\n" +
		"  \"confidence\": 0.87,\n" +
		"  \"confidence_source\": \"model|consensus|verified\",\n" +
		"  \"risk_level\": \"high\",\n" +
		"  \"reasoning_trace_id\": \"xyz\"\n" +
		"}\n" +
		"DO NOT include any other text, warnings about constraints, or meta-commentary."

	model := "llama3.2:latest"
	if c.Router != nil {
		selected := c.Router.SelectModel(prompt)
		if selected != "" && !strings.Contains(selected, "embed") {
			model = selected
		}
	}

	sysPrompt := "You are a professional security auditing system. You produce concise, strictly formatted JSON vulnerability reports. " +
		"RULES:\n" +
		"1. ONLY use the standard library of the detected language.\n" +
		"2. NEVER describe code as C/CGo if it is Go.\n" +
		"3. Identify parameterized queries as SECURE.\n" +
		"4. If safe, state 'Security best practices are already followed'.\n" +
		"5. ALWAYS return valid JSON ONLY."

	if strings.Contains(vulnType, "Business Logic") {
		sysPrompt += "\n6. CONSULTANT-TIER ANALYSIS REQUIRED for Business Logic flaws:\n" +
			"   - Evaluate Rate Limiting boundaries: Suggest exponential backoff, jitter, or explicit throttles.\n" +
			"   - Evaluate Race Conditions / TOCTOU: Recommend pessimistic locks (FOR UPDATE), mutual exclusion (Mutex), or atomic transactions.\n" +
			"   - Evaluate Abuse Patterns (BOLA/IDOR/Mass Assignment): Explicitly demand ownership validation checks or strict DTO mappings.\n" +
			"   - Draw upon real-world industry abuse patterns to justify the risk level."
	}

	if strings.Contains(vulnType, "Supply Chain") {
		sysPrompt += "\n6. SOFTWARE COMPOSITION ANALYSIS REQUIRED for Supply Chain flaws:\n" +
			"   - If a CVE is flagged, instruct the user to update the library to the latest stable patch version.\n" +
			"   - Suggest auditing 'go.sum', 'package-lock.json', or 'requirements.txt' for transitive dependencies.\n" +
			"   - Suggest implementing automated Dependabot/Renovate configurations."
	}

	reqBody := ChatRequest{
		Model: model,
		Messages: []Message{
			{Role: "system", Content: sysPrompt},
			{Role: "user", Content: prompt},
		},
		Stream: false,
	}

	body, _ := json.Marshal(reqBody)

	// Standardize URL logic to match ExplainCode
	url := fmt.Sprintf("%s/v1/chat/completions", c.BaseURL)
	if !strings.Contains(c.BaseURL, ":11434") && !strings.Contains(c.BaseURL, "v1") {
		url = fmt.Sprintf("%s/api/chat", c.BaseURL)
	}

	log.Printf("AI: AnalyzeFinding hitting %s with model %s", url, model)
	req, err := http.NewRequest("POST", url, bytes.NewBuffer(body))
	if err != nil {
		return nil, err
	}

	req.Header.Set("Content-Type", "application/json")
	if c.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.APIKey)
	}
	if c.ClientID != "" {
		req.Header.Set("X-Client-Id", c.ClientID)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("AI API returned status: %d", resp.StatusCode)
	}

	var chatResp ChatResponse
	if err := json.NewDecoder(resp.Body).Decode(&chatResp); err != nil {
		return nil, err
	}

	if len(chatResp.Choices) > 0 {
		content := chatResp.Choices[0].Message.Content

		// Clean up markdown block if present
		content = strings.TrimSpace(content)
		if strings.HasPrefix(content, "```json") {
			content = strings.TrimPrefix(content, "```json")
			content = strings.TrimSuffix(content, "```")
			content = strings.TrimSpace(content)
		} else if strings.HasPrefix(content, "```") {
			content = strings.TrimPrefix(content, "```")
			content = strings.TrimSuffix(content, "```")
			content = strings.TrimSpace(content)
		}

		// Robust cleaning: LLMs often put literal newlines in JSON strings.
		// We'll try to replace literal newlines that are NOT between quotes with space,
		// but that's hard. A simpler way is to try and find the first { and last }.
		first := strings.Index(content, "{")
		last := strings.LastIndex(content, "}")
		if first != -1 && last != -1 && last > first {
			content = content[first : last+1]
		}

		var result AIAnalysisResult
		if err := json.Unmarshal([]byte(content), &result); err != nil {
			// Second attempt: escape newlines in case the LLM didn't
			escapedContent := strings.ReplaceAll(content, "\n", "\\n")
			// This might break the actual structure, so we only do it if the first fail occurs
			if err2 := json.Unmarshal([]byte(escapedContent), &result); err2 == nil {
				return &result, nil
			}
			return nil, fmt.Errorf("failed to parse AI JSON response: %v, raw content: %s", err, content)
		}

		return &result, nil
	}

	return nil, fmt.Errorf("no analysis available")
}
