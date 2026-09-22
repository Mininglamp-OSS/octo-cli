package mcp

import "github.com/Mininglamp-OSS/octo-cli/internal/registry"

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
	// The message.search* family is force-when-configured, NOT excluded: when the
	// operator sets a --force-* value it must win (confine the search to that
	// channel / on-behalf-of subject), and apply's empty-value skip preserves the
	// legitimate default — an unconfigured operator leaves channel_id as optional
	// cross-channel scope and on_behalf_of as the model-chosen search subject.
	// (channel_id / channel_type / on_behalf_of are declared in the body;
	// message.search.groups declares only on_behalf_of.)
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
}

// sessionBoundFields are the argument names that decide who/what scope an
// operation acts on; the coverage guard classifies every enabled op declaring
// one of these. space_id is included so the guard cannot be blind to a
// cross-space argument (its omission was the fail-open the guard exists to
// prevent); drive's same-named resource id is handled by isDriveResourceSpaceID.
var sessionBoundFields = map[string]bool{
	"channel_id":   true,
	"channel_type": true,
	"on_behalf_of": true,
	"space_id":     true,
}

// isDriveResourceSpaceID is a documented, service-scoped exclusion: in the drive
// namespace space_id is a resource identifier (personal:<octo-space>:<uid> or
// shared:<uuid>, drive.json:83), NOT the Octo space context X-Space-Id carries.
// Forcing the connection's Octo space onto it would break drive entirely (the
// same-name trap). This is an EXCLUSION (never inject), so it is the safe
// direction — the coverage guard treats a drive op whose only session-bound
// field is space_id as classified.
func isDriveResourceSpaceID(op registry.OperationInfo, fields []string) bool {
	if op.Service != "drive" || len(fields) == 0 {
		return false
	}
	for _, f := range fields {
		if f != "space_id" {
			return false
		}
	}
	return true
}

// divergentMCPOps are operations whose generated OpenAPI schema does NOT
// describe their actual callable shape, because cmd/drive.go detaches the
// generated leaf and replaces it with a hand-written composite that takes a
// different argument shape (a positional body field / a whole share URL). Their
// generated schema is therefore uncallable via call_op's schema-derived argv,
// so this version withholds them from MCP discovery (search_ops), refuses to
// describe them (describe_op), and refuses call_op with a pointer to the CLI.
// Keep in sync with the RemoveLeaf(share, ...) calls in cmd/drive.go.
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
// the caller can surface which arguments were server-forced. A value the trusted
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
