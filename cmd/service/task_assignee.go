package service

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Mininglamp-OSS/octo-cli/internal/client"
	"github.com/Mininglamp-OSS/octo-cli/internal/cmdutil"
	"github.com/Mininglamp-OSS/octo-cli/internal/output"
	"github.com/Mininglamp-OSS/octo-cli/internal/registry"
)

// Assignment is a CLI convenience over task.update, not a separate Fleet API.
func attachTaskAssignAlias(loop *cobra.Command, f *cmdutil.Factory) {
	tasks := findChild(loop, "task")
	update, ok := f.Registry().GetOperation("task.update")
	if tasks == nil || !ok {
		return
	}
	alias := *update
	alias.ID = "task.assign"
	alias.Summary = "Assign or unassign a task"
	alias.Description = "Alias for task.update. Use --to <name>, --to-id <id>, or --unassign. The CLI resolves members, experts and expert teams in the selected workspace."
	body := *update.RequestBody
	body.Properties = map[string]registry.SchemaInfo{}
	for _, name := range []string{"assignee_type", "assignee_id", "assignee_octo_uid", "suppress_run", "handoff_note"} {
		body.Properties[name] = update.RequestBody.Properties[name]
	}
	alias.RequestBody = &body
	command := buildOperationCmd(f, &alias, "assign")
	for _, name := range []string{"assignee-type", "assignee-id", "assignee-octo-uid"} {
		command.Flags().Lookup(name).Hidden = true
	}
	tasks.AddCommand(command)
}

func registerTaskAssigneeFlags(cmd *cobra.Command, d *registry.OperationDetail) {
	if d.Service != "loop" {
		return
	}
	switch d.ID {
	case "task.assign":
		cmd.Flags().String("to", "", "Assignee name; type is inferred across members, experts and expert teams")
		cmd.Flags().String("to-id", "", "Exact member Octo UID or Fleet actor UUID; type is inferred")
		cmd.Flags().Bool("unassign", false, "Remove the current assignee")
	case "task.create", "task.update", "task.list":
		cmd.Flags().String("assignee", "", "Assignee name; mutually exclusive with explicit assignee selectors")
		flag := cmd.Flags().Lookup("assignee-id")
		if flag != nil {
			flag.Usage += " When type is omitted, resolve the exact ID across workspace members, experts and expert teams."
		}
	}
}

func applyTaskAssigneeBody(cmd *cobra.Command, f *cmdutil.Factory, rt *operationRuntime, body map[string]any) error {
	if rt.detail.Service != "loop" {
		return nil
	}
	var input string
	var byID, resolve bool
	var err error
	switch rt.detail.ID {
	case "task.assign":
		input, byID, resolve, err = taskAssignSelector(cmd, body)
	case "task.create", "task.update":
		input, byID, resolve, err = taskBodyAssigneeSelector(cmd, body)
	default:
		return nil
	}
	if err != nil || !resolve {
		return err
	}
	headers, verr := buildHeaders(cmd, rt)
	if verr != nil {
		return verr
	}
	actor, err := resolveTaskAssignee(cmd, f, headers, input, byID)
	if err != nil {
		return err
	}
	delete(body, "assignee_id")
	body["assignee_type"] = actor.kind
	if actor.uid != "" {
		body["assignee_octo_uid"] = actor.uid
	} else {
		body["assignee_id"] = actor.id
	}
	return nil
}

func taskAssignSelector(cmd *cobra.Command, body map[string]any) (input string, byID, resolve bool, err error) {
	to, toID := cmd.Flags().Changed("to"), cmd.Flags().Changed("to-id")
	unassign := cmd.Flags().Lookup("unassign").Value.String() == "true"
	count := 0
	for _, selected := range []bool{to, toID, unassign} {
		if selected {
			count++
		}
	}
	if count != 1 || hasAssignmentFields(body) {
		return "", false, false, assignmentError("provide exactly one of --to, --to-id or --unassign; do not mix assignment fields")
	}
	if unassign {
		body["assignee_type"], body["assignee_id"] = nil, nil
		return "", false, false, nil
	}
	if toID {
		return cmd.Flags().Lookup("to-id").Value.String(), true, true, nil
	}
	return cmd.Flags().Lookup("to").Value.String(), false, true, nil
}

func taskBodyAssigneeSelector(cmd *cobra.Command, body map[string]any) (input string, byID, resolve bool, err error) {
	if cmd.Flags().Changed("assignee") {
		if hasAssignmentFields(body) {
			return "", false, false, assignmentError("--assignee cannot be combined with explicit assignment fields")
		}
		return cmd.Flags().Lookup("assignee").Value.String(), false, true, nil
	}
	if cmd.Flags().Changed("assignee-id") && body["assignee_type"] == nil {
		if _, supplied := body["assignee_octo_uid"]; supplied {
			return "", false, false, assignmentError("--assignee-id cannot be combined with assignee_octo_uid")
		}
		return cmd.Flags().Lookup("assignee-id").Value.String(), true, true, nil
	}
	return "", false, false, nil
}

func hasAssignmentFields(body map[string]any) bool {
	for _, key := range []string{"assignee_type", "assignee_id", "assignee_octo_uid"} {
		if _, ok := body[key]; ok {
			return true
		}
	}
	return false
}

func applyTaskAssigneeQuery(cmd *cobra.Command, f *cmdutil.Factory, rt *operationRuntime, query url.Values, headers map[string]string) error {
	if rt.detail.Service != "loop" || rt.detail.ID != "task.list" {
		return nil
	}
	input, byID, resolve, err := taskQueryAssigneeSelector(cmd, query)
	if err != nil || !resolve {
		return err
	}
	actor, err := resolveTaskAssignee(cmd, f, headers, input, byID)
	if err != nil {
		return err
	}
	query.Del("assignee_id")
	if actor.uid != "" {
		query.Set("assignee_octo_uid", actor.uid)
	} else {
		query.Set("assignee_id", actor.id)
		query.Set("assignee_types", actor.kind)
	}
	return nil
}

func taskQueryAssigneeSelector(cmd *cobra.Command, query url.Values) (input string, byID, resolve bool, err error) {
	byName, byID := cmd.Flags().Changed("assignee"), cmd.Flags().Changed("assignee-id")
	if !byName && !byID {
		return "", false, false, nil
	}
	if byID && !byName && (query.Get("assignee_id") == "me" || query.Has("assignee_types")) {
		return "", false, false, nil
	}
	for key := range query {
		if strings.HasPrefix(key, "assignee_") && (key != "assignee_id" || !byID || byName) {
			return "", false, false, assignmentError("--assignee and --assignee-id cannot be combined with other assignee filters")
		}
	}
	if byID {
		return cmd.Flags().Lookup("assignee-id").Value.String(), true, true, nil
	}
	return cmd.Flags().Lookup("assignee").Value.String(), false, true, nil
}

type taskAssignee struct {
	kind, id, uid, name string
}

func resolveTaskAssignee(cmd *cobra.Command, f *cmdutil.Factory, headers map[string]string, input string, byID bool) (taskAssignee, error) {
	var empty taskAssignee
	if !byID {
		input = strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(input), "@＠"))
	}
	if input == "" || input != strings.TrimSpace(input) {
		return empty, assignmentError("assignee name or ID must not be empty or space-padded")
	}
	if f.Globals != nil && f.Globals.DryRun {
		return empty, assignmentError("--dry-run cannot resolve an assignee without HTTP; use explicit --assignee-type with --assignee-id or --assignee-octo-uid")
	}
	actors, err := readTaskAssignees(cmd, f, headers)
	if err != nil {
		return empty, err
	}
	return chooseTaskAssignee(actors, input, byID)
}

func chooseTaskAssignee(actors []taskAssignee, input string, byID bool) (taskAssignee, error) {
	buckets := [3][]taskAssignee{}
	for _, actor := range actors {
		if rank := actor.matchRank(input, byID); rank >= 0 {
			buckets[rank] = append(buckets[rank], actor)
		}
	}
	for _, matches := range buckets {
		if len(matches) == 1 {
			return matches[0], nil
		}
		if len(matches) > 1 {
			options := make([]string, 0, len(matches))
			for _, actor := range matches {
				options = append(options, fmt.Sprintf("%s %q (%s)", actor.kind, actor.name, actor.id))
			}
			return taskAssignee{}, assignmentError("ambiguous assignee; use an exact ID or explicit type: " + strings.Join(options, ", "))
		}
	}
	return taskAssignee{}, assignmentError("no member, expert or expert team found for the supplied name or ID")
}

func (a taskAssignee) matchRank(input string, byID bool) int {
	match := strings.EqualFold(a.id, input)
	if a.uid != "" {
		match = a.uid == input
	}
	if match {
		return 0
	}
	if byID {
		return -1
	}
	if a.uid == "" && len(input) == 8 && strings.EqualFold(a.id[:min(8, len(a.id))], input) {
		return 0
	}
	if strings.EqualFold(a.name, input) {
		return 1
	}
	if strings.Contains(strings.ToLower(a.name), strings.ToLower(input)) {
		return 2
	}
	return -1
}

func assignmentError(message string) *output.ExitError {
	return output.ErrValidation(message, "list workspace members, experts and expert teams separately to inspect their names and IDs")
}

type taskAssigneeRecord struct {
	ID            string          `json:"id"`
	ExpertID      string          `json:"expert_id"`
	TeamID        string          `json:"expert_team_id"`
	UserID        string          `json:"user_id"`
	UID           string          `json:"uid"`
	Name          string          `json:"name"`
	PrincipalType string          `json:"principal_type"`
	Robot         *int            `json:"robot"`
	ArchivedAt    json.RawMessage `json:"archived_at"`
}

func readTaskAssignees(cmd *cobra.Command, f *cmdutil.Factory, headers map[string]string) ([]taskAssignee, error) {
	workspace := headers["X-Workspace-ID"]
	if workspace == "" {
		return nil, assignmentError("--workspace-id is required to resolve assignees")
	}
	cli, err := f.Client()
	if err != nil {
		return nil, err
	}
	var actors []taskAssignee
	for _, source := range []struct{ operation, kind string }{
		{"workspace.member.list", "member"}, {"expert.list", "expert"}, {"expert_team.list", "expert_team"},
	} {
		op, ok := f.Registry().GetOperation(source.operation)
		if !ok {
			return nil, assignmentError("assignee list operation is unavailable: " + source.operation)
		}
		path := strings.ReplaceAll(op.Path, "{workspace_id}", url.PathEscape(workspace))
		records, err := readTaskAssigneePages(cmd, cli, path, headers, source.kind == "member")
		if err != nil {
			return nil, fmt.Errorf("read %s assignees: %w", source.kind, err)
		}
		for i := range records {
			actor, include, err := records[i].assignee(source.kind)
			if err != nil {
				return nil, err
			}
			if include {
				actors = append(actors, actor)
			}
		}
	}
	return actors, nil
}

func (r *taskAssigneeRecord) assignee(kind string) (taskAssignee, bool, error) {
	actor := taskAssignee{kind: kind, name: r.Name, id: r.ID}
	if kind == "member" {
		if r.PrincipalType == "bot" || (r.Robot != nil && *r.Robot != 0) {
			return actor, false, nil
		}
		actor.id = r.UserID
		if r.UID != "" {
			if r.Robot == nil {
				return actor, false, assignmentError("Project member response omitted robot identity")
			}
			actor.uid, actor.id = r.UID, r.UID
		}
	} else if kind == "expert" && r.ExpertID != "" {
		actor.id = r.ExpertID
	} else if kind == "expert_team" && r.TeamID != "" {
		actor.id = r.TeamID
	}
	if len(r.ArchivedAt) > 0 && string(r.ArchivedAt) != "null" && string(r.ArchivedAt) != `""` {
		return actor, false, nil
	}
	if actor.id == "" {
		return actor, false, assignmentError("assignee response omitted its user or actor ID")
	}
	return actor, true, nil
}

func readTaskAssigneePages(cmd *cobra.Command, cli *client.Client, path string, headers map[string]string, members bool) ([]taskAssigneeRecord, error) {
	var records []taskAssigneeRecord
	seen := map[string]bool{}
	expected := -1
	for page := 1; page <= 1000; page++ {
		query := url.Values{"page": {strconv.Itoa(page)}, "page_size": {"100"}}
		if members {
			query = url.Values{"limit": {"100"}, "offset": {strconv.Itoa(len(records))}}
		}
		body, err := cli.Do(cmd.Context(), &client.Request{Service: "loop", Method: "GET", Path: path, Query: query, Headers: headers})
		if err != nil {
			return nil, err
		}
		items, total, err := decodeTaskAssigneePage(body)
		if err != nil {
			return nil, err
		}
		if expected >= 0 && total != expected {
			return nil, assignmentError("assignee list changed during pagination; retry the lookup")
		}
		expected = total
		fingerprint, err := json.Marshal(items)
		if err != nil {
			return nil, fmt.Errorf("encode assignee page: %w", err)
		}
		if len(items) > 0 && seen[string(fingerprint)] {
			return nil, assignmentError("assignee pagination repeated a page")
		}
		seen[string(fingerprint)] = true
		records = append(records, items...)
		if len(records) == total {
			return records, nil
		}
		if len(items) == 0 || len(records) > total {
			return nil, assignmentError("assignee pagination returned an incomplete or invalid list")
		}
	}
	return nil, assignmentError("assignee list exceeds the lookup page limit; use an explicit type and ID")
}

func decodeTaskAssigneePage(body []byte) ([]taskAssigneeRecord, int, error) {
	var array []taskAssigneeRecord
	if json.Unmarshal(body, &array) == nil && strings.HasPrefix(strings.TrimSpace(string(body)), "[") {
		return array, len(array), nil
	}
	var envelope struct {
		Items      []taskAssigneeRecord `json:"items"`
		Data       json.RawMessage      `json:"data"`
		Total      *int                 `json:"total"`
		Pagination *struct {
			Total *int `json:"total"`
		} `json:"pagination"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return nil, 0, assignmentError("invalid assignee list JSON")
	}
	if envelope.Pagination != nil {
		envelope.Total = envelope.Pagination.Total
		if err := json.Unmarshal(envelope.Data, &envelope.Items); err != nil {
			return nil, 0, assignmentError("invalid assignee collection data")
		}
	}
	if envelope.Total == nil || *envelope.Total < 0 || envelope.Items == nil {
		return nil, 0, assignmentError("assignee list omitted items or total")
	}
	return envelope.Items, *envelope.Total, nil
}
