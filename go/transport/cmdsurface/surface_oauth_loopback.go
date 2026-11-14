package cmdsurface

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"time"

	"golang.org/x/oauth2"

	"hop.top/kit/go/transport/api"
)

// DefaultLoopbackTimeout bounds a loopback login when none is given.
const DefaultLoopbackTimeout = 5 * time.Minute

// LoopbackOptions configures LoopbackPKCE.
type LoopbackOptions struct {
	// Timeout bounds the whole login (default DefaultLoopbackTimeout).
	Timeout time.Duration
	// OnReady receives the full upstream authorization URL (challenge and
	// state included) once the listener is up; the CLI opens it in a
	// browser. Called once.
	OnReady func(authorizeURL string)
}

// LoopbackPKCE runs one interactive consent for a CLI: it listens on
// 127.0.0.1 with an ephemeral port, mounts only the callback route there (the
// provider's Config.RedirectURL is set to the loopback callback), and
// returns the first completed result. It pre-issues exactly one state and
// verifier itself and hands OnReady the upstream authorization URL; no local
// route mints states, so other local processes or web pages cannot start a
// login of their own. It fails on provider denial, issuer mismatch or
// exchange failure carrying the valid state, and with
// context.DeadlineExceeded on timeout. Callbacks with a missing or unknown
// state are rejected but do not end the login.
func LoopbackPKCE(ctx context.Context, p PKCEProvider, opts LoopbackOptions) (PKCEResult, error) {
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = DefaultLoopbackTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return PKCEResult{}, fmt.Errorf("cmdsurface: loopback listen: %w", err)
	}
	base := "http://" + ln.Addr().String()
	cfg := defaultOAuthConfig()
	p.Config.RedirectURL = base + cfg.prefix + "/" + p.Name + "/callback"

	type outcome struct {
		res PKCEResult
		err error
	}
	ch := make(chan outcome, 1)
	hooks := pkceHooks{done: func(res PKCEResult, err error, fatal bool) {
		if !fatal {
			return
		}
		select {
		case ch <- outcome{res, err}:
		default:
		}
	}}
	hooks.noAuthorize = true
	store := NewInMemoryPKCEStateStore()
	router := api.NewRouter()
	if err := mountPKCE(router, []PKCEProvider{p}, store, hooks); err != nil {
		_ = ln.Close()
		return PKCEResult{}, err
	}
	verifier := oauth2.GenerateVerifier()
	state, err := store.Issue(ctx, p.Name, verifier, "", timeout)
	if err != nil {
		_ = ln.Close()
		return PKCEResult{}, fmt.Errorf("cmdsurface: loopback state: %w", err)
	}
	p.Config.Endpoint.AuthStyle = oauth2.AuthStyleInParams
	authOpts := append([]oauth2.AuthCodeOption{oauth2.S256ChallengeOption(verifier)}, p.AuthParams...)
	authURL := p.Config.AuthCodeURL(state, authOpts...)
	srv := &http.Server{Handler: router, ReadHeaderTimeout: 10 * time.Second}
	go func() { _ = srv.Serve(ln) }()
	defer func() {
		sctx, c := context.WithTimeout(context.Background(), 2*time.Second)
		defer c()
		_ = srv.Shutdown(sctx)
	}()

	if opts.OnReady != nil {
		opts.OnReady(authURL)
	}
	select {
	case o := <-ch:
		return o.res, o.err
	case <-ctx.Done():
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return PKCEResult{}, fmt.Errorf("cmdsurface: loopback authorize timed out: %w", ctx.Err())
		}
		return PKCEResult{}, ctx.Err()
	}
}
