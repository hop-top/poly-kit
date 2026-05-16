package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/transport/api"
)

// e2eMockService is an in-memory Service[e2eItem] for testing.
type e2eMockService struct {
	mu    sync.RWMutex
	items map[string]e2eItem
}

func newE2EMockService() *e2eMockService {
	return &e2eMockService{items: make(map[string]e2eItem)}
}

func (s *e2eMockService) Create(_ context.Context, entity e2eItem) (e2eItem, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.items[entity.GetID()]; exists {
		return e2eItem{}, api.ErrConflict
	}
	s.items[entity.GetID()] = entity
	return entity, nil
}

func (s *e2eMockService) Get(_ context.Context, id string) (e2eItem, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	item, ok := s.items[id]
	if !ok {
		return e2eItem{}, api.ErrNotFound
	}
	return item, nil
}

func (s *e2eMockService) List(_ context.Context, _ api.Query) ([]e2eItem, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]e2eItem, 0, len(s.items))
	for _, v := range s.items {
		out = append(out, v)
	}
	return out, nil
}

func (s *e2eMockService) Update(_ context.Context, entity e2eItem) (e2eItem, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.items[entity.GetID()]; !exists {
		return e2eItem{}, api.ErrNotFound
	}
	s.items[entity.GetID()] = entity
	return entity, nil
}

func (s *e2eMockService) Delete(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.items[id]; !exists {
		return api.ErrNotFound
	}
	delete(s.items, id)
	return nil
}

func TestE2E_Resource_CRUD(t *testing.T) {
	svc := newE2EMockService()
	handler := api.ResourceRouter[e2eItem](svc)
	srv := httptest.NewServer(handler)
	defer srv.Close()

	t.Run("POST creates item 201", func(t *testing.T) {
		resp, err := http.Post(srv.URL+"/",
			"application/json",
			strings.NewReader(`{"id":"1","name":"alpha"}`))
		require.NoError(t, err)
		defer resp.Body.Close()

		assert.Equal(t, http.StatusCreated, resp.StatusCode)

		var item e2eItem
		require.NoError(t, json.NewDecoder(resp.Body).Decode(&item))
		assert.Equal(t, "1", item.ID)
		assert.Equal(t, "alpha", item.Name)
	})

	t.Run("GET list returns 200 with array", func(t *testing.T) {
		resp, err := http.Get(srv.URL + "/")
		require.NoError(t, err)
		defer resp.Body.Close()

		assert.Equal(t, http.StatusOK, resp.StatusCode)

		var items []e2eItem
		require.NoError(t, json.NewDecoder(resp.Body).Decode(&items))
		assert.Len(t, items, 1)
	})

	t.Run("GET by id returns 200", func(t *testing.T) {
		resp, err := http.Get(srv.URL + "/1")
		require.NoError(t, err)
		defer resp.Body.Close()

		assert.Equal(t, http.StatusOK, resp.StatusCode)

		var item e2eItem
		require.NoError(t, json.NewDecoder(resp.Body).Decode(&item))
		assert.Equal(t, "1", item.ID)
		assert.Equal(t, "alpha", item.Name)
	})

	t.Run("PUT updates item 200", func(t *testing.T) {
		req, _ := http.NewRequest("PUT", srv.URL+"/1",
			strings.NewReader(`{"id":"1","name":"beta"}`))
		req.Header.Set("Content-Type", "application/json")

		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()

		assert.Equal(t, http.StatusOK, resp.StatusCode)

		var item e2eItem
		require.NoError(t, json.NewDecoder(resp.Body).Decode(&item))
		assert.Equal(t, "beta", item.Name)
	})

	t.Run("DELETE returns 204", func(t *testing.T) {
		req, _ := http.NewRequest("DELETE", srv.URL+"/1", nil)
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()

		assert.Equal(t, http.StatusNoContent, resp.StatusCode)
	})
}

func TestE2E_Resource_NotFound(t *testing.T) {
	svc := newE2EMockService()
	handler := api.ResourceRouter[e2eItem](svc)
	srv := httptest.NewServer(handler)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/missing")
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusNotFound, resp.StatusCode)

	var apiErr api.APIError
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&apiErr))
	assert.Equal(t, "not_found", apiErr.Code)
}

func TestE2E_Resource_Conflict(t *testing.T) {
	svc := newE2EMockService()
	handler := api.ResourceRouter[e2eItem](svc)
	srv := httptest.NewServer(handler)
	defer srv.Close()

	body := `{"id":"dup","name":"first"}`
	resp, err := http.Post(srv.URL+"/", "application/json", strings.NewReader(body))
	require.NoError(t, err)
	resp.Body.Close()
	assert.Equal(t, http.StatusCreated, resp.StatusCode)

	// Duplicate
	resp, err = http.Post(srv.URL+"/", "application/json", strings.NewReader(body))
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusConflict, resp.StatusCode)

	var apiErr api.APIError
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&apiErr))
	assert.Equal(t, "conflict", apiErr.Code)
}

func TestE2E_Resource_RouteFilter(t *testing.T) {
	svc := newE2EMockService()
	// Seed data
	svc.items["1"] = e2eItem{ID: "1", Name: "alpha"}

	handler := api.ResourceRouter[e2eItem](svc,
		api.WithRouteFilter[e2eItem]("list", "get"),
	)
	srv := httptest.NewServer(handler)
	defer srv.Close()

	t.Run("GET list allowed", func(t *testing.T) {
		resp, err := http.Get(srv.URL + "/")
		require.NoError(t, err)
		defer resp.Body.Close()
		assert.Equal(t, http.StatusOK, resp.StatusCode)
	})

	t.Run("GET by id allowed", func(t *testing.T) {
		resp, err := http.Get(srv.URL + "/1")
		require.NoError(t, err)
		defer resp.Body.Close()
		assert.Equal(t, http.StatusOK, resp.StatusCode)
	})

	t.Run("POST not registered", func(t *testing.T) {
		resp, err := http.Post(srv.URL+"/", "application/json",
			strings.NewReader(`{"id":"2","name":"beta"}`))
		require.NoError(t, err)
		defer resp.Body.Close()
		// Should be 405 or 404 since route isn't registered
		assert.True(t, resp.StatusCode == http.StatusMethodNotAllowed ||
			resp.StatusCode == http.StatusNotFound,
			"expected 405 or 404, got %d", resp.StatusCode)
	})

	t.Run("DELETE not registered", func(t *testing.T) {
		req, _ := http.NewRequest("DELETE", srv.URL+"/1", nil)
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()
		assert.True(t, resp.StatusCode == http.StatusMethodNotAllowed ||
			resp.StatusCode == http.StatusNotFound,
			"expected 405 or 404, got %d", resp.StatusCode)
	})
}

func TestE2E_Resource_DefaultQueryParser(t *testing.T) {
	req := httptest.NewRequest("GET", "/?limit=5&offset=2&sort=name&search=al", nil)
	q := api.DefaultQueryParser(req)

	assert.Equal(t, 5, q.Limit)
	assert.Equal(t, 2, q.Offset)
	assert.Equal(t, "name", q.Sort)
	assert.Equal(t, "al", q.Search)
}
