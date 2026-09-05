package cli_test

import (
	"bytes"
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"hop.top/kit/go/console/cli"
	"hop.top/kit/go/console/output"
	"hop.top/kit/go/console/serve"
)

// stubService is a minimal Service for command-surface tests: it comes
// up immediately and stays up until its context is canceled.
type stubService struct {
	name string

	mu      sync.Mutex
	ready   bool
	started bool
}

func (s *stubService) Name() string { return s.name }

func (s *stubService) Start(ctx context.Context, report func()) error {
	s.mu.Lock()
	s.started, s.ready = true, true
	s.mu.Unlock()
	report()
	<-ctx.Done()
	return nil
}

func (s *stubService) Ready() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.ready
}

func (s *stubService) Stop(context.Context) error {
	s.mu.Lock()
	s.ready = false
	s.mu.Unlock()
	return nil
}

func (s *stubService) wasStarted() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.started
}

func newServeRoot(t *testing.T, opts ...func(*cli.Root)) *cli.Root {
	t.Helper()
	base := []func(*cli.Root){}
	base = append(base, opts...)
	return cli.New(cli.Config{
		Name: "test", Version: "0.1.0", DisableValidate: true,
	}, base...)
}

// runServeArgs executes the root with args and returns the error,
// canceling once the run is under way so a supervisor form returns.
func runServeArgs(t *testing.T, r *cli.Root, args []string, settle time.Duration) error {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	r.SetArgs(args)
	errCh := make(chan error, 1)
	go func() { errCh <- r.Execute(ctx) }()

	select {
	case err := <-errCh:
		return err
	case <-time.After(settle):
		cancel()
	}

	select {
	case err := <-errCh:
		return err
	case <-time.After(5 * time.Second):
		t.Fatal("serve did not return after cancellation")
		return nil
	}
}

func TestServe_RegistryMountsParentCommand(t *testing.T) {
	r := newServeRoot(t, cli.WithService(&stubService{name: "worker"}))

	var serveCmd bool
	for _, c := range r.Cmd.Commands() {
		if c.Name() == "serve" {
			serveCmd = true
		}
	}
	assert.True(t, serveCmd, "registering a service mounts the serve parent")
	require.NotNil(t, r.ServeRegistry())
	assert.Equal(t, []string{"worker"}, r.ServeRegistry().Names())
}

func TestServe_SupervisorStartsEnabledService(t *testing.T) {
	svc := &stubService{name: "worker"}
	r := newServeRoot(t, cli.WithService(svc))
	r.Viper.Set("services.worker.enabled", true)

	err := runServeArgs(t, r, []string{"serve"}, 300*time.Millisecond)
	assert.NoError(t, err, "a signal-initiated stop is a clean stop")
	assert.True(t, svc.wasStarted())
}

func TestServe_SupervisorSkipsDisabledService(t *testing.T) {
	on := &stubService{name: "on"}
	off := &stubService{name: "off"}
	r := newServeRoot(t, cli.WithServices(on, off))
	r.Viper.Set("services.on.enabled", true)
	r.Viper.Set("services.off.enabled", false)

	err := runServeArgs(t, r, []string{"serve"}, 300*time.Millisecond)
	assert.NoError(t, err)
	assert.True(t, on.wasStarted())
	assert.False(t, off.wasStarted(),
		"a disabled service is skipped silently and must not affect the exit code")
}

func TestServe_SelectorOverridesEnablement(t *testing.T) {
	svc := &stubService{name: "worker"}
	r := newServeRoot(t, cli.WithService(svc))
	r.Viper.Set("services.worker.enabled", false)

	err := runServeArgs(t, r, []string{"serve", "worker"}, 300*time.Millisecond)
	assert.NoError(t, err)
	assert.True(t, svc.wasStarted(),
		"explicit selection starts a disabled service: enablement is not authorization")
}

func TestServe_UnknownServiceIsNotFoundWithHint(t *testing.T) {
	r := newServeRoot(t, cli.WithServices(
		&stubService{name: "api"}, &stubService{name: "socket"},
	))

	err := runServeArgs(t, r, []string{"serve", "sockte"}, 2*time.Second)
	require.Error(t, err)

	var kitErr *output.Error
	require.ErrorAs(t, err, &kitErr)
	assert.Equal(t, output.CodeNotFound, kitErr.Code)
	assert.Equal(t, 3, kitErr.ExitCode)
	assert.Contains(t, kitErr.Message, `unknown service "sockte"`)
	assert.Contains(t, kitErr.SuggestedFix, "socket", "nearest-name hint")
}

func TestServe_TwoPositionalArgsIsUsage(t *testing.T) {
	r := newServeRoot(t, cli.WithService(&stubService{name: "worker"}))

	err := runServeArgs(t, r, []string{"serve", "worker", "extra"}, 2*time.Second)
	require.Error(t, err, "serve accepts at most one service name")

	// The refusal is cobra's MaximumNArgs, raised before RunE; the
	// contract still owes the caller USAGE, exit 2.
	var kitErr *output.Error
	require.ErrorAs(t, err, &kitErr)
	assert.Equal(t, output.CodeUsage, kitErr.Code)
	assert.Equal(t, 2, kitErr.ExitCode)
	assert.Contains(t, kitErr.Message, "accepts at most 1 arg(s), received 2")
}

func TestServe_ZeroResolvedServicesIsUsage(t *testing.T) {
	r := newServeRoot(t, cli.WithService(&stubService{name: "worker"}))
	// Configured but not enabled: the supervisor resolves to zero.
	r.Viper.Set("services.worker.enabled", false)

	err := runServeArgs(t, r, []string{"serve"}, 2*time.Second)
	require.Error(t, err)

	var kitErr *output.Error
	require.ErrorAs(t, err, &kitErr)
	assert.Equal(t, output.CodeUsage, kitErr.Code)
	assert.Equal(t, 2, kitErr.ExitCode,
		"exiting 0 without listening is indistinguishable from a successful start")
}

func TestServe_ListShowsRegisteredServices(t *testing.T) {
	r := newServeRoot(t, cli.WithServices(
		&stubService{name: "alpha"}, &stubService{name: "beta"},
	))
	r.Viper.Set("services.alpha.enabled", true)

	var out bytes.Buffer
	r.Cmd.SetOut(&out)

	err := runServeArgs(t, r, []string{"serve", "--list"}, 2*time.Second)
	require.NoError(t, err)

	got := out.String()
	assert.Contains(t, got, "alpha")
	assert.Contains(t, got, "beta")
	assert.Less(t, strings.Index(got, "alpha"), strings.Index(got, "beta"),
		"listing is in registration order, mirroring the adopter's wiring")
}

func TestServe_ReservedNamePanicsAtRegistration(t *testing.T) {
	for _, name := range []string{"all", "none", "list"} {
		assert.Panics(t, func() {
			newServeRoot(t, cli.WithService(&stubService{name: name}))
		}, "%q is reserved selector vocabulary", name)
	}
}

func TestServe_DuplicateNamePanicsButOverrideDoesNot(t *testing.T) {
	assert.Panics(t, func() {
		newServeRoot(t,
			cli.WithService(&stubService{name: "dup"}),
			cli.WithService(&stubService{name: "dup"}))
	}, "a collision is a wiring bug, surfaced on the first run")

	replacement := &stubService{name: "dup"}
	r := newServeRoot(t,
		cli.WithService(&stubService{name: "dup"}),
		cli.WithServiceOverride(replacement))
	got, ok := r.ServeRegistry().Lookup("dup")
	require.True(t, ok)
	assert.Same(t, replacement, got, "Override is the documented escape hatch")
}

func TestServe_InvalidFailurePolicyIsUsage(t *testing.T) {
	r := newServeRoot(t, cli.WithService(&stubService{name: "worker"}))
	r.Viper.Set("services.worker.enabled", true)
	r.Viper.Set("services.failure_policy", "yolo")

	err := runServeArgs(t, r, []string{"serve"}, 2*time.Second)
	require.Error(t, err)

	var kitErr *output.Error
	require.ErrorAs(t, err, &kitErr)
	assert.Equal(t, output.CodeUsage, kitErr.Code)
	assert.Contains(t, kitErr.Message, "unknown policy",
		"silently running fail-fast when isolate was asked for is not acceptable")
}

func TestServe_EnableFlagOverridesAggregateEnablement(t *testing.T) {
	svc := &stubService{name: "worker"}
	r := newServeRoot(t, cli.WithService(svc))
	r.Viper.Set("services.worker.enabled", false)

	err := runServeArgs(t, r, []string{"serve", "--enable", "worker"}, 300*time.Millisecond)
	assert.NoError(t, err)
	assert.True(t, svc.wasStarted())
}

func TestServe_EnableFlagRefusedUnderSelector(t *testing.T) {
	r := newServeRoot(t, cli.WithService(&stubService{name: "worker"}))

	err := runServeArgs(t, r,
		[]string{"serve", "worker", "--enable", "worker"}, 2*time.Second)
	require.Error(t, err)

	var kitErr *output.Error
	require.ErrorAs(t, err, &kitErr)
	assert.Equal(t, output.CodeUsage, kitErr.Code)
}

func TestServe_PolicyDenialIsUnauthorized(t *testing.T) {
	r := newServeRoot(t,
		cli.WithService(&classifiedStub{stubService: &stubService{name: "worker"}}),
		cli.WithServicePolicy(denyGate{}))
	r.Viper.Set("services.worker.enabled", true)

	err := runServeArgs(t, r, []string{"serve"}, 2*time.Second)
	require.Error(t, err)

	var kitErr *output.Error
	require.ErrorAs(t, err, &kitErr)
	assert.Equal(t, output.CodeUnauthorized, kitErr.Code)
	assert.Equal(t, 5, kitErr.ExitCode, "a policy deny is a refusal, not a prompt")
}

type classifiedStub struct{ *stubService }

func (c classifiedStub) Class() (string, string) { return "write-shared", "listen" }

type denyGate struct{}

func (denyGate) Allow(string, string) (bool, string) { return false, "network denied" }

func TestServe_HelpListsRegisteredServices(t *testing.T) {
	r := newServeRoot(t, cli.WithServices(
		&stubService{name: "alpha"}, &stubService{name: "beta"},
	))

	var out bytes.Buffer
	r.Cmd.SetOut(&out)
	r.Cmd.SetErr(&out)

	_ = runServeArgs(t, r, []string{"serve", "--help"}, 2*time.Second)
	assert.Contains(t, out.String(), "alpha")
	assert.Contains(t, out.String(), "beta")
}

// --- WithAPI compatibility -------------------------------------------------

func TestWithAPI_RegistersAPIService(t *testing.T) {
	r := newServeRoot(t, cli.WithAPI(cli.APIConfig{}))

	require.NotNil(t, r.ServeRegistry())
	assert.Equal(t, []string{cli.APIServiceName}, r.ServeRegistry().Names(),
		"WithAPI reaches the surface as the api service, not a leaf serve")
}

func TestWithAPI_ServeStartsAPIWithoutConfiguration(t *testing.T) {
	r := newServeRoot(t, cli.WithAPI(cli.APIConfig{Addr: "127.0.0.1:0"}))

	// No services.api.enabled key at all: calling WithAPI IS the
	// request to serve it, so `serve` must behave as it did before.
	err := runServeArgs(t, r, []string{"serve"}, 400*time.Millisecond)
	assert.NoError(t, err)

	svc, ok := r.ServeRegistry().Lookup(cli.APIServiceName)
	require.True(t, ok)
	assert.False(t, svc.Ready(), "the service is stopped after the run")
}

// TestWithAPI_ListReadsTheSupervisorsResolution pins that `serve
// --list` reports the api service the way the supervisor resolves it:
// enabled by default under WithAPI, disabled only by an explicit key.
func TestWithAPI_ListReadsTheSupervisorsResolution(t *testing.T) {
	apiLine := func(r *cli.Root) string {
		var out bytes.Buffer
		r.Cmd.SetOut(&out)
		require.NoError(t, runServeArgs(t, r, []string{"serve", "--list"}, 2*time.Second))
		for _, line := range strings.Split(out.String(), "\n") {
			if strings.HasPrefix(line, cli.APIServiceName+" ") {
				return line
			}
		}
		t.Fatalf("no api line in listing:\n%s", out.String())
		return ""
	}

	r := newServeRoot(t, cli.WithAPI(cli.APIConfig{Addr: "127.0.0.1:0"}))
	fields := strings.Fields(apiLine(r))
	require.Len(t, fields, 4, "SERVICE CONFIGURED ENABLED READY")
	assert.Equal(t, "true", fields[1], "the compat default makes the api configured")
	assert.Equal(t, "true", fields[2], "the listing must say what a bare serve does: start it")

	off := newServeRoot(t, cli.WithAPI(cli.APIConfig{Addr: "127.0.0.1:0"}))
	off.Viper.Set("services.api.enabled", false)
	fields = strings.Fields(apiLine(off))
	require.Len(t, fields, 4)
	assert.Equal(t, "false", fields[2], "an explicit services.api.enabled: false wins")
}

func TestWithAPI_ExplicitDisableWinsOverCompatDefault(t *testing.T) {
	r := newServeRoot(t, cli.WithAPI(cli.APIConfig{Addr: "127.0.0.1:0"}))
	r.Viper.Set("services.api.enabled", false)

	err := runServeArgs(t, r, []string{"serve"}, 2*time.Second)
	require.Error(t, err, "an adopter that has migrated can turn the api off")

	var kitErr *output.Error
	require.ErrorAs(t, err, &kitErr)
	assert.Equal(t, output.CodeUsage, kitErr.Code)
}

func TestWithAPI_ExactlyOneServeCommandInEitherOptionOrder(t *testing.T) {
	// Whichever option mounts the parent first, exactly one command
	// owns the `serve` word: the two must never both own it.
	for _, tc := range []struct {
		name  string
		opts  []func(*cli.Root)
		names []string
	}{
		{
			name: "api first",
			opts: []func(*cli.Root){
				cli.WithAPI(cli.APIConfig{Addr: ":0"}),
				cli.WithService(&stubService{name: "worker"}),
			},
			names: []string{cli.APIServiceName, "worker"},
		},
		{
			name: "service first",
			opts: []func(*cli.Root){
				cli.WithService(&stubService{name: "worker"}),
				cli.WithAPI(cli.APIConfig{Addr: ":0"}),
			},
			names: []string{"worker", cli.APIServiceName},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newServeRoot(t, tc.opts...)

			count := 0
			for _, c := range r.Cmd.Commands() {
				if c.Name() == "serve" {
					count++
				}
			}
			assert.Equal(t, 1, count, "exactly one command owns the serve word")
			assert.Equal(t, tc.names, r.ServeRegistry().Names())

			// The api service's own flags reach the parent regardless
			// of which option mounted it.
			for _, c := range r.Cmd.Commands() {
				if c.Name() == "serve" {
					assert.NotNil(t, c.Flags().Lookup("addr"),
						"--addr must reach the serve parent in either order")
				}
			}
		})
	}
}

func TestWithAPI_AddrFlagStillWorks(t *testing.T) {
	r := newServeRoot(t, cli.WithAPI(cli.APIConfig{Addr: ":0"}))

	for _, c := range r.Cmd.Commands() {
		if c.Name() == "serve" {
			f := c.Flags().Lookup("addr")
			require.NotNil(t, f, "--addr must keep working for existing adopters")
			assert.Equal(t, ":0", f.DefValue)
			return
		}
	}
	t.Fatal("serve command not found")
}

func TestWithAPI_InvalidAddrIsAConfigUsageError(t *testing.T) {
	r := newServeRoot(t, cli.WithAPI(cli.APIConfig{Addr: "not-an-address"}))

	err := runServeArgs(t, r, []string{"serve", "api"}, 2*time.Second)
	require.Error(t, err)

	var kitErr *output.Error
	require.ErrorAs(t, err, &kitErr)
	assert.Equal(t, output.CodeUsage, kitErr.Code)
	assert.Equal(t, 2, kitErr.ExitCode)
	assert.Contains(t, kitErr.Message, `service "api"`)
}

var _ serve.Service = (*stubService)(nil)
