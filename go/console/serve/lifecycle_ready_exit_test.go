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
