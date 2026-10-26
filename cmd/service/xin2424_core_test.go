package service

import (
	"encoding/json"
	"io"
	"net/http"
	"sort"
	"strings"
	"testing"

	"github.com/Mininglamp-OSS/octo-cli/internal/registry"
)

type xin2424CoreRow struct {
	row         string
	op          string
	method      string
	path        string
	risk        string
	pathParams  []string
	reqQuery    []string
	reqBody     []string
	hasBody     bool
	bodyNeeded  bool
	multipart   bool
	binaryResp  bool
	spaceHeader bool
}

var xin2424CoreRows = []xin2424CoreRow{
	{row: "core#10", op: "bot.resolve-targets", method: http.MethodGet, path: "/v1/bot/resolve/targets", risk: "read", reqQuery: []string{"name"}},
	{row: "core#11", op: "bot.users-batch", method: http.MethodPost, path: "/v1/bot/users/batch", risk: "read", reqBody: []string{"uids"}, hasBody: true, bodyNeeded: true},
	{row: "core#12", op: "group.mention-pref", method: http.MethodGet, path: "/v1/bot/groups/{group_no}/mention_pref", risk: "read", pathParams: []string{"group_no"}},
	{row: "core#14", op: "bot.space-principal", method: http.MethodGet, path: "/v1/bot/space/principals/{uid}", risk: "read", pathParams: []string{"uid"}, reqQuery: []string{"space_id"}},
	{row: "core#22", op: "message.card-revisions-clear", method: http.MethodPost, path: "/v1/bot/message/card/revisions/clear", risk: "high-risk-write", reqBody: []string{"channel_id", "channel_type", "message_id"}, hasBody: true, bodyNeeded: true, spaceHeader: true},
	{row: "core#23", op: "bot.card-profile", method: http.MethodGet, path: "/v1/bot/card/profile", risk: "read"},
	{row: "core#25", op: "bot.obo-grant", method: http.MethodGet, path: "/v1/bot/obo-grant", risk: "read"},
	{row: "core#26", op: "bot.obo-grants-self", method: http.MethodGet, path: "/v1/bot/obo-grants/self", risk: "read", reqQuery: []string{"space_id"}},
	{row: "core#27", op: "message.group-get", method: http.MethodGet, path: "/v1/bot/groups/{group_no}/messages/{message_id}", risk: "read", pathParams: []string{"group_no", "message_id"}, spaceHeader: true},
	{row: "core#28", op: "message.person-get", method: http.MethodGet, path: "/v1/bot/messages/person/{peer_uid}/{message_id}", risk: "read", pathParams: []string{"message_id", "peer_uid"}, spaceHeader: true},
	{row: "core#29", op: "message.thread-get", method: http.MethodGet, path: "/v1/bot/groups/{group_no}/threads/{short_id}/messages/{message_id}", risk: "read", pathParams: []string{"group_no", "message_id", "short_id"}, spaceHeader: true},
	{row: "core#41", op: "thread.archive", method: http.MethodPost, path: "/v1/bot/groups/{group_no}/threads/{short_id}/archive", risk: "write", pathParams: []string{"group_no", "short_id"}},
	{row: "core#42", op: "group.webhook.create", method: http.MethodPost, path: "/v1/bot/groups/{group_no}/incoming-webhooks", risk: "high-risk-write", pathParams: []string{"group_no"}, hasBody: true, bodyNeeded: true},
	{row: "core#43", op: "group.webhook.list", method: http.MethodGet, path: "/v1/bot/groups/{group_no}/incoming-webhooks", risk: "read", pathParams: []string{"group_no"}},
	{row: "core#44", op: "group.webhook.regenerate", method: http.MethodPost, path: "/v1/bot/groups/{group_no}/incoming-webhooks/{webhook_id}/regenerate", risk: "high-risk-write", pathParams: []string{"group_no", "webhook_id"}},
	{row: "core#45", op: "group.webhook.deliveries", method: http.MethodGet, path: "/v1/bot/groups/{group_no}/incoming-webhooks/{webhook_id}/deliveries", risk: "read", pathParams: []string{"group_no", "webhook_id"}},
	{row: "core#46", op: "group.webhook.test", method: http.MethodPost, path: "/v1/bot/groups/{group_no}/incoming-webhooks/{webhook_id}/test", risk: "write", pathParams: []string{"group_no", "webhook_id"}},
	{row: "core#47", op: "thread.webhook.create", method: http.MethodPost, path: "/v1/bot/groups/{group_no}/threads/{short_id}/incoming-webhooks", risk: "high-risk-write", pathParams: []string{"group_no", "short_id"}, hasBody: true, bodyNeeded: true},
	{row: "core#48", op: "thread.webhook.list", method: http.MethodGet, path: "/v1/bot/groups/{group_no}/threads/{short_id}/incoming-webhooks", risk: "read", pathParams: []string{"group_no", "short_id"}},
	{row: "core#49", op: "thread.webhook.regenerate", method: http.MethodPost, path: "/v1/bot/groups/{group_no}/threads/{short_id}/incoming-webhooks/{webhook_id}/regenerate", risk: "high-risk-write", pathParams: []string{"group_no", "short_id", "webhook_id"}},
	{row: "core#50", op: "thread.webhook.deliveries", method: http.MethodGet, path: "/v1/bot/groups/{group_no}/threads/{short_id}/incoming-webhooks/{webhook_id}/deliveries", risk: "read", pathParams: []string{"group_no", "short_id", "webhook_id"}},
	{row: "core#51", op: "thread.webhook.test", method: http.MethodPost, path: "/v1/bot/groups/{group_no}/threads/{short_id}/incoming-webhooks/{webhook_id}/test", risk: "write", pathParams: []string{"group_no", "short_id", "webhook_id"}},
	{row: "core#66", op: "thread.delete", method: http.MethodDelete, path: "/v1/bot/groups/{group_no}/threads/{short_id}", risk: "high-risk-write", pathParams: []string{"group_no", "short_id"}},
	{row: "core#72", op: "group.webhook.update", method: http.MethodPut, path: "/v1/bot/groups/{group_no}/incoming-webhooks/{webhook_id}", risk: "write", pathParams: []string{"group_no", "webhook_id"}, hasBody: true, bodyNeeded: true},
	{row: "core#73", op: "group.webhook.delete", method: http.MethodDelete, path: "/v1/bot/groups/{group_no}/incoming-webhooks/{webhook_id}", risk: "high-risk-write", pathParams: []string{"group_no", "webhook_id"}},
	{row: "core#74", op: "thread.webhook.update", method: http.MethodPut, path: "/v1/bot/groups/{group_no}/threads/{short_id}/incoming-webhooks/{webhook_id}", risk: "write", pathParams: []string{"group_no", "short_id", "webhook_id"}, hasBody: true, bodyNeeded: true},
	{row: "core#75", op: "thread.webhook.delete", method: http.MethodDelete, path: "/v1/bot/groups/{group_no}/threads/{short_id}/incoming-webhooks/{webhook_id}", risk: "high-risk-write", pathParams: []string{"group_no", "short_id", "webhook_id"}},
}

func xin2424Required(d *registry.OperationDetail, in string) []string {
	var out []string
	for i := range d.Parameters {
		if p := &d.Parameters[i]; p.In == in && p.Required {
			out = append(out, p.Name)
		}
	}
	sort.Strings(out)
	return out
}

func xin2424Equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestXIN2424CoreRegistryRows(t *testing.T) {
	reg := registry.MustNew()
	if len(xin2424CoreRows) != 27 {
		t.Fatalf("fixture rows = %d, want 27", len(xin2424CoreRows))
	}
	for _, want := range xin2424CoreRows {
		t.Run(want.row+"_"+want.op, func(t *testing.T) {
			d, ok := reg.GetOperation(want.op)
			if !ok {
				t.Fatalf("operation %s is not registered", want.op)
			}
			if d.Method != want.method || d.Path != want.path || d.Risk != want.risk {
				t.Errorf("got %s %s risk=%s, want %s %s risk=%s", d.Method, d.Path, d.Risk, want.method, want.path, want.risk)
			}
			if d.BaseURLEnv != "OCTO_API_BASE_URL" {
				t.Errorf("base url env = %q", d.BaseURLEnv)
			}
			if d.Multipart != want.multipart || d.BinaryResponse != want.binaryResp {
				t.Errorf("multipart=%v binary=%v, want %v/%v", d.Multipart, d.BinaryResponse, want.multipart, want.binaryResp)
			}
			if d.SpaceHeader != want.spaceHeader {
				t.Errorf("space header = %v, want %v", d.SpaceHeader, want.spaceHeader)
			}
			if got := xin2424Required(d, "path"); !xin2424Equal(got, want.pathParams) {
				t.Errorf("required path params = %v, want %v", got, want.pathParams)
			}
			if got := xin2424Required(d, "query"); !xin2424Equal(got, want.reqQuery) {
				t.Errorf("required query params = %v, want %v", got, want.reqQuery)
			}
			if (d.RequestBody != nil) != want.hasBody || d.RequestBodyRequired != want.bodyNeeded {
				t.Fatalf("request body present=%v required=%v, want %v/%v", d.RequestBody != nil, d.RequestBodyRequired, want.hasBody, want.bodyNeeded)
			}
			if d.RequestBody != nil {
				got := append([]string(nil), d.RequestBody.Required...)
				sort.Strings(got)
				if !xin2424Equal(got, want.reqBody) {
					t.Errorf("required body fields = %v, want %v", got, want.reqBody)
				}
			}
			if d.ResponseSchema == nil && want.method != http.MethodDelete && want.op != "thread.archive" {
				t.Errorf("missing 2xx response schema")
			}
		})
	}
}

func TestXIN2424CoreBodyConstraints(t *testing.T) {
	reg := registry.MustNew()
	batch, _ := reg.GetOperation("bot.users-batch")
	if uids := batch.RequestBody.Properties["uids"]; uids.MinItems != 1 || uids.MaxItems != 200 || uids.Items == nil || uids.Items.MaxLength != 64 {
		t.Errorf("users-batch uids bounds = %+v", uids)
	}
	for _, id := range []string{"group.webhook.create", "thread.webhook.create"} {
		d, _ := reg.GetOperation(id)
		if name := d.RequestBody.Properties["name"]; name.MaxLength != 64 {
			t.Errorf("%s name maxLength = %d, want 64", id, name.MaxLength)
		}
		if len(d.RequestBody.Required) != 0 {
			t.Errorf("%s: backend auto-names an empty webhook, so no body field is required; got %v", id, d.RequestBody.Required)
		}
		for _, f := range []string{"avatar", "mention_uids", "allow_mention_all", "allow_mention_bots"} {
			if _, ok := d.RequestBody.Properties[f]; !ok {
				t.Errorf("%s missing body field %s", id, f)
			}
		}
		if _, ok := d.ResponseSchema.Properties["token"]; !ok {
			t.Errorf("%s response must describe the one-time token", id)
		}
	}
	for _, id := range []string{"group.webhook.update", "thread.webhook.update"} {
		d, _ := reg.GetOperation(id)
		if st := d.RequestBody.Properties["status"]; len(st.Enum) != 2 {
			t.Errorf("%s status enum = %v", id, st.Enum)
		}
	}
	for _, id := range []string{"group.webhook.deliveries", "thread.webhook.deliveries"} {
		d, _ := reg.GetOperation(id)
		var limit *registry.ParamInfo
		for i := range d.Parameters {
			if d.Parameters[i].Name == "limit" {
				limit = &d.Parameters[i]
			}
		}
		if limit == nil || limit.In != "query" || limit.Required {
			t.Errorf("%s limit param = %+v", id, limit)
		}
	}
}

func TestXIN2424CoreRequestAssembly(t *testing.T) {
	type hit struct {
		method, path, query, body string
	}
	cases := []struct {
		name string
		args []string
		want hit
	}{
		{
			name: "thread webhook create",
			args: []string{"thread", "webhook", "create", "g1", "s1", "--data", `{"name":"ci","mention_uids":["u1"]}`},
			want: hit{method: http.MethodPost, path: "/v1/bot/groups/g1/threads/s1/incoming-webhooks", body: `"mention_uids":["u1"]`},
		},
		{
			name: "group webhook regenerate",
			args: []string{"group", "webhook", "regenerate", "g1", "w1"},
			want: hit{method: http.MethodPost, path: "/v1/bot/groups/g1/incoming-webhooks/w1/regenerate"},
		},
		{
			name: "obo grants self",
			args: []string{"bot", "obo-grants-self", "--space-id", "sp1"},
			want: hit{method: http.MethodGet, path: "/v1/bot/obo-grants/self", query: "space_id=sp1"},
		},
		{
			name: "users batch",
			args: []string{"bot", "users-batch", "--data", `{"uids":["a","b"]}`},
			want: hit{method: http.MethodPost, path: "/v1/bot/users/batch", body: `"uids":["a","b"]`},
		},
		{
			name: "thread delete",
			args: []string{"thread", "delete", "g1", "s1"},
			want: hit{method: http.MethodDelete, path: "/v1/bot/groups/g1/threads/s1"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var got hit
			root, _, _ := rootWithService(t, func(w http.ResponseWriter, r *http.Request) {
				b, _ := io.ReadAll(r.Body)
				got = hit{method: r.Method, path: r.URL.Path, query: r.URL.RawQuery, body: string(b)}
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]any{"status": 200})
			})
			root.SetArgs(tc.args)
			if err := root.Execute(); err != nil {
				t.Fatalf("execute: %v", err)
			}
			if got.method != tc.want.method || got.path != tc.want.path {
				t.Fatalf("got %s %s, want %s %s", got.method, got.path, tc.want.method, tc.want.path)
			}
			if tc.want.query != "" && got.query != tc.want.query {
				t.Errorf("query = %q, want %q", got.query, tc.want.query)
			}
			if tc.want.body != "" && !strings.Contains(got.body, tc.want.body) {
				t.Errorf("body = %s, want it to contain %s", got.body, tc.want.body)
			}
		})
	}
}
