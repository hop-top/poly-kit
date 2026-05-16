package api_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"hop.top/kit/go/transport/api"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- mock entity ---

type widget struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

func (w widget) GetID() string { return w.ID }

// --- mock service ---

type mockService struct {
	items   map[string]widget
	createF func(ctx context.Context, e widget) (widget, error)
}

func newMockService() *mockService {
	return &mockService{items: make(map[string]widget)}
}

func (m *mockService) Create(ctx context.Context, e widget) (widget, error) {
	if m.createF != nil {
		return m.createF(ctx, e)
	}
	e.ID = "new-1"
	m.items[e.ID] = e
	return e, nil
}

func (m *mockService) Get(_ context.Context, id string) (widget, error) {
	w, ok := m.items[id]
	if !ok {
		return w, api.ErrNotFound
	}
	return w, nil
}

func (m *mockService) List(_ context.Context, q api.Query) ([]widget, error) {
	out := make([]widget, 0, len(m.items))
	for _, w := range m.items {
		out = append(out, w)
	}
	// Apply limit.
	if q.Limit > 0 && q.Limit < len(out) {
		out = out[:q.Limit]
	}
	return out, nil
}

func (m *mockService) Update(_ context.Context, e widget) (widget, error) {
	if _, ok := m.items[e.ID]; !ok {
		return e, api.ErrNotFound
	}
	m.items[e.ID] = e
	return e, nil
}

func (m *mockService) Delete(_ context.Context, id string) error {
	if _, ok := m.items[id]; !ok {
		return api.ErrNotFound
	}
	delete(m.items, id)
	return nil
}

// --- tests ---

func TestResourceRouter_Create(t *testing.T) {
	svc := newMockService()
	h := api.ResourceRouter[widget](svc)

	body := `{"name":"bolt"}`
	req := httptest.NewRequest("POST", "/", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusCreated, rec.Code)
	var got widget
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&got))
	assert.Equal(t, "new-1", got.ID)
	assert.Equal(t, "bolt", got.Name)
}

func TestResourceRouter_Get(t *testing.T) {
	svc := newMockService()
	svc.items["42"] = widget{ID: "42", Name: "gear"}
	h := api.ResourceRouter[widget](svc)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/42", nil)
	h.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	var got widget
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&got))
	assert.Equal(t, "gear", got.Name)
}

func TestResourceRouter_Get_NotFound(t *testing.T) {
	svc := newMockService()
	h := api.ResourceRouter[widget](svc)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/999", nil)
	h.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestResourceRouter_List(t *testing.T) {
	svc := newMockService()
	svc.items["1"] = widget{ID: "1", Name: "a"}
	svc.items["2"] = widget{ID: "2", Name: "b"}
	h := api.ResourceRouter[widget](svc)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)
	h.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	var got []widget
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&got))
	assert.Len(t, got, 2)
}

func TestResourceRouter_Update(t *testing.T) {
	svc := newMockService()
	svc.items["1"] = widget{ID: "1", Name: "old"}
	h := api.ResourceRouter[widget](svc)

	body := `{"id":"1","name":"new"}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("PUT", "/1", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	h.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	var got widget
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&got))
	assert.Equal(t, "new", got.Name)
}

func TestResourceRouter_Update_IDMismatch(t *testing.T) {
	svc := newMockService()
	svc.items["1"] = widget{ID: "1", Name: "old"}
	h := api.ResourceRouter[widget](svc)

	body := `{"id":"999","name":"new"}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("PUT", "/1", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	h.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)

	var got api.APIError
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&got))
	assert.Equal(t, "id_mismatch", got.Code)
}

func TestResourceRouter_Delete(t *testing.T) {
	svc := newMockService()
	svc.items["1"] = widget{ID: "1", Name: "doomed"}
	h := api.ResourceRouter[widget](svc)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("DELETE", "/1", nil)
	h.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusNoContent, rec.Code)
	assert.Empty(t, svc.items)
}

func TestResourceRouter_Delete_NotFound(t *testing.T) {
	svc := newMockService()
	h := api.ResourceRouter[widget](svc)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("DELETE", "/999", nil)
	h.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestResourceRouter_WithPrefix(t *testing.T) {
	svc := newMockService()
	svc.items["1"] = widget{ID: "1", Name: "prefixed"}
	h := api.ResourceRouter[widget](svc, api.WithPrefix[widget]("/widgets"))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/widgets/1", nil)
	h.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestResourceRouter_WithRouteFilter(t *testing.T) {
	svc := newMockService()
	svc.items["1"] = widget{ID: "1", Name: "x"}
	h := api.ResourceRouter[widget](svc, api.WithRouteFilter[widget]("list", "get"))

	// GET / (list) should work.
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
	assert.Equal(t, http.StatusOK, rec.Code)

	// POST / (create) should 405.
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("POST", "/", strings.NewReader(`{}`)))
	assert.NotEqual(t, http.StatusCreated, rec.Code)
}

func TestResourceRouter_WithQueryParser(t *testing.T) {
	svc := newMockService()
	svc.items["1"] = widget{ID: "1", Name: "a"}
	svc.items["2"] = widget{ID: "2", Name: "b"}
	svc.items["3"] = widget{ID: "3", Name: "c"}

	parser := func(r *http.Request) api.Query {
		return api.Query{Limit: 1}
	}
	h := api.ResourceRouter[widget](svc, api.WithQueryParser[widget](parser))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)
	h.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	var got []widget
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&got))
	assert.Len(t, got, 1)
}

func TestResourceRouter_WithSerializer(t *testing.T) {
	svc := newMockService()
	called := false
	s := api.Serializer[widget]{
		Encode: func(w http.ResponseWriter, status int, v widget) {
			called = true
			api.JSON(w, status, map[string]string{"custom": v.Name})
		},
	}
	h := api.ResourceRouter[widget](svc, api.WithSerializer[widget](s))

	body := `{"name":"ser"}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	h.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusCreated, rec.Code)
	assert.True(t, called)
}

func TestResourceRouter_CreateValidationError(t *testing.T) {
	svc := newMockService()
	svc.createF = func(_ context.Context, _ widget) (widget, error) {
		return widget{}, fmt.Errorf("bad name: %w", api.ErrValidation)
	}
	h := api.ResourceRouter[widget](svc)

	body := `{"name":""}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	h.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code)
}

func TestDefaultQueryParser(t *testing.T) {
	req := httptest.NewRequest("GET", "/?limit=5&offset=10&sort=name&search=foo", nil)
	q := api.DefaultQueryParser(req)

	assert.Equal(t, 5, q.Limit)
	assert.Equal(t, 10, q.Offset)
	assert.Equal(t, "name", q.Sort)
	assert.Equal(t, "foo", q.Search)
}

func TestDefaultQueryParser_Defaults(t *testing.T) {
	req := httptest.NewRequest("GET", "/", nil)
	q := api.DefaultQueryParser(req)

	assert.Equal(t, 20, q.Limit)
	assert.Equal(t, 0, q.Offset)
}
