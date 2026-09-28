package socket_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/transport/cmdsurface"
	"hop.top/kit/go/transport/socket"
	"hop.top/kit/go/transport/transportsvc"
)

// A call the bridge's rate limit refuses is RATE_LIMITED on the wire,
// with retry_after_ms: how long until the caller's bucket refills.
func TestRateLimitedCallCarriesRetryAfter(t *testing.T) {
	t.Parallel()
	path := socketPath(t)
	startSocket(t, path, transportsvc.WithBridgeOptions(cmdsurface.WithRateLimit(cmdsurface.RateLimit{
		Write: cmdsurface.RateRule{PerMinute: 1, Burst: 1}, // ping is unannotated: the write tier
	})))

	first := call(t, path, socket.Request{Path: []string{"ping"}, Caller: "alice"})
	require.True(t, first.Ok, "the burst admits one call: %+v", first.Error)

	resp := call(t, path, socket.Request{Path: []string{"ping"}, Caller: "alice"})
	require.False(t, resp.Ok)
	require.NotNil(t, resp.Error)
	assert.Equal(t, socket.CodeRateLimited, resp.Error.Code)
	assert.Contains(t, resp.Error.Message, "rate limited")
	// One token a minute, just spent: the wait is just under a minute.
	assert.LessOrEqual(t, resp.Error.RetryAfterMs, int64(60_000))
	assert.Greater(t, resp.Error.RetryAfterMs, int64(55_000))

	// The owner-only socket vouches for the connection, not the name
	// a request claims: every caller on it is the owner, and shares
	// the owner's bucket.
	other := call(t, path, socket.Request{Path: []string{"ping"}, Caller: "bob"})
	require.False(t, other.Ok)
	assert.Equal(t, socket.CodeRateLimited, other.Error.Code, "a claimed name buys no fresh budget")
}

// retry_after_ms is on the wire only for a retryable refusal.
func TestRetryAfterIsOmittedOtherwise(t *testing.T) {
	t.Parallel()
	raw, err := json.Marshal(socket.Error{Code: socket.CodeDenied, Message: "no"})
	require.NoError(t, err)
	assert.False(t, strings.Contains(string(raw), "retry_after_ms"), string(raw))
}
