package api

import (
	"context"
	"errors"
	"net/http"
	"os/signal"
	"syscall"
	"time"
)

// ServerOption configures the HTTP server.
type ServerOption func(*serverConfig)

type serverConfig struct {
	readTimeout     time.Duration
	writeTimeout    time.Duration
	shutdownTimeout time.Duration
}

// WithReadTimeout sets the server's read timeout.
func WithReadTimeout(d time.Duration) ServerOption {
	return func(c *serverConfig) { c.readTimeout = d }
}

// WithWriteTimeout sets the server's write timeout.
func WithWriteTimeout(d time.Duration) ServerOption {
	return func(c *serverConfig) { c.writeTimeout = d }
}

// WithShutdownTimeout sets the graceful shutdown timeout.
func WithShutdownTimeout(d time.Duration) ServerOption {
	return func(c *serverConfig) { c.shutdownTimeout = d }
}

// ListenAndServe starts an HTTP server and blocks until the provided
// context is canceled, then performs a graceful shutdown.
// http.ErrServerClosed is treated as a clean exit (returns nil).
func ListenAndServe(ctx context.Context, addr string, handler http.Handler, opts ...ServerOption) error {
	cfg := &serverConfig{
		readTimeout:     5 * time.Second,
		writeTimeout:    10 * time.Second,
		shutdownTimeout: 30 * time.Second,
	}
	for _, o := range opts {
		o(cfg)
	}

	srv := &http.Server{
		Addr:         addr,
		Handler:      handler,
		ReadTimeout:  cfg.readTimeout,
		WriteTimeout: cfg.writeTimeout,
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- srv.ListenAndServe()
	}()

	select {
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		shutCtx, cancel := context.WithTimeout(context.Background(), cfg.shutdownTimeout)
		defer cancel()
		if err := srv.Shutdown(shutCtx); err != nil {
			return err
		}
		return nil
	}
}

// ListenAndServeWithSignals is a convenience wrapper that creates a
// signal-based context (SIGINT, SIGTERM) and delegates to ListenAndServe.
func ListenAndServeWithSignals(addr string, handler http.Handler, opts ...ServerOption) error {
	ctx, stop := signal.NotifyContext(context.Background(),
		syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	return ListenAndServe(ctx, addr, handler, opts...)
}
