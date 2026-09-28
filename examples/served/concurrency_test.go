package main

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// getCode issues a GET and returns its status, Retry-After, content
// type and error code.
func getCode(t *testing.T, url string) (status int, retryAfter, contentType, code string) {
	t.Helper()
	resp, err := http.Get(url)
	require.NoError(t, err)
	defer resp.Body.Close()
	var body struct {
		Code string `json:"code"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&body)
	return resp.StatusCode, resp.Header.Get("Retry-After"), resp.Header.Get("Content-Type"), body.Code
}

// services.<svc>.concurrency is on without being named. The fixture
// serves one shared tree, so one call runs at a time; with no queue a
// call arriving while another runs is 503 overloaded, a stream
// included, before the stream opens.
func TestBinaryServicesConcurrency(t *testing.T) {
	r := runBinary(t, newHome(t, ""), []string{"SERVED_SERVICES_API_CONCURRENCY_MAX_QUEUE=0"},
		"serve", "api", "--addr", "127.0.0.1:0")
	require.True(t, r.ready, r.output())

	// An unbounded watch holds the one slot until its client leaves.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		r.apiURL+"/v1/commands/item/watch/stream?interval=10ms", nil)
	require.NoError(t, err)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	_, err = bufio.NewReader(resp.Body).ReadString('\n')
	require.NoError(t, err)

	status, retryAfter, _, code := getCode(t, r.apiURL+"/v1/commands/item/list")
	assert.Equal(t, http.StatusServiceUnavailable, status)
	assert.Equal(t, "1", retryAfter)
	assert.Equal(t, "overloaded", code)

	status, retryAfter, contentType, code := getCode(t, r.apiURL+"/v1/commands/item/watch/stream?count=1")
	assert.Equal(t, http.StatusServiceUnavailable, status, "refused before the stream opens")
	assert.Equal(t, "1", retryAfter)
	assert.Contains(t, contentType, "application/json")
	assert.Equal(t, "overloaded", code)

	cancel()
	_ = resp.Body.Close()
	assert.Eventually(t, func() bool {
		status, _, _, _ := getCode(t, r.apiURL+"/v1/commands/item/list")
		return status == http.StatusOK
	}, 5*time.Second, 20*time.Millisecond, "the slot frees when the watch's client leaves")
}

func TestBinaryServicesConcurrencyRefusesBadConfiguration(t *testing.T) {
	for name, c := range map[string]struct {
		env  []string
		want string
	}{
		"misspelled key": {
			[]string{"SERVED_SERVICES_API_CONCURRENCY_MAX_INFLIGHTS=5"},
			`services.api.concurrency.max_inflights: unknown key "max_inflights"`,
		},
		"zero in flight": {
			[]string{"SERVED_SERVICES_ALL_CONCURRENCY_MAX_INFLIGHT=0"},
			"services.all.concurrency.max_inflight: 0 is out of range",
		},
	} {
		t.Run(name, func(t *testing.T) {
			r := runBinary(t, newHome(t, ""), c.env, "serve", "api", "--addr", "127.0.0.1:0")
			require.False(t, r.ready, "served with a bad key\n%s", r.output())
			assert.Equal(t, 2, r.exitCode(), r.output())
			assert.Contains(t, r.output(), c.want)
		})
	}
}
