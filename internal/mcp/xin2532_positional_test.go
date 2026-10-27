package mcp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/spf13/cobra"

	"github.com/Mininglamp-OSS/octo-cli/internal/registry"
)

// XIN-2532 (F-C1): a query parameter that declares x-octo-positional is the
// engine leaf command's <operand>, not a flag. buildArgv used to render it as
// --<name>=value, which the engine never registers, so task.search failed
// locally ("a flag in the command line was not recognised") from execute,
// dry_run and call_op without sending anything.

const xin2532Workspace = "11111111-1111-4111-8111-111111111111"

// xin2532Values are search terms that must reach the wire byte for byte: a
// leading hyphen (parsed as a flag if it is not after "--"), spaces, Unicode,
// quotes and shell metacharacters, and a value that spells a reserved flag.
var xin2532Values = []string{
	"XIN-2452",
	"-x",
	"--profile=evil",
	"--",
	"two words",
	"中文 検索 ✓",
	`"quoted" 'single' $(id) ; | & > < * ? ~ \`,
}

type xin2532Request struct {
	method, path string
	query        url.Values
	workspace    string
}

type xin2532Backend struct {
	mu   sync.Mutex
	reqs []xin2532Request
}

func (b *xin2532Backend) server(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b.mu.Lock()
		b.reqs = append(b.reqs, xin2532Request{r.Method, r.URL.Path, r.URL.Query(), r.Header.Get("X-Workspace-ID")})
		b.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[],"pagination":{"total":0,"page":1,"page_size":20}}`))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func (b *xin2532Backend) take() []xin2532Request {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := b.reqs
	b.reqs = nil
	return out
}

func xin2532Detail(t *testing.T, id string) *registry.OperationDetail {
	t.Helper()
	d, ok := registry.MustNew().GetOperation(id)
	if !ok {
		t.Fatalf("operation %s is not in the registry", id)
	}
	return d
}

func TestXIN2532BuildArgvTaskSearchUsesOperand(t *testing.T) {
	d := xin2532Detail(t, "task.search")
	for _, v := range xin2532Values {
		argv, err := buildArgv(context.Background(), d, map[string]any{
			"X-Workspace-ID": xin2532Workspace, "q": v, "page": json.Number("2"), "page_size": json.Number("5"),
		}, execPolicy{})
		if err != nil {
			t.Fatalf("buildArgv(%q): %v", v, err)
		}
		n := len(argv)
		if n < 2 || argv[n-2] != "--" || argv[n-1] != v {
			t.Errorf("q=%q must be the only operand after \"--\", argv=%q", v, argv)
		}
		for _, a := range argv[:n-2] {
			if strings.HasPrefix(a, "--q=") || strings.HasPrefix(a, "--query=") {
				t.Errorf("q=%q must not become a flag, argv=%q", v, argv)
			}
		}
		assertHas(t, argv, "--workspace-id="+xin2532Workspace)
		assertHas(t, argv, "--page=2")
		assertHas(t, argv, "--page-size=5")
	}
}

// A synthetic operation with two path params and two positional queries
// declared out of alphabetical order: the operands follow the path values, in
// declaration order, after a single "--".
func TestXIN2532BuildArgvOperandOrderAndRequired(t *testing.T) {
	d := &registry.OperationDetail{
		OperationInfo: registry.OperationInfo{ID: "thing.find", Service: "thing", Method: "GET", Path: "/v1/{a}/x/{b}"},
		Parameters: []registry.ParamInfo{
			{Name: "zeta", In: "query", Required: true, Type: "string", PositionalName: "first"},
			{Name: "limit", In: "query", Type: "integer"},
			{Name: "alpha", In: "query", Required: true, Type: "string", PositionalName: "second"},
		},
	}
	argv, err := buildArgv(context.Background(), d, map[string]any{"a": "-A", "b": "B", "zeta": "-z", "alpha": "a b", "limit": json.Number("3")}, execPolicy{})
	if err != nil {
		t.Fatalf("buildArgv: %v", err)
	}
	want := "thing find --limit=3 -- -A B -z a b"
	if got := strings.Join(argv, " "); got != want {
		t.Errorf("argv = %q, want %q", got, want)
	}
	if indexOf(argv, "a b") != len(argv)-1 {
		t.Errorf("a value with a space must stay one argument, argv=%q", argv)
	}

	for name, args := range map[string]map[string]any{
		"absent": {"a": "A", "b": "B", "alpha": "x"},
		"null":   {"a": "A", "b": "B", "alpha": "x", "zeta": nil},
	} {
		_, err := buildArgv(context.Background(), d, args, execPolicy{})
		if err == nil || !strings.Contains(err.Error(), `missing required query argument "zeta"`) {
			t.Errorf("%s operand: want a missing-argument error naming zeta, got %v", name, err)
		}
	}
	_, err = buildArgv(context.Background(), d, map[string]any{"a": "A", "b": "B", "alpha": "x", "zeta": []any{"p", "q"}}, execPolicy{})
	if err == nil || !strings.Contains(err.Error(), `query argument "zeta"`) {
		t.Errorf("an array operand must be refused, got %v", err)
	}
	_, err = buildArgv(context.Background(), d, map[string]any{"a": "A", "b": "B", "alpha": "x", "zeta": map[string]any{"k": "v"}}, execPolicy{})
	if err == nil || !strings.Contains(err.Error(), `query argument "zeta"`) {
		t.Errorf("an object operand must be refused, got %v", err)
	}
}

// The CLI operand label is not an argument name: it stays refused, and the
// error names the argument to use instead.
func TestXIN2532OperandLabelIsRefusedWithHint(t *testing.T) {
	d := xin2532Detail(t, "task.search")
	_, err := buildArgv(context.Background(), d, map[string]any{"X-Workspace-ID": xin2532Workspace, "query": "x"}, execPolicy{})
	if err == nil || !strings.Contains(err.Error(), "unexpected argument(s) for task.search: query") ||
		!strings.Contains(err.Error(), `pass it as "q"`) {
		t.Errorf("want the label refused with a hint naming q, got %v", err)
	}
	_, err = buildArgv(context.Background(), d, map[string]any{"X-Workspace-ID": xin2532Workspace, "q": "x", "query": "y"}, execPolicy{})
	if err == nil || !strings.Contains(err.Error(), "unexpected argument") {
		t.Errorf("the label next to q must still be refused, got %v", err)
	}
	_, err = buildArgv(context.Background(), d, map[string]any{"X-Workspace-ID": xin2532Workspace, "Q": "x"}, execPolicy{})
	if err == nil || !strings.Contains(err.Error(), `not the declared spelling of "q"`) {
		t.Errorf("a case-folded spelling of q must still be refused, got %v", err)
	}
}

// xin2532SampleArgs fills every declared path, query and header parameter of
// an operation with a value its CLI flag parses, so the argv can be checked
// against the engine's own cobra command.
func xin2532SampleArgs(d *registry.OperationDetail) (map[string]any, map[string]string) {
	args := map[string]any{}
	operands := map[string]string{}
	for _, n := range extractPathParams(d.Path) {
		args[n] = "-p" + n // leading hyphen: must survive as a path value
	}
	for i := range d.Parameters {
		p := &d.Parameters[i]
		if p.In != "query" && p.In != "header" {
			continue
		}
		if p.PositionalName != "" {
			v := "-" + p.Name + " v"
			args[p.Name] = v
			operands[p.Name] = v
			continue
		}
		var v any
		switch {
		case len(p.Enum) > 0:
			v = p.Enum[0]
		case p.Type == "integer" || p.Type == "number":
			v = json.Number("1")
		case p.Type == "boolean":
			v = true
		case p.Type == "array":
			v = []any{"v1", "v2"}
		default:
			v = "v"
		}
		if p.Type == "array" && len(p.Enum) > 0 {
			v = []any{p.Enum[0]}
		} else if p.Type == "array" && p.Items != nil && len(p.Items.Enum) > 0 {
			v = []any{p.Items.Enum[0]}
		}
		args[p.Name] = v
	}
	return args, operands
}

// Registry-wide: for every operation, the argv buildArgv produces from all of
// its named path/query/header parameters parses on the engine's real leaf
// command (no unknown flag, right operand count), every positional query value
// lands on the operand slot the engine reads it from, and no positional query
// is ever rendered as a flag.
func TestXIN2532RegistryWideArgvMatchesEngine(t *testing.T) {
	reg := registry.MustNew()
	f, _, _ := fakeBackendFactory("http://127.0.0.1:1", "bf_test")(TrustedContext{})
	root := testRoot(f)
	positionalOps, parsed, reserved := 0, 0, 0
	for _, info := range reg.ListAllOperations() { //nolint:gocritic // read-only snapshot
		d, ok := reg.GetOperation(info.ID)
		if !ok {
			t.Fatalf("listed operation %s is not resolvable", info.ID)
		}
		args, operands := xin2532SampleArgs(d)
		argv, err := buildArgv(context.Background(), d, args, execPolicy{})
		if err != nil {
			if strings.Contains(err.Error(), "reserved flag") {
				reserved++
				continue // refused before cobra by design
			}
			t.Errorf("%s: buildArgv: %v", d.ID, err)
			continue
		}
		words := commandWords(d)
		leaf, rest, err := root.Find(words)
		if err != nil || len(rest) != 0 || leaf == root {
			t.Errorf("%s: no engine command for %v", d.ID, words)
			continue
		}
		if err := xin2532Parse(leaf, argv[len(words):]); err != nil {
			t.Errorf("%s: engine rejects MCP argv %q: %v", d.ID, argv, err)
			continue
		}
		parsed++
		if len(operands) == 0 {
			continue
		}
		positionalOps++
		got := leaf.Flags().Args()
		var wantTail []string
		for i := range d.Parameters {
			if p := &d.Parameters[i]; p.In == "query" && p.PositionalName != "" {
				wantTail = append(wantTail, operands[p.Name])
				for _, a := range argv {
					if strings.HasPrefix(a, "--"+flagName(p)+"=") || strings.HasPrefix(a, "--"+p.Name+"=") {
						t.Errorf("%s: positional query %s rendered as a flag: %q", d.ID, p.Name, argv)
					}
				}
			}
		}
		if len(got) < len(wantTail) || strings.Join(got[len(got)-len(wantTail):], "\x00") != strings.Join(wantTail, "\x00") {
			t.Errorf("%s: engine operands %q, want tail %q", d.ID, got, wantTail)
		}
	}
	if positionalOps == 0 {
		t.Fatal("the registry has no positional query operation; this test no longer covers F-C1")
	}
	t.Logf("%d operations parsed on the engine (%d refused for a reserved flag), %d with positional query operands", parsed, reserved, positionalOps)
}

// xin2532Parse runs the same flag parse and arity check cobra applies before
// RunE, without executing the command.
func xin2532Parse(cmd *cobra.Command, args []string) error {
	if err := cmd.ParseFlags(args); err != nil {
		return err
	}
	return cmd.ValidateArgs(cmd.Flags().Args())
}

func xin2532Server(t *testing.T, facade facadeMode, url string) *Server {
	t.Helper()
	s := facadeServer(t, facade)
	s.factoryFn = fakeBackendFactory(url, "bf_test")
	return s
}

func xin2532CheckRequest(t *testing.T, label string, reqs []xin2532Request, q string) {
	t.Helper()
	if len(reqs) != 1 {
		t.Fatalf("%s: backend saw %d requests, want 1: %+v", label, len(reqs), reqs)
	}
	r := reqs[0]
	if r.method != http.MethodGet || r.path != "/fleet/api/v1/tasks/_search" {
		t.Errorf("%s: request %s %s, want GET /fleet/api/v1/tasks/_search", label, r.method, r.path)
	}
	if got := r.query["q"]; len(got) != 1 || got[0] != q {
		t.Errorf("%s: q = %q, want exactly %q", label, got, q)
	}
	if r.query.Get("page") != "2" || r.query.Get("page_size") != "5" {
		t.Errorf("%s: page/page_size = %q/%q, want 2/5", label, r.query.Get("page"), r.query.Get("page_size"))
	}
	if r.workspace != xin2532Workspace {
		t.Errorf("%s: X-Workspace-ID = %q", label, r.workspace)
	}
	for k := range r.query {
		if k != "q" && k != "page" && k != "page_size" {
			t.Errorf("%s: unexpected query key %q", label, k)
		}
	}
}

// execute, its dry run and call_op all drive task.search through the shared
// engine; the request matches what the CLI sends for the same operand.
func TestXIN2532TaskSearchThroughEveryFacade(t *testing.T) {
	be := &xin2532Backend{}
	srv := be.server(t)
	two := xin2532Server(t, facadeTwo, srv.URL)
	three := xin2532Server(t, facadeThree, srv.URL)

	for _, v := range xin2532Values {
		args := map[string]any{"X-Workspace-ID": xin2532Workspace, "q": v, "page": 2, "page_size": 5}

		res := callTool(t, two, "execute", map[string]any{"operation_id": "task.search", "dry_run": true, "arguments": args})
		if out := toolText(t, res); res.IsError || out["status"] != "ok" || out["dry_run"] != true {
			t.Errorf("dry_run q=%q: %s", v, res.Content[0].Text)
		}
		if n := len(be.take()); n != 0 {
			t.Errorf("dry_run q=%q sent %d requests, want 0", v, n)
		}

		res = callTool(t, two, "execute", map[string]any{"operation_id": "task.search", "arguments": args})
		if out := toolText(t, res); res.IsError || out["status"] != "ok" {
			t.Errorf("execute q=%q: %s", v, res.Content[0].Text)
		}
		xin2532CheckRequest(t, "execute "+v, be.take(), v)

		res = callTool(t, three, "call_op", map[string]any{"operation_id": "task.search", "arguments": args})
		if res.IsError {
			t.Errorf("call_op q=%q: %s", v, res.Content[0].Text)
		}
		xin2532CheckRequest(t, "call_op "+v, be.take(), v)

		// The CLI, on the same engine and configuration, sends the same request.
		f, _, errBuf := fakeBackendFactory(srv.URL, "bf_test")(TrustedContext{})
		root := testRoot(f)
		root.SetArgs([]string{"loop", "task", "search", "--workspace-id", xin2532Workspace, "--page", "2", "--page-size", "5", "--", v})
		if err := root.Execute(); err != nil {
			t.Errorf("cli q=%q: %v %s", v, err, errBuf.String())
		}
		xin2532CheckRequest(t, "cli "+v, be.take(), v)
	}
}

// The local refusals stay local: a missing or empty operand, the CLI label and
// a case-folded spelling never reach the backend, on any facade.
func TestXIN2532TaskSearchRefusalsSendNothing(t *testing.T) {
	be := &xin2532Backend{}
	srv := be.server(t)
	two := xin2532Server(t, facadeTwo, srv.URL)
	three := xin2532Server(t, facadeThree, srv.URL)
	cases := map[string]map[string]any{
		"missing": {"X-Workspace-ID": xin2532Workspace},
		"null":    {"X-Workspace-ID": xin2532Workspace, "q": nil},
		"empty":   {"X-Workspace-ID": xin2532Workspace, "q": ""},
		"label":   {"X-Workspace-ID": xin2532Workspace, "query": "x"},
		"folded":  {"X-Workspace-ID": xin2532Workspace, "Q": "x"},
	}
	for name, args := range cases {
		for _, dry := range []bool{false, true} {
			res := callTool(t, two, "execute", map[string]any{"operation_id": "task.search", "dry_run": dry, "arguments": args})
			if out := toolText(t, res); !res.IsError || out["status"] != "validation_error" {
				t.Errorf("execute %s dry_run=%v: want validation_error, got %s", name, dry, res.Content[0].Text)
			}
		}
		if res := callTool(t, three, "call_op", map[string]any{"operation_id": "task.search", "arguments": args}); !res.IsError {
			t.Errorf("call_op %s: want an error, got %s", name, res.Content[0].Text)
		}
		if n := len(be.take()); n != 0 {
			t.Errorf("%s: backend saw %d requests, want 0", name, n)
		}
	}
}
