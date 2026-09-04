package serve_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/console/output"
	"hop.top/kit/go/console/serve"
)

// fakeService is the minimal Service used across these tests. The
// optional contract interfaces are opted into by the two wrapper
// types below, so a bare fakeService exercises the
// "no Validator, no Classified" path.
type fakeService struct {
	name string
}

func (f fakeService) Name() string                        { return f.name }
func (f fakeService) Start(context.Context, func()) error { return nil }
func (f fakeService) Ready() bool                         { return true }
func (f fakeService) Stop(context.Context) error          { return nil }

type invalidService struct {
	fakeService
	err error
}

func (s invalidService) Validate() error { return s.err }

type classifiedService struct {
	fakeService
	sideEffect string
	network    string
}

func (s classifiedService) Class() (string, string) { return s.sideEffect, s.network }

// denyGate refuses every class it is asked about.
type denyGate struct{ reason string }

func (g denyGate) Allow(string, string) (bool, string) { return false, g.reason }

// allowGate permits every class.
type allowGate struct{}

func (allowGate) Allow(string, string) (bool, string) { return true, "" }

func enabled() serve.Config  { return serve.Config{Enabled: true} }
func disabled() serve.Config { return serve.Config{Enabled: false} }

func TestResolve_SelectorOverridesEnablement(t *testing.T) {
	tests := []struct {
		name    string
		cfg     map[string]serve.Config
		wantSel []string
	}{
		{
			name:    "disabled service still runs when named",
			cfg:     map[string]serve.Config{"api": disabled()},
			wantSel: []string{"api"},
		},
		{
			name:    "enabled service runs when named",
			cfg:     map[string]serve.Config{"api": enabled()},
			wantSel: []string{"api"},
		},
		{
			name:    "unconfigured service still runs when named",
			cfg:     map[string]serve.Config{},
			wantSel: []string{"api"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			reg := serve.NewRegistry()
			reg.Register(fakeService{name: "api"})

			got := serve.Resolve(reg, serve.Request{Args: []string{"api"}, Configs: tc.cfg})

			require.Nil(t, got.Err)
			assert.Equal(t, tc.wantSel, got.Selected)
			assert.True(t, got.Explicit)
		})
	}
}

func TestResolve_SelectorGatesInOrder(t *testing.T) {
	// A service that is both config-invalid and policy-denied must
	// report the configuration failure: gate 2 runs before gate 3.
	reg := serve.NewRegistry()
	reg.Register(invalidService{
		fakeService: fakeService{name: "api"},
		err:         errors.New("addr: missing"),
	})

	got := serve.Resolve(reg, serve.Request{
		Args:   []string{"api"},
		Policy: denyGate{reason: "destructive"},
	})

	require.NotNil(t, got.Err)
	assert.Equal(t, output.CodeUsage, got.Err.Code)
	assert.Equal(t, 2, got.Err.ExitCode)
	assert.Contains(t, got.Err.Message, `service "api": addr: missing`)
}

func TestResolve_SelectorFailures(t *testing.T) {
	tests := []struct {
		name     string
		register func(*serve.Registry)
		req      serve.Request
		wantCode string
		wantExit int
		wantMsg  string
	}{
		{
			name:     "unknown service",
			register: func(r *serve.Registry) { r.Register(fakeService{name: "api"}) },
			req:      serve.Request{Args: []string{"nope"}},
			wantCode: output.CodeNotFound,
			wantExit: 3,
			wantMsg:  `unknown service "nope"`,
		},
		{
			name:     "two positional args",
			register: func(r *serve.Registry) { r.Register(fakeService{name: "api"}) },
			req:      serve.Request{Args: []string{"api", "socket"}},
			wantCode: output.CodeUsage,
			wantExit: 2,
			wantMsg:  "at most one service name",
		},
		{
			name: "config invalid",
			register: func(r *serve.Registry) {
				r.Register(invalidService{
					fakeService: fakeService{name: "api"},
					err:         errors.New("addr: not a host:port"),
				})
			},
			req:      serve.Request{Args: []string{"api"}},
			wantCode: output.CodeUsage,
			wantExit: 2,
			wantMsg:  "not a host:port",
		},
		{
			name: "policy denied",
			register: func(r *serve.Registry) {
				r.Register(classifiedService{
					fakeService: fakeService{name: "api"},
					sideEffect:  "destructive",
					network:     "egress",
				})
			},
			req: serve.Request{
				Args:   []string{"api"},
				Policy: denyGate{reason: "remote destructive denied"},
			},
			wantCode: output.CodeUnauthorized,
			wantExit: 5,
			wantMsg:  "denied by policy",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			reg := serve.NewRegistry()
			tc.register(reg)

			got := serve.Resolve(reg, tc.req)

			require.NotNil(t, got.Err)
			assert.Empty(t, got.Selected)
			assert.Equal(t, tc.wantCode, got.Err.Code)
			assert.Equal(t, tc.wantExit, got.Err.ExitCode)
			assert.Contains(t, got.Err.Message, tc.wantMsg)
		})
	}
}

func TestResolve_UnknownServiceSuggestsNearest(t *testing.T) {
	reg := serve.NewRegistry()
	reg.Register(fakeService{name: "socket"})

	got := serve.Resolve(reg, serve.Request{Args: []string{"sockte"}})

	require.NotNil(t, got.Err)
	assert.Contains(t, got.Err.SuggestedFix, `"socket"`)
}

func TestResolve_UnknownServiceOmitsUnrelatedSuggestion(t *testing.T) {
	reg := serve.NewRegistry()
	reg.Register(fakeService{name: "api"})

	got := serve.Resolve(reg, serve.Request{Args: []string{"telemetry"}})

	require.NotNil(t, got.Err)
	assert.Empty(t, got.Err.SuggestedFix)
}

func TestResolve_AggregateEnablement(t *testing.T) {
	tests := []struct {
		name        string
		cfg         map[string]serve.Config
		wantSel     []string
		wantSkipped []string
		wantErrCode string
	}{
		{
			name:    "all enabled runs all in registration order",
			cfg:     map[string]serve.Config{"api": enabled(), "socket": enabled()},
			wantSel: []string{"api", "socket"},
		},
		{
			name:        "disabled is skipped silently, not an error",
			cfg:         map[string]serve.Config{"api": enabled(), "socket": disabled()},
			wantSel:     []string{"api"},
			wantSkipped: []string{"socket"},
		},
		{
			name:    "unconfigured is neither run nor skipped",
			cfg:     map[string]serve.Config{"api": enabled()},
			wantSel: []string{"api"},
		},
		{
			name:        "zero resolved is a usage error, not a clean exit",
			cfg:         map[string]serve.Config{"api": disabled(), "socket": disabled()},
			wantSkipped: []string{"api", "socket"},
			wantErrCode: output.CodeUsage,
		},
		{
			name:        "empty config resolves to zero",
			cfg:         map[string]serve.Config{},
			wantErrCode: output.CodeUsage,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			reg := serve.NewRegistry()
			reg.Register(fakeService{name: "api"})
			reg.Register(fakeService{name: "socket"})

			got := serve.Resolve(reg, serve.Request{Configs: tc.cfg})

			assert.Equal(t, tc.wantSel, got.Selected)
			assert.Equal(t, tc.wantSkipped, got.Skipped)
			assert.False(t, got.Explicit)
			if tc.wantErrCode == "" {
				assert.Nil(t, got.Err)
				return
			}
			require.NotNil(t, got.Err)
			assert.Equal(t, tc.wantErrCode, got.Err.Code)
			assert.Equal(t, 2, got.Err.ExitCode)
		})
	}
}

func TestResolve_AggregateAppliesGatesToEnabledOnly(t *testing.T) {
	// A disabled service with broken config must not fail the run:
	// the supervisor never reaches its gates.
	reg := serve.NewRegistry()
	reg.Register(fakeService{name: "api"})
	reg.Register(invalidService{
		fakeService: fakeService{name: "socket"},
		err:         errors.New("path: missing"),
	})

	got := serve.Resolve(reg, serve.Request{
		Configs: map[string]serve.Config{"api": enabled(), "socket": disabled()},
	})

	require.Nil(t, got.Err)
	assert.Equal(t, []string{"api"}, got.Selected)
	assert.Equal(t, []string{"socket"}, got.Skipped)
}

func TestResolve_AggregateFailsOnEnabledInvalidService(t *testing.T) {
	reg := serve.NewRegistry()
	reg.Register(invalidService{
		fakeService: fakeService{name: "socket"},
		err:         errors.New("path: missing"),
	})

	got := serve.Resolve(reg, serve.Request{
		Configs: map[string]serve.Config{"socket": enabled()},
	})

	require.NotNil(t, got.Err)
	assert.Equal(t, output.CodeUsage, got.Err.Code)
	assert.Contains(t, got.Err.Message, "path: missing")
}

func TestResolve_NilPolicyGatePassesClassifiedService(t *testing.T) {
	reg := serve.NewRegistry()
	reg.Register(classifiedService{
		fakeService: fakeService{name: "api"},
		sideEffect:  "destructive",
		network:     "egress",
	})

	got := serve.Resolve(reg, serve.Request{Args: []string{"api"}})

	require.Nil(t, got.Err)
	assert.Equal(t, []string{"api"}, got.Selected)
}

func TestResolve_AllowGatePassesClassifiedService(t *testing.T) {
	reg := serve.NewRegistry()
	reg.Register(classifiedService{
		fakeService: fakeService{name: "api"},
		sideEffect:  "read",
		network:     "local-only",
	})

	got := serve.Resolve(reg, serve.Request{Args: []string{"api"}, Policy: allowGate{}})

	require.Nil(t, got.Err)
	assert.Equal(t, []string{"api"}, got.Selected)
}

func TestResolve_NilRegistryIsUsageError(t *testing.T) {
	got := serve.Resolve(nil, serve.Request{})

	require.NotNil(t, got.Err)
	assert.Equal(t, output.CodeUsage, got.Err.Code)
}

func TestFailurePolicy_Validity(t *testing.T) {
	tests := []struct {
		policy serve.FailurePolicy
		want   bool
	}{
		{serve.FailFast, true},
		{serve.Isolate, true},
		{serve.FailurePolicy("halt"), false},
		{serve.FailurePolicy(""), false},
	}

	for _, tc := range tests {
		t.Run(tc.policy.String(), func(t *testing.T) {
			assert.Equal(t, tc.want, tc.policy.IsValid())
		})
	}
}

func TestFailurePolicy_DefaultIsFailFast(t *testing.T) {
	assert.Equal(t, serve.FailFast, serve.DefaultFailurePolicy)
}
