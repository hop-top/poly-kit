package rpc_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/structpb"

	crudv1 "hop.top/kit/contracts/proto/crud/v1"
	"hop.top/kit/contracts/proto/crud/v1/crudv1connect"
	"hop.top/kit/go/runtime/domain"
	"hop.top/kit/go/transport/rpc"
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

func setupClient(t *testing.T, svc *memService) (crudv1connect.EntityServiceClient, *httptest.Server) {
	t.Helper()
	mux := http.NewServeMux()
	path, handler := rpc.RPCResource[testEntity](svc)
	mux.Handle(path, handler)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	client := crudv1connect.NewEntityServiceClient(
		srv.Client(), srv.URL,
	)
	return client, srv
}

func entityStruct(t *testing.T, id, name string) *structpb.Struct {
	t.Helper()
	s, err := structpb.NewStruct(map[string]any{
		"id":   id,
		"name": name,
	})
	require.NoError(t, err)
	return s
}

func TestRPCResource_Create(t *testing.T) {
	client, _ := setupClient(t, newMemService())
	resp, err := client.Create(context.Background(), connect.NewRequest(
		&crudv1.CreateRequest{Entity: entityStruct(t, "1", "alice")},
	))
	require.NoError(t, err)
	assert.Equal(t, "alice", resp.Msg.Entity.Fields["name"].GetStringValue())
	assert.Equal(t, "1", resp.Msg.Entity.Fields["id"].GetStringValue())
}

func TestRPCResource_Get(t *testing.T) {
	svc := newMemService()
	client, _ := setupClient(t, svc)

	_, err := client.Create(context.Background(), connect.NewRequest(
		&crudv1.CreateRequest{Entity: entityStruct(t, "1", "alice")},
	))
	require.NoError(t, err)

	resp, err := client.Get(context.Background(), connect.NewRequest(
		&crudv1.GetRequest{Id: "1"},
	))
	require.NoError(t, err)
	assert.Equal(t, "alice", resp.Msg.Entity.Fields["name"].GetStringValue())
}

func TestRPCResource_List(t *testing.T) {
	svc := newMemService()
	client, _ := setupClient(t, svc)

	for i := range 3 {
		_, err := client.Create(context.Background(), connect.NewRequest(
			&crudv1.CreateRequest{Entity: entityStruct(t,
				fmt.Sprintf("%d", i), fmt.Sprintf("user-%d", i))},
		))
		require.NoError(t, err)
	}

	resp, err := client.List(context.Background(), connect.NewRequest(
		&crudv1.ListRequest{Limit: 10},
	))
	require.NoError(t, err)
	assert.Len(t, resp.Msg.Entities, 3)
}

func TestRPCResource_ListWithSearch(t *testing.T) {
	svc := newMemService()
	client, _ := setupClient(t, svc)

	_, _ = client.Create(context.Background(), connect.NewRequest(
		&crudv1.CreateRequest{Entity: entityStruct(t, "1", "alice")},
	))
	_, _ = client.Create(context.Background(), connect.NewRequest(
		&crudv1.CreateRequest{Entity: entityStruct(t, "2", "bob")},
	))

	resp, err := client.List(context.Background(), connect.NewRequest(
		&crudv1.ListRequest{Search: "alice"},
	))
	require.NoError(t, err)
	assert.Len(t, resp.Msg.Entities, 1)
	assert.Equal(t, "alice",
		resp.Msg.Entities[0].Fields["name"].GetStringValue())
}

func TestRPCResource_Update(t *testing.T) {
	svc := newMemService()
	client, _ := setupClient(t, svc)

	_, err := client.Create(context.Background(), connect.NewRequest(
		&crudv1.CreateRequest{Entity: entityStruct(t, "1", "alice")},
	))
	require.NoError(t, err)

	resp, err := client.Update(context.Background(), connect.NewRequest(
		&crudv1.UpdateRequest{Entity: entityStruct(t, "1", "alice-updated")},
	))
	require.NoError(t, err)
	assert.Equal(t, "alice-updated",
		resp.Msg.Entity.Fields["name"].GetStringValue())
}

func TestRPCResource_Delete(t *testing.T) {
	svc := newMemService()
	client, _ := setupClient(t, svc)

	_, err := client.Create(context.Background(), connect.NewRequest(
		&crudv1.CreateRequest{Entity: entityStruct(t, "1", "alice")},
	))
	require.NoError(t, err)

	_, err = client.Delete(context.Background(), connect.NewRequest(
		&crudv1.DeleteRequest{Id: "1"},
	))
	require.NoError(t, err)

	// confirm gone
	_, err = client.Get(context.Background(), connect.NewRequest(
		&crudv1.GetRequest{Id: "1"},
	))
	require.Error(t, err)
	assert.Equal(t, connect.CodeNotFound, connect.CodeOf(err))
}

func TestRPCResource_NotFoundError(t *testing.T) {
	client, _ := setupClient(t, newMemService())
	_, err := client.Get(context.Background(), connect.NewRequest(
		&crudv1.GetRequest{Id: "missing"},
	))
	require.Error(t, err)
	assert.Equal(t, connect.CodeNotFound, connect.CodeOf(err))
}

func TestRPCResource_ConflictError(t *testing.T) {
	svc := newMemService()
	client, _ := setupClient(t, svc)

	_, err := client.Create(context.Background(), connect.NewRequest(
		&crudv1.CreateRequest{Entity: entityStruct(t, "1", "alice")},
	))
	require.NoError(t, err)

	_, err = client.Create(context.Background(), connect.NewRequest(
		&crudv1.CreateRequest{Entity: entityStruct(t, "1", "alice")},
	))
	require.Error(t, err)
	assert.Equal(t, connect.CodeAlreadyExists, connect.CodeOf(err))
}
