package mcp

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/Mininglamp-OSS/octo-cli/internal/registry"
)

// Trusted-context (over-privilege protection) injection.
//
// Some arguments decide *who / what scope* an operation acts on — space id,
// the channel a message goes to, the on-behalf-of identity. Left to the model
// they are an over-privilege vector (a hallucinated or coaxed channel_id sends
// to the wrong group). The defence: a trusted party (the MCP host / connection
// config) forces these via server-side values that override whatever the model
// put in arguments, and describe_op marks them server-managed so the model is
// steered not to supply them.
//
// The white-list is PER OPERATION, never a global parameter-name match. That is
// deliberate: message.send.channel_id is forced, but drive's space_id (a drive
// resource id of the form personal:<octo-space>:<uid> / shared:<uuid>,
// drive.json:83) is never forced, so the same-name trap is structurally
// impossible here. Forcing is force-WHEN-CONFIGURED: apply only overwrites a
// field the trusted context carries a non-empty value for, so the message.search*
// family (channel_id optional cross-channel scope, on_behalf_of the search
// subject) keeps its model-controlled default when the operator configures
// nothing, and is confined the moment the operator sets a --force-* value.

// TrustedContext holds the connection-scoped values a trusted source injects.
// SpaceID additionally flows onto the credential so X-Space-Id is set the same
// way the CLI already does it (client.go:961-963); the others rewrite the
// matching argument before call_op assembles the request.
type TrustedContext struct {
	SpaceID          string
	ChannelID        string
	ChannelType      string
	OnBehalfOf       string
	ThreadID         string
	WorkspaceID      string
	PrincipalSpaceID string
}

type overridableField int

const (
	fieldChannelID overridableField = iota
	fieldChannelType
	fieldOnBehalfOf
	fieldSpaceID
	fieldThreadID
	fieldWorkspaceID
	fieldPrincipalSpaceID
	fieldGroupID
	fieldThreadShortID
)

// overridableParams is the per-operation authz white-list: operation_id ->
// (argument name -> which trusted value overrides it).
var overridableParams = map[string]map[string]overridableField{
	"message.send": {
		"channel_id":   fieldChannelID,
		"channel_type": fieldChannelType,
		"on_behalf_of": fieldOnBehalfOf,
	},
	"message.sync": {
		"channel_id":   fieldChannelID,
		"channel_type": fieldChannelType,
	},
	"message.edit": {
		"channel_id":   fieldChannelID,
		"channel_type": fieldChannelType,
	},
	"message.read-receipt": {
		"channel_id":   fieldChannelID,
		"channel_type": fieldChannelType,
	},
	"bot.typing": {
		"channel_id":   fieldChannelID,
		"channel_type": fieldChannelType,
		"on_behalf_of": fieldOnBehalfOf,
	},
	"bot.space-members": {
		"space_id": fieldSpaceID,
	},
	// group.create (body space_id) and group.list (query space_id) run under
	// x-octo-space-header:false (group.json:10), so X-Space-Id is suppressed and
	// the space_id argument is the ONLY space signal on the wire. Force it, like
	// bot.space-members, so a connection-forced space actually confines them.
	"group.create": {
		"space_id": fieldSpaceID,
	},
	"group.list": {
		"space_id": fieldSpaceID,
	},
	// Searches with a channel selector are force-when-configured. The global-only
	// group overview has no channel selector and is refused when ChannelID is
	// pinned; an OBO-only or unconfigured connection can still use it.
	"message.search": {
		"channel_id":   fieldChannelID,
		"channel_type": fieldChannelType,
		"on_behalf_of": fieldOnBehalfOf,
	},
	"message.search.all": {
		"channel_id":   fieldChannelID,
		"channel_type": fieldChannelType,
		"on_behalf_of": fieldOnBehalfOf,
	},
	"message.search.files": {
		"channel_id":   fieldChannelID,
		"channel_type": fieldChannelType,
		"on_behalf_of": fieldOnBehalfOf,
	},
	"message.search.media": {
		"channel_id":   fieldChannelID,
		"channel_type": fieldChannelType,
		"on_behalf_of": fieldOnBehalfOf,
	},
	"message.search.around": {
		"channel_id":   fieldChannelID,
		"channel_type": fieldChannelType,
		"on_behalf_of": fieldOnBehalfOf,
	},
	"message.search.groups": {
		"on_behalf_of": fieldOnBehalfOf,
	},
	"attachment.content.get":          {"X-Workspace-ID": fieldWorkspaceID},
	"attachment.delete":               {"X-Workspace-ID": fieldWorkspaceID},
	"attachment.download":             {"X-Workspace-ID": fieldWorkspaceID},
	"attachment.get":                  {"X-Workspace-ID": fieldWorkspaceID},
	"attachment.upload":               {"X-Workspace-ID": fieldWorkspaceID},
	"autopilot.create":                {"X-Workspace-ID": fieldWorkspaceID},
	"autopilot.delete":                {"X-Workspace-ID": fieldWorkspaceID},
	"autopilot.delivery.get":          {"X-Workspace-ID": fieldWorkspaceID},
	"autopilot.delivery.list":         {"X-Workspace-ID": fieldWorkspaceID},
	"autopilot.delivery.replay":       {"X-Workspace-ID": fieldWorkspaceID},
	"autopilot.get":                   {"X-Workspace-ID": fieldWorkspaceID},
	"autopilot.list":                  {"X-Workspace-ID": fieldWorkspaceID},
	"autopilot.run.get":               {"X-Workspace-ID": fieldWorkspaceID},
	"autopilot.run.list":              {"X-Workspace-ID": fieldWorkspaceID},
	"autopilot.signing_secret.set":    {"X-Workspace-ID": fieldWorkspaceID},
	"autopilot.trigger":               {"X-Workspace-ID": fieldWorkspaceID},
	"autopilot.trigger_config.create": {"X-Workspace-ID": fieldWorkspaceID},
	"autopilot.trigger_config.delete": {"X-Workspace-ID": fieldWorkspaceID},
	"autopilot.trigger_config.update": {"X-Workspace-ID": fieldWorkspaceID},
	"autopilot.update":                {"X-Workspace-ID": fieldWorkspaceID},
	"autopilot.webhook_token.rotate":  {"X-Workspace-ID": fieldWorkspaceID},
	"comment.delete":                  {"X-Workspace-ID": fieldWorkspaceID},
	"comment.reaction.add":            {"X-Workspace-ID": fieldWorkspaceID},
	"comment.reaction.remove":         {"X-Workspace-ID": fieldWorkspaceID},
	"comment.reopen":                  {"X-Workspace-ID": fieldWorkspaceID},
	"comment.resolve":                 {"X-Workspace-ID": fieldWorkspaceID},
	"comment.update":                  {"X-Workspace-ID": fieldWorkspaceID},
	"docs.members.remove":             {"principalSpaceId": fieldPrincipalSpaceID},
	"docs.members.set":                {"principalSpaceId": fieldPrincipalSpaceID},
	"drive.im-transfer.create":        {"im_group_no": fieldChannelID, "im_channel_type": fieldChannelType},
	"execution.cancel":                {"X-Workspace-ID": fieldWorkspaceID},
	"execution.message.list":          {"X-Workspace-ID": fieldWorkspaceID},
	"expert.archive":                  {"X-Workspace-ID": fieldWorkspaceID},
	"expert.create":                   {"X-Workspace-ID": fieldWorkspaceID},
	"expert.create_from_template":     {"X-Workspace-ID": fieldWorkspaceID},
	"expert.environment.get":          {"X-Workspace-ID": fieldWorkspaceID},
	"expert.environment.update":       {"X-Workspace-ID": fieldWorkspaceID},
	"expert.execution.cancel":         {"X-Workspace-ID": fieldWorkspaceID},
	"expert.execution.list":           {"X-Workspace-ID": fieldWorkspaceID},
	"expert.get":                      {"X-Workspace-ID": fieldWorkspaceID},
	"expert.list":                     {"X-Workspace-ID": fieldWorkspaceID},
	"expert.restore":                  {"X-Workspace-ID": fieldWorkspaceID},
	"expert.skill.add":                {"X-Workspace-ID": fieldWorkspaceID},
	"expert.skill.list":               {"X-Workspace-ID": fieldWorkspaceID},
	"expert.skill.replace":            {"X-Workspace-ID": fieldWorkspaceID},
	"expert.update":                   {"X-Workspace-ID": fieldWorkspaceID},
	"expert_team.create":              {"X-Workspace-ID": fieldWorkspaceID},
	"expert_team.delete":              {"X-Workspace-ID": fieldWorkspaceID},
	"expert_team.get":                 {"X-Workspace-ID": fieldWorkspaceID},
	"expert_team.list":                {"X-Workspace-ID": fieldWorkspaceID},
	"expert_team.member.add":          {"X-Workspace-ID": fieldWorkspaceID},
	"expert_team.member.list":         {"X-Workspace-ID": fieldWorkspaceID},
	"expert_team.member.remove":       {"X-Workspace-ID": fieldWorkspaceID},
	"expert_team.member.update":       {"X-Workspace-ID": fieldWorkspaceID},
	"expert_team.member_status.list":  {"X-Workspace-ID": fieldWorkspaceID},
	"expert_team.update":              {"X-Workspace-ID": fieldWorkspaceID},
	"expert_template.get":             {"X-Workspace-ID": fieldWorkspaceID},
	"expert_template.list":            {"X-Workspace-ID": fieldWorkspaceID},
	"group.get":                       {"group_no": fieldGroupID},
	"group.md-get":                    {"group_no": fieldGroupID},
	"group.md-update":                 {"group_no": fieldGroupID},
	"group.member-add":                {"group_no": fieldGroupID},
	"group.member-remove":             {"group_no": fieldGroupID},
	"group.members":                   {"group_no": fieldGroupID},
	"group.update":                    {"group_no": fieldGroupID},
	"html.draft.create":               {"group_no": fieldGroupID, "thread_id": fieldThreadID},
	"html.publish":                    {"group_no": fieldGroupID, "thread_id": fieldThreadID},
	"label.create":                    {"X-Workspace-ID": fieldWorkspaceID},
	"label.delete":                    {"X-Workspace-ID": fieldWorkspaceID},
	"label.get":                       {"X-Workspace-ID": fieldWorkspaceID},
	"label.list":                      {"X-Workspace-ID": fieldWorkspaceID},
	"label.update":                    {"X-Workspace-ID": fieldWorkspaceID},
	"loop.skill.create":               {"X-Workspace-ID": fieldWorkspaceID},
	"loop.skill.delete":               {"X-Workspace-ID": fieldWorkspaceID},
	"loop.skill.get":                  {"X-Workspace-ID": fieldWorkspaceID},
	"loop.skill.import":               {"X-Workspace-ID": fieldWorkspaceID},
	"loop.skill.list":                 {"X-Workspace-ID": fieldWorkspaceID},
	"loop.skill.search":               {"X-Workspace-ID": fieldWorkspaceID},
	"loop.skill.update":               {"X-Workspace-ID": fieldWorkspaceID},
	"loop.skill_file.delete":          {"X-Workspace-ID": fieldWorkspaceID},
	"loop.skill_file.list":            {"X-Workspace-ID": fieldWorkspaceID},
	"loop.skill_file.upsert":          {"X-Workspace-ID": fieldWorkspaceID},
	"plugin.install":                  {"workspace_id": fieldWorkspaceID},
	"project.create":                  {"X-Workspace-ID": fieldWorkspaceID},
	"project.delete":                  {"X-Workspace-ID": fieldWorkspaceID},
	"project.get":                     {"X-Workspace-ID": fieldWorkspaceID},
	"project.list":                    {"X-Workspace-ID": fieldWorkspaceID},
	"project.resource.create":         {"X-Workspace-ID": fieldWorkspaceID},
	"project.resource.delete":         {"X-Workspace-ID": fieldWorkspaceID},
	"project.resource.list":           {"X-Workspace-ID": fieldWorkspaceID},
	"project.resource.update":         {"X-Workspace-ID": fieldWorkspaceID},
	"project.search":                  {"X-Workspace-ID": fieldWorkspaceID},
	"project.update":                  {"X-Workspace-ID": fieldWorkspaceID},
	"runtime.activity.list":           {"X-Workspace-ID": fieldWorkspaceID},
	"runtime.local_skill_import.get":  {"X-Workspace-ID": fieldWorkspaceID},
	"runtime.local_skill_request.get": {"X-Workspace-ID": fieldWorkspaceID},
	"runtime.model_request.get":       {"X-Workspace-ID": fieldWorkspaceID},
	"runtime.update_request.get":      {"X-Workspace-ID": fieldWorkspaceID},
	"runtime.usage.get":               {"X-Workspace-ID": fieldWorkspaceID},
	"runtime.usage_by_expert.get":     {"X-Workspace-ID": fieldWorkspaceID},
	"runtime.usage_by_hour.get":       {"X-Workspace-ID": fieldWorkspaceID},
	"task.active_execution.list":      {"X-Workspace-ID": fieldWorkspaceID},
	"task.attachment.list":            {"X-Workspace-ID": fieldWorkspaceID},
	"task.child.list":                 {"X-Workspace-ID": fieldWorkspaceID},
	"task.child_progress":             {"X-Workspace-ID": fieldWorkspaceID},
	"task.children_by_parent":         {"X-Workspace-ID": fieldWorkspaceID},
	"task.comment.create":             {"X-Workspace-ID": fieldWorkspaceID},
	"task.comment.list":               {"X-Workspace-ID": fieldWorkspaceID},
	"task.comment.preview":            {"X-Workspace-ID": fieldWorkspaceID},
	"task.create":                     {"X-Workspace-ID": fieldWorkspaceID},
	"task.delete":                     {"X-Workspace-ID": fieldWorkspaceID},
	"task.execution.cancel":           {"X-Workspace-ID": fieldWorkspaceID},
	"task.execution.list":             {"X-Workspace-ID": fieldWorkspaceID},
	"task.get":                        {"X-Workspace-ID": fieldWorkspaceID},
	"task.group":                      {"X-Workspace-ID": fieldWorkspaceID},
	"task.label.add":                  {"X-Workspace-ID": fieldWorkspaceID},
	"task.label.list":                 {"X-Workspace-ID": fieldWorkspaceID},
	"task.label.remove":               {"X-Workspace-ID": fieldWorkspaceID},
	"task.list":                       {"X-Workspace-ID": fieldWorkspaceID},
	"task.metadata.delete":            {"X-Workspace-ID": fieldWorkspaceID},
	"task.metadata.list":              {"X-Workspace-ID": fieldWorkspaceID},
	"task.metadata.set":               {"X-Workspace-ID": fieldWorkspaceID},
	"task.pull_request.list":          {"X-Workspace-ID": fieldWorkspaceID},
	"task.quick_create":               {"X-Workspace-ID": fieldWorkspaceID},
	"task.reaction.add":               {"X-Workspace-ID": fieldWorkspaceID},
	"task.reaction.remove":            {"X-Workspace-ID": fieldWorkspaceID},
	"task.rerun":                      {"X-Workspace-ID": fieldWorkspaceID},
	"task.search":                     {"X-Workspace-ID": fieldWorkspaceID},
	"task.subscribe":                  {"X-Workspace-ID": fieldWorkspaceID},
	"task.subscriber.list":            {"X-Workspace-ID": fieldWorkspaceID},
	"task.team_evaluation.record":     {"X-Workspace-ID": fieldWorkspaceID},
	"task.timeline.list":              {"X-Workspace-ID": fieldWorkspaceID},
	"task.unsubscribe":                {"X-Workspace-ID": fieldWorkspaceID},
	"task.update":                     {"X-Workspace-ID": fieldWorkspaceID},
	"task.usage":                      {"X-Workspace-ID": fieldWorkspaceID},
	"thread.create":                   {"group_no": fieldGroupID},
	"thread.get":                      {"group_no": fieldGroupID, "short_id": fieldThreadShortID},
	"thread.join":                     {"group_no": fieldGroupID, "short_id": fieldThreadShortID},
	"thread.leave":                    {"group_no": fieldGroupID, "short_id": fieldThreadShortID},
	"thread.list":                     {"group_no": fieldGroupID},
	"thread.md-get":                   {"group_no": fieldGroupID, "short_id": fieldThreadShortID},
	"thread.md-update":                {"group_no": fieldGroupID, "short_id": fieldThreadShortID},
	"thread.members":                  {"group_no": fieldGroupID, "short_id": fieldThreadShortID},
	"workspace.get":                   {"X-Workspace-ID": fieldWorkspaceID, "workspace_id": fieldWorkspaceID},
	"workspace.member.list":           {"X-Workspace-ID": fieldWorkspaceID, "workspace_id": fieldWorkspaceID},
}

// sessionBoundFields identifies scope concepts for the coverage audit, including
// their declared wire aliases. The explicit per-operation table controls writes.
// Drive space_id/target_space_id are Drive resource ids, never Octo Space context.
var sessionBoundFields = map[string]bool{
	"channel_id":   true,
	"channel_type": true,
	"on_behalf_of": true,
	"space_id":     true,
	"group_no":     true, "im_group_no": true, "im_channel_type": true, "thread_id": true, "X-Workspace-ID": true, "workspace_id": true, "principalSpaceId": true, "target_space_id": true,
}

// divergentMCPOps are operations whose generated OpenAPI schema does NOT
// describe their actual callable shape, because cmd/drive.go detaches the
// generated leaf and replaces it with a hand-written composite that takes a
// different argument shape (a positional body field / a whole share URL). Their
// generated schema is therefore uncallable via call_op's schema-derived argv,
// so this version withholds them from MCP discovery (search_ops), refuses to
// describe them (describe_op), and refuses call_op with a pointer to the CLI.
// Binary exports also require an operator output destination and have no MCP
// binary-result contract. Keep the Drive entries in sync with RemoveLeaf calls.
var divergentMCPOps = sync.OnceValue(func() map[string]string {
	withheld := map[string]string{
		"drive.share.blob-create": "octo-cli drive share blob-create",
		"drive.share.access":      "octo-cli drive share access",
		"drive.share.download":    "octo-cli drive share download",
	}
	reg := registry.MustNew()
	for _, op := range reg.EnabledOperations() {
		detail, ok := reg.GetOperation(op.ID)
		if ok && detail.BinaryBody {
			withheld[op.ID] = "octo-cli " + strings.Join(commandWords(detail), " ") + " --output <file>"
		}
	}
	return withheld
})

func (tc *TrustedContext) value(f overridableField) string {
	switch f {
	case fieldChannelID:
		return tc.ChannelID
	case fieldGroupID:
		if tc.ChannelType == "2" {
			return tc.ChannelID
		}
		group, _ := tc.threadChannelParts()
		return group
	case fieldThreadShortID:
		if tc.ThreadID != "" {
			return tc.ThreadID
		}
		_, short := tc.threadChannelParts()
		return short
	case fieldChannelType:
		return tc.ChannelType
	case fieldOnBehalfOf:
		return tc.OnBehalfOf
	case fieldSpaceID:
		return tc.SpaceID
	case fieldThreadID:
		if tc.ThreadID != "" {
			return tc.ThreadID
		}
		_, short := tc.threadChannelParts()
		return short
	case fieldWorkspaceID:
		return tc.WorkspaceID
	case fieldPrincipalSpaceID:
		return tc.PrincipalSpaceID
	}
	return ""
}

func (tc *TrustedContext) threadChannelParts() (group, short string) {
	if tc.ChannelType == "5" {
		group, short, ok := strings.Cut(tc.ChannelID, "____")
		if ok && group != "" && short != "" {
			return group, short
		}
	}
	return "", ""
}

// ValidateBinding rejects contradictory operator or gateway scope values.
func (tc *TrustedContext) ValidateBinding() error {
	if tc.ChannelID != "" && strings.TrimSpace(tc.ChannelID) == "" {
		return errors.New("forced channel ID cannot be blank")
	}
	if tc.ChannelType == "5" && tc.ChannelID != "" {
		_, short := tc.threadChannelParts()
		if short == "" {
			return errors.New("type-5 channel ID must contain group____thread")
		}
		if tc.ThreadID != "" && tc.ThreadID != short {
			return errors.New("forced thread ID conflicts with type-5 channel ID")
		}
	}
	return nil
}

// validateOperationBinding checks the selector and every channel path field
// before a request is assembled. A partially configured trusted scope must
// never leave the matching write target in caller-controlled arguments.
func (tc *TrustedContext) validateOperationBinding(opID string, args map[string]any) error { //nolint:gocyclo // independent trusted scope selectors must each fail closed
	if err := tc.ValidateBinding(); err != nil {
		return err
	}
	if opID == "message.search.groups" && tc.ChannelID != "" {
		return errors.New("global group search cannot run with a forced channel; use message.search in the pinned channel")
	}
	if strings.HasPrefix(opID, "message.search") && tc.hasForcedSearchScope(opID) {
		if filters, ok := args["filters"]; ok && containsSearchScope(filters) {
			return errors.New("search filters cannot select another protected scope when search identity or channel is forced")
		}
	}
	channelPinned := tc.ChannelID != "" || tc.ChannelType != "" || tc.ThreadID != ""
	if !channelPinned {
		return nil
	}
	if opID == "html.publish" || opID == "html.draft.create" {
		if tc.ChannelID == "" && tc.ThreadID != "" {
			if mount, present := args["mount_type"]; present && mount != "" && mount != "thread" {
				return errors.New("mount_type conflicts with forced thread placement")
			}
			return nil
		}
		if tc.ChannelID != "" {
			if tc.ChannelType != "2" && tc.ChannelType != "5" {
				return errors.New("HTML placement requires a forced group (type 2) or thread (type 5) channel")
			}
			if tc.ChannelType == "5" && tc.value(fieldThreadID) == "" {
				return errors.New("forced thread channel has no short ID")
			}
			expected := "group"
			if tc.value(fieldThreadID) != "" {
				expected = "thread"
			}
			if mount, present := args["mount_type"]; present && mount != "" && mount != expected {
				return fmt.Errorf("mount_type conflicts with forced %s placement", expected)
			}
			return nil
		}
		group, thread := tc.value(fieldGroupID), tc.value(fieldThreadID)
		mount, _ := args["mount_type"].(string)
		switch mount {
		case "group":
			if group == "" {
				return errors.New("mount_type=group requires a trusted group binding")
			}
		case "thread":
			if thread == "" {
				return errors.New("mount_type=thread requires a trusted thread binding")
			}
		case "", "space":
			return errors.New("a trusted channel binding requires mount_type=group or thread")
		default:
			return errors.New("unknown mount_type under a trusted channel binding")
		}
		return nil
	}
	for name, field := range overridableParams[opID] {
		if (field == fieldGroupID || field == fieldThreadShortID || field == fieldThreadID) && tc.value(field) == "" {
			return fmt.Errorf("trusted channel binding cannot determine %s for %s", name, opID)
		}
	}
	return nil
}

// apply overrides the white-listed arguments of opID with the connection's
// trusted values, in place. It returns the set of argument names it forced, so
// the caller can surface which arguments were server-forced. A value the trusted
// context does not carry is left to the model (the operation's own required
// check still applies).
func (tc *TrustedContext) apply(opID string, args map[string]any) map[string]bool {
	forced := map[string]bool{}
	for param, field := range overridableParams[opID] {
		v := tc.value(field)
		if v == "" {
			continue
		}
		args[param] = v
		forced[param] = true
	}
	if (opID == "html.publish" || opID == "html.draft.create") && tc.ChannelID == "" && tc.ThreadID != "" {
		args["mount_type"] = "thread"
		delete(args, "group_no")
		forced["mount_type"] = true
	} else if (opID == "html.publish" || opID == "html.draft.create") && tc.ChannelID != "" && (tc.ChannelType == "2" || tc.ChannelType == "5") {
		if tc.value(fieldThreadID) != "" {
			args["mount_type"] = "thread"
		} else {
			args["mount_type"] = "group"
			if tc.value(fieldThreadID) == "" {
				delete(args, "thread_id")
			}
		}
		forced["mount_type"] = true
	}
	return forced
}

func (tc *TrustedContext) hasForcedSearchScope(opID string) bool {
	for _, field := range overridableParams[opID] {
		if tc.value(field) != "" {
			return true
		}
	}
	return false
}

func containsSearchScope(value any) bool {
	switch v := value.(type) {
	case map[string]any:
		for key, child := range v {
			if isScopeField(key) || containsSearchScope(child) {
				return true
			}
		}
	case []any:
		for _, child := range v {
			if containsSearchScope(child) {
				return true
			}
		}
	}
	return false
}

// serverManaged reports the arguments describe_op should flag as server-managed
// for opID given the trusted values this context actually carries. An empty
// context manages nothing (the arguments stay fully model-controlled).
func (tc *TrustedContext) serverManaged(opID string) map[string]bool {
	managed := map[string]bool{}
	for param, field := range overridableParams[opID] {
		if tc.value(field) != "" {
			managed[param] = true
		}
	}
	if (opID == "html.publish" || opID == "html.draft.create") && (tc.ThreadID != "" || tc.ChannelID != "" && (tc.ChannelType == "2" || tc.ChannelType == "5")) {
		managed["mount_type"] = true
	}
	return managed
}

// UnforcedChannelFields reports operation fields that a configured channel ID
// cannot derive from the current channel type/thread flags. It is an operator
// startup diagnostic, not an authorization grant: backend ACLs still apply.
func (tc *TrustedContext) UnforcedChannelFields() []string {
	if tc.ChannelID == "" && tc.ChannelType == "" && tc.ThreadID == "" {
		return nil
	}
	var gaps []string
	for op, fields := range overridableParams {
		for name, field := range fields {
			switch field {
			case fieldChannelID, fieldChannelType, fieldGroupID, fieldThreadID, fieldThreadShortID:
				if tc.value(field) == "" {
					gaps = append(gaps, op+"."+name)
				}
			}
		}
	}
	sort.Strings(gaps)
	return gaps
}

// isScopeField makes unknown scope spellings visible to the coverage audit.
// Execution remains controlled by the explicit operation/field table above.
func isScopeField(name string) bool {
	if sessionBoundFields[name] {
		return true
	}
	normalized := strings.ToLower(strings.ReplaceAll(strings.ReplaceAll(name, "_", ""), "-", ""))
	return strings.HasSuffix(normalized, "spaceid") || strings.HasSuffix(normalized, "channelid") || strings.HasSuffix(normalized, "channelids") || strings.HasSuffix(normalized, "channeltype") || strings.HasSuffix(normalized, "channeltypes") || strings.HasSuffix(normalized, "groupno") || strings.HasSuffix(normalized, "groupid") || strings.HasSuffix(normalized, "threadid") || strings.HasSuffix(normalized, "shortid") || strings.HasSuffix(normalized, "conversationid") || strings.HasSuffix(normalized, "chatid") || strings.HasSuffix(normalized, "tenantid") || normalized == "onbehalfof"
}
