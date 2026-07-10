package fracindex

import "testing"

func TestValidateOrderKey(t *testing.T) {
	tests := []struct {
		name    string
		key     string
		wantErr bool
	}{
		// Valid keys produced by the fractional-indexing library.
		{"integer zero", "a0", false},
		{"integer one", "a1", false},
		{"integer with fractional part", "a0V", false},
		{"upper-head integer", "Zz", false},
		{"deep fractional", "a0zzzzzzzzzz", false},

		// The exact repair artifact from XIN-792 must be rejected: head 'r'
		// demands a 19-char integer part but the key is only 9 chars.
		{"repair artifact r00000003", "r00000003", true},

		// Other malformed shapes.
		{"empty", "", true},
		{"plain integer", "3", true},
		{"timestamp", "1720598400000", true},
		{"reserved smallest integer", smallestInteger, true},
		{"bad head", "!0", true},
		{"truncated integer part", "b0", true}, // head 'b' requires a 3-char integer part
		{"bad fractional digit", "a0-", true},

		// Integer-part charset: every byte of the key must be base62, not just
		// the fractional part. The frontend (INDEX_RE = /^[A-Za-z0-9]+$/) and
		// backend (identical regex) reject a non-base62 integer-part byte, so
		// the CLI must too.
		{"integer-part non-base62", "a!", true},
		{"integer-part non-base62 with fraction", "c00!", true},

		// Trailing-zero rule: the fractional part must not end in the first
		// digit '0' (a non-canonical key). The reference validateOrderKey and
		// the frontend/backend all reject these.
		{"trailing zero", "a00", true},
		{"trailing zero short", "a10", true},
		{"trailing zero deep", "a0V0", true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateOrderKey(tc.key)
			if tc.wantErr && err == nil {
				t.Errorf("ValidateOrderKey(%q) = nil, want error", tc.key)
			}
			if !tc.wantErr && err != nil {
				t.Errorf("ValidateOrderKey(%q) = %v, want nil", tc.key, err)
			}
		})
	}
}
