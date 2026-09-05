package xdg

import (
	"sync"

	axdg "github.com/adrg/xdg"
)

// reloadMu serializes every entry into github.com/adrg/xdg.
//
// That library resolves its directories into package-level globals
// (Home, ConfigHome, DataHome, StateHome, CacheHome, RuntimeDir,
// BinHome, UserDirs, FontDirs, ApplicationDirs, the *Dirs search
// slices, and the unexported baseDirs struct) and rewrites all of them
// on every Reload(). It documents no concurrency guarantees and takes
// no lock of its own, so two goroutines resolving a path at the same
// time race: one is mid-write to the globals while the other reads
// them. The race detector flags it; the observable failure is a torn
// path built from a half-updated baseDirs.
//
// The lock therefore has to span more than the Reload() call itself.
// axdg's own helpers (ConfigFile, SearchConfigFile, ...) read baseDirs
// when they run, so a lock released after Reload() would still let a
// concurrent Reload() shred the state those helpers are reading.
// withReload holds it across both halves.
var reloadMu sync.Mutex

// withReload refreshes axdg from the environment and evaluates fn
// against the freshly resolved globals, holding reloadMu throughout.
//
// Reload() is deliberately called on every resolve rather than
// memoised. It is pure environment reads and string joins — no
// syscalls beyond os.Getenv, no filesystem access — so it is cheap,
// and callers (kit's own tests included) set XDG_* variables and
// expect the very next resolve to observe them. A cache would need an
// invalidation check that re-read every XDG_* variable, which is the
// work Reload() already does, in exchange for a staleness bug.
func withReload[T any](fn func() T) T {
	reloadMu.Lock()
	defer reloadMu.Unlock()
	axdg.Reload()
	return fn()
}

// pathResult pairs a resolved path with axdg's error, so withReload can
// carry the two-value returns of the file and search helpers.
type pathResult struct {
	path string
	err  error
}

// withReloadPath is withReload specialised to the (string, error)
// shape that every file and search helper returns.
func withReloadPath(fn func() (string, error)) (string, error) {
	r := withReload(func() pathResult {
		p, err := fn()
		return pathResult{path: p, err: err}
	})
	return r.path, r.err
}
