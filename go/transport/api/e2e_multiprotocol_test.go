package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/transport/api"
	restclient "hop.top/kit/go/transport/api/client"
)

type testWidget struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Color string `json:"color"`
}

func (w testWidget) GetID() string { return w.ID }

type multiSvc struct {
	mu    sync.RWMutex
	items map[string]testWidget
}

func newMultiSvc() *multiSvc {
	return &multiSvc{items: make(map[string]testWidget)}
}

func (s *multiSvc) Create(_ context.Context, e testWidget) (testWidget, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.items[e.ID]; ok {
		return testWidget{}, api.ErrConflict
	}
	s.items[e.ID] = e
	return e, nil
}

func (s *multiSvc) Get(_ context.Context, id string) (testWidget, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	e, ok := s.items[id]
	if !ok {
		return testWidget{}, api.ErrNotFound
	}
	return e, nil
}

func (s *multiSvc) List(_ context.Context, _ api.Query) ([]testWidget, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]testWidget, 0, len(s.items))
	for _, v := range s.items {
		out = append(out, v)
	}
	return out, nil
}

func (s *multiSvc) Update(_ context.Context, e testWidget) (testWidget, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.items[e.ID]; !ok {
		return testWidget{}, api.ErrNotFound
	}
	s.items[e.ID] = e
	return e, nil
}

func (s *multiSvc) Delete(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.items[id]; !ok {
		return api.ErrNotFound
	}
	delete(s.items, id)
	return nil
}

func setupMultiProtocolServer(
	t *testing.T,
) (*multiSvc, *httptest.Server, *api.Hub) {
	t.Helper()

	svc := newMultiSvc()
	hub := api.NewHub()

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go hub.Run(ctx)

	r := api.NewRouter(api.WithOpenAPI(api.OpenAPIConfig{
		Title:   "Multi-Protocol Test",
		Version: "0.1.0",
	}))

	humaAPI := api.HumaAPI(r)
	handler := api.ResourceRouter[testWidget](svc,
		api.WithPrefix[testWidget]("/widgets"),
		api.WithHumaAPI[testWidget](humaAPI, "/api"),
	)
	r.Mount("/api/widgets", handler)
	r.Mount("/ws", api.WSHandler(hub))

	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)

	return svc, srv, hub
}

func TestE2E_MultiProtocol_REST_CreateAndGet(t *testing.T) {
	_, srv, _ := setupMultiProtocolServer(t)
	c := restclient.New[testWidget](srv.URL+"/api/widgets",
		restclient.WithHTTPClient(srv.Client()),
	)
	ctx := context.Background()

	created, err := c.Create(ctx, testWidget{
		ID: "w1", Name: "Sprocket", Color: "red",
	})
	require.NoError(t, err)
	assert.Equal(t, "w1", created.ID)
	assert.Equal(t, "Sprocket", created.Name)
	assert.Equal(t, "red", created.Color)

	got, err := c.Get(ctx, "w1")
	require.NoError(t, err)
	assert.Equal(t, created, got)
}

func TestE2E_MultiProtocol_REST_CreateAndList(t *testing.T) {
	_, srv, _ := setupMultiProtocolServer(t)
	c := restclient.New[testWidget](srv.URL+"/api/widgets",
		restclient.WithHTTPClient(srv.Client()),
	)
	ctx := context.Background()

	for i, w := range []testWidget{
		{ID: "a", Name: "Alpha", Color: "blue"},
		{ID: "b", Name: "Beta", Color: "green"},
		{ID: "c", Name: "Gamma", Color: "yellow"},
	} {
		_, err := c.Create(ctx, w)
		require.NoError(t, err, "create widget %d", i)
	}

	items, err := c.List(ctx, api.Query{Limit: 10})
	require.NoError(t, err)
	assert.Len(t, items, 3)
}

func TestE2E_MultiProtocol_WS_SubscribeAndReceive(t *testing.T) {
	_, srv, hub := setupMultiProtocolServer(t)

	wsURL := "ws" + srv.URL[len("http"):] + "/ws"
	ctx, cancel := context.WithTimeout(
		context.Background(), 5*time.Second,
	)
	defer cancel()

	ws, err := restclient.DialWS(ctx, wsURL)
	require.NoError(t, err)
	defer ws.Close()

	received := make(chan api.WSMessage, 4)
	ws.OnMessage(func(msg api.WSMessage) {
		received <- msg
	})
	go ws.Listen(ctx)

	// drain welcome
	select {
	case msg := <-received:
		assert.Equal(t, "welcome", msg.Type)
	case <-ctx.Done():
		t.Fatal("timeout waiting for welcome")
	}

	err = ws.Subscribe(ctx, "test.*")
	require.NoError(t, err)

	// drain ack
	select {
	case msg := <-received:
		assert.Equal(t, "ack", msg.Type)
	case <-ctx.Done():
		t.Fatal("timeout waiting for ack")
	}

	payload := map[string]string{"action": "created", "id": "w1"}
	err = hub.Publish("test.created", payload)
	require.NoError(t, err)

	select {
	case msg := <-received:
		assert.Equal(t, "message", msg.Type)
		assert.Equal(t, "test.created", msg.Topic)

		var p map[string]string
		require.NoError(t, json.Unmarshal(msg.Payload, &p))
		assert.Equal(t, "w1", p["id"])
	case <-ctx.Done():
		t.Fatal("timeout waiting for broadcast")
	}
}

func TestE2E_MultiProtocol_OpenAPI_Accessible(t *testing.T) {
	_, srv, _ := setupMultiProtocolServer(t)

	resp, err := srv.Client().Get(srv.URL + "/openapi.json")
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	ct := resp.Header.Get("Content-Type")
	assert.True(t,
		strings.Contains(ct, "application/json") ||
			strings.Contains(ct, "application/openapi+json"),
		"expected JSON content type, got: %s", ct,
	)

	var spec map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&spec))
	assert.Equal(t, "3.1.0", spec["openapi"])

	info, ok := spec["info"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "Multi-Protocol Test", info["title"])
}

func TestE2E_MultiProtocol_REST_DeleteThenNotFound(t *testing.T) {
	_, srv, _ := setupMultiProtocolServer(t)
	c := restclient.New[testWidget](srv.URL+"/api/widgets",
		restclient.WithHTTPClient(srv.Client()),
	)
	ctx := context.Background()

	_, err := c.Create(ctx, testWidget{
		ID: "del1", Name: "Doomed", Color: "black",
	})
	require.NoError(t, err)

	err = c.Delete(ctx, "del1")
	require.NoError(t, err)

	_, err = c.Get(ctx, "del1")
	require.Error(t, err)

	var ae *api.APIError
	require.ErrorAs(t, err, &ae)
	assert.Equal(t, http.StatusNotFound, ae.Status)
}
