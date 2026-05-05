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
	cmd.AddCommand(newTodoUpdateCmd())
	cmd.AddCommand(newTodoCloseCmd())
	cmd.AddCommand(newTodoReopenCmd())
	cmd.AddCommand(newTodoAssignCmd())
	cmd.AddCommand(newTodoUnassignCmd())
	cmd.AddCommand(newTodoCommentCmd())
	cmd.AddCommand(newTodoCommentListCmd())
	cmd.AddCommand(newTodoCommentDeleteCmd())
	cmd.AddCommand(newTodoAttachmentCmd())
	cmd.AddCommand(newTodoDeleteCmd())
	cmd.AddCommand(newTodoGoalCmd())

	return cmd
}

// --- todo list ---

func newTodoListCmd() *cobra.Command {
	var goalID, status, assignee, cursor, creator, search, sourceChannel string
	var sourceType, limit int

	cmd := &cobra.Command{
		Use:   "list",
		Short: "List todos",
		Example: `  octo todo list
  octo todo list --status open
  octo todo list --goal <goal-id> --limit 20
  octo todo list -q "deploy" --creator <uid>
  octo todo list --cursor <next_cursor>`,
		RunE: func(cmd *cobra.Command, args []string) error {
			data, err := apiClient.TodoList(goalID, status, assignee, cursor, creator, search, sourceChannel, sourceType, limit)
			if err != nil {
				return err
			}
			output.Print(cfg.Format, data)
			return nil
		},
	}

	cmd.Flags().StringVar(&goalID, "goal", "", "filter by goal ID")
	cmd.Flags().StringVar(&status, "status", "", "filter by status (open|closed)")
	cmd.Flags().StringVar(&assignee, "assignee", "", "filter by assignee user ID")
	cmd.Flags().StringVar(&cursor, "cursor", "", "pagination cursor (from previous response)")
	cmd.Flags().IntVar(&limit, "limit", 0, "max number of results")
	cmd.Flags().StringVar(&creator, "creator", "", "filter by creator user ID")
	cmd.Flags().StringVarP(&search, "search", "q", "", "full-text search query")
	cmd.Flags().StringVar(&sourceChannel, "source-channel", "", "filter by source channel ID")
	cmd.Flags().IntVar(&sourceType, "source-type", 0, "filter by source channel type")

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
	var title, desc, goalID, deadline, sourceChannelID, remindAt, sourceName string
	var sourceChannelType int
	var assignees []string

	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create a new todo",
		Example: `  octo todo create --title "Deploy v2.0"
  octo todo create --title "Fix bug" --goal <goal-id> --assignee user-1 --assignee user-2
  octo todo create --title "From chat" --source-channel <channel-id> --source-type 2`,
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
			if sourceChannelID != "" {
				req["source_channel_id"] = sourceChannelID
			}
			if sourceChannelType > 0 {
				req["source_channel_type"] = sourceChannelType
			}
			if remindAt != "" {
				req["remind_at"] = remindAt
			}
			if sourceName != "" {
				req["source_name"] = sourceName
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
	cmd.Flags().StringVar(&sourceChannelID, "source-channel", "", "source channel ID (for chat context)")
	cmd.Flags().IntVar(&sourceChannelType, "source-type", 0, "source channel type (2=group, 5=thread)")
	cmd.Flags().StringVar(&remindAt, "remind-at", "", "reminder time (RFC3339 format)")
	cmd.Flags().StringVar(&sourceName, "source-name", "", "source name")
	_ = cmd.MarkFlagRequired("title")

	return cmd
}

// --- todo update ---

func newTodoUpdateCmd() *cobra.Command {
	var title, desc, deadline, remindAt, goalID string

	cmd := &cobra.Command{
		Use:   "update <todo-id>",
		Short: "Update a todo",
		Example: `  octo todo update <id> --title "New title"
  octo todo update <id> --desc "Updated description" --deadline 2026-06-01T00:00:00Z
  octo todo update <id> --goal <goal-id> --remind-at 2026-06-01T09:00:00Z`,
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			req := map[string]any{}
			if title != "" {
				req["title"] = title
			}
			if desc != "" {
				req["description"] = desc
			}
			if deadline != "" {
				req["deadline"] = deadline
			}
			if remindAt != "" {
				req["remind_at"] = remindAt
			}
			if goalID != "" {
				req["goal_id"] = goalID
			}
			if len(req) == 0 {
				return fmt.Errorf("at least one flag is required")
			}
			data, err := apiClient.TodoUpdate(args[0], req)
			if err != nil {
				return err
			}
			output.Print(cfg.Format, data)
			return nil
		},
	}

	cmd.Flags().StringVar(&title, "title", "", "new title")
	cmd.Flags().StringVar(&desc, "desc", "", "new description")
	cmd.Flags().StringVar(&deadline, "deadline", "", "new deadline (RFC3339)")
	cmd.Flags().StringVar(&remindAt, "remind-at", "", "reminder time (RFC3339)")
	cmd.Flags().StringVar(&goalID, "goal", "", "reassign to a different goal ID")

	return cmd
}

// --- todo close ---

func newTodoCloseCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "close <todo-id>",
		Short: "Close a todo",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			data, err := apiClient.TodoTransition(args[0], "closed")
			if err != nil {
				return err
			}
			output.Print(cfg.Format, data)
			return nil
		},
	}
}

// --- todo reopen ---

func newTodoReopenCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "reopen <todo-id>",
		Short: "Reopen a closed todo",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			data, err := apiClient.TodoTransition(args[0], "open")
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
			output.Print(cfg.Format, []byte(`{"status":"deleted"}`))
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
	cmd.AddCommand(newGoalUpdateCmd())
	cmd.AddCommand(newGoalAssignCmd())
	cmd.AddCommand(newGoalUnassignCmd())
	cmd.AddCommand(newGoalArchiveCmd())

	return cmd
}

func newGoalListCmd() *cobra.Command {
	var status string

	cmd := &cobra.Command{
		Use:   "list",
		Short: "List goals",
		RunE: func(cmd *cobra.Command, args []string) error {
			data, err := apiClient.GoalList(status)
			if err != nil {
				return err
			}
			output.Print(cfg.Format, data)
			return nil
		},
	}

	cmd.Flags().StringVar(&status, "status", "", "filter by status (active, completed, archived)")
	return cmd
}

func newGoalGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "get <goal-id>",
		Short: "Get goal detail",
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
	var title, desc, deadline string
	var assignees []string

	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create a new goal",
		RunE: func(cmd *cobra.Command, args []string) error {
			req := map[string]any{"title": title}
			if desc != "" {
				req["description"] = desc
			}
			if deadline != "" {
				req["deadline"] = deadline
			}
			if len(assignees) > 0 {
				req["assignee_ids"] = assignees
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
	cmd.Flags().StringVar(&deadline, "deadline", "", "deadline (RFC3339 format)")
	cmd.Flags().StringSliceVar(&assignees, "assignee", nil, "assignee user IDs (repeatable)")
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
			output.Print(cfg.Format, []byte(`{"status":"archived"}`))
			return nil
		},
	}
}


// --- todo comment list ---

func newTodoCommentListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "comments <todo-id>",
		Short: "List comments on a todo",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			data, err := apiClient.TodoListComments(args[0])
			if err != nil {
				return err
			}
			output.Print(cfg.Format, data)
			return nil
		},
	}
}

// --- todo comment delete ---

func newTodoCommentDeleteCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "comment-delete <todo-id> <comment-id>",
		Short: "Delete a comment from a todo",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := apiClient.TodoDeleteComment(args[0], args[1]); err != nil {
				return err
			}
			output.Print(cfg.Format, []byte(`{"status":"deleted"}`))
			return nil
		},
	}
}

// --- todo attachment (subcommand group) ---

func newTodoAttachmentCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "attachment",
		Short: "Manage todo attachments",
	}
	cmd.AddCommand(newAttachmentListCmd())
	cmd.AddCommand(newAttachmentAddCmd())
	cmd.AddCommand(newAttachmentDeleteCmd())
	return cmd
}

func newAttachmentListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list <todo-id>",
		Short: "List attachments on a todo",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			data, err := apiClient.TodoListAttachments(args[0])
			if err != nil {
				return err
			}
			output.Print(cfg.Format, data)
			return nil
		},
	}
}

func newAttachmentAddCmd() *cobra.Command {
	var fileURL, fileName, mimeType string

	cmd := &cobra.Command{
		Use:   "add <todo-id>",
		Short: "Add an attachment to a todo",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			req := map[string]any{"file_url": fileURL}
			if fileName != "" {
				req["file_name"] = fileName
			}
			if mimeType != "" {
				req["mime_type"] = mimeType
			}
			data, err := apiClient.TodoAddAttachment(args[0], req)
			if err != nil {
				return err
			}
			output.Print(cfg.Format, data)
			return nil
		},
	}

	cmd.Flags().StringVar(&fileURL, "url", "", "attachment URL (required, must be https)")
	cmd.Flags().StringVar(&fileName, "name", "", "file name")
	cmd.Flags().StringVar(&mimeType, "type", "", "MIME type")
	_ = cmd.MarkFlagRequired("url")

	return cmd
}

func newAttachmentDeleteCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "delete <todo-id> <attachment-id>",
		Short: "Delete an attachment from a todo",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := apiClient.TodoDeleteAttachment(args[0], args[1]); err != nil {
				return err
			}
			output.Print(cfg.Format, []byte(`{"status":"deleted"}`))
			return nil
		},
	}
}

// --- goal update ---

func newGoalUpdateCmd() *cobra.Command {
	var title, desc, deadline string

	cmd := &cobra.Command{
		Use:   "update <goal-id>",
		Short: "Update a goal",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			req := map[string]any{}
			if title != "" {
				req["title"] = title
			}
			if desc != "" {
				req["description"] = desc
			}
			if deadline != "" {
				req["deadline"] = deadline
			}
			if len(req) == 0 {
				return fmt.Errorf("at least one of --title, --desc, or --deadline is required")
			}
			data, err := apiClient.GoalUpdate(args[0], req)
			if err != nil {
				return err
			}
			output.Print(cfg.Format, data)
			return nil
		},
	}

	cmd.Flags().StringVar(&title, "title", "", "new title")
	cmd.Flags().StringVar(&desc, "desc", "", "new description")
	cmd.Flags().StringVar(&deadline, "deadline", "", "new deadline (RFC3339)")

	return cmd
}

// --- goal assign ---

func newGoalAssignCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "assign <goal-id> <user-id>",
		Short: "Add an assignee to a goal",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			data, err := apiClient.GoalAddAssignee(args[0], args[1])
			if err != nil {
				return err
			}
			output.Print(cfg.Format, data)
			return nil
		},
	}
}

// --- goal unassign ---

func newGoalUnassignCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "unassign <goal-id> <user-id>",
		Short: "Remove an assignee from a goal",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := apiClient.GoalRemoveAssignee(args[0], args[1]); err != nil {
				return err
			}
			output.Print(cfg.Format, []byte(`{"status":"removed"}`))
			return nil
		},
	}
}
