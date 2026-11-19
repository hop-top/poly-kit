package dek_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v5"
	"hop.top/kit/go/transport/api"
	"hop.top/kit/go/transport/dek"
)

var update = flag.Bool("update", false, "rewrite golden files")

func descriptors() []api.CommandDescriptor {
	dry := api.CommandFlag{Name: "dry-run", Type: "bool", Description: "preview"}
	return []api.CommandDescriptor{
		{
			Path: []string{"item", "list"}, Summary: "list items",
			SideEffect: api.SideEffectRead, SideEffectSource: api.SideEffectSourceDeclared,
			Invocable:    true,
			Flags:        []api.CommandFlag{{Name: "limit", Type: "int", Description: "max rows", Default: "20"}, dry},
			OutputSchema: []byte(`{"type":"array","items":{"type":"object"}}`),
		},
		{
			Path: []string{"item", "show"}, Summary: "show one item",
			SideEffect: api.SideEffectRead, SideEffectSource: api.SideEffectSourceDeclared,
			Invocable: true,
			Args:      []api.CommandArg{{Name: "id", Required: true}},
			Flags:     []api.CommandFlag{dry},
		},
		{
			Path: []string{"item", "add"}, Summary: "add an item",
			SideEffect: api.SideEffectWrite, SideEffectSource: api.SideEffectSourceDeclared,
			Invocable: true,
			Flags: []api.CommandFlag{
				{Name: "name", Type: "string", Required: true},
				{Name: "tag", Type: "stringSlice"},
				{Name: "force", Type: "bool"},
				dry,
			},
		},
		{
			Path: []string{"item", "delete"}, Summary: "delete an item",
			SideEffect: api.SideEffectDestructive, SideEffectSource: api.SideEffectSourceDeclared,
			Invocable: true, RequiresConfirmation: true,
			Args:  []api.CommandArg{{Name: "id", Required: true}},
			Flags: []api.CommandFlag{dry},
		},
		{
			Path: []string{"item", "import"}, Summary: "import items",
			SideEffect: api.SideEffectWrite, SideEffectSource: api.SideEffectSourceDeclared,
			Invocable: true, LongRunning: true,
			Flags: []api.CommandFlag{{Name: "src", Type: "string"}, dry},
		},
		{
			Path: []string{"ping"}, Summary: "ping",
			SideEffect: api.SideEffectRead, SideEffectSource: api.SideEffectSourceDeclared,
			Invocable: true,
		},
		{
			Path: []string{"shell"}, Summary: "interactive shell",
			SideEffect: api.SideEffectInteractive, SideEffectSource: api.SideEffectSourceDeclared,
			Invocable: false, Reason: "interactive",
		},
	}
}

type fakeExec struct {
	got []api.CommandRequest
	res api.CommandResult
	err error
}

func (f *fakeExec) Execute(_ context.Context, req api.CommandRequest) (api.CommandResult, error) {
	f.got = append(f.got, req)
	return f.res, f.err
}

func newCore(t *testing.T, ex api.CommandExecutor, opts ...func(*dek.Config)) (*httptest.Server, *dek.Core) {
	t.Helper()
	cfg := dek.Config{
		Projection: api.ProjectionConfig{
			ToolName: "acme", ToolVersion: "1.2.3",
			Descriptors: descriptors(), Executor: ex,
		},
		CoreID: "acme-core",
	}
	for _, o := range opts {
		o(&cfg)
	}
	core, err := dek.New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	r := api.NewRouter()
	core.Mount(r)
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)
	return srv, core
}

func schema(t *testing.T, name string) *jsonschema.Schema {
	t.Helper()
	s, err := jsonschema.NewCompiler().Compile(filepath.Join("testdata", "contract", name+".json"))
	if err != nil {
		t.Fatalf("compile %s: %v", name, err)
	}
	return s
}

func validate(t *testing.T, name string, raw []byte) {
	t.Helper()
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatalf("%s: not json: %v\n%s", name, err, raw)
	}
	if err := schema(t, name).Validate(v); err != nil {
		t.Fatalf("%s violates dek contract schema: %v\n%s", name, err, raw)
	}
}

func golden(t *testing.T, file string, got []byte) {
	t.Helper()
	var pretty bytes.Buffer
	if err := json.Indent(&pretty, got, "", "  "); err != nil {
		t.Fatalf("indent: %v", err)
	}
	pretty.WriteByte('\n')
	p := filepath.Join("testdata", file)
	if *update {
		if err := os.WriteFile(p, pretty.Bytes(), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("read golden: %v (run with -update)", err)
	}
	if !bytes.Equal(want, pretty.Bytes()) {
		t.Fatalf("golden %s differs:\n--- got\n%s\n--- want\n%s", file, pretty.Bytes(), want)
	}
}

func get(t *testing.T, url string, hdr ...string) (*http.Response, []byte) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, url, nil)
	for i := 0; i+1 < len(hdr); i += 2 {
		req.Header.Set(hdr[i], hdr[i+1])
	}
	return do(t, req)
}

func do(t *testing.T, req *http.Request) (*http.Response, []byte) {
	t.Helper()
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var b bytes.Buffer
	_, _ = b.ReadFrom(res.Body)
	return res, b.Bytes()
}

func invoke(t *testing.T, base string, body string) (*http.Response, []byte) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, base+"/v1/dek/invoke", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	return do(t, req)
}

func errReason(t *testing.T, b []byte) string {
	t.Helper()
	var e struct {
		Reason string `json:"reason"`
	}
	if err := json.Unmarshal(b, &e); err != nil || e.Reason == "" {
		t.Fatalf("error body must be {\"reason\":...}: %s", b)
	}
	return e.Reason
}

func TestManifest_Golden(t *testing.T) {
	caps, err := dek.Manifest(descriptors(), dek.Options{})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(caps)
	golden(t, "capabilities.golden.json", raw)
	for _, c := range caps {
		one, _ := json.Marshal(c)
		validate(t, "Capability", one)
	}
}

func TestManifest_Rules(t *testing.T) {
	caps, err := dek.Manifest(descriptors(), dek.Options{ToolName: "acme"})
	if err != nil {
		t.Fatal(err)
	}
	by := map[string]dek.Capability{}
	for _, c := range caps {
		by[c.ID] = c
	}
	if _, ok := by["shell"]; ok {
		t.Fatal("non-invocable command must not be listed")
	}
	if c := by["item.list"]; !c.Read || c.Destructive || c.DryRun || c.Output != dek.OutputList {
		t.Fatalf("item.list: %+v", c)
	}
	if c := by["item.add"]; c.Read || c.Destructive || c.DryRun || c.Gate != "free" {
		t.Fatalf("item.add: %+v", c)
	}
	if c := by["item.delete"]; !c.Destructive || !c.DryRun || c.Gate != "apply" || c.Output != dek.OutputPlan {
		t.Fatalf("item.delete: %+v", c)
	}
	if c := by["acme.ping"]; c.Noun != "acme" || c.Verb != "ping" {
		t.Fatalf("single segment: %+v", c)
	}
	if raw, _ := json.Marshal(by["item.add"].ArgsSchema); strings.Contains(string(raw), `"dry-run"`) {
		t.Fatal("dry-run must not appear in args_schema: invoke carries it")
	}
}

func TestManifest_GateHook(t *testing.T) {
	caps, err := dek.Manifest(descriptors(), dek.Options{Gate: func(d api.CommandDescriptor) string {
		if d.SideEffect == api.SideEffectWrite {
			return "acme.ask"
		}
		return ""
	}})
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range caps {
		if c.ID == "item.add" && c.Gate != "acme.ask" {
			t.Fatalf("hook ignored: %+v", c)
		}
		if c.ID == "item.delete" && c.Gate != "apply" {
			t.Fatalf("empty hook answer must keep default: %+v", c)
		}
	}
}

func TestManifest_FlagArgCollision(t *testing.T) {
	d := api.CommandDescriptor{
		Path: []string{"x", "y"}, SideEffect: api.SideEffectRead, Invocable: true,
		Flags: []api.CommandFlag{{Name: "id", Type: "string"}},
		Args:  []api.CommandArg{{Name: "id"}},
	}
	if _, err := dek.Manifest([]api.CommandDescriptor{d}, dek.Options{}); err == nil {
		t.Fatal("flag and positional sharing a name must be refused")
	}
}

func TestHello(t *testing.T) {
	srv, core := newCore(t, &fakeExec{})
	res, body := get(t, srv.URL+"/v1/dek/hello")
	if res.StatusCode != 200 {
		t.Fatalf("status %d %s", res.StatusCode, body)
	}
	validate(t, "Hello", body)
	var h struct {
		Contract     struct{ Major, Minor int } `json:"contract"`
		CoreID       string                     `json:"core_id"`
		ManifestETag string                     `json:"manifest_etag"`
	}
	_ = json.Unmarshal(body, &h)
	if h.Contract.Major != 1 || h.CoreID != "acme-core" || h.ManifestETag != core.ETag() {
		t.Fatalf("hello %+v etag %s", h, core.ETag())
	}
}

func TestCapabilities_ETag(t *testing.T) {
	srv, core := newCore(t, &fakeExec{})
	res, body := get(t, srv.URL+"/v1/dek/capabilities")
	if res.StatusCode != 200 || res.Header.Get("ETag") != core.ETag() {
		t.Fatalf("status %d etag %q want %q", res.StatusCode, res.Header.Get("ETag"), core.ETag())
	}
	var arr []json.RawMessage
	if err := json.Unmarshal(body, &arr); err != nil || len(arr) != 6 {
		t.Fatalf("want 6 capabilities: %v %s", err, body)
	}
	for _, c := range arr {
		validate(t, "Capability", c)
	}
	res, _ = get(t, srv.URL+"/v1/dek/capabilities", "If-None-Match", core.ETag())
	if res.StatusCode != http.StatusNotModified {
		t.Fatalf("want 304, got %d", res.StatusCode)
	}
}

func TestInvoke_ReadResult(t *testing.T) {
	ex := &fakeExec{res: api.CommandResult{Data: map[string]any{"rows": []any{}}}}
	srv, _ := newCore(t, ex)
	res, body := invoke(t, srv.URL,
		`{"capability":"item.show","args":{"id":"7"},"dry_run":false,"idempotency_key":""}`)
	if res.StatusCode != 200 {
		t.Fatalf("status %d %s", res.StatusCode, body)
	}
	validate(t, "InvokeResponse", body)
	golden(t, "invoke_result.golden.json", body)
	if len(ex.got) != 1 || strings.Join(ex.got[0].Path, " ") != "item show" ||
		len(ex.got[0].Args) != 1 || ex.got[0].Args[0] != "7" {
		t.Fatalf("executor saw %+v", ex.got)
	}
	if _, set := ex.got[0].Flags["dry-run"]; set {
		t.Fatal("read invoke must not set dry-run")
	}
}

func TestInvoke_FlagsAndPositionalsMapped(t *testing.T) {
	ex := &fakeExec{res: api.CommandResult{Data: "ok"}}
	srv, _ := newCore(t, ex)
	res, body := invoke(t, srv.URL,
		`{"capability":"item.add","args":{"name":"n","tag":["a","b"],"force":true},"dry_run":false,"idempotency_key":"k1"}`)
	if res.StatusCode != 200 {
		t.Fatalf("status %d %s", res.StatusCode, body)
	}
	f := ex.got[0].Flags
	if f["name"] != "n" || f["force"] != true || len(f["tag"].([]any)) != 2 {
		t.Fatalf("flags %+v", f)
	}
	if ex.got[0].Meta.IdempotencyKey != "k1" {
		t.Fatalf("idempotency key not carried: %+v", ex.got[0].Meta)
	}
}

func TestInvoke_DryRunIsPlan(t *testing.T) {
	ex := &fakeExec{res: api.CommandResult{Data: map[string]any{"effects": []any{"delete item 7"}}}}
	srv, _ := newCore(t, ex)
	res, body := invoke(t, srv.URL,
		`{"capability":"item.delete","args":{"id":"7"},"dry_run":true,"idempotency_key":"k9"}`)
	if res.StatusCode != 200 {
		t.Fatalf("status %d %s", res.StatusCode, body)
	}
	validate(t, "InvokeResponse", body)
	var out struct {
		Kind string `json:"kind"`
		Body struct {
			ID             string `json:"id"`
			Capability     string `json:"capability"`
			IdempotencyKey string `json:"idempotency_key"`
			CreatedAt      string `json:"created_at"`
			Rendered       any    `json:"rendered"`
		}
	}
	_ = json.Unmarshal(body, &out)
	if out.Kind != "plan" || out.Body.Capability != "item.delete" || out.Body.IdempotencyKey != "k9" ||
		out.Body.ID == "" || out.Body.CreatedAt == "" || out.Body.Rendered == nil {
		t.Fatalf("plan %s", body)
	}
	if ex.got[0].Flags["dry-run"] != true {
		t.Fatalf("dry run must reach the command as --dry-run: %+v", ex.got[0].Flags)
	}
}

func TestInvoke_WriteMayDryRunOrRun(t *testing.T) {
	ex := &fakeExec{res: api.CommandResult{Data: "x"}}
	srv, _ := newCore(t, ex)
	res, body := invoke(t, srv.URL,
		`{"capability":"item.add","args":{"name":"n"},"dry_run":true,"idempotency_key":"k"}`)
	if res.StatusCode != 200 || !strings.Contains(string(body), `"plan"`) {
		t.Fatalf("%d %s", res.StatusCode, body)
	}
}

func TestInvoke_Refusals(t *testing.T) {
	cases := []struct {
		name, body string
		status     int
		reason     string
	}{
		{"unknown", `{"capability":"nope","args":{},"dry_run":false,"idempotency_key":""}`, 404, "no such capability"},
		{"destructive needs plan", `{"capability":"item.delete","args":{"id":"1"},"dry_run":false,"idempotency_key":"k"}`, 422,
			"capability needs a plan: invoke with dry_run, then apply"},
		{"read has no dry run", `{"capability":"item.list","args":{},"dry_run":true,"idempotency_key":""}`, 422, "capability has no dry run"},
		{"unknown arg", `{"capability":"item.list","args":{"bogus":1},"dry_run":false,"idempotency_key":""}`, 422, "unknown argument: bogus"},
		{"missing required", `{"capability":"item.show","args":{},"dry_run":false,"idempotency_key":""}`, 422, "missing required argument: id"},
		{"missing key", `{"capability":"item.add","args":{"name":"n"},"dry_run":false,"idempotency_key":""}`, 422, "idempotency_key required"},
		{"args not object", `{"capability":"item.list","args":[1],"dry_run":false,"idempotency_key":""}`, 422, "args must be an object"},
		{"malformed", `{`, 400, "malformed json"},
		{"positional not string", `{"capability":"item.show","args":{"id":5},"dry_run":false,"idempotency_key":""}`, 422, "argument id must be a string"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ex := &fakeExec{}
			srv, _ := newCore(t, ex)
			res, body := invoke(t, srv.URL, c.body)
			if res.StatusCode != c.status || errReason(t, body) != c.reason {
				t.Fatalf("got %d %s; want %d %q", res.StatusCode, body, c.status, c.reason)
			}
			if len(ex.got) != 0 {
				t.Fatal("a refused call must not reach the command")
			}
		})
	}
}

func TestInvoke_NonFreeGateRefusedDirect(t *testing.T) {
	ex := &fakeExec{}
	srv, _ := newCore(t, ex, func(c *dek.Config) {
		c.Gate = func(d api.CommandDescriptor) string {
			if d.SideEffect == api.SideEffectWrite {
				return "acme.ask"
			}
			return ""
		}
	})
	res, body := invoke(t, srv.URL, `{"capability":"item.add","args":{"name":"n"},"dry_run":false,"idempotency_key":"k"}`)
	if res.StatusCode != 403 || !strings.Contains(errReason(t, body), "acme.ask") || len(ex.got) != 0 {
		t.Fatalf("%d %s", res.StatusCode, body)
	}
}

func TestInvoke_Content(t *testing.T) {
	srv, _ := newCore(t, &fakeExec{})
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/v1/dek/invoke", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "text/plain")
	res, body := do(t, req)
	if res.StatusCode != 415 || errReason(t, body) != "expected application/json" {
		t.Fatalf("%d %s", res.StatusCode, body)
	}
}

func TestInvoke_ErrorMapping(t *testing.T) {
	cases := []struct {
		name   string
		res    api.CommandResult
		err    error
		status int
	}{
		{"permission", api.CommandResult{}, api.ErrPermissionDenied, 403},
		{"destructive blocked", api.CommandResult{}, api.ErrDestructiveBlocked, 403},
		{"unauthenticated", api.CommandResult{}, api.ErrUnauthenticated, 401},
		{"not invocable", api.CommandResult{}, api.ErrCommandNotInvocable, 404},
		{"other", api.CommandResult{}, errors.New("boom secret detail"), 500},
		{"exit code", api.CommandResult{ExitCode: 3, Stderr: "no such item"}, nil, api.StatusForExitCode(3)},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			srv, _ := newCore(t, &fakeExec{res: c.res, err: c.err})
			res, body := invoke(t, srv.URL, `{"capability":"item.list","args":{},"dry_run":false,"idempotency_key":""}`)
			if res.StatusCode != c.status {
				t.Fatalf("got %d %s want %d", res.StatusCode, body, c.status)
			}
			if r := errReason(t, body); strings.Contains(r, "secret detail") {
				t.Fatalf("internal error text leaked: %s", r)
			}
		})
	}
}

func TestUnknownRouteAndMethod(t *testing.T) {
	srv, _ := newCore(t, &fakeExec{})
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/v1/dek/capabilities", nil)
	res, _ := do(t, req)
	if res.StatusCode != http.StatusMethodNotAllowed && res.StatusCode != http.StatusNotFound {
		t.Fatalf("POST capabilities: %d", res.StatusCode)
	}
}
