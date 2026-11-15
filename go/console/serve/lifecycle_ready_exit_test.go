package serve

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A service that reports ready and returns at once leaves both its
// ready signal and its exit pending when awaitReady looks. select
// picks randomly between them, so the pending state is built by hand
// and the pick is repeated until either branch would have been taken.
func TestAwaitReady_ReadyAndExitBothPending(t *testing.T) {
	for i := 0; i < 200; i++ {
		s := NewSupervisor(NewRegistry(), SupervisorConfig{})
		ctx := context.Background()
		sup := newSupervised(ctx, 1)
		st := newRunState(time.Now, []string{"svc"}, nil)
		em := &emitter{optional: st.optional}

		st.markStarted("svc")
		sup.running["svc"] = true
		readyCh := make(chan struct{})
		close(readyCh)
		sup.exits <- serviceExit{name: "svc"}

		err := s.awaitReady(ctx, sup, "svc", readyCh, nil, st, em)
		require.NoError(t, err, "iteration %d", i)

		_, ready, failed, outcomes := st.snapshot()
		assert.Equal(t, []string{"svc"}, ready, "iteration %d", i)
		assert.Empty(t, failed, "iteration %d", i)
		assert.NotContains(t, outcomes, OutcomeStartFailed, "iteration %d", i)
		// The exit is never lost: still queued for the run loop, or
		// already handled as an ordinary post-ready stop.
		if len(sup.exits) == 0 {
			assert.Empty(t, sup.running, "iteration %d", i)
			assert.True(t, st.markStopped("svc"), "stop not reported, iteration %d", i)
		}
	}
}

// A Start that returns nil once the run is canceled, before it reported
// ready, leaves both ctx.Done and its exit pending when awaitReady
// looks. Either pick is the cancellation, never a start failure: the
// return is the cancellation's doing, and stopped waits for the ordered
// stop, as for any other service.
func TestAwaitReady_CleanExitOnCanceledRunIsNotStartFailure(t *testing.T) {
	for _, optional := range []bool{false, true} {
		for i := 0; i < 200; i++ {
			s := NewSupervisor(NewRegistry(), SupervisorConfig{})
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			sup := newSupervised(ctx, 1)
			configs := map[string]Config{"svc": {Enabled: true, Optional: optional}}
			st := newRunState(time.Now, []string{"svc"}, configs)
			em := &emitter{optional: st.optional}

			st.markStarted("svc")
			sup.running["svc"] = true
			sup.exits <- serviceExit{name: "svc"}

			err := s.awaitReady(ctx, sup, "svc", make(chan struct{}), configs, st, em)
			require.ErrorIs(t, err, context.Canceled, "optional=%v iteration %d", optional, i)

			_, ready, failed, outcomes := st.snapshot()
			assert.Empty(t, ready, "optional=%v iteration %d", optional, i)
			assert.Empty(t, failed, "optional=%v iteration %d", optional, i)
			assert.NotContains(t, outcomes, OutcomeStartFailed, "optional=%v iteration %d", optional, i)
			// stopped is left to the ordered stop.
			assert.False(t, st.markStopped("svc"), "stopped reported early, optional=%v iteration %d", optional, i)
		}
	}
}
