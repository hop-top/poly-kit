package cli_test

import (
	"testing"
	"time"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/console/cli"
	"hop.top/kit/go/transport/socket"
)

// TestServeSocketCommandDeadline pins that the socket surface arms
// services.socket.timeouts.command and answers a call it cut short
// with DEADLINE_EXCEEDED.
func TestServeSocketCommandDeadline(t *testing.T) {
	path := shortSocketPath(t)
	r := socketRoot(t, cli.SocketConfig{Path: path})
	r.Cmd.AddCommand(&cobra.Command{
		Use: "hang",
		RunE: func(cmd *cobra.Command, _ []string) error {
			<-cmd.Context().Done()
			return cmd.Context().Err()
		},
	})
	r.Viper.Set("services.socket.timeouts.command", "100ms")
	stop := serveInBackground(t, r, []string{"serve", "socket"}, path)
	defer stop()

	start := time.Now()
	resp := callSocket(t, path, socket.Request{Path: []string{"hang"}})
	require.False(t, resp.Ok)
	require.NotNil(t, resp.Error)
	assert.Equal(t, socket.CodeDeadlineExceeded, resp.Error.Code)
	assert.Contains(t, resp.Error.Message, "hang ran past its 100ms deadline")
	assert.Less(t, time.Since(start), 5*time.Second)

	resp = callSocket(t, path, socket.Request{Path: []string{"ping"}})
	require.True(t, resp.Ok, "a quick call is untouched: %+v", resp.Error)
}
