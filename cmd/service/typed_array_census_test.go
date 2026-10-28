package service

import (
	"github.com/Mininglamp-OSS/octo-cli/internal/registry"
	"github.com/spf13/cobra"
	"testing"
)

func TestXIN2549TypedArrayReachability(t *testing.T) {
	reg := registry.MustNew()
	arrays, intersections := 0, 0
	for _, info := range reg.ListAllOperations() {
		d, _ := reg.GetOperation(info.ID)
		rt := &operationRuntime{detail: d, pathParams: extractPathParams(d.Path), bodyFlags: map[string]*bodyFlag{}, queryFlags: map[string]*queryFlag{}, headerFlags: map[string]*headerFlag{}}
		cmd := &cobra.Command{Use: "fixture"}
		registerPathFlags(cmd, rt, d)
		registerQueryFlags(cmd, rt, d)
		registerHeaderFlags(cmd, rt, d)
		registerBodyFlags(cmd, rt, d)
		secret := schemaHasSecret(d.RequestBody) || len(d.RequestSecretPaths) > 0
		for _, p := range d.Parameters {
			secret = secret || p.Secret
		}
		for flag, bf := range rt.bodyFlags {
			if bf.kind == kindStringSlice {
				arrays++
				if secret {
					intersections++
					t.Logf("secret-and-typed-array operation=%s flag=%s", info.ID, flag)
				}
				if info.ID == "docs.search" {
					if err := cmd.Flags().Set(flag, "public-kind"); err != nil {
						t.Fatal(err)
					}
					body := map[string]any{}
					if err := applyBodyFlags(cmd, rt, body); err != nil {
						t.Fatal(err)
					}
					if _, ok := body[bf.apiName].([]string); !ok {
						t.Fatal("real string-array flag no longer produces typed []string")
					}
					t.Logf("real operation=%s flag=%s runtime_type=[]string declared_secret=%t", info.ID, flag, secret)
				}
			}
		}
	}
	t.Logf("operations=%d typed_array_flags=%d secret_intersections=%d", len(reg.ListAllOperations()), arrays, intersections)
	if arrays == 0 {
		t.Fatal("census failed")
	}
}
