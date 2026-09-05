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
// memoized. It is pure environment reads and string joins — no
// syscalls beyond os.Getenv — and callers (kit's own tests included)
// set XDG_* variables and expect the very next resolve to observe
// them. A cache would need an invalidation check that re-read every
// XDG_* variable, which is the work Reload() already does, in
// exchange for a staleness bug.
//
// # What the lock costs
//
// The lock spans the refresh AND the resolve, so the cost is fn()'s,
// not Reload()'s. That matters, because several fn()s touch the
// filesystem:
//
//   - ConfigFile, DataFile, StateFile, CacheFile and RuntimeFile
//     create the parent directories of the path they return
//     (os.MkdirAll, via the library's internal pathutil.Create).
//     RuntimeFile also stats its candidate bases first.
//   - The five Search*File helpers stat each search path until one
//     hits.
//
// Those calls therefore serialize filesystem I/O across goroutines,
// not just a handful of getenvs. On a cold path — resolving a config
// location once at startup — that is irrelevant. A caller resolving
// files in a hot loop from several goroutines will see them queue up,
// and should hoist the resolve out of the loop: these functions
// return a path for a fixed (tool, name), so the answer does not
// change between iterations. The directory-only functions (ConfigDir,
// DataDir, the Raw* variants, Home, UserDirs, FontDirs,
// ApplicationDirs) perform no I/O and hold the lock for the getenvs
// and joins alone.
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
