package client

import (
	"strings"

	"github.com/Mininglamp-OSS/octo-cli/internal/credential"
	"github.com/Mininglamp-OSS/octo-cli/internal/output"
)

// searchBotPrefix is the bot-mounted path prefix for the message-search family.
// User API keys (uk_) search the same endpoints under a real-person mount, so
// their requests are rewritten to searchUserPrefix.
const (
	searchBotPrefix  = "/v1/bot/messages/"
	searchUserPrefix = "/v1/user/messages/"
)

// searchGlobalFallback maps the in-scope search suffixes to their cross-session
// global counterparts. When a request in this set carries no channel_id in its
// body, the suffix is rewritten to the global endpoint. Suffixes not listed
// here (media / around / _search_global_*) are fixed endpoints and never
// rerouted by chat-id.
var searchGlobalFallback = map[string]string{
	"_search":       "_search_global_messages",
	"_search_all":   "_search_global_messages",
	"_search_files": "_search_global_files",
}

// bodyHasChannelID reports whether the assembled request body carries a
// non-empty channel_id. Used to decide in-scope (has channel_id) vs
// cross-session global (no channel_id) routing for the search family.
func bodyHasChannelID(body any) bool {
	m, ok := body.(map[string]any)
	if !ok {
		return false
	}
	v, ok := m["channel_id"].(string)
	return ok && strings.TrimSpace(v) != ""
}

// routeSearchPath adjusts a message-search request path for chat-id scope and
// the active token kind. It only touches the search family — paths under
// /v1/bot/messages/ whose suffix starts with "_search" (message.sync and other
// non-search ops are left alone).
//
// First, chat-id routing: if the suffix is one of the in-scope search endpoints
// (_search / _search_all / _search_files) and the request body carries no
// channel_id, the suffix is rewritten to its cross-session global counterpart
// (_search_global_messages / _search_global_files). Fixed endpoints (media,
// around, _search_global_*) are never rerouted this way.
//
// Then, token-kind routing:
//
//   - user_bot (bf_):  unchanged — bot searches under /v1/bot/messages.
//   - user_key (uk_):  rewritten to /v1/user/messages/_search* (real-person mount).
//   - app_bot  (app_): rejected — App Bots cannot search.
//   - anything else (unknown kind, nil credential, empty token): passed through
//     unchanged, leaving any failure to the backend.
//
// Non-search paths are always returned unchanged.
func routeSearchPath(cred *credential.BotCredential, path string, body any) (string, error) {
	suffix, ok := strings.CutPrefix(path, searchBotPrefix)
	if !ok || !strings.HasPrefix(suffix, "_search") {
		return path, nil
	}

	// chat-id scope: in-scope search without a channel_id → cross-session global.
	if global, ok := searchGlobalFallback[suffix]; ok && !bodyHasChannelID(body) {
		suffix = global
	}
	path = searchBotPrefix + suffix

	if cred == nil {
		return path, nil
	}
	switch credential.TokenKind(cred.Token) {
	case "user_key":
		return searchUserPrefix + suffix, nil
	case "app_bot":
		return "", output.ErrValidation(
			"App Bot tokens cannot search messages",
			"use a User Bot (bf_) or user API key (uk_) token",
		)
	default:
		// user_bot, unknown, or empty token: leave the path as-is.
		return path, nil
	}
}
