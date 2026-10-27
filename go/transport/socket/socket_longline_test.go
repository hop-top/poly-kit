package socket_test

import (
	"encoding/json"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/transport/socket"
)

// TestRequestLineTooLongIsAnswered checks that a request line past the
// line bound is answered with an INVALID error, after the responses
// to the requests read before it, rather than the connection closing
// without a word.
func TestRequestLineTooLongIsAnswered(t *testing.T) {
	t.Parallel()
	path := socketPath(t)
	startSocket(t, path)

	conn, err := net.Dial("unix", path)
	require.NoError(t, err)
	defer func() { _ = conn.Close() }()
	require.NoError(t, conn.SetDeadline(time.Now().Add(10*time.Second)))

	ping, err := json.Marshal(socket.Request{Path: []string{"ping"}})
	require.NoError(t, err)
	big, err := json.Marshal(socket.Request{Path: []string{"ping"}, Args: []string{strings.Repeat("a", 2<<20)}})
	require.NoError(t, err)

	// The server stops reading at the bound, so the rest of the
	// write may fail; only the answers matter.
	go func() {
		_, _ = conn.Write(append(append(ping, '\n'), append(big, '\n')...))
	}()

	dec := json.NewDecoder(conn)
	var first, second socket.Response
	require.NoError(t, dec.Decode(&first))
	assert.True(t, first.Ok, "request before the long line: %+v", first.Error)
	require.NoError(t, dec.Decode(&second), "long line must be answered")
	assert.False(t, second.Ok)
	require.NotNil(t, second.Error)
	assert.Equal(t, socket.CodeInvalid, second.Error.Code)
	assert.Contains(t, second.Error.Message, "exceeds")
}
