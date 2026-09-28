package api_test

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"net/http/httptest"

	"github.com/coder/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/transport/api"
)

// guarded wraps h the way the api service does: headers outermost so
// refusals carry them, then Host, then Origin.
func guarded(t *testing.T, h http.Handler) http.Handler {
	t.Helper()
	origin, err := api.OriginCheck(api.OriginCheckConfig{})
	require.NoError(t, err)
	return api.Chain(
		api.SecurityHeaders(api.SecurityHeadersConfig{}),
		api.HostCheck(api.HostCheckConfig{Allow: api.LoopbackHosts}),
		origin,
	)(h)
}

func TestGuards_RefusalCarriesSecurityHeaders(t *testing.T) {
	h := guarded(t, okHandler)
	rec := serveWithHost(h, http.MethodGet, "attacker.example:8080")
	assert.Equal(t, http.StatusForbidden, rec.Code)
	assert.Equal(t, "nosniff", rec.Header().Get("X-Content-Type-Options"))
	assert.Equal(t, api.DefaultContentSecurityPolicy, rec.Header().Get("Content-Security-Policy"))
}

func TestGuards_SSEStillStreams(t *testing.T) {
	// The handler emits one event, then blocks until the client has
	// seen it. A guard that buffered the response, or hid
	// http.Flusher, would deadlock this test into its timeout.
	seen := make(chan struct{})
	r := api.NewRouter()
	r.Handle(http.MethodGet, "/events", func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fl, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "no flusher", http.StatusInternalServerError)
			return
		}
		_, _ = fmt.Fprint(w, "data: one\n\n")
		fl.Flush()
		select {
		case <-seen:
		case <-req.Context().Done():
			return
		}
		_, _ = fmt.Fprint(w, "data: two\n\n")
		fl.Flush()
	})

	srv := httptest.NewServer(guarded(t, r))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/events", nil)
	req.Header.Set("Origin", "https://attacker.example") // GET: not state-changing
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "text/event-stream", resp.Header.Get("Content-Type"))

	sc := bufio.NewScanner(resp.Body)
	require.True(t, sc.Scan())
	assert.Equal(t, "data: one", sc.Text())
	close(seen)
	var rest []string
	for sc.Scan() {
		if line := strings.TrimSpace(sc.Text()); line != "" {
			rest = append(rest, line)
		}
	}
	assert.Equal(t, []string{"data: two"}, rest)
}

func TestGuards_WebSocketUpgradeStillWorks(t *testing.T) {
	hub := api.NewHub()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go hub.Run(ctx)

	r := api.NewRouter()
	r.Handle(http.MethodGet, "/ws", api.WSHandler(hub))

	srv := httptest.NewServer(guarded(t, r))
	defer srv.Close()

	dialCtx, dialCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer dialCancel()
	c, resp, err := websocket.Dial(dialCtx, "ws"+strings.TrimPrefix(srv.URL, "http")+"/ws", nil)
	require.NoError(t, err)
	defer c.Close(websocket.StatusNormalClosure, "")
	assert.Equal(t, http.StatusSwitchingProtocols, resp.StatusCode)

	_, data, err := c.Read(dialCtx)
	require.NoError(t, err)
	var msg api.WSMessage
	require.NoError(t, json.Unmarshal(data, &msg))
	assert.Equal(t, "welcome", msg.Type)
}
