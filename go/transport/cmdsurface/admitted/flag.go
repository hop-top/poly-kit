package admitted

import (
	"strings"

	"github.com/spf13/pflag"
)

// FlagState is the value and Changed bit one flag had at a moment a
// runner wants to return to: for the in-process runner, the state
// the operator's own command line left when the runner was built.
// Its fields are opaque; [CaptureFlag] records one and [RestoreFlag]
// puts it back.
//
// CaptureFlag, FlagState, RestoreFlag and [ResetFlagToDefault] are
// runner primitives: the one implementation of pflag's edge cases
// that every path re-parsing a tree shares — slice flags, which pflag
// appends to on every Set after the first; callback-backed flags,
// whose Set is the adopter's own function; and values whose String
// form does not round-trip through Set.
type FlagState struct {
	value   string
	slice   []string
	isSlice bool
	changed bool
}

// CaptureFlag records f's current value and Changed bit.
func CaptureFlag(f *pflag.Flag) FlagState {
	st := FlagState{value: f.Value.String(), changed: f.Changed}
	if sv, ok := f.Value.(pflag.SliceValue); ok {
		st.isSlice = true
		st.slice = append([]string(nil), sv.GetSlice()...)
	}
	return st
}

// RestoreFlag puts one flag back to st. A flag with no recorded state
// (st nil: registered after the baseline was taken) goes to its
// declared default with Changed cleared. passed says the coming
// invocation sets the flag itself: a slice flag is then emptied
// instead, so the invocation's values do not stack on the baseline.
// Callback-backed flags (pflag Func and BoolFunc) keep their value:
// their Set is the adopter's callback, and a restore would invoke it.
func RestoreFlag(f *pflag.Flag, st *FlagState, passed bool) {
	// Changed is bookkeeping pflag owns, not part of the value, so it
	// is restored for every flag — including the callback-backed ones
	// that return early below. Set first so no later branch can skip
	// it.
	f.Changed = st != nil && st.changed

	// A callback-backed flag's Set IS the adopter's function, so its
	// value is left alone: restoring it would invoke that function
	// with a value the operator never passed.
	switch f.Value.Type() {
	case "func", "boolfunc":
		return
	}
	if sv, ok := f.Value.(pflag.SliceValue); ok {
		switch {
		case passed:
			_ = sv.Replace(nil)
		case st != nil && st.isSlice:
			_ = sv.Replace(append([]string(nil), st.slice...))
		default:
			_ = sv.Replace(defaultSlice(f.DefValue))
		}
	} else {
		target := f.DefValue
		if st != nil {
			target = st.value
		}
		// A Value whose String form does not round-trip through Set
		// (pflag's nil IP prints "<nil>") falls back to its default.
		if err := f.Value.Set(target); err != nil && target != f.DefValue {
			_ = f.Value.Set(f.DefValue)
		}
	}
}

// ResetFlagToDefault returns one flag to its declared default and
// clears its Changed bit: the flag state an admitted run starts from
// for a flag no baseline recorded, and the state a flag had before any
// command line was parsed onto it. A callback-backed flag (pflag Func,
// BoolFunc) keeps its value, but its Changed bit is cleared like any
// other. It is RestoreFlag(f, nil, false); a nil f is a no-op.
func ResetFlagToDefault(f *pflag.Flag) {
	if f == nil {
		return
	}
	RestoreFlag(f, nil, false)
}

// defaultSlice parses the "[a,b]" form pflag prints as a slice flag's
// default back into its elements.
func defaultSlice(def string) []string {
	def = strings.TrimSuffix(strings.TrimPrefix(def, "["), "]")
	if def == "" {
		return nil
	}
	return strings.Split(def, ",")
}
