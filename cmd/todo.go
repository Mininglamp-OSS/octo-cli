package cmd

import (
	"context"
	"fmt"
	"net/url"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/dmwork-org/octo-cli/internal/client"
	"github.com/dmwork-org/octo-cli/internal/cmdutil"
	"github.com/dmwork-org/octo-cli/internal/output"
)

// todoService is the service name passed to the client. Phase 1 keeps the
// current "todos" routes; Phase 2b migrates to "matters" per the backend.
const todoService = ""

// todoBase is the path prefix for the todo-service REST routes.
const todoBase = "/api/v1"

// --- todo root + subcommand tree ---

func newTodoCmd(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "todo",
		Short: "Manage todos and goals",
	}

	cmd.AddCommand(newTodoListCmd(f))
	cmd.AddCommand(newTodoGetCmd(f))
	cmd.AddCommand(newTodoCreateCmd(f))
	cmd.AddCommand(newTodoUpdateCmd(f))
	cmd.AddCommand(newTodoCloseCmd(f))
	cmd.AddCommand(newTodoReopenCmd(f))
	cmd.AddCommand(newTodoDeleteCmd(f))
	cmd.AddCommand(newTodoAssignCmd(f))
	cmd.AddCommand(newTodoUnassignCmd(f))
	cmd.AddCommand(newTodoCommentCmd(f))
	cmd.AddCommand(newTodoCommentListCmd(f))
	cmd.AddCommand(newTodoCommentDeleteCmd(f))
	cmd.AddCommand(newTodoAttachmentCmd(f))
	cmd.AddCommand(newTodoGoalCmd(f))

	return cmd
}

// emit runs req against the API and emits either the success envelope or an
// error envelope to stderr. Returns the original error so cobra sets a
// non-zero exit code.
func emit(ctx context.Context, f *cmdutil.Factory, req client.Request) error {
	cli, err := f.Client()
	if err != nil {
		_ = f.EmitError(err)
		return err
	}
	body, err := cli.Do(ctx, req)
	if err != nil {
		_ = f.EmitError(err)
		return err
	}
	return f.EmitSuccess(body)
}

// emitOK emits a synthetic success body for operations that return no content
// (e.g. DELETE 204). The envelope carries the provided status string.
func emitOK(f *cmdutil.Factory, status string) error {
	return f.EmitSuccess([]byte(fmt.Sprintf(`{"status":%q}`, status)))
}

// requireYes returns a confirmation_required ExitError when Globals.Yes is
// false. Emits and returns so the caller can bail out.
func requireYes(f *cmdutil.Factory, action string) error {
	if f.Globals != nil && f.Globals.Yes {
		return nil
	}
	ee := output.ErrConfirmationRequired(fmt.Sprintf("%s requires --yes", action))
	_ = f.EmitError(ee)
	return ee
}

// --- todo list ---

func newTodoListCmd(f *cmdutil.Factory) *cobra.Command {
	var goalID, status, assignee, cursor, creator, search, sourceChannel string
	var sourceType, limit int

	cmd := &cobra.Command{
		Use:   "list",
		Short: "List todos",
		RunE: func(cmd *cobra.Command, args []string) error {
			q := url.Values{}
			if goalID != "" {
				q.Set("goal_id", goalID)
			}
			if status != "" {
				q.Set("status", status)
			}
			if assignee != "" {
				q.Set("assignee_id", assignee)
			}
			if cursor != "" {
				q.Set("cursor", cursor)
			}
			if creator != "" {
				q.Set("creator_id", creator)
			}
			if search != "" {
				q.Set("q", search)
			}
			if sourceChannel != "" {
				q.Set("source_channel_id", sourceChannel)
			}
			if sourceType > 0 {
				q.Set("source_channel_type", strconv.Itoa(sourceType))
			}
			if limit > 0 {
				q.Set("limit", strconv.Itoa(limit))
			}
			return emit(cmd.Context(), f, client.Request{
				Service: todoService,
				Method:  "GET",
				Path:    todoBase + "/todos",
				Query:   q,
			})
		},
	}

	cmd.Flags().StringVar(&goalID, "goal", "", "filter by goal ID")
	cmd.Flags().StringVar(&status, "status", "", "filter by status (open|closed)")
	cmd.Flags().StringVar(&assignee, "assignee", "", "filter by assignee user ID")
	cmd.Flags().StringVar(&cursor, "cursor", "", "pagination cursor (from previous response)")
	cmd.Flags().IntVar(&limit, "limit", 0, "max number of results")
	cmd.Flags().StringVar(&creator, "creator", "", "filter by creator user ID")
	cmd.Flags().StringVar(&search, "search", "", "full-text search query")
	cmd.Flags().StringVar(&sourceChannel, "source-channel", "", "filter by source channel ID")
	cmd.Flags().IntVar(&sourceType, "source-type", 0, "filter by source channel type")
	return cmd
}

// --- todo get ---

func newTodoGetCmd(f *cmdutil.Factory) *cobra.Command {
	return &cobra.Command{
		Use:   "get <todo-id>",
		Short: "Get todo detail",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return emit(cmd.Context(), f, client.Request{
				Method: "GET",
				Path:   todoBase + "/todos/" + url.PathEscape(args[0]),
			})
		},
	}
}

// --- todo create ---

func newTodoCreateCmd(f *cmdutil.Factory) *cobra.Command {
	var title, desc, goalID, deadline, sourceChannelID, remindAt, sourceName string
	var sourceChannelType int
	var assignees []string

	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create a new todo",
		RunE: func(cmd *cobra.Command, args []string) error {
			body := map[string]any{"title": title}
			if desc != "" {
				body["description"] = desc
			}
			if goalID != "" {
				body["goal_id"] = goalID
			}
			if deadline != "" {
				body["deadline"] = deadline
			}
			if len(assignees) > 0 {
				body["assignee_ids"] = assignees
			}
			if sourceChannelID != "" {
				body["source_channel_id"] = sourceChannelID
			}
			if sourceChannelType > 0 {
				body["source_channel_type"] = sourceChannelType
			}
			if remindAt != "" {
				body["remind_at"] = remindAt
			}
			if sourceName != "" {
				body["source_name"] = sourceName
			}
			return emit(cmd.Context(), f, client.Request{
				Method: "POST",
				Path:   todoBase + "/todos",
				Body:   body,
			})
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

func newTodoUpdateCmd(f *cmdutil.Factory) *cobra.Command {
	var title, desc, deadline, remindAt, goalID string

	cmd := &cobra.Command{
		Use:   "update <todo-id>",
		Short: "Update a todo",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			body := map[string]any{}
			if title != "" {
				body["title"] = title
			}
			if desc != "" {
				body["description"] = desc
			}
			if deadline != "" {
				body["deadline"] = deadline
			}
			if remindAt != "" {
				body["remind_at"] = remindAt
			}
			if goalID != "" {
				body["goal_id"] = goalID
			}
			if len(body) == 0 {
				ee := output.ErrValidation("at least one flag is required", "pass --title, --desc, --deadline, --remind-at, or --goal")
				_ = f.EmitError(ee)
				return ee
			}
			return emit(cmd.Context(), f, client.Request{
				Method: "PUT",
				Path:   todoBase + "/todos/" + url.PathEscape(args[0]),
				Body:   body,
			})
		},
	}

	cmd.Flags().StringVar(&title, "title", "", "new title")
	cmd.Flags().StringVar(&desc, "desc", "", "new description")
	cmd.Flags().StringVar(&deadline, "deadline", "", "new deadline (RFC3339)")
	cmd.Flags().StringVar(&remindAt, "remind-at", "", "reminder time (RFC3339)")
	cmd.Flags().StringVar(&goalID, "goal", "", "reassign to a different goal ID")
	return cmd
}

// --- status transitions ---

func newTodoCloseCmd(f *cmdutil.Factory) *cobra.Command {
	return statusTransitionCmd(f, "close", "Close a todo", "closed")
}

func newTodoReopenCmd(f *cmdutil.Factory) *cobra.Command {
	return statusTransitionCmd(f, "reopen", "Reopen a closed todo", "open")
}

func statusTransitionCmd(f *cmdutil.Factory, name, short, target string) *cobra.Command {
	return &cobra.Command{
		Use:   name + " <todo-id>",
		Short: short,
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return emit(cmd.Context(), f, client.Request{
				Method: "PUT",
				Path:   todoBase + "/todos/" + url.PathEscape(args[0]) + "/status",
				Body:   map[string]string{"status": target},
			})
		},
	}
}

// --- todo delete ---

func newTodoDeleteCmd(f *cmdutil.Factory) *cobra.Command {
	return &cobra.Command{
		Use:   "delete <todo-id>",
		Short: "Delete a todo (requires --yes)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := requireYes(f, "delete"); err != nil {
				return err
			}
			cli, err := f.Client()
			if err != nil {
				_ = f.EmitError(err)
				return err
			}
			if _, err := cli.Do(cmd.Context(), client.Request{
				Method: "DELETE",
				Path:   todoBase + "/todos/" + url.PathEscape(args[0]),
			}); err != nil {
				_ = f.EmitError(err)
				return err
			}
			return emitOK(f, "deleted")
		},
	}
}

// --- assignees ---

func newTodoAssignCmd(f *cmdutil.Factory) *cobra.Command {
	return &cobra.Command{
		Use:   "assign <todo-id> <user-id>",
		Short: "Add an assignee to a todo",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return emit(cmd.Context(), f, client.Request{
				Method: "POST",
				Path:   todoBase + "/todos/" + url.PathEscape(args[0]) + "/assignees",
				Body:   map[string]string{"user_id": args[1]},
			})
		},
	}
}

func newTodoUnassignCmd(f *cmdutil.Factory) *cobra.Command {
	return &cobra.Command{
		Use:   "unassign <todo-id> <user-id>",
		Short: "Remove an assignee from a todo",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			cli, err := f.Client()
			if err != nil {
				_ = f.EmitError(err)
				return err
			}
			if _, err := cli.Do(cmd.Context(), client.Request{
				Method: "DELETE",
				Path:   todoBase + "/todos/" + url.PathEscape(args[0]) + "/assignees/" + url.PathEscape(args[1]),
			}); err != nil {
				_ = f.EmitError(err)
				return err
			}
			return emitOK(f, "removed")
		},
	}
}

// --- comments ---

func newTodoCommentCmd(f *cmdutil.Factory) *cobra.Command {
	return &cobra.Command{
		Use:   "comment <todo-id> <text>",
		Short: "Add a comment to a todo",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return emit(cmd.Context(), f, client.Request{
				Method: "POST",
				Path:   todoBase + "/todos/" + url.PathEscape(args[0]) + "/comments",
				Body:   map[string]string{"content": args[1]},
			})
		},
	}
}

func newTodoCommentListCmd(f *cmdutil.Factory) *cobra.Command {
	return &cobra.Command{
		Use:   "comments <todo-id>",
		Short: "List comments on a todo",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return emit(cmd.Context(), f, client.Request{
				Method: "GET",
				Path:   todoBase + "/todos/" + url.PathEscape(args[0]) + "/comments",
			})
		},
	}
}

func newTodoCommentDeleteCmd(f *cmdutil.Factory) *cobra.Command {
	return &cobra.Command{
		Use:   "comment-delete <todo-id> <comment-id>",
		Short: "Delete a comment from a todo (requires --yes)",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := requireYes(f, "comment-delete"); err != nil {
				return err
			}
			cli, err := f.Client()
			if err != nil {
				_ = f.EmitError(err)
				return err
			}
			if _, err := cli.Do(cmd.Context(), client.Request{
				Method: "DELETE",
				Path:   todoBase + "/todos/" + url.PathEscape(args[0]) + "/comments/" + url.PathEscape(args[1]),
			}); err != nil {
				_ = f.EmitError(err)
				return err
			}
			return emitOK(f, "deleted")
		},
	}
}

// --- attachments ---

func newTodoAttachmentCmd(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "attachment",
		Short: "Manage todo attachments",
	}
	cmd.AddCommand(newAttachmentListCmd(f))
	cmd.AddCommand(newAttachmentAddCmd(f))
	cmd.AddCommand(newAttachmentDeleteCmd(f))
	return cmd
}

func newAttachmentListCmd(f *cmdutil.Factory) *cobra.Command {
	return &cobra.Command{
		Use:   "list <todo-id>",
		Short: "List attachments on a todo",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return emit(cmd.Context(), f, client.Request{
				Method: "GET",
				Path:   todoBase + "/todos/" + url.PathEscape(args[0]) + "/attachments",
			})
		},
	}
}

func newAttachmentAddCmd(f *cmdutil.Factory) *cobra.Command {
	var fileURL, fileName, mimeType string

	cmd := &cobra.Command{
		Use:   "add <todo-id>",
		Short: "Add an attachment to a todo",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			body := map[string]any{"file_url": fileURL}
			if fileName != "" {
				body["file_name"] = fileName
			}
			if mimeType != "" {
				body["mime_type"] = mimeType
			}
			return emit(cmd.Context(), f, client.Request{
				Method: "POST",
				Path:   todoBase + "/todos/" + url.PathEscape(args[0]) + "/attachments",
				Body:   body,
			})
		},
	}

	cmd.Flags().StringVar(&fileURL, "url", "", "attachment URL (required, must be https)")
	cmd.Flags().StringVar(&fileName, "name", "", "file name")
	cmd.Flags().StringVar(&mimeType, "type", "", "MIME type")
	_ = cmd.MarkFlagRequired("url")
	return cmd
}

func newAttachmentDeleteCmd(f *cmdutil.Factory) *cobra.Command {
	return &cobra.Command{
		Use:   "delete <todo-id> <attachment-id>",
		Short: "Delete an attachment from a todo (requires --yes)",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := requireYes(f, "attachment delete"); err != nil {
				return err
			}
			cli, err := f.Client()
			if err != nil {
				_ = f.EmitError(err)
				return err
			}
			if _, err := cli.Do(cmd.Context(), client.Request{
				Method: "DELETE",
				Path:   todoBase + "/todos/" + url.PathEscape(args[0]) + "/attachments/" + url.PathEscape(args[1]),
			}); err != nil {
				_ = f.EmitError(err)
				return err
			}
			return emitOK(f, "deleted")
		},
	}
}

// --- goal subcommands ---

func newTodoGoalCmd(f *cmdutil.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "goal",
		Short: "Manage goals",
	}
	cmd.AddCommand(newGoalListCmd(f))
	cmd.AddCommand(newGoalGetCmd(f))
	cmd.AddCommand(newGoalCreateCmd(f))
	cmd.AddCommand(newGoalUpdateCmd(f))
	cmd.AddCommand(newGoalArchiveCmd(f))
	cmd.AddCommand(newGoalAssignCmd(f))
	cmd.AddCommand(newGoalUnassignCmd(f))
	return cmd
}

func newGoalListCmd(f *cmdutil.Factory) *cobra.Command {
	var status string
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List goals",
		RunE: func(cmd *cobra.Command, args []string) error {
			q := url.Values{}
			if status != "" {
				q.Set("status", status)
			}
			return emit(cmd.Context(), f, client.Request{
				Method: "GET",
				Path:   todoBase + "/goals",
				Query:  q,
			})
		},
	}
	cmd.Flags().StringVar(&status, "status", "", "filter by status (active, completed, archived)")
	return cmd
}

func newGoalGetCmd(f *cmdutil.Factory) *cobra.Command {
	return &cobra.Command{
		Use:   "get <goal-id>",
		Short: "Get goal detail",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return emit(cmd.Context(), f, client.Request{
				Method: "GET",
				Path:   todoBase + "/goals/" + url.PathEscape(args[0]),
			})
		},
	}
}

func newGoalCreateCmd(f *cmdutil.Factory) *cobra.Command {
	var title, desc, deadline string
	var assignees []string

	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create a new goal",
		RunE: func(cmd *cobra.Command, args []string) error {
			body := map[string]any{"title": title}
			if desc != "" {
				body["description"] = desc
			}
			if deadline != "" {
				body["deadline"] = deadline
			}
			if len(assignees) > 0 {
				body["assignee_ids"] = assignees
			}
			return emit(cmd.Context(), f, client.Request{
				Method: "POST",
				Path:   todoBase + "/goals",
				Body:   body,
			})
		},
	}

	cmd.Flags().StringVar(&title, "title", "", "goal title (required)")
	cmd.Flags().StringVar(&desc, "desc", "", "description")
	cmd.Flags().StringVar(&deadline, "deadline", "", "deadline (RFC3339 format)")
	cmd.Flags().StringSliceVar(&assignees, "assignee", nil, "assignee user IDs (repeatable)")
	_ = cmd.MarkFlagRequired("title")
	return cmd
}

func newGoalUpdateCmd(f *cmdutil.Factory) *cobra.Command {
	var title, desc, deadline string

	cmd := &cobra.Command{
		Use:   "update <goal-id>",
		Short: "Update a goal",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			body := map[string]any{}
			if title != "" {
				body["title"] = title
			}
			if desc != "" {
				body["description"] = desc
			}
			if deadline != "" {
				body["deadline"] = deadline
			}
			if len(body) == 0 {
				ee := output.ErrValidation("at least one of --title, --desc, or --deadline is required", "")
				_ = f.EmitError(ee)
				return ee
			}
			return emit(cmd.Context(), f, client.Request{
				Method: "PUT",
				Path:   todoBase + "/goals/" + url.PathEscape(args[0]),
				Body:   body,
			})
		},
	}

	cmd.Flags().StringVar(&title, "title", "", "new title")
	cmd.Flags().StringVar(&desc, "desc", "", "new description")
	cmd.Flags().StringVar(&deadline, "deadline", "", "new deadline (RFC3339)")
	return cmd
}

func newGoalArchiveCmd(f *cmdutil.Factory) *cobra.Command {
	return &cobra.Command{
		Use:   "archive <goal-id>",
		Short: "Archive a goal (requires --yes)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := requireYes(f, "archive"); err != nil {
				return err
			}
			cli, err := f.Client()
			if err != nil {
				_ = f.EmitError(err)
				return err
			}
			if _, err := cli.Do(cmd.Context(), client.Request{
				Method: "DELETE",
				Path:   todoBase + "/goals/" + url.PathEscape(args[0]),
			}); err != nil {
				_ = f.EmitError(err)
				return err
			}
			return emitOK(f, "archived")
		},
	}
}

func newGoalAssignCmd(f *cmdutil.Factory) *cobra.Command {
	return &cobra.Command{
		Use:   "assign <goal-id> <user-id>",
		Short: "Add an assignee to a goal",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return emit(cmd.Context(), f, client.Request{
				Method: "POST",
				Path:   todoBase + "/goals/" + url.PathEscape(args[0]) + "/assignees",
				Body:   map[string]string{"user_id": args[1]},
			})
		},
	}
}

func newGoalUnassignCmd(f *cmdutil.Factory) *cobra.Command {
	return &cobra.Command{
		Use:   "unassign <goal-id> <user-id>",
		Short: "Remove an assignee from a goal",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			cli, err := f.Client()
			if err != nil {
				_ = f.EmitError(err)
				return err
			}
			if _, err := cli.Do(cmd.Context(), client.Request{
				Method: "DELETE",
				Path:   todoBase + "/goals/" + url.PathEscape(args[0]) + "/assignees/" + url.PathEscape(args[1]),
			}); err != nil {
				_ = f.EmitError(err)
				return err
			}
			return emitOK(f, "removed")
		},
	}
}
