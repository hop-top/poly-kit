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
// Merging follows yaml.v3 decoding into an existing value: struct fields merge
// one by one at any depth, while scalars, slices, arrays, each map entry and
// any (interface) values are replaced whole by the later layer. A null clears
// pointer, slice, map and interface fields and leaves scalars and structs.
// A value of the wrong kind for a typed field is a decode error.
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
	// as a file layer): struct fields merge one by one at any depth; scalars,
	// slices, map entries and any values are replaced whole (a map entry is
	// not merged, even when it is itself a map). Use this to keep typed
	// defaults colocated with the Config struct itself.
	Defaults any

	// SystemConfigPath is the path to the system-wide config file (e.g. /etc/tool/config.yaml).
	SystemConfigPath string
	// UserConfigPath is the path to the per-user config file (e.g. ~/.config/tool/config.yaml).
	// It is the canonical user file and the write target for [ScopeUser];
	// see LegacyUserConfigPaths for fallbacks when it does not exist.
	UserConfigPath string
	// LegacyUserConfigPaths lists fallback locations for the user config,
	// for a tool that shipped its own loader before adopting this package
	// (an old directory, or a different file name such as config.json).
	// They are consulted in order only when no file exists at
	// UserConfigPath; the first existing regular file fills the user layer
	// exactly as UserConfigPath would (merge, Migrations, PathKeys, origin
	// recording, which reports that file's real path). The rest are not
	// read. When UserConfigPath exists they are ignored entirely. A
	// candidate that exists but is a directory, is unreadable or fails to
	// parse is an error naming it, never a fall-through. The file may be
	// any format yaml.v3 reads, JSON included.
	//
	// Entries: "" is skipped; "~" and a "~/" prefix expand to the home
	// directory; any other path, relative included, is used as written
	// (relative to the process working directory, like UserConfigPath).
	// An empty UserConfigPath disables the user layer and these with it.
	// Nil or empty keeps the plain behavior. [LegacyDotConfigPath] builds
	// the common ~/.config/<tool>/<name> candidate.
	//
	// The legacy file is never modified, moved or deleted. A [Set],
	// [SetValue] or [Unset] on [ScopeUser] while only a legacy file exists
	// writes the canonical file, seeded from the legacy content first; the
	// canonical file then shadows the legacy one. A Migrations write-back
	// (MigrateOptions.WriteBack) is skipped for a legacy file.
	LegacyUserConfigPaths []string
	// OnLegacyConfig, when non-nil, is called as (used, canonical) each
	// time a Load, LoadWithOrigins or Reloadable reload takes the user
	// layer from a legacy file, after that file merged successfully: once
	// per call, not once per process. Use it to print a notice. It does
	// not fire for Get, List, Set, Unset or the Paths* listings.
	OnLegacyConfig func(used, canonical string)
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

	// Project, when non-nil, restricts ProjectConfigPath only (forbidden
	// keys, narrow-only lists); every other layer is unaffected. Nil keeps
	// the plain merge.
	Project *ProjectRules

	// PathKeys lists dotted keys whose values are filesystem paths. A
	// relative value is resolved to an absolute path: against the directory
	// of the file that supplied it (a project ".tool/config.yaml" resolves
	// against the project root, the parent of ".tool"; ".tool.yaml" against
	// its own directory), or against the cwd when it came from EnvOverride
	// or Overrides (-c key=value). Absolute and "~"-prefixed values are left
	// as written. Keys match case-folded per segment; "*" matches any one
	// segment. Values may be a string or a list of strings.
	PathKeys []string

	// DeepMergeMaps, when true and dst is a *map[string]any, merges nested
	// mappings key by key across layers instead of replacing each top-level
	// entry whole. Scalars, lists and nulls still replace. Off by default
	// (the documented whole-entry replacement); it has no effect on struct
	// dst, where map-typed fields keep replacing entry by entry.
	DeepMergeMaps bool

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
//	  → SystemConfigPath → UserConfigPath (or the first existing
//	    LegacyUserConfigPaths entry when it is absent) → ProjectConfigPath
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
	return load(dst, opts, nil)
}

// load is Load with an optional origin recorder.
func load(dst any, opts Options, rec *originRec) error {
	target := mergeTarget(dst, opts)
	if err := applyDefaults(target, opts.Defaults); err != nil {
		return err
	}
	if rec != nil && opts.Defaults != nil {
		defs, err := leafValues(opts.Defaults)
		if err != nil {
			return fmt.Errorf("marshal defaults: %w", err)
		}
		for p := range defs {
			rec.setLeaf(p, LayerDefault, "")
		}
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

	userPath, userOpts, userLegacy, err := userSlot(opts)
	if err != nil {
		return err
	}
	slots := []struct {
		path   string
		layer  LayerName
		o      Options
		legacy bool
	}{
		{opts.SystemConfigPath, LayerSystem, opts, false},
		{userPath, LayerUser, userOpts, userLegacy},
		{opts.ProjectConfigPath, LayerProject, opts, false},
	}
	for _, s := range slots {
		path := s.path
		if path == "" {
			continue
		}
		merge := mergeFileWithMigrations
		if path == opts.ProjectConfigPath && opts.Project != nil {
			merge = mergeProjectFile
		}
		err := merge(target, path, s.o)
		if err == nil {
			if err := recordFile(rec, path, s.layer, s.o); err != nil {
				return err
			}
			if s.legacy && opts.OnLegacyConfig != nil {
				opts.OnLegacyConfig(path, opts.UserConfigPath)
			}
			continue
		}
		if !errors.Is(err, os.ErrNotExist) {
			var fe *ErrProjectForbidden
			var ne *ErrNarrowEmpty
			if errors.As(err, &fe) || errors.As(err, &ne) {
				return err // already names the file
			}
			return fmt.Errorf("load config %s: %w", path, err)
		}
	}
	for _, path := range opts.ExtraConfigPaths {
		if err := mergeFileWithMigrations(target, path, opts); err != nil {
			return fmt.Errorf("load config %s: %w", path, err)
		}
		if err := recordFile(rec, path, LayerExtra, opts); err != nil {
			return err
		}
	}
	if opts.EnvOverride != nil {
		var before map[string]any
		track := rec != nil || len(opts.PathKeys) > 0
		if track {
			var err error
			if before, err = leafValues(dst); err != nil {
				return fmt.Errorf("read merged config: %w", err)
			}
		}
		opts.EnvOverride(dst)
		if track {
			after, err := leafValues(dst)
			if err != nil {
				return fmt.Errorf("read merged config: %w", err)
			}
			changed := changedLeaves(before, after)
			if err := resolveEnvPaths(dst, changed, opts.PathKeys); err != nil {
				return fmt.Errorf("resolve env paths: %w", err)
			}
			if rec != nil {
				for _, p := range changed {
					rec.setLeaf(p, LayerEnv, "")
				}
			}
		}
	}
	overrides, err := resolveOverridePaths(opts.Overrides, opts.PathKeys)
	if err != nil {
		return fmt.Errorf("apply CLI overrides: %w", err)
	}
	if err := ApplyOverrides(target, overrides); err != nil {
		return fmt.Errorf("apply CLI overrides: %w", err)
	}
	if rec != nil {
		rec.set(opts.Overrides, LayerOverride, "")
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
	data, err := fileData(path, opts)
	if err != nil {
		return err
	}
	return yaml.Unmarshal(data, dst)
}

// fileData is the YAML bytes of path, migrated when Options supplies
// migrations.
func fileData(path string, opts Options) ([]byte, error) {
	var data []byte
	var err error
	if len(opts.Migrations) == 0 {
		data, err = os.ReadFile(path)
		if err != nil {
			return nil, err
		}
	} else {
		migrated, err := Migrate(path, opts.Migrations, opts.MigrateOptions)
		if err != nil {
			return nil, err
		}
		data, err = yaml.Marshal(migrated)
		if err != nil {
			return nil, fmt.Errorf("re-marshal migrated %s: %w", path, err)
		}
	}
	return resolveFilePaths(data, path, opts)
}

// recordFile notes every key path supplies as coming from layer; a no-op
// without a recorder.
func recordFile(rec *originRec, path string, layer LayerName, opts Options) error {
	if rec == nil {
		return nil
	}
	data, err := fileData(path, opts)
	if err != nil {
		return fmt.Errorf("load config %s: %w", path, err)
	}
	var doc any
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return fmt.Errorf("load config %s: %w", path, err)
	}
	rec.set(doc, layer, path)
	return nil
}
