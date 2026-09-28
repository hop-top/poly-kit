package api_test

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/transport/api"
)

func TestServerTimeoutsApplyAndRead(t *testing.T) {
	srv := &http.Server{}
	want := api.ServerTimeouts{ReadHeader: time.Second, Read: 2 * time.Second, Write: 3 * time.Second, Idle: 4 * time.Second}
	want.Apply(srv)
	assert.Equal(t, time.Second, srv.ReadHeaderTimeout)
	assert.Equal(t, 2*time.Second, srv.ReadTimeout)
	assert.Equal(t, 3*time.Second, srv.WriteTimeout)
	assert.Equal(t, 4*time.Second, srv.IdleTimeout)
	assert.Equal(t, want, api.ServerTimeoutsOf(srv))

	assert.Equal(t, api.ServerTimeouts{
		ReadHeader: 5 * time.Second, Read: 5 * time.Second, Write: 10 * time.Second,
	}, api.DefaultServerTimeouts())
}

// TestLiftWriteDeadline pins that a lifted route writes past the
// server's write timeout, and an ordinary route does not.
func TestLiftWriteDeadline(t *testing.T) {
	slow := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(300 * time.Millisecond)
		_, _ = fmt.Fprint(w, "late")
	})
	mux := http.NewServeMux()
	mux.Handle("/lifted", api.LiftWriteDeadline(slow))
	mux.Handle("/plain", slow)
	srv := httptest.NewUnstartedServer(mux)
	srv.Config.WriteTimeout = 100 * time.Millisecond
	srv.Start()
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/lifted")
	require.NoError(t, err)
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	assert.Equal(t, "late", string(body))

	resp, err = http.Get(srv.URL + "/plain")
	if err == nil {
		body, err = io.ReadAll(resp.Body)
		_ = resp.Body.Close()
	}
	assert.True(t, err != nil || string(body) != "late", "the plain route kept the write deadline")
}

func TestMapErrorDeadlineIs504(t *testing.T) {
	ae := api.MapError(fmt.Errorf("run: %w", context.DeadlineExceeded))
	assert.Equal(t, http.StatusGatewayTimeout, ae.Status)
	assert.Equal(t, api.CodeDeadlineExceeded, ae.Code)
}
