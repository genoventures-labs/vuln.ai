package learner

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
)

// LessonType defines the category of the curriculum lesson.
type LessonType string

const (
	LessonTypeBaseline  LessonType = "Baseline"
	LessonTypeCanonical LessonType = "Canonical"
	LessonTypeClean     LessonType = "Clean"
	LessonTypeFailure   LessonType = "Failure"
	LessonTypeEdge      LessonType = "Edge"
)

// CurriculumLesson represents a formalized teaching artifact for the agent.
type CurriculumLesson struct {
	ID      string     `json:"id"`
	Topic   string     `json:"topic"` // e.g., "SQLi", "CWE-285", "IDOR"
	Type    LessonType `json:"type"`
	Content string     `json:"content"` // Markdown or raw code syllabus
}

// CurriculumStore manages the persistence and retrieval of the school's syllabus.
type CurriculumStore struct {
	mu       sync.Mutex
	filePath string
	Lessons  map[string]CurriculumLesson `json:"lessons"`
}

// NewCurriculumStore initializes a new or existing curriculum database.
func NewCurriculumStore(dataDir string) (*CurriculumStore, error) {
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		return nil, err
	}

	filePath := filepath.Join(dataDir, "curriculum.json")
	store := &CurriculumStore{
		filePath: filePath,
		Lessons:  make(map[string]CurriculumLesson),
	}

	if _, err := os.Stat(filePath); err == nil {
		data, err := os.ReadFile(filePath)
		if err == nil && len(data) > 0 {
			_ = json.Unmarshal(data, &store.Lessons)
		}
	}

	return store, nil
}

// SaveLesson adds or updates a lesson in the curriculum.
func (s *CurriculumStore) SaveLesson(lesson CurriculumLesson) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.Lessons[lesson.ID] = lesson

	data, err := json.MarshalIndent(s.Lessons, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(s.filePath, data, 0644)
}

// GetLessons returns all lessons matching the topic and type. Empty strings mean "any".
func (s *CurriculumStore) GetLessons(topic string, lType LessonType) []CurriculumLesson {
	s.mu.Lock()
	defer s.mu.Unlock()

	var list []CurriculumLesson
	for _, l := range s.Lessons {
		topicMatch := topic == "" || l.Topic == topic
		typeMatch := lType == "" || l.Type == lType
		if topicMatch && typeMatch {
			list = append(list, l)
		}
	}
	return list
}

// GetAllLessons returns the entire curriculum.
func (s *CurriculumStore) GetAllLessons() []CurriculumLesson {
	s.mu.Lock()
	defer s.mu.Unlock()

	var list []CurriculumLesson
	for _, l := range s.Lessons {
		list = append(list, l)
	}
	return list
}
