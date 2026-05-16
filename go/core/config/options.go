package config

import (
	"os"
)

// OptionsForTool returns Options pre-populated with the canonical
// 4-layer cascade for the given tool name. The resolution order is:
//
//  1. project — nearest project marker walking up from cwd
//     (".kit/config.yaml" → ".kit.yaml" → "kit.yaml")
//  2. user    — ~/.config/<tool>/config.yaml
//  3. system  — /etc/<tool>/config.yaml
//  4. defaults — caller-provided in-binary fallbacks (out of band)
//
// Adopters that previously composed Options by hand (and frequently
// dropped the system layer) should use this helper instead so every
// kit-built tool participates in the same cascade.
//
// When a path can't be resolved (e.g. xdg.RawConfigDir rejection,
// or no project marker found) the corresponding field is left
// empty. Load skips empty paths, so partial resolution is safe.
//
// The tool name parameterizes the user/system layers — passing
// "c12n" yields ~/.config/c12n/config.yaml and /etc/c12n/config.yaml,
// not the package-default "kit". Project markers are kit-shaped by
// convention (.kit/, .kit.yaml, kit.yaml) and not parameterized.
func OptionsForTool(tool string) Options {
	cwd, err := os.Getwd()
	if err != nil {
		cwd = ""
	}
	return optionsForToolFrom(cwd, tool)
}

// optionsForToolFrom is the testable seam for OptionsForTool — it
// takes an explicit cwd so unit tests don't rely on os.Getwd.
func optionsForToolFrom(cwd, tool string) Options {
	opts := Options{}

	// Project: pick the highest-precedence existing project marker
	// from the resolution chain (cwd or walked-up project dirs).
	if cwd != "" {
		for _, p := range Paths(cwd) {
			if (p.Source == "cwd" || p.Source == "project") && p.Exists {
				opts.ProjectConfigPath = p.Path
				break
			}
		}
	}

	// User: ~/.config/<tool>/config.yaml.
	if userPath, ok := userConfigPath(tool); ok {
		opts.UserConfigPath = userPath
	}

	// System: /etc/<tool>/config.yaml.
	opts.SystemConfigPath = systemConfigPath(tool)

	return opts
}

// PathsForTool is the tool-parameterized variant of [Paths]. It
// returns the same chain shape but with user/system layers built
// against the supplied tool name instead of the package default.
//
// PathsForTool exists so callers that want the chain (for "config
// paths" output, audits, or cascade visualization) can use the same
// tool name they pass to [OptionsForTool] and stay consistent.
func PathsForTool(cwd, tool string) []ResolvedPath {
	out := Paths(cwd)
	if tool == "" || tool == pathsTool {
		return out
	}
	// Rewrite user/system entries to use the requested tool name.
	for i := range out {
		switch out[i].Source {
		case "user":
			if userPath, ok := userConfigPath(tool); ok {
				out[i].Path = userPath
				out[i].Exists = fileExists(userPath)
			}
		case "system":
			sysPath := systemConfigPath(tool)
			out[i].Path = sysPath
			out[i].Exists = fileExists(sysPath)
		}
	}
	return out
}
