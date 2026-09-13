package envelope

import "sort"

// Exit codes for the standard classes that previously existed only as
// trailing comments beside the Code* constants. The asymmetry was a real
// cost, not a cosmetic one: a package aligning to kit's table could
// import ExitTransient but had to mint its own literal for exit 5,
// because kit exported no name for it. A number a caller must retype is
// a number that can drift.
//
// These are additive. The Exit* constants already exported elsewhere in
// this package keep their declarations and their documentation; nothing
// is renamed or removed.
const (
	// ExitOK is success. Present for completeness so a table of the
	// full taxonomy can be written without a bare 0.
	ExitOK = 0

	// ExitUsage is the caller's invocation being wrong: an unknown
	// flag, a missing argument, a value the command cannot parse.
	// Permanent by construction — the same argv fails identically.
	ExitUsage = 2

	// ExitNotFound is a named resource the command could not locate.
	ExitNotFound = 3

	// ExitConflict is a request that cannot be satisfied against the
	// current state: a precondition failed, a write raced, an
	// identifier is already taken.
	ExitConflict = 4

	// ExitUnauthorized is an authentication or authorization refusal.
	// Permanent: the caller needs new credentials or a different
	// policy, not a retry. Distinct from ExitConsentRefused, which a
	// re-invocation with --confirm=yes clears.
	ExitUnauthorized = 5
)

// ExitCodeForClass is the single source of truth relating a standard
// class symbol to its numeric exit code.
//
// Expressed as data rather than as constants plus trailing comments
// because the string-to-number relationship is the thing consumers
// actually need, and a comment cannot be consumed. Every hand-maintained
// copy of this mapping in the tree existed because there was no table to
// read; a comment beside a constant is not a table.
//
// Scope is the classes kit itself owns. The conformance band's
// tool-specific slots (66 LEAK_DETECTED, 67 CONFIG, 68 GRADE_FAIL,
// 69 GRADE_UNGRADABLE) are deliberately absent: they are declared by the
// packages that own them, and listing them here would invert the
// dependency this package exists to avoid. ExtensionBand below records
// the allocation so a future slot cannot silently collide.
var exitCodeForClass = map[string]int{
	CodeOK:                ExitOK,
	CodeGeneric:           ExitGeneric,
	CodeUsage:             ExitUsage,
	CodeNotFound:          ExitNotFound,
	CodeConflict:          ExitConflict,
	CodeUnauthorized:      ExitUnauthorized,
	CodeTransient:         ExitTransient,
	CodeConsentRefused:    ExitConsentRefused,
	CodeRateLimited:       ExitRateLimited,
	CodeProvenanceMissing: ExitProvenanceMissing,
	CodePrerequisite:      ExitPrerequisite,
}

// ExitCodeForClass resolves a standard class symbol to its numeric exit
// code. The second result reports whether the class is one kit defines;
// it is false for adopter-defined and tool-specific codes.
//
// Callers that must produce a number for an unknown class decide their
// own fallback. This function does not pick one, because the previous
// hand-maintained copy of this table did — it returned 1 for anything it
// did not recognize, which turned "this class was added to kit and never
// copied here" into an assertion against exit 1 that looked like a real
// failure rather than a stale table.
func ExitCodeForClass(class string) (int, bool) {
	code, ok := exitCodeForClass[class]
	return code, ok
}

// ExitClasses returns the class symbols kit defines, in ascending
// exit-code order (ties broken by symbol). Callers rendering the
// taxonomy — documentation generators, a discovery endpoint's legend,
// a conformance harness reporting what it understands — iterate this
// rather than hard-coding rows, so a class added above appears without
// a second edit.
func ExitClasses() []string {
	out := make([]string, 0, len(exitCodeForClass))
	for class := range exitCodeForClass {
		out = append(out, class)
	}
	sort.Slice(out, func(i, j int) bool {
		if a, b := exitCodeForClass[out[i]], exitCodeForClass[out[j]]; a != b {
			return a < b
		}
		return out[i] < out[j]
	})
	return out
}

// ClassForExitCode returns the class symbol carrying the given exit
// code. The second result is false for codes kit does not define,
// including the tool-specific band slots owned by other packages.
//
// Not every numeric code has a unique class: the scenario-grader codes
// above reuse 1/2/4/5 deliberately. Only the standard classes are
// reversible, which is what this answers.
func ClassForExitCode(code int) (string, bool) {
	for _, class := range ExitClasses() {
		if exitCodeForClass[class] == code {
			return class, true
		}
	}
	return "", false
}

// ExtensionBandSlot is one allocated slot in kit's >6 extension band.
type ExtensionBandSlot struct {
	// Exit is the numeric code.
	Exit int
	// Class is the class symbol occupying the slot.
	Class string
	// Owner names the package that declares the constant. Slots owned
	// elsewhere are recorded by import path rather than imported, so
	// this leaf keeps its dependency shape.
	Owner string
}

// ExtensionBand records every allocated slot in kit's >6 band, including
// the slots owned by other packages.
//
// The spec reserves 0-6 for the shared taxonomy and leaves >6 to
// documented per-tool codes. Kit allocates its band contiguously so no
// two features claim the same number, but the allocation is spread
// across three trees — this package, go/console/cli/conformance, and
// go/conformance/client — and a contiguous allocation nobody can read in
// one place is an allocation that collides.
//
// The symbols for slots owned elsewhere are repeated as literals rather
// than imported, for the same reason this package repeats "json" and
// "yaml": importing them would make the envelope leaf depend on the
// conformance trees, which is the edge this package exists to cut. The
// drift guard in those packages asserts their own constants against
// these rows, so a slot renumbered on either side fails a test rather
// than silently double-booking.
func ExtensionBand() []ExtensionBandSlot {
	return []ExtensionBandSlot{
		{Exit: ExitRateLimited, Class: CodeRateLimited, Owner: "hop.top/kit/go/console/output/envelope"},
		{Exit: ExitProvenanceMissing, Class: CodeProvenanceMissing, Owner: "hop.top/kit/go/console/output/envelope"},
		{Exit: 66, Class: "LEAK_DETECTED", Owner: "hop.top/kit/go/console/cli/conformance"},
		{Exit: 67, Class: "CONFIG", Owner: "hop.top/kit/go/console/cli/conformance"},
		{Exit: 68, Class: "GRADE_FAIL", Owner: "hop.top/kit/go/conformance/client"},
		{Exit: 69, Class: "GRADE_UNGRADABLE", Owner: "hop.top/kit/go/conformance/client"},
		{Exit: ExitPrerequisite, Class: CodePrerequisite, Owner: "hop.top/kit/go/console/output/envelope"},
	}
}
