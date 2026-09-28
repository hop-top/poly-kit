package main

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// rateLimitEnv sets a small read limit through the environment and the
// opt-ins a remote bind needs; it never names enabled.
var rateLimitEnv = []string{
	"SERVED_SERVICES_API_INSECURE_REMOTE=true",
	"SERVED_SERVICES_API_INSECURE_NO_POLICY=true",
	"SERVED_SERVICES_API_RATE_LIMIT_READ_PER_MINUTE=1",
	"SERVED_SERVICES_ALL_RATE_LIMIT_READ_BURST=2",
}

// readThrice issues three reads and returns their statuses and the
// last response's Retry-After and error code.
func readThrice(t *testing.T, apiURL string) (statuses []int, retryAfter, code string) {
	t.Helper()
	url := strings.Replace(apiURL, "0.0.0.0", "127.0.0.1", 1) + "/v1/commands/item/list"
	for range 3 {
		resp, err := http.Get(url)
		require.NoError(t, err)
		var body struct {
			Code string `json:"code"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&body)
		_ = resp.Body.Close()
		statuses = append(statuses, resp.StatusCode)
		retryAfter, code = resp.Header.Get("Retry-After"), body.Code
	}
	return statuses, retryAfter, code
}

// services.<svc>.rate_limit is on beyond loopback without being named,
// off on loopback until enabled, and answers 429 with Retry-After.
func TestBinaryServicesRateLimit(t *testing.T) {
	t.Run("beyond loopback: on by default", func(t *testing.T) {
		r := runBinary(t, newHome(t, ""), rateLimitEnv, "serve", "api", "--addr", "0.0.0.0:0")
		require.True(t, r.ready, r.output())
		statuses, retryAfter, code := readThrice(t, r.apiURL)
		assert.Equal(t, []int{200, 200, http.StatusTooManyRequests}, statuses)
		assert.Equal(t, "60", retryAfter)
		assert.Equal(t, "rate_limited", code)
	})
	t.Run("loopback: off by default", func(t *testing.T) {
		r := runBinary(t, newHome(t, ""), rateLimitEnv, "serve", "api", "--addr", "127.0.0.1:0")
		require.True(t, r.ready, r.output())
		statuses, _, _ := readThrice(t, r.apiURL)
		assert.Equal(t, []int{200, 200, 200}, statuses)
	})
	t.Run("loopback: enabled from the config file", func(t *testing.T) {
		home := newHome(t, "services:\n  all:\n    rate_limit:\n      enabled: true\n")
		r := runBinary(t, home, rateLimitEnv, "serve", "api", "--addr", "127.0.0.1:0")
		require.True(t, r.ready, r.output())
		statuses, retryAfter, _ := readThrice(t, r.apiURL)
		assert.Equal(t, []int{200, 200, http.StatusTooManyRequests}, statuses)
		assert.Equal(t, "60", retryAfter)
	})
	t.Run("beyond loopback: switched off with -c", func(t *testing.T) {
		r := runBinary(t, newHome(t, ""), rateLimitEnv,
			"-c", "services.api.rate_limit.enabled=false", "serve", "api", "--addr", "0.0.0.0:0")
		require.True(t, r.ready, r.output())
		statuses, _, _ := readThrice(t, r.apiURL)
		assert.Equal(t, []int{200, 200, 200}, statuses)
	})
}

func TestBinaryServicesRateLimitRefusesBadConfiguration(t *testing.T) {
	for name, c := range map[string]struct {
		env  []string
		want string
	}{
		"misspelled tier key": {
			[]string{"SERVED_SERVICES_API_RATE_LIMIT_READ_BRUST=5"},
			`services.api.rate_limit.read.brust: unknown key "brust"`,
		},
		"zero rate": {
			[]string{"SERVED_SERVICES_ALL_RATE_LIMIT_WRITE_PER_MINUTE=0"},
			"services.all.rate_limit.write.per_minute: 0 is out of range",
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
