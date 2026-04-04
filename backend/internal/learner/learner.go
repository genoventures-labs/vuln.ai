package learner

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"

	"github.com/user/azimuthal-belt/backend/internal/db"
)

type VulnerabilityReport struct {
	ID            string   `json:"id"`
	Title         string   `json:"title"`
	BugType       string   `json:"bug_type"`
	Severity      string   `json:"severity"`
	PoC           string   `json:"poc"`
	URL           string   `json:"url"`
	LastUpdated   string   `json:"last_updated"`
	Source        string   `json:"source"`
	Version       string   `json:"version"`
	ParseWarnings []string `json:"parse_warnings"`
}

type CWEDefinition struct {
	ID            string   `json:"id"`
	Name          string   `json:"name"`
	Description   string   `json:"description"`
	Class         string   `json:"class"`
	Mitigations   []string `json:"mitigations"`
	URL           string   `json:"url"`
	LastUpdated   string   `json:"last_updated"`
	Source        string   `json:"source"`
	Version       string   `json:"version"`
	ParseWarnings []string `json:"parse_warnings"`
}

type AgentFeedback struct {
	ID            string `json:"id"`
	TaskType      string `json:"task_type"`  // e.g. "Recon", "PayloadGen", "Verify"
	ContextID     string `json:"context_id"` // E.g., Target URL or Vulnerability Type
	FailedPath    string `json:"failed_path"`
	CorrectedPath string `json:"corrected_path"`
	Timestamp     string `json:"timestamp"`
}

type FormField struct {
	Name  string `json:"name"`
	Type  string `json:"type"`
	Value string `json:"value"`
}

// ProbingTarget represents an interactive element found during crawling that can be probed.
type ProbingTarget struct {
	SourceURL string      `json:"source_url"`
	Method    string      `json:"method"`
	Action    string      `json:"action"`
	Type      string      `json:"type"` // e.g., "api_target" or "form"
	Fields    []FormField `json:"fields"`
}

type Store struct {
	mu               sync.Mutex
	h1FilePath       string
	cweFilePath      string
	feedbackFilePath string
	Reports          map[string]VulnerabilityReport `json:"reports"`
	CWEDefs          map[string]CWEDefinition       `json:"cwe_defs"`
	Feedback         map[string]AgentFeedback       `json:"feedback"`
	PBClient         *db.PocketbaseClient
}

func NewStore(dataDir string) (*Store, error) {
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		return nil, err
	}

	h1FilePath := filepath.Join(dataDir, "learned_reports.json")
	cweFilePath := filepath.Join(dataDir, "learned_cwes.json")
	feedbackFilePath := filepath.Join(dataDir, "learned_feedback.json")
	store := &Store{
		h1FilePath:       h1FilePath,
		cweFilePath:      cweFilePath,
		feedbackFilePath: feedbackFilePath,
		Reports:          make(map[string]VulnerabilityReport),
		CWEDefs:          make(map[string]CWEDefinition),
		Feedback:         make(map[string]AgentFeedback),
	}

	if _, err := os.Stat(h1FilePath); err == nil {
		data, err := os.ReadFile(h1FilePath)
		if err == nil && len(data) > 0 {
			_ = json.Unmarshal(data, &store.Reports)
		}
	}

	if _, err := os.Stat(cweFilePath); err == nil {
		data, err := os.ReadFile(cweFilePath)
		if err == nil && len(data) > 0 {
			_ = json.Unmarshal(data, &store.CWEDefs)
		}
	}

	if _, err := os.Stat(feedbackFilePath); err == nil {
		data, err := os.ReadFile(feedbackFilePath)
		if err == nil && len(data) > 0 {
			_ = json.Unmarshal(data, &store.Feedback)
		}
	}

	return store, nil
}

func (s *Store) SaveReport(report VulnerabilityReport) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.Reports[report.ID] = report

	data, err := json.MarshalIndent(s.Reports, "", "  ")
	if err != nil {
		return err
	}

	if s.PBClient != nil {
		go func() {
			_ = s.PBClient.SaveIntelligence(report)
		}()
	}

	return os.WriteFile(s.h1FilePath, data, 0644)
}

func (s *Store) SaveCWE(cwe CWEDefinition) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.CWEDefs[cwe.ID] = cwe

	data, err := json.MarshalIndent(s.CWEDefs, "", "  ")
	if err != nil {
		return err
	}

	if s.PBClient != nil {
		go func() {
			_ = s.PBClient.SaveIntelligence(cwe)
		}()
	}

	return os.WriteFile(s.cweFilePath, data, 0644)
}

func (s *Store) GetReports() []VulnerabilityReport {
	s.mu.Lock()
	defer s.mu.Unlock()

	var list []VulnerabilityReport
	for _, r := range s.Reports {
		list = append(list, r)
	}
	return list
}

func (s *Store) NeedsLearning(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	_, exists := s.Reports[id]
	return !exists
}

func (s *Store) NeedsLearningCWE(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	_, exists := s.CWEDefs[id]
	return !exists
}

func (s *Store) SaveFeedback(f AgentFeedback) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.Feedback[f.ID] = f

	data, err := json.MarshalIndent(s.Feedback, "", "  ")
	if err != nil {
		return err
	}

	if s.PBClient != nil {
		go func() {
			_ = s.PBClient.SaveMemory(db.MemoryNode{
				Key:     f.ID,
				Content: f.CorrectedPath,
				Metadata: map[string]interface{}{
					"task_type":   f.TaskType,
					"context_id":  f.ContextID,
					"failed_path": f.FailedPath,
					"timestamp":   f.Timestamp,
				},
			})
		}()
	}

	return os.WriteFile(s.feedbackFilePath, data, 0644)
}

func (s *Store) GetRelevantFeedback(taskType string, contextID string) []AgentFeedback {
	s.mu.Lock()
	defer s.mu.Unlock()

	var list []AgentFeedback
	for _, f := range s.Feedback {
		taskMatch := f.TaskType == taskType || taskType == ""
		contextMatch := f.ContextID == contextID || contextID == ""
		if taskMatch && contextMatch {
			list = append(list, f)
		}
	}
	return list
}
