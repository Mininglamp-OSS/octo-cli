package client

import (
	"testing"

	"github.com/Mininglamp-OSS/octo-cli/internal/credential"
)

func TestRouteSearchPath(t *testing.T) {
	withChat := map[string]any{"channel_id": "c1"}
	blankChat := map[string]any{"channel_id": "  "}
	noChat := map[string]any{"keyword": "hi"}

	tests := []struct {
		name     string
		cred     *credential.BotCredential
		path     string
		body     any
		wantPath string
		wantErr  bool
	}{
		// --- chat-id scope, user_bot (bf_) ---
		{
			name:     "bf_ _search with channel_id stays in-scope",
			cred:     &credential.BotCredential{Token: "bf_abc"},
			path:     "/v1/bot/messages/_search",
			body:     withChat,
			wantPath: "/v1/bot/messages/_search",
		},
		{
			name:     "bf_ _search without channel_id → global messages",
			cred:     &credential.BotCredential{Token: "bf_abc"},
			path:     "/v1/bot/messages/_search",
			body:     noChat,
			wantPath: "/v1/bot/messages/_search_global_messages",
		},
		{
			name:     "bf_ _search with blank channel_id → global messages",
			cred:     &credential.BotCredential{Token: "bf_abc"},
			path:     "/v1/bot/messages/_search",
			body:     blankChat,
			wantPath: "/v1/bot/messages/_search_global_messages",
		},
		{
			name:     "bf_ _search_all with channel_id stays in-scope",
			cred:     &credential.BotCredential{Token: "bf_abc"},
			path:     "/v1/bot/messages/_search_all",
			body:     withChat,
			wantPath: "/v1/bot/messages/_search_all",
		},
		{
			name:     "bf_ _search_all without channel_id → global messages",
			cred:     &credential.BotCredential{Token: "bf_abc"},
			path:     "/v1/bot/messages/_search_all",
			body:     noChat,
			wantPath: "/v1/bot/messages/_search_global_messages",
		},
		{
			name:     "bf_ _search_files with channel_id stays in-scope",
			cred:     &credential.BotCredential{Token: "bf_abc"},
			path:     "/v1/bot/messages/_search_files",
			body:     withChat,
			wantPath: "/v1/bot/messages/_search_files",
		},
		{
			name:     "bf_ _search_files without channel_id → global files",
			cred:     &credential.BotCredential{Token: "bf_abc"},
			path:     "/v1/bot/messages/_search_files",
			body:     noChat,
			wantPath: "/v1/bot/messages/_search_global_files",
		},
		{
			name:     "nil body treated as no channel_id → global",
			cred:     &credential.BotCredential{Token: "bf_abc"},
			path:     "/v1/bot/messages/_search",
			body:     nil,
			wantPath: "/v1/bot/messages/_search_global_messages",
		},

		// --- chat-id scope + uk_ prefix rewrite ---
		{
			name:     "uk_ _search with channel_id → /v1/user in-scope",
			cred:     &credential.BotCredential{Token: "uk_abc"},
			path:     "/v1/bot/messages/_search",
			body:     withChat,
			wantPath: "/v1/user/messages/_search",
		},
		{
			name:     "uk_ _search without channel_id → /v1/user global messages",
			cred:     &credential.BotCredential{Token: "uk_abc"},
			path:     "/v1/bot/messages/_search",
			body:     noChat,
			wantPath: "/v1/user/messages/_search_global_messages",
		},
		{
			name:     "uk_ _search_files without channel_id → /v1/user global files",
			cred:     &credential.BotCredential{Token: "uk_abc"},
			path:     "/v1/bot/messages/_search_files",
			body:     noChat,
			wantPath: "/v1/user/messages/_search_global_files",
		},

		// --- app_ rejected regardless of chat-id ---
		{
			name:    "app_ _search with channel_id rejected",
			cred:    &credential.BotCredential{Token: "app_abc"},
			path:    "/v1/bot/messages/_search",
			body:    withChat,
			wantErr: true,
		},
		{
			name:    "app_ _search without channel_id rejected",
			cred:    &credential.BotCredential{Token: "app_abc"},
			path:    "/v1/bot/messages/_search",
			body:    noChat,
			wantErr: true,
		},

		// --- fixed endpoints unaffected by chat-id ---
		{
			name:     "media fixed endpoint, no channel_id, bf_",
			cred:     &credential.BotCredential{Token: "bf_abc"},
			path:     "/v1/bot/messages/_search_media",
			body:     noChat,
			wantPath: "/v1/bot/messages/_search_media",
		},
		{
			name:     "around fixed endpoint, no channel_id, uk_",
			cred:     &credential.BotCredential{Token: "uk_abc"},
			path:     "/v1/bot/messages/_search_around",
			body:     noChat,
			wantPath: "/v1/user/messages/_search_around",
		},
		{
			name:     "global_groups fixed endpoint preserved (uk_)",
			cred:     &credential.BotCredential{Token: "uk_abc"},
			path:     "/v1/bot/messages/_search_global_groups",
			body:     noChat,
			wantPath: "/v1/user/messages/_search_global_groups",
		},

		// --- token-kind passthrough branches (unchanged behaviour) ---
		{
			name:     "unknown kind passes through",
			cred:     &credential.BotCredential{Token: "xyz_abc"},
			path:     "/v1/bot/messages/_search",
			body:     withChat,
			wantPath: "/v1/bot/messages/_search",
		},
		{
			name:     "empty token passes through",
			cred:     &credential.BotCredential{Token: ""},
			path:     "/v1/bot/messages/_search",
			body:     withChat,
			wantPath: "/v1/bot/messages/_search",
		},
		{
			name:     "nil credential passes through (still resolves chat-id scope)",
			cred:     nil,
			path:     "/v1/bot/messages/_search",
			body:     noChat,
			wantPath: "/v1/bot/messages/_search_global_messages",
		},
		{
			name:     "non-search message path untouched (uk_)",
			cred:     &credential.BotCredential{Token: "uk_abc"},
			path:     "/v1/bot/messages/sync",
			body:     noChat,
			wantPath: "/v1/bot/messages/sync",
		},
		{
			name:     "non-search message path untouched (app_)",
			cred:     &credential.BotCredential{Token: "app_abc"},
			path:     "/v1/bot/messages/sync",
			body:     noChat,
			wantPath: "/v1/bot/messages/sync",
		},
		{
			name:     "unrelated path untouched (uk_)",
			cred:     &credential.BotCredential{Token: "uk_abc"},
			path:     "/v1/bot/sendMessage",
			body:     withChat,
			wantPath: "/v1/bot/sendMessage",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := routeSearchPath(tt.cred, tt.path, tt.body)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("routeSearchPath(%q) = %q, want error", tt.path, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("routeSearchPath(%q): unexpected error %v", tt.path, err)
			}
			if got != tt.wantPath {
				t.Errorf("routeSearchPath(%q) = %q, want %q", tt.path, got, tt.wantPath)
			}
		})
	}
}
