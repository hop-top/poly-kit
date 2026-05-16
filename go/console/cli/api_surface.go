// Package cli — framework API inventory.
//
// This file lists every package-level symbol carrying the
// `// API: framework` marker (see ADR-0031). Symbols here are
// consumed by adopters, not by kit itself. Deleting one is a build
// break — that is the point.
//
// The list intentionally excludes exported symbols that ARE
// referenced internally (the linter accepts those without help) and
// non-framework dead code (which should be deleted, not preserved).
//
// To add a symbol: annotate it at its declaration with
//
//	// API: framework — <one-line purpose>
//	//
//	// <godoc body>
//	//
//	//lint:ignore U1000 framework API surface (ADR-0031)
//
// then add an assertion line below.
//
// Pilot package for ADR-0031.
package cli

// Compile-time assertions that each framework-API symbol still
// exists. Deleting the symbol breaks the build here; that is the
// signal to update the inventory deliberately.
var (
	_ = kitArgs
	_ = kitExitCodes
)
