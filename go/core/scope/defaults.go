// Default deny list and named scope helpers.
//
// SecretPaths returns the platform-tailored default deny patterns that protect
// well-known credential and secret stores. The pattern set is loaded once at
// init() from the embedded copy of contracts/parity/scope-defaults.json
// (canonical source of truth for cross-language ports) plus a curated set of
// platform-specific paths.
//
// Named helpers (UserDocs, UserDownloads, ToolConfig, ...) return Pattern
// slices that callers can spread into Allow / Deny:
//
//	scope.Default().Allow(scope.UserDocs()...)
//
// Helpers are functions rather than vars so they re-resolve XDG and home
// overrides each call. This makes them safe to use from tests that mutate
// XDG_CONFIG_HOME via t.Setenv.

package scope

import (
	_ "embed"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	"hop.top/kit/go/core/xdg"
)

//go:embed scope-defaults.json
var scopeDefaultsRaw []byte

// scopeDefaults mirrors the structure of scope-defaults.json.
type scopeDefaults struct {
	Version int                 `json:"version"`
	Deny    map[string][]string `json:"deny"`
}

var (
	defaultsOnce  sync.Once
	defaultsCache scopeDefaults
)

func loadDefaults() scopeDefaults {
	defaultsOnce.Do(func() {
		if err := json.Unmarshal(scopeDefaultsRaw, &defaultsCache); err != nil {
			// The file ships with the binary; a parse failure is a build defect.
			panic("scope: parse scope-defaults.json: " + err.Error())
		}
	})
	return defaultsCache
}

// SecretPaths returns the default deny pattern set: common patterns plus the
// patterns specific to the current GOOS. Patterns are ready to feed into
// Policy.Deny — "~" and Windows env macros are resolved at match time.
func SecretPaths() []Pattern {
	d := loadDefaults()
	common := d.Deny["common"]
	platform := d.Deny[runtime.GOOS]
	out := make([]Pattern, 0, len(common)+len(platform))
	for _, p := range common {
		out = append(out, Pattern(expandWindowsEnv(p)))
	}
	for _, p := range platform {
		out = append(out, Pattern(expandWindowsEnv(p)))
	}
	return out
}

// expandWindowsEnv expands %APPDATA%, %LOCALAPPDATA%, %USERPROFILE% to the
// matching env values when present. On non-Windows hosts the expansion is
// best-effort and may return the macro unchanged when the var is empty —
// that's fine: the pattern simply won't match anything on those hosts.
func expandWindowsEnv(p string) string {
	for _, key := range []string{"APPDATA", "LOCALAPPDATA", "USERPROFILE"} {
		token := "%" + key + "%"
		if !strings.Contains(p, token) {
			continue
		}
		v := os.Getenv(key)
		if v == "" {
			continue
		}
		p = strings.ReplaceAll(p, token, v)
	}
	return p
}

// UserDocs returns the user's Documents dir as a doublestar pattern.
// Falls back to ~/Documents when XDG/system lookup fails.
func UserDocs() []Pattern { return userDir("XDG_DOCUMENTS_DIR", "Documents") }

// UserDownloads returns the user's Downloads dir as a doublestar pattern.
func UserDownloads() []Pattern { return userDir("XDG_DOWNLOAD_DIR", "Downloads") }

// UserDesktop returns the user's Desktop dir as a doublestar pattern.
func UserDesktop() []Pattern { return userDir("XDG_DESKTOP_DIR", "Desktop") }

// UserPictures returns the user's Pictures dir as a doublestar pattern.
func UserPictures() []Pattern { return userDir("XDG_PICTURES_DIR", "Pictures") }

// UserMusic returns the user's Music dir as a doublestar pattern.
func UserMusic() []Pattern { return userDir("XDG_MUSIC_DIR", "Music") }

// UserVideos returns the user's Videos dir as a doublestar pattern.
func UserVideos() []Pattern { return userDir("XDG_VIDEOS_DIR", "Videos") }

// UserHome returns the entire user home as a single broad pattern.
// Use sparingly — broader allow rules hurt the value of deny-by-default.
func UserHome() []Pattern {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	return []Pattern{Pattern(filepath.Join(home, "**"))}
}

// userDir resolves the named user dir, preferring the XDG env var when set.
// Returns a single recursive pattern (e.g. ~/Documents/**).
func userDir(envKey, defaultName string) []Pattern {
	if v := os.Getenv(envKey); v != "" {
		return []Pattern{Pattern(filepath.Join(v, "**"))}
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	return []Pattern{Pattern(filepath.Join(home, defaultName, "**"))}
}

// ToolConfig returns the configured config dir for the named tool as a
// recursive pattern. Resolves through xdg.RawConfigDir (no guard) so this
// helper can run safely while the policy is still being built.
func ToolConfig(name string) []Pattern { return resolveToolDir(xdg.RawConfigDir, name) }

// ToolData returns the configured data dir for the named tool as a recursive pattern.
func ToolData(name string) []Pattern { return resolveToolDir(xdg.RawDataDir, name) }

// ToolCache returns the configured cache dir for the named tool as a recursive pattern.
func ToolCache(name string) []Pattern { return resolveToolDir(xdg.RawCacheDir, name) }

// ToolState returns the configured state dir for the named tool as a recursive pattern.
func ToolState(name string) []Pattern { return resolveToolDir(xdg.RawStateDir, name) }

// ToolRuntime returns the conventional runtime dir for the named tool.
// XDG defines $XDG_RUNTIME_DIR but kit/xdg does not yet expose it; we
// fall back to <CacheDir>/runtime which is a safe per-user location.
func ToolRuntime(name string) []Pattern {
	if v := os.Getenv("XDG_RUNTIME_DIR"); v != "" {
		return []Pattern{Pattern(filepath.Join(v, name, "**"))}
	}
	cache, err := xdg.RawCacheDir(name)
	if err != nil {
		return nil
	}
	return []Pattern{Pattern(filepath.Join(cache, "runtime", "**"))}
}

// ToolBin returns the conventional per-user bin dir for the named tool
// (~/.local/bin/<name>** on POSIX, %LOCALAPPDATA%\Programs\<name>\** on Windows).
func ToolBin(name string) []Pattern {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	switch runtime.GOOS {
	case "windows":
		if local := os.Getenv("LOCALAPPDATA"); local != "" {
			return []Pattern{Pattern(filepath.Join(local, "Programs", name, "**"))}
		}
	}
	return []Pattern{Pattern(filepath.Join(home, ".local", "bin", name+"**"))}
}

func resolveToolDir(fn func(string) (string, error), name string) []Pattern {
	dir, err := fn(name)
	if err != nil {
		return nil
	}
	return []Pattern{Pattern(filepath.Join(dir, "**"))}
}

// SystemDirs returns the platform's system directory patterns. These should
// almost always be denied for write and require explicit allow for read.
func SystemDirs() []Pattern {
	switch runtime.GOOS {
	case "windows":
		return []Pattern{
			"C:/Windows/**",
			"C:/Program Files/**",
			"C:/Program Files (x86)/**",
			"C:/ProgramData/**",
		}
	case "darwin":
		return []Pattern{
			"/etc/**",
			"/usr/**",
			"/var/**",
			"/System/**",
			"/Library/**",
			"/private/**",
			"/sbin/**",
			"/bin/**",
		}
	default:
		return []Pattern{
			"/etc/**",
			"/usr/**",
			"/var/**",
			"/sbin/**",
			"/bin/**",
			"/boot/**",
			"/sys/**",
			"/proc/**",
		}
	}
}

// init wires the package singleton with the default deny set so importers
// of go/core/scope/defaults (i.e. anything that imports this file) see a
// hardened scope.Default().
//
// Default mode is Warn (not Strict) so the auto-installed xdg.Guard logs
// hits against SecretPaths without breaking apps that legitimately write
// to other xdg homes (~/.local/share/kit/, etc.). Binaries that want
// hard-fail can call scope.Default().SetMode(scope.Strict) at startup —
// or build a fresh scope.New() policy with explicit Allow rules.
//
// The bare scope package (without the defaults file) leaves Default() in
// Strict with no rules — useful for unit tests that want full control.
//
// init also registers the xdg.Guard hook so every directory returned by
// kit/xdg flows through scope.Default(). xdg's only consumer-facing op is
// OpWrite (caller intent: create + write), so we map it to scope.Write.
func init() {
	Default().SetMode(Warn).DenyOp(Read|Write|Exec, SecretPaths()...)
	xdg.SetGuard(func(path string, op xdg.Op) error {
		var sop Op
		switch op {
		case xdg.OpWrite:
			sop = Write
		default:
			sop = Read
		}
		return Default().Enforce(Path(path), sop)
	})
}
