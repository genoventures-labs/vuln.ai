package taloscli

import (
	"fmt"
	"strings"

	"github.com/Thynaptic/P-LMv1/pkg/state"
	"github.com/spf13/cobra"
)

var (
	taskBoardStoreFactory = state.NewTaskBoardStore
	tasksNamespace        string
	tasksBoardID          string
	tasksTaskID           string
	tasksStatus           string
)

var tasksCmd = &cobra.Command{
	Use:   "tasks",
	Short: "Manage autonomous task boards and progression.",
}

var tasksListCmd = &cobra.Command{
	Use:   "list",
	Short: "List task boards in a namespace.",
	Run: func(cmd *cobra.Command, args []string) {
		ns, ok := resolveNamespaceTarget(cmd, tasksNamespace)
		if !ok {
			return
		}
		store, err := taskBoardStoreFactory()
		if err != nil {
			fmt.Fprintf(cmd.OutOrStdout(), "Error loading task board store: %v\n", err)
			return
		}
		boards := store.ListBoards(ns)
		printCommandStatus(cmd.OutOrStdout(), "talos tasks list", "success")
		printSection(cmd.OutOrStdout(), "target")
		printKV(cmd.OutOrStdout(), "namespace", ns)
		printSection(cmd.OutOrStdout(), "summary")
		printKV(cmd.OutOrStdout(), "boards", fmt.Sprintf("%d", len(boards)))
		if len(boards) == 0 {
			return
		}
		active, hasActive := store.ActiveBoard(ns)
		printSection(cmd.OutOrStdout(), "boards")
		for _, board := range boards {
			progress := store.Progress(board.ID)
			tag := "inactive"
			if hasActive && strings.EqualFold(strings.TrimSpace(active.ID), strings.TrimSpace(board.ID)) {
				tag = "active"
			}
			fmt.Fprintf(cmd.OutOrStdout(), "  - %s [%s] done:%d/%d in_progress:%d blocked:%d objective:%s\n",
				board.ID, tag, progress.Done, progress.Total, progress.InProgress, progress.Blocked, strings.TrimSpace(board.Objective))
		}
	},
}

var tasksShowCmd = &cobra.Command{
	Use:   "show",
	Short: "Show active task board and progress.",
	Run: func(cmd *cobra.Command, args []string) {
		ns, ok := resolveNamespaceTarget(cmd, tasksNamespace)
		if !ok {
			return
		}
		store, err := taskBoardStoreFactory()
		if err != nil {
			fmt.Fprintf(cmd.OutOrStdout(), "Error loading task board store: %v\n", err)
			return
		}
		board, found := resolveTaskBoard(store, ns, tasksBoardID)
		printCommandStatus(cmd.OutOrStdout(), "talos tasks show", "success")
		printSection(cmd.OutOrStdout(), "target")
		printKV(cmd.OutOrStdout(), "namespace", ns)
		if !found {
			printSection(cmd.OutOrStdout(), "summary")
			printKV(cmd.OutOrStdout(), "active_board", "none")
			return
		}
		progress := store.Progress(board.ID)
		printSection(cmd.OutOrStdout(), "summary")
		printKV(cmd.OutOrStdout(), "board_id", board.ID)
		printKV(cmd.OutOrStdout(), "objective", board.Objective)
		printKV(cmd.OutOrStdout(), "active_task_id", emptyAsNone(board.ActiveTaskID))
		printKV(cmd.OutOrStdout(), "progress", fmt.Sprintf("done:%d/%d pending:%d in_progress:%d blocked:%d", progress.Done, progress.Total, progress.Pending, progress.InProgress, progress.Blocked))
		printSection(cmd.OutOrStdout(), "items")
		for _, item := range board.Items {
			deps := "none"
			if len(item.DependsOn) > 0 {
				deps = strings.Join(item.DependsOn, ",")
			}
			fmt.Fprintf(cmd.OutOrStdout(), "  - %s [%s] deps:%s title:%s\n", item.ID, item.Status, deps, item.Title)
		}
	},
}

var tasksNextCmd = &cobra.Command{
	Use:   "next",
	Short: "Show ready (dependency-unblocked) pending tasks.",
	Run: func(cmd *cobra.Command, args []string) {
		ns, ok := resolveNamespaceTarget(cmd, tasksNamespace)
		if !ok {
			return
		}
		store, err := taskBoardStoreFactory()
		if err != nil {
			fmt.Fprintf(cmd.OutOrStdout(), "Error loading task board store: %v\n", err)
			return
		}
		board, found := resolveTaskBoard(store, ns, tasksBoardID)
		printCommandStatus(cmd.OutOrStdout(), "talos tasks next", "success")
		if !found {
			printSection(cmd.OutOrStdout(), "summary")
			printKV(cmd.OutOrStdout(), "active_board", "none")
			return
		}
		ready := store.ReadyTasks(board.ID)
		printSection(cmd.OutOrStdout(), "summary")
		printKV(cmd.OutOrStdout(), "board_id", board.ID)
		printKV(cmd.OutOrStdout(), "ready_count", fmt.Sprintf("%d", len(ready)))
		if len(ready) == 0 {
			return
		}
		printSection(cmd.OutOrStdout(), "ready_tasks")
		for _, item := range ready {
			fmt.Fprintf(cmd.OutOrStdout(), "  - %s title:%s\n", item.ID, item.Title)
		}
	},
}

var tasksUpdateCmd = &cobra.Command{
	Use:   "update",
	Short: "Update a task status on a board.",
	Run: func(cmd *cobra.Command, args []string) {
		ns, ok := resolveNamespaceTarget(cmd, tasksNamespace)
		if !ok {
			return
		}
		taskID := strings.TrimSpace(tasksTaskID)
		status := strings.TrimSpace(tasksStatus)
		if taskID == "" || status == "" {
			fmt.Fprintln(cmd.OutOrStdout(), "Error: --id and --status are required")
			return
		}
		store, err := taskBoardStoreFactory()
		if err != nil {
			fmt.Fprintf(cmd.OutOrStdout(), "Error loading task board store: %v\n", err)
			return
		}
		board, found := resolveTaskBoard(store, ns, tasksBoardID)
		if !found {
			fmt.Fprintln(cmd.OutOrStdout(), "Error: no active task board for namespace")
			return
		}
		if err := store.UpdateTaskStatus(board.ID, taskID, status); err != nil {
			fmt.Fprintf(cmd.OutOrStdout(), "Error: %v\n", err)
			return
		}
		if err := store.Save(); err != nil {
			fmt.Fprintf(cmd.OutOrStdout(), "Error saving task board store: %v\n", err)
			return
		}
		progress := store.Progress(board.ID)
		printCommandStatus(cmd.OutOrStdout(), "talos tasks update", "success")
		printSection(cmd.OutOrStdout(), "result")
		printKV(cmd.OutOrStdout(), "board_id", board.ID)
		printKV(cmd.OutOrStdout(), "task_id", taskID)
		printKV(cmd.OutOrStdout(), "status", status)
		printKV(cmd.OutOrStdout(), "progress", fmt.Sprintf("done:%d/%d pending:%d in_progress:%d blocked:%d", progress.Done, progress.Total, progress.Pending, progress.InProgress, progress.Blocked))
	},
}

var tasksClearCmd = &cobra.Command{
	Use:   "clear",
	Short: "Clear active task board pointer for namespace.",
	Run: func(cmd *cobra.Command, args []string) {
		ns, ok := resolveNamespaceTarget(cmd, tasksNamespace)
		if !ok {
			return
		}
		store, err := taskBoardStoreFactory()
		if err != nil {
			fmt.Fprintf(cmd.OutOrStdout(), "Error loading task board store: %v\n", err)
			return
		}
		store.ClearActiveBoard(ns)
		if err := store.Save(); err != nil {
			fmt.Fprintf(cmd.OutOrStdout(), "Error saving task board store: %v\n", err)
			return
		}
		printCommandStatus(cmd.OutOrStdout(), "talos tasks clear", "success")
		printSection(cmd.OutOrStdout(), "result")
		printKV(cmd.OutOrStdout(), "namespace", ns)
		printKV(cmd.OutOrStdout(), "active_board", "none")
	},
}

func resolveTaskBoard(store *state.TaskBoardStore, namespace, boardID string) (state.TaskBoard, bool) {
	id := strings.TrimSpace(boardID)
	if id != "" {
		return store.Board(id)
	}
	return store.ActiveBoard(namespace)
}

func emptyAsNone(v string) string {
	if strings.TrimSpace(v) == "" {
		return "none"
	}
	return strings.TrimSpace(v)
}

func init() {
	tasksListCmd.Flags().StringVar(&tasksNamespace, "namespace", "", "target namespace (defaults to active namespace)")
	tasksShowCmd.Flags().StringVar(&tasksNamespace, "namespace", "", "target namespace (defaults to active namespace)")
	tasksShowCmd.Flags().StringVar(&tasksBoardID, "board-id", "", "task board ID (defaults to active board)")
	tasksNextCmd.Flags().StringVar(&tasksNamespace, "namespace", "", "target namespace (defaults to active namespace)")
	tasksNextCmd.Flags().StringVar(&tasksBoardID, "board-id", "", "task board ID (defaults to active board)")
	tasksUpdateCmd.Flags().StringVar(&tasksNamespace, "namespace", "", "target namespace (defaults to active namespace)")
	tasksUpdateCmd.Flags().StringVar(&tasksBoardID, "board-id", "", "task board ID (defaults to active board)")
	tasksUpdateCmd.Flags().StringVar(&tasksTaskID, "id", "", "task item ID")
	tasksUpdateCmd.Flags().StringVar(&tasksStatus, "status", "", "new status (pending|in_progress|done|blocked)")
	tasksClearCmd.Flags().StringVar(&tasksNamespace, "namespace", "", "target namespace (defaults to active namespace)")

	tasksCmd.AddCommand(tasksListCmd)
	tasksCmd.AddCommand(tasksShowCmd)
	tasksCmd.AddCommand(tasksNextCmd)
	tasksCmd.AddCommand(tasksUpdateCmd)
	tasksCmd.AddCommand(tasksClearCmd)
	rootCmd.AddCommand(tasksCmd)
}
