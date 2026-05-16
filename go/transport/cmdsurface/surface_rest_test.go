package cmdsurface_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/spf13/cobra"

	"hop.top/kit/go/transport/api"
	"hop.top/kit/go/transport/cmdsurface"
)

// recordingRunner is a test-only Runner that records every Invocation and
// dispatches the configured RunFn (or a Result echoing the path).
type recordingRunner struct {
	mu  sync.Mutex
	got []cmdsurface.Invocation

	RunFn func(ctx context.Context, inv cmdsurface.Invocation) (cmdsurface.Result, error)
}

func (f *recordingRunner) Run(ctx context.Context, inv cmdsurface.Invocation) (cmdsurface.Result, error) {
	f.mu.Lock()
	f.got = append(f.got, inv)
	f.mu.Unlock()
	if f.RunFn != nil {
		return f.RunFn(ctx, inv)
	}
	return cmdsurface.Result{Stdout: strings.Join(inv.Path, " ")}, nil
}

func (f *recordingRunner) Stream(context.Context, cmdsurface.Invocation, chan<- cmdsurface.Event) error {
	return errors.New("recordingRunner: Stream not supported")
}

func (f *recordingRunner) captured() []cmdsurface.Invocation {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]cmdsurface.Invocation, len(f.got))
	copy(out, f.got)
	return out
}

// testTree builds a small cobra tree with leaves of varying safety
// classes used across the REST surface tests:
//
//	root
//	├── widget add               (write)
//	├── widget delete            (destructive)
//	├── widget secure            (auth-required, write)
//	├── widget confirm           (requires-confirmation, write)
//	└── ping                     (read)
func testTree() *cobra.Command {
	root := &cobra.Command{Use: "root"}

	widget := &cobra.Command{Use: "widget"}
	add := &cobra.Command{
		Use:         "add",
		Short:       "Add a widget",
		Long:        "Add a new widget to the inventory.",
		RunE:        func(*cobra.Command, []string) error { return nil },
		Annotations: map[string]string{"kit/side-effect": "write"},
	}
	del := &cobra.Command{
		Use:         "delete",
		Short:       "Delete a widget",
		RunE:        func(*cobra.Command, []string) error { return nil },
		Annotations: map[string]string{"kit/side-effect": "destructive"},
	}
	secure := &cobra.Command{
		Use:   "secure",
		Short: "Mutate a secure resource",
		RunE:  func(*cobra.Command, []string) error { return nil },
		Annotations: map[string]string{
			"kit/side-effect":   "write",
			"kit/auth-required": "true",
		},
	}
	confirm := &cobra.Command{
		Use:   "confirm",
		Short: "Two-step operation",
		RunE:  func(*cobra.Command, []string) error { return nil },
		Annotations: map[string]string{
			"kit/side-effect":           "write",
			"kit/requires-confirmation": "true",
		},
	}
	widget.AddCommand(add, del, secure, confirm)
	root.AddCommand(widget)

	ping := &cobra.Command{
		Use:         "ping",
		Short:       "Ping the bridge",
		RunE:        func(*cobra.Command, []string) error { return nil },
		Annotations: map[string]string{"kit/side-effect": "read"},
	}
	root.AddCommand(ping)

	return root
}

// newServer builds a server with the given bridge configuration and
// returns a teardown closure.
func newServer(t *testing.T, b *cmdsurface.Bridge, opts ...cmdsurface.RESTOption) (string, func()) {
	t.Helper()
	router := api.NewRouter()
	if err := cmdsurface.MountREST(b, router, opts...); err != nil {
		t.Fatalf("MountREST: %v", err)
	}
	srv := httptest.NewServer(router)
	return srv.URL, srv.Close
}

// post is a tiny helper for body-bearing POSTs that returns the
// decoded body (or APIError) plus the status code.
func post(t *testing.T, url string, body any, headers map[string]string) (int, []byte) {
	t.Helper()
	var r io.Reader
	if body != nil {
		switch v := body.(type) {
		case string:
			r = strings.NewReader(v)
		case []byte:
			r = bytes.NewReader(v)
		default:
			buf, err := json.Marshal(body)
			if err != nil {
				t.Fatalf("marshal body: %v", err)
			}
			r = bytes.NewReader(buf)
		}
	}
	req, err := http.NewRequest(http.MethodPost, url, r)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer resp.Body.Close()
	buf, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, buf
}

func TestRESTSurface_HappyPath(t *testing.T) {
	runner := &recordingRunner{
		RunFn: func(_ context.Context, inv cmdsurface.Invocation) (cmdsurface.Result, error) {
			return cmdsurface.Result{Stdout: "ok"}, nil
		},
	}
	b := cmdsurface.New(testTree(), cmdsurface.WithRunner(runner)).
		Expose("ping", cmdsurface.SurfaceREST)

	url, stop := newServer(t, b)
	defer stop()

	status, body := post(t, url+"/cmd/ping", cmdsurface.Invocation{}, nil)
	if status != http.StatusOK {
		t.Fatalf("status=%d want 200; body=%s", status, body)
	}
	var got cmdsurface.Result
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decode: %v; body=%s", err, body)
	}
	if got.Stdout != "ok" {
		t.Errorf("Stdout=%q want ok", got.Stdout)
	}
}

func TestRESTSurface_UnknownCommand(t *testing.T) {
	// Only "ping" is exposed; "/cmd/widget/missing" path does not map
	// to a mounted route, so the router itself returns 404.
	b := cmdsurface.New(testTree(), cmdsurface.WithRunner(&recordingRunner{})).
		Expose("ping", cmdsurface.SurfaceREST)
	url, stop := newServer(t, b)
	defer stop()

	status, _ := post(t, url+"/cmd/widget/missing", cmdsurface.Invocation{}, nil)
	if status != http.StatusNotFound {
		t.Errorf("status=%d want 404", status)
	}
}

func TestRESTSurface_SurfaceNotEnabled(t *testing.T) {
	// Mount nothing under REST: every call to a known leaf hits the
	// 404 path because the route is never registered. To exercise the
	// ErrSurfaceNotEnabled code path through the handler, we manually
	// register a handler against a leaf that the bridge does NOT have
	// REST enabled for.
	runner := &recordingRunner{}
	b := cmdsurface.New(testTree(), cmdsurface.WithRunner(runner)).
		Expose("ping", cmdsurface.SurfaceREST)

	router := api.NewRouter()
	if err := cmdsurface.MountREST(b, router); err != nil {
		t.Fatalf("MountREST: %v", err)
	}
	// Manually register a route that points at the same handler logic
	// but for a leaf whose REST surface is OFF (widget add). We do
	// this by re-mounting after flipping REST on widget add, hitting
	// the path, then flipping it off again before the request runs.
	// Simpler: register a hand-rolled handler that calls Bridge.Invoke
	// directly with Surface=REST so the bridge raises ErrSurfaceNotEnabled.
	router.Handle(http.MethodPost, "/raw/widget/add", func(w http.ResponseWriter, r *http.Request) {
		_, err := b.Invoke(r.Context(), cmdsurface.Invocation{
			Path: []string{"widget", "add"},
			Meta: cmdsurface.Meta{Surface: cmdsurface.SurfaceREST},
		})
		if err == nil {
			t.Errorf("expected ErrSurfaceNotEnabled from bridge")
			return
		}
		// Use the package's own mapping by surfacing the error through
		// a public surface: assert via errors.Is and write our own
		// minimal response.
		if !errors.Is(err, cmdsurface.ErrSurfaceNotEnabled) {
			t.Errorf("err=%v want ErrSurfaceNotEnabled", err)
		}
		api.Error(w, http.StatusNotFound, &api.APIError{
			Status:  http.StatusNotFound,
			Code:    "not_enabled",
			Message: err.Error(),
		})
	})
	srv := httptest.NewServer(router)
	defer srv.Close()

	status, body := post(t, srv.URL+"/raw/widget/add", nil, nil)
	if status != http.StatusNotFound {
		t.Fatalf("status=%d want 404; body=%s", status, body)
	}
	var ae api.APIError
	if err := json.Unmarshal(body, &ae); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if ae.Code != "not_enabled" {
		t.Errorf("code=%q want not_enabled", ae.Code)
	}
}

func TestRESTSurface_DestructiveBlocked(t *testing.T) {
	// Expose REST on the destructive leaf with no AllowDestructiveOn:
	// the route registers, but Bridge.Invoke refuses with
	// ErrDestructiveBlocked, which the handler maps to 403.
	runner := &recordingRunner{}
	b := cmdsurface.New(testTree(), cmdsurface.WithRunner(runner)).
		Expose("widget delete", cmdsurface.SurfaceREST)
	url, stop := newServer(t, b)
	defer stop()

	status, body := post(t, url+"/cmd/widget/delete", cmdsurface.Invocation{}, nil)
	if status != http.StatusForbidden {
		t.Fatalf("status=%d want 403; body=%s", status, body)
	}
	var ae api.APIError
	if err := json.Unmarshal(body, &ae); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if ae.Code != "destructive_blocked" {
		t.Errorf("code=%q want destructive_blocked", ae.Code)
	}
	if len(runner.captured()) != 0 {
		t.Errorf("runner saw invocations despite block: %v", runner.captured())
	}
}

func TestRESTSurface_DestructiveAllowed(t *testing.T) {
	runner := &recordingRunner{
		RunFn: func(_ context.Context, _ cmdsurface.Invocation) (cmdsurface.Result, error) {
			return cmdsurface.Result{Stdout: "deleted"}, nil
		},
	}
	b := cmdsurface.New(testTree(),
		cmdsurface.WithRunner(runner),
		cmdsurface.WithPolicy(cmdsurface.Policy{
			AllowDestructiveOn: []cmdsurface.Surface{cmdsurface.SurfaceREST},
			DefaultEnabled:     []cmdsurface.Surface{cmdsurface.SurfaceCLI, cmdsurface.SurfaceLib},
		}),
	).Expose("widget delete", cmdsurface.SurfaceREST)
	url, stop := newServer(t, b)
	defer stop()

	status, body := post(t, url+"/cmd/widget/delete", cmdsurface.Invocation{}, nil)
	if status != http.StatusOK {
		t.Fatalf("status=%d want 200; body=%s", status, body)
	}
	var got cmdsurface.Result
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Stdout != "deleted" {
		t.Errorf("Stdout=%q want deleted", got.Stdout)
	}
}

func TestRESTSurface_AuthRequired_NoAuth(t *testing.T) {
	// No WithRESTAuth: handler installs denyAuth, every call → 401.
	b := cmdsurface.New(testTree(), cmdsurface.WithRunner(&recordingRunner{})).
		Expose("widget secure", cmdsurface.SurfaceREST)
	url, stop := newServer(t, b)
	defer stop()

	status, _ := post(t, url+"/cmd/widget/secure", cmdsurface.Invocation{}, nil)
	if status != http.StatusUnauthorized {
		t.Errorf("status=%d want 401", status)
	}
}

func TestRESTSurface_AuthRequired_WithAuth(t *testing.T) {
	b := cmdsurface.New(testTree(), cmdsurface.WithRunner(&recordingRunner{})).
		Expose("widget secure", cmdsurface.SurfaceREST)
	authFn := func(r *http.Request) (any, error) {
		if r.Header.Get("Authorization") == "" {
			return nil, errors.New("missing token")
		}
		return "ok", nil
	}
	url, stop := newServer(t, b, cmdsurface.WithRESTAuth(authFn))
	defer stop()

	status, _ := post(t, url+"/cmd/widget/secure", cmdsurface.Invocation{}, map[string]string{
		"Authorization": "Bearer t",
	})
	if status != http.StatusOK {
		t.Errorf("status=%d want 200", status)
	}
}

func TestRESTSurface_ConfirmationRequired(t *testing.T) {
	b := cmdsurface.New(testTree(), cmdsurface.WithRunner(&recordingRunner{})).
		Expose("widget confirm", cmdsurface.SurfaceREST)
	url, stop := newServer(t, b)
	defer stop()

	status, body := post(t, url+"/cmd/widget/confirm", cmdsurface.Invocation{}, nil)
	if status != http.StatusPreconditionRequired {
		t.Fatalf("status=%d want 428; body=%s", status, body)
	}
	var ae api.APIError
	if err := json.Unmarshal(body, &ae); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if ae.Code != "confirmation_required" {
		t.Errorf("code=%q want confirmation_required", ae.Code)
	}
}

func TestRESTSurface_ConfirmationPresent(t *testing.T) {
	b := cmdsurface.New(testTree(), cmdsurface.WithRunner(&recordingRunner{})).
		Expose("widget confirm", cmdsurface.SurfaceREST)
	url, stop := newServer(t, b)
	defer stop()

	status, _ := post(t, url+"/cmd/widget/confirm", cmdsurface.Invocation{}, map[string]string{
		"X-Confirm-Token": "any-token-shape",
	})
	if status != http.StatusOK {
		t.Errorf("status=%d want 200", status)
	}
}

func TestRESTSurface_BodyDecodeError(t *testing.T) {
	b := cmdsurface.New(testTree(), cmdsurface.WithRunner(&recordingRunner{})).
		Expose("ping", cmdsurface.SurfaceREST)
	url, stop := newServer(t, b)
	defer stop()

	status, body := post(t, url+"/cmd/ping", "not-json", nil)
	if status != http.StatusBadRequest {
		t.Fatalf("status=%d want 400; body=%s", status, body)
	}
	var ae api.APIError
	if err := json.Unmarshal(body, &ae); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if ae.Code != "bad_request" {
		t.Errorf("code=%q want bad_request", ae.Code)
	}
}

func TestRESTSurface_ExitCodePreserved(t *testing.T) {
	runner := &recordingRunner{
		RunFn: func(_ context.Context, _ cmdsurface.Invocation) (cmdsurface.Result, error) {
			return cmdsurface.Result{ExitCode: 2, Stderr: "oops"}, nil
		},
	}
	b := cmdsurface.New(testTree(), cmdsurface.WithRunner(runner)).
		Expose("ping", cmdsurface.SurfaceREST)
	url, stop := newServer(t, b)
	defer stop()

	status, body := post(t, url+"/cmd/ping", cmdsurface.Invocation{}, nil)
	if status != http.StatusOK {
		t.Fatalf("status=%d want 200 (exit code does not change status); body=%s", status, body)
	}
	var got cmdsurface.Result
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.ExitCode != 2 {
		t.Errorf("ExitCode=%d want 2", got.ExitCode)
	}
	if got.Stderr != "oops" {
		t.Errorf("Stderr=%q want oops", got.Stderr)
	}
}

func TestRESTSurface_CallerPathOverridden(t *testing.T) {
	runner := &recordingRunner{}
	b := cmdsurface.New(testTree(), cmdsurface.WithRunner(runner)).
		Expose("widget add", cmdsurface.SurfaceREST)
	url, stop := newServer(t, b)
	defer stop()

	// Lie in the body: pass a bogus path. The handler must override.
	status, _ := post(t, url+"/cmd/widget/add", map[string]any{
		"path": []string{"bogus", "evil"},
		"args": []string{"x"},
	}, nil)
	if status != http.StatusOK {
		t.Fatalf("status=%d want 200", status)
	}
	got := runner.captured()
	if len(got) != 1 {
		t.Fatalf("captured=%d want 1", len(got))
	}
	want := []string{"widget", "add"}
	if !equalSlice(got[0].Path, want) {
		t.Errorf("runner saw path=%v want=%v", got[0].Path, want)
	}
}

func TestRESTSurface_MetaSurfaceForced(t *testing.T) {
	runner := &recordingRunner{}
	b := cmdsurface.New(testTree(), cmdsurface.WithRunner(runner)).
		Expose("ping", cmdsurface.SurfaceREST)
	url, stop := newServer(t, b)
	defer stop()

	// Caller claims a different surface in the body — handler must overwrite.
	status, _ := post(t, url+"/cmd/ping", map[string]any{
		"meta": map[string]any{"surface": "cli", "caller": "u"},
	}, nil)
	if status != http.StatusOK {
		t.Fatalf("status=%d want 200", status)
	}
	got := runner.captured()
	if len(got) != 1 {
		t.Fatalf("captured=%d want 1", len(got))
	}
	if got[0].Meta.Surface != cmdsurface.SurfaceREST {
		t.Errorf("Meta.Surface=%q want rest", got[0].Meta.Surface)
	}
	if got[0].Meta.Caller != "u" {
		t.Errorf("Meta.Caller=%q want u (caller-provided fields preserved)", got[0].Meta.Caller)
	}
}

func TestRESTSurface_OpenAPI_Registration(t *testing.T) {
	runner := &recordingRunner{}
	b := cmdsurface.New(testTree(), cmdsurface.WithRunner(runner)).
		Expose("ping", cmdsurface.SurfaceREST).
		Expose("widget add", cmdsurface.SurfaceREST).
		Expose("widget delete", cmdsurface.SurfaceREST)

	router := api.NewRouter(api.WithOpenAPI(api.OpenAPIConfig{
		Title:   "cmdsurface test",
		Version: "0.0.0",
	}))
	humaAPI := api.HumaAPI(router)
	if humaAPI == nil {
		t.Fatal("HumaAPI should be non-nil after WithOpenAPI")
	}

	if err := cmdsurface.MountREST(b, router,
		cmdsurface.WithRESTOpenAPI(humaAPI),
	); err != nil {
		t.Fatalf("MountREST: %v", err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/openapi.json", nil)
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("openapi.json status=%d body=%s", rec.Code, rec.Body.String())
	}

	var spec map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &spec); err != nil {
		t.Fatalf("decode spec: %v", err)
	}
	paths, ok := spec["paths"].(map[string]any)
	if !ok {
		t.Fatalf("spec has no paths: %v", spec)
	}
	wantPaths := []string{"/cmd/ping", "/cmd/widget/add", "/cmd/widget/delete"}
	for _, p := range wantPaths {
		op, present := paths[p]
		if !present {
			t.Errorf("missing OpenAPI path %s", p)
			continue
		}
		mp, _ := op.(map[string]any)
		if _, hasPost := mp["post"]; !hasPost {
			t.Errorf("path %s has no POST operation", p)
		}
	}
	// Check operation id and destructive prefix on the delete leaf.
	if mp, _ := paths["/cmd/widget/delete"].(map[string]any); mp != nil {
		if post, _ := mp["post"].(map[string]any); post != nil {
			if id, _ := post["operationId"].(string); id != "cmd_widget_delete" {
				t.Errorf("operationId=%q want cmd_widget_delete", id)
			}
			if sum, _ := post["summary"].(string); !strings.HasPrefix(sum, "[destructive]") {
				t.Errorf("summary=%q want [destructive] prefix", sum)
			}
		}
	}
}

func equalSlice(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
