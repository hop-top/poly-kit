package client_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/transport/api"
	"hop.top/kit/go/transport/api/client"
)

type item struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

func (i item) GetID() string { return i.ID }

type mockService struct {
	mu    sync.RWMutex
	items map[string]item
}

func newMock() *mockService {
	return &mockService{items: make(map[string]item)}
}

func (s *mockService) Create(_ context.Context, e item) (item, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.items[e.ID]; ok {
		return item{}, api.ErrConflict
	}
	s.items[e.ID] = e
	return e, nil
}

func (s *mockService) Get(_ context.Context, id string) (item, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	v, ok := s.items[id]
	if !ok {
		return item{}, api.ErrNotFound
	}
	return v, nil
}

func (s *mockService) List(_ context.Context, _ api.Query) ([]item, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]item, 0, len(s.items))
	for _, v := range s.items {
		out = append(out, v)
	}
	return out, nil
}

func (s *mockService) Update(_ context.Context, e item) (item, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.items[e.ID]; !ok {
		return item{}, api.ErrNotFound
	}
	s.items[e.ID] = e
	return e, nil
}

func (s *mockService) Delete(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.items[id]; !ok {
		return api.ErrNotFound
	}
	delete(s.items, id)
	return nil
}

func setup(t *testing.T) (*client.Client[item], *mockService) {
	t.Helper()
	svc := newMock()
	handler := api.ResourceRouter[item](svc)
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return client.New[item](srv.URL), svc
}

func TestCreate(t *testing.T) {
	c, _ := setup(t)
	got, err := c.Create(context.Background(), item{ID: "1", Name: "alpha"})
	require.NoError(t, err)
	assert.Equal(t, "1", got.ID)
	assert.Equal(t, "alpha", got.Name)
}

func TestGet(t *testing.T) {
	c, svc := setup(t)
	svc.items["2"] = item{ID: "2", Name: "beta"}

	got, err := c.Get(context.Background(), "2")
	require.NoError(t, err)
	assert.Equal(t, "beta", got.Name)
}

func TestList(t *testing.T) {
	c, svc := setup(t)
	svc.items["a"] = item{ID: "a", Name: "A"}
	svc.items["b"] = item{ID: "b", Name: "B"}

	items, err := c.List(context.Background(), api.Query{Limit: 10})
	require.NoError(t, err)
	assert.Len(t, items, 2)
}

func TestUpdate(t *testing.T) {
	c, svc := setup(t)
	svc.items["3"] = item{ID: "3", Name: "old"}

	got, err := c.Update(context.Background(), item{ID: "3", Name: "new"})
	require.NoError(t, err)
	assert.Equal(t, "new", got.Name)
}

func TestDelete(t *testing.T) {
	c, svc := setup(t)
	svc.items["4"] = item{ID: "4", Name: "doomed"}

	err := c.Delete(context.Background(), "4")
	require.NoError(t, err)

	_, err = c.Get(context.Background(), "4")
	require.Error(t, err)
}

func TestErrorResponse(t *testing.T) {
	c, _ := setup(t)
	_, err := c.Get(context.Background(), "missing")
	require.Error(t, err)

	apiErr, ok := err.(*api.APIError)
	require.True(t, ok)
	assert.Equal(t, 404, apiErr.Status)
	assert.Equal(t, "not_found", apiErr.Code)
}

func TestWithAuth(t *testing.T) {
	svc := newMock()
	var gotAuth string
	handler := api.ResourceRouter[item](svc)
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		handler.ServeHTTP(w, r)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	c := client.New[item](srv.URL, client.WithAuth("tok123"))
	svc.items["x"] = item{ID: "x", Name: "X"}
	_, _ = c.Get(context.Background(), "x")
	assert.Equal(t, "Bearer tok123", gotAuth)
}
