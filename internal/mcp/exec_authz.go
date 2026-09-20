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
	"bot.space-members": {
		"space_id": fieldSpaceID,
	},
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
