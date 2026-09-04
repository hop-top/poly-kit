package serve

import (
	"context"
	"os"
	"os/signal"
	"syscall"
)

// ShutdownSignals is the set the supervisor listens for, matching
// api.ListenAndServeWithSignals (contract §"Signals"). SIGKILL and
// SIGSTOP are not catchable and are out of contract.
var ShutdownSignals = []os.Signal{syscall.SIGINT, syscall.SIGTERM}

// SignalContext returns a context canceled by the first SIGINT or
// SIGTERM, plus an escalation channel that receives on the second.
//
// The first signal begins graceful shutdown; a second signal of either
// kind during shutdown aborts the drain, so an operator can escalate
// without reaching for SIGKILL (contract §"Signals"). The caller
// selects on escalate alongside the run and abandons the drain when it
// fires.
//
// stop releases the signal handler and must be called; the returned
// channel is closed by stop, so a select on it after stop does not
// block forever.
func SignalContext(parent context.Context) (ctx context.Context, escalate <-chan os.Signal, stop func()) {
	ch := make(chan os.Signal, 2)
	signal.Notify(ch, ShutdownSignals...)

	ctx, cancel := context.WithCancel(parent)
	second := make(chan os.Signal, 1)

	go func() {
		defer close(second)
		for i := 0; ; i++ {
			sig, ok := <-ch
			if !ok {
				return
			}
			if i == 0 {
				cancel()
				continue
			}
			select {
			case second <- sig:
			default:
			}
			return
		}
	}()

	var stopOnce bool
	return ctx, second, func() {
		if stopOnce {
			return
		}
		stopOnce = true
		signal.Stop(ch)
		close(ch)
		cancel()
	}
}
