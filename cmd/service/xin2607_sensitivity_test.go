package service

import (
	"reflect"
	"testing"

	"github.com/Mininglamp-OSS/octo-cli/internal/registry"
)

func TestXIN2607DocsReplayPolicy(t *testing.T) {
	if _, allowed := verifiedReplayDedup["docs.create"]; allowed {
		t.Fatal("ordinary docs.create must remain outside verified replay safety")
	}
}

func TestXIN2607WriteOnlyCollection(t *testing.T) {
	r := registry.MustNew()
	for _, tc := range []struct{ op, field string }{{"drive.share.transfer", "password"}, {"autopilot.signing_secret.set", "signing_secret"}} {
		d, ok := r.GetOperation(tc.op)
		if !ok {
			t.Fatal(tc.op)
		}
		for _, resolved := range []bool{true, false} {
			snapshot := *d
			if resolved {
				snapshot.RequestSecretPaths = nil
			} else {
				snapshot.RequestBody = nil
			}
			got, err := CollectRequestSecrets(&snapshot, map[string]any{tc.field: "SYNTHETIC_SECRET"})
			if err != nil || !reflect.DeepEqual(got, []string{"SYNTHETIC_SECRET"}) {
				t.Errorf("%s resolved=%v: sensitivity lost, count=%d error=%v", tc.op, resolved, len(got), err)
			}
		}
	}
	schema := &registry.SchemaInfo{Type: "object", Properties: map[string]registry.SchemaInfo{"items": {Type: "array", Items: &registry.SchemaInfo{Type: "object", Properties: map[string]registry.SchemaInfo{"password": {Type: "string", WriteOnly: true}}}}}}
	got, err := CollectBodySecrets(schema, map[string]any{"items": []any{map[string]any{"paſſword": "SYNTHETIC_SECRET"}}})
	if err != nil || len(got) != 1 {
		t.Fatal("nested writeOnly sensitivity lost")
	}
}
