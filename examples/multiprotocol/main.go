// Command multiprotocol demonstrates a server exposing REST+OpenAPI,
// WebSocket, and ConnectRPC for the same Widget entity.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"sync"

	"connectrpc.com/connect"

	"hop.top/kit/go/transport/api"
	"hop.top/kit/go/transport/rpc"
)

// Widget is a minimal entity for demonstration.
type Widget struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

func (w Widget) GetID() string { return w.ID }

// widgetStore is a thread-safe in-memory Service.
type widgetStore struct {
	mu    sync.RWMutex
	items map[string]Widget
}

func newWidgetStore() *widgetStore {
	return &widgetStore{items: make(map[string]Widget)}
}

func (s *widgetStore) Create(_ context.Context, w Widget) (Widget, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.items[w.ID]; ok {
		return Widget{}, fmt.Errorf("widget %s exists", w.ID)
	}
	s.items[w.ID] = w
	return w, nil
}

func (s *widgetStore) Get(_ context.Context, id string) (Widget, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	w, ok := s.items[id]
	if !ok {
		return Widget{}, fmt.Errorf("widget %s not found", id)
	}
	return w, nil
}

func (s *widgetStore) List(_ context.Context, _ api.Query) ([]Widget, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Widget, 0, len(s.items))
	for _, w := range s.items {
		out = append(out, w)
	}
	return out, nil
}

func (s *widgetStore) Update(_ context.Context, w Widget) (Widget, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.items[w.ID]; !ok {
		return Widget{}, fmt.Errorf("widget %s not found", w.ID)
	}
	s.items[w.ID] = w
	return w, nil
}

func (s *widgetStore) Delete(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.items, id)
	return nil
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	svc := newWidgetStore()
	log := slog.Default()

	// --- REST + OpenAPI (port 8080) ---

	r := api.NewRouter(
		api.WithMiddleware(api.RequestID(), api.Recovery(nil)),
		api.WithOpenAPI(api.OpenAPIConfig{
			Title:   "Widget API",
			Version: "1.0.0",
		}),
	)

	r.Mount("/widgets", api.ResourceRouter[Widget](svc,
		api.WithHumaAPI[Widget](api.HumaAPI(r), "/widgets"),
	))

	// WebSocket hub on same router.
	hub := api.NewHub()
	go hub.Run(ctx)
	r.Handle("GET", "/ws", api.WSHandler(hub))

	httpSrv := &http.Server{Addr: ":8080", Handler: r}

	// --- ConnectRPC (port 8081) ---

	rpcSrv := rpc.NewServer(
		rpc.WithInterceptors(
			rpc.RequestIDInterceptor(),
			rpc.LogInterceptor(log.Info),
			rpc.RecoveryInterceptor(func(v any) {
				log.Error("rpc panic", "value", v)
			}),
		),
	)

	path, handler := rpc.RPCResource[Widget](svc,
		connect.WithInterceptors(rpcSrv.Interceptors()...),
	)
	rpcSrv.Handle(path, handler)

	rpcHTTP := &http.Server{Addr: ":8081", Handler: rpcSrv}

	// --- Start both servers ---

	errCh := make(chan error, 2)
	go func() { errCh <- httpSrv.ListenAndServe() }()
	go func() { errCh <- rpcHTTP.ListenAndServe() }()
	log.Info("servers started",
		"rest", "http://localhost:8080",
		"rpc", "http://localhost:8081",
	)

	select {
	case <-ctx.Done():
	case err := <-errCh:
		if !errors.Is(err, http.ErrServerClosed) {
			log.Error("server error", "err", err)
			os.Exit(1)
		}
	}

	_ = httpSrv.Shutdown(context.Background())
	_ = rpcHTTP.Shutdown(context.Background())
}
