package config

import (
	"os"
	"path/filepath"

	"hop.top/kit/go/core/xdg"
)

// PathsForToolWithMarkers is the marker-parameterized variant of
// [PathsForTool]. It returns the same chain shape but lets adopters
// override the hard-coded ".kit/config.yaml" → ".kit.yaml" → "kit.yaml"
// project marker chain with their own (e.g. ".ctxt/config.yaml" for the
// ctxt CLI).
//
// Markers are interpreted as paths relative to each candidate directory.
// The first match wins per directory, in slice order. A nil or empty
// markers slice falls back to the package default ([projectMarkers]).
//
// Use this when you ship a CLI whose project marker convention differs
// from kit's default (.kit/...). The user/system layers are parameterized
// by tool just like [PathsForTool].
func PathsForToolWithMarkers(cwd, tool string, markers []string) []ResolvedPath {
	if len(markers) == 0 {
		return PathsForTool(cwd, tool)
	}

	abs, err := filepath.Abs(cwd)
	if err != nil {
		abs = cwd
	}

	var out []ResolvedPath

	for _, m := range markers {
		p := filepath.Join(abs, m)
		out = append(out, ResolvedPath{
			Path:   p,
			Source: "cwd",
			Exists: fileExists(p),
		})
	}

	home := xdg.Home()
	dir := abs
	for depth := 0; depth < DefaultMaxDepth; depth++ {
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
		if home != "" && dir == home {
			break
		}
		for _, m := range markers {
			p := filepath.Join(dir, m)
			out = append(out, ResolvedPath{
				Path:   p,
				Source: "project",
				Exists: fileExists(p),
			})
		}
	}

	if userPath, ok := userConfigPath(tool); ok {
		out = append(out, ResolvedPath{
			Path:   userPath,
			Source: "user",
			Exists: fileExists(userPath),
		})
	}

	sysPath := systemConfigPath(tool)
	out = append(out, ResolvedPath{
		Path:   sysPath,
		Source: "system",
		Exists: fileExists(sysPath),
	})

	out = append(out, ResolvedPath{
		Path:   defaultsSentinel,
		Source: "default",
		Exists: true,
	})

	return out
}

// OptionsForToolWithMarkers is the marker-parameterized variant of
// [OptionsForTool]. It composes the canonical 4-layer cascade for the
// given tool but uses caller-supplied project markers instead of the
// hard-coded .kit/ chain.
//
// A nil or empty markers slice falls back to [OptionsForTool].
func OptionsForToolWithMarkers(tool string, markers []string) Options {
	cwd, err := os.Getwd()
	if err != nil {
		cwd = ""
	}
	return OptionsForToolWithMarkersFrom(cwd, tool, markers)
}

// OptionsForToolWithMarkersFrom is the testable seam for
// [OptionsForToolWithMarkers] — it takes an explicit cwd so unit tests
// don't rely on os.Getwd.
func OptionsForToolWithMarkersFrom(cwd, tool string, markers []string) Options {
	if len(markers) == 0 {
		return optionsForToolFrom(cwd, tool)
	}

	opts := Options{}

	if cwd != "" {
		for _, p := range PathsForToolWithMarkers(cwd, tool, markers) {
			if (p.Source == "cwd" || p.Source == "project") && p.Exists {
				opts.ProjectConfigPath = p.Path
				break
			}
		}
	}

	if userPath, ok := userConfigPath(tool); ok {
		opts.UserConfigPath = userPath
	}

	opts.SystemConfigPath = systemConfigPath(tool)

	return opts
}
