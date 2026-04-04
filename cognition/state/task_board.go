package state

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const defaultTaskBoardFile = ".memory/task_boards.json"

const (
	TaskStatusPending    = "pending"
	TaskStatusInProgress = "in_progress"
	TaskStatusDone       = "done"
	TaskStatusBlocked    = "blocked"
)

type TaskBoardItem struct {
	ID          string    `json:"id"`
	Title       string    `json:"title"`
	Description string    `json:"description,omitempty"`
	Status      string    `json:"status"`
	DependsOn   []string  `json:"depends_on,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
	StartedAt   time.Time `json:"started_at,omitempty"`
	CompletedAt time.Time `json:"completed_at,omitempty"`
}

type TaskBoard struct {
	ID            string          `json:"id"`
	Namespace     string          `json:"namespace,omitempty"`
	Objective     string          `json:"objective"`
	Items         []TaskBoardItem `json:"items"`
	ActiveTaskID  string          `json:"active_task_id,omitempty"`
	CreatedAt     time.Time       `json:"created_at"`
	UpdatedAt     time.Time       `json:"updated_at"`
	LastEventNote string          `json:"last_event_note,omitempty"`
}

type TaskBoardProgress struct {
	Total      int `json:"total"`
	Pending    int `json:"pending"`
	InProgress int `json:"in_progress"`
	Done       int `json:"done"`
	Blocked    int `json:"blocked"`
}

type TaskBoardStore struct {
	mu       sync.RWMutex
	filePath string
	boards   map[string]TaskBoard
	active   map[string]string
}

func NewTaskBoardStore() (*TaskBoardStore, error) {
	return NewTaskBoardStoreWithPath(defaultTaskBoardFile)
}

func NewTaskBoardStoreWithPath(path string) (*TaskBoardStore, error) {
	s := &TaskBoardStore{
		filePath: strings.TrimSpace(path),
		boards:   map[string]TaskBoard{},
		active:   map[string]string{},
	}
	if err := s.Load(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *TaskBoardStore) Load() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if strings.TrimSpace(s.filePath) == "" {
		return fmt.Errorf("task board store path is required")
	}
	raw, err := os.ReadFile(s.filePath)
	if err != nil {
		if os.IsNotExist(err) {
			s.boards = map[string]TaskBoard{}
			s.active = map[string]string{}
			return nil
		}
		return err
	}
	var payload struct {
		Boards map[string]TaskBoard `json:"boards"`
		Active map[string]string    `json:"active"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return err
	}
	s.boards = map[string]TaskBoard{}
	for k, b := range payload.Boards {
		id := normalizeTaskBoardID(k)
		if id == "" {
			id = normalizeTaskBoardID(b.ID)
		}
		if id == "" {
			continue
		}
		b.ID = id
		b.Namespace = normalizeNamespaceKey(b.Namespace)
		b.Items = normalizeTaskItems(b.Items)
		s.boards[id] = b
	}
	s.active = map[string]string{}
	for ns, id := range payload.Active {
		n := normalizeNamespaceKey(ns)
		boardID := normalizeTaskBoardID(id)
		if n == "" || boardID == "" {
			continue
		}
		s.active[n] = boardID
	}
	return nil
}

func (s *TaskBoardStore) Save() error {
	s.mu.RLock()
	payload := struct {
		Boards map[string]TaskBoard `json:"boards"`
		Active map[string]string    `json:"active"`
	}{
		Boards: map[string]TaskBoard{},
		Active: map[string]string{},
	}
	for id, b := range s.boards {
		payload.Boards[id] = b
	}
	for ns, id := range s.active {
		payload.Active[ns] = id
	}
	path := s.filePath
	s.mu.RUnlock()

	if strings.TrimSpace(path) == "" {
		return fmt.Errorf("task board store path is required")
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil && dir != "." {
		return err
	}
	b, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o644)
}

func (s *TaskBoardStore) CreateBoard(namespace, objective string, tasks []TaskBoardItem) (TaskBoard, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ns := normalizeNamespaceKey(namespace)
	obj := strings.TrimSpace(objective)
	if obj == "" {
		return TaskBoard{}, fmt.Errorf("objective is required")
	}
	items := normalizeTaskItems(tasks)
	if len(items) == 0 {
		return TaskBoard{}, fmt.Errorf("at least one task is required")
	}
	now := time.Now().UTC()
	id := fmt.Sprintf("taskboard_%d", now.UnixNano())
	board := TaskBoard{
		ID:        id,
		Namespace: ns,
		Objective: obj,
		Items:     items,
		CreatedAt: now,
		UpdatedAt: now,
	}
	s.boards[id] = board
	if ns != "" {
		s.active[ns] = id
	}
	return board, nil
}

func (s *TaskBoardStore) CreateBoardFromSteps(namespace, objective string, steps []string) (TaskBoard, error) {
	items := make([]TaskBoardItem, 0, len(steps))
	prev := ""
	for i, raw := range steps {
		title := strings.TrimSpace(raw)
		if title == "" {
			continue
		}
		itemID := "task-" + strconv.Itoa(i+1)
		item := TaskBoardItem{
			ID:        itemID,
			Title:     title,
			Status:    TaskStatusPending,
			CreatedAt: time.Now().UTC(),
			UpdatedAt: time.Now().UTC(),
		}
		if prev != "" {
			item.DependsOn = []string{prev}
		}
		prev = itemID
		items = append(items, item)
	}
	return s.CreateBoard(namespace, objective, items)
}

func (s *TaskBoardStore) ActiveBoard(namespace string) (TaskBoard, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	ns := normalizeNamespaceKey(namespace)
	if ns == "" {
		return TaskBoard{}, false
	}
	id := normalizeTaskBoardID(s.active[ns])
	if id == "" {
		return TaskBoard{}, false
	}
	b, ok := s.boards[id]
	return b, ok
}

func (s *TaskBoardStore) Board(boardID string) (TaskBoard, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	id := normalizeTaskBoardID(boardID)
	if id == "" {
		return TaskBoard{}, false
	}
	b, ok := s.boards[id]
	return b, ok
}

func (s *TaskBoardStore) SetActiveBoard(namespace, boardID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ns := normalizeNamespaceKey(namespace)
	id := normalizeTaskBoardID(boardID)
	if ns == "" || id == "" {
		return
	}
	if _, ok := s.boards[id]; !ok {
		return
	}
	s.active[ns] = id
}

func (s *TaskBoardStore) ListBoards(namespace string) []TaskBoard {
	s.mu.RLock()
	defer s.mu.RUnlock()
	ns := normalizeNamespaceKey(namespace)
	out := make([]TaskBoard, 0, len(s.boards))
	for _, b := range s.boards {
		if ns != "" && normalizeNamespaceKey(b.Namespace) != ns {
			continue
		}
		out = append(out, b)
	}
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].CreatedAt.After(out[j].CreatedAt)
	})
	return out
}

func (s *TaskBoardStore) ClearActiveBoard(namespace string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ns := normalizeNamespaceKey(namespace)
	if ns == "" {
		return
	}
	delete(s.active, ns)
}

func (s *TaskBoardStore) UpdateTaskStatus(boardID, taskID, status string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	id := normalizeTaskBoardID(boardID)
	tid := strings.TrimSpace(taskID)
	next := normalizeTaskStatus(status)
	if id == "" || tid == "" || next == "" {
		return fmt.Errorf("board id, task id, and status are required")
	}
	board, ok := s.boards[id]
	if !ok {
		return fmt.Errorf("task board %q not found", id)
	}
	idx := -1
	for i := range board.Items {
		if strings.EqualFold(strings.TrimSpace(board.Items[i].ID), tid) {
			idx = i
			break
		}
	}
	if idx < 0 {
		return fmt.Errorf("task %q not found", tid)
	}
	if (next == TaskStatusDone || next == TaskStatusInProgress) && !depsSatisfied(board, board.Items[idx]) {
		return fmt.Errorf("task %q dependencies are not complete", tid)
	}
	now := time.Now().UTC()
	board.Items[idx].Status = next
	board.Items[idx].UpdatedAt = now
	if next == TaskStatusInProgress {
		board.Items[idx].StartedAt = now
		board.ActiveTaskID = board.Items[idx].ID
	}
	if next == TaskStatusDone {
		board.Items[idx].CompletedAt = now
		if strings.EqualFold(strings.TrimSpace(board.ActiveTaskID), board.Items[idx].ID) {
			board.ActiveTaskID = ""
		}
	}
	board.UpdatedAt = now
	board.LastEventNote = fmt.Sprintf("%s -> %s", board.Items[idx].ID, next)
	s.boards[id] = board
	return nil
}

func (s *TaskBoardStore) ReadyTasks(boardID string) []TaskBoardItem {
	s.mu.RLock()
	defer s.mu.RUnlock()
	id := normalizeTaskBoardID(boardID)
	board, ok := s.boards[id]
	if !ok {
		return nil
	}
	out := make([]TaskBoardItem, 0)
	for _, item := range board.Items {
		if item.Status != TaskStatusPending {
			continue
		}
		if depsSatisfied(board, item) {
			out = append(out, item)
		}
	}
	return out
}

func (s *TaskBoardStore) Progress(boardID string) TaskBoardProgress {
	s.mu.RLock()
	defer s.mu.RUnlock()
	id := normalizeTaskBoardID(boardID)
	board, ok := s.boards[id]
	if !ok {
		return TaskBoardProgress{}
	}
	return progressForBoard(board)
}

func depsSatisfied(board TaskBoard, item TaskBoardItem) bool {
	if len(item.DependsOn) == 0 {
		return true
	}
	done := map[string]bool{}
	for _, other := range board.Items {
		if other.Status == TaskStatusDone {
			done[strings.ToLower(strings.TrimSpace(other.ID))] = true
		}
	}
	for _, dep := range item.DependsOn {
		if !done[strings.ToLower(strings.TrimSpace(dep))] {
			return false
		}
	}
	return true
}

func progressForBoard(board TaskBoard) TaskBoardProgress {
	p := TaskBoardProgress{Total: len(board.Items)}
	for _, item := range board.Items {
		switch normalizeTaskStatus(item.Status) {
		case TaskStatusPending:
			p.Pending++
		case TaskStatusInProgress:
			p.InProgress++
		case TaskStatusDone:
			p.Done++
		case TaskStatusBlocked:
			p.Blocked++
		default:
			p.Pending++
		}
	}
	return p
}

func normalizeTaskBoardID(v string) string {
	return strings.ToLower(strings.TrimSpace(v))
}

func normalizeTaskStatus(v string) string {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case TaskStatusPending:
		return TaskStatusPending
	case TaskStatusInProgress:
		return TaskStatusInProgress
	case TaskStatusDone:
		return TaskStatusDone
	case TaskStatusBlocked:
		return TaskStatusBlocked
	default:
		return ""
	}
}

func normalizeTaskItems(items []TaskBoardItem) []TaskBoardItem {
	out := make([]TaskBoardItem, 0, len(items))
	seen := map[string]bool{}
	for _, item := range items {
		id := strings.TrimSpace(item.ID)
		title := strings.TrimSpace(item.Title)
		if id == "" {
			continue
		}
		if title == "" {
			title = id
		}
		id = strings.ToLower(id)
		if seen[id] {
			continue
		}
		seen[id] = true
		status := normalizeTaskStatus(item.Status)
		if status == "" {
			status = TaskStatusPending
		}
		deps := make([]string, 0, len(item.DependsOn))
		depSeen := map[string]bool{}
		for _, dep := range item.DependsOn {
			d := strings.ToLower(strings.TrimSpace(dep))
			if d == "" || depSeen[d] || d == id {
				continue
			}
			depSeen[d] = true
			deps = append(deps, d)
		}
		sort.Strings(deps)
		out = append(out, TaskBoardItem{
			ID:          id,
			Title:       title,
			Description: strings.TrimSpace(item.Description),
			Status:      status,
			DependsOn:   deps,
			CreatedAt:   item.CreatedAt.UTC(),
			UpdatedAt:   item.UpdatedAt.UTC(),
			StartedAt:   item.StartedAt.UTC(),
			CompletedAt: item.CompletedAt.UTC(),
		})
	}
	return out
}
