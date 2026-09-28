package socket_test

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/console/cli/idemstore"
	"hop.top/kit/go/transport/cmdsurface"
	"hop.top/kit/go/transport/socket"
	"hop.top/kit/go/transport/transportsvc"
)

// ordinalRunner answers each run with its ordinal; with hold set, a
// run signals started and waits for hold to close.
type ordinalRunner struct {
	calls   atomic.Int32
	hold    chan struct{}
	started chan struct{}
}

func (r *ordinalRunner) Run(ctx context.Context, inv cmdsurface.Invocation) (cmdsurface.Result, error) {
	n := r.calls.Add(1)
	if r.hold != nil {
		r.started <- struct{}{}
		select {
		case <-r.hold:
		case <-ctx.Done():
			return cmdsurface.Result{}, ctx.Err()
		}
	}
	return cmdsurface.Result{Stdout: fmt.Sprintf("run %d %v", n, inv.Args)}, nil
}

func (r *ordinalRunner) Stream(context.Context, cmdsurface.Invocation, chan<- cmdsurface.Event) error {
	return nil
}

func startIdempotentSocket(t *testing.T, run cmdsurface.Runner) string {
	t.Helper()
	path := socketPath(t)
	startSocket(t, path, transportsvc.WithBridgeOptions(
		cmdsurface.WithRunner(run),
		cmdsurface.WithIdempotency(cmdsurface.NewIdempotencyLedger(idemstore.Memory()), time.Hour),
	))
	return path
}

func TestIdempotencyKeyReplaysOverTheSocket(t *testing.T) {
	t.Parallel()
	run := &ordinalRunner{}
	path := startIdempotentSocket(t, run)
	req := socket.Request{Path: []string{"ping"}, Args: []string{"a"}, IdempotencyKey: "k1"}

	first := call(t, path, req)
	require.True(t, first.Ok, "%+v", first.Error)
	assert.False(t, first.Replayed)

	second := call(t, path, req)
	require.True(t, second.Ok, "%+v", second.Error)
	assert.True(t, second.Replayed, "the second answer is the record")
	assert.Equal(t, first.Result.Stdout, second.Result.Stdout)
	assert.EqualValues(t, 1, run.calls.Load())

	// Another caller's key is its own.
	bob := req
	bob.Caller = "bob"
	resp := call(t, path, bob)
	require.True(t, resp.Ok)
	assert.False(t, resp.Replayed)

	reused := req
	reused.Args = []string{"b"}
	resp = call(t, path, reused)
	require.False(t, resp.Ok)
	assert.Equal(t, socket.CodeConflict, resp.Error.Code)
	assert.Contains(t, resp.Error.Message, cmdsurface.CodeIdempotencyKeyReused)
	assert.EqualValues(t, 2, run.calls.Load())
}

func TestIdempotencyKeyInFlightIsAConflict(t *testing.T) {
	t.Parallel()
	run := &ordinalRunner{hold: make(chan struct{}), started: make(chan struct{}, 1)}
	path := startIdempotentSocket(t, run)
	req := socket.Request{Path: []string{"ping"}, IdempotencyKey: "k1"}

	done := make(chan socket.Response, 1)
	go func() { done <- call(t, path, req) }()
	<-run.started

	resp := call(t, path, req)
	require.False(t, resp.Ok)
	assert.Equal(t, socket.CodeConflict, resp.Error.Code)
	assert.Contains(t, resp.Error.Message, cmdsurface.CodeIdempotencyConflict)

	close(run.hold)
	first := <-done
	require.True(t, first.Ok, "%+v", first.Error)
}
