package mcp

// Trusted-context (over-privilege防护) injection, design §5.
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
// resource id, drive.json:83) and message.search's channel_id (optional
// cross-channel scope, message.json:157) are absent from the table, so the
// same-name trap the design calls out is structurally impossible here. This
// centralized Go table is the first-version bridge; the target end-state is a
// per-op `x-octo-authz-overridable` spec extension audited across all 342 ops
// (design §9 item 2 / P2), which this table's shape mirrors.

// TrustedContext holds the connection-scoped values a trusted source injects.
// SpaceID additionally flows onto the credential so X-Space-Id is set the same
// way the CLI already does it (client.go:961-963); the others rewrite the
// matching argument before call_op assembles the request.
type TrustedContext struct {
	SpaceID     string
	ChannelID   string
	ChannelType string
	OnBehalfOf  string
}

// empty reports whether no trusted value is configured.
func (tc TrustedContext) empty() bool {
	return tc.SpaceID == "" && tc.ChannelID == "" && tc.ChannelType == "" && tc.OnBehalfOf == ""
}

type overridableField int

const (
	fieldChannelID overridableField = iota
	fieldChannelType
	fieldOnBehalfOf
	fieldSpaceID
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
}

// authzExclusions records operations that DECLARE a session-bound field but are
// deliberately NOT forced, with the rationale. The coverage guard
// (TestAuthzCoverage) requires every enabled operation carrying a session-bound
// field to be either in overridableParams or here, so a newly added equivalent
// operation fails CI instead of silently bypassing over-privilege防护.
//
// The message.search family is the sole exclusion: its channel_id is an
// OPTIONAL cross-channel scope ("omit to search across all reachable channels",
// message.json:157) and its on_behalf_of selects the real-person search subject
// a bf_ token searches as (design §5.3; a bf_ token searches as the bot, or as
// a real person when on_behalf_of is supplied). Forcing either would break
// legitimate cross-channel / on-behalf-of search, so these stay
// model/caller-controlled and the backend ACL is the guard.
var authzExclusions = map[string]string{
	"message.search":        "search scope: channel_id is optional cross-channel scope; on_behalf_of selects the search subject",
	"message.search.all":    "search scope: optional cross-channel scope / search subject",
	"message.search.around": "search scope: optional cross-channel scope / search subject",
	"message.search.files":  "search scope: optional cross-channel scope / search subject",
	"message.search.media":  "search scope: optional cross-channel scope / search subject",
	"message.search.groups": "search scope: on_behalf_of selects the search subject",
}

// sessionBoundFields are the argument names that decide who/what scope an
// operation acts on; the coverage guard classifies every enabled op declaring
// one of these.
var sessionBoundFields = map[string]bool{
	"channel_id":   true,
	"channel_type": true,
	"on_behalf_of": true,
}

// divergentMCPOps are operations whose generated OpenAPI schema does NOT
// describe their actual callable shape, because cmd/drive.go detaches the
// generated leaf and replaces it with a hand-written composite that takes a
// different argument shape (a positional body field / a whole share URL). Their
// generated schema is therefore uncallable via call_op's schema-derived argv,
// so this version withholds them from MCP discovery (search_ops), refuses to
// describe them (describe_op), and refuses call_op with a pointer to the CLI
// (B6). Keep in sync with the RemoveLeaf(share, …) calls in cmd/drive.go.
var divergentMCPOps = map[string]string{
	"drive.share.blob-create": "octo-cli drive share blob-create",
	"drive.share.access":      "octo-cli drive share access",
	"drive.share.download":    "octo-cli drive share download",
}

func (tc TrustedContext) value(f overridableField) string {
	switch f {
	case fieldChannelID:
		return tc.ChannelID
	case fieldChannelType:
		return tc.ChannelType
	case fieldOnBehalfOf:
		return tc.OnBehalfOf
	case fieldSpaceID:
		return tc.SpaceID
	}
	return ""
}

// apply overrides the white-listed arguments of opID with the connection's
// trusted values, in place. It returns the set of argument names it forced, so
// the caller can report over-privilege防护 in effect. A value the trusted
// context does not carry is left to the model (the operation's own required
// check still applies).
func (tc TrustedContext) apply(opID string, args map[string]any) map[string]bool {
	forced := map[string]bool{}
	for param, field := range overridableParams[opID] {
		v := tc.value(field)
		if v == "" {
			continue
		}
		args[param] = v
		forced[param] = true
	}
	return forced
}

// serverManaged reports the arguments describe_op should flag as server-managed
// for opID given the trusted values this context actually carries. An empty
// context manages nothing (the arguments stay fully model-controlled).
func (tc TrustedContext) serverManaged(opID string) map[string]bool {
	managed := map[string]bool{}
	for param, field := range overridableParams[opID] {
		if tc.value(field) != "" {
			managed[param] = true
		}
	}
	return managed
}
