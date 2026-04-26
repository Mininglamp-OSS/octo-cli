package client

import (
	"encoding/base64"
	"encoding/json"
	"testing"
)

func TestExtractSpaceIDFromJWT(t *testing.T) {
	tests := []struct {
		name  string
		token string
		want  string
	}{
		{
			name:  "valid JWT with space_id",
			token: makeTestJWT(map[string]any{"sub": "bot-1", "space_id": "sp-123"}),
			want:  "sp-123",
		},
		{
			name:  "JWT without space_id",
			token: makeTestJWT(map[string]any{"sub": "bot-1"}),
			want:  "",
		},
		{
			name:  "empty string",
			token: "",
			want:  "",
		},
		{
			name:  "not a JWT",
			token: "not-a-jwt",
			want:  "",
		},
		{
			name:  "malformed base64 payload",
			token: "header.!!!invalid!!!.sig",
			want:  "",
		},
		{
			name:  "space_id is not a string",
			token: makeTestJWT(map[string]any{"sub": "bot-1", "space_id": 42}),
			want:  "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractSpaceIDFromJWT(tt.token)
			if got != tt.want {
				t.Errorf("extractSpaceIDFromJWT() = %q, want %q", got, tt.want)
			}
		})
	}
}

// makeTestJWT creates a fake JWT with the given payload claims.
// The header and signature are placeholders — only the payload matters for extraction.
func makeTestJWT(claims map[string]any) string {
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"RS256","typ":"JWT"}`))
	payload, _ := json.Marshal(claims)
	payloadB64 := base64.RawURLEncoding.EncodeToString(payload)
	return header + "." + payloadB64 + ".fake-signature"
}
