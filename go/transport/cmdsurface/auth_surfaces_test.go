package cmdsurface_test

// Per-surface checks of the kit/auth-required rule: with no verifier
// the leaf is refused, a verifier that rejects refuses it, one that
// accepts runs it, and the identity the command runs as is the
// verifier's, never what the request claimed.

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
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aws/aws-lambda-go/events"
	"github.com/coder/websocket"
	"github.com/spf13/cobra"

	"hop.top/kit/go/transport/api"
	"hop.top/kit/go/transport/cmdsurface"
)

// azTree is root with "vault open" (read, auth-required) and
// "vault peek" (read).
func azTree() *cobra.Command {
	root := &cobra.Command{Use: "root"}
	vault := &cobra.Command{Use: "vault"}
	vault.AddCommand(
		&cobra.Command{
			Use:         "open",
			RunE:        func(*cobra.Command, []string) error { return nil },
			Annotations: map[string]string{"kit/side-effect": "read", "kit/auth-required": "true"},
		},
		&cobra.Command{
			Use:         "peek",
			RunE:        func(*cobra.Command, []string) error { return nil },
			Annotations: map[string]string{"kit/side-effect": "read"},
		},
	)
	root.AddCommand(vault)
	return root
}

// azRunner records every invocation that reached it.
type azRunner struct {
	mu  sync.Mutex
	got []cmdsurface.Invocation
}

func (r *azRunner) record(inv cmdsurface.Invocation) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.got = append(r.got, inv)
}

func (r *azRunner) Run(_ context.Context, inv cmdsurface.Invocation) (cmdsurface.Result, error) {
	r.record(inv)
	return cmdsurface.Result{Stdout: "ran"}, nil
}

func (r *azRunner) Stream(_ context.Context, inv cmdsurface.Invocation, out chan<- cmdsurface.Event) error {
	r.record(inv)
	out <- cmdsurface.Event{Kind: "done", Data: &cmdsurface.Result{Stdout: "ran"}, At: time.Now()}
	close(out)
	return nil
}

func (r *azRunner) calls() []cmdsurface.Invocation {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]cmdsurface.Invocation(nil), r.got...)
}

// azBridge exposes azTree on surfaces, recording runs.
func azBridge(surfaces ...cmdsurface.Surface) (*cmdsurface.Bridge, *azRunner) {
	run := &azRunner{}
	b := cmdsurface.New(azTree(), cmdsurface.WithRunner(run))
	b.Expose("*", surfaces...)
	return b, run
}

// azVerify accepts "Bearer good" as alice@acme with one scope.
func azVerify(r *http.Request) (any, error) {
	if r.Header.Get("Authorization") != "Bearer good" {
		return nil, errors.New("bad credential")
	}
	return api.Claims{Subject: "alice", Tenant: "acme", Scopes: []string{"vault:open"}}, nil
}

// azServe serves r and returns its URL.
func azServe(t *testing.T, r http.Handler) string {
	t.Helper()
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)
	return srv.URL
}

// azDo sends one request and returns its status and body.
func azDo(t *testing.T, method, u string, body []byte, hdr map[string]string) (int, string) {
	t.Helper()
	req, err := http.NewRequest(method, u, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, u, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(raw)
}

// assertVerifiedAlice checks the one run the runner saw was alice's,
// verified, with the credential's scopes.
func assertVerifiedAlice(t *testing.T, run *azRunner) {
	t.Helper()
	got := run.calls()
	if len(got) != 1 {
		t.Fatalf("runs=%d want 1", len(got))
	}
	m := got[0].Meta
	if m.Caller != "alice" || m.Tenant != "acme" || m.Established != cmdsurface.EstablishedVerified {
		t.Errorf("meta=%+v want alice@acme, verified", m)
	}
	if m.Extra["scopes"] != "vault:open" {
		t.Errorf("scopes=%q want the credential's", m.Extra["scopes"])
	}
}

// claimingBody is an invocation body naming root with an admin scope.
var claimingBody = []byte(`{"meta":{"caller":"root","tenant":"evil","extra":{"scopes":"admin"}}}`)

func TestAuthSurfaces_REST(t *testing.T) {
	b, _ := azBridge(cmdsurface.SurfaceREST)
	bare := api.NewRouter()
	if err := cmdsurface.MountREST(b, bare); err != nil { //nolint:staticcheck // the deprecated mount follows the rule too
		t.Fatal(err)
	}
	u := azServe(t, bare)
	if s, _ := azDo(t, http.MethodPost, u+"/cmd/vault/open", claimingBody,
		map[string]string{"Authorization": "Bearer good"}); s != http.StatusUnauthorized {
		t.Errorf("no verifier: status=%d want 401", s)
	}

	b, run := azBridge(cmdsurface.SurfaceREST)
	r := api.NewRouter()
	if err := cmdsurface.MountREST(b, r, cmdsurface.WithRESTAuth(azVerify)); err != nil { //nolint:staticcheck // as above
		t.Fatal(err)
	}
	u = azServe(t, r)
	if s, _ := azDo(t, http.MethodPost, u+"/cmd/vault/open", claimingBody,
		map[string]string{"Authorization": "Bearer bad"}); s != http.StatusUnauthorized {
		t.Errorf("rejected: status=%d want 401", s)
	}
	if n := len(run.calls()); n != 0 {
		t.Fatalf("runner reached %d times while refusing", n)
	}
	if s, body := azDo(t, http.MethodPost, u+"/cmd/vault/open", claimingBody,
		map[string]string{"Authorization": "Bearer good"}); s != http.StatusOK {
		t.Fatalf("verified: status=%d body=%s", s, body)
	}
	assertVerifiedAlice(t, run)

	// An unauthenticated call to a plain leaf keeps its claim as
	// provenance, unestablished and without the claimed scope.
	if s, body := azDo(t, http.MethodPost, u+"/cmd/vault/peek", claimingBody, nil); s != http.StatusOK {
		t.Fatalf("plain leaf: status=%d body=%s", s, body)
	}
	got := run.calls()
	m := got[len(got)-1].Meta
	if m.Caller != "root" || m.Authenticated() {
		t.Errorf("plain leaf meta=%+v want the claim, unestablished", m)
	}
	if _, ok := m.Extra["scopes"]; ok {
		t.Errorf("claimed scopes reached the bridge: %v", m.Extra)
	}
}

func TestAuthSurfaces_SSE(t *testing.T) {
	stream := "/cmd/vault/open/stream"
	b, _ := azBridge(cmdsurface.SurfaceSSE)
	bare := api.NewRouter()
	if err := cmdsurface.MountSSE(b, bare); err != nil {
		t.Fatal(err)
	}
	if s, _ := azDo(t, http.MethodGet, azServe(t, bare)+stream, nil,
		map[string]string{"Authorization": "Bearer good"}); s != http.StatusUnauthorized {
		t.Errorf("no verifier: status=%d want 401", s)
	}

	b, run := azBridge(cmdsurface.SurfaceSSE)
	r := api.NewRouter()
	if err := cmdsurface.MountSSE(b, r, cmdsurface.WithSSEAuth(azVerify)); err != nil {
		t.Fatal(err)
	}
	u := azServe(t, r)
	if s, _ := azDo(t, http.MethodGet, u+stream, nil,
		map[string]string{"Authorization": "Bearer bad"}); s != http.StatusUnauthorized {
		t.Errorf("rejected: status=%d want 401", s)
	}
	if s, body := azDo(t, http.MethodGet, u+stream, nil,
		map[string]string{"Authorization": "Bearer good"}); s != http.StatusOK {
		t.Fatalf("verified: status=%d body=%s", s, body)
	}
	assertVerifiedAlice(t, run)
}

func TestAuthSurfaces_WS(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	dial := func(t *testing.T, u, auth string) (*websocket.Conn, int) {
		t.Helper()
		ws := "ws" + strings.TrimPrefix(u, "http") + "/ws/cmd"
		c, resp, err := websocket.Dial(ctx, ws, &websocket.DialOptions{
			HTTPHeader: http.Header{"Authorization": []string{auth}},
		})
		if err != nil {
			if resp == nil {
				t.Fatalf("dial: %v", err)
			}
			return nil, resp.StatusCode
		}
		return c, http.StatusSwitchingProtocols
	}

	b, _ := azBridge(cmdsurface.SurfaceWS)
	bare := api.NewRouter()
	if err := cmdsurface.MountWS(b, bare, cmdsurface.WithWSContext(ctx)); err != nil {
		t.Fatal(err)
	}
	if _, s := dial(t, azServe(t, bare), "Bearer good"); s != http.StatusUnauthorized {
		t.Errorf("no verifier: status=%d want 401", s)
	}

	b, run := azBridge(cmdsurface.SurfaceWS)
	r := api.NewRouter()
	if err := cmdsurface.MountWS(b, r, cmdsurface.WithWSContext(ctx), cmdsurface.WithWSAuth(azVerify)); err != nil {
		t.Fatal(err)
	}
	u := azServe(t, r)
	if _, s := dial(t, u, "Bearer bad"); s != http.StatusUnauthorized {
		t.Errorf("rejected: status=%d want 401", s)
	}
	c, s := dial(t, u, "Bearer good")
	if s != http.StatusSwitchingProtocols {
		t.Fatalf("verified: status=%d", s)
	}
	defer func() { _ = c.Close(websocket.StatusNormalClosure, "") }()
	frame := `{"op":"invoke","id":"1","invocation":{"path":["vault","open"],"meta":{"caller":"root","extra":{"scopes":"admin"}}}}`
	if err := c.Write(ctx, websocket.MessageText, []byte(frame)); err != nil {
		t.Fatal(err)
	}
	for {
		_, data, err := c.Read(ctx)
		if err != nil {
			t.Fatalf("read: %v", err)
		}
		var f struct{ Op string }
		_ = json.Unmarshal(data, &f)
		if f.Op == "result" {
			break
		}
		if f.Op == "error" {
			t.Fatalf("verified invoke refused: %s", data)
		}
	}
	assertVerifiedAlice(t, run)
}

func TestAuthSurfaces_MCPLegacy(t *testing.T) {
	call := []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"vault.open"}}`)

	b, _ := azBridge(cmdsurface.SurfaceMCP)
	bare := api.NewRouter()
	if err := cmdsurface.MountMCP(b, bare); err != nil { //nolint:staticcheck // the deprecated mount follows the rule too
		t.Fatal(err)
	}
	if s, _ := azDo(t, http.MethodPost, azServe(t, bare)+"/mcp", call,
		map[string]string{"Authorization": "Bearer good", "Content-Type": "application/json"}); s != http.StatusUnauthorized {
		t.Errorf("no verifier: status=%d want 401", s)
	}

	b, run := azBridge(cmdsurface.SurfaceMCP)
	r := api.NewRouter(api.WithMiddleware(api.Auth(azVerify)))
	if err := cmdsurface.MountMCP(b, r); err != nil { //nolint:staticcheck // as above
		t.Fatal(err)
	}
	u := azServe(t, r)
	if s, _ := azDo(t, http.MethodPost, u+"/mcp", call,
		map[string]string{"Authorization": "Bearer bad", "Content-Type": "application/json"}); s != http.StatusUnauthorized {
		t.Errorf("rejected: status=%d want 401", s)
	}
	if s, body := azDo(t, http.MethodPost, u+"/mcp", call,
		map[string]string{"Authorization": "Bearer good", "Content-Type": "application/json"}); s != http.StatusOK || strings.Contains(body, `"isError":true`) {
		t.Fatalf("verified: status=%d body=%s", s, body)
	}
	assertVerifiedAlice(t, run)
}

func TestAuthSurfaces_Bus(t *testing.T) {
	binding := []cmdsurface.BusBinding{{Path: []string{"vault", "open"}, RequestTopic: "req", ResponseTopic: "resp"}}
	msg := cmdsurface.BusMessage{
		Topic:   "req",
		Payload: []byte(`{"meta":{"caller":"root","extra":{"scopes":"admin"}}}`),
		Headers: map[string]string{cmdsurface.BusHeaderAuthorization: "Bearer good"},
	}
	errCode := func(t *testing.T, bus *busFakeBus) string {
		t.Helper()
		pubs := bus.publicationsOn("resp")
		if len(pubs) == 0 {
			t.Fatal("no response published")
		}
		raw, _ := json.Marshal(pubs[len(pubs)-1].Payload)
		var env struct {
			Error *struct{ Code string } `json:"error"`
		}
		_ = json.Unmarshal(raw, &env)
		if env.Error == nil {
			return ""
		}
		return env.Error.Code
	}
	verify := func(_ context.Context, m cmdsurface.BusMessage) (any, error) {
		return azVerify(&http.Request{Header: http.Header{"Authorization": []string{m.Headers[cmdsurface.BusHeaderAuthorization]}}})
	}

	b, run := azBridge(cmdsurface.SurfaceBus)
	bus := newBusFakeBus()
	cleanup, err := cmdsurface.MountBus(b, bus, bus, binding)
	if err != nil {
		t.Fatal(err)
	}
	bus.deliver(msg)
	cleanup()
	if code := errCode(t, bus); code != "unauthenticated" {
		t.Errorf("no verifier: code=%q want unauthenticated", code)
	}

	bus = newBusFakeBus()
	cleanup, err = cmdsurface.MountBus(b, bus, bus, binding, cmdsurface.WithBusAuth(verify))
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	bad := msg
	bad.Headers = map[string]string{cmdsurface.BusHeaderAuthorization: "Bearer bad"}
	bus.deliver(bad)
	if code := errCode(t, bus); code != "unauthenticated" {
		t.Errorf("rejected: code=%q want unauthenticated", code)
	}
	if n := len(run.calls()); n != 0 {
		t.Fatalf("runner reached %d times while refusing", n)
	}
	bus.deliver(msg)
	if code := errCode(t, bus); code != "" {
		t.Fatalf("verified: code=%q", code)
	}
	assertVerifiedAlice(t, run)
}

func TestAuthSurfaces_Webhook(t *testing.T) {
	secret := []byte("s3cret")
	b, run := azBridge(cmdsurface.SurfaceWebhook)
	r := api.NewRouter()
	if err := cmdsurface.MountWebhooks(b, r, []cmdsurface.WebhookMapping{{
		Name: "open",
		Path: []string{"vault", "open"},
		Auth: cmdsurface.AuthHMAC{Header: "X-Sig", Secret: secret},
	}}); err != nil {
		t.Fatal(err)
	}
	u := azServe(t, r) + "/hooks/open"
	body := []byte(`{}`)
	if s, _ := azDo(t, http.MethodPost, u, body, map[string]string{"X-Sig": "00"}); s != http.StatusUnauthorized {
		t.Errorf("rejected: status=%d want 401", s)
	}
	mac := hmac.New(sha256.New, secret)
	mac.Write(body)
	if s, out := azDo(t, http.MethodPost, u, body,
		map[string]string{"X-Sig": hex.EncodeToString(mac.Sum(nil)), "Content-Type": "application/json"}); s != http.StatusAccepted {
		t.Fatalf("verified: status=%d body=%s", s, out)
	}
	got := run.calls()
	if len(got) != 1 || got[0].Meta.Established != cmdsurface.EstablishedVerified || got[0].Meta.Caller != "open" {
		t.Errorf("runs=%+v want one, verified, as the mapping", got)
	}
}

func TestAuthSurfaces_Signed(t *testing.T) {
	key := []byte("0123456789abcdef0123456789abcdef")
	b, run := azBridge(cmdsurface.SurfaceSigned)
	store := cmdsurface.NewInMemoryNonceStore()
	r := api.NewRouter()
	if err := cmdsurface.MountSigned(b, r, key, store); err != nil {
		t.Fatal(err)
	}
	base := azServe(t, r)
	issuer := &cmdsurface.SignedIssuer{Key: key, Store: store, URLPrefix: base + "/x"}
	link, err := issuer.IssueViaBridge(context.Background(), b, cmdsurface.SignedToken{
		Path: []string{"vault", "open"}, Caller: "alice",
	}, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if s, _ := azDo(t, http.MethodGet, link+"tampered", nil, nil); s != http.StatusUnauthorized {
		t.Errorf("tampered token: status=%d want 401", s)
	}
	if s, out := azDo(t, http.MethodGet, link, nil, nil); s != http.StatusOK {
		t.Fatalf("verified: status=%d body=%s", s, out)
	}
	got := run.calls()
	if len(got) != 1 || got[0].Meta.Caller != "alice" || got[0].Meta.Established != cmdsurface.EstablishedVerified {
		t.Errorf("runs=%+v want alice, verified", got)
	}
}

func TestAuthSurfaces_OAuthCallback(t *testing.T) {
	b, run := azBridge(cmdsurface.SurfaceOAuthCB)
	store := cmdsurface.NewInMemoryStateStore()
	r := api.NewRouter()
	if err := cmdsurface.MountOAuth(b, r, []cmdsurface.OAuthProvider{{
		Name: "p", Path: []string{"vault", "open"},
	}}, store); err != nil {
		t.Fatal(err)
	}
	u := azServe(t, r) + "/oauth/p/callback?state="
	if s, _ := azDo(t, http.MethodGet, u+"forged", nil, nil); s != http.StatusBadRequest {
		t.Errorf("forged state: status=%d want 400", s)
	}
	state, err := store.Issue(context.Background(), "p", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if s, out := azDo(t, http.MethodGet, u+url.QueryEscape(state), nil, nil); s != http.StatusOK {
		t.Fatalf("verified: status=%d body=%s", s, out)
	}
	got := run.calls()
	if len(got) != 1 || got[0].Meta.Established != cmdsurface.EstablishedVerified {
		t.Errorf("runs=%+v want one, verified", got)
	}
}

func TestAuthSurfaces_Lambda(t *testing.T) {
	cfg := func(ev cmdsurface.LambdaEventType) cmdsurface.LambdaConfig {
		return cmdsurface.LambdaConfig{Event: ev, Mapping: cmdsurface.LambdaMapping{Path: []string{"vault", "open"}}}
	}
	invoke := func(t *testing.T, c cmdsurface.LambdaConfig, event any) (json.RawMessage, *azRunner, error) {
		t.Helper()
		b, run := azBridge(cmdsurface.SurfaceFaaS)
		h, err := cmdsurface.LambdaHandler(b, c)
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := json.Marshal(event)
		out, err := h(context.Background(), raw)
		return out, run, err
	}

	// An API Gateway request no authorizer verified is not established.
	out, run, _ := invoke(t, cfg(cmdsurface.EventAPIGatewayV2), events.APIGatewayV2HTTPRequest{
		Headers: map[string]string{"authorization": "Bearer good"},
	})
	var v2 events.APIGatewayV2HTTPResponse
	_ = json.Unmarshal(out, &v2)
	if v2.StatusCode != http.StatusUnauthorized || !strings.Contains(v2.Body, "unauthenticated") ||
		v2.Headers["WWW-Authenticate"] == "" || len(run.calls()) != 0 {
		t.Errorf("v2 without authorizer: %+v", v2)
	}
	out, run, _ = invoke(t, cfg(cmdsurface.EventAPIGatewayV2), events.APIGatewayV2HTTPRequest{
		RequestContext: events.APIGatewayV2HTTPRequestContext{
			Authorizer: &events.APIGatewayV2HTTPRequestContextAuthorizerDescription{
				JWT: &events.APIGatewayV2HTTPRequestContextAuthorizerJWTDescription{Claims: map[string]string{"sub": "alice"}},
			},
		},
	})
	_ = json.Unmarshal(out, &v2)
	if v2.StatusCode != http.StatusOK || len(run.calls()) != 1 || run.calls()[0].Meta.Established != cmdsurface.EstablishedVerified {
		t.Errorf("v2 with authorizer: %+v", v2)
	}

	out, run, _ = invoke(t, cfg(cmdsurface.EventAPIGatewayV1), events.APIGatewayProxyRequest{})
	var v1 events.APIGatewayProxyResponse
	_ = json.Unmarshal(out, &v1)
	if v1.StatusCode != http.StatusUnauthorized || len(run.calls()) != 0 {
		t.Errorf("v1 without authorizer: %+v", v1)
	}
	out, run, _ = invoke(t, cfg(cmdsurface.EventAPIGatewayV1), events.APIGatewayProxyRequest{
		RequestContext: events.APIGatewayProxyRequestContext{Authorizer: map[string]any{"principalId": "alice"}},
	})
	_ = json.Unmarshal(out, &v1)
	if v1.StatusCode != http.StatusOK || len(run.calls()) != 1 {
		t.Errorf("v1 with authorizer: %+v", v1)
	}

	// A queue delivery was authorized by the platform's IAM.
	_, run, err := invoke(t, cfg(cmdsurface.EventSQS), events.SQSEvent{Records: []events.SQSMessage{{MessageId: "m1", Body: "{}"}}})
	if err != nil || len(run.calls()) != 1 || run.calls()[0].Meta.Established != cmdsurface.EstablishedTransport {
		t.Errorf("sqs: err=%v runs=%+v", err, run.calls())
	}
}
