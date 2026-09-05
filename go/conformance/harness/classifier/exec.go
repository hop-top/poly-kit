package classifier

import (
	xrrexec "hop.top/xrr/adapters/exec"
)

// ExecClassifier is the adopter-supplied function the harness uses
// to classify exec interactions. A subprocess is opaque to xrr; the
// classifier translates argv into one of the Class values.
//
// The conservative default treats every exec call as Write (i.e.
// mutating). Adopters who know their subprocess catalog override
// via harness.WithExecClassifier.
type ExecClassifier func(argv []string) Class

// DefaultExecClassifier returns Write for every non-empty argv —
// a conservative posture. Empty argv (which should never happen
// in practice) returns Unknown.
func DefaultExecClassifier(argv []string) Class {
	if len(argv) == 0 {
		return ClassUnknown
	}
	return ClassWrite
}

// ClassifyExec routes through the supplied classifier. A nil
// classifier falls back to DefaultExecClassifier.
func ClassifyExec(req *xrrexec.Request, fn ExecClassifier) Class {
	if req == nil {
		return ClassUnknown
	}
	if fn == nil {
		fn = DefaultExecClassifier
	}
	return fn(req.Argv)
}
