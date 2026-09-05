package cmdsurface

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"hop.top/kit/go/ai/cmdreflect"
)

// ErrNotInvocable is returned when the resolved leaf can never run
// under a runner: an interactive command, because a runner captures
// the streams and supplies no terminal; or a self-hosting one,
// because the runner IS the process the command would start a server
// inside of or replace. The wrapped message names the command and
// the reflector's reason.
//
// Discovery already reports both classes with `invocable: false`;
// this is the execution-side guarantee for a caller that addresses
// one anyway.
var ErrNotInvocable = errors.New("cmdsurface: command not invocable through a runner")

// InProcessRunner returns a Runner that invokes a cobra tree in the
// current process.
//
// With a bare root, every invocation runs on that one shared tree.
// Cobra and pflag keep argv, writers, the command context, and every
// parsed flag value on the tree itself, so the runner serializes
// invocations on a mutex and, around each one, resets the leaf's
// flag chain to the state it had when the runner was built, points
// the command at the caller's context, and gives it an empty stdin.
// One invocation at a time is the throughput consequence; see
// [WithRootFactory] for the alternative.
//
// The runner does not mutate the tree beyond that: writers, argv,
// context, stdin, and the silence bits are restored on return.
func InProcessRunner(root *cobra.Command, opts ...RunnerOption) Runner {
	r := &inProcessRunner{root: root}
	for _, o := range opts {
		o(r)
	}
	if r.newRoot == nil && root != nil {
		r.baseline = captureFlagState(root)
	}
	return r
}

// RunnerOption configures [InProcessRunner].
type RunnerOption func(*inProcessRunner)

// WithRootFactory makes the runner build a fresh cobra tree for every
// invocation instead of re-entering one shared root. Each invocation
// then owns its argv, writers, context, and flag values outright:
// nothing is reset, nothing is serialized, and invocations run in
// parallel.
//
// The factory must return a tree that shares no mutable state with
// the trees it returned before — in particular no flag bound to a
// package-level variable — or the isolation is only nominal. A tree
// built by [hop.top/kit/go/console/cli.New] is prepared for
// execution inside Root.Execute (the confirmation and policy gates
// are installed there), so a factory over cli.New yields an ungated
// tree; use the shared-root form for a kit root until cli exposes a
// prepare-without-execute hook.
//
// When a factory is set the root passed to InProcessRunner is ignored
// and may be nil.
func WithRootFactory(newRoot func() *cobra.Command) RunnerOption {
	return func(r *inProcessRunner) { r.newRoot = newRoot }
}

type inProcessRunner struct {
	root     *cobra.Command
	newRoot  func() *cobra.Command
	baseline map[*pflag.Flag]flagState
	mu       sync.Mutex
}

// formatFlag is the output package's --format flag, the one an
// invocation uses to ask for a rendering. jsonFormat is the
// structured rendering the runner selects on the caller's behalf
// when the command declares an output schema and the caller named
// none.
const (
	formatFlag = "format"
	jsonFormat = "json"
)

// execution is one invocation prepared on a tree: the leaf resolved,
// the refusals applied, argv built, flag state reset, and the
// caller's context, an empty stdin, and the silence bits installed.
// release undoes every mutation and, on a shared tree, drops the
// mutex.
type execution struct {
	root *cobra.Command
	leaf *cobra.Command
	args []string
	// structured reports that stdout will carry the declared output
	// in json, and the Result should decode it into Data.
	structured bool
	// selected reports that the runner, not the caller, chose json:
	// the caller named no format, so the JSON text on stdout exists
	// only to produce Data and is not a rendering anyone asked for.
	selected bool

	restores []func()
	release  func()
}

// prepare resolves inv on the runner's tree and makes the tree ready
// to execute it. On a shared tree the runner's mutex is held from
// here until release; on a factory tree nothing is shared.
func (r *inProcessRunner) prepare(ctx context.Context, inv Invocation) (*execution, error) {
	ex := &execution{}
	if r.newRoot != nil {
		ex.root = r.newRoot()
		ex.release = ex.undo
	} else {
		r.mu.Lock()
		ex.root = r.root
		ex.release = func() {
			ex.undo()
			r.mu.Unlock()
		}
	}
	if ex.root == nil {
		ex.release()
		return nil, errors.New("cmdsurface: nil cobra root")
	}

	leaf, err := resolveLeaf(ex.root, inv.Path)
	if err != nil {
		ex.release()
		return nil, err
	}
	ex.leaf = leaf

	d := cmdreflect.Describe(ex.root, leaf)
	if err := refuse(d, inv.Path); err != nil {
		ex.release()
		return nil, err
	}

	flags, selected := effectiveFlags(inv, leaf, d)
	ex.selected = selected
	ex.structured = declaresOutput(d) &&
		strings.EqualFold(fmt.Sprint(flags[formatFlag]), jsonFormat)
	ex.args = buildArgs(Invocation{Path: inv.Path, Args: inv.Args, Flags: flags})

	if r.newRoot == nil {
		// Start from the baseline, whatever the previous invocation
		// left behind, and put it back afterwards so the tree is
		// clean between invocations too.
		resetFlags(leaf, r.baseline, flags)
		ex.restores = append(ex.restores, func() { resetFlags(leaf, r.baseline, nil) })
	}

	// Cobra copies the root's context onto the leaf only when the
	// leaf has none, so a leaf executed once keeps that first
	// context forever. Set it explicitly, every time.
	prevLeafCtx := leaf.Context()
	leaf.SetContext(ctx)
	prevRootCtx := ex.root.Context()
	ex.restores = append(ex.restores, func() {
		leaf.SetContext(prevLeafCtx)
		ex.root.SetContext(prevRootCtx)
	})

	// A served invocation has no standard input. Without this a
	// command that prompts would read the serving process's
	// terminal, and one that checks for a TTY would find one.
	prevIn := ex.root.InOrStdin()
	ex.root.SetIn(bytes.NewReader(nil))
	ex.restores = append(ex.restores, func() { ex.root.SetIn(prevIn) })

	return ex, nil
}

// attach installs the invocation's argv and output writers on the
// root, silencing cobra's own usage and error printing so the
// captured streams carry only what the command wrote.
func (ex *execution) attach(stdout, stderr io.Writer) {
	ex.restores = append(ex.restores, captureRootWriters(ex.root, stdout, stderr, ex.args))
}

// undo runs the restores in reverse order.
func (ex *execution) undo() {
	for i := len(ex.restores) - 1; i >= 0; i-- {
		ex.restores[i]()
	}
	ex.restores = nil
}

// result assembles the Result for an execution that returned execErr
// with the given captured streams.
func (ex *execution) result(execErr error, stdout, stderr string) Result {
	res := Result{Stdout: stdout, Stderr: stderr}
	if execErr != nil {
		res.ExitCode = inProcessExitCode(execErr)
		if res.Stderr == "" {
			res.Stderr = execErr.Error()
		}
	}
	if ex.structured {
		if data, ok := decodeStructured(stdout); ok {
			res.Data = data
			if ex.selected {
				// The caller asked for no rendering; Data is the
				// output, and the text would only repeat it.
				res.Stdout = ""
			}
		}
	}
	return res
}

// canceled reports the caller's cancellation as the returned error
// when the command failed while the context was done, so a caller
// can tell an abort it asked for from a failure of the command's own.
// A command that ignored the context and completed is not reported
// as canceled: it completed.
func canceled(ctx context.Context, execErr error) error {
	if execErr == nil {
		return nil
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		return fmt.Errorf("cmdsurface: canceled: %w", ctxErr)
	}
	return nil
}

// refuse returns ErrNotInvocable for the two classes no runner can
// execute, naming the reflector's reason so the message reads the
// same as discovery.
func refuse(d *cmdreflect.Descriptor, path []string) error {
	if d == nil {
		return nil
	}
	switch {
	case d.Surface.SelfHosting:
		return fmt.Errorf("%w: %s is %s (%s)", ErrNotInvocable, joinPath(path),
			cmdreflect.ReasonSelfHosting, cmdreflect.ReasonSelfHosting.Explain())
	case d.Safety.Tier == cmdreflect.TierInteractive:
		return fmt.Errorf("%w: %s is %s (%s)", ErrNotInvocable, joinPath(path),
			cmdreflect.ReasonInteractive, cmdreflect.ReasonInteractive.Explain())
	}
	return nil
}

// declaresOutput reports whether d carries a usable output schema.
func declaresOutput(d *cmdreflect.Descriptor) bool {
	return d != nil && len(d.Output.Schema) > 0 && !d.Output.SchemaMalformed
}

// effectiveFlags returns the flags the invocation will run with, and
// whether the runner added --format=json itself. It does so when the
// command declares an output schema, can take the flag, and the
// caller named no format: a declared schema is the command's
// statement that its output is data, and a caller that does not ask
// for a rendering gets the data. A caller that names a format, json
// or otherwise, is honored as written.
func effectiveFlags(inv Invocation, leaf *cobra.Command, d *cmdreflect.Descriptor) (map[string]any, bool) {
	flags := make(map[string]any, len(inv.Flags)+1)
	for k, v := range inv.Flags {
		flags[k] = v
	}
	if _, named := flags[formatFlag]; named {
		return flags, false
	}
	if declaresOutput(d) && lookupFlag(leaf, formatFlag) != nil {
		flags[formatFlag] = jsonFormat
		return flags, true
	}
	return flags, false
}

// lookupFlag finds a flag visible to cmd: its own, or a persistent
// flag on any ancestor. Cobra merges ancestors' persistent flags into
// cmd.Flags() only at parse time, so the walk is what works before
// the first execution.
func lookupFlag(cmd *cobra.Command, name string) *pflag.Flag {
	for c := cmd; c != nil; c = c.Parent() {
		if f := c.Flags().Lookup(name); f != nil {
			return f
		}
		if f := c.PersistentFlags().Lookup(name); f != nil {
			return f
		}
	}
	return nil
}

// flagState is the value and Changed a flag had when the runner was
// built: the baseline every invocation starts from and returns to.
// For a tree built by cli.New at service start, that is the state the
// operator's own command line left — a served invocation inherits how
// the process was configured and adds only the flags it carries.
type flagState struct {
	value   string
	slice   []string
	isSlice bool
	changed bool
}

// captureFlagState snapshots every flag on every command under root.
// Persistent flags are shared by pointer between a command and the
// merged sets of its descendants, so the map is keyed by *pflag.Flag
// and each flag is recorded once.
func captureFlagState(root *cobra.Command) map[*pflag.Flag]flagState {
	out := map[*pflag.Flag]flagState{}
	var walk func(*cobra.Command)
	walk = func(c *cobra.Command) {
		record := func(f *pflag.Flag) {
			if _, seen := out[f]; seen {
				return
			}
			st := flagState{value: f.Value.String(), changed: f.Changed}
			if sv, ok := f.Value.(pflag.SliceValue); ok {
				st.isSlice = true
				st.slice = append([]string(nil), sv.GetSlice()...)
			}
			out[f] = st
		}
		c.Flags().VisitAll(record)
		c.PersistentFlags().VisitAll(record)
		for _, child := range c.Commands() {
			walk(child)
		}
	}
	walk(root)
	return out
}

// resetFlags returns every flag the leaf's parse will touch — its
// own and every ancestor's persistent set — to its baseline. passed
// names the flags the coming invocation carries; a slice flag among
// them is emptied instead, because pflag appends to a slice on every
// Set after the first and would otherwise stack the invocation's
// values on top of the baseline.
func resetFlags(leaf *cobra.Command, baseline map[*pflag.Flag]flagState, passed map[string]any) {
	seen := map[*pflag.Flag]bool{}
	reset := func(f *pflag.Flag) {
		if seen[f] {
			return
		}
		seen[f] = true
		_, isPassed := passed[f.Name]
		var st *flagState
		if known, ok := baseline[f]; ok {
			st = &known
		}
		restoreFlag(f, st, isPassed)
	}
	for c := leaf; c != nil; c = c.Parent() {
		c.Flags().VisitAll(reset)
		c.PersistentFlags().VisitAll(reset)
	}
}

// ResetFlagToDefault returns one flag to its declared default and
// clears its Changed bit, the state it had before any command line
// was parsed onto it.
//
// It is the primitive behind this package's per-invocation reset,
// exported so a caller that re-parses a tree — a cobra root executed
// more than once in a process — returns flags to their defaults the
// same way, rather than growing a second implementation of the same
// pflag edge cases: slice flags, which pflag appends to on every Set
// after the first; callback-backed flags, whose Set is the adopter's
// own function; and values whose String form does not round-trip
// through Set.
func ResetFlagToDefault(f *pflag.Flag) {
	if f == nil {
		return
	}
	restoreFlag(f, nil, false)
}

// restoreFlag puts one flag back to st. A flag the baseline never saw
// (st nil: registered after the runner was built) goes to its
// declared default. Callback-backed flags (pflag Func and BoolFunc)
// are left alone: their Set IS the adopter's callback, and a reset
// would invoke it.
func restoreFlag(f *pflag.Flag, st *flagState, passed bool) {
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
	f.Changed = st != nil && st.changed
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
