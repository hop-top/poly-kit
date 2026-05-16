// Package config provides a layered YAML configuration loader.
//
// Layers are merged in the order: system → user → project → env, where each
// later layer overwrites fields set by earlier layers. Missing files are silently
// skipped; a file that exists but cannot be parsed returns an error.
//
// dst must be a pointer to a struct. YAML unmarshalling follows the rules of
// gopkg.in/yaml.v3: struct fields are matched by their yaml tag, or by
// lower-cased field name if no tag is present.
//
// Warning: YAML unmarshal replaces slice and array fields entirely rather than
// appending to them. Each layer overwrites the previous value for those types.
package config

import (
	"errors"
	"fmt"
	"os"

	"github.com/spf13/viper"
	"gopkg.in/yaml.v3"
)

// Options configures the sources for Load.
// Any Path field may be empty; empty paths are skipped entirely.
// EnvOverride, if non-nil, is called after all files have been merged and
// receives the fully-merged dst value so callers can apply environment-variable
// overrides last.
type Options struct {
	// Defaults, when non-nil, is a pointer to a struct whose values seed dst
	// before any file is read. Yaml round-trip merge semantics apply (same
	// as a file layer): scalars and slices replaced, maps merged. Use this
	// to keep typed defaults colocated with the Config struct itself.
	Defaults any

	// SystemConfigPath is the path to the system-wide config file (e.g. /etc/tool/config.yaml).
	SystemConfigPath string
	// UserConfigPath is the path to the per-user config file (e.g. ~/.config/tool/config.yaml).
	UserConfigPath string
	// ProjectConfigPath is the path to the project-level config file (e.g. .tool.yaml).
	ProjectConfigPath string
	// ExtraConfigPaths is an ordered list of additional config files merged
	// after ProjectConfigPath (and before EnvOverride). Each path is treated
	// like the layered files: missing entries error (unlike the system/user/
	// project slots which silently skip), since these are explicit user
	// requests via -c <path>. Build it from CLI flags via ParseConfigArgs.
	ExtraConfigPaths []string

	// Migrations, when non-empty, is run on each file before it merges into
	// dst. Files at or above the highest migration's To version are
	// untouched. Failure aborts Load with the offending file path.
	Migrations []Migration
	// MigrateOptions controls migration behavior (schema key name,
	// optional write-back, on-migration hook). Zero value is fine for
	// in-memory-only migrations (the common case).
	MigrateOptions MigrateOptions

	// EnvOverride, if non-nil, is called with the merged config after files
	// have been merged but before CLI overrides. Use it to layer environment
	// variables on top of file values. Stays available for power users; for
	// the 1:1 binding case, prefer setting EnvPrefix + EnvBinds with a
	// non-nil Viper.
	EnvOverride func(cfg any)

	// Overrides, when non-empty, is applied last (after EnvOverride) and wins
	// over every other layer. Build it from CLI flags via ParseConfigArgs.
	Overrides map[string]any

	// Viper, when non-nil, gets:
	//   - Defaults seeded via SeedDefaults before files load (typed defaults
	//     also reach viper.Get* callers).
	//   - The fully merged dst written back after all layers apply, so any
	//     code path that reads via viper.GetString sees the same view as
	//     code reading dst directly.
	Viper *viper.Viper
	// EnvPrefix, when non-empty and Viper is set, configures viper's env
	// reader (SetEnvPrefix + AutomaticEnv with "." -> "_" replacer).
	EnvPrefix string
	// EnvBinds explicitly maps dotted config keys to env var names that
	// don't follow the prefix-derived convention. Applied alongside the
	// automatic prefix mapping.
	EnvBinds []EnvBind
}

// Load merges configuration into dst across every configured layer.
//
// dst must be a non-nil pointer. Files in the system/user/project slots that
// do not exist are silently ignored — those slots are conventional locations,
// not user assertions. Files in ExtraConfigPaths are explicit user requests
// (via -c <path>) so missing files there are an error. A file that exists
// but is not valid YAML causes Load to return an error wrapping the parse
// failure and the offending path.
//
// Layer order:
//
//	Defaults (struct seed)
//	  → SystemConfigPath → UserConfigPath → ProjectConfigPath
//	  → ExtraConfigPaths (in order)
//	  → EnvOverride callback (if set)
//	  → Overrides (-c key=value)
//	  → Viper sync (if Options.Viper set)
//
// Migrations, when supplied, run on each file before it merges. EnvPrefix /
// EnvBinds, when supplied with a non-nil Viper, configure viper's env reader
// before files load so viper.Get* sees env values; the typed dst still picks
// them up via syncToViper at the end (only for keys present in env at the
// time of sync).
func Load(dst any, opts Options) error {
	if err := applyDefaults(dst, opts.Defaults); err != nil {
		return err
	}
	if opts.Viper != nil {
		if opts.Defaults != nil {
			if err := SeedDefaults(opts.Viper, opts.Defaults); err != nil {
				return err
			}
		}
		if opts.EnvPrefix != "" || len(opts.EnvBinds) > 0 {
			if err := BindEnv(opts.Viper, opts.EnvPrefix, opts.EnvBinds...); err != nil {
				return err
			}
		}
	}

	for _, path := range []string{
		opts.SystemConfigPath, opts.UserConfigPath, opts.ProjectConfigPath,
	} {
		if path == "" {
			continue
		}
		if err := mergeFileWithMigrations(dst, path, opts); err != nil &&
			!errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("load config %s: %w", path, err)
		}
	}
	for _, path := range opts.ExtraConfigPaths {
		if err := mergeFileWithMigrations(dst, path, opts); err != nil {
			return fmt.Errorf("load config %s: %w", path, err)
		}
	}
	if opts.EnvOverride != nil {
		opts.EnvOverride(dst)
	}
	if err := ApplyOverrides(dst, opts.Overrides); err != nil {
		return fmt.Errorf("apply CLI overrides: %w", err)
	}
	// We do NOT sync the merged dst back into viper at "Set" precedence —
	// doing so would clobber env-bound values that AutomaticEnv resolves at
	// runtime, and viper's precedence tree (override > flag > env > default)
	// has no slot for "came from a file". Callers who need viper-visible
	// file values should read from dst, or call SyncToViper explicitly when
	// they understand the precedence implications.
	return nil
}

// SyncToViper writes a struct's flattened values into a viper instance via
// v.Set, which lands at viper's highest precedence (override). Use this only
// when you fully control the viper instance and want it to mirror dst — e.g.
// after Load, when no env/flag layer should beat the merged config.
//
// Most callers should read directly from the typed dst; SyncToViper is a
// pragmatic helper for code paths that consult viper.GetString directly.
func SyncToViper(v *viper.Viper, src any) error {
	return syncToViper(v, src)
}

// mergeFileWithMigrations is mergeFile + migration support. If Options
// supplies migrations, the file's parsed map is migrated before being
// re-marshaled to YAML and unmarshalled into dst. Without migrations this
// reduces to the legacy mergeFile path.
func mergeFileWithMigrations(dst any, path string, opts Options) error {
	if len(opts.Migrations) == 0 {
		return mergeFile(dst, path)
	}
	migrated, err := Migrate(path, opts.Migrations, opts.MigrateOptions)
	if err != nil {
		return err
	}
	data, err := yaml.Marshal(migrated)
	if err != nil {
		return fmt.Errorf("re-marshal migrated %s: %w", path, err)
	}
	return yaml.Unmarshal(data, dst)
}

func mergeFile(dst any, path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return yaml.Unmarshal(data, dst)
}
