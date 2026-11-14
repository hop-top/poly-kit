package cmdsurface_test

// A PermissionFunc can say that a refusal is an authentication
// failure (PermissionDecision.Unauthenticated). Every surface then
// answers it as it answers a failed authentication, and an ordinary
// refusal keeps the forbidden class.

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"

	"connectrpc.com/connect"

	"hop.top/kit/go/transport/api"
	"hop.top/kit/go/transport/cmdsurface"
	"hop.top/kit/go/transport/cmdsurface/gen/cmdsurfacev1"
	"hop.top/kit/go/transport/cmdsurface/gen/cmdsurfacev1/cmdsurfacev1connect"
)

// puVerify verifies four bearer tokens; the host's permission function
// then decides about the verified subject.
func puVerify(r *http.Request) (any, error) {
	switch strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ") {
	case "good", "ghost", "dormant", "mallory":
		return api.Claims{Subject: strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")}, nil
	}
	return nil, errors.New("bad credential")
}

// puPerm is the host's permission function: ghost maps to no
// principal, dormant to an inactive one (both authentication
// failures), mallory is known but not entitled (an ordinary refusal).
func puPerm(_ context.Context, meta cmdsurface.Meta, _ *cmdsurface.Leaf) cmdsurface.PermissionDecision {
	switch meta.Caller {
	case "ghost":
		return cmdsurface.PermissionDecision{Reason: "unknown_principal", Unauthenticated: true}
	case "dormant":
		return cmdsurface.PermissionDecision{Reason: "principal_inactive", Unauthenticated: true}
	case "mallory":
		return cmdsurface.PermissionDecision{Reason: "no_grant"}
	}
	return cmdsurface.PermissionDecision{Allowed: true}
}

// puSink records every audit emission.
type puSink struct {
	mu   sync.Mutex
	errs []error
	invs []cmdsurface.Invocation
}

func (s *puSink) Emit(_ context.Context, inv cmdsurface.Invocation, _ cmdsurface.Result, err error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.errs = append(s.errs, err)
	s.invs = append(s.invs, inv)
	return nil
}

func (s *puSink) last(t *testing.T) (cmdsurface.Invocation, error) {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.errs) == 0 {
		t.Fatal("no audit record")
	}
	return s.invs[len(s.invs)-1], s.errs[len(s.errs)-1]
}

// puBridge is azTree behind puPerm with a recording runner and sink.
func puBridge(s cmdsurface.Surface) (*cmdsurface.Bridge, *azRunner, *puSink) {
	run, sink := &azRunner{}, &puSink{}
	b := cmdsurface.New(azTree(),
		cmdsurface.WithRunner(run),
		cmdsurface.WithPermission(puPerm),
		cmdsurface.WithSinks(cmdsurface.SinkSpec{Sink: sink, OnOK: true, OnError: true}))
	b.Expose("*", s)
	return b, run, sink
}

// puAssertAudit checks the record of a refusal: its class and the
// host's reason, never the credential.
func puAssertAudit(t *testing.T, sink *puSink, wantAuth bool, reason, credential string) {
	t.Helper()
	_, err := sink.last(t)
	if errors.Is(err, cmdsurface.ErrAuthRefused) != wantAuth {
		t.Errorf("audit err = %v, ErrAuthRefused = %t want %t", err, !wantAuth, wantAuth)
	}
	if errors.Is(err, cmdsurface.ErrPermissionDenied) == wantAuth {
		t.Errorf("audit err = %v, ErrPermissionDenied = %t want %t", err, wantAuth, !wantAuth)
	}
	if !strings.Contains(err.Error(), reason) {
		t.Errorf("audit err = %q, want the reason %q", err, reason)
	}
	if strings.Contains(err.Error(), credential) {
		t.Errorf("audit err = %q carries the credential", err)
	}
}

func TestPermissionUnauthenticated_RESTProjection(t *testing.T) {
	b, run, sink := puBridge(cmdsurface.SurfaceREST)
	r := api.NewRouter()
	if err := cmdsurface.MountProjection(b, r, cmdsurface.WithProjectionAuth(puVerify)); err != nil {
		t.Fatal(err)
	}
	u := azServe(t, r)
	for _, tc := range []struct {
		token, reason string
	}{{"ghost", "unknown_principal"}, {"dormant", "principal_inactive"}} {
		resp := puDoHeader(t, http.MethodGet, u+"/v1/commands/vault/open", "Bearer "+tc.token)
		if resp.status != http.StatusUnauthorized || !strings.Contains(resp.body, `"unauthenticated"`) ||
			!strings.Contains(resp.body, tc.reason) {
			t.Errorf("%s: status=%d body=%s want 401 unauthenticated with %q", tc.token, resp.status, resp.body, tc.reason)
		}
		if resp.challenge == "" {
			t.Errorf("%s: no WWW-Authenticate header on the 401", tc.token)
		}
		puAssertAudit(t, sink, true, tc.reason, tc.token)
	}
	resp := puDoHeader(t, http.MethodGet, u+"/v1/commands/vault/open", "Bearer mallory")
	if resp.status != http.StatusForbidden || !strings.Contains(resp.body, "permission_denied") || resp.challenge != "" {
		t.Errorf("mallory: status=%d challenge=%q body=%s want 403 permission_denied, no challenge",
			resp.status, resp.challenge, resp.body)
	}
	puAssertAudit(t, sink, false, "no_grant", "mallory")
	if n := len(run.calls()); n != 0 {
		t.Fatalf("runner reached %d times while refusing", n)
	}
	if resp := puDoHeader(t, http.MethodGet, u+"/v1/commands/vault/open", "Bearer good"); resp.status != http.StatusOK {
		t.Fatalf("good: status=%d body=%s", resp.status, resp.body)
	}
}

func TestPermissionUnauthenticated_RESTLegacy(t *testing.T) {
	b, run, sink := puBridge(cmdsurface.SurfaceREST)
	r := api.NewRouter()
	if err := cmdsurface.MountREST(b, r, cmdsurface.WithRESTAuth(puVerify)); err != nil { //nolint:staticcheck // the deprecated mount follows the rule too
		t.Fatal(err)
	}
	u := azServe(t, r)
	for _, tc := range []struct{ token, reason string }{{"ghost", "unknown_principal"}, {"dormant", "principal_inactive"}} {
		resp := puDoHeader(t, http.MethodPost, u+"/cmd/vault/open", "Bearer "+tc.token)
		if resp.status != http.StatusUnauthorized || !strings.Contains(resp.body, tc.reason) || resp.challenge == "" {
			t.Errorf("%s: status=%d challenge=%q body=%s want 401 with a challenge and %q",
				tc.token, resp.status, resp.challenge, resp.body, tc.reason)
		}
		puAssertAudit(t, sink, true, tc.reason, tc.token)
	}
	resp := puDoHeader(t, http.MethodPost, u+"/cmd/vault/open", "Bearer mallory")
	if resp.status != http.StatusForbidden || resp.challenge != "" {
		t.Errorf("mallory: status=%d challenge=%q want 403, no challenge", resp.status, resp.challenge)
	}
	puAssertAudit(t, sink, false, "no_grant", "mallory")
	if n := len(run.calls()); n != 0 {
		t.Fatalf("runner reached %d times while refusing", n)
	}
}

func TestPermissionUnauthenticated_MCPLegacy(t *testing.T) {
	b, run, sink := puBridge(cmdsurface.SurfaceMCP)
	r := api.NewRouter(api.WithMiddleware(api.Auth(puVerify)))
	if err := cmdsurface.MountMCP(b, r); err != nil { //nolint:staticcheck // as above
		t.Fatal(err)
	}
	u := azServe(t, r)
	call := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"vault.open"}}`
	do := func(token string) puResponse {
		return puDoBody(t, http.MethodPost, u+"/mcp", call, "Bearer "+token)
	}
	for _, tc := range []struct{ token, reason string }{{"ghost", "unknown_principal"}, {"dormant", "principal_inactive"}} {
		resp := do(tc.token)
		if resp.status != http.StatusUnauthorized || resp.challenge == "" ||
			!strings.Contains(resp.body, `"isError":true`) || !strings.Contains(resp.body, tc.reason) {
			t.Errorf("%s: status=%d challenge=%q body=%s want 401, a challenge, isError and %q",
				tc.token, resp.status, resp.challenge, resp.body, tc.reason)
		}
		puAssertAudit(t, sink, true, tc.reason, tc.token)
	}
	resp := do("mallory")
	if resp.status != http.StatusOK || resp.challenge != "" || !strings.Contains(resp.body, `"isError":true`) ||
		!strings.Contains(resp.body, "no_grant") {
		t.Errorf("mallory: status=%d challenge=%q body=%s want the ordinary isError refusal", resp.status, resp.challenge, resp.body)
	}
	puAssertAudit(t, sink, false, "no_grant", "mallory")
	if n := len(run.calls()); n != 0 {
		t.Fatalf("runner reached %d times while refusing", n)
	}
}

func TestPermissionUnauthenticated_RPC(t *testing.T) {
	for _, p := range wireProtocols {
		t.Run(p.name, func(t *testing.T) {
			b, run, sink := puBridge(cmdsurface.SurfaceRPC)
			mux := newMountServer()
			if err := cmdsurface.MountRPC(b, mux, cmdsurface.WithRPCCallMeta(
				func(_ context.Context, req connect.AnyRequest, _ cmdsurface.Meta) cmdsurface.Meta {
					return cmdsurface.Meta{Caller: req.Header().Get("X-Caller"), Established: cmdsurface.EstablishedVerified}
				})); err != nil {
				t.Fatal(err)
			}
			client := cmdsurfacev1connect.NewCommandsClient(h2cClient(), newH2CServer(t, mux).URL, p.opts...)
			call := func(caller string) error {
				req := connect.NewRequest(&cmdsurfacev1.Invocation{Path: []string{"vault", "open"}})
				req.Header().Set("X-Caller", caller)
				_, err := client.Invoke(context.Background(), req)
				return err
			}
			for _, tc := range []struct{ caller, reason string }{{"ghost", "unknown_principal"}, {"dormant", "principal_inactive"}} {
				err := call(tc.caller)
				if connect.CodeOf(err) != connect.CodeUnauthenticated || !strings.Contains(err.Error(), tc.reason) {
					t.Errorf("%s: err = %v want unauthenticated with %q", tc.caller, err, tc.reason)
				}
				puAssertAudit(t, sink, true, tc.reason, tc.caller)
			}
			err := call("mallory")
			if connect.CodeOf(err) != connect.CodePermissionDenied {
				t.Errorf("mallory: err = %v want permission_denied", err)
			}
			puAssertAudit(t, sink, false, "no_grant", "mallory")
			if n := len(run.calls()); n != 0 {
				t.Fatalf("runner reached %d times while refusing", n)
			}
		})
	}
}

// puResponse is one HTTP answer: status, the WWW-Authenticate challenge
// and the body.
type puResponse struct {
	status    int
	challenge string
	body      string
}

func puDoHeader(t *testing.T, method, u, auth string) puResponse {
	t.Helper()
	return puDoBody(t, method, u, "{}", auth)
}

func puDoBody(t *testing.T, method, u, body, auth string) puResponse {
	t.Helper()
	req, err := http.NewRequest(method, u, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", auth)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, u, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	return puResponse{status: resp.StatusCode, challenge: resp.Header.Get("WWW-Authenticate"), body: string(raw)}
}
