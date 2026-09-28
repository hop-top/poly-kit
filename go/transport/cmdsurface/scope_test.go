package cmdsurface

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"hop.top/kit/go/transport/api"
)

// scopeTree is root with "items export" (read, kit/permissions
// items:read,items:admin), "items peek" (read, kit/permissions
// items:read), "items list" (read, no permissions), "items vault"
// (read, auth-required and items:admin) and "items wipe" (destructive,
// items:admin).
func scopeTree() *cobra.Command {
	root := &cobra.Command{Use: "root"}
	items := &cobra.Command{Use: "items"}
	leaf := func(use string, ann map[string]string) *cobra.Command {
		return &cobra.Command{
			Use:         use,
			RunE:        func(cmd *cobra.Command, _ []string) error { cmd.Print("ran"); return nil },
			Annotations: ann,
		}
	}
	items.AddCommand(
		leaf("export", map[string]string{"kit/side-effect": "read", "kit/permissions": "items:read, items:admin"}),
		leaf("peek", map[string]string{"kit/side-effect": "read", "kit/permissions": "items:read"}),
		leaf("list", map[string]string{"kit/side-effect": "read"}),
		leaf("vault", map[string]string{"kit/side-effect": "read", "kit/auth-required": "true", "kit/permissions": "items:admin"}),
		leaf("wipe", map[string]string{"kit/side-effect": "destructive", "kit/permissions": "items:admin"}),
	)
	root.AddCommand(items)
	return root
}

// verified returns a Meta a verifier established on surface, holding
// scopes.
func verified(surface Surface, scopes string) Meta {
	m := Meta{Surface: surface, Caller: "alice", Established: EstablishedVerified}
	if scopes != "" {
		m.Extra = map[string]string{"scopes": scopes}
	}
	return m
}

// TestScopeCheck_Verdicts pins who the built-in scope check admits to
// a leaf declaring kit/permissions, and that a leaf declaring none
// keeps the behavior it had before the check existed.
func TestScopeCheck_Verdicts(t *testing.T) {
	cases := []struct {
		name    string
		path    string
		meta    Meta
		missing []string // nil: admitted
	}{
		{"verified with every scope", "items export", verified(SurfaceREST, "items:admin,items:read,other"), nil},
		{"verified lacking one", "items export", verified(SurfaceREST, "items:read"), []string{"items:admin"}},
		{"verified with none", "items export", verified(SurfaceREST, ""), []string{"items:read", "items:admin"}},
		{"scope matching is exact", "items export", verified(SurfaceREST, "items:READ,items:admin:all"), []string{"items:read", "items:admin"}},
		{"unestablished holds none", "items peek", Meta{Surface: SurfaceREST}, []string{"items:read"}},
		{
			"a claimed caller's scopes prove nothing", "items peek",
			Meta{Surface: SurfaceRPC, Caller: "alice", Extra: map[string]string{"scopes": "items:read"}},
			[]string{"items:read"},
		},
		{"transport-established holds the owner's authority", "items export", Meta{Surface: SurfaceSocket, Established: EstablishedTransport}, nil},
		{"the CLI is the operator's own", "items export", Meta{Surface: SurfaceCLI}, nil},
		{"the library is the operator's own", "items export", Meta{Surface: SurfaceLib}, nil},
		{"no permissions declared: anonymous runs", "items list", Meta{Surface: SurfaceREST}, nil},
		{"no permissions declared: claimed runs", "items list", Meta{Surface: SurfaceMCP, Caller: "mallory"}, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			b := New(scopeTree(), WithRunner(countingRunner(&calls, nil)))
			b.Expose("*", tc.meta.Surface)

			_, err := b.Invoke(context.Background(), Invocation{Path: strings.Fields(tc.path), Meta: tc.meta})
			if tc.missing == nil {
				if err != nil {
					t.Fatalf("want admitted, got %v", err)
				}
				if calls != 1 {
					t.Fatalf("runner calls = %d, want 1", calls)
				}
				return
			}
			var se *InsufficientScopeError
			if !errors.As(err, &se) {
				t.Fatalf("err = %v, want *InsufficientScopeError", err)
			}
			if !errors.Is(err, ErrInsufficientScope) || !errors.Is(err, ErrPermissionDenied) {
				t.Fatalf("err = %v must be ErrInsufficientScope and ErrPermissionDenied", err)
			}
			if !reflect.DeepEqual(se.Missing, tc.missing) {
				t.Fatalf("missing = %v, want %v", se.Missing, tc.missing)
			}
			leaf, _ := b.resolveLeaf(strings.Fields(tc.path))
			if req, ok := RequiredScopes(err); !ok || !reflect.DeepEqual(req, leaf.Class.Permissions) {
				t.Fatalf("RequiredScopes = %v, %v; want %v", req, ok, leaf.Class.Permissions)
			}
			for _, s := range tc.missing {
				if !strings.Contains(err.Error(), s) {
					t.Fatalf("message %q does not name missing scope %s", err.Error(), s)
				}
			}
			if calls != 0 {
				t.Fatalf("runner must not run a refused call; calls = %d", calls)
			}
		})
	}
}

// TestScopeCheck_AdopterPermissionOnlyNarrows pins that the adopter's
// PermissionFunc is asked after the scope check: never about a call
// the check refused, and able to refuse one it admitted.
func TestScopeCheck_AdopterPermissionOnlyNarrows(t *testing.T) {
	var asked []string
	b := New(scopeTree(),
		WithRunner(countingRunner(new(int), nil)),
		WithPermission(func(_ context.Context, meta Meta, leaf *Leaf) PermissionDecision {
			asked = append(asked, leaf.PathKey())
			if meta.Caller == "alice" {
				return PermissionDecision{Reason: "alice is suspended"}
			}
			// An adopter that permits everything cannot widen.
			return PermissionDecision{Allowed: true}
		}),
	)
	b.Expose("*", SurfaceREST)

	// Refused by the scope check: the adopter is not asked, even though
	// it would allow the caller.
	bob := verified(SurfaceREST, "items:read")
	bob.Caller = "bob"
	_, err := b.Invoke(context.Background(), Invocation{Path: []string{"items", "export"}, Meta: bob})
	if !errors.Is(err, ErrInsufficientScope) {
		t.Fatalf("err = %v, want ErrInsufficientScope", err)
	}
	if len(asked) != 0 {
		t.Fatalf("adopter asked about %v; the scope check refused first", asked)
	}

	// Admitted by the scope check, refused by the adopter.
	_, err = b.Invoke(context.Background(), Invocation{
		Path: []string{"items", "export"},
		Meta: verified(SurfaceREST, "items:read,items:admin"),
	})
	if !errors.Is(err, ErrPermissionDenied) || errors.Is(err, ErrInsufficientScope) {
		t.Fatalf("err = %v, want the adopter's ErrPermissionDenied", err)
	}
	if !strings.Contains(err.Error(), "alice is suspended") {
		t.Fatalf("adopter reason missing from %q", err.Error())
	}
}

// TestScopeCheck_GateOrder pins that authentication (slot 4) and the
// destructive ceiling (slot 5) answer before the scope check.
func TestScopeCheck_GateOrder(t *testing.T) {
	b := New(scopeTree(), WithRunner(countingRunner(new(int), nil)))
	b.Expose("*", SurfaceREST)

	_, err := b.Invoke(context.Background(), Invocation{
		Path: []string{"items", "vault"},
		Meta: Meta{Surface: SurfaceREST, Caller: "alice"},
	})
	if !errors.Is(err, ErrAuthRefused) {
		t.Fatalf("auth-required leaf, unauthenticated: err = %v, want ErrAuthRefused", err)
	}

	_, err = b.Invoke(context.Background(), Invocation{
		Path: []string{"items", "wipe"},
		Meta: verified(SurfaceREST, ""),
	})
	if !errors.Is(err, ErrDestructiveBlocked) {
		t.Fatalf("destructive leaf: err = %v, want ErrDestructiveBlocked", err)
	}
}

// TestScopeCheck_RefusalIsAudited pins that a scope refusal reaches the
// sinks with its class, as every invocation-plane refusal does.
func TestScopeCheck_RefusalIsAudited(t *testing.T) {
	sink := &admitSink{}
	b := New(scopeTree(),
		WithRunner(countingRunner(new(int), nil)),
		WithSinks(SinkSpec{Sink: sink, OnError: true}),
	)
	b.Expose("*", SurfaceREST)

	_, _ = b.Invoke(context.Background(), Invocation{Path: []string{"items", "peek"}, Meta: Meta{Surface: SurfaceREST}})
	sink.mu.Lock()
	got := append([]error(nil), sink.errs...)
	sink.mu.Unlock()
	if len(got) != 1 || !errors.Is(got[0], ErrInsufficientScope) {
		t.Fatalf("audited errors = %v, want one ErrInsufficientScope", got)
	}
}

// TestScopeCheck_HTTPAnswers pins the HTTP answer on both REST mounts:
// 403 insufficient_scope with the RFC 6750 challenge naming every
// scope the command requires, for a verified caller lacking one and
// for an anonymous caller alike; and a verified caller holding them
// runs.
func TestScopeCheck_HTTPAnswers(t *testing.T) {
	const challenge = `Bearer error="insufficient_scope", scope="items:read items:admin"`

	mounts := map[string]struct {
		mount func(b *Bridge, r *api.Router) error
		url   func(path string) string
	}{
		"rest": {
			mount: func(b *Bridge, r *api.Router) error { return MountREST(b, r) },
			url:   func(path string) string { return "/cmd/" + path },
		},
		"projection": {
			mount: func(b *Bridge, r *api.Router) error { return MountProjection(b, r, WithProjectionRouterAuth()) },
			url:   func(path string) string { return "/v1/commands/" + path },
		},
	}
	for name, m := range mounts {
		t.Run(name, func(t *testing.T) {
			b := New(scopeTree(), WithRunner(countingRunner(new(int), nil)))
			b.Expose("*", SurfaceREST)
			r := api.NewRouter(api.WithMiddleware(verifyGoodOnly))
			if err := m.mount(b, r); err != nil {
				t.Fatalf("mount: %v", err)
			}
			srv := httptest.NewServer(r)
			t.Cleanup(srv.Close)

			call := func(path, auth string) (*http.Response, api.APIError) {
				t.Helper()
				method := http.MethodPost
				if name == "projection" {
					method = http.MethodGet // reads are GETs on the projection
				}
				req, _ := http.NewRequest(method, srv.URL+m.url(path), nil)
				if auth != "" {
					req.Header.Set("Authorization", auth)
				}
				resp, err := srv.Client().Do(req)
				if err != nil {
					t.Fatalf("%s: %v", path, err)
				}
				defer resp.Body.Close()
				raw, _ := io.ReadAll(resp.Body)
				var ae api.APIError
				_ = json.Unmarshal(raw, &ae)
				return resp, ae
			}

			// alice (verifyGood) holds items:read only.
			for _, auth := range []string{goodBearer, ""} {
				resp, ae := call("items/export", auth)
				if resp.StatusCode != http.StatusForbidden || ae.Code != api.CodeInsufficientScope {
					t.Fatalf("auth %q: status=%d code=%q, want 403 insufficient_scope", auth, resp.StatusCode, ae.Code)
				}
				if got := resp.Header.Get("WWW-Authenticate"); got != challenge {
					t.Fatalf("auth %q: WWW-Authenticate = %q, want %q", auth, got, challenge)
				}
			}
			if resp, ae := call("items/peek", goodBearer); resp.StatusCode != http.StatusOK {
				t.Fatalf("items/peek as alice: status=%d code=%q, want 200", resp.StatusCode, ae.Code)
			}
		})
	}
}

// TestScopeCheck_MCPRefusal pins the MCP answer: an isError result
// whose text starts with the code, carrying it in _meta.
func TestScopeCheck_MCPRefusal(t *testing.T) {
	err := scopeCheck(Meta{Surface: SurfaceMCP}, &Leaf{
		Path:  []string{"items", "peek"},
		Class: SafetyClass{Permissions: []string{"items:read"}},
	})
	text, refusal, ok := MCPRefusal(err)
	if !ok {
		t.Fatalf("MCPRefusal(%v) not a refusal", err)
	}
	if !strings.HasPrefix(text, "insufficient_scope: ") {
		t.Fatalf("text = %q, want the code first", text)
	}
	if refusal["code"] != "insufficient_scope" {
		t.Fatalf("refusal = %v, want code insufficient_scope", refusal)
	}
}

// TestScopeCheck_WireCodes pins the code each non-HTTP mapping gives
// the class.
func TestScopeCheck_WireCodes(t *testing.T) {
	err := error(&InsufficientScopeError{Path: "items peek", Surface: SurfaceWS, Required: []string{"items:read"}, Missing: []string{"items:read"}})
	if got := errorCode(err); got != api.CodeInsufficientScope {
		t.Errorf("ws errorCode = %q", got)
	}
	if got := bridgeErrorCode(err); got != api.CodeInsufficientScope {
		t.Errorf("bus bridgeErrorCode = %q", got)
	}
	if status, code := lambdaHTTPErrorCode(err); status != http.StatusForbidden || code != api.CodeInsufficientScope {
		t.Errorf("lambda = %d %q", status, code)
	}
	if got := translateProjectionError(err); !errors.Is(got, api.ErrInsufficientScope) {
		t.Errorf("projection translation = %v", got)
	}
}
