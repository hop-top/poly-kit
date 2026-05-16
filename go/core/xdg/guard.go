package xdg

import (
	"fmt"
	"sync"
)

// Op is a coarse operation hint passed to the Guard hook.
type Op int

const (
	// OpRead = caller intends to read the returned path.
	OpRead Op = iota
	// OpWrite = caller intends to write to the returned path.
	OpWrite
)

// Guard is the policy hook every directory-returning function in this package
// runs its result through before returning. Default = no-op (always nil). The
// kit/scope package replaces it via SetGuard at init time so importing scope
// hardens xdg automatically.
//
// Guard implementations MUST be safe for concurrent use.
type Guard func(path string, op Op) error

var (
	guardMu sync.RWMutex
	guard   Guard = func(string, Op) error { return nil }
)

// SetGuard installs g as the global guard. Returns the previous guard so
// callers can chain or restore. Pass nil to disable guarding (a permissive
// no-op replaces it).
func SetGuard(g Guard) Guard {
	guardMu.Lock()
	defer guardMu.Unlock()
	prev := guard
	if g == nil {
		guard = func(string, Op) error { return nil }
	} else {
		guard = g
	}
	return prev
}

// CurrentGuard returns the active guard (never nil).
func CurrentGuard() Guard {
	guardMu.RLock()
	defer guardMu.RUnlock()
	return guard
}

// enforce runs the active guard against (path, op) and wraps its error.
func enforce(path string, op Op) error {
	if err := CurrentGuard()(path, op); err != nil {
		return fmt.Errorf("xdg: guard rejected %q: %w", path, err)
	}
	return nil
}
