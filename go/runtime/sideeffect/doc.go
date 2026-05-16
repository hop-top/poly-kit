// Package sideeffect declares the four interfaces kit treats as
// the canonical seam for mutating side effects in CLI commands:
// FS, HTTP, Bus, and Exec.
//
// Three implementations of each interface live in sub-packages:
//
//   - real     — wraps stdlib + kit primitives. Production default.
//   - dryrun   — prints what would happen and returns synthetic
//     responses. Wired in by the cli when --dry-run is on.
//   - testfake — records calls into a slice with assertion helpers.
//     Optimized for tests.
//
// Reads (os.ReadFile, os.Stat, http.Get, http.Head) pass through
// to stdlib unchanged. Dry-run does not pretend reads are unsafe.
//
// Adopters wire the interfaces through dependency injection. A
// command author imports sideeffect (interfaces only), takes the
// implementation via a struct field or constructor argument, and
// the cli wrapper picks real/dryrun/testfake at the boundary.
//
// See ADR-0019 for the full design rationale of the package
// (interfaces, three-impl model, dryrun-vs-testfake separation).
// See ADR-0020 for the current --dry-run policy: tier-driven
// default-allow off kit/side-effect, with cli.OptOutDryRun() as
// the explicit escape hatch. ADR-0019's per-command opt-in
// registry is partially superseded; cli.SupportsDryRun() and the
// kit/dry-run: supported annotation remain back-compat synonyms.
package sideeffect
