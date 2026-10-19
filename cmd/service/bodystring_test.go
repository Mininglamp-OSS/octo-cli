package service

import (
	"strings"
	"testing"

	"github.com/Mininglamp-OSS/octo-cli/internal/registry"
)

func TestValidateStringCountsUnicodeCodePoints(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		schema  registry.SchemaInfo
		value   string
		wantErr bool
	}{
		{
			name:   "multibyte characters within maximum",
			schema: registry.SchemaInfo{Type: "string", MaxLength: 100},
			value:  strings.Repeat("界", 34),
		},
		{
			name:    "multibyte characters above maximum",
			schema:  registry.SchemaInfo{Type: "string", MaxLength: 33},
			value:   strings.Repeat("界", 34),
			wantErr: true,
		},
		{
			name:    "multibyte characters below minimum",
			schema:  registry.SchemaInfo{Type: "string", MinLength: 16},
			value:   strings.Repeat("界", 6),
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateString(&tt.schema, tt.value, "name")
			if (err != nil) != tt.wantErr {
				t.Fatalf("validateString() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
