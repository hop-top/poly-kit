package cli

import (
	"errors"
	"fmt"
	"log/slog"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"hop.top/kit/go/transport/cmdsurface"
)

// WithRootFactory makes every served invocation run on a tree of its
// own, built by build, instead of on the serving Root's tree. The
// kit-shipped api and socket services then hand the bridge a
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
//			cli.WithSocket(cli.SocketConfig{}),
//			cli.WithRootFactory(newRoot),
//		)
//		root.Cmd.AddCommand(widgetCmd())
//		return root
//	}
//
// Each tree build returns is prepared with [Root.Prepare] before it
// runs, so the confirmation and policy gates, the auto-registered
// flags, and validation apply exactly as on the CLI; and the
// persistent root flags the operator set on the serving command line
// (`mytool --no-color -c key=val serve socket`) are replayed onto it,
// so a served invocation starts from the same state it would on the
// shared tree. The factory is exercised once when the service
// validates, so a build that cannot produce a usable tree is a
// configuration error at exit 2 rather than a per-request failure.
func WithRootFactory(build func() *Root) func(*Root) {
	return func(r *Root) { r.rootFactory = build }
}

// Prepare installs on the command tree everything Execute installs
// before it parses argv — the alias annotations, the completion
// command, leaf help, the kit-managed flags, the help addenda, and
// the RunE middleware chain that carries the confirmation, policy,
// idempotency, and error-envelope gates ([Root.WrapRunE]) — and runs
// the same validation, without executing anything and without
// touching cobra's process-global initializer list.
//
// It is the pre-flight half of Execute for a tree that will be
// executed by something other than Execute: a root factory's trees,
// or an adopter's own dispatch loop over r.Cmd. Execute performs the
// same steps itself, so a Root that goes through Execute needs no
// Prepare, and calling both is harmless: every step is idempotent.
//
// Unlike Execute, a validation failure is returned rather than
// dispatched through Config.ValidationFailureMode — a *ValidationError
// when Config.EnforceValidate is set and the tree fails it, a
// *SignatureReportError under SignatureStrictnessReject. Under
// SignatureStrictnessWarn the violations are logged, as Execute logs
// them. When Prepare returns an error the gates are not installed.
func (r *Root) Prepare() error {
	if r == nil || r.Cmd == nil {
		return errors.New("cli: Prepare on a Root with no command")
	}
	r.prepareTree()
	if err := r.validateTree(); err != nil {
		return err
	}
	switch r.Config.SignatureStrictness {
	case SignatureStrictnessWarn:
		// Logs every violation and never dispatches.
		_, _ = r.dispatchSignatureReport()
	case SignatureStrictnessReject:
		if report := r.ValidateSignature(); report.HasViolations() {
			return &SignatureReportError{Report: report}
		}
	}
	r.WrapRunE()
	return nil
}

// serveRunnerOptions returns the bridge option that runs served
// invocations on factory-built trees, or nil when no factory is set
// so the bridge keeps its shared-tree runner. It is called when a
// kit-shipped service starts, which is when the serving command line
// has been parsed: the operator's root flags are captured here.
func (r *Root) serveRunnerOptions() []cmdsurface.Option {
	if r.rootFactory == nil {
		return nil
	}
	globals := captureOperatorGlobals(r.Cmd)
	runner := cmdsurface.InProcessRunner(nil,
		cmdsurface.WithRootFactory(r.invocationRoot(globals)))
	return []cmdsurface.Option{cmdsurface.WithRunner(runner)}
}

// validateRootFactory builds and prepares one tree through the
// factory, so a factory that returns nil, returns the serving Root,
// or builds a tree that fails validation is refused when the service
// validates — a usage error before anything binds — rather than
// failing every request after it has.
func (r *Root) validateRootFactory() error {
	if r.rootFactory == nil {
		return nil
	}
	_, err := r.buildInvocationRoot(nil)
	return err
}

// invocationRoot returns the factory the runner calls per invocation:
// one prepared tree carrying the operator's globals. A factory that
// cannot produce a tree yields nil, which the runner reports as a
// failed invocation; the reason is logged, since the runner's
// factory contract has no channel for it and validateRootFactory has
// already refused the deterministic cases.
func (r *Root) invocationRoot(globals []operatorGlobal) func() *cobra.Command {
	return func() *cobra.Command {
		fresh, err := r.buildInvocationRoot(globals)
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
func (r *Root) buildInvocationRoot(globals []operatorGlobal) (*Root, error) {
	fresh := r.rootFactory()
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
