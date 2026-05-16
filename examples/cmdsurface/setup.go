// setup.go wires the example's cobra tree, bridge, and HTTP/RPC
// servers and returns them to callers (main.go and the e2e suite).
// Keeping the wiring in one helper means both entry points see the
// same surface set and the same policy.
package main

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/spf13/cobra"

	"hop.top/kit/go/transport/api"
	"hop.top/kit/go/transport/cmdsurface"
	"hop.top/kit/go/transport/rpc"
)

// exampleApp bundles every wired component the example exposes so
// the e2e suite can drive each surface against the same instance.
//
// Bridge is the shared command-projection layer. Router is the
// HTTP mux REST + MCP + WS + SSE share. RPCSrv is the ConnectRPC
// server (its own listener). Bus is the in-process pub/sub used by
// the Bus surface binding. SinkBuf is the buffer the FileSink
// writes to so tests can assert that the sink pipeline fired.
// Cron is the running cron engine. Cleanup releases the bus / cron
// subscriptions and any other resources BuildExample acquired.
//
// Wave 3 fields:
//
//   - WebhookSecret is the HMAC secret the webhook surface uses to
//     verify inbound POSTs. Tests use it to sign requests.
//   - OAuthState is the in-memory state store the OAuth surface
//     reads on callback. Tests use it to drive Issue/Consume
//     directly without round-tripping the authorize endpoint.
//   - SignedIssuer is the signed-URL issuer wired against the
//     verifier's key + nonce store. Tests mint URLs via Issue or
//     IssueViaBridge.
//   - SignedNonce is the in-memory nonce store the signed-URL
//     verifier consults. Tests use it to drive Revoke directly.
type exampleApp struct {
	Bridge        *cmdsurface.Bridge
	Router        *api.Router
	RPCSrv        *rpc.Server
	HTTPSrv       *http.Server
	RPCHTTP       *http.Server
	Bus           *exampleBus
	SinkBuf       *bytes.Buffer
	Cron          cmdsurface.CronEngine
	WebhookSecret []byte
	OAuthState    cmdsurface.StateStore
	SignedIssuer  *cmdsurface.SignedIssuer
	SignedNonce   cmdsurface.NonceStore
	Cleanup       func()
}

// exampleConfig captures the test-toggleable options BuildExample
// understands. Production code (main.go) passes no options.
type exampleConfig struct {
	allowDestructiveOn []cmdsurface.Surface
	signedURLTTL       time.Duration // 0 = use the issuer's default per-call TTL
}

// ExampleOption configures BuildExample.
type ExampleOption func(*exampleConfig)

// WithAllowDestructiveOn adds surfaces on which destructive leaves
// may run, on top of the default CLI+Lib pair. Used by the
// signed-URL adversarial tests to opt SurfaceSigned in for the
// `subscription cancel` flow.
func WithAllowDestructiveOn(s ...cmdsurface.Surface) ExampleOption {
	return func(c *exampleConfig) {
		c.allowDestructiveOn = append(c.allowDestructiveOn, s...)
	}
}

// WithSignedURLTTL stamps a default TTL on the exposed SignedIssuer
// instance so tests that need expiration semantics (Signed expired)
// can issue short-lived URLs without reaching into the issuer's
// internals. Zero leaves the per-call TTL choice to the caller.
//
// NOTE: this only affects exampleApp.SignedIssuer's default; the
// existing Issue(ctx, token, ttl) signature still controls the
// returned URL's expiry. Tests pass the short TTL into Issue
// directly.
func WithSignedURLTTL(d time.Duration) ExampleOption {
	return func(c *exampleConfig) { c.signedURLTTL = d }
}

// BuildExample wires the demo cobra tree onto every supported
// surface — CLI, REST, RPC, MCP, WS, SSE, Bus, Cron, Lib, Webhook,
// OAuth-CB, Signed — and returns the assembled exampleApp. ctx
// bounds the lifetime of the WS hub goroutine and the bus
// subscriptions; canceling it stops them even if the returned
// Cleanup is never called.
//
// The function never panics; surface-mount failures are returned as
// errors so callers (main and tests) can decide how to react.
func BuildExample(ctx context.Context, logger *slog.Logger, opts ...ExampleOption) (*exampleApp, error) {
	if logger == nil {
		logger = slog.Default()
	}
	cfg := exampleConfig{}
	for _, o := range opts {
		o(&cfg)
	}

	root := buildCobraTree()
	bridge, sinkBuf := buildBridge(root, logger, cfg)

	router, err := buildRouter(ctx, bridge)
	if err != nil {
		return nil, err
	}
	rpcSrv, err := buildRPC(bridge, logger)
	if err != nil {
		return nil, err
	}

	// Bus: in-process backend doubling as Subscriber + EventPublisher.
	// One binding wires `widget add` to widgets.add.req / widgets.add.resp.
	bus := newExampleBus()
	busCleanup, err := cmdsurface.MountBus(bridge, bus, bus,
		[]cmdsurface.BusBinding{
			{
				Path:          []string{"widget", "add"},
				RequestTopic:  "widgets.add.req",
				ResponseTopic: "widgets.add.resp",
			},
		},
		cmdsurface.WithBusContext(ctx),
	)
	if err != nil {
		return nil, fmt.Errorf("MountBus: %w", err)
	}

	// Cron: schedule `report generate` every minute. The default
	// engine uses robfig/cron/v3 under the hood and autostarts.
	cronEng := cmdsurface.DefaultCronEngine()
	cronCleanup, err := cmdsurface.MountCron(bridge, cronEng,
		[]cmdsurface.CronSchedule{
			{Path: []string{"report", "generate"}, Expr: "*/1 * * * *"},
		},
		cmdsurface.WithCronContext(ctx),
		cmdsurface.WithCronLogger(func(format string, args ...any) {
			log.Printf("[cron] "+format, args...)
		}),
	)
	if err != nil {
		busCleanup()
		return nil, fmt.Errorf("MountCron: %w", err)
	}

	// Webhook: one mapping for `notify message`. Inbound POST
	// /hooks/notify with an HMAC-SHA256 signature in
	// X-Webhook-Signature renders the JSON body's .source / .title
	// into the leaf's --source / --title flags.
	webhookSecret := []byte("example-webhook-secret")
	webhookMappings := []cmdsurface.WebhookMapping{
		{
			Name: "notify",
			Path: []string{"notify", "message"},
			FlagMap: map[string]string{
				"source": `{{ .body.source }}`,
				"title":  `{{ .body.title }}`,
			},
			Auth: cmdsurface.AuthHMAC{
				Header: "X-Webhook-Signature",
				Prefix: "sha256=",
				Secret: webhookSecret,
			},
		},
	}
	if err := cmdsurface.MountWebhooks(bridge, router, webhookMappings); err != nil {
		cronCleanup()
		busCleanup()
		return nil, fmt.Errorf("MountWebhooks: %w", err)
	}

	// OAuth callback: one provider for `auth oauth-link`. State is
	// issued at GET /oauth/example/authorize (redirects to a stub
	// upstream URL) and consumed at GET /oauth/example/callback.
	stateStore := cmdsurface.NewInMemoryStateStore()
	oauthProviders := []cmdsurface.OAuthProvider{
		{
			Name: "example",
			Path: []string{"auth", "oauth-link"},
			FlagFromQuery: map[string]string{
				"code":  "code",
				"state": "state",
			},
			ErrorRedirect:   "/oauth-error",
			SuccessRedirect: "/oauth-done",
		},
	}
	if err := cmdsurface.MountOAuth(bridge, router, oauthProviders, stateStore,
		cmdsurface.WithOAuthAuthorizeFn(func(_ string) (string, error) {
			return "https://example.invalid/authorize?client_id=demo", nil
		}),
		cmdsurface.WithOAuthStateTTL(2*time.Minute),
	); err != nil {
		cronCleanup()
		busCleanup()
		return nil, fmt.Errorf("MountOAuth: %w", err)
	}

	// Signed URL: verifier at /x/{token}; issuer constructed
	// separately so tests can mint URLs via Issue / IssueViaBridge.
	signedKey := []byte("example-signed-url-secret-please-rotate")
	nonceStore := cmdsurface.NewInMemoryNonceStore()
	if err := cmdsurface.MountSigned(bridge, router, signedKey, nonceStore,
		cmdsurface.WithSignedPrefix("/x"),
	); err != nil {
		cronCleanup()
		busCleanup()
		return nil, fmt.Errorf("MountSigned: %w", err)
	}
	issuer := &cmdsurface.SignedIssuer{
		Key:       signedKey,
		Store:     nonceStore,
		URLPrefix: "/x",
	}

	httpSrv := &http.Server{Addr: ":8080", Handler: router}
	rpcHTTP := &http.Server{Addr: ":8081", Handler: rpcSrv}

	var cleanupOnce sync.Once
	cleanup := func() {
		cleanupOnce.Do(func() {
			cronCleanup()
			busCleanup()
		})
	}

	return &exampleApp{
		Bridge:        bridge,
		Router:        router,
		RPCSrv:        rpcSrv,
		HTTPSrv:       httpSrv,
		RPCHTTP:       rpcHTTP,
		Bus:           bus,
		SinkBuf:       sinkBuf,
		Cron:          cronEng,
		WebhookSecret: webhookSecret,
		OAuthState:    stateStore,
		SignedIssuer:  issuer,
		SignedNonce:   nonceStore,
		Cleanup:       cleanup,
	}, nil
}

// buildRouter constructs the shared HTTP router and mounts every
// HTTP-bound surface on it: REST, MCP, WebSocket, SSE.
func buildRouter(ctx context.Context, b *cmdsurface.Bridge) (*api.Router, error) {
	r := api.NewRouter(
		api.WithMiddleware(api.RequestID(), api.Recovery(nil)),
		api.WithOpenAPI(api.OpenAPIConfig{
			Title:   "cmdsurface example",
			Version: "0.0.0",
		}),
	)
	if err := cmdsurface.MountREST(b, r,
		cmdsurface.WithRESTOpenAPI(api.HumaAPI(r)),
		cmdsurface.WithRESTAuth(allowAnyAuth),
	); err != nil {
		return nil, fmt.Errorf("MountREST: %w", err)
	}
	if err := cmdsurface.MountMCP(b, r,
		cmdsurface.WithMCPServerInfo("cmdsurface-example", "0.0.0"),
	); err != nil {
		return nil, fmt.Errorf("MountMCP: %w", err)
	}
	if err := cmdsurface.MountWS(b, r,
		cmdsurface.WithWSContext(ctx),
	); err != nil {
		return nil, fmt.Errorf("MountWS: %w", err)
	}
	if err := cmdsurface.MountSSE(b, r,
		cmdsurface.WithSSEAuth(allowAnyAuth),
	); err != nil {
		return nil, fmt.Errorf("MountSSE: %w", err)
	}
	return r, nil
}

// buildRPC wires the ConnectRPC service on its own *rpc.Server.
func buildRPC(b *cmdsurface.Bridge, logger *slog.Logger) (*rpc.Server, error) {
	rpcSrv := rpc.NewServer(rpc.WithInterceptors(
		rpc.RequestIDInterceptor(),
		rpc.RecoveryInterceptor(func(v any) {
			logger.Error("rpc panic", "value", v)
		}),
	))
	if err := cmdsurface.MountRPC(b, rpcSrv); err != nil {
		return nil, fmt.Errorf("MountRPC: %w", err)
	}
	return rpcSrv, nil
}

// buildBridge wires the bridge with the example's policy. CLI and
// Lib are the only surfaces allowed to invoke destructive leaves;
// every other surface refuses with destructive_blocked. `widget
// delete` is explicitly hidden from every remote surface as
// belt-and-braces (so it does not appear in OpenAPI / MCP listings).
//
// Test callers can extend AllowDestructiveOn via
// WithAllowDestructiveOn(s ...Surface). The default (no option) keeps
// the conservative production policy.
//
// The bridge is built with a sinkRunner wrapping the default
// InProcessRunner so every Result the bridge produces — regardless
// of originating surface — fans out to:
//
//   - a LogSink emitting structured records via slog
//   - a FileSink appending JSON Lines to an in-memory buffer
//     (real adopters substitute an *os.File or io.Writer to disk)
//
// The buffer is returned so tests can assert that the sink pipeline
// fired without racing the slog handler's stderr write.
func buildBridge(root *cobra.Command, logger *slog.Logger, cfg exampleConfig) (*cmdsurface.Bridge, *bytes.Buffer) {
	allowDestructive := []cmdsurface.Surface{
		cmdsurface.SurfaceCLI,
		cmdsurface.SurfaceLib,
	}
	allowDestructive = append(allowDestructive, cfg.allowDestructiveOn...)

	policy := cmdsurface.Policy{
		DefaultEnabled: []cmdsurface.Surface{
			cmdsurface.SurfaceCLI,
			cmdsurface.SurfaceREST,
			cmdsurface.SurfaceRPC,
			cmdsurface.SurfaceMCP,
			cmdsurface.SurfaceWS,
			cmdsurface.SurfaceSSE,
			cmdsurface.SurfaceBus,
			cmdsurface.SurfaceCron,
			cmdsurface.SurfaceLib,
			cmdsurface.SurfaceWebhook,
			cmdsurface.SurfaceOAuthCB,
			cmdsurface.SurfaceSigned,
		},
		AllowDestructiveOn: allowDestructive,
	}

	// Build inner Runner first; the bridge holds the wrapped runner so
	// every Bridge.Invoke flows through the sink fan-out.
	sinkBuf := &bytes.Buffer{}
	sinks := cmdsurface.SinkSet{
		{Sink: &cmdsurface.LogSink{Handler: logger.Handler()}, OnError: true, OnOK: true},
		{Sink: &cmdsurface.FileSink{W: sinkBuf}, OnError: true, OnOK: true},
	}
	inner := cmdsurface.InProcessRunner(root)
	runner := newSinkRunner(inner, sinks, logger)

	b := cmdsurface.New(root,
		cmdsurface.WithPolicy(policy),
		cmdsurface.WithRunner(runner),
	)
	b.Expose("*",
		cmdsurface.SurfaceCLI,
		cmdsurface.SurfaceREST,
		cmdsurface.SurfaceRPC,
		cmdsurface.SurfaceMCP,
		cmdsurface.SurfaceWS,
		cmdsurface.SurfaceSSE,
		cmdsurface.SurfaceBus,
		cmdsurface.SurfaceCron,
		cmdsurface.SurfaceLib,
		cmdsurface.SurfaceWebhook,
		cmdsurface.SurfaceOAuthCB,
		cmdsurface.SurfaceSigned,
	)
	b.Hide("widget delete",
		cmdsurface.SurfaceREST,
		cmdsurface.SurfaceRPC,
		cmdsurface.SurfaceMCP,
		cmdsurface.SurfaceWS,
		cmdsurface.SurfaceSSE,
		cmdsurface.SurfaceBus,
		cmdsurface.SurfaceCron,
		cmdsurface.SurfaceWebhook,
		cmdsurface.SurfaceOAuthCB,
		cmdsurface.SurfaceSigned,
	)
	// Lock the new destructive `subscription cancel` leaf off every
	// remote surface the example otherwise auto-exposes. Signed is
	// handled separately below (it stays exposed when the caller
	// opts in via WithAllowDestructiveOn(SurfaceSigned)).
	b.Hide("subscription cancel",
		cmdsurface.SurfaceREST,
		cmdsurface.SurfaceRPC,
		cmdsurface.SurfaceMCP,
		cmdsurface.SurfaceWS,
		cmdsurface.SurfaceSSE,
		cmdsurface.SurfaceBus,
		cmdsurface.SurfaceCron,
		cmdsurface.SurfaceWebhook,
		cmdsurface.SurfaceOAuthCB,
	)

	// MountSigned scans every leaf enabled on SurfaceSigned and
	// refuses to mount when one is destructive but
	// Policy.AllowDestructiveOn omits SurfaceSigned. Hide every
	// destructive leaf from SurfaceSigned unless the caller has
	// opted SurfaceSigned in via WithAllowDestructiveOn.
	signedAllowsDestructive := false
	for _, s := range cfg.allowDestructiveOn {
		if s == cmdsurface.SurfaceSigned {
			signedAllowsDestructive = true
			break
		}
	}
	if !signedAllowsDestructive {
		b.Hide("subscription cancel", cmdsurface.SurfaceSigned)
		b.Hide("report purge", cmdsurface.SurfaceSigned)
	}
	return b, sinkBuf
}

// allowAnyAuth is a permissive AuthFunc used to satisfy the
// auth-required gating on `report purge` in the example. Real
// adopters supply a validator that inspects the request.
func allowAnyAuth(_ *http.Request) (any, error) { return struct{}{}, nil }
