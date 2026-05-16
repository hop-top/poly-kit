package cmdsurface

import (
	"context"
	"strings"
)

// Sink consumes the outcome of a command invocation for logging,
// auditing, or downstream fan-out. Sinks are orthogonal to surfaces:
// any surface's Result can fan out to one or more Sinks.
//
// Implementations must be safe for concurrent use.
type Sink interface {
	Emit(ctx context.Context, inv Invocation, res Result, err error) error
}

// SinkSpec filters which invocations a Sink receives. The Surfaces
// and Paths fields are AND-combined: an Invocation must satisfy both
// (when set) to reach the wrapped Sink. The OnError / OnOK flags
// select by outcome; at least one must be true for the spec to match
// any invocation.
type SinkSpec struct {
	// Sink receives matching invocations. Required.
	Sink Sink
	// OnError selects invocations where err != nil or
	// Result.ExitCode != 0.
	OnError bool
	// OnOK selects invocations where err == nil and
	// Result.ExitCode == 0.
	OnOK bool
	// Surfaces is the surface allow-set. Empty means every surface.
	Surfaces []Surface
	// Paths is the pattern set ("widget *", "report.purge", "*").
	// Empty means every path. Patterns follow the same dotted-path
	// matcher used by Bridge.Expose / Bridge.Hide.
	Paths []string
}

// matches reports whether s would emit for the given invocation.
func (s SinkSpec) matches(inv Invocation, res Result, err error) bool {
	failed := err != nil || res.ExitCode != 0
	if failed && !s.OnError {
		return false
	}
	if !failed && !s.OnOK {
		return false
	}
	if len(s.Surfaces) > 0 {
		hit := false
		for _, sf := range s.Surfaces {
			if sf == inv.Meta.Surface {
				hit = true
				break
			}
		}
		if !hit {
			return false
		}
	}
	if len(s.Paths) > 0 {
		hit := false
		for _, p := range s.Paths {
			if sinkMatchPath(p, inv.Path) {
				hit = true
				break
			}
		}
		if !hit {
			return false
		}
	}
	return true
}

// SinkSet is a sequence of SinkSpecs. Emit iterates the set and
// dispatches each matching sink; errors are collected, not returned
// as fatal — sinks must be best-effort.
type SinkSet []SinkSpec

// Emit dispatches inv/res/err to every matching SinkSpec in s. It
// does NOT short-circuit on the first error: every matching sink is
// called, and the slice of observed errors is returned (nil if all
// succeeded). The order of the returned errors matches the order of
// matching sinks in s.
func (s SinkSet) Emit(ctx context.Context, inv Invocation, res Result, err error) []error {
	var errs []error
	for _, spec := range s {
		if spec.Sink == nil {
			continue
		}
		if !spec.matches(inv, res, err) {
			continue
		}
		if e := spec.Sink.Emit(ctx, inv, res, err); e != nil {
			errs = append(errs, e)
		}
	}
	return errs
}

// sinkMatchPath reports whether path matches pattern. Patterns
// accept either space-separated ("widget add") or dotted
// ("widget.add") segment forms; the final segment may be "*" to
// match any descendant, and a single "*" matches every path.
//
// This duplicates the bridge's matchPattern intentionally — sinks
// commonly accept the dotted form callers use in config, and the
// bridge helper is unexported.
func sinkMatchPath(pattern string, path []string) bool {
	pattern = strings.TrimSpace(pattern)
	if pattern == "" {
		return false
	}
	if pattern == "*" {
		return true
	}
	pat := sinkSplitPattern(pattern)
	if len(pat) == 0 {
		return false
	}
	if pat[len(pat)-1] == "*" {
		prefix := pat[:len(pat)-1]
		if len(path) < len(prefix) {
			return false
		}
		for i, seg := range prefix {
			if path[i] != seg {
				return false
			}
		}
		return true
	}
	if len(pat) != len(path) {
		return false
	}
	for i, seg := range pat {
		if path[i] != seg {
			return false
		}
	}
	return true
}

// sinkSplitPattern splits pattern on either '.' or whitespace,
// accepting both "widget.add" and "widget add" forms.
func sinkSplitPattern(pattern string) []string {
	if strings.ContainsRune(pattern, '.') {
		return strings.Split(pattern, ".")
	}
	return strings.Fields(pattern)
}
