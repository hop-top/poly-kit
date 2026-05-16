package cmdsurface_test

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
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

// webhookFakeRunner records every Invocation and dispatches RunFn if set.
type webhookFakeRunner struct {
	mu  sync.Mutex
	got []cmdsurface.Invocation

	RunFn func(ctx context.Context, inv cmdsurface.Invocation) (cmdsurface.Result, error)
}

func (f *webhookFakeRunner) Run(ctx context.Context, inv cmdsurface.Invocation) (cmdsurface.Result, error) {
	f.mu.Lock()
	f.got = append(f.got, inv)
	f.mu.Unlock()
	if f.RunFn != nil {
		return f.RunFn(ctx, inv)
	}
	return cmdsurface.Result{Stdout: strings.Join(inv.Path, " ")}, nil
}

func (f *webhookFakeRunner) Stream(context.Context, cmdsurface.Invocation, chan<- cmdsurface.Event) error {
	return errors.New("webhookFakeRunner: Stream not supported")
}

func (f *webhookFakeRunner) captured() []cmdsurface.Invocation {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]cmdsurface.Invocation, len(f.got))
	copy(out, f.got)
	return out
}

// webhookTestTree mirrors the REST tree (intentionally separate to
// satisfy the naming discipline for sibling agents).
func webhookTestTree() *cobra.Command {
	root := &cobra.Command{Use: "root"}

	widget := &cobra.Command{Use: "widget"}
	add := &cobra.Command{
		Use:         "add",
		Short:       "Add a widget",
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
		Short:       "Ping",
		RunE:        func(*cobra.Command, []string) error { return nil },
		Annotations: map[string]string{"kit/side-effect": "read"},
	}
	root.AddCommand(ping)

	return root
}

// webhookNewServer builds a server with the given mappings.
func webhookNewServer(
	t *testing.T,
	b *cmdsurface.Bridge,
	mappings []cmdsurface.WebhookMapping,
	opts ...cmdsurface.WebhookOption,
) (string, func()) {
	t.Helper()
	router := api.NewRouter()
	if err := cmdsurface.MountWebhooks(b, router, mappings, opts...); err != nil {
		t.Fatalf("MountWebhooks: %v", err)
	}
	srv := httptest.NewServer(router)
	return srv.URL, srv.Close
}

// webhookPost POSTs body to url with the given headers and content type.
func webhookPost(t *testing.T, url string, body []byte, contentType string, headers map[string]string) (int, []byte) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
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

// webhookTestHMAC returns a hex-encoded HMAC-SHA256 digest of body.
func webhookTestHMAC(secret, body []byte) string {
	m := hmac.New(sha256.New, secret)
	m.Write(body)
	return hex.EncodeToString(m.Sum(nil))
}

// webhookBridge constructs a bridge with the standard test tree and
// applies Expose for each (pattern, surfaces...) pair.
func webhookBridge(t *testing.T, runner cmdsurface.Runner, exposes map[string][]cmdsurface.Surface, policy *cmdsurface.Policy) *cmdsurface.Bridge {
	t.Helper()
	opts := []cmdsurface.Option{cmdsurface.WithRunner(runner)}
	if policy != nil {
		opts = append(opts, cmdsurface.WithPolicy(*policy))
	}
	b := cmdsurface.New(webhookTestTree(), opts...)
	for pat, sfs := range exposes {
		b.Expose(pat, sfs...)
	}
	return b
}

func TestWebhook_HappyPath(t *testing.T) {
	runner := &webhookFakeRunner{}
	b := webhookBridge(t, runner, map[string][]cmdsurface.Surface{
		"widget add": {cmdsurface.SurfaceWebhook},
	}, nil)

	url, stop := webhookNewServer(t, b, []cmdsurface.WebhookMapping{{
		Name:    "create",
		Path:    []string{"widget", "add"},
		FlagMap: map[string]string{"name": "{{ .body.title }}"},
		Auth:    cmdsurface.AuthNone{},
	}})
	defer stop()

	body := []byte(`{"title":"foo"}`)
	status, buf := webhookPost(t, url+"/hooks/create", body, "application/json", nil)
	if status != http.StatusAccepted {
		t.Fatalf("status=%d want 202; body=%s", status, buf)
	}
	got := runner.captured()
	if len(got) != 1 {
		t.Fatalf("captured=%d want 1", len(got))
	}
	if v := got[0].Flags["name"]; v != "foo" {
		t.Errorf("flags[name]=%v want foo", v)
	}
	if got[0].Meta.Surface != cmdsurface.SurfaceWebhook {
		t.Errorf("Meta.Surface=%q want webhook", got[0].Meta.Surface)
	}
	if got[0].Meta.Caller != "create" {
		t.Errorf("Meta.Caller=%q want create", got[0].Meta.Caller)
	}
}

func TestWebhook_UnknownMappingName(t *testing.T) {
	runner := &webhookFakeRunner{}
	b := webhookBridge(t, runner, map[string][]cmdsurface.Surface{
		"widget add": {cmdsurface.SurfaceWebhook},
	}, nil)
	url, stop := webhookNewServer(t, b, []cmdsurface.WebhookMapping{{
		Name: "create",
		Path: []string{"widget", "add"},
		Auth: cmdsurface.AuthNone{},
	}})
	defer stop()

	status, _ := webhookPost(t, url+"/hooks/bogus", []byte(`{}`), "application/json", nil)
	if status != http.StatusNotFound {
		t.Errorf("status=%d want 404", status)
	}
}

func TestWebhook_AuthHMAC_Valid(t *testing.T) {
	secret := []byte("topsecret")
	runner := &webhookFakeRunner{}
	b := webhookBridge(t, runner, map[string][]cmdsurface.Surface{
		"widget add": {cmdsurface.SurfaceWebhook},
	}, nil)
	url, stop := webhookNewServer(t, b, []cmdsurface.WebhookMapping{{
		Name: "github",
		Path: []string{"widget", "add"},
		Auth: cmdsurface.AuthHMAC{
			Header: "X-Hub-Signature-256",
			Prefix: "sha256=",
			Secret: secret,
		},
	}})
	defer stop()

	body := []byte(`{"title":"x"}`)
	sig := "sha256=" + webhookTestHMAC(secret, body)
	status, buf := webhookPost(t, url+"/hooks/github", body, "application/json",
		map[string]string{"X-Hub-Signature-256": sig})
	if status != http.StatusAccepted {
		t.Fatalf("status=%d want 202; body=%s", status, buf)
	}
}

func TestWebhook_AuthHMAC_Invalid(t *testing.T) {
	secret := []byte("topsecret")
	b := webhookBridge(t, &webhookFakeRunner{}, map[string][]cmdsurface.Surface{
		"widget add": {cmdsurface.SurfaceWebhook},
	}, nil)
	url, stop := webhookNewServer(t, b, []cmdsurface.WebhookMapping{{
		Name: "github",
		Path: []string{"widget", "add"},
		Auth: cmdsurface.AuthHMAC{
			Header: "X-Hub-Signature-256",
			Prefix: "sha256=",
			Secret: secret,
		},
	}})
	defer stop()

	body := []byte(`{"title":"x"}`)
	bogus := "sha256=" + webhookTestHMAC([]byte("wrong"), body)
	status, body2 := webhookPost(t, url+"/hooks/github", body, "application/json",
		map[string]string{"X-Hub-Signature-256": bogus})
	if status != http.StatusUnauthorized {
		t.Fatalf("status=%d want 401; body=%s", status, body2)
	}
	var ae api.APIError
	if err := json.Unmarshal(body2, &ae); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if ae.Code != "unauthorized" {
		t.Errorf("code=%q want unauthorized", ae.Code)
	}
}

func TestWebhook_AuthBearer_Valid(t *testing.T) {
	runner := &webhookFakeRunner{}
	b := webhookBridge(t, runner, map[string][]cmdsurface.Surface{
		"widget add": {cmdsurface.SurfaceWebhook},
	}, nil)
	url, stop := webhookNewServer(t, b, []cmdsurface.WebhookMapping{{
		Name: "tok",
		Path: []string{"widget", "add"},
		Auth: cmdsurface.AuthBearer{Token: "abc123"},
	}})
	defer stop()

	status, buf := webhookPost(t, url+"/hooks/tok", []byte(`{}`), "application/json",
		map[string]string{"Authorization": "Bearer abc123"})
	if status != http.StatusAccepted {
		t.Fatalf("status=%d want 202; body=%s", status, buf)
	}
}

func TestWebhook_AuthBearer_Invalid(t *testing.T) {
	b := webhookBridge(t, &webhookFakeRunner{}, map[string][]cmdsurface.Surface{
		"widget add": {cmdsurface.SurfaceWebhook},
	}, nil)
	url, stop := webhookNewServer(t, b, []cmdsurface.WebhookMapping{{
		Name: "tok",
		Path: []string{"widget", "add"},
		Auth: cmdsurface.AuthBearer{Token: "abc123"},
	}})
	defer stop()

	status, _ := webhookPost(t, url+"/hooks/tok", []byte(`{}`), "application/json",
		map[string]string{"Authorization": "Bearer wrong"})
	if status != http.StatusUnauthorized {
		t.Errorf("status=%d want 401", status)
	}
}

func TestWebhook_AuthNone(t *testing.T) {
	runner := &webhookFakeRunner{}
	b := webhookBridge(t, runner, map[string][]cmdsurface.Surface{
		"widget add": {cmdsurface.SurfaceWebhook},
	}, nil)
	url, stop := webhookNewServer(t, b, []cmdsurface.WebhookMapping{{
		Name: "open",
		Path: []string{"widget", "add"},
		Auth: cmdsurface.AuthNone{},
	}})
	defer stop()

	status, _ := webhookPost(t, url+"/hooks/open", []byte(`{}`), "application/json", nil)
	if status != http.StatusAccepted {
		t.Errorf("status=%d want 202", status)
	}
}

func TestWebhook_BodyTooLarge(t *testing.T) {
	b := webhookBridge(t, &webhookFakeRunner{}, map[string][]cmdsurface.Surface{
		"widget add": {cmdsurface.SurfaceWebhook},
	}, nil)
	url, stop := webhookNewServer(t, b,
		[]cmdsurface.WebhookMapping{{
			Name: "create",
			Path: []string{"widget", "add"},
			Auth: cmdsurface.AuthNone{},
		}},
		cmdsurface.WithWebhookMaxBody(10),
	)
	defer stop()

	big := bytes.Repeat([]byte("a"), 1024)
	status, buf := webhookPost(t, url+"/hooks/create", big, "application/json", nil)
	if status != http.StatusRequestEntityTooLarge {
		t.Fatalf("status=%d want 413; body=%s", status, buf)
	}
	var ae api.APIError
	if err := json.Unmarshal(buf, &ae); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if ae.Code != "payload_too_large" {
		t.Errorf("code=%q want payload_too_large", ae.Code)
	}
}

func TestWebhook_BodyNotJSON_HeadersOnly(t *testing.T) {
	runner := &webhookFakeRunner{}
	b := webhookBridge(t, runner, map[string][]cmdsurface.Surface{
		"widget add": {cmdsurface.SurfaceWebhook},
	}, nil)
	url, stop := webhookNewServer(t, b, []cmdsurface.WebhookMapping{{
		Name:    "h",
		Path:    []string{"widget", "add"},
		FlagMap: map[string]string{"src": `{{ index .headers "X-Source" }}`},
		Auth:    cmdsurface.AuthNone{},
	}})
	defer stop()

	status, buf := webhookPost(t, url+"/hooks/h", []byte("plain text"), "text/plain",
		map[string]string{"X-Source": "alpha"})
	if status != http.StatusAccepted {
		t.Fatalf("status=%d want 202; body=%s", status, buf)
	}
	got := runner.captured()
	if len(got) != 1 {
		t.Fatalf("captured=%d want 1", len(got))
	}
	if v := got[0].Flags["src"]; v != "alpha" {
		t.Errorf("flags[src]=%v want alpha", v)
	}
}

func TestWebhook_BodyNotJSON_TemplateRefersBody(t *testing.T) {
	runner := &webhookFakeRunner{}
	b := webhookBridge(t, runner, map[string][]cmdsurface.Surface{
		"widget add": {cmdsurface.SurfaceWebhook},
	}, nil)
	url, stop := webhookNewServer(t, b, []cmdsurface.WebhookMapping{{
		Name:    "b",
		Path:    []string{"widget", "add"},
		FlagMap: map[string]string{"x": "{{ .body.missing }}"},
		Auth:    cmdsurface.AuthNone{},
	}})
	defer stop()

	status, _ := webhookPost(t, url+"/hooks/b", []byte("not json"), "text/plain", nil)
	if status != http.StatusAccepted {
		t.Fatalf("status=%d want 202", status)
	}
	got := runner.captured()
	if len(got) != 1 {
		t.Fatalf("captured=%d want 1", len(got))
	}
	if v := got[0].Flags["x"]; v != "<no value>" {
		t.Errorf("flags[x]=%v want <no value>", v)
	}
}

func TestWebhook_UnknownTemplateRootRejected(t *testing.T) {
	b := webhookBridge(t, &webhookFakeRunner{}, map[string][]cmdsurface.Surface{
		"widget add": {cmdsurface.SurfaceWebhook},
	}, nil)
	router := api.NewRouter()
	err := cmdsurface.MountWebhooks(b, router, []cmdsurface.WebhookMapping{{
		Name:    "bad",
		Path:    []string{"widget", "add"},
		FlagMap: map[string]string{"x": "{{ .bogus.foo }}"},
		Auth:    cmdsurface.AuthNone{},
	}})
	if err == nil {
		t.Fatal("MountWebhooks succeeded; want disallowed-root error")
	}
	if !strings.Contains(err.Error(), "disallowed template root") {
		t.Errorf("err=%q want contains 'disallowed template root'", err)
	}
}

func TestWebhook_ArgsTemplate_Single(t *testing.T) {
	runner := &webhookFakeRunner{}
	b := webhookBridge(t, runner, map[string][]cmdsurface.Surface{
		"widget add": {cmdsurface.SurfaceWebhook},
	}, nil)
	url, stop := webhookNewServer(t, b, []cmdsurface.WebhookMapping{{
		Name:         "a",
		Path:         []string{"widget", "add"},
		ArgsTemplate: "{{ .body.id }}",
		Auth:         cmdsurface.AuthNone{},
	}})
	defer stop()

	status, _ := webhookPost(t, url+"/hooks/a", []byte(`{"id":"42"}`), "application/json", nil)
	if status != http.StatusAccepted {
		t.Fatalf("status=%d want 202", status)
	}
	got := runner.captured()
	if len(got) != 1 {
		t.Fatalf("captured=%d want 1", len(got))
	}
	if len(got[0].Args) != 1 || got[0].Args[0] != "42" {
		t.Errorf("Args=%v want [42]", got[0].Args)
	}
}

func TestWebhook_ArgsTemplate_Multi(t *testing.T) {
	runner := &webhookFakeRunner{}
	b := webhookBridge(t, runner, map[string][]cmdsurface.Surface{
		"widget add": {cmdsurface.SurfaceWebhook},
	}, nil)
	url, stop := webhookNewServer(t, b, []cmdsurface.WebhookMapping{{
		Name:         "a2",
		Path:         []string{"widget", "add"},
		ArgsTemplate: "{{ .body.x }} {{ .body.y }}",
		Auth:         cmdsurface.AuthNone{},
	}})
	defer stop()

	status, _ := webhookPost(t, url+"/hooks/a2", []byte(`{"x":"a","y":"b"}`), "application/json", nil)
	if status != http.StatusAccepted {
		t.Fatalf("status=%d want 202", status)
	}
	got := runner.captured()
	if len(got) != 1 {
		t.Fatalf("captured=%d want 1", len(got))
	}
	if len(got[0].Args) != 2 || got[0].Args[0] != "a" || got[0].Args[1] != "b" {
		t.Errorf("Args=%v want [a b]", got[0].Args)
	}
}

func TestWebhook_MountUnknownLeaf(t *testing.T) {
	b := webhookBridge(t, &webhookFakeRunner{}, nil, nil)
	router := api.NewRouter()
	err := cmdsurface.MountWebhooks(b, router, []cmdsurface.WebhookMapping{{
		Name: "x",
		Path: []string{"does", "not", "exist"},
		Auth: cmdsurface.AuthNone{},
	}})
	if err == nil || !errors.Is(err, cmdsurface.ErrUnknownCommand) {
		t.Fatalf("err=%v want ErrUnknownCommand", err)
	}
}

func TestWebhook_MountSurfaceNotEnabled(t *testing.T) {
	// Default policy enables CLI/Lib/MCP only; webhook is NOT default.
	b := webhookBridge(t, &webhookFakeRunner{}, nil, nil)
	router := api.NewRouter()
	err := cmdsurface.MountWebhooks(b, router, []cmdsurface.WebhookMapping{{
		Name: "x",
		Path: []string{"widget", "add"},
		Auth: cmdsurface.AuthNone{},
	}})
	if err == nil || !errors.Is(err, cmdsurface.ErrSurfaceNotEnabled) {
		t.Fatalf("err=%v want ErrSurfaceNotEnabled", err)
	}
}

func TestWebhook_MountDestructiveWithoutOptIn(t *testing.T) {
	b := webhookBridge(t, &webhookFakeRunner{}, map[string][]cmdsurface.Surface{
		"widget delete": {cmdsurface.SurfaceWebhook},
	}, nil)
	router := api.NewRouter()
	err := cmdsurface.MountWebhooks(b, router, []cmdsurface.WebhookMapping{{
		Name: "del",
		Path: []string{"widget", "delete"},
		Auth: cmdsurface.AuthBearer{Token: "t"},
	}})
	if err == nil || !errors.Is(err, cmdsurface.ErrDestructiveBlocked) {
		t.Fatalf("err=%v want ErrDestructiveBlocked", err)
	}
}

func TestWebhook_DestructiveAllowed(t *testing.T) {
	policy := cmdsurface.Policy{
		AllowDestructiveOn: []cmdsurface.Surface{cmdsurface.SurfaceWebhook},
		DefaultEnabled:     []cmdsurface.Surface{cmdsurface.SurfaceCLI, cmdsurface.SurfaceLib},
	}
	runner := &webhookFakeRunner{}
	b := webhookBridge(t, runner, map[string][]cmdsurface.Surface{
		"widget delete": {cmdsurface.SurfaceWebhook},
	}, &policy)
	url, stop := webhookNewServer(t, b, []cmdsurface.WebhookMapping{{
		Name: "del",
		Path: []string{"widget", "delete"},
		Auth: cmdsurface.AuthBearer{Token: "t"},
	}})
	defer stop()

	status, _ := webhookPost(t, url+"/hooks/del", []byte(`{}`), "application/json",
		map[string]string{"Authorization": "Bearer t"})
	if status != http.StatusAccepted {
		t.Errorf("status=%d want 202", status)
	}
}

func TestWebhook_MountAuthRequiredWithAuthNone(t *testing.T) {
	b := webhookBridge(t, &webhookFakeRunner{}, map[string][]cmdsurface.Surface{
		"widget secure": {cmdsurface.SurfaceWebhook},
	}, nil)
	router := api.NewRouter()
	err := cmdsurface.MountWebhooks(b, router, []cmdsurface.WebhookMapping{{
		Name: "s",
		Path: []string{"widget", "secure"},
		Auth: cmdsurface.AuthNone{},
	}})
	if err == nil {
		t.Fatal("MountWebhooks succeeded; want auth-required+AuthNone error")
	}
	if !strings.Contains(err.Error(), "auth-required") {
		t.Errorf("err=%q want contains 'auth-required'", err)
	}
}

func TestWebhook_MountConfirmationWithoutOptIn(t *testing.T) {
	b := webhookBridge(t, &webhookFakeRunner{}, map[string][]cmdsurface.Surface{
		"widget confirm": {cmdsurface.SurfaceWebhook},
	}, nil)
	router := api.NewRouter()
	err := cmdsurface.MountWebhooks(b, router, []cmdsurface.WebhookMapping{{
		Name: "c",
		Path: []string{"widget", "confirm"},
		Auth: cmdsurface.AuthBearer{Token: "t"},
	}})
	if err == nil {
		t.Fatal("MountWebhooks succeeded; want confirmation-required error")
	}
	if !strings.Contains(err.Error(), "confirmation-required") {
		t.Errorf("err=%q want contains 'confirmation-required'", err)
	}
}

func TestWebhook_ConfirmationAllowed(t *testing.T) {
	runner := &webhookFakeRunner{}
	b := webhookBridge(t, runner, map[string][]cmdsurface.Surface{
		"widget confirm": {cmdsurface.SurfaceWebhook},
	}, nil)
	url, stop := webhookNewServer(t, b,
		[]cmdsurface.WebhookMapping{{
			Name: "c",
			Path: []string{"widget", "confirm"},
			Auth: cmdsurface.AuthBearer{Token: "t"},
		}},
		cmdsurface.WithWebhookAllowConfirmation(),
	)
	defer stop()

	status, _ := webhookPost(t, url+"/hooks/c", []byte(`{}`), "application/json",
		map[string]string{"Authorization": "Bearer t"})
	if status != http.StatusAccepted {
		t.Errorf("status=%d want 202", status)
	}
}

func TestWebhook_ResultLogCalled(t *testing.T) {
	runner := &webhookFakeRunner{
		RunFn: func(_ context.Context, _ cmdsurface.Invocation) (cmdsurface.Result, error) {
			return cmdsurface.Result{Stdout: "logged"}, nil
		},
	}
	b := webhookBridge(t, runner, map[string][]cmdsurface.Surface{
		"widget add": {cmdsurface.SurfaceWebhook},
	}, nil)

	var (
		mu      sync.Mutex
		calls   int
		lastRes cmdsurface.Result
		lastErr error
		lastMap cmdsurface.WebhookMapping
	)
	url, stop := webhookNewServer(t, b,
		[]cmdsurface.WebhookMapping{{
			Name: "log",
			Path: []string{"widget", "add"},
			Auth: cmdsurface.AuthNone{},
		}},
		cmdsurface.WithWebhookResultLog(func(m cmdsurface.WebhookMapping, r cmdsurface.Result, err error) {
			mu.Lock()
			defer mu.Unlock()
			calls++
			lastMap = m
			lastRes = r
			lastErr = err
		}),
	)
	defer stop()

	status, _ := webhookPost(t, url+"/hooks/log", []byte(`{}`), "application/json", nil)
	if status != http.StatusAccepted {
		t.Fatalf("status=%d want 202", status)
	}
	mu.Lock()
	defer mu.Unlock()
	if calls != 1 {
		t.Errorf("calls=%d want 1", calls)
	}
	if lastErr != nil {
		t.Errorf("lastErr=%v want nil", lastErr)
	}
	if lastRes.Stdout != "logged" {
		t.Errorf("lastRes.Stdout=%q want logged", lastRes.Stdout)
	}
	if lastMap.Name != "log" {
		t.Errorf("lastMap.Name=%q want log", lastMap.Name)
	}
}

func TestWebhook_MetaForced(t *testing.T) {
	runner := &webhookFakeRunner{}
	b := webhookBridge(t, runner, map[string][]cmdsurface.Surface{
		"widget add": {cmdsurface.SurfaceWebhook},
	}, nil)
	url, stop := webhookNewServer(t, b, []cmdsurface.WebhookMapping{{
		Name: "meta",
		Path: []string{"widget", "add"},
		Auth: cmdsurface.AuthNone{},
	}})
	defer stop()

	status, _ := webhookPost(t, url+"/hooks/meta", []byte(`{}`), "application/json",
		map[string]string{"X-Request-ID": "trace-xyz"})
	if status != http.StatusAccepted {
		t.Fatalf("status=%d want 202", status)
	}
	got := runner.captured()
	if len(got) != 1 {
		t.Fatalf("captured=%d want 1", len(got))
	}
	if got[0].Meta.Surface != cmdsurface.SurfaceWebhook {
		t.Errorf("Meta.Surface=%q want webhook", got[0].Meta.Surface)
	}
	if got[0].Meta.Caller != "meta" {
		t.Errorf("Meta.Caller=%q want meta", got[0].Meta.Caller)
	}
	if got[0].Meta.TraceID != "trace-xyz" {
		t.Errorf("Meta.TraceID=%q want trace-xyz", got[0].Meta.TraceID)
	}
}
