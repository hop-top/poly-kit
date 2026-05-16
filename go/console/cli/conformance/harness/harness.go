package harness

import (
	"bytes"
	"io"
	"sync"

	"hop.top/kit/go/console/cli/conformance/harness/classifier"
	xrr "hop.top/xrr"
)

// Option mutates the per-invocation config struct. Adopters compose
// options at the call site: harness.Args("launch", "--payload",
// "alpha"), harness.WithMode(xrr.ModeRecord), etc. The list of
// known options is closed for v1 — bespoke behavior goes through
// harness.WithInvoker.
type Option func(*config)

// config carries every per-invocation knob. The default value runs
// cmd via cobra.Execute, with stdin set to /dev/null, no tty, and
// no recording.
type config struct {
	args           []string
	mode           xrr.Mode
	cassetteDir    string
	env            map[string]string
	stdin          io.Reader
	withTTY        bool
	configSnapshot map[string]any
	configSnapFile string

	execClassifier classifier.ExecClassifier
	grpcClassifier func(service, method string) classifier.Class

	expectedClass    []string
	schemaJSON       []byte
	parallelism      int
	failFast         bool
	leafExitOverride map[string]string

	invoker Invoker
}

// defaultConfig returns the zero-value tuned for the safest
// behavior: no recording (passthrough), no tty, /dev/null stdin.
func defaultConfig() *config {
	return &config{
		mode:  "", // tests opt in to xrr.Mode via WithMode
		stdin: bytes.NewReader(nil),
	}
}

func apply(opts []Option) *config {
	c := defaultConfig()
	for _, o := range opts {
		if o != nil {
			o(c)
		}
	}
	return c
}

// Args sets the argv the harness passes to cmd.SetArgs before
// cmd.Execute. The first element is the leaf path under root
// (e.g. "launch", or "mission graph"), followed by the leaf's
// flags and positional args.
func Args(args ...string) Option {
	return func(c *config) { c.args = args }
}

// WithMode pins the xrr session mode. Default is ModeRecord for
// the primitives that need a cassette (AssertDryRunNoMutation,
// PlanApplyReplay).
func WithMode(m xrr.Mode) Option {
	return func(c *config) { c.mode = m }
}

// WithCassetteDir overrides the temp-dir default. Persistent
// cassette dirs survive t.TempDir() cleanup and feed back into
// later record/replay cycles.
func WithCassetteDir(path string) Option {
	return func(c *config) { c.cassetteDir = path }
}

// WithEnv sets an env var k=v for the duration of the invocation;
// the previous value is restored on return.
func WithEnv(k, v string) Option {
	return func(c *config) {
		if c.env == nil {
			c.env = map[string]string{}
		}
		c.env[k] = v
	}
}

// WithStdin pipes r into the cmd as os.Stdin / cmd.SetIn. Default
// is an empty bytes.Reader (EOF on first read).
func WithStdin(r io.Reader) Option {
	return func(c *config) { c.stdin = r }
}

// WithExecClassifier overrides the default conservative exec
// classifier (which treats every exec as Write). Adopters who know
// their subprocess catalog return Read / Write / Destructive per
// argv.
func WithExecClassifier(fn classifier.ExecClassifier) Option {
	return func(c *config) { c.execClassifier = fn }
}

// WithGRPCClassifier overrides the gRPC verb-prefix heuristic with
// an adopter-supplied function. Useful when the proto schema
// doesn't follow the Get/List/Create/Delete convention.
func WithGRPCClassifier(fn func(service, method string) classifier.Class) Option {
	return func(c *config) { c.grpcClassifier = fn }
}

// WithExpectedClass overrides the kit/exit-codes annotation read
// from the leaf. Pass one or more class symbols (e.g. "OK",
// "NOT_FOUND"). The harness asserts the observed exit code matches
// any one of them.
func WithExpectedClass(classes ...string) Option {
	return func(c *config) { c.expectedClass = classes }
}

// WithSchema overrides the kit/output-schema annotation. Pass the
// JSON Schema document directly.
func WithSchema(schemaJSON []byte) Option {
	return func(c *config) { c.schemaJSON = schemaJSON }
}

// WithParallelism caps the number of concurrent leaf re-invocations
// AssertCapabilityRoundtrip performs. 0 = GOMAXPROCS.
func WithParallelism(n int) Option {
	return func(c *config) { c.parallelism = n }
}

// WithFailFast flips AssertCapabilityRoundtrip from collect-all to
// stop-on-first-failure.
func WithFailFast() Option {
	return func(c *config) { c.failFast = true }
}

// WithLeafExitOverride lets an adopter declare that a specific leaf
// path legitimately returns a non-OK exit code under --help. The
// map key is the cobra command path (e.g. "spaced mission graph");
// the value is the expected exit-class symbol.
func WithLeafExitOverride(m map[string]string) Option {
	return func(c *config) {
		c.leafExitOverride = map[string]string{}
		for k, v := range m {
			c.leafExitOverride[k] = v
		}
	}
}

// WithConfigSnapshot pins the kit/viper config state for the
// duration of one invocation. See WithConfigSnapshotFile for the
// file-based variant.
func WithConfigSnapshot(settings map[string]any) Option {
	return func(c *config) {
		c.configSnapshot = make(map[string]any, len(settings))
		for k, v := range settings {
			c.configSnapshot[k] = v
		}
	}
}

// WithConfigSnapshotFile reads a YAML or JSON config file from
// path and pins viper to it for the invocation.
func WithConfigSnapshotFile(path string) Option {
	return func(c *config) { c.configSnapFile = path }
}

// NonTTY is the harness default — no terminal attached. The option
// exists for self-documentation at the call site.
func NonTTY() Option {
	return func(c *config) { c.withTTY = false }
}

// WithTTY simulates an attached terminal for the invocation. See
// tty.go for the implementation.
func WithTTY() Option {
	return func(c *config) { c.withTTY = true }
}

// WithInvoker is the escape hatch for adopters whose tests don't
// drive a *cobra.Command. The Invoker implements the harness's
// minimal "run with argv, get stdout/stderr/exit" contract.
func WithInvoker(inv Invoker) Option {
	return func(c *config) { c.invoker = inv }
}

// Invoker is the abstract execution surface every Assert* primitive
// uses. The default implementation wraps cobra.Command's Execute;
// adopters can plug in their own (e.g. an os/exec-backed runner
// against a pre-built binary).
type Invoker interface {
	Invoke(args []string, stdin io.Reader, stdout, stderr io.Writer, env map[string]string) (exitCode int, err error)
}

// TB is the subset of testing.TB the harness primitives consume.
// Adopter tests pass the *testing.T they receive from a Test*
// function; harness internal tests pass a recorder that captures
// failures without escalating, mirroring kitconformance.TB.
//
// TempDir is included so each primitive can allocate cassette
// dirs that get auto-cleaned on test completion; the recording
// stub in the harness's own test suite implements TempDir via
// os.MkdirTemp / t.Cleanup-equivalent (see harness_test.go).
type TB interface {
	Helper()
	Errorf(format string, args ...any)
	Fatalf(format string, args ...any)
	Logf(format string, args ...any)
	TempDir() string
}

// classifierOverrides projects the per-invocation config into the
// dispatcher's Overrides shape.
func (c *config) classifierOverrides() classifier.Overrides {
	return classifier.Overrides{
		Exec: c.execClassifier,
		GRPC: func() func(string, string) classifier.Class {
			if c.grpcClassifier == nil {
				return nil
			}
			return c.grpcClassifier
		}(),
	}
}

// configSnapshotMu serializes harness invocations that mutate
// viper global state via WithConfigSnapshot.
var configSnapshotMu sync.Mutex
