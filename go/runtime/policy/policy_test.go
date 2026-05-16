package policy_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/runtime/bus"
	"hop.top/kit/go/runtime/domain"
	"hop.top/kit/go/runtime/policy"
	"hop.top/kit/go/runtime/policy/withcel"
)

// --- helpers ---

// busAdapter adapts bus.Bus to domain.EventPublisher.
type busAdapter struct{ b bus.Bus }

func (a *busAdapter) Publish(ctx context.Context, topic, source string, payload any) error {
	return a.b.Publish(ctx, bus.NewEvent(bus.Topic(topic), source, payload))
}

// staticPrincipal returns the same principal for every call. Tests use
// it to avoid depending on env state.
func staticPrincipal(p policy.Principal) policy.PrincipalResolver {
	return func(_ context.Context) policy.Principal { return p }
}

// loadSample parses the testdata YAML.
func loadSample(t *testing.T) *policy.Config {
	t.Helper()
	cfg, err := policy.LoadConfig(filepath.Join("testdata", "policies.yaml"))
	require.NoError(t, err)
	return cfg
}

// --- ParseConfig: schema validation ---

func TestParseConfig_OK(t *testing.T) {
	yaml := `policies:
  - name: p1
    on: kit.runtime.entity.pre_validated
    when: 'principal.role == "admin"'
    effect: allow
    otherwise: deny
    message: admin only
`
	cfg, err := policy.ParseConfig([]byte(yaml))
	require.NoError(t, err)
	require.Len(t, cfg.Policies, 1)
	assert.Equal(t, "p1", cfg.Policies[0].Name)
	assert.Equal(t, policy.EffectAllow, cfg.Policies[0].Effect)
	assert.Equal(t, policy.EffectDeny, cfg.Policies[0].Otherwise)
}

func TestParseConfig_BadTopic(t *testing.T) {
	yaml := `policies:
  - name: p1
    on: kit.runtime.entity.created
    when: 'true'
    effect: allow
    otherwise: deny
`
	_, err := policy.ParseConfig([]byte(yaml))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not a veto-able topic")
}

func TestParseConfig_DuplicateName(t *testing.T) {
	yaml := `policies:
  - name: dup
    on: kit.runtime.entity.pre_validated
    when: 'true'
    effect: allow
    otherwise: deny
  - name: dup
    on: kit.runtime.entity.pre_persisted
    when: 'true'
    effect: allow
    otherwise: deny
`
	_, err := policy.ParseConfig([]byte(yaml))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "duplicate name")
}

func TestParseConfig_MissingFields(t *testing.T) {
	cases := map[string]string{
		"missing name": `policies:
  - on: kit.runtime.entity.pre_validated
    when: 'true'
    effect: allow
    otherwise: deny
`,
		"missing when": `policies:
  - name: p1
    on: kit.runtime.entity.pre_validated
    effect: allow
    otherwise: deny
`,
		"missing effect": `policies:
  - name: p1
    on: kit.runtime.entity.pre_validated
    when: 'true'
    otherwise: deny
`,
		"missing otherwise": `policies:
  - name: p1
    on: kit.runtime.entity.pre_validated
    when: 'true'
    effect: allow
`,
		"bad effect value": `policies:
  - name: p1
    on: kit.runtime.entity.pre_validated
    when: 'true'
    effect: maybe
    otherwise: deny
`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := policy.ParseConfig([]byte(body))
			require.Error(t, err)
		})
	}
}

func TestParseConfig_RejectsAsync(t *testing.T) {
	yaml := `policies:
  - name: p1
    on: kit.runtime.entity.pre_validated
    when: 'true'
    effect: allow
    otherwise: deny
    async: true
`
	_, err := policy.ParseConfig([]byte(yaml))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "async not supported")
}

// --- NewEngine: CEL compile errors surface at boot ---

func TestNewEngine_BadCEL(t *testing.T) {
	yaml := `policies:
  - name: p1
    on: kit.runtime.entity.pre_validated
    when: 'this is not cel ?? !!'
    effect: allow
    otherwise: deny
`
	cfg, err := policy.ParseConfig([]byte(yaml))
	require.NoError(t, err)
	_, err = withcel.New(cfg)
	require.Error(t, err)
}

func TestNewEngine_NilConfig(t *testing.T) {
	_, err := policy.NewEngine(nil)
	require.Error(t, err)
}

// TestNewEngine_NoEvaluator verifies the core package refuses to build
// without an explicit evaluator. Adopters MUST use withcel.New or pass
// policy.WithEvaluator.
func TestNewEngine_NoEvaluator(t *testing.T) {
	_, err := policy.NewEngine(&policy.Config{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no evaluator configured")
}

// --- Decide: matrix of allow/deny outcomes ---

func TestDecide_NoPolicies_DefaultAllow(t *testing.T) {
	eng, err := withcel.New(&policy.Config{})
	require.NoError(t, err)
	err = eng.Decide("kit.runtime.entity.pre_validated", map[string]any{
		"principal": map[string]any{"id": "", "role": "", "source": "none"},
		"resource":  map[string]any{},
		"context":   map[string]any{"note": "", "request_attrs": map[string]any{}},
		"payload":   map[string]any{},
	})
	require.NoError(t, err)
}

func TestDecide_UnmatchedTopic_DefaultAllow(t *testing.T) {
	// A topic with zero matching policies must default-allow.
	yaml := `policies:
  - name: admin-only-cancel
    on: kit.runtime.state.pre_transitioned
    when: 'true'
    effect: allow
    otherwise: deny
`
	cfg, err := policy.ParseConfig([]byte(yaml))
	require.NoError(t, err)
	eng, err := withcel.New(cfg)
	require.NoError(t, err)
	err = eng.Decide("kit.runtime.entity.pre_persisted", map[string]any{
		"principal": map[string]any{"id": "", "role": "", "source": "none"},
		"resource":  map[string]any{},
		"context":   map[string]any{"note": "", "request_attrs": map[string]any{}},
		"payload":   map[string]any{},
	})
	require.NoError(t, err)
}

func TestDecide_AllowOnMatch(t *testing.T) {
	cfg := loadSample(t)
	eng, err := withcel.New(cfg)
	require.NoError(t, err)
	err = eng.Decide("kit.runtime.entity.pre_validated", map[string]any{
		"principal": map[string]any{"id": "u1", "role": "admin", "source": "ctx"},
		"resource":  map[string]any{},
		"context":   map[string]any{"note": "", "request_attrs": map[string]any{}},
		"payload":   map[string]any{},
	})
	require.NoError(t, err)
}

func TestDecide_DenyOnNoMatch(t *testing.T) {
	cfg := loadSample(t)
	eng, err := withcel.New(cfg)
	require.NoError(t, err)
	// writes-need-role: when=principal.role!="", effect=allow,
	// otherwise=deny. With empty role the otherwise fires → deny.
	err = eng.Decide("kit.runtime.entity.pre_validated", map[string]any{
		"principal": map[string]any{"id": "", "role": "", "source": "none"},
		"resource":  map[string]any{},
		"context":   map[string]any{"note": "", "request_attrs": map[string]any{}},
		"payload":   map[string]any{},
	})
	require.Error(t, err)
	var pde *policy.PolicyDeniedError
	require.True(t, errors.As(err, &pde))
	assert.Equal(t, "writes-need-role", pde.PolicyName)
	assert.Equal(t, "kit.runtime.entity.pre_validated", pde.Topic)
}

func TestDecide_DenyOverrides_FirstDenyMessageWins(t *testing.T) {
	yaml := `policies:
  - name: p1-allow
    on: kit.runtime.entity.pre_validated
    when: 'true'
    effect: allow
    otherwise: deny
  - name: p2-deny
    on: kit.runtime.entity.pre_validated
    when: 'false'
    effect: allow
    otherwise: deny
    message: "first deny"
  - name: p3-deny
    on: kit.runtime.entity.pre_validated
    when: 'false'
    effect: allow
    otherwise: deny
    message: "second deny"
`
	cfg, err := policy.ParseConfig([]byte(yaml))
	require.NoError(t, err)
	eng, err := withcel.New(cfg)
	require.NoError(t, err)
	err = eng.Decide("kit.runtime.entity.pre_validated", map[string]any{
		"principal": map[string]any{}, "resource": map[string]any{},
		"context": map[string]any{}, "payload": map[string]any{},
	})
	require.Error(t, err)
	var pde *policy.PolicyDeniedError
	require.True(t, errors.As(err, &pde))
	assert.Equal(t, "p2-deny", pde.PolicyName, "first denying policy wins error")
	assert.Contains(t, err.Error(), "first deny")
}

func TestDecide_AllAllow_Allows(t *testing.T) {
	yaml := `policies:
  - name: p1
    on: kit.runtime.entity.pre_validated
    when: 'true'
    effect: allow
    otherwise: deny
  - name: p2
    on: kit.runtime.entity.pre_validated
    when: 'true'
    effect: allow
    otherwise: deny
`
	cfg, err := policy.ParseConfig([]byte(yaml))
	require.NoError(t, err)
	eng, err := withcel.New(cfg)
	require.NoError(t, err)
	err = eng.Decide("kit.runtime.entity.pre_validated", map[string]any{
		"principal": map[string]any{}, "resource": map[string]any{},
		"context": map[string]any{}, "payload": map[string]any{},
	})
	require.NoError(t, err)
}

// --- PolicyDeniedError unwraps to domain.ErrConflict ---

func TestPolicyDeniedError_UnwrapsErrConflict(t *testing.T) {
	e := &policy.PolicyDeniedError{
		PolicyName: "x",
		Topic:      "kit.runtime.entity.pre_validated",
		Message:    "nope",
	}
	require.True(t, errors.Is(e, domain.ErrConflict),
		"PolicyDeniedError must unwrap to domain.ErrConflict")
}

// --- DefaultPrincipalResolver ---

func TestDefaultPrincipalResolver_FromCtx(t *testing.T) {
	want := policy.Principal{ID: "u", Role: "admin", Source: "ctx"}
	ctx := context.WithValue(context.Background(), policy.ContextPrincipalKey, want)
	got := policy.DefaultPrincipalResolver(ctx)
	assert.Equal(t, want, got)
}

func TestDefaultPrincipalResolver_FromEnv(t *testing.T) {
	t.Setenv("KIT_POLICY_ROLE", "lead")
	t.Setenv("USER", "alice")
	got := policy.DefaultPrincipalResolver(context.Background())
	assert.Equal(t, "alice", got.ID)
	assert.Equal(t, "lead", got.Role)
	assert.Equal(t, "env", got.Source)
}

func TestDefaultPrincipalResolver_Empty(t *testing.T) {
	t.Setenv("KIT_POLICY_ROLE", "")
	t.Setenv("USER", "")
	got := policy.DefaultPrincipalResolver(context.Background())
	assert.Equal(t, "", got.Role)
	assert.Equal(t, "none", got.Source)
}

// --- Integration: real bus + state machine ---

func TestIntegration_StateMachine_Veto(t *testing.T) {
	yaml := `policies:
  - name: admin-only-cancel
    on: kit.runtime.state.pre_transitioned
    when: 'payload.To == "CANCELED" && principal.role == "admin"'
    effect: allow
    otherwise: deny
    message: "only admin may cancel"
`
	cfg, err := policy.ParseConfig([]byte(yaml))
	require.NoError(t, err)
	eng, err := withcel.New(cfg, policy.WithPrincipalResolver(staticPrincipal(
		policy.Principal{ID: "u", Role: "user", Source: "ctx"},
	)))
	require.NoError(t, err)

	b := bus.New()
	t.Cleanup(func() { b.Close(context.Background()) })
	cancel := policy.Wire(b, eng)
	t.Cleanup(cancel)

	rules := map[domain.State][]domain.State{
		"OPEN": {"CANCELED", "DONE"},
	}
	sm := domain.NewStateMachine(rules, &busAdapter{b: b})
	err = sm.Transition(context.Background(), "OPEN", "CANCELED", false)
	require.Error(t, err)
	assert.True(t, errors.Is(err, domain.ErrConflict))
	assert.Contains(t, err.Error(), "only admin may cancel")
}

func TestIntegration_StateMachine_Allowed(t *testing.T) {
	yaml := `policies:
  - name: admin-only-cancel
    on: kit.runtime.state.pre_transitioned
    when: 'payload.To == "CANCELED" && principal.role == "admin"'
    effect: allow
    otherwise: deny
`
	cfg, err := policy.ParseConfig([]byte(yaml))
	require.NoError(t, err)
	eng, err := withcel.New(cfg, policy.WithPrincipalResolver(staticPrincipal(
		policy.Principal{ID: "u", Role: "admin", Source: "ctx"},
	)))
	require.NoError(t, err)

	b := bus.New()
	t.Cleanup(func() { b.Close(context.Background()) })
	cancel := policy.Wire(b, eng)
	t.Cleanup(cancel)

	rules := map[domain.State][]domain.State{
		"OPEN": {"CANCELED"},
	}
	sm := domain.NewStateMachine(rules, &busAdapter{b: b})
	require.NoError(t, sm.Transition(context.Background(), "OPEN", "CANCELED", false))
}

// --- Integration: real bus + domain.Service Create (pre_validated) ---

type intItem struct {
	ID   string
	Name string
}

func (i intItem) GetID() string { return i.ID }

type memRepo struct {
	created []intItem
}

func (r *memRepo) Create(_ context.Context, e *intItem) error {
	r.created = append(r.created, *e)
	return nil
}

func (r *memRepo) Get(_ context.Context, _ string) (*intItem, error) { return nil, nil }
func (r *memRepo) List(_ context.Context, _ domain.Query) ([]intItem, error) {
	return nil, nil
}
func (r *memRepo) Update(_ context.Context, _ *intItem) error { return nil }
func (r *memRepo) Delete(_ context.Context, _ string) error   { return nil }

func TestIntegration_Service_PreValidatedVeto(t *testing.T) {
	yaml := `policies:
  - name: writes-need-role
    on: kit.runtime.entity.pre_validated
    when: 'principal.role != ""'
    effect: allow
    otherwise: deny
    message: "writes require an authenticated principal"
`
	cfg, err := policy.ParseConfig([]byte(yaml))
	require.NoError(t, err)
	eng, err := withcel.New(cfg, policy.WithPrincipalResolver(staticPrincipal(
		policy.Principal{ID: "u", Role: "", Source: "none"},
	)))
	require.NoError(t, err)

	b := bus.New()
	t.Cleanup(func() { b.Close(context.Background()) })
	cancel := policy.Wire(b, eng)
	t.Cleanup(cancel)

	repo := &memRepo{}
	svc := domain.NewService[intItem](repo, domain.WithPublisher[intItem](&busAdapter{b: b}))
	err = svc.Create(context.Background(), &intItem{ID: "1", Name: "x"})
	require.Error(t, err)
	assert.True(t, errors.Is(err, domain.ErrConflict))
	assert.Empty(t, repo.created, "veto must abort before repo.Create")
}

func TestIntegration_Service_PrePersistedVeto(t *testing.T) {
	yaml := `policies:
  - name: delete-requires-note
    on: kit.runtime.entity.pre_persisted
    when: 'payload.Op != "delete" || context.note != ""'
    effect: allow
    otherwise: deny
    message: "deleting requires --note"
`
	cfg, err := policy.ParseConfig([]byte(yaml))
	require.NoError(t, err)
	eng, err := withcel.New(cfg, policy.WithPrincipalResolver(staticPrincipal(
		policy.Principal{ID: "u", Role: "admin", Source: "ctx"},
	)))
	require.NoError(t, err)

	b := bus.New()
	t.Cleanup(func() { b.Close(context.Background()) })
	cancel := policy.Wire(b, eng)
	t.Cleanup(cancel)

	repo := &memRepo{}
	svc := domain.NewService[intItem](repo, domain.WithPublisher[intItem](&busAdapter{b: b}))

	// No --note → policy denies on pre_persisted.
	err = svc.Delete(context.Background(), "1")
	require.Error(t, err)
	assert.True(t, errors.Is(err, domain.ErrConflict))
	assert.True(t, strings.Contains(err.Error(), "deleting requires --note"))

	// With --note via ContextAttrsKey → allowed.
	ctx := context.WithValue(context.Background(), policy.ContextAttrsKey, map[string]any{
		"note": "cleanup",
	})
	require.NoError(t, svc.Delete(ctx, "1"))
}

// --- Integration: end-to-end via testdata/policies.yaml ---

func TestIntegration_TestdataYAML(t *testing.T) {
	cfg := loadSample(t)
	eng, err := withcel.New(cfg, policy.WithPrincipalResolver(staticPrincipal(
		policy.Principal{ID: "u", Role: "admin", Source: "ctx"},
	)))
	require.NoError(t, err)

	b := bus.New()
	t.Cleanup(func() { b.Close(context.Background()) })
	cancel := policy.Wire(b, eng)
	t.Cleanup(cancel)

	repo := &memRepo{}
	svc := domain.NewService[intItem](repo, domain.WithPublisher[intItem](&busAdapter{b: b}))

	// admin creating a record → all policies pass.
	require.NoError(t, svc.Create(context.Background(), &intItem{ID: "x", Name: "y"}))
}

// --- Sanity: testdata file is reachable from tests ---

func TestTestdataExists(t *testing.T) {
	_, err := os.Stat(filepath.Join("testdata", "policies.yaml"))
	require.NoError(t, err)
}

// --- Performance smoke: not gating, informational ---

func BenchmarkDecide(b *testing.B) {
	yaml := `policies:
  - name: p1
    on: kit.runtime.entity.pre_validated
    when: 'principal.role == "admin"'
    effect: allow
    otherwise: deny
`
	cfg, _ := policy.ParseConfig([]byte(yaml))
	eng, _ := withcel.New(cfg)
	act := map[string]any{
		"principal": map[string]any{"id": "u", "role": "admin", "source": "ctx"},
		"resource":  map[string]any{},
		"context":   map[string]any{"note": "", "request_attrs": map[string]any{}},
		"payload":   map[string]any{},
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = eng.Decide("kit.runtime.entity.pre_validated", act)
	}
}
