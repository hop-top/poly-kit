package config

import (
	"context"
	"fmt"
	"os"
	"os/signal"
)

// WatchSignal installs a signal handler that triggers Reload using the
// Reloadable's currently-held Options on every receipt of any signal in
// sigs. The handler runs synchronously in the calling goroutine until ctx
// is done, then returns ctx.Err() (typically context.Canceled).
//
// Callers typically dedicate a goroutine to WatchSignal:
//
//	go func() {
//	    if err := r.WatchSignal(ctx, syscall.SIGHUP); err != nil &&
//	        !errors.Is(err, context.Canceled) {
//	        log.Error(err)
//	    }
//	}()
//
// SIGHUP is the conventional choice on Unix for "reload your config";
// callers may pass any signal. Tests should use SIGUSR1 or SIGUSR2 —
// SIGHUP delivered in the wrong context can confuse the surrounding
// runner. Decoupling from a baked-in SIGHUP is the whole point of taking
// os.Signal here.
//
// Reload errors are NOT returned from WatchSignal — that would terminate
// the watcher on the first immutable-change veto, which is not the
// desired contract. Instead, the failure event is published to the bus
// (the same channel any operator should already be tailing) and the
// handler waits for the next signal.
//
// Returns an error only when ctx ends or the signal arguments are
// invalid (no signals, or any nil).
func (r *Reloadable[T]) WatchSignal(ctx context.Context, sigs ...os.Signal) error {
	if len(sigs) == 0 {
		return fmt.Errorf("config.WatchSignal: at least one signal required")
	}
	for _, s := range sigs {
		if s == nil {
			return fmt.Errorf("config.WatchSignal: nil signal in args")
		}
	}
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, sigs...)
	defer signal.Stop(ch)

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ch:
			// Capture opts under the wrapper's lock so a concurrent caller
			// supplying a fresh Options to Reload still observes a coherent
			// snapshot here. Errors are dropped on purpose; see doc comment.
			opts := r.Options()
			_ = r.Reload(opts)
		}
	}
}
