// Package projects is the kit-owned reader/writer for the rux project
// registry stored at the user's XDG config dir under
// rux/projects.yaml.
//
// # Overview
//
// The registry maps short project names (e.g. "ops", "kit") to a path
// plus optional startup command. Two callers share the file:
//
//   - wsm writes entries when a workspace space is added or synced.
//   - rux reads entries during `rux connect <name>` to resolve the
//     project to a path and startup command.
//
// # Config path resolution
//
// DefaultPath delegates to kit/go/core/xdg.ConfigDir("rux") and appends
// projects.yaml. On Linux without an explicit XDG_CONFIG_HOME this
// resolves to ~/.config/rux/projects.yaml. On macOS — where Go's
// os.UserConfigDir falls back to ~/Library/Application Support — it
// resolves to ~/Library/Application Support/rux/projects.yaml. Setting
// XDG_CONFIG_HOME overrides both.
//
// # Schema
//
// File on disk is YAML, schema version 2 (SchemaVersion):
//
//	schema: 2
//	projects:
//	  ops:
//	    path: /Users/alice/src/ops
//	    startup_cmd: zsh
//	    source: wsm
//	    stage:              # optional, owned by kit/core/stage
//	      stage: maintenance
//	      since: 2026-01-02T03:04:05Z
//	      reason: freeze for release
//
// Schema 1 files (no stage block) read transparently. The schema version is intentionally tracked so future migrations can
// reject forward-incompatible files via ErrSchemaUnsupported.
//
// # Concurrency
//
// Write and Delete are safe across processes and goroutines. Each
// mutating call acquires an exclusive github.com/gofrs/flock lock on a
// sidecar projects.yaml.lock for the full read-modify-write cycle, then
// performs an atomic temp+rename to publish the new file.
//
// # Errors
//
// Read, Write and Delete return sentinel errors so callers can branch
// with errors.Is:
//
//   - ErrMalformed         — file exists but YAML is unparseable.
//   - ErrSchemaUnsupported — file's schema field is greater than the
//     constant SchemaVersion supported by this build.
//
// A missing file is not an error: Read returns an empty File and nil, and
// Write/Delete treat it as empty. Write and Delete never overwrite a
// malformed or newer-schema file; they return the sentinel and leave the
// file untouched.
// kit core packages do not log; callers (rux command code) decide log
// behavior on top of these sentinels.
package projects
