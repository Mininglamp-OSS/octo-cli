package cmd

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/Mininglamp-OSS/octo-cli/internal/client"
	"github.com/Mininglamp-OSS/octo-cli/internal/cmdutil"
	"github.com/Mininglamp-OSS/octo-cli/internal/config"
	"github.com/Mininglamp-OSS/octo-cli/internal/credential"
	"github.com/Mininglamp-OSS/octo-cli/internal/output"
	"github.com/Mininglamp-OSS/octo-cli/internal/registry"
)

// Round-6 review. Two properties about which values the spec declares as secrets,
// driven through the real command tree because that is the only place the
// spec → collectSecrets → transport chain exists end to end.

// secretsTestRoot builds the command tree against apiURL with explicit client
// options, so a test can put the *client* in dry-run or point it at a closed
// listener. newDriveTestEnv always builds a live non-dry-run client, which is what
// the composites need but not what a generated leaf's dry-run path reads: the
// generated path renders its dry run inside the client, not from Globals.
func secretsTestRoot(t *testing.T, apiURL string, opts client.Options) (*cobra.Command, *cmdutil.TestFactory) {
	t.Helper()
	tf := cmdutil.NewTestFactory()
	cfg := &config.Config{APIBaseURL: apiURL, BotToken: "uk_person", Format: "json"}
	cred := &credential.BotCredential{Token: "uk_person", Source: "test"}
	tf.SetConfig(cfg)
	tf.SetCredential(cred)
	if opts.ErrOut == nil {
		opts.ErrOut = tf.ErrOut
	}
	tf.SetClient(client.New(cfg, cred, opts))
	tf.RegistryFunc = registry.MustNew
	return NewRootCmd(tf.Factory), tf
}

// TestSecrets_ShareIDIsMaskedOnEverySurface covers the round-6 P1.
//
// drive.share.revoke's share_id path parameter was not marked x-octo-secret while
// its siblings share.access / share.download / invite.accept were. The value is
// credential-equivalent: the spec's own Share description records that the backend
// returns one opaque id that is both the management handle and the access token,
// and `share blob-create --help` says the two "hold the same value today". So
// every masking point this PR built — the verbose trace, the *url.Error transport
// envelope, the dry-run URL, and a backend error body that echoes the path —
// became a disclosure point for revoke alone, because its SecretValues was empty.
//
// The transport-envelope and dry-run paths are asserted explicitly, not just
// verbose: those two are emitted without the caller opting in.
func TestSecrets_ShareIDIsMaskedOnEverySurface(t *testing.T) {
	const shareID = "SUPERSECRETSHAREID123"

	t.Run("dry-run url", func(t *testing.T) {
		root, tf := secretsTestRoot(t, "https://octo.test", client.Options{DryRun: true, ErrOut: io.Discard})
		root.SetArgs([]string{"drive", "share", "revoke", "--share-id", shareID})
		if err := root.Execute(); err != nil {
			t.Fatalf("execute: %v", err)
		}
		out := tf.Out.String()
		if strings.Contains(out, shareID) {
			t.Errorf("the dry-run description leaked the share id:\n%s", out)
		}
		if !strings.Contains(out, "REDACTED") {
			t.Errorf("the dry-run description should show the masked id:\n%s", out)
		}
	})

	t.Run("transport error envelope", func(t *testing.T) {
		// A closed listener: the request fails at dial time with a *url.Error whose
		// text embeds the whole URL.
		dead := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
		deadURL := dead.URL
		dead.Close()

		root, tf := secretsTestRoot(t, deadURL, client.Options{NoRetry: true, ErrOut: io.Discard})
		root.SetArgs([]string{"drive", "share", "revoke", "--share-id", shareID})
		err := root.Execute()
		if err == nil {
			t.Fatal("expected a transport error against a closed listener")
		}
		ee := output.AsExitError(err)
		if ee == nil {
			t.Fatalf("expected a structured error, got %v", err)
		}
		if strings.Contains(ee.Message, shareID) {
			t.Errorf("the error envelope leaked the share id: %s", ee.Message)
		}
		if !strings.Contains(ee.Message, "REDACTED") {
			t.Errorf("the error envelope should show the masked id: %s", ee.Message)
		}
		if streams := tf.Out.String() + tf.ErrOut.String(); strings.Contains(streams, shareID) {
			t.Errorf("the emitted output leaked the share id:\n%s", streams)
		}
	})

	t.Run("backend error body echoing the path", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNotFound)
			// The octo-drive envelope shape, with the requested path echoed back —
			// the most natural thing for a backend to write when the id is the token.
			_, _ = w.Write([]byte(`{"error":"not_found","message":"share ` + r.URL.Path + ` not found"}`))
		}))
		t.Cleanup(srv.Close)

		root, tf := secretsTestRoot(t, srv.URL, client.Options{NoRetry: true, ErrOut: io.Discard})
		root.SetArgs([]string{"drive", "share", "revoke", "--share-id", shareID})
		err := root.Execute()
		if err == nil {
			t.Fatal("expected the backend error to surface")
		}
		ee := output.AsExitError(err)
		if ee == nil {
			t.Fatalf("expected a structured error, got %v", err)
		}
		if strings.Contains(ee.Message, shareID) || strings.Contains(string(ee.Detail), shareID) {
			t.Errorf("the backend error echoed the share id: %s / %s", ee.Message, ee.Detail)
		}
		if streams := tf.Out.String() + tf.ErrOut.String(); strings.Contains(streams, shareID) {
			t.Errorf("the emitted output echoed the share id:\n%s", streams)
		}
		// Case-sensitive on purpose. This assertion was EqualFold in round 6, and
		// that is exactly why it passed while the property it guards was violated:
		// when redaction masked the code value, parsing fell back to the
		// status-derived "NOT_FOUND" and EqualFold accepted it, so the test stayed
		// green with the backend's real code gone and detail dropped.
		if ee.Code != "not_found" {
			t.Errorf("code: got %q, want exactly \"not_found\" — an upper-case value means the real code was "+
				"masked and parsing fell back to the status", ee.Code)
		}
		if len(ee.Detail) == 0 {
			t.Error("detail was dropped, which is the other half of the same fallback")
		}
	})

	t.Run("verbose trace", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNoContent)
		}))
		t.Cleanup(srv.Close)

		root, tf := secretsTestRoot(t, srv.URL, client.Options{Verbose: true})
		root.SetArgs([]string{"drive", "share", "revoke", "--share-id", shareID})
		if err := root.Execute(); err != nil {
			t.Fatalf("execute: %v", err)
		}
		if trace := tf.ErrOut.String(); strings.Contains(trace, shareID) {
			t.Errorf("the verbose trace leaked the share id:\n%s", trace)
		}
	})

	t.Run("the wire still carries the real id", func(t *testing.T) {
		var gotPath string
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotPath = r.URL.Path
			w.WriteHeader(http.StatusNoContent)
		}))
		t.Cleanup(srv.Close)

		root, _ := secretsTestRoot(t, srv.URL, client.Options{})
		root.SetArgs([]string{"drive", "share", "revoke", "--share-id", shareID})
		if err := root.Execute(); err != nil {
			t.Fatalf("execute: %v", err)
		}
		if !strings.Contains(gotPath, shareID) {
			t.Errorf("request path lost the share id: %q — redaction is a logging concern only", gotPath)
		}
	})
}

// TestSecrets_CredentialEquivalentPathParamsAreMarked pins which drive path
// parameters must carry x-octo-secret, and — just as importantly — which must not.
//
// The share and invite families differ on purpose. A Share is one opaque backend
// id doubling as handle and token, so share_id and share_token are the same value
// and both are credential-equivalent. An Invite has genuinely distinct id and
// token fields ("invite_id (NOT the invite_token)"), so invite_id is a management
// id and marking it would mask a value callers need to read. Recording both
// directions stops the next reader from "fixing" the asymmetry either way.
func TestSecrets_CredentialEquivalentPathParamsAreMarked(t *testing.T) {
	mustBeSecret := map[string][]string{
		"drive.share.revoke":   {"share_id"},
		"drive.share.access":   {"share_token"},
		"drive.share.download": {"share_token"},
		"drive.invite.accept":  {"invite_token"},
	}
	mustNotBeSecret := map[string][]string{
		"drive.invite.revoke": {"space_id", "invite_id"},
		"drive.invite.create": {"space_id"},
		"drive.invite.list":   {"space_id"},
	}

	reg := registry.MustNew()
	check := func(opID string, names []string, want bool) {
		d, ok := reg.GetOperation(opID)
		if !ok {
			t.Fatalf("%s is not in the embedded registry", opID)
		}
		for _, name := range names {
			var found bool
			for i := range d.Parameters {
				p := &d.Parameters[i]
				if p.In != "path" || p.Name != name {
					continue
				}
				found = true
				if p.Secret == want {
					continue
				}
				if want {
					t.Errorf("%s: path param %q must declare x-octo-secret — it is credential-equivalent, "+
						"so every masking point in the CLI becomes a disclosure point without it", opID, name)
				} else {
					t.Errorf("%s: path param %q must NOT declare x-octo-secret — it is a management id "+
						"distinct from the token, and masking it hides a value callers need to read", opID, name)
				}
			}
			if !found {
				t.Errorf("%s declares no path param %q; this test is out of date with the spec", opID, name)
			}
		}
	}
	for opID, names := range mustBeSecret {
		check(opID, names, true)
	}
	for opID, names := range mustNotBeSecret {
		check(opID, names, false)
	}
}

// TestSecrets_ASecretNameIsSecretEverywhere is the derived half of the same
// property, and the round-6 ask the allowlist above only partly met.
//
// The allowlist pins seven known operations; it derives nothing, so a *new*
// operation carrying the same credential-equivalent value unmarked slips through —
// the round-8 review demonstrated exactly that by injecting a drive.share.peek with
// an unmarked share_token and watching the secrets tests stay green.
//
// The rule here needs no list: if any operation marks a parameter name as secret,
// that name is credential-equivalent by nature, so every other operation using it
// in a path or query must mark it too. Exceptions have to be stated, not defaulted.
func TestSecrets_ASecretNameIsSecretEverywhere(t *testing.T) {
	// Names that are legitimately secret in one place and not in another. Empty
	// today, and that is the point: an entry here is a claim someone has to defend.
	knownExceptions := map[string]string{}

	reg := registry.MustNew()
	type site struct{ op, in string }
	secretNames := map[string]site{}
	var allSites []struct {
		op, in, name string
		secret       bool
	}

	for _, svc := range reg.ListServices() {
		for _, info := range reg.ListOperations(svc) {
			d, ok := reg.GetOperation(info.ID)
			if !ok {
				continue
			}
			for i := range d.Parameters {
				p := &d.Parameters[i]
				if p.In != "path" && p.In != "query" {
					continue
				}
				allSites = append(allSites, struct {
					op, in, name string
					secret       bool
				}{info.ID, p.In, p.Name, p.Secret})
				if p.Secret {
					secretNames[p.Name] = site{info.ID, p.In}
				}
			}
		}
	}
	if len(secretNames) == 0 {
		t.Fatal("no x-octo-secret path or query parameter found at all; this guard has nothing to derive from")
	}

	for _, s := range allSites {
		if s.secret {
			continue
		}
		declaredAt, isSecretSomewhere := secretNames[s.name]
		if !isSecretSomewhere {
			continue
		}
		if why, excepted := knownExceptions[s.op+"|"+s.name]; excepted {
			t.Logf("%s %s is a documented exception: %s", s.op, s.name, why)
			continue
		}
		t.Errorf("%s declares %s parameter %q without x-octo-secret, but %s marks the same name as secret. "+
			"The same value cannot be credential-equivalent in one operation and not in another — mark it, or "+
			"add it to knownExceptions with the reason it genuinely differs.",
			s.op, s.in, s.name, declaredAt.op)
	}
}

// secretBodyFields collects the request-body properties marked x-octo-secret at
// any depth, so the tripwire sees a nested declaration too.
func secretBodyFields(schema *registry.SchemaInfo, path string) []string {
	if schema == nil {
		return nil
	}
	var out []string
	for name := range schema.Properties {
		prop := schema.Properties[name]
		field := name
		if path != "" {
			field = path + "." + name
		}
		if prop.Secret {
			out = append(out, field)
		}
		if prop.Type == "object" {
			out = append(out, secretBodyFields(&prop, field)...)
		}
		if prop.Items != nil && prop.Items.Type == "object" {
			out = append(out, secretBodyFields(prop.Items, field+"[]")...)
		}
	}
	return out
}

// TestSecrets_EverySecretBodyPropertyBelongsToADetachedLeaf is the round-6 P2
// tripwire, and the reason collectSecrets' flag-only behaviour is recorded as
// unreachable rather than fixed.
//
// collectSecrets reads flag values, never --data, so a secret body property
// supplied through --data would reach the verbose trace unmasked. No operation is
// exposed that way today: the only three declaring one (all `password`) have their
// generated leaves detached by registerDriveCmds in favour of hand-written
// composites that pass SecretValues explicitly.
//
// The check is against the real command tree rather than a hard-coded list, so it
// stays true as leaves are attached or detached. The day a *reachable* generated
// leaf declares a secret body property, this fails — and collectSecrets has to
// walk the merged body, which is an engine change, not a doc change.
func TestSecrets_EverySecretBodyPropertyBelongsToADetachedLeaf(t *testing.T) {
	tf := cmdutil.NewTestFactory()
	tf.RegistryFunc = registry.MustNew
	root := NewRootCmd(tf.Factory)
	reg := registry.MustNew()

	for _, svc := range reg.ListServices() {
		for _, info := range reg.ListOperations(svc) {
			d, ok := reg.GetOperation(info.ID)
			if !ok || d.RequestBody == nil {
				continue
			}
			// Recursive: an operation declaring x-octo-secret on a *nested*
			// property would otherwise pass this guard while
			// --data '{"credentials":{"password":"…"}}' --verbose logged it,
			// because collectSecrets reads promoted top-level flags only.
			secretFields := secretBodyFields(d.RequestBody, "")
			if len(secretFields) == 0 {
				continue
			}
			if leaf := findGeneratedLeaf(root, d); leaf != nil {
				t.Errorf("%s declares secret body field(s) %v and its generated leaf %q is still registered; "+
					"collectSecrets reads flags only, so supplying %q through --data would reach the verbose "+
					"trace unmasked. Either detach the leaf in favour of a composite that passes SecretValues, "+
					"or make collectSecrets walk the merged body",
					info.ID, secretFields, leaf.CommandPath(), secretFields[0])
			}
		}
	}
}

// findGeneratedLeaf resolves where the engine would have registered d's leaf and
// returns it, or nil when no *generated* command sits there.
//
// A name match is not enough: the three detached drive leaves were replaced by
// hand-written composites under the same names. Only the engine writes
// "operationId: <id>" into a command's Long description (buildLongDesc), so that
// is what distinguishes a generated leaf from a composite occupying its name.
func findGeneratedLeaf(root *cobra.Command, d *registry.OperationDetail) *cobra.Command {
	segs := strings.Split(d.ID, ".")
	if len(segs) < 2 {
		return nil
	}
	cur := findDriveCmd(root, d.Service)
	if cur == nil {
		return nil
	}
	start := 1
	if segs[0] != d.Service {
		start = 0
	}
	for i := start; i < len(segs); i++ {
		next := findDriveCmd(cur, strings.ReplaceAll(segs[i], "_", "-"))
		if next == nil {
			return nil
		}
		cur = next
	}
	if !strings.Contains(cur.Long, "operationId: "+d.ID) {
		return nil // a hand-written composite occupies this name
	}
	return cur
}
