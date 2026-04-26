package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/dmwork-org/octo-cli/internal/output"
)

func newTodoCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "todo",
		Short: "Manage todos and goals",
	}

	cmd.AddCommand(newTodoListCmd())
	cmd.AddCommand(newTodoGetCmd())
	cmd.AddCommand(newTodoCreateCmd())
	cmd.AddCommand(newTodoDoneCmd())
	cmd.AddCommand(newTodoMoveCmd())
	cmd.AddCommand(newTodoAssignCmd())
	cmd.AddCommand(newTodoUnassignCmd())
	cmd.AddCommand(newTodoCommentCmd())
	cmd.AddCommand(newTodoDeleteCmd())
	cmd.AddCommand(newTodoGoalCmd())

	return cmd
}

// --- todo list ---

func newTodoListCmd() *cobra.Command {
	var goalID, status, assignee string
	var limit int

	cmd := &cobra.Command{
		Use:   "list",
		Short: "List todos",
		Example: `  octo todo list
  octo todo list --status in_progress
  octo todo list --goal <goal-id> --limit 20`,
		RunE: func(cmd *cobra.Command, args []string) error {
			data, err := apiClient.TodoList(goalID, status, assignee, limit)
			if err != nil {
				return err
			}
			output.Print(cfg.Format, data)
			return nil
		},
	}

	cmd.Flags().StringVar(&goalID, "goal", "", "filter by goal ID")
	cmd.Flags().StringVar(&status, "status", "", "filter by status (draft|planned|in_progress|done|cancelled)")
	cmd.Flags().StringVar(&assignee, "assignee", "", "filter by assignee user ID")
	cmd.Flags().IntVar(&limit, "limit", 0, "max number of results")

	return cmd
}

// --- todo get ---

func newTodoGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "get <todo-id>",
		Short: "Get todo detail",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			data, err := apiClient.TodoGet(args[0])
			if err != nil {
				return err
			}
			output.Print(cfg.Format, data)
			return nil
		},
	}
}

// --- todo create ---

func newTodoCreateCmd() *cobra.Command {
	var title, desc, goalID, deadline string
	var assignees []string

	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create a new todo",
		Example: `  octo todo create --title "Deploy v2.0"
  octo todo create --title "Fix bug" --goal <goal-id> --assignee user-1 --assignee user-2`,
		RunE: func(cmd *cobra.Command, args []string) error {
			req := map[string]any{"title": title}
			if desc != "" {
				req["description"] = desc
			}
			if goalID != "" {
				req["goal_id"] = goalID
			}
			if deadline != "" {
				req["deadline"] = deadline
			}
			if len(assignees) > 0 {
				req["assignee_ids"] = assignees
			}
			data, err := apiClient.TodoCreate(req)
			if err != nil {
				return err
			}
			output.Print(cfg.Format, data)
			return nil
		},
	}

	cmd.Flags().StringVar(&title, "title", "", "todo title (required)")
	cmd.Flags().StringVar(&desc, "desc", "", "description")
	cmd.Flags().StringVar(&goalID, "goal", "", "parent goal ID")
	cmd.Flags().StringVar(&deadline, "deadline", "", "deadline (RFC3339 format)")
	cmd.Flags().StringSliceVar(&assignees, "assignee", nil, "assignee user IDs (repeatable)")
	_ = cmd.MarkFlagRequired("title")

	return cmd
}

// --- todo done ---

func newTodoDoneCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "done <todo-id>",
		Short: "Mark a todo as done",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			data, err := apiClient.TodoTransition(args[0], "done")
			if err != nil {
				return err
			}
			output.Print(cfg.Format, data)
			return nil
		},
	}
}

// --- todo move ---

func newTodoMoveCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "move <todo-id> <status>",
		Short: "Transition todo to a new status",
		Long:  "Valid statuses: draft, planned, in_progress, done, cancelled",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			data, err := apiClient.TodoTransition(args[0], args[1])
			if err != nil {
				return err
			}
			output.Print(cfg.Format, data)
			return nil
		},
	}
}

// --- todo assign ---

func newTodoAssignCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "assign <todo-id> <user-id>",
		Short: "Add an assignee to a todo",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			data, err := apiClient.TodoAddAssignee(args[0], args[1])
			if err != nil {
				return err
			}
			output.Print(cfg.Format, data)
			return nil
		},
	}
}

// --- todo unassign ---

func newTodoUnassignCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "unassign <todo-id> <user-id>",
		Short: "Remove an assignee from a todo",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return apiClient.TodoRemoveAssignee(args[0], args[1])
		},
	}
}

// --- todo comment ---

func newTodoCommentCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "comment <todo-id> <text>",
		Short: "Add a comment to a todo",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			data, err := apiClient.TodoComment(args[0], args[1])
			if err != nil {
				return err
			}
			output.Print(cfg.Format, data)
			return nil
		},
	}
}

// --- todo delete ---

func newTodoDeleteCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "delete <todo-id>",
		Short: "Delete a todo",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := apiClient.TodoDelete(args[0]); err != nil {
				return err
			}
			fmt.Println(`{"status":"deleted"}`)
			return nil
		},
	}
}

// --- todo goal (subcommand group) ---

func newTodoGoalCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "goal",
		Short: "Manage goals",
	}

	cmd.AddCommand(newGoalListCmd())
	cmd.AddCommand(newGoalGetCmd())
	cmd.AddCommand(newGoalCreateCmd())
	cmd.AddCommand(newGoalArchiveCmd())

	return cmd
}

func newGoalListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List goals",
		RunE: func(cmd *cobra.Command, args []string) error {
			data, err := apiClient.GoalList()
			if err != nil {
				return err
			}
			output.Print(cfg.Format, data)
			return nil
		},
	}
}

func newGoalGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "get <goal-id>",
		Short: "Get goal detail (kanban view)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			data, err := apiClient.GoalGet(args[0])
			if err != nil {
				return err
			}
			output.Print(cfg.Format, data)
			return nil
		},
	}
}

func newGoalCreateCmd() *cobra.Command {
	var title, desc string

	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create a new goal",
		RunE: func(cmd *cobra.Command, args []string) error {
			req := map[string]any{"title": title}
			if desc != "" {
				req["description"] = desc
			}
			data, err := apiClient.GoalCreate(req)
			if err != nil {
				return err
			}
			output.Print(cfg.Format, data)
			return nil
		},
	}

	cmd.Flags().StringVar(&title, "title", "", "goal title (required)")
	cmd.Flags().StringVar(&desc, "desc", "", "description")
	_ = cmd.MarkFlagRequired("title")

	return cmd
}

func newGoalArchiveCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "archive <goal-id>",
		Short: "Archive a goal",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := apiClient.GoalArchive(args[0]); err != nil {
				return err
			}
			fmt.Println(`{"status":"archived"}`)
			return nil
		},
	}
}
