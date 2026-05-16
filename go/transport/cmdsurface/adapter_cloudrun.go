package cmdsurface

// Cloud Run is containerised, request-scoped HTTP. The adapter ships
// a binary that:
//
//   - Listens on $PORT (default 8080).
//   - Honors Cloud Run's signal contract: SIGTERM → drain in-flight
//     requests within ~10s → exit. The default grace is 9s so the
//     server replies cleanly before SIGKILL lands.
//   - Mounts the bridge on the surfaces an adopter opts into (REST /
//     SSE / MCP / WS — the surfaces that have meaningful defaults
//     without per-adopter wiring).
//
// Webhook / OAuth / Signed need mappings, providers, or key+store
// configuration that has no universal default. Adopters that want
// those build the *api.Router themselves and pass it via
// CloudRunConfig.Router; RunCloudRun will start the server and
// handle the lifecycle without mounting extras.
//
// Reference Dockerfile (place in your binary's directory):
//
//	FROM golang:1.26-alpine AS build
//	WORKDIR /src
//	COPY go.mod go.sum ./
//	RUN go mod download
//	COPY . .
//	RUN go build -o /app ./cmd/your-binary
//
//	FROM gcr.io/distroless/static-debian12
//	COPY --from=build /app /app
//	ENV PORT=8080
//	ENTRYPOINT ["/app"]
//
// Deploy:
//
//	gcloud run deploy your-service --source . --region us-central1

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"hop.top/kit/go/transport/api"
)

// CloudRunConfig configures the Cloud Run entry point.
type CloudRunConfig struct {
	// Port overrides the $PORT env var. When zero, $PORT is read
	// from the environment; if $PORT is also unset, the adapter
	// falls back to 8080 (Cloud Run's documented default).
	Port int

	// ShutdownGrace is the deadline http.Server.Shutdown gets
	// before the call returns. Cloud Run sends SIGTERM ~10s before
	// SIGKILL, so the default of 9s leaves a 1s margin for the
	// runtime to flush and exit cleanly.
	ShutdownGrace time.Duration

	// Router is an optional pre-built router. When nil, a default
	// router with api.RequestID + api.Recovery middleware is
	// created. Surfaces in cfg.Surfaces mount onto whichever router
	// is in effect; adopters that need more control (custom
	// middleware, manual Webhook/OAuth/Signed mounts) build the
	// router themselves and pass it here.
	Router *api.Router

	// Surfaces declares which surfaces to mount with default
	// options. For more control, build the router yourself and
	// mount surfaces manually; this field is for the common case.
	Surfaces CloudRunSurfaces

	// OnReady is called after the listener is up. Useful for
	// startup-probe / health-check wiring and for tests that need
	// to discover the bound address when Port is zero.
	OnReady func(addr string)

	// OnShutdown is called when SIGTERM (or context cancellation)
	// is received, before the grace period elapses. Useful for
	// flushing logs or closing collaborator connections.
	OnShutdown func()
}

// CloudRunSurfaces selects which surfaces RunCloudRun mounts with
// default options. For each field set true, the corresponding
// Mount... helper is called against cfg.Router (or the default
// router) with no surface options — equivalent to MountREST(b, r),
// MountSSE(b, r), MountMCP(b, r), and MountWS(b, r).
//
// Webhook / OAuth / Signed are intentionally absent: those surfaces
// require adopter-supplied mappings, providers, or keys, and have
// no safe defaults. Adopters that want them build the router
// manually and pass it via CloudRunConfig.Router.
type CloudRunSurfaces struct {
	REST bool
	SSE  bool
	MCP  bool
	WS   bool
}

// RunCloudRun starts the Cloud Run-shaped HTTP server. It reads
// $PORT (overridable via cfg.Port), builds a router (overridable
// via cfg.Router), mounts the surfaces requested in cfg.Surfaces,
// and serves until SIGTERM / SIGINT (or fatal listener error).
// Returns nil on clean shutdown.
//
// Typical adopter usage:
//
//	func main() {
//	    b := buildBridge()
//	    err := cmdsurface.RunCloudRun(b, cmdsurface.CloudRunConfig{
//	        Surfaces: cmdsurface.CloudRunSurfaces{REST: true},
//	        OnReady:  func(addr string) { log.Printf("ready on %s", addr) },
//	    })
//	    if err != nil {
//	        log.Fatal(err)
//	    }
//	}
func RunCloudRun(b *Bridge, cfg CloudRunConfig) error {
	ctx, stop := signal.NotifyContext(context.Background(),
		syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	return runCloudRunCtx(ctx, b, cfg)
}

// runCloudRunCtx is the testable inner loop. It uses the supplied
// ctx as the shutdown trigger instead of installing signal handlers,
// so tests can drive the lifecycle with a cancel func.
func runCloudRunCtx(ctx context.Context, b *Bridge, cfg CloudRunConfig) error {
	if b == nil {
		return errors.New("cmdsurface: RunCloudRun: nil Bridge")
	}

	port, err := resolveCloudRunPort(cfg.Port, os.Getenv("PORT"))
	if err != nil {
		return err
	}

	r := cfg.Router
	if r == nil {
		r = api.NewRouter(api.WithMiddleware(api.RequestID(), api.Recovery(nil)))
	}

	if err := mountCloudRunSurfaces(b, r, cfg.Surfaces); err != nil {
		return err
	}

	srv := &http.Server{
		Addr:    fmt.Sprintf(":%d", port),
		Handler: r,
		BaseContext: func(_ net.Listener) context.Context {
			return context.Background()
		},
	}

	// Bind the listener up front so we can report the real address
	// (matters when port==0 → OS-assigned). ListenAndServe gives us
	// no way to read back the chosen port; Listen + Serve does.
	ln, err := net.Listen("tcp", srv.Addr)
	if err != nil {
		return fmt.Errorf("cmdsurface: listen %s: %w", srv.Addr, err)
	}

	srvErr := make(chan error, 1)
	go func() {
		err := srv.Serve(ln)
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
		srvErr <- err
	}()

	if cfg.OnReady != nil {
		cfg.OnReady(ln.Addr().String())
	}

	select {
	case err := <-srvErr:
		return err
	case <-ctx.Done():
	}

	if cfg.OnShutdown != nil {
		cfg.OnShutdown()
	}

	grace := cfg.ShutdownGrace
	if grace <= 0 {
		grace = 9 * time.Second
	}
	shutCtx, cancel := context.WithTimeout(context.Background(), grace)
	defer cancel()
	if err := srv.Shutdown(shutCtx); err != nil {
		return err
	}
	// Drain the serve goroutine's return value so callers don't
	// miss a late listener error.
	if err := <-srvErr; err != nil {
		return err
	}
	return nil
}

// resolveCloudRunPort picks the effective listen port. cfgPort wins
// when non-zero; otherwise envPort (a string, possibly empty) is
// parsed; otherwise 8080. Returns an error when envPort is set but
// not a valid integer.
func resolveCloudRunPort(cfgPort int, envPort string) (int, error) {
	if cfgPort != 0 {
		return cfgPort, nil
	}
	if envPort == "" {
		return 8080, nil
	}
	n, err := strconv.Atoi(envPort)
	if err != nil {
		return 0, fmt.Errorf("cmdsurface: invalid $PORT %q: %w", envPort, err)
	}
	return n, nil
}

// mountCloudRunSurfaces calls Mount... for every surface set true
// in s. Errors are wrapped with the surface name to keep diagnosis
// short. A zero-valued CloudRunSurfaces mounts nothing.
func mountCloudRunSurfaces(b *Bridge, r *api.Router, s CloudRunSurfaces) error {
	if s.REST {
		if err := MountREST(b, r); err != nil {
			return fmt.Errorf("cmdsurface: mount REST: %w", err)
		}
	}
	if s.SSE {
		if err := MountSSE(b, r); err != nil {
			return fmt.Errorf("cmdsurface: mount SSE: %w", err)
		}
	}
	if s.MCP {
		if err := MountMCP(b, r); err != nil {
			return fmt.Errorf("cmdsurface: mount MCP: %w", err)
		}
	}
	if s.WS {
		if err := MountWS(b, r); err != nil {
			return fmt.Errorf("cmdsurface: mount WS: %w", err)
		}
	}
	return nil
}
