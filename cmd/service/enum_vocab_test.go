package service

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"testing"

	"github.com/Mininglamp-OSS/octo-cli/internal/registry"
)

// This PR promotes spec `enum` from help text into a pre-flight gate: an
// out-of-vocabulary value exits 2 with ENUM_NOT_ALLOWED and sends no HTTP. That
// gate is global — it applies to every enabled spec, not just the domain this PR
// adds — so every request-side vocabulary in the registry became load-bearing at
// once. A vocabulary *narrower* than its backend now turns a working call into a
// hard local failure, and no response can teach the CLI otherwise.
//
// Round 8 found four such vocabularies in three domains that no earlier round had
// looked at, because they live in specs a PR titled feat(drive) does not touch.
// This table is the fix for the class rather than the four instances: it pins
// every request-side enum in every enabled spec, in both directions.
//
//   - a vocabulary whose values drift from this table fails
//   - a vocabulary that is not in this table at all fails
//   - a table entry whose vocabulary has disappeared fails
//
// The middle rule is the one that matters: it means a spec author cannot add an
// enum — and with it a new hard gate — without transcribing the backend's set and
// recording where it came from.
//
// Provenance is required, not decorative. Each entry names the backend definition
// it was transcribed from, at a *remote* ref: a local checkout that is behind
// origin will happily confirm a stale vocabulary. Three of the four round-8
// findings were invisible in a checkout 40 commits behind main.

// vocabulary is one request-side enum and the backend authority for its values.
type vocabulary struct {
	op    string // operationId
	in    string // "query", "query[]", "body", "body[]"
	field string
	want  []string
	// why records the backend definition this set was transcribed from, or the
	// reason the CLI's set legitimately differs from it.
	why string
}

// requestSideVocabularies is the complete set for every spec without
// x-octo-disabled. matter and summary are withheld, so their enums cannot fire
// and are deliberately absent.
var requestSideVocabularies = []vocabulary{
	// --- loop ---
	{op: "autopilot.create", in: "body", field: "dispatch_mode",
		want: []string{"create_task", "direct_execution"},
		why:  "octo-fleet openapi/public-v1.yaml AutopilotDispatchMode; internal/publicapi/normalize.go validates the same public values"},
	{op: "autopilot.create", in: "body", field: "assignee_type",
		want: []string{"expert", "expert_team"},
		why:  "octo-fleet openapi/public-v1.yaml AutopilotCreateRequest; the Public API adapter maps these to the legacy agent/squad values"},
	{op: "autopilot.update", in: "body", field: "status",
		want: []string{"active", "paused"},
		why:  "octo-fleet openapi/public-v1.yaml AutopilotUpdateRequest"},
	{op: "autopilot.update", in: "body", field: "dispatch_mode",
		want: []string{"create_task", "direct_execution"},
		why:  "same AutopilotDispatchMode schema as autopilot.create"},
	{op: "autopilot.update", in: "body", field: "assignee_type",
		want: []string{"expert", "expert_team"},
		why:  "octo-fleet openapi/public-v1.yaml AutopilotUpdateRequest"},
	{op: "autopilot.trigger_config.create", in: "body", field: "kind",
		want: []string{"schedule", "webhook"},
		why:  "octo-fleet openapi/public-v1.yaml AutopilotTriggerCreateRequest; the legacy handler enforces the same two trigger kinds"},
	{op: "autopilot.trigger_config.create", in: "body", field: "provider",
		want: []string{"generic", "github"},
		why:  "octo-fleet openapi/public-v1.yaml AutopilotTriggerCreateRequest webhook providers"},
	{op: "expert_team.member.add", in: "body", field: "member_type",
		want: []string{"expert", "member"},
		why:  "octo-fleet openapi/public-v1.yaml ExpertTeamMemberMutationRequest"},
	{op: "expert_team.member.remove", in: "body", field: "member_type",
		want: []string{"expert", "member"},
		why:  "same ExpertTeamMemberMutationRequest schema as expert_team.member.add"},
	{op: "expert_team.member.update", in: "body", field: "member_type",
		want: []string{"expert", "member"},
		why:  "same ExpertTeamMemberMutationRequest schema as expert_team.member.add"},
	{op: "task.create", in: "body", field: "assignee_type",
		want: []string{"expert", "expert_team", "member"},
		why:  "octo-fleet openapi/public-v1.yaml TaskCreateRequest; internal/publicapi normalization maps public expert terminology to legacy storage values"},
	{op: "task.update", in: "body", field: "assignee_type",
		want: []string{"expert", "expert_team", "member"},
		why:  "octo-fleet openapi/public-v1.yaml TaskUpdateRequest"},
	{op: "task.update", in: "body", field: "status",
		want: []string{"backlog", "todo", "in_progress", "in_review", "done", "blocked", "cancelled"},
		why:  "octo-fleet openapi/public-v1.yaml TaskUpdateRequest and internal/publicapi/normalize.go validateTaskStatus"},
	{op: "task.subscribe", in: "body", field: "user_type",
		want: []string{"expert", "expert_team", "member"},
		why:  "octo-fleet openapi/public-v1.yaml TaskSubscriptionRequest"},
	{op: "task.unsubscribe", in: "body", field: "user_type",
		want: []string{"expert", "expert_team", "member"},
		why:  "same TaskSubscriptionRequest schema as task.subscribe"},

	// --- drive (this PR's domain) ---
	{op: "drive.member.add", in: "body", field: "role",
		want: []string{"preview_only", "downloader", "uploader_downloader", "editor", "admin", "super_admin", "custom"},
		why:  "octo-drive models.AllRoles; the grantable subset is narrower but the enum must stay a superset of every surface, or the gate rejects a role the backend accepts"},
	{op: "drive.member.set-role", in: "body", field: "role",
		want: []string{"preview_only", "downloader", "uploader_downloader", "editor", "admin", "super_admin", "custom"},
		why:  "same shared Role schema as member.add"},
	{op: "drive.invite.create", in: "body", field: "role",
		want: []string{"preview_only", "downloader", "uploader_downloader", "editor", "admin", "super_admin", "custom"},
		why:  "same shared Role schema; invite.inviteRoleAllowed narrows it server-side"},
	{op: "drive.blob.create", in: "body", field: "source",
		want: []string{"user-upload", "im-transfer"},
		why:  "octo-drive blob.allowedBlobSources"},
	{op: "drive.doc.mount", in: "body", field: "source",
		want: []string{"user-mount", "docs-sync"},
		why:  "octo-drive docref.allowedMountSources; this is the one the E2E round caught being narrower than the service"},
	{op: "drive.share.blob-create", in: "body", field: "permission",
		want: []string{"view", "download"},
		why:  "octo-drive share.CreateShare; octo-web useShare.ts:45 records that edit is backend-P2 and callers must not offer it"},
	{op: "drive.browse", in: "query", field: "type",
		want: []string{"all", "doc", "blob", "folder"},
		why:  "octo-drive browse filter"},
	{op: "drive.search", in: "body", field: "scope",
		want: []string{"all", "space"},
		why:  "octo-drive internal/search/query.go ScopeAll/ScopeSpace; service.go validate() rejects anything else with ErrInvalidArgument"},
	{op: "drive.browse", in: "query", field: "source",
		want: []string{"all", "user-upload", "im-transfer", "user-mount", "docs-sync"},
		why:  "octo-drive browse filter: the blob and mount source sets unioned, plus all"},
	{op: "drive.im-transfer.create", in: "body", field: "im_channel_type",
		want: []string{"1", "2", "5"},
		why: "NOT backend-enforced — octo-drive declares im_channel_type as a bare uint8 " +
			"(internal/modules/imtransfer/api.go) and forwards it unchecked to octo-server, and its own " +
			"routing branches only on person-vs-rest. The values come from octo-lib/common ChannelType " +
			"(None=0, Person=1, Group=2, CustomerService=3, Community=4, CommunityTopic=5, Info=6); the " +
			"CLI deliberately offers the three IM transfer supports — DM, group, community topic (sub-thread). " +
			"This is the only entry here with no backend validation to compare against, so widening it is a " +
			"product decision rather than a transcription fix",
	},

	// --- docs ---
	{op: "docs.members.set", in: "body", field: "role",
		want: []string{"reader", "commenter", "writer", "admin"},
		why:  "octo-docs-backend origin/main src/permission/role.ts isMemberRole; the route's own 400 text is \"role must be reader|commenter|writer|admin\" (src/api/routes/members.ts)"},
	{op: "docs.forward-grant", in: "body", field: "role",
		want: []string{"reader", "commenter", "writer"},
		why:  "octo-docs-backend origin/main src/permission/role.ts isForwardGrantRole (= Role minus admin); route 400 text \"role must be reader|commenter|writer\""},
	{op: "docs.search", in: "body[]", field: "docType",
		want: []string{"doc", "sheet", "board", "html", "html_ppt"},
		why:  "octo-docs-backend origin/main src/db/docType.ts DOC_TYPES, self-described as \"the SINGLE source of truth\"; html_ppt is a first-class Bento slide-deck kind, not a variant of html"},
	{op: "docs.list", in: "query", field: "sort",
		want: []string{"updatedAt:asc", "updatedAt:desc"},
		why:  "octo-docs-backend list route sort whitelist; an unknown value is coerced rather than rejected server-side, so the local gate is at worst equally strict"},
	{op: "docs.scene.export", in: "query", field: "format",
		want: []string{"png", "svg"},
		why:  "octo-docs-backend scene export supports exactly these two renderers"},
	{op: "docs.share.set", in: "body", field: "shareScope",
		want: []string{"anyone_in_space", "restricted"},
		why:  "octo-docs-backend share scope enum"},
	{op: "docs.share.set", in: "body", field: "shareRole",
		want: []string{"read", "edit"},
		why:  "octo-docs-backend share role enum; distinct from the member Role ladder and deliberately only two values"},
	{op: "docs.versions.list", in: "query", field: "kind",
		want: []string{"all", "auto", "manual"},
		why:  "octo-docs-backend version kind filter"},

	// --- html ---
	{op: "html.grant.add", in: "body", field: "role",
		want: []string{"reader", "commenter", "writer"},
		why:  "octo-docs-html internal/service/grants.go AddGrant: roleLabelToCode accepts reader|commenter|writer|admin and AddGrant then refuses admin, so the grantable set is these three; the 400 text is literally \"role must be reader|commenter|writer\""},
	{op: "html.publish", in: "body", field: "mount_type",
		want: []string{"space", "group", "thread"},
		why:  "octo-docs-html publish mount kinds"},
	{op: "html.draft.create", in: "body", field: "mount_type",
		want: []string{"space", "group", "thread"},
		why:  "octo-docs-html canonical draft creation uses the same mount kinds as publish"},
	{op: "html.reply", in: "body", field: "status",
		want: []string{"applied", "partial", "question"},
		why:  "octo-docs-html reply status enum"},

	// --- marketplace ---
	{op: "marketplace.expert_category.list", in: "query", field: "kind",
		want: []string{"agent", "squad"},
		why: "octo-marketplace origin/feat/expert-marketplace@448636d internal/api/handler/expert/handler.go " +
			"ListCategories: `if c.Query(\"kind\") == \"squad\"` selects squads, any other value (including absent) " +
			"means agent — two values cover every input the backend distinguishes"},
	{op: "marketplace.expert_tag.list", in: "query", field: "kind",
		want: []string{"agent", "squad"},
		why:  "same kind branch in ListTags (handler.go:807) as expert_category.list"},
	{op: "marketplace.expert_tag.list", in: "query", field: "mode",
		want: []string{"all", "mine"},
		why: "octo-marketplace origin/feat/expert-marketplace@448636d internal/api/handler/expert/handler.go " +
			"ListTags: `c.Query(\"mode\") == \"mine\"` restricts to caller-owned rows, any other value means all — " +
			"mirrors the pinned mcp_category.list mode pair"},
	{op: "mcp_category.list", in: "query", field: "mode",
		want: []string{"all", "mine"},
		why:  "marketplace swagger binding tag; generated from the backend's own definition"},
	{op: "mcp.create", in: "body", field: "visibility",
		want: []string{"public", "private"},
		why: "octo-marketplace internal/service/mcp.go validatePublicCreateVisibility accepts only \"\"|public|private. " +
			"model.Visibility has four values, but internal/model/mcp.go says \"system never appears in a client write\" " +
			"and VisibilitySpace is unused by the mcp service — so the two-value set is right for an mcp write even " +
			"though skill's is three"},
	{op: "mcp.update", in: "body", field: "visibility",
		want: []string{"public", "private"},
		why:  "same validatePublicCreateVisibility gate as mcp.create; a system row additionally refuses any visibility patch"},
	{op: "mcp.create", in: "body", field: "transport",
		want: []string{"stdio", "streamable-http", "sse"},
		why:  "octo-marketplace internal/model/mcp.go ValidTransport — exactly these three"},
	{op: "mcp.update", in: "body", field: "transport",
		want: []string{"stdio", "streamable-http", "sse"},
		why:  "same ValidTransport gate"},
	{op: "mcp.probe", in: "body", field: "transport",
		want: []string{"stdio", "streamable-http", "sse"},
		why:  "same ValidTransport gate"},
	{op: "skill.publish", in: "body", field: "visibility",
		want: []string{"public", "private", "space"},
		why: "octo-marketplace model.Visibility minus system (never a client write); the skill service does use " +
			"VisibilitySpace (internal/service/skill/service.go), which is why skill's set is wider than mcp's"},
	{op: "skill.update", in: "body", field: "visibility",
		want: []string{"public", "private", "space"},
		why:  "same set as skill.publish"},
	{op: "skill.publish", in: "body", field: "publish_mode",
		want: []string{"create"},
		why: "backend-enforced single value: octo-marketplace internal/api/handler/upload/handler.go rejects anything " +
			"else with \"only publish_mode=create is supported\". A one-value enum is the shape that made " +
			"html.grant.add wrong, so this one was checked at source rather than assumed"},

	// --- message ---
	{op: "message.search", in: "body", field: "sort",
		want: []string{"time_desc", "time_asc", "relevance"},
		why:  "dmworkim search sort options"},
	{op: "message.search.all", in: "body", field: "sort",
		want: []string{"time_desc", "time_asc", "relevance"},
		why:  "same sort options as message.search"},
	{op: "message.search.files", in: "body", field: "sort",
		want: []string{"time_desc", "time_asc", "relevance"},
		why:  "same sort options as message.search"},
	{op: "message.search.media", in: "body", field: "sort",
		want: []string{"time_desc", "time_asc"},
		why:  "relevance is excluded on purpose: search_media.go hardcodes allowRelevance=false and media search refuses a keyword, so there is nothing to rank on"},
}

// TestEnum_EveryRequestSideVocabularyIsPinned is the completeness half. It walks
// the registry rather than the table, so a vocabulary nobody transcribed fails.
func TestEnum_EveryRequestSideVocabularyIsPinned(t *testing.T) {
	pinned := map[string]vocabulary{}
	for _, v := range requestSideVocabularies {
		key := v.op + "|" + v.in + "|" + v.field
		if _, dup := pinned[key]; dup {
			t.Fatalf("duplicate table entry for %s", key)
		}
		if strings.TrimSpace(v.why) == "" {
			t.Errorf("%s has no provenance; record the backend definition its values came from", key)
		}
		pinned[key] = v
	}

	found := map[string][]string{}
	for _, got := range discoverRequestSideEnums(t) {
		key := got.op + "|" + got.in + "|" + got.field
		found[key] = got.want
		want, ok := pinned[key]
		if !ok {
			t.Errorf("%s %s %s declares enum %v but is not pinned in requestSideVocabularies.\n"+
				"The enum is a hard local gate: a set narrower than the backend's turns a working call into "+
				"exit 2 with no way through except `octo-cli api`. Transcribe the backend's set (from a REMOTE "+
				"ref — a stale checkout confirms stale vocabularies) and add an entry with provenance.",
				got.op, got.in, got.field, got.want)
			continue
		}
		if !sameSet(got.want, want.want) {
			t.Errorf("%s %s %s enum = %v, want %v\nprovenance: %s",
				got.op, got.in, got.field, got.want, want.want, want.why)
		}
	}

	for key := range pinned {
		if _, ok := found[key]; !ok {
			t.Errorf("requestSideVocabularies pins %s but no such request-side enum exists in the registry; "+
				"the entry is stale — remove it or fix the spec", key)
		}
	}
}

// TestEnum_NoEnabledVocabularyIsNarrowerThanItsBackend documents the four round-8
// widenings explicitly, separately from the table above.
//
// The table would catch a regression on any of them, but only as "values drifted".
// These four were each a working call that this PR's gate started rejecting, and
// naming them keeps the reason legible: whoever narrows one of these again should
// have to delete an assertion that says why it is wrong.
func TestEnum_NoEnabledVocabularyIsNarrowerThanItsBackend(t *testing.T) {
	mustAccept := []struct {
		op, in, field, value, backend string
	}{
		{"docs.members.set", "body", "role", "commenter",
			"role.ts isMemberRole accepts it; it is a stored role with rank 20, deliberately between reader and writer"},
		{"docs.forward-grant", "body", "role", "commenter",
			"role.ts isForwardGrantRole accepts it"},
		{"docs.search", "body[]", "docType", "html_ppt",
			"docType.ts DOC_TYPES lists it as a first-class document kind with its own /api/v1/ppt routes"},
		{"html.grant.add", "body", "role", "writer",
			"grants.go AddGrant accepts reader|commenter|writer"},
		{"html.grant.add", "body", "role", "commenter",
			"grants.go AddGrant accepts reader|commenter|writer"},
	}
	byKey := map[string][]string{}
	for _, got := range discoverRequestSideEnums(t) {
		byKey[got.op+"|"+got.in+"|"+got.field] = got.want
	}
	for _, tc := range mustAccept {
		t.Run(tc.op+"/"+tc.field+"/"+tc.value, func(t *testing.T) {
			key := tc.op + "|" + tc.in + "|" + tc.field
			values, ok := byKey[key]
			if !ok {
				t.Fatalf("%s has no %s enum on %s any more; this test is out of date", tc.op, tc.field, tc.in)
			}
			for _, v := range values {
				if v == tc.value {
					return
				}
			}
			t.Errorf("%s %s excludes %q, so the local gate rejects a call the backend accepts.\nbackend: %s\ncurrent set: %v",
				tc.op, tc.field, tc.value, tc.backend, values)
		})
	}
}

// discoverRequestSideEnums walks every enabled spec and returns each request-side
// enum it declares. Response schemas are excluded because checkEnum has no call
// site that can reach one.
func discoverRequestSideEnums(t *testing.T) []vocabulary {
	t.Helper()
	reg := registry.MustNew()
	var out []vocabulary

	for _, svc := range reg.ListServices() {
		if reg.ServiceDisabled(svc) {
			continue
		}
		for _, info := range reg.ListOperations(svc) {
			d, ok := reg.GetOperation(info.ID)
			if !ok {
				continue
			}
			for i := range d.Parameters {
				p := &d.Parameters[i]
				if len(p.Enum) > 0 {
					out = append(out, vocabulary{op: info.ID, in: p.In, field: p.Name, want: enumStrings(t, info.ID, p.Name, p.Enum)})
				}
				if p.Items != nil && len(p.Items.Enum) > 0 {
					out = append(out, vocabulary{op: info.ID, in: p.In + "[]", field: p.Name, want: enumStrings(t, info.ID, p.Name, p.Items.Enum)})
				}
			}
			if d.RequestBody != nil {
				out = append(out, bodyEnums(t, info.ID, d.RequestBody, "")...)
			}
		}
	}
	return out
}

// bodyEnums recurses into the request schema, so an enum on a nested property is
// discovered too — the walker enforces those, so the guard has to see them.
func bodyEnums(t *testing.T, opID string, schema *registry.SchemaInfo, path string) []vocabulary {
	t.Helper()
	var out []vocabulary
	for name := range schema.Properties {
		prop := schema.Properties[name]
		field := name
		if path != "" {
			field = path + "." + name
		}
		if len(prop.Enum) > 0 {
			out = append(out, vocabulary{op: opID, in: "body", field: field, want: enumStrings(t, opID, field, prop.Enum)})
		}
		if prop.Items != nil && len(prop.Items.Enum) > 0 {
			out = append(out, vocabulary{op: opID, in: "body[]", field: field, want: enumStrings(t, opID, field, prop.Items.Enum)})
		}
		if prop.Type == "object" {
			out = append(out, bodyEnums(t, opID, &prop, field)...)
		}
		// An array of objects: the production walker enforces enums on the item
		// schema's properties (validateArray → validate → validateObject), so a
		// vocabulary declared there is a live hard gate. Without this branch the
		// completeness guard is blind to exactly the class it exists to catch.
		if prop.Items != nil && prop.Items.Type == "object" {
			out = append(out, bodyEnums(t, opID, prop.Items, field+"[]")...)
		}
	}
	return out
}

// enumStrings renders enum members as the decimal / literal text the gate
// compares by, so a numeric vocabulary is pinnable in the same table as a string
// one.
func enumStrings(t *testing.T, opID, field string, values []any) []string {
	t.Helper()
	out := make([]string, 0, len(values))
	for _, v := range values {
		switch val := v.(type) {
		case string:
			out = append(out, val)
		case json.Number:
			out = append(out, val.String())
		case float64:
			out = append(out, strings.TrimSuffix(fmt.Sprintf("%v", val), ".0"))
		case bool:
			out = append(out, fmt.Sprintf("%t", val))
		default:
			t.Fatalf("%s %s: enum member %#v has an unpinnable type %T", opID, field, v, v)
		}
	}
	return out
}

func sameSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	x := append([]string(nil), a...)
	y := append([]string(nil), b...)
	sort.Strings(x)
	sort.Strings(y)
	for i := range x {
		if x[i] != y[i] {
			return false
		}
	}
	return true
}

// TestEnum_BodyWalkerFindsAnEnumInsideAnArrayOfObjects covers the walker itself
// rather than the registry, because no spec currently declares such an enum. The
// production validator does enforce one (validateArray → validate → validateObject),
// so without the array-of-object branch in bodyEnums the completeness guard above
// would be blind to the first spec that adds one — a new hard gate would ship with
// nobody having transcribed the backend's vocabulary.
//
// A synthetic schema is the only way to assert this today. Waiting for a real one is
// exactly the order that lets the gap through.
func TestEnum_BodyWalkerFindsAnEnumInsideAnArrayOfObjects(t *testing.T) {
	schema := &registry.SchemaInfo{
		Type: "object",
		Properties: map[string]registry.SchemaInfo{
			"filters": {
				Type: "array",
				Items: &registry.SchemaInfo{
					Type: "object",
					Properties: map[string]registry.SchemaInfo{
						"op": {Type: "string", Enum: []any{"eq", "neq"}},
					},
				},
			},
		},
	}

	found := bodyEnums(t, "synthetic.op", schema, "")
	for _, v := range found {
		if v.field == "filters[].op" {
			if !sameSet(v.want, []string{"eq", "neq"}) {
				t.Errorf("discovered filters[].op = %v, want [eq neq]", v.want)
			}
			return
		}
	}
	t.Errorf("bodyEnums did not discover the enum on filters[].op; found %v.\n"+
		"An enum on an array item's property is enforced by the walker, so the guard must see it.", found)
}
