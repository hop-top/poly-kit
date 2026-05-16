package config

import (
	"os"
	"path/filepath"

	"hop.top/kit/go/core/xdg"
)

// ResolvedPath describes one entry in the config resolution chain.
type ResolvedPath struct {
	// Path is the absolute filesystem path that would be consulted.
	// For the synthetic "default" entry, Path is the sentinel "<defaults>".
	Path string
	// Source labels where the path comes from in the precedence chain.
	// One of: "cwd", "project", "workspace", "user", "system", "default".
	Source string
	// Scope is the user-facing scope label (project name, etc.). May be empty.
	Scope string
	// Exists reports whether a config file actually exists at Path right now.
	// Always true for the synthetic "default" entry.
	Exists bool
}

// defaultsSentinel is the Path emitted for the synthetic "default" entry,
// representing in-binary defaults that always apply last.
const defaultsSentinel = "<defaults>"

// pathsTool is the tool name used to compose user/system config paths
// (e.g. ~/.config/<tool>/config.yaml). Hard-coded to "kit" because this
// package lives under hop.top/kit and is the shared config layer for every
// kit-built CLI; tools that need a different name should compose their own
// Options and call Load directly.
const pathsTool = "kit"

// projectMarkers are searched for during the cwd → project root walk-up,
// in order. The first match wins per directory.
var projectMarkers = []string{
	filepath.Join(".kit", "config.yaml"),
	".kit.yaml",
	"kit.yaml",
}

// Paths returns the config resolution chain that would be walked starting
// from cwd, ordered highest-precedence first.
//
// The chain reflects what the loader would consult — every entry, whether
// or not the file exists. Callers can filter on Exists if they only care
// about populated paths. cwd should be an absolute path; if relative, it
// is resolved against the current working directory.
//
// The chain is: cwd-marker(s) → walk-up to project root → user
// (~/.config/<tool>/config.yaml) → system (/etc/<tool>/config.yaml) →
// synthetic "default" entry. The walk-up stops at the user's home dir so
// user-scope discovery never escapes ~.
func Paths(cwd string) []ResolvedPath {
	abs, err := filepath.Abs(cwd)
	if err != nil {
		// Fall back to the input — best-effort; caller still gets a chain.
		abs = cwd
	}

	var out []ResolvedPath

	// 1) cwd marker(s): probe the start directory itself first.
	for _, m := range projectMarkers {
		p := filepath.Join(abs, m)
		out = append(out, ResolvedPath{
			Path:   p,
			Source: "cwd",
			Exists: fileExists(p),
		})
	}

	// 2) Walk up looking for project root. Stop at fs root or $HOME.
	home := xdg.Home()
	dir := abs
	for depth := 0; depth < DefaultMaxDepth; depth++ {
		parent := filepath.Dir(dir)
		if parent == dir {
			break // fs root
		}
		dir = parent
		if home != "" && dir == home {
			break // never walk above $HOME for user-scope discovery
		}
		for _, m := range projectMarkers {
			p := filepath.Join(dir, m)
			out = append(out, ResolvedPath{
				Path:   p,
				Source: "project",
				Exists: fileExists(p),
			})
		}
	}

	// 3) User config: ~/.config/<tool>/config.yaml.
	if userPath, ok := userConfigPath(pathsTool); ok {
		out = append(out, ResolvedPath{
			Path:   userPath,
			Source: "user",
			Exists: fileExists(userPath),
		})
	}

	// 4) System config: /etc/<tool>/config.yaml.
	sysPath := systemConfigPath(pathsTool)
	out = append(out, ResolvedPath{
		Path:   sysPath,
		Source: "system",
		Exists: fileExists(sysPath),
	})

	// 5) Synthetic defaults entry — represents in-binary fallbacks.
	out = append(out, ResolvedPath{
		Path:   defaultsSentinel,
		Source: "default",
		Exists: true,
	})

	return out
}

// userConfigPath returns the per-user config file path under XDG_CONFIG_HOME
// for the given tool. Returns ok=false if the path can't be resolved (e.g.
// guard rejection); the chain then omits the user entry.
func userConfigPath(tool string) (string, bool) {
	dir, err := xdg.RawConfigDir(tool)
	if err != nil || dir == "" {
		return "", false
	}
	return filepath.Join(dir, "config.yaml"), true
}

// systemConfigPath returns the system-wide config file path for the given
// tool. Convention is /etc/<tool>/config.yaml on Unix; the package keeps
// this hard-coded (rather than via xdg) because XDG_CONFIG_DIRS varies by
// distro and "config paths" output should be predictable.
func systemConfigPath(tool string) string {
	return filepath.Join("/etc", tool, "config.yaml")
}

// fileExists is a small wrapper around os.Stat that swallows errors and
// returns false for non-regular files (e.g. directories at the candidate
// path). Symlinks are followed.
func fileExists(path string) bool {
	if path == "" {
		return false
	}
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return info.Mode().IsRegular()
}
