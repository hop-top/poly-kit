package api_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/transport/api"
)

func TestE2E_WS_MountOnRouter(t *testing.T) {
	hub := api.NewHub()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go hub.Run(ctx)

	r := api.NewRouter()
	r.Mount("/ws", api.WSHandler(hub))

	srv := httptest.NewServer(r)
	defer srv.Close()

	wsURL := "ws" + srv.URL[len("http"):] + "/ws"
	dialCtx, dialCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer dialCancel()

	c, _, err := websocket.Dial(dialCtx, wsURL, nil)
	require.NoError(t, err)
	defer c.Close(websocket.StatusNormalClosure, "")

	// should receive welcome
	_, data, err := c.Read(dialCtx)
	require.NoError(t, err)

	var msg api.WSMessage
	require.NoError(t, json.Unmarshal(data, &msg))
	assert.Equal(t, "welcome", msg.Type)
}

func TestE2E_WS_AuthMiddlewareBeforeUpgrade(t *testing.T) {
	hub := api.NewHub()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go hub.Run(ctx)

	authMW := api.Auth(func(r *http.Request) (any, error) {
		if r.Header.Get("Authorization") != "Bearer ws-token" {
			return nil, errors.New("unauthorized")
		}
		return "ok", nil
	})

	r := api.NewRouter()
	ws := r.Group("/ws", authMW)
	ws.Mount("", api.WSHandler(hub))

	srv := httptest.NewServer(r)
	defer srv.Close()

	wsURL := "ws" + srv.URL[len("http"):] + "/ws"

	t.Run("rejected without token", func(t *testing.T) {
		dialCtx, dialCancel := context.WithTimeout(
			context.Background(), 2*time.Second,
		)
		defer dialCancel()

		_, resp, err := websocket.Dial(dialCtx, wsURL, nil)
		// dial should fail — server returns 401 before upgrade
		require.Error(t, err)
		if resp != nil {
			assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
		}
	})

	t.Run("accepted with valid token", func(t *testing.T) {
		dialCtx, dialCancel := context.WithTimeout(
			context.Background(), 5*time.Second,
		)
		defer dialCancel()

		c, _, err := websocket.Dial(dialCtx, wsURL, &websocket.DialOptions{
			HTTPHeader: http.Header{
				"Authorization": []string{"Bearer ws-token"},
			},
		})
		require.NoError(t, err)
		defer c.Close(websocket.StatusNormalClosure, "")

		_, data, err := c.Read(dialCtx)
		require.NoError(t, err)

		var msg api.WSMessage
		require.NoError(t, json.Unmarshal(data, &msg))
		assert.Equal(t, "welcome", msg.Type)
	})
}
