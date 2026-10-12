package cmdsurface

import (
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

// AnnotationWithholdFrom is the cobra annotation a command sets to keep
// itself off named surfaces: a comma-separated list of [Surface] names
// ("rest,sse,rpc,mcp"). On every bridge that discovers the command, the
// named surfaces are never enabled for it — not by the default enabled
// set, [Bridge.Expose], nor a surfaces.commands.<path>.enabled list —
// so discovery on those surfaces does not offer it and a call there is
// refused with [ErrSurfaceNotEnabled]. Every surface it does not name
// keeps the command: an event watch withheld from the served surfaces
// stays reachable over the socket unless "socket" is in the list too.
//
// It is the command author's declaration, as kit/self-hosting is, and
// unlike kit/self-hosting it is per surface. Configuration can still
// hide a command on any surface; it cannot serve one the command
// withholds itself from.
//
// "rest" covers everything the REST projection serves for the command,
// its /stream route included; "sse" is the separate [MountSSE] surface.
//
// A value that names an unknown surface does not parse: the kit-shipped
// services refuse to start ([ValidateWithholdFrom]), and a bridge that
// discovers the command anyway withholds it from every surface but
// [SurfaceCLI] and [SurfaceLib].
const AnnotationWithholdFrom = "kit/withhold-from"

// ReasonWithheldByCommand is the discovery reason for a command whose
// leaf is not exposed on SurfaceREST because the command itself names
// "rest" in [AnnotationWithholdFrom]. It is distinct from
// [ReasonWithheldByConfig] so an operator can tell "the command never
// serves here" from "this deployment chose not to".
const ReasonWithheldByCommand = "withheld-by-command"

// ParseWithholdFrom parses a kit/withhold-from value into the surfaces
// it names, in order, without duplicates. Blank entries are skipped; an
// entry that is not a [Surface] name is an error.
func ParseWithholdFrom(v string) ([]Surface, error) {
	var out []Surface
	seen := map[Surface]bool{}
	for _, part := range strings.Split(v, ",") {
		s := Surface(strings.TrimSpace(part))
		if s == "" || seen[s] {
			continue
		}
		if !s.IsValid() {
			return nil, fmt.Errorf("%s %q: unknown surface %q", AnnotationWithholdFrom, v, s)
		}
		seen[s] = true
		out = append(out, s)
	}
	return out, nil
}

// ValidateWithholdFrom refuses every command under root whose
// kit/withhold-from does not parse, naming each one.
func ValidateWithholdFrom(root *cobra.Command) error {
	var errs []error
	var walk func(*cobra.Command)
	walk = func(c *cobra.Command) {
		if raw, ok := c.Annotations[AnnotationWithholdFrom]; ok {
			if _, err := ParseWithholdFrom(raw); err != nil {
				errs = append(errs, fmt.Errorf("%s: %w", c.CommandPath(), err))
			}
		}
		for _, sub := range c.Commands() {
			walk(sub)
		}
	}
	if root != nil {
		walk(root)
	}
	return errors.Join(errs...)
}

// withheldOf resolves cmd's kit/withhold-from at discovery: nil when it
// declares none. A value that does not parse fails closed, withholding
// the command from every surface outside the process.
func withheldOf(cmd *cobra.Command) map[Surface]bool {
	raw := annotationOf(cmd, AnnotationWithholdFrom)
	if raw == "" {
		return nil
	}
	surfaces, err := ParseWithholdFrom(raw)
	if err != nil {
		out := map[Surface]bool{}
		for _, s := range AllSurfaces() {
			if s != SurfaceCLI && s != SurfaceLib {
				out[s] = true
			}
		}
		return out
	}
	if len(surfaces) == 0 {
		return nil
	}
	out := make(map[Surface]bool, len(surfaces))
	for _, s := range surfaces {
		out[s] = true
	}
	return out
}

// Withheld reports whether the command withholds itself from s through
// [AnnotationWithholdFrom]. Such a surface is never in Enabled.
func (l *Leaf) Withheld(s Surface) bool { return l.withheld[s] }
