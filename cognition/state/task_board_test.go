package state

import (
	"path/filepath"
	"testing"
)

func TestTaskBoardStoreCreateAndPersist(t *testing.T) {
	path := filepath.Join(t.TempDir(), "task_boards.json")
	store, err := NewTaskBoardStoreWithPath(path)
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	board, err := store.CreateBoardFromSteps("training", "ship namespace taskboards", []string{"plan", "build", "verify"})
	if err != nil {
		t.Fatalf("create board: %v", err)
	}
	if err := store.Save(); err != nil {
		t.Fatalf("save board: %v", err)
	}

	reloaded, err := NewTaskBoardStoreWithPath(path)
	if err != nil {
		t.Fatalf("reload store: %v", err)
	}
	active, ok := reloaded.ActiveBoard("training")
	if !ok {
		t.Fatal("expected active board for namespace")
	}
	if active.ID != board.ID {
		t.Fatalf("expected active board %q, got %q", board.ID, active.ID)
	}
	if len(active.Items) != 3 {
		t.Fatalf("expected 3 items, got %d", len(active.Items))
	}
}

func TestTaskBoardStoreDependencyGates(t *testing.T) {
	store, err := NewTaskBoardStoreWithPath(filepath.Join(t.TempDir(), "task_boards.json"))
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	board, err := store.CreateBoardFromSteps("training", "dependency flow", []string{"task one", "task two"})
	if err != nil {
		t.Fatalf("create board: %v", err)
	}
	if err := store.UpdateTaskStatus(board.ID, "task-2", TaskStatusInProgress); err == nil {
		t.Fatal("expected dependency gating for task-2")
	}
	if err := store.UpdateTaskStatus(board.ID, "task-1", TaskStatusDone); err != nil {
		t.Fatalf("set task-1 done: %v", err)
	}
	if err := store.UpdateTaskStatus(board.ID, "task-2", TaskStatusInProgress); err != nil {
		t.Fatalf("set task-2 in progress after deps: %v", err)
	}
}

func TestTaskBoardStoreProgress(t *testing.T) {
	store, err := NewTaskBoardStoreWithPath(filepath.Join(t.TempDir(), "task_boards.json"))
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	board, err := store.CreateBoard("ops", "progress counters", []TaskBoardItem{
		{ID: "a", Title: "A", Status: TaskStatusDone},
		{ID: "b", Title: "B", Status: TaskStatusInProgress},
		{ID: "c", Title: "C", Status: TaskStatusBlocked},
		{ID: "d", Title: "D", Status: TaskStatusPending},
	})
	if err != nil {
		t.Fatalf("create board: %v", err)
	}
	p := store.Progress(board.ID)
	if p.Total != 4 || p.Done != 1 || p.InProgress != 1 || p.Blocked != 1 || p.Pending != 1 {
		t.Fatalf("unexpected progress: %+v", p)
	}
}
