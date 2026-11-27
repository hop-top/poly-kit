package cli

import (
	"os"

	coreconfig "hop.top/kit/go/core/config"
)

// projectMarkers is the one place that decides which project files
// the tool's chain looks for: [Config.ProjectMarker] when set, else nil,
// which core/config reads as "kit's default markers".
func (c Config) projectMarkers() []string {
	if c.ProjectMarker == "" {
		return nil
	}
	return []string{c.ProjectMarker}
}

// ConfigOptions returns core/config Options for the tool this Config
// declares, resolved from the process working directory. See
// [Config.ConfigOptionsFrom].
func (c Config) ConfigOptions() coreconfig.Options {
	cwd, err := os.Getwd()
	if err != nil {
		cwd = ""
	}
	return c.ConfigOptionsFrom(cwd)
}

// ConfigOptionsFrom returns core/config Options for the tool this Config
// declares: system, user and project paths for Name (project files from
// ProjectMarker when set, else kit's default markers, found from cwd)
// plus LegacyUserConfigPaths, so the tool's own Load, Get, Set and List
// agree with `serve` and `config path` on the user file without
// restating the list. Set Defaults, Migrations, OnLegacyConfig or any
// other field on the returned value. With an empty Name there is no
// tool to resolve files for: the result is the zero Options, legacy
// paths included, matching an empty chain from [config.RootResolver].
func (c Config) ConfigOptionsFrom(cwd string) coreconfig.Options {
	if c.Name == "" {
		return coreconfig.Options{}
	}
	opts := coreconfig.OptionsForToolWithMarkersFrom(cwd, c.Name, c.projectMarkers())
	opts.LegacyUserConfigPaths = c.LegacyUserConfigPaths
	return opts
}

// ConfigPathsFrom returns the precedence chain for the tool this Config
// declares, highest first, resolved from cwd: the same layers as
// [Config.ConfigOptionsFrom], with LegacyUserConfigPaths as
// "user-legacy" entries right after "user". Nil when Name is empty.
func (c Config) ConfigPathsFrom(cwd string) []coreconfig.ResolvedPath {
	if c.Name == "" {
		return nil
	}
	chain := coreconfig.PathsForToolWithMarkers(cwd, c.Name, c.projectMarkers())
	return coreconfig.WithLegacyUserPaths(chain, c.LegacyUserConfigPaths)
}
