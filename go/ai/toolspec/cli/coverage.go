// coverage.go implements the annotation-coverage report over a
// reflected cobra tree: how many of a tool's commands declare a
// kit/side-effect class, which ones do not, and whether that
// fraction clears a threshold an adopter can gate on in CI.
//
// The report exists because the manifest alone cannot create
// pressure. Once an unannotated command stops masquerading as read
// (see [hop.top/kit/go/ai/cmdreflect.TierUnannotated]), an agent
// reading the manifest fails closed on it — correct, but silent
// from the adopter's side. Coverage makes the gap countable, names
// the commands, and gives CI a number to refuse below.

package cli

import (
	"fmt"
	"sort"
	"strings"

	"hop.top/kit/go/ai/cmdreflect"
	kitcli "hop.top/kit/go/console/cli"
)

// CoverageReport is the result of measuring annotation coverage
// over one tool's command tree.
//
// Total counts only the commands coverage can sensibly be measured
// on: runnable leaves, excluding command groups (which have no
// invocation to classify), framework built-ins (help, completion),
// and kit's own reserved management verbs (an adopter did not write
// `spec` and cannot annotate it). Measuring against every node in
// the tree would report a coverage figure no adopter could ever
// reach.
type CoverageReport struct {
	// Tool is the binary name.
	Tool string `json:"tool" yaml:"tool" table:"TOOL"`
	// Total is the number of measurable commands.
	Total int `json:"total" yaml:"total" table:"TOTAL"`
	// Annotated is how many of Total declared a kit/side-effect.
	Annotated int `json:"annotated" yaml:"annotated" table:"ANNOTATED"`
	// Unannotated is Total-Annotated, carried explicitly so a
	// consumer reading the JSON need not subtract.
	Unannotated int `json:"unannotated" yaml:"unannotated" table:"UNANNOTATED"`
	// Inferred counts commands with no annotation whose name tripped
	// kit's destructive-name heuristic (delete, rm, purge, …).
	//
	// These are counted as UNANNOTATED, not annotated: the heuristic
	// is kit guessing from a verb, which is exactly the kind of
	// inference this report exists to replace with a declaration.
	// The separate count is here so an adopter can see that the
	// riskiest part of the gap is at least being guessed at
	// conservatively.
	Inferred int `json:"inferred" yaml:"inferred" table:"INFERRED"`
	// Malformed counts commands whose kit/side-effect value is not
	// in the accepted vocabulary — a typo, not an omission. They are
	// not annotated for coverage purposes, because a value kit
	// cannot resolve carries no more information than silence.
	Malformed int `json:"malformed" yaml:"malformed" table:"MALFORMED"`
	// UnannotatedPaths lists the space-joined command paths that
	// declared nothing, sorted, so the report says WHICH commands to
	// fix rather than only how many.
	UnannotatedPaths []string `json:"unannotated_paths,omitempty" yaml:"unannotated_paths,omitempty"`
	// MalformedPaths lists the paths whose declared value did not
	// resolve, each rendered "path=value" so the typo is visible.
	MalformedPaths []string `json:"malformed_paths,omitempty" yaml:"malformed_paths,omitempty"`
	// Measurable reports whether the tree could be measured at all.
	// False when Total is 0 — a tool with no reflectable commands.
	//
	// It is a distinct field rather than an inference from Total
	// because 0/0 must never render as 0% coverage. A tool whose
	// tree a reporter could not read is UNMEASURABLE, which is a
	// different finding from a tool that annotates nothing, and
	// reporting the second when the first is true is the same class
	// of error as reporting an unannotated command as read.
	Measurable bool `json:"measurable" yaml:"measurable" table:"MEASURABLE"`
}

// Percent returns coverage as a percentage in [0,100], rounded to
// the nearest whole number the way a report renders it.
//
// An unmeasurable tree returns 0 — callers MUST check Measurable
// before showing the number, because 0 here means "no answer", not
// "no coverage". [CoverageReport.Meets] makes that distinction for
// the gating case.
func (r CoverageReport) Percent() float64 {
	if r.Total == 0 {
		return 0
	}
	return float64(r.Annotated) / float64(r.Total) * 100
}

// Meets reports whether coverage clears threshold (a percentage in
// [0,100]).
//
// An unmeasurable tree does NOT meet any threshold above zero. The
// alternative — passing a gate because there was nothing to check —
// turns a broken reporter into a green build, which is the failure
// mode this whole change exists to remove. A threshold of 0 passes
// unconditionally, so an adopter who has not started annotating can
// still mount the report without failing CI.
func (r CoverageReport) Meets(threshold float64) bool {
	if threshold <= 0 {
		return true
	}
	if !r.Measurable {
		return false
	}
	return r.Percent() >= threshold
}

// CoverageOf measures annotation coverage over root's command tree.
//
// A nil root yields an unmeasurable report rather than an empty
// measured one, so a caller that failed to build a tree cannot
// mistake the result for 0% coverage.
func CoverageOf(root *kitcli.Root) CoverageReport {
	if root == nil || root.Cmd == nil {
		return CoverageReport{}
	}
	rep := CoverageReport{Tool: root.Config.Name}

	// Reflect with every relaxation the manifest uses, so coverage
	// is measured over the same set of commands the manifest
	// publishes. Hidden and interactive leaves are counted: they
	// reach a transport or a human and their class matters just as
	// much.
	tree := cmdreflect.Reflect(
		root.Cmd,
		cmdreflect.WithReserved(root),
		cmdreflect.AllowHidden(),
		cmdreflect.AllowInteractive(),
		cmdreflect.AllowReserved(),
		cmdreflect.AllowDeprecated(),
	)

	for _, d := range tree.Descriptors {
		if !coverageMeasures(d) {
			continue
		}
		rep.Total++
		path := strings.Join(d.Path, " ")

		switch {
		case d.Safety.Tier == cmdreflect.TierUnknown:
			// A declared value kit could not resolve. Counted
			// against coverage: a typo is not a declaration.
			rep.Malformed++
			rep.MalformedPaths = append(rep.MalformedPaths,
				fmt.Sprintf("%s=%q", path, d.Safety.DeclaredSideEffect))
			rep.UnannotatedPaths = append(rep.UnannotatedPaths, path)
		case d.Safety.TierInferred:
			// No annotation; kit guessed destructive from the verb.
			rep.Inferred++
			rep.Unannotated++
			rep.UnannotatedPaths = append(rep.UnannotatedPaths, path)
		case !d.Safety.Tier.Declared():
			rep.Unannotated++
			rep.UnannotatedPaths = append(rep.UnannotatedPaths, path)
		default:
			rep.Annotated++
		}
	}

	// Malformed entries were pushed onto UnannotatedPaths above but
	// not onto the Unannotated count; fold them in now so the two
	// always agree with Total.
	rep.Unannotated += rep.Malformed
	rep.Measurable = rep.Total > 0

	sort.Strings(rep.UnannotatedPaths)
	sort.Strings(rep.MalformedPaths)
	return rep
}

// coverageMeasures reports whether d is a command an adopter could
// reasonably be asked to annotate.
//
// It mirrors [manifestIncludes] — the same leaves-only rule — with
// two differences. Deprecated leaves ARE measured: a deprecated
// command still runs when typed and still needs a class. Reserved
// kit verbs are NOT: the adopter did not write them and cannot
// annotate them, so counting them would put a ceiling on coverage
// that no amount of adopter work could lift.
func coverageMeasures(d *cmdreflect.Descriptor) bool {
	if d == nil || d.Cmd == nil || d.IsRoot() {
		return false
	}
	// Runnable is the test, not leafness: a command with children
	// that also has its own Run is invoked directly and needs a
	// class like any other. A pure group does not.
	if !d.Surface.Runnable {
		return false
	}
	if d.Surface.Builtin || d.Surface.Reserved || d.Surface.SpecCommand {
		return false
	}
	return true
}
