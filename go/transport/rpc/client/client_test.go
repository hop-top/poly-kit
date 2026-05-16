package client_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/runtime/domain"
	"hop.top/kit/go/transport/api"
	"hop.top/kit/go/transport/rpc"
	rpcclient "hop.top/kit/go/transport/rpc/client"
)

// testEntity is a minimal domain.Entity for tests.
type testEntity struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

func (e testEntity) GetID() string { return e.ID }

// memService is an in-memory api.Service[testEntity].
type memService struct {
	mu    sync.RWMutex
	store map[string]testEntity
}

func newMemService() *memService {
	return &memService{store: make(map[string]testEntity)}
}

func (s *memService) Create(_ context.Context, e testEntity) (testEntity, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.store[e.ID]; ok {
		return testEntity{}, fmt.Errorf("already exists: %w", domain.ErrConflict)
	}
	s.store[e.ID] = e
	return e, nil
}

func (s *memService) Get(_ context.Context, id string) (testEntity, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	e, ok := s.store[id]
	if !ok {
		return testEntity{}, fmt.Errorf("not found: %w", domain.ErrNotFound)
	}
	return e, nil
}

func (s *memService) List(_ context.Context, q domain.Query) ([]testEntity, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var result []testEntity
	for _, e := range s.store {
		if q.Search != "" && e.Name != q.Search {
			continue
		}
		result = append(result, e)
	}
	if q.Offset > 0 && q.Offset < len(result) {
		result = result[q.Offset:]
	}
	if q.Limit > 0 && q.Limit < len(result) {
		result = result[:q.Limit]
	}
	return result, nil
}

func (s *memService) Update(_ context.Context, e testEntity) (testEntity, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.store[e.ID]; !ok {
		return testEntity{}, fmt.Errorf("not found: %w", domain.ErrNotFound)
	}
	s.store[e.ID] = e
	return e, nil
}

func (s *memService) Delete(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.store[id]; !ok {
		return fmt.Errorf("not found: %w", domain.ErrNotFound)
	}
	delete(s.store, id)
	return nil
}

func setupServer(t *testing.T) (*memService, *httptest.Server) {
	t.Helper()
	svc := newMemService()
	srv := rpc.NewServer()
	path, handler := rpc.RPCResource[testEntity](svc,
		connect.WithInterceptors(srv.Interceptors()...),
	)
	srv.Handle(path, handler)
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)
	return svc, ts
}

func TestClient_Create(t *testing.T) {
	_, ts := setupServer(t)
	c := rpcclient.New[testEntity](ts.URL,
		rpcclient.WithHTTPClient(ts.Client()),
	)

	got, err := c.Create(context.Background(), testEntity{
		ID: "1", Name: "alice",
	})
	require.NoError(t, err)
	assert.Equal(t, "1", got.GetID())
	assert.Equal(t, "alice", got.Name)
}

func TestClient_Get(t *testing.T) {
	_, ts := setupServer(t)
	c := rpcclient.New[testEntity](ts.URL,
		rpcclient.WithHTTPClient(ts.Client()),
	)
	ctx := context.Background()

	_, err := c.Create(ctx, testEntity{ID: "1", Name: "alice"})
	require.NoError(t, err)

	got, err := c.Get(ctx, "1")
	require.NoError(t, err)
	assert.Equal(t, "1", got.GetID())
	assert.Equal(t, "alice", got.Name)
}

func TestClient_List(t *testing.T) {
	_, ts := setupServer(t)
	c := rpcclient.New[testEntity](ts.URL,
		rpcclient.WithHTTPClient(ts.Client()),
	)
	ctx := context.Background()

	for i := range 3 {
		_, err := c.Create(ctx, testEntity{
			ID:   fmt.Sprintf("%d", i),
			Name: fmt.Sprintf("user-%d", i),
		})
		require.NoError(t, err)
	}

	items, err := c.List(ctx, api.Query{Limit: 10})
	require.NoError(t, err)
	assert.Len(t, items, 3)
}

func TestClient_ListWithSearch(t *testing.T) {
	_, ts := setupServer(t)
	c := rpcclient.New[testEntity](ts.URL,
		rpcclient.WithHTTPClient(ts.Client()),
	)
	ctx := context.Background()

	_, _ = c.Create(ctx, testEntity{ID: "1", Name: "alice"})
	_, _ = c.Create(ctx, testEntity{ID: "2", Name: "bob"})

	items, err := c.List(ctx, api.Query{Search: "alice"})
	require.NoError(t, err)
	assert.Len(t, items, 1)
	assert.Equal(t, "alice", items[0].Name)
}

func TestClient_Update(t *testing.T) {
	_, ts := setupServer(t)
	c := rpcclient.New[testEntity](ts.URL,
		rpcclient.WithHTTPClient(ts.Client()),
	)
	ctx := context.Background()

	_, err := c.Create(ctx, testEntity{ID: "1", Name: "alice"})
	require.NoError(t, err)

	got, err := c.Update(ctx, testEntity{ID: "1", Name: "alice-updated"})
	require.NoError(t, err)
	assert.Equal(t, "alice-updated", got.Name)
}

func TestClient_Delete(t *testing.T) {
	_, ts := setupServer(t)
	c := rpcclient.New[testEntity](ts.URL,
		rpcclient.WithHTTPClient(ts.Client()),
	)
	ctx := context.Background()

	_, err := c.Create(ctx, testEntity{ID: "1", Name: "alice"})
	require.NoError(t, err)

	err = c.Delete(ctx, "1")
	require.NoError(t, err)

	// confirm gone
	_, err = c.Get(ctx, "1")
	require.Error(t, err)
	var ae *api.APIError
	require.True(t, errors.As(err, &ae))
	assert.Equal(t, http.StatusNotFound, ae.Status)
}

func TestClient_NotFoundError(t *testing.T) {
	_, ts := setupServer(t)
	c := rpcclient.New[testEntity](ts.URL,
		rpcclient.WithHTTPClient(ts.Client()),
	)

	_, err := c.Get(context.Background(), "missing")
	require.Error(t, err)

	var ae *api.APIError
	require.True(t, errors.As(err, &ae))
	assert.Equal(t, http.StatusNotFound, ae.Status)
	assert.Equal(t, "not_found", ae.Code)
}

func TestClient_WithAuth(t *testing.T) {
	svc := newMemService()

	// interceptor that checks for Bearer token
	var gotAuth string
	authCheck := connect.UnaryInterceptorFunc(
		func(next connect.UnaryFunc) connect.UnaryFunc {
			return func(
				ctx context.Context,
				req connect.AnyRequest,
			) (connect.AnyResponse, error) {
				gotAuth = req.Header().Get("Authorization")
				return next(ctx, req)
			}
		},
	)

	srv := rpc.NewServer(rpc.WithInterceptors(authCheck))
	path, handler := rpc.RPCResource[testEntity](svc,
		connect.WithInterceptors(srv.Interceptors()...),
	)
	srv.Handle(path, handler)
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	c := rpcclient.New[testEntity](ts.URL,
		rpcclient.WithHTTPClient(ts.Client()),
		rpcclient.WithAuth("test-token-123"),
	)

	_, err := c.Create(context.Background(), testEntity{
		ID: "1", Name: "alice",
	})
	require.NoError(t, err)
	assert.Equal(t, "Bearer test-token-123", gotAuth)
}
