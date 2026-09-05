// Package xdg resolves per-user directories and files for the named tool,
// delegating to github.com/adrg/xdg for XDG Base Directory Specification
// compliance with OS-native fallbacks (macOS, Windows, Linux/BSD).
//
// Directory functions (ConfigDir/DataDir/CacheDir/StateDir/RuntimeDir/BinHome)
// return paths joined with the tool name; they have no filesystem side effects.
// Use MustEnsure to create one.
//
// File functions (ConfigFile/DataFile/CacheFile/StateFile/RuntimeFile) return
// a full file path under the tool's directory and create parent directories
// on demand.
//
// Search functions (SearchConfigFile/SearchDataFile/etc.) walk both the
// user-specific directory and system-wide $XDG_*_DIRS, returning the first
// match. Use these to honor org-wide defaults under e.g. /etc/xdg/<tool>/.
//
// # Guardrails
//
// ConfigDir/DataDir/CacheDir/StateDir run their result through the package
// Guard (see guard.go) with OpWrite before returning. Default guard is a
// no-op; kit/scope's defaults.go init() replaces it with a scope-policy
// adapter, so importing scope hardens xdg automatically. Use the Raw*
// variants when you need the path to construct a pattern (not perform I/O).
//
// # Concurrency
//
// Every function here is safe for concurrent use. They resolve against
// github.com/adrg/xdg, which keeps its directories in package-level
// globals and rewrites all of them on each refresh without a lock of its
// own; this package serializes the refresh and the read that follows it
// (see reload.go), so callers need no mutex of their own.
//
// Resolution reads the environment on every call, so a caller that
// changes an XDG_* variable sees the new value on the very next call.
// There is no cache and so no refresh entry point to invoke.
//
// The lock spans the refresh and the resolve that follows it, which it
// has to: the library's own file helpers read the state the refresh
// rewrites. So the lock covers whatever the resolve does, and some
// resolves do filesystem I/O — the *File functions create parent
// directories, and the Search*File functions stat each search path.
// Those are serialized across goroutines. Resolving a location once at
// startup does not care; resolving files in a hot loop from several
// goroutines will queue on it, and should hoist the resolve out of the
// loop, since the path for a given (tool, name) does not change. The
// directory functions do no I/O and hold the lock only for environment
// reads and string joins.
package xdg

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	axdg "github.com/adrg/xdg"
)

// ConfigDir returns $XDG_CONFIG_HOME/<tool> (or OS-native equivalent).
// Result is run through the package Guard with OpWrite; rejection becomes
// an error. Use RawConfigDir to bypass the guard for pattern construction.
func ConfigDir(tool string) (string, error) {
	path, err := RawConfigDir(tool)
	if err != nil {
		return "", err
	}
	if err := enforce(path, OpWrite); err != nil {
		return "", err
	}
	return path, nil
}

// RawConfigDir returns the same path as ConfigDir without invoking the guard.
// Pattern-construction only; never for I/O.
func RawConfigDir(tool string) (string, error) {
	return withReload(func() string {
		return filepath.Join(axdg.ConfigHome, tool)
	}), nil
}

// DataDir returns $XDG_DATA_HOME/<tool> (or OS-native equivalent).
// Subject to the package Guard with OpWrite. Use RawDataDir to skip.
func DataDir(tool string) (string, error) {
	path, err := RawDataDir(tool)
	if err != nil {
		return "", err
	}
	if err := enforce(path, OpWrite); err != nil {
		return "", err
	}
	return path, nil
}

// RawDataDir returns the same path as DataDir without invoking the guard.
// Pattern-construction only; never for I/O.
func RawDataDir(tool string) (string, error) {
	return withReload(func() string {
		return filepath.Join(axdg.DataHome, tool)
	}), nil
}

// CacheDir returns $XDG_CACHE_HOME/<tool> (or OS-native equivalent).
// Subject to the package Guard with OpWrite. Use RawCacheDir to skip.
func CacheDir(tool string) (string, error) {
	path, err := RawCacheDir(tool)
	if err != nil {
		return "", err
	}
	if err := enforce(path, OpWrite); err != nil {
		return "", err
	}
	return path, nil
}

// RawCacheDir returns the same path as CacheDir without invoking the guard.
// Pattern-construction only; never for I/O.
func RawCacheDir(tool string) (string, error) {
	return withReload(func() string {
		return filepath.Join(axdg.CacheHome, tool)
	}), nil
}

// StateDir returns $XDG_STATE_HOME/<tool> (or OS-native equivalent).
// Subject to the package Guard with OpWrite. Use RawStateDir to skip.
func StateDir(tool string) (string, error) {
	path, err := RawStateDir(tool)
	if err != nil {
		return "", err
	}
	if err := enforce(path, OpWrite); err != nil {
		return "", err
	}
	return path, nil
}

// RawStateDir returns the same path as StateDir without invoking the guard.
// Pattern-construction only; never for I/O.
func RawStateDir(tool string) (string, error) {
	return withReload(func() string {
		return filepath.Join(axdg.StateHome, tool)
	}), nil
}

// RuntimeDir returns $XDG_RUNTIME_DIR/<tool> (or OS-native equivalent).
// Suitable for sockets, PID files, and other ephemeral IPC artifacts.
func RuntimeDir(tool string) (string, error) {
	return withReload(func() string {
		return filepath.Join(axdg.RuntimeDir, tool)
	}), nil
}

// BinHome returns $XDG_BIN_HOME/<tool> (or ~/.local/bin/<tool>).
// Suitable for installed user-scoped binaries.
func BinHome(tool string) (string, error) {
	return withReload(func() string {
		return filepath.Join(axdg.BinHome, tool)
	}), nil
}

// ConfigFile returns the full path to <tool>/<name> under the config base
// directory. Parent directories are created on demand.
func ConfigFile(tool, name string) (string, error) {
	return withReloadPath(func() (string, error) {
		return axdg.ConfigFile(filepath.Join(tool, name))
	})
}

// DataFile returns the full path to <tool>/<name> under the data base
// directory. Parent directories are created on demand.
func DataFile(tool, name string) (string, error) {
	return withReloadPath(func() (string, error) {
		return axdg.DataFile(filepath.Join(tool, name))
	})
}

// CacheFile returns the full path to <tool>/<name> under the cache base
// directory. Parent directories are created on demand.
func CacheFile(tool, name string) (string, error) {
	return withReloadPath(func() (string, error) {
		return axdg.CacheFile(filepath.Join(tool, name))
	})
}

// StateFile returns the full path to <tool>/<name> under the state base
// directory. Parent directories are created on demand.
func StateFile(tool, name string) (string, error) {
	return withReloadPath(func() (string, error) {
		return axdg.StateFile(filepath.Join(tool, name))
	})
}

// RuntimeFile returns the full path to <tool>/<name> under the runtime base
// directory. Parent directories are created on demand. Falls back to the OS
// temp directory when XDG_RUNTIME_DIR is unavailable.
func RuntimeFile(tool, name string) (string, error) {
	return withReloadPath(func() (string, error) {
		return axdg.RuntimeFile(filepath.Join(tool, name))
	})
}

// SearchConfigFile looks for <tool>/<name> in $XDG_CONFIG_HOME first, then in
// each $XDG_CONFIG_DIRS entry (e.g. /etc/xdg). Returns the first existing
// path or an error listing the searched paths.
func SearchConfigFile(tool, name string) (string, error) {
	return withReloadPath(func() (string, error) {
		return axdg.SearchConfigFile(filepath.Join(tool, name))
	})
}

// SearchDataFile looks for <tool>/<name> in $XDG_DATA_HOME first, then in
// each $XDG_DATA_DIRS entry (e.g. /usr/local/share, /usr/share).
func SearchDataFile(tool, name string) (string, error) {
	return withReloadPath(func() (string, error) {
		return axdg.SearchDataFile(filepath.Join(tool, name))
	})
}

// SearchCacheFile looks for <tool>/<name> in $XDG_CACHE_HOME.
func SearchCacheFile(tool, name string) (string, error) {
	return withReloadPath(func() (string, error) {
		return axdg.SearchCacheFile(filepath.Join(tool, name))
	})
}

// SearchStateFile looks for <tool>/<name> in $XDG_STATE_HOME.
func SearchStateFile(tool, name string) (string, error) {
	return withReloadPath(func() (string, error) {
		return axdg.SearchStateFile(filepath.Join(tool, name))
	})
}

// SearchRuntimeFile looks for <tool>/<name> in $XDG_RUNTIME_DIR, falling back
// to the OS temp directory when the runtime base is unavailable.
func SearchRuntimeFile(tool, name string) (string, error) {
	return withReloadPath(func() (string, error) {
		return axdg.SearchRuntimeFile(filepath.Join(tool, name))
	})
}

// Home returns the current user's home directory.
func Home() string {
	return withReload(func() string {
		return axdg.Home
	})
}

// UserDir resolves a well-known user directory by name. Recognized names
// (case-insensitive): desktop, download(s), documents, music, pictures,
// videos, templates, publicshare. Returns the platform-native path when
// resolvable; falls back to a sensible default under Home otherwise.
func UserDir(name string) (string, error) {
	dirs := UserDirs()
	switch strings.ToLower(name) {
	case "desktop":
		return dirs.Desktop, nil
	case "download", "downloads":
		return dirs.Download, nil
	case "documents":
		return dirs.Documents, nil
	case "music":
		return dirs.Music, nil
	case "pictures":
		return dirs.Pictures, nil
	case "videos":
		return dirs.Videos, nil
	case "templates":
		return dirs.Templates, nil
	case "publicshare", "public":
		return dirs.PublicShare, nil
	default:
		return "", fmt.Errorf("xdg: unknown user dir %q", name)
	}
}

// UserDirs returns all well-known user directories at once. Useful for
// agents that need to enumerate or present picker-style choices.
func UserDirs() axdg.UserDirectories {
	return withReload(func() axdg.UserDirectories {
		return axdg.UserDirs
	})
}

// FontDirs returns common system locations for installed fonts.
func FontDirs() []string {
	return withReload(func() []string {
		return axdg.FontDirs
	})
}

// ApplicationDirs returns common system locations for installed applications
// (e.g. /Applications on macOS, /usr/share/applications on Linux).
func ApplicationDirs() []string {
	return withReload(func() []string {
		return axdg.ApplicationDirs
	})
}

// MustEnsure creates dir (and parents) with 0750, panicking on error.
func MustEnsure(dir string, err error) string {
	if err != nil {
		panic(err)
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		panic(err)
	}
	return dir
}
