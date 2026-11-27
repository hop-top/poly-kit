package config

import (
	"errors"
	"fmt"
)

// Scope identifies which configuration layer to target.
type Scope int

const (
	ScopeSystem  Scope = iota // system-wide (/etc/...)
	ScopeUser                 // per-user (~/.config/...)
	ScopeProject              // project-level (.tool.yaml)
)

// ErrEmptyScope is returned when the target scope has no path configured.
var ErrEmptyScope = errors.New("config: scope path is empty")

// ScopePath returns the file path for the given scope from Options. It is
// the write target: for ScopeUser that is always UserConfigPath, never a
// legacy file (see Options.LegacyUserConfigPaths; Set, SetValue and Unset
// seed it from the legacy file when only that exists).
func ScopePath(opts Options, scope Scope) (string, error) {
	switch scope {
	case ScopeSystem:
		if opts.SystemConfigPath == "" {
			return "", ErrEmptyScope
		}
		return opts.SystemConfigPath, nil
	case ScopeUser:
		if opts.UserConfigPath == "" {
			return "", ErrEmptyScope
		}
		return opts.UserConfigPath, nil
	case ScopeProject:
		if opts.ProjectConfigPath == "" {
			return "", ErrEmptyScope
		}
		return opts.ProjectConfigPath, nil
	}
	return "", fmt.Errorf("config: unknown scope %d", scope)
}
