package taloscli

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Thynaptic/P-LMv1/pkg/state"
)

func TestTasksShowAndNextRenderActiveBoard(t *testing.T) {
	prevManagerFactory := namespaceManagerFactory
	prevStoreFactory := taskBoardStoreFactory
	prevTasksNS := tasksNamespace
	prevBoardID := tasksBoardID
	defer func() {
		namespaceManagerFactory = prevManagerFactory
		taskBoardStoreFactory = prevStoreFactory
		tasksNamespace = prevTasksNS
		tasksBoardID = prevBoardID
	}()

	tmp := t.TempDir()
	statePath := filepath.Join(tmp, "session_state.json")
	boardsPath := filepath.Join(tmp, "task_boards.json")

	sm, err := state.NewManagerWithPath(statePath)
	if err != nil {
		t.Fatalf("state manager: %v", err)
	}
	sm.SetActiveNamespace("training")
	if err := sm.Save(); err != nil {
		t.Fatalf("save state: %v", err)
	}
	store, err := state.NewTaskBoardStoreWithPath(boardsPath)
	if err != nil {
		t.Fatalf("task board store: %v", err)
	}
	board, err := store.CreateBoardFromSteps("training", "Test objective", []string{"step one", "step two"})
	if err != nil {
		t.Fatalf("create board: %v", err)
	}
	if err := store.UpdateTaskStatus(board.ID, "task-1", state.TaskStatusDone); err != nil {
		t.Fatalf("update task status: %v", err)
	}
	if err := store.Save(); err != nil {
		t.Fatalf("save board store: %v", err)
	}

	namespaceManagerFactory = func() (*state.Manager, error) { return state.NewManagerWithPath(statePath) }
	taskBoardStoreFactory = func() (*state.TaskBoardStore, error) { return state.NewTaskBoardStoreWithPath(boardsPath) }

	out := &bytes.Buffer{}
	errOut := &bytes.Buffer{}
	rootCmd.SetOut(out)
	rootCmd.SetErr(errOut)
	rootCmd.SetArgs([]string{"tasks", "show"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("tasks show failed: %v", err)
	}
	if !strings.Contains(out.String(), "board_id: "+board.ID) || !strings.Contains(out.String(), "progress: done:1/2") {
		t.Fatalf("expected board/progress in output, got: %s", out.String())
	}

	out.Reset()
	rootCmd.SetArgs([]string{"tasks", "next"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("tasks next failed: %v", err)
	}
	if !strings.Contains(out.String(), "ready_count: 1") || !strings.Contains(out.String(), "task-2") {
		t.Fatalf("expected next task in output, got: %s", out.String())
	}
}

func TestTasksUpdateChangesStatus(t *testing.T) {
	prevManagerFactory := namespaceManagerFactory
	prevStoreFactory := taskBoardStoreFactory
	prevTasksNS := tasksNamespace
	prevBoardID := tasksBoardID
	prevTaskID := tasksTaskID
	prevStatus := tasksStatus
	defer func() {
		namespaceManagerFactory = prevManagerFactory
		taskBoardStoreFactory = prevStoreFactory
		tasksNamespace = prevTasksNS
		tasksBoardID = prevBoardID
		tasksTaskID = prevTaskID
		tasksStatus = prevStatus
	}()

	tmp := t.TempDir()
	statePath := filepath.Join(tmp, "session_state.json")
	boardsPath := filepath.Join(tmp, "task_boards.json")

	sm, err := state.NewManagerWithPath(statePath)
	if err != nil {
		t.Fatalf("state manager: %v", err)
	}
	sm.SetActiveNamespace("training")
	if err := sm.Save(); err != nil {
		t.Fatalf("save state: %v", err)
	}
	store, err := state.NewTaskBoardStoreWithPath(boardsPath)
	if err != nil {
		t.Fatalf("task board store: %v", err)
	}
	board, err := store.CreateBoardFromSteps("training", "Test objective", []string{"step one"})
	if err != nil {
		t.Fatalf("create board: %v", err)
	}
	if err := store.Save(); err != nil {
		t.Fatalf("save board store: %v", err)
	}

	namespaceManagerFactory = func() (*state.Manager, error) { return state.NewManagerWithPath(statePath) }
	taskBoardStoreFactory = func() (*state.TaskBoardStore, error) { return state.NewTaskBoardStoreWithPath(boardsPath) }

	out := &bytes.Buffer{}
	errOut := &bytes.Buffer{}
	rootCmd.SetOut(out)
	rootCmd.SetErr(errOut)
	rootCmd.SetArgs([]string{"tasks", "update", "--id", "task-1", "--status", "done"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("tasks update failed: %v", err)
	}
	if !strings.Contains(out.String(), "status: done") {
		t.Fatalf("expected status update output, got: %s", out.String())
	}

	reloaded, err := state.NewTaskBoardStoreWithPath(boardsPath)
	if err != nil {
		t.Fatalf("reload board store: %v", err)
	}
	updated, ok := reloaded.Board(board.ID)
	if !ok {
		t.Fatal("expected board after reload")
	}
	if len(updated.Items) != 1 || updated.Items[0].Status != state.TaskStatusDone {
		t.Fatalf("expected task done after update, got: %+v", updated.Items)
	}
}
