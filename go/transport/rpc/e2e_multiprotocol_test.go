package rpc_test

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
	restclient "hop.top/kit/go/transport/api/client"
	"hop.top/kit/go/transport/rpc"
	rpcclient "hop.top/kit/go/transport/rpc/client"
)

// crossWidget is the entity type shared across protocols.
type crossWidget struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Color string `json:"color"`
}

func (w crossWidget) GetID() string { return w.ID }

// crossSvc is an in-memory Service[crossWidget] for cross-protocol
// tests. Both REST and RPC servers share the same instance.
type crossSvc struct {
	mu    sync.RWMutex
	items map[string]crossWidget
}

func newCrossSvc() *crossSvc {
	return &crossSvc{items: make(map[string]crossWidget)}
}

func (s *crossSvc) Create(
	_ context.Context, e crossWidget,
) (crossWidget, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.items[e.ID]; ok {
		return crossWidget{}, fmt.Errorf(
			"already exists: %w", domain.ErrConflict,
		)
	}
	s.items[e.ID] = e
	return e, nil
}

func (s *crossSvc) Get(
	_ context.Context, id string,
) (crossWidget, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	e, ok := s.items[id]
	if !ok {
		return crossWidget{}, fmt.Errorf(
			"not found: %w", domain.ErrNotFound,
		)
	}
	return e, nil
}

func (s *crossSvc) List(
	_ context.Context, _ domain.Query,
) ([]crossWidget, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]crossWidget, 0, len(s.items))
	for _, v := range s.items {
		out = append(out, v)
	}
	return out, nil
}

func (s *crossSvc) Update(
	_ context.Context, e crossWidget,
) (crossWidget, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.items[e.ID]; !ok {
		return crossWidget{}, fmt.Errorf(
			"not found: %w", domain.ErrNotFound,
		)
	}
	s.items[e.ID] = e
	return e, nil
}

func (s *crossSvc) Delete(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.items[id]; !ok {
		return fmt.Errorf("not found: %w", domain.ErrNotFound)
	}
	delete(s.items, id)
	return nil
}

// setupCrossProtocol creates two httptest servers (REST + RPC)
// backed by the same crossSvc instance.
func setupCrossProtocol(t *testing.T) (
	rest *restclient.Client[crossWidget],
	rpcC *rpcclient.Client[crossWidget],
) {
	t.Helper()

	svc := newCrossSvc()

	// REST server
	restHandler := api.ResourceRouter[crossWidget](svc)
	restSrv := httptest.NewServer(restHandler)
	t.Cleanup(restSrv.Close)

	// RPC server
	rpcSrv := rpc.NewServer()
	path, handler := rpc.RPCResource[crossWidget](svc,
		connect.WithInterceptors(rpcSrv.Interceptors()...),
	)
	rpcSrv.Handle(path, handler)
	rpcTS := httptest.NewServer(rpcSrv)
	t.Cleanup(rpcTS.Close)

	rest = restclient.New[crossWidget](restSrv.URL,
		restclient.WithHTTPClient(restSrv.Client()),
	)
	rpcC = rpcclient.New[crossWidget](rpcTS.URL,
		rpcclient.WithHTTPClient(rpcTS.Client()),
	)
	return rest, rpcC
}

func TestE2E_CrossProtocol_CreateRPC_GetREST(t *testing.T) {
	rest, rpcC := setupCrossProtocol(t)
	ctx := context.Background()

	created, err := rpcC.Create(ctx, crossWidget{
		ID: "x1", Name: "Gear", Color: "silver",
	})
	require.NoError(t, err)
	assert.Equal(t, "x1", created.ID)

	got, err := rest.Get(ctx, "x1")
	require.NoError(t, err)
	assert.Equal(t, "Gear", got.Name)
	assert.Equal(t, "silver", got.Color)
}

func TestE2E_CrossProtocol_CreateREST_GetRPC(t *testing.T) {
	rest, rpcC := setupCrossProtocol(t)
	ctx := context.Background()

	_, err := rest.Create(ctx, crossWidget{
		ID: "x2", Name: "Bolt", Color: "zinc",
	})
	require.NoError(t, err)

	got, err := rpcC.Get(ctx, "x2")
	require.NoError(t, err)
	assert.Equal(t, "Bolt", got.Name)
	assert.Equal(t, "zinc", got.Color)
}

func TestE2E_CrossProtocol_ListRPC_AfterCreateREST(t *testing.T) {
	rest, rpcC := setupCrossProtocol(t)
	ctx := context.Background()

	for _, w := range []crossWidget{
		{ID: "a", Name: "Alpha", Color: "red"},
		{ID: "b", Name: "Beta", Color: "blue"},
	} {
		_, err := rest.Create(ctx, w)
		require.NoError(t, err)
	}

	items, err := rpcC.List(ctx, api.Query{Limit: 10})
	require.NoError(t, err)
	assert.Len(t, items, 2)
}

func TestE2E_CrossProtocol_DeleteRPC_GetREST_NotFound(t *testing.T) {
	rest, rpcC := setupCrossProtocol(t)
	ctx := context.Background()

	_, err := rest.Create(ctx, crossWidget{
		ID: "d1", Name: "Temp", Color: "grey",
	})
	require.NoError(t, err)

	err = rpcC.Delete(ctx, "d1")
	require.NoError(t, err)

	_, err = rest.Get(ctx, "d1")
	require.Error(t, err)
	var ae *api.APIError
	require.True(t, errors.As(err, &ae))
	assert.Equal(t, http.StatusNotFound, ae.Status)
}

func TestE2E_CrossProtocol_DeleteREST_GetRPC_NotFound(t *testing.T) {
	rest, rpcC := setupCrossProtocol(t)
	ctx := context.Background()

	_, err := rpcC.Create(ctx, crossWidget{
		ID: "d2", Name: "Ephemeral", Color: "white",
	})
	require.NoError(t, err)

	err = rest.Delete(ctx, "d2")
	require.NoError(t, err)

	_, err = rpcC.Get(ctx, "d2")
	require.Error(t, err)
	var ae *api.APIError
	require.True(t, errors.As(err, &ae))
	assert.Equal(t, http.StatusNotFound, ae.Status)
}
