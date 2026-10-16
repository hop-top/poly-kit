package service

import (
	"errors"
	"fmt"
	"log/slog"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"hop.top/kit/go/console/cli"
	"hop.top/kit/go/transport/cmdsurface"
)

// WithRootFactory makes every served invocation run on a tree of its
// own, built by build, instead of on the serving Root's tree. The
// kit-shipped api, socket, and mcp services then hand the bridge a
// per-invocation runner ([cmdsurface.WithRootFactory]): nothing is
// shared between invocations, nothing is reset, and they run in
// parallel. Without this option the services keep the shared-tree
// runner, which serializes invocations.
//
// build is the tool's own construction — cli.New plus every command
// the tool mounts — and it runs once per served invocation, so it
// must be cheap and must return a Root that shares no mutable state
// with the ones before it: no flag bound to a package-level variable,
// no closure over a struct another invocation writes. Dependencies
// that are expensive to build or must be shared (a database handle,
// a client) belong outside build, and must then be safe for
// concurrent use. The function may name itself:
//
//	func newRoot() *cli.Root {
//		root := cli.New(cli.Config{Name: "mytool", Version: "1.0.0"},
//			service.WithSocket(service.SocketConfig{}),
//			service.WithRootFactory(newRoot),
//		)
//		root.Cmd.AddCommand(widgetCmd())
//		return root
//	}
//
// Each tree build returns is prepared with [cli.Root.Prepare] before it
// runs, so the confirmation and policy gates, the auto-registered
// flags, and validation apply exactly as on the CLI; and the
// persistent root flags the operator set on the serving command line
// (`mytool --no-color -c key=val serve socket`) are replayed onto it,
// so a served invocation starts from the same state it would on the
// shared tree. The factory is exercised once when the service
// validates, so a build that cannot produce a usable tree is a
// configuration error at exit 2 rather than a per-request failure.
func WithRootFactory(build func() *cli.Root) func(*cli.Root) {
	return func(r *cli.Root) { stateOf(r).rootFactory = build }
}

// serveRunnerOptions returns the bridge option that runs served
// invocations on factory-built trees, or nil when no factory is set
// so the bridge keeps its shared-tree runner. It is called when a
// kit-shipped service starts, which is when the serving command line
// has been parsed: the operator's root flags are captured here.
func serveRunnerOptions(r *cli.Root) []cmdsurface.Option {
	if stateOf(r).rootFactory == nil {
		return nil
	}
	globals := captureOperatorGlobals(r.Cmd)
	runner := cmdsurface.InProcessRunner(nil,
		cmdsurface.WithRootFactory(invocationRoot(r, globals)))
	return []cmdsurface.Option{cmdsurface.WithRunner(runner)}
}

// validateRootFactory builds and prepares one tree through the
// factory, so a factory that returns nil, returns the serving Root,
// or builds a tree that fails validation is refused when the service
// validates — a usage error before anything binds — rather than
// failing every request after it has.
func validateRootFactory(r *cli.Root) error {
	if stateOf(r).rootFactory == nil {
		return nil
	}
	_, err := buildInvocationRoot(r, nil)
	return err
}

// invocationRoot returns the factory the runner calls per invocation:
// one prepared tree carrying the operator's globals. A factory that
// cannot produce a tree yields nil, which the runner reports as a
// failed invocation; the reason is logged, since the runner's
// factory contract has no channel for it and validateRootFactory has
// already refused the deterministic cases.
func invocationRoot(r *cli.Root, globals []operatorGlobal) func() *cobra.Command {
	return func() *cobra.Command {
		fresh, err := buildInvocationRoot(r, globals)
		if err != nil {
			slog.Error("cli: served invocation has no tree",
				"tool", r.Config.Name, "error", err)
			return nil
		}
		return fresh.Cmd
	}
}

// buildInvocationRoot runs the factory once and makes its tree ready
// to execute: prepared, and carrying the operator's globals.
func buildInvocationRoot(r *cli.Root, globals []operatorGlobal) (*cli.Root, error) {
	fresh := stateOf(r).rootFactory()
	if fresh == nil || fresh.Cmd == nil {
		return nil, errors.New("root factory: returned no root")
	}
	if fresh == r || fresh.Cmd == r.Cmd {
		return nil, errors.New("root factory: returned the serving root; it must build a new one on every call")
	}
	if err := fresh.Prepare(); err != nil {
		return nil, fmt.Errorf("root factory: prepare: %w", err)
	}
	replayOperatorGlobals(fresh.Cmd, globals)
	return fresh, nil
}

// operatorGlobal is one persistent root flag the operator set on the
// serving command line, captured when the service starts so a
// factory-built tree starts from the state the shared tree's baseline
// would give it.
type operatorGlobal struct {
	name   string
	values []string
	slice  bool
}

// captureOperatorGlobals records every persistent root flag whose
// Changed bit is set. Callback-backed flags (pflag Func and BoolFunc)
// are skipped: their Set IS the adopter's callback, and replaying it
// per invocation would run the callback again.
func captureOperatorGlobals(root *cobra.Command) []operatorGlobal {
	var out []operatorGlobal
	root.PersistentFlags().VisitAll(func(f *pflag.Flag) {
		if !f.Changed {
			return
		}
		switch f.Value.Type() {
		case "func", "boolfunc":
			return
		}
		g := operatorGlobal{name: f.Name}
		if sv, ok := f.Value.(pflag.SliceValue); ok {
			g.slice = true
			g.values = append([]string(nil), sv.GetSlice()...)
		} else {
			g.values = []string{f.Value.String()}
		}
		out = append(out, g)
	})
	return out
}

// replayOperatorGlobals sets the captured globals on a fresh tree's
// persistent root flags, Changed bit included, so viper bindings
// report them the way they report a parsed flag. A flag the fresh
// tree does not declare is skipped.
func replayOperatorGlobals(root *cobra.Command, globals []operatorGlobal) {
	pf := root.PersistentFlags()
	for _, g := range globals {
		f := pf.Lookup(g.name)
		if f == nil || len(g.values) == 0 {
			continue
		}
		if sv, ok := f.Value.(pflag.SliceValue); ok && g.slice {
			_ = sv.Replace(append([]string(nil), g.values...))
			f.Changed = true
			continue
		}
		_ = pf.Set(g.name, g.values[0])
	}
}
