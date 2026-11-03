package mcp

import (
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"github.com/Mininglamp-OSS/octo-cli/cmd/service"
	"github.com/Mininglamp-OSS/octo-cli/internal/registry"
)

var requestArgumentFields = map[string][]string{
	"message.send":         {"channel_id", "channel_type"},
	"message.edit":         {"channel_id", "channel_type", "message_id"},
	"message.sync":         {"channel_id", "channel_type", "start_message_seq", "end_message_seq", "limit", "pull_mode"},
	"message.read-receipt": {"channel_id", "channel_type", "message_ids"},
	"message.group-get":    {"group_no", "message_id"}, "message.person-get": {"peer_uid", "message_id"}, "message.thread-get": {"group_no", "short_id", "message_id"},
	"group.list": {"space_id"}, "group.get": {"group_no"}, "group.members": {"group_no"}, "group.md-get": {"group_no"}, "group.md-update": {"group_no"},
	"thread.get": {"group_no", "short_id"}, "thread.delete": {"group_no", "short_id"}, "thread.archive": {"group_no", "short_id"}, "thread.members": {"group_no", "short_id"}, "thread.join": {"group_no", "short_id"}, "thread.leave": {"group_no", "short_id"}, "thread.md-get": {"group_no", "short_id"}, "thread.md-update": {"group_no", "short_id"},
	"thread.list": {"group_no", "page_index", "page_size"}, "thread.create": {"group_no", "source_message_id"}, "bot.typing": {"channel_id", "channel_type"}, "bot.space-members": {"space_id"},
}

func secretIn(s string, secrets []string) bool {
	for _, v := range secrets {
		if v != "" && strings.Contains(s, v) {
			return true
		}
	}
	return false
}
func redactLogValue(raw json.RawMessage, secrets []string) json.RawMessage {
	var s string
	if json.Unmarshal(raw, &s) == nil && secretIn(s, secrets) {
		return json.RawMessage(`"***REDACTED***"`)
	}
	var a []string
	if json.Unmarshal(raw, &a) == nil {
		for i, v := range a {
			if secretIn(v, secrets) {
				a[i] = "***REDACTED***"
			}
		}
		b, err := json.Marshal(a)
		if err != nil {
			return nil
		}
		return b
	}
	return append(json.RawMessage(nil), raw...)
}

func validLoggedArgument(key string, raw json.RawMessage, typ string) bool {
	if key == "message_ids" {
		var a []string
		if json.Unmarshal(raw, &a) != nil || len(a) > 100 {
			return false
		}
		for _, v := range a {
			if !positiveDecimal(v, 63) {
				return false
			}
		}
		return true
	}
	if typ == "integer" {
		s := string(raw)
		if len(s) > 20 || strings.ContainsAny(s, ".eE+\"") {
			return false
		}
		_, e := strconv.ParseInt(s, 10, 64)
		return e == nil
	}
	if typ != "string" {
		return false
	}
	var s string
	if json.Unmarshal(raw, &s) != nil {
		return false
	}
	switch key {
	case "message_id", "source_message_id":
		return positiveDecimal(s, 63)
	case "space_id", "group_no", "peer_uid":
		return validID(s, 40)
	case "short_id":
		return positiveDecimal(s, 63)
	case "channel_id":
		return validID(s, 256)
	}
	return false
}

func projectRequest(s *Server, req rpcRequest, p toolCallParams, uid, token string, source *DeclaredSource, received time.Time) (RequestRecord, error) { //nolint:gocritic,gocyclo // immutable request snapshots are copied at this boundary; construct one frozen request record and redact every permitted identity leaf
	id, err := randomUUID()
	if err != nil {
		return RequestRecord{}, logUnavailable()
	}
	r := RequestRecord{ReceivedAt: received.UTC().Format(time.RFC3339Nano), RequestID: id, BotUID: uid, Tool: p.Name, DeclaredSource: source, RequestedArguments: map[string]json.RawMessage{}}
	secrets := []string{token}
	args, parseErr := strictObject(p.Arguments)
	if parseErr != nil {
		args = nil
	}
	var operation string
	if err := json.Unmarshal(args["operation_id"], &operation); err != nil {
		operation = ""
	}
	detail, known := s.reg.GetOperation(operation)
	var business map[string]json.RawMessage
	if p.Name == "call_op" || p.Name == "execute" {
		business, parseErr = strictObject(args["arguments"])
		if parseErr != nil {
			business = nil
		}
	}
	if known {
		r.OperationID = operation
		decoded, e := decodeArgs(args["arguments"])
		if e == nil && business != nil {
			found, e := service.CollectRequestSecrets(detail, decoded)
			if e != nil {
				return r, admissionFailure(-32602, "INVALID_ARGUMENTS")
			}
			secrets = append(secrets, found...)
		}
		for i := range detail.Parameters {
			param := &detail.Parameters[i]
			if param.Secret {
				var v string
				if json.Unmarshal(business[param.Name], &v) == nil {
					secrets = append(secrets, v)
				}
			}
		}
	}
	add := func(k string, v json.RawMessage) {
		if len(v) == 0 {
			return
		}
		r.RequestedArguments[k] = redactLogValue(v, secrets)
		b, err := json.Marshal(r.RequestedArguments)
		if err != nil || len(b) > 2048 {
			delete(r.RequestedArguments, k)
		}
	}
	projectToolArguments(s, p.Name, args, business, detail, add)

	if len(req.ID) == 0 || string(req.ID) == "null" {
		r.RPCID = RecordedRPCID{Type: "null"}
	} else if req.ID[0] == '"' {
		var v string
		if err := json.Unmarshal(req.ID, &v); err != nil {
			return r, admissionFailure(-32600, "INVALID_REQUEST")
		}
		if secretIn(v, secrets) {
			v = "***REDACTED***"
		}
		r.RPCID = RecordedRPCID{Type: "string", Value: &v}
	} else {
		r.RPCID = RecordedRPCID{Type: "number", Lexeme: string(req.ID)}
	}
	if source != nil {
		for _, v := range []string{source.Sender, source.Channel, source.SpaceID, source.MessageID} {
			if secretIn(v, secrets) {
				return r, contextFailure("params._meta.octo.im", "range")
			}
		}
	}
	// Directory tools do not promote arbitrary operation-looking strings.
	if p.Name != "call_op" && p.Name != "execute" && p.Name != "describe_op" && p.Name != "get_skill" {
		r.OperationID = ""
	}
	return r, nil
}

func logFieldType(d *registry.OperationDetail, key string) (string, bool) {
	for i := range d.Parameters {
		p := &d.Parameters[i]
		if p.Name == key {
			return p.Type, p.Secret
		}
	}
	if d.RequestBody != nil {
		if p, ok := d.RequestBody.Properties[key]; ok {
			return p.Type, p.Secret || p.WriteOnly
		}
	}
	return "", true
}

func projectToolArguments(s *Server, tool string, args, business map[string]json.RawMessage, detail *registry.OperationDetail, add func(string, json.RawMessage)) { //nolint:gocyclo // explicit whitelist branches prevent a new operation from adding log fields
	known := detail != nil
	operation := ""
	if known {
		operation = detail.ID
	}
	knownService := func(v string) bool {
		for _, service := range s.reg.ListServices() {
			if service == v {
				return true
			}
		}
		return false
	}
	switch tool {
	case "search_ops", "get_skill":
		var domain string
		if json.Unmarshal(args["domain"], &domain) == nil && knownService(domain) {
			add("domain", args["domain"])
		}
		if tool == "get_skill" {
			for _, key := range []string{"intent", "depth"} {
				var v string
				if json.Unmarshal(args[key], &v) == nil && (key == "intent" && (v == "search" || v == "describe") || key == "depth" && (v == "summary" || v == "full")) {
					add(key, args[key])
				}
			}
			if known {
				add("operation_id", args["operation_id"])
			}
		}
	case "describe_op":
		if known {
			add("operation_id", args["operation_id"])
		}
	case "execute", "call_op":
		if tool == "execute" && (string(args["dry_run"]) == "true" || string(args["dry_run"]) == "false") {
			add("tool_options", json.RawMessage(`{"dry_run":`+string(args["dry_run"])+`}`))
		}
		if known && !detail.Multipart && !detail.RawBody && !detail.BinaryResponse {
			for _, key := range requestArgumentFields[operation] {
				typ, sensitive := logFieldType(detail, key)
				if !sensitive && validLoggedArgument(key, business[key], typ) {
					add(key, business[key])
				}
			}
		}
	}
}
