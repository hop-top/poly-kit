package socket_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/transport/cmdsurface"
	"hop.top/kit/go/transport/socket"
	"hop.top/kit/go/transport/transportsvc"
)

// holdRunner holds every run until release is closed, signaling
// started as each one begins.
type holdRunner struct {
	started chan struct{}
	release chan struct{}
}

func (r *holdRunner) Run(ctx context.Context, _ cmdsurface.Invocation) (cmdsurface.Result, error) {
	r.started <- struct{}{}
	select {
	case <-r.release:
	case <-ctx.Done():
	}
	return cmdsurface.Result{Stdout: "done"}, nil
}

func (r *holdRunner) Stream(ctx context.Context, inv cmdsurface.Invocation, out chan<- cmdsurface.Event) error {
	defer close(out)
	res, err := r.Run(ctx, inv)
	out <- cmdsurface.Event{Kind: "done", Data: &res, At: time.Now()}
	return err
}

// A call the capacity gate refuses — every slot taken, no queue — is
// OVERLOADED on the wire, with retry_after_ms.
func TestOverloadedCallCarriesRetryAfter(t *testing.T) {
	t.Parallel()
	run := &holdRunner{started: make(chan struct{}, 1), release: make(chan struct{})}
	path := socketPath(t)
	startSocket(t, path, transportsvc.WithBridgeOptions(
		cmdsurface.WithRunner(run),
		cmdsurface.WithConcurrency(cmdsurface.Concurrency{MaxInflight: 1}),
	))

	first := make(chan socket.Response, 1)
	go func() { first <- call(t, path, socket.Request{Path: []string{"ping"}}) }()
	<-run.started

	resp := call(t, path, socket.Request{Path: []string{"ping"}})
	require.False(t, resp.Ok)
	require.NotNil(t, resp.Error)
	assert.Equal(t, socket.CodeOverloaded, resp.Error.Code)
	assert.Contains(t, resp.Error.Message, "overloaded")
	// No run has ended to estimate from: the hint is its floor.
	assert.Equal(t, int64(1000), resp.Error.RetryAfterMs)

	close(run.release)
	got := <-first
	assert.True(t, got.Ok, "the call holding the slot completes: %+v", got.Error)
}
