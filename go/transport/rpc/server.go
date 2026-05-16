package rpc

import (
	"context"
	"errors"
	"net/http"
	"time"

	"connectrpc.com/connect"
)

// ServerOption configures the RPC server.
type ServerOption func(*serverConfig)

type serverConfig struct {
	readTimeout     time.Duration
	writeTimeout    time.Duration
	shutdownTimeout time.Duration
	interceptors    []connect.Interceptor
}

// WithReadTimeout sets the underlying HTTP server's read timeout.
func WithReadTimeout(d time.Duration) ServerOption {
	return func(c *serverConfig) { c.readTimeout = d }
}

// WithWriteTimeout sets the underlying HTTP server's write timeout.
func WithWriteTimeout(d time.Duration) ServerOption {
	return func(c *serverConfig) { c.writeTimeout = d }
}

// WithShutdownTimeout sets the graceful shutdown timeout.
func WithShutdownTimeout(d time.Duration) ServerOption {
	return func(c *serverConfig) { c.shutdownTimeout = d }
}

// WithInterceptors appends Connect interceptors applied to every
// registered service handler.
func WithInterceptors(interceptors ...connect.Interceptor) ServerOption {
	return func(c *serverConfig) {
		c.interceptors = append(c.interceptors, interceptors...)
	}
}

// Server wraps an http.ServeMux for ConnectRPC handler registration.
type Server struct {
	mux *http.ServeMux
	cfg *serverConfig
}

// NewServer creates a Server with the given options.
func NewServer(opts ...ServerOption) *Server {
	cfg := &serverConfig{
		readTimeout:     5 * time.Second,
		writeTimeout:    10 * time.Second,
		shutdownTimeout: 30 * time.Second,
	}
	for _, o := range opts {
		o(cfg)
	}
	return &Server{mux: http.NewServeMux(), cfg: cfg}
}

// Handle registers a Connect service handler at the given path.
func (s *Server) Handle(path string, handler http.Handler) {
	s.mux.Handle(path, handler)
}

// Interceptors returns the configured interceptors so service
// registrations can apply them via connect.WithInterceptors.
func (s *Server) Interceptors() []connect.Interceptor {
	return s.cfg.interceptors
}

// ServeHTTP implements http.Handler.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mux.ServeHTTP(w, r)
}

// ListenAndServe starts an HTTP/2-capable server for Connect handlers
// and blocks until ctx is canceled, then performs a graceful shutdown.
func ListenAndServe(ctx context.Context, addr string, srv *Server, opts ...ServerOption) error {
	for _, o := range opts {
		o(srv.cfg)
	}

	httpSrv := &http.Server{
		Addr:         addr,
		Handler:      srv,
		ReadTimeout:  srv.cfg.readTimeout,
		WriteTimeout: srv.cfg.writeTimeout,
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- httpSrv.ListenAndServe()
	}()

	select {
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		shutCtx, cancel := context.WithTimeout(
			context.Background(), srv.cfg.shutdownTimeout,
		)
		defer cancel()
		return httpSrv.Shutdown(shutCtx)
	}
}
