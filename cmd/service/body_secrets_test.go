package service

import (
	"strings"
	"testing"

	"github.com/Mininglamp-OSS/octo-cli/internal/registry"
)

func TestCollectBodySecrets_CollectsStringMapValues(t *testing.T) {
	const first = "marketplace-secret-1"
	const second = "marketplace-secret-2"
	schema := &registry.SchemaInfo{
		Type: "object",
		Properties: map[string]registry.SchemaInfo{
			"custom_env": {Type: "object", Secret: true},
		},
	}
	body := map[string]any{
		"custom_env": map[string]any{"API_TOKEN": first, "CLIENT_SECRET": second},
	}

	got, err := CollectBodySecrets(schema, body)
	if err != nil {
		t.Fatalf("CollectBodySecrets: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("secret values = %v, want both map values", got)
	}
	foundFirst, foundSecond := false, false
	for _, value := range got {
		foundFirst = foundFirst || value == first
		foundSecond = foundSecond || value == second
	}
	if !foundFirst || !foundSecond {
		t.Errorf("secret values = %v, want [%q %q]", got, first, second)
	}
}

func TestCollectBodySecrets_RefusesNonStringMapValue(t *testing.T) {
	const numeric = 8675309
	schema := &registry.SchemaInfo{
		Type: "object",
		Properties: map[string]registry.SchemaInfo{
			"custom_env": {Type: "object", Secret: true},
		},
	}
	body := map[string]any{
		"custom_env": map[string]any{"API_TOKEN": numeric},
	}

	_, err := CollectBodySecrets(schema, body)
	if err == nil {
		t.Fatal("non-string secret map value was accepted without a maskable spelling")
	}
	if !strings.Contains(err.Error(), "custom_env.API_TOKEN") {
		t.Errorf("error does not identify the secret map entry: %v", err)
	}
	if strings.Contains(err.Error(), "8675309") {
		t.Errorf("error exposed the unmaskable secret value: %v", err)
	}
}

func TestCollectBodySecretsKeepsScalarSecretsStringOnly(t *testing.T) {
	schema := &registry.SchemaInfo{
		Type: "object",
		Properties: map[string]registry.SchemaInfo{
			"password": {Type: "string", Secret: true},
		},
	}

	got, err := CollectBodySecrets(schema, map[string]any{"password": "scalar-secret"})
	if err != nil {
		t.Fatalf("CollectBodySecrets string: %v", err)
	}
	if len(got) != 1 || got[0] != "scalar-secret" {
		t.Errorf("scalar secret values = %v, want [scalar-secret]", got)
	}

	_, err = CollectBodySecrets(schema, map[string]any{"password": 8675309})
	if err == nil {
		t.Fatal("numeric scalar secret was accepted")
	}
	if !strings.Contains(err.Error(), "password") || strings.Contains(err.Error(), "8675309") {
		t.Errorf("scalar refusal must name the property without exposing its value: %v", err)
	}
}
