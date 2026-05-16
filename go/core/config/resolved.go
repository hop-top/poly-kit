package config

import (
	"fmt"
	"sort"
	"strings"
)

// Origin tags which precedence layer produced a Resolved value.
// Numerically ordered low-to-high (Default lowest, Flag highest) so callers
// can compare with `<` / `>` if they need to reason about precedence.
type Origin int

const (
	OriginDefault Origin = iota
	OriginGlobal
	OriginProject
	OriginEnv
	OriginFlag
)

// String returns the lower-case origin label used in CLI output
// (matches rlz's `Source` strings for backward-compat).
func (o Origin) String() string {
	switch o {
	case OriginDefault:
		return "default"
	case OriginGlobal:
		return "global"
	case OriginProject:
		return "project"
	case OriginEnv:
		return "env"
	case OriginFlag:
		return "flag"
	}
	return fmt.Sprintf("origin(%d)", int(o))
}

// Resolved pairs a value with the origin layer that supplied it.
// Detail is a human-readable pointer to the exact source (file path, env var
// name, flag name) — useful for `config show` style commands.
type Resolved[T any] struct {
	Key    string
	Value  T
	Origin Origin
	Detail string
}

// FormatResolved renders a single resolved entry as `value (origin)`.
// Used by `<tool> config show` style commands.
func FormatResolved[T any](r Resolved[T]) string {
	return fmt.Sprintf("%v (%s)", r.Value, r.Origin)
}

// FormatResolvedTable renders a slice of string-typed Resolved entries as a
// left-aligned table with KEY/VALUE/ORIGIN/DETAIL columns. Mirrors rlz's
// FormatResolved table so migration is a drop-in.
func FormatResolvedTable(entries []Resolved[string]) string {
	maxKey, maxVal, maxOrigin := len("KEY"), len("VALUE"), len("ORIGIN")
	for _, e := range entries {
		if len(e.Key) > maxKey {
			maxKey = len(e.Key)
		}
		if len(e.Value) > maxVal {
			maxVal = len(e.Value)
		}
		if l := len(e.Origin.String()); l > maxOrigin {
			maxOrigin = l
		}
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "%-*s  %-*s  %-*s  %s\n",
		maxKey, "KEY", maxVal, "VALUE", maxOrigin, "ORIGIN", "DETAIL")
	fmt.Fprintln(&sb, strings.Repeat("-", maxKey+maxVal+maxOrigin+30))
	for _, e := range entries {
		fmt.Fprintf(&sb, "%-*s  %-*s  %-*s  %s\n",
			maxKey, e.Key, maxVal, e.Value, maxOrigin, e.Origin, e.Detail)
	}
	return sb.String()
}

// ResolvedAsJSON renders entries as a JSON-friendly map keyed by Key.
func ResolvedAsJSON(entries []Resolved[string]) map[string]map[string]any {
	out := make(map[string]map[string]any, len(entries))
	for _, e := range entries {
		out[e.Key] = map[string]any{
			"value":  e.Value,
			"origin": e.Origin.String(),
			"detail": e.Detail,
		}
	}
	return out
}

// SortedByKey returns entries sorted alphabetically by Key (stable).
func SortedByKey(entries []Resolved[string]) []Resolved[string] {
	out := make([]Resolved[string], len(entries))
	copy(out, entries)
	sort.SliceStable(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out
}
