package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"

	"hop.top/kit/go/core/xdg"
)

// SourceUserLegacy is the [ResolvedPath.Source] label of a legacy user
// config candidate (see [Options.LegacyUserConfigPaths]).
const SourceUserLegacy = "user-legacy"

// LegacyDotConfigPath returns ~/.config/<tool>/<name>: the location many
// tools used before adopting this package, on macOS where the canonical user
// directory is "~/Library/Application Support/<tool>" unless XDG_CONFIG_HOME
// is set. Pass it in [Options.LegacyUserConfigPaths] so a tool's existing
// file keeps loading. Returns "" when the home directory is unknown; empty
// entries in LegacyUserConfigPaths are skipped, so the result is safe to
// pass unchecked.
func LegacyDotConfigPath(tool, name string) string {
	home := xdg.Home()
	if home == "" || tool == "" || name == "" {
		return ""
	}
	return filepath.Join(home, ".config", tool, name)
}

// expandLegacyPath resolves one legacy entry: "~" and a "~/" prefix expand
// to the home directory; everything else, relative paths included, is used
// as written (a relative path resolves against the process working
// directory, exactly like Options.UserConfigPath).
func expandLegacyPath(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") || strings.HasPrefix(p, "~"+string(filepath.Separator)) {
		if home := xdg.Home(); home != "" {
			return filepath.Join(home, p[1:])
		}
	}
	return p
}

// effectiveUserPath is the single answer to "which file is the user layer":
// opts.UserConfigPath, unless that file does not exist and one of
// opts.LegacyUserConfigPaths does. legacy reports the latter. Every reader
// and writer of the user layer goes through here.
//
// Candidates are tried in order; the first that exists wins and the rest
// are not consulted. A candidate that exists but is not a regular file, or
// cannot be stat'ed, is an error naming it rather than a fall-through. An
// empty UserConfigPath disables the user layer, legacy included. Without
// legacy paths the result is always UserConfigPath, untouched.
func effectiveUserPath(opts Options) (path string, legacy bool, err error) {
	canon := opts.UserConfigPath
	if canon == "" || len(opts.LegacyUserConfigPaths) == 0 {
		return canon, false, nil
	}
	if _, serr := os.Stat(canon); !errors.Is(serr, fs.ErrNotExist) {
		// Present (or unreadable, or a directory): the canonical path
		// owns the layer and reports its own problems.
		return canon, false, nil
	}
	for _, raw := range opts.LegacyUserConfigPaths {
		if raw == "" {
			continue
		}
		p := expandLegacyPath(raw)
		if filepath.Clean(p) == filepath.Clean(canon) {
			continue
		}
		info, serr := os.Stat(p)
		if errors.Is(serr, fs.ErrNotExist) {
			continue
		}
		if serr != nil {
			return "", false, fmt.Errorf("load config %s: %w", p, serr)
		}
		if !info.Mode().IsRegular() {
			return "", false, fmt.Errorf("load config %s: not a regular file", p)
		}
		return p, true, nil
	}
	return canon, false, nil
}

// EffectiveUserPath reports which file is the user layer for opts: the
// canonical [Options.UserConfigPath] unless that file does not exist and one
// of [Options.LegacyUserConfigPaths] does, in which case path is that
// legacy file and legacy is true. It is the resolver Load, Get, List, the
// writers and reload use, exported so other readers of the user layer (a
// service config loader) agree with them. A candidate that is a directory
// or cannot be stat'ed is an error naming it, with no fall-through.
func EffectiveUserPath(opts Options) (path string, legacy bool, err error) {
	return effectiveUserPath(opts)
}

// userSlot resolves the user layer for Load. When a legacy file is in use,
// the returned Options never write migrations back: the file belongs to
// another tool layout (possibly another format) and must not be rewritten.
func userSlot(opts Options) (path string, o Options, legacy bool, err error) {
	path, legacy, err = effectiveUserPath(opts)
	o = opts
	if legacy {
		o.MigrateOptions.WriteBack = false
	}
	return path, o, legacy, err
}

// parseWriteDoc loads the document a write to scope starts from. For the
// user scope with a legacy file in use (canonical absent), that is the
// legacy file's content, so the first write seeds the canonical file
// instead of shadowing it with a near-empty one. The legacy file is only
// read.
func parseWriteDoc(path string, opts Options, scope Scope) (*yaml.Node, error) {
	if scope == ScopeUser {
		src, legacy, err := effectiveUserPath(opts)
		if err != nil {
			return nil, err
		}
		if legacy {
			doc, err := parseOrCreateDoc(src)
			if err == nil {
				blockStyle(doc)
			}
			return doc, err
		}
	}
	return parseOrCreateDoc(path)
}

// blockStyle clears flow style on every collection so a JSON legacy file
// seeds a readable block-style YAML file. Scalar styles stay: a quoted
// "1" must remain a string.
func blockStyle(n *yaml.Node) {
	if n.Kind == yaml.MappingNode || n.Kind == yaml.SequenceNode {
		n.Style &^= yaml.FlowStyle
	}
	for _, c := range n.Content {
		blockStyle(c)
	}
}

// WithLegacyUserPaths inserts the legacy candidates right after the
// canonical [ResolvedPath] with Source "user" in chain (before "system"
// when the chain has none), labeled [SourceUserLegacy]. Exists is the
// file's real state. Entries are shown whether or not they would be used:
// they apply only while the canonical file is absent. It returns a new
// slice; chain is not modified, and empty legacy yields an identical copy.
func WithLegacyUserPaths(chain []ResolvedPath, legacy []string) []ResolvedPath {
	var extra []ResolvedPath
	for _, raw := range legacy {
		if raw == "" {
			continue
		}
		p := expandLegacyPath(raw)
		extra = append(extra, ResolvedPath{
			Path:   p,
			Source: SourceUserLegacy,
			Exists: fileExists(p),
		})
	}
	at := len(chain)
	for i, e := range chain {
		if e.Source == "user" {
			at = i + 1
			break
		}
		if e.Source == "system" && at == len(chain) {
			at = i
		}
	}
	out := make([]ResolvedPath, 0, len(chain)+len(extra))
	out = append(out, chain[:at]...)
	out = append(out, extra...)
	return append(out, chain[at:]...)
}

// PathsForToolWithLegacy is [PathsForTool] plus the legacy user candidates
// ([WithLegacyUserPaths]), for "config paths" output that matches a Load
// using [Options.LegacyUserConfigPaths]. With no legacy paths the result
// equals PathsForTool's. It never invokes [Options.OnLegacyConfig].
func PathsForToolWithLegacy(cwd, tool string, legacy []string) []ResolvedPath {
	return WithLegacyUserPaths(PathsForTool(cwd, tool), legacy)
}
