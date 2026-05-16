package cmdsurface

import (
	"context"
	"fmt"
	"strings"
)

// The Library surface is the in-process Go API. It is the simplest
// surface in the package: callers already in the same process build
// an Invocation and hand it to Bridge.Invoke. The helpers in this
// file shave off the two pieces of ceremony that show up in adopter
// code over and over again:
//
//  1. Constructing an Invocation by parsing an argv-shaped slice
//     (the same shape cobra parses on the command line). Adopters
//     writing tests, REPLs, or internal automation tend to have argv
//     in hand and would otherwise reach for a tiny shell parser of
//     their own.
//  2. Forcing Meta.Surface = SurfaceLib so the policy gate keys off
//     the right surface. Calls that omit the field already default
//     to SurfaceLib in Bridge.Invoke; the helpers make the intent
//     explicit and override any incoming Meta.Surface.
//
// The helpers are free functions (not methods on *Bridge) so the
// foundation surface stays untouched and the lib surface remains
// purely additive — same shape every other surface follows.
//
// Example:
//
//	res, err := cmdsurface.InvokeArgs(ctx, b,
//	    []string{"widget", "add", "--name", "foo"})
//
// or equivalently:
//
//	res, err := cmdsurface.InvokeArgs(ctx, b,
//	    []string{"widget", "add"},
//	    cmdsurface.WithFlag("name", "foo"))

// InvokeOption configures an InvokeArgs / StreamArgs call. Options
// run after argv parsing, so values set here override whatever the
// argv parser produced.
type InvokeOption func(*invokeBuilder)

// invokeBuilder is the mutable state an InvokeOption operates on.
// It is private; callers shape it through the WithX helpers.
type invokeBuilder struct {
	flags  map[string]any
	caller string
	trace  string
	extra  map[string]string
}

// WithFlag sets flag name to value, overriding any value the argv
// parser produced for the same name. Use this to inject typed
// values (numbers, bools, structs) that the bare argv form cannot
// express, or to override a value programmatically.
func WithFlag(name string, value any) InvokeOption {
	return func(b *invokeBuilder) {
		if b.flags == nil {
			b.flags = make(map[string]any)
		}
		b.flags[name] = value
	}
}

// WithCaller sets Meta.Caller. The caller string is opaque to the
// bridge; adopters use it to record the originating principal in
// audit logs.
func WithCaller(caller string) InvokeOption {
	return func(b *invokeBuilder) { b.caller = caller }
}

// WithTraceID sets Meta.TraceID, propagating a request/trace
// identifier into the runner and audit sinks.
func WithTraceID(id string) InvokeOption {
	return func(b *invokeBuilder) { b.trace = id }
}

// WithExtra adds a key/value pair to Meta.Extra. Repeated calls
// with the same key overwrite previous values. The map is created
// lazily, so a caller that never calls WithExtra leaves Meta.Extra
// nil.
func WithExtra(key, value string) InvokeOption {
	return func(b *invokeBuilder) {
		if b.extra == nil {
			b.extra = make(map[string]string)
		}
		b.extra[key] = value
	}
}

// InvokeArgs parses argv as a CLI-style invocation against b's leaf
// set and runs the resolved leaf through Bridge.Invoke with
// Meta.Surface forced to SurfaceLib.
//
// The argv parser walks b.Leaves() to find the longest leaf-path
// prefix of argv; any remaining tokens become positional Args and
// --flag pairs (--name value, --name=value, or bare --bool).
// Options run after the parse and may override flag values or set
// Meta fields.
//
// Errors:
//
//   - ErrUnknownCommand: no leaf prefix of argv matched.
//   - ErrSurfaceNotEnabled: the resolved leaf does not have
//     SurfaceLib in its Enabled set (which, under DefaultPolicy, it
//     does — this surfaces only when adopters explicitly hide the
//     leaf from the lib surface via config or Bridge.Hide).
//   - ErrDestructiveBlocked: defensive, since DefaultPolicy.Allowed
//     already permits destructive leaves on SurfaceLib; this error
//     can only arise if a custom Policy.Allowed implementation
//     refuses SurfaceLib.
func InvokeArgs(ctx context.Context, b *Bridge, argv []string, opts ...InvokeOption) (Result, error) {
	inv, err := buildLibInvocation(b, argv, opts)
	if err != nil {
		return Result{}, err
	}
	return b.Invoke(ctx, inv)
}

// StreamArgs is the streaming counterpart of InvokeArgs. It parses
// argv, builds the same Invocation, and hands it to b.Runner().Stream
// after applying the bridge's policy gate (leaf resolution + surface
// enablement + destructive check). Events flow on out; the Runner
// closes out when streaming completes.
//
// StreamArgs returns the first non-nil error from leaf resolution,
// the policy gate, or the Runner. After the policy gate passes,
// transport errors flow through as the Runner produced them.
func StreamArgs(ctx context.Context, b *Bridge, argv []string, out chan<- Event, opts ...InvokeOption) error {
	inv, err := buildLibInvocation(b, argv, opts)
	if err != nil {
		return err
	}
	// Re-run the same policy checks Bridge.Invoke applies. Stream
	// bypasses Bridge.Invoke so we have to gate explicitly; without
	// this, an adopter could reach a destructive leaf via the
	// streaming path while the synchronous path refuses.
	leaf, err := b.resolveLeaf(inv.Path)
	if err != nil {
		return err
	}
	if !leaf.Enabled[inv.Meta.Surface] {
		return errSurfaceDisabled(leaf, inv.Meta.Surface)
	}
	if !b.cfg.policy.Allowed(leaf.Class, inv.Meta.Surface) {
		return errDestructiveBlocked(leaf, inv.Meta.Surface)
	}
	return b.cfg.runner.Stream(ctx, inv, out)
}

// buildLibInvocation parses argv, applies opts, and returns the
// resolved Invocation. Returns ErrUnknownCommand when argv does not
// start with a known leaf path.
func buildLibInvocation(b *Bridge, argv []string, opts []InvokeOption) (Invocation, error) {
	path, rest, ok := splitLeafPath(b, argv)
	if !ok {
		return Invocation{}, errUnknownCommand(argv)
	}
	args, flags := parseArgsAndFlags(rest)

	builder := invokeBuilder{flags: flags}
	for _, o := range opts {
		o(&builder)
	}

	inv := Invocation{
		Path:  path,
		Args:  args,
		Flags: builder.flags,
		Meta: Meta{
			Surface: SurfaceLib,
			Caller:  builder.caller,
			TraceID: builder.trace,
			Extra:   builder.extra,
		},
	}
	return inv, nil
}

// splitLeafPath returns the longest prefix of argv that matches a
// leaf in b. The matching leaf's Path is returned (a fresh slice
// the caller may keep); rest is whatever argv tokens followed the
// matched prefix. ok is false when no leaf prefix matched.
func splitLeafPath(b *Bridge, argv []string) (path, rest []string, ok bool) {
	leaves := b.Leaves()
	best := -1
	var bestLeaf *Leaf
	for _, leaf := range leaves {
		n := len(leaf.Path)
		if n == 0 || n > len(argv) {
			continue
		}
		match := true
		for i, seg := range leaf.Path {
			if argv[i] != seg {
				match = false
				break
			}
		}
		if !match {
			continue
		}
		if n > best {
			best = n
			bestLeaf = leaf
		}
	}
	if bestLeaf == nil {
		return nil, nil, false
	}
	path = append([]string(nil), bestLeaf.Path...)
	rest = append([]string(nil), argv[best:]...)
	return path, rest, true
}

// parseArgsAndFlags splits tokens into positional args and flag
// pairs. Recognized flag forms:
//
//	--name value   (two tokens: long flag + value)
//	--name=value   (one token: equals form)
//	--name         (bare flag → true; treated as boolean)
//
// Non-flag tokens become positional Args. A "--" token terminates
// flag parsing: every subsequent token is a positional arg, even
// if it begins with "--". An unmatched trailing flag (last token
// is "--name" with no value) is treated as bare → true.
//
// Returns nil maps when there are no flags so the produced
// Invocation does not advertise an empty Flags map to surfaces
// that distinguish "unset" from "empty".
func parseArgsAndFlags(tokens []string) ([]string, map[string]any) {
	var args []string
	var flags map[string]any
	endOfFlags := false
	for i := 0; i < len(tokens); i++ {
		tok := tokens[i]
		if endOfFlags {
			args = append(args, tok)
			continue
		}
		if tok == "--" {
			endOfFlags = true
			continue
		}
		if !strings.HasPrefix(tok, "--") {
			args = append(args, tok)
			continue
		}
		// --name or --name=value
		name := tok[2:]
		var value any
		if eq := strings.IndexByte(name, '='); eq >= 0 {
			value = name[eq+1:]
			name = name[:eq]
		} else if i+1 < len(tokens) && !strings.HasPrefix(tokens[i+1], "--") {
			value = tokens[i+1]
			i++
		} else {
			value = true
		}
		if name == "" {
			// Defensive: "--=value" or "--" already handled above.
			// Treat as a positional rather than a malformed flag.
			args = append(args, tok)
			continue
		}
		if flags == nil {
			flags = make(map[string]any)
		}
		flags[name] = value
	}
	return args, flags
}

// errUnknownCommand renders an ErrUnknownCommand for argv. Kept
// out-of-line so the hot path in InvokeArgs stays terse.
func errUnknownCommand(argv []string) error {
	return fmt.Errorf("%w: no leaf matches %s", ErrUnknownCommand, joinPath(argv))
}

// errSurfaceDisabled renders an ErrSurfaceNotEnabled for leaf+s.
func errSurfaceDisabled(leaf *Leaf, s Surface) error {
	return fmt.Errorf("%w: %s on %s", ErrSurfaceNotEnabled, leaf.PathKey(), s)
}

// errDestructiveBlocked renders an ErrDestructiveBlocked for leaf+s.
func errDestructiveBlocked(leaf *Leaf, s Surface) error {
	return fmt.Errorf("%w: %s on %s", ErrDestructiveBlocked, leaf.PathKey(), s)
}
