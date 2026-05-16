// Package template implements the kit init template engine.
//
// # Overview
//
// The package separates concerns across three primary units:
//
//   - Manifest (manifest.go): parses kit-template.yaml, validates
//     name, semver constraint, choice variables, regex patterns,
//     and hook script paths.
//   - Engine (engine.go): walks an fs.FS source tree, applies
//     per-file rules from files.go (.tmpl, kit-conditional, exclude,
//     binary), enforces tier filtering for augment mode, writes to
//     target with conflict-aware semantics (.kit-suggested for
//     non-identical existing files; idempotent skip for byte-identical).
//   - Registry (registry.go): resolves template specs (built-in name,
//     @org/name via index lookup, direct git URL, filesystem path)
//     into an fs.FS the Engine can render. Caches git clones under
//     <cacheDir>/<sha256>/.
//
// Hooks (hooks.go) are executed by the orchestrator (cmd/kit/init);
// the engine itself does not run hooks, git, or gh.
//
// # Conditional rendering
//
// Files under a kit-conditional.<expr>/ subtree are rendered only
// when <expr> evaluates true against the variable map. v1 supports
// only key=value expressions; future versions may add ! prefix and
// boolean operators.
//
// # Tier filtering
//
// tiers.yaml at the template root maps file paths to applicable
// tier numbers. Bootstrap mode passes tier=0 to bypass the filter;
// augment mode passes tier 1-4 per the user's --tier flag.
//
// # Errors
//
// All error factories live in errors.go and pair with sentinel vars
// for both errors.Is and errors.As compatibility:
//
//   - ErrManifestInvalid / NewManifestInvalidError / IsManifestInvalid
//   - ErrTemplateNotFound / NewTemplateNotFoundError / IsTemplateNotFound
//   - ErrHookFailed / NewHookFailedError / IsHookFailed
//   - ErrFileConflict / NewFileConflictError / IsFileConflict
//
// # Spec
//
// See docs/superpowers/specs/2026-04-26-kit-init-design.md (in the
// ops repo) for the full design.
package template
