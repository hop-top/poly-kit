package domain_test

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/runtime/domain"
)

// --- helpers: a validator that captures the Op + SubOp it observes ---

// capturingValidator records the Op and SubOp values pulled from ctx
// the moment Validate runs, so tests can assert what Service injected.
type capturingValidator struct {
	mu       sync.Mutex
	gotOp    domain.Op
	gotSubOp string
	calls    int
}

func (v *capturingValidator) Validate(ctx context.Context, _ testEntity) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.gotOp = domain.OpFromCtx(ctx)
	v.gotSubOp = domain.SubOpFromCtx(ctx)
	v.calls++
	return nil
}

func (v *capturingValidator) snapshot() (domain.Op, string, int) {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.gotOp, v.gotSubOp, v.calls
}

// reinjectingValidator calls WithOp(ctx, OpCreate) before re-reading
// OpFromCtx — used to assert Service auto-injection is authoritative
// (Validator can shadow only its own captured ctx, never override what
// Service already injected for the in-flight operation).
type reinjectingValidator struct {
	mu              sync.Mutex
	observedFromCtx domain.Op
	observedAfter   domain.Op
}

func (v *reinjectingValidator) Validate(ctx context.Context, _ testEntity) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.observedFromCtx = domain.OpFromCtx(ctx)
	shadowed := domain.WithOp(ctx, domain.OpCreate)
	v.observedAfter = domain.OpFromCtx(shadowed)
	return nil
}

func (v *reinjectingValidator) snapshot() (domain.Op, domain.Op) {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.observedFromCtx, v.observedAfter
}

// --- ctx round-trip tests ---

func TestOpFromCtx_EmptyDefault(t *testing.T) {
	got := domain.OpFromCtx(context.Background())
	assert.Equal(t, domain.Op(""), got,
		"empty ctx must yield empty Op — documented sentinel")
}

func TestSubOpFromCtx_EmptyDefault(t *testing.T) {
	got := domain.SubOpFromCtx(context.Background())
	assert.Equal(t, "", got,
		"empty ctx must yield empty SubOp — documented sentinel")
}

func TestWithOp_RoundTrip(t *testing.T) {
	ctx := domain.WithOp(context.Background(), domain.OpCreate)
	assert.Equal(t, domain.OpCreate, domain.OpFromCtx(ctx))

	ctx = domain.WithOp(ctx, domain.OpUpdate)
	assert.Equal(t, domain.OpUpdate, domain.OpFromCtx(ctx),
		"WithOp must overwrite a prior value")

	ctx = domain.WithOp(ctx, domain.OpDelete)
	assert.Equal(t, domain.OpDelete, domain.OpFromCtx(ctx))
}

func TestWithSubOp_RoundTrip(t *testing.T) {
	ctx := domain.WithSubOp(context.Background(), "archive")
	assert.Equal(t, "archive", domain.SubOpFromCtx(ctx))

	ctx = domain.WithSubOp(ctx, "unarchive")
	assert.Equal(t, "unarchive", domain.SubOpFromCtx(ctx),
		"WithSubOp must overwrite a prior value")
}

func TestWithOp_AndWithSubOp_Orthogonal(t *testing.T) {
	// Setting Op must not clear SubOp.
	ctx := domain.WithSubOp(context.Background(), "archive")
	ctx = domain.WithOp(ctx, domain.OpUpdate)
	assert.Equal(t, domain.OpUpdate, domain.OpFromCtx(ctx))
	assert.Equal(t, "archive", domain.SubOpFromCtx(ctx),
		"WithOp must not clobber SubOp")

	// Setting SubOp must not clear Op.
	ctx = domain.WithOp(context.Background(), domain.OpUpdate)
	ctx = domain.WithSubOp(ctx, "heartbeat")
	assert.Equal(t, domain.OpUpdate, domain.OpFromCtx(ctx),
		"WithSubOp must not clobber Op")
	assert.Equal(t, "heartbeat", domain.SubOpFromCtx(ctx))
}

// --- Service auto-injection tests ---

func TestService_AutoInjectsOpCreate(t *testing.T) {
	repo := newMockRepo()
	v := &capturingValidator{}
	svc := domain.NewService[testEntity](repo, domain.WithValidation[testEntity](v))

	require.NoError(t, svc.Create(context.Background(), &testEntity{ID: "1", Name: "a"}))

	op, _, calls := v.snapshot()
	assert.Equal(t, 1, calls)
	assert.Equal(t, domain.OpCreate, op,
		"Service.Create must auto-inject OpCreate before invoking Validator")
}

func TestService_AutoInjectsOpUpdate(t *testing.T) {
	repo := newMockRepo()
	v := &capturingValidator{}
	svc := domain.NewService[testEntity](repo, domain.WithValidation[testEntity](v))
	ctx := context.Background()

	// Pre-seed via Create; then snapshot resets the validator's view
	// before we exercise Update.
	require.NoError(t, svc.Create(ctx, &testEntity{ID: "1", Name: "a"}))
	require.NoError(t, svc.Update(ctx, &testEntity{ID: "1", Name: "b"}))

	op, _, calls := v.snapshot()
	assert.Equal(t, 2, calls)
	assert.Equal(t, domain.OpUpdate, op,
		"Service.Update must auto-inject OpUpdate before invoking Validator")
}

func TestService_AutoInjectsOpDelete(t *testing.T) {
	// Delete has no validator path (no entity to validate), so we
	// observe the auto-injection through a pre-event subscriber that
	// records the ctx-derived Op alongside the payload Op.
	repo := newMockRepo()
	require.NoError(t, repo.Create(context.Background(), &testEntity{ID: "1", Name: "doomed"}))

	pub := &opCapturingPublisher{}
	svc := domain.NewService[testEntity](repo, domain.WithPublisher[testEntity](pub))

	require.NoError(t, svc.Delete(context.Background(), "1"))

	pub.mu.Lock()
	defer pub.mu.Unlock()
	require.NotEmpty(t, pub.observed, "at least one pre-event must fire")
	for _, obs := range pub.observed {
		assert.Equal(t, domain.OpDelete, obs.ctxOp,
			"Service.Delete must auto-inject OpDelete on every pre-event ctx")
		assert.Equal(t, domain.OpDelete, obs.payloadOp,
			"PreEntityPayload.Op must agree with OpFromCtx for Delete")
	}
}

func TestService_AutoInjectOverridesCallerWithOp(t *testing.T) {
	// Caller set OpDelete on ctx; Service.Create must overwrite with
	// OpCreate. Documented behavior: canonical Op is determined by
	// which Service method is running, not by the caller.
	repo := newMockRepo()
	v := &capturingValidator{}
	svc := domain.NewService[testEntity](repo, domain.WithValidation[testEntity](v))

	ctx := domain.WithOp(context.Background(), domain.OpDelete)
	require.NoError(t, svc.Create(ctx, &testEntity{ID: "1", Name: "a"}))

	op, _, _ := v.snapshot()
	assert.Equal(t, domain.OpCreate, op,
		"Service is authoritative: caller's WithOp must lose to Service auto-inject")
}

func TestService_AutoInjectPreservesCallerSubOp(t *testing.T) {
	// SubOp set by caller (e.g. Manager.Archive) survives Service
	// auto-injection — the two are orthogonal channels.
	repo := newMockRepo()
	v := &capturingValidator{}
	svc := domain.NewService[testEntity](repo, domain.WithValidation[testEntity](v))

	require.NoError(t, svc.Create(context.Background(), &testEntity{ID: "1", Name: "a"}))

	ctx := domain.WithSubOp(context.Background(), "archive")
	require.NoError(t, svc.Update(ctx, &testEntity{ID: "1", Name: "b"}))

	op, subOp, _ := v.snapshot()
	assert.Equal(t, domain.OpUpdate, op)
	assert.Equal(t, "archive", subOp,
		"Service auto-injection must not clear caller-supplied SubOp")
}

func TestService_ValidatorReinjectionIsLocalShadow(t *testing.T) {
	// A Validator that calls WithOp on the ctx it received only
	// shadows its own local copy — Service has already injected the
	// authoritative Op, and downstream subscribers (publishPrePersisted)
	// keep seeing the Service-injected value.
	repo := newMockRepo()
	v := &reinjectingValidator{}
	svc := domain.NewService[testEntity](repo, domain.WithValidation[testEntity](v))
	ctx := context.Background()

	// Pre-seed via Create (validator captures OpCreate the first time
	// — fine, snapshot records the latest) then exercise Update.
	require.NoError(t, svc.Create(ctx, &testEntity{ID: "1", Name: "x"}))
	require.NoError(t, svc.Update(ctx, &testEntity{ID: "1", Name: "y"}))

	from, after := v.snapshot()
	assert.Equal(t, domain.OpUpdate, from,
		"Validator sees Service-injected OpUpdate from ctx on the latest call")
	assert.Equal(t, domain.OpCreate, after,
		"Validator's local WithOp produces a shadowed ctx for its own use")
}

// --- payload alignment: PreEntityPayload.Op matches OpFromCtx ---

// opObservation records both the ctx-derived Op and the payload Op
// observed at one Publish call site.
type opObservation struct {
	topic     string
	ctxOp     domain.Op
	payloadOp domain.Op
}

// opCapturingPublisher records, for every pre-event, the Op the
// validator-side helper would see (OpFromCtx) and the Op the bus
// payload carries. The two must agree post-injection.
type opCapturingPublisher struct {
	mu       sync.Mutex
	observed []opObservation
}

func (p *opCapturingPublisher) Publish(ctx context.Context, topic, _ string, payload any) error {
	pp, ok := payload.(domain.PreEntityPayload)
	if !ok {
		return nil // ignore non-pre payloads (post-events use map[string]string)
	}
	p.mu.Lock()
	p.observed = append(p.observed, opObservation{
		topic:     topic,
		ctxOp:     domain.OpFromCtx(ctx),
		payloadOp: pp.Op,
	})
	p.mu.Unlock()
	return nil
}

func TestService_PreEventPayload_OpMatchesOpFromCtx(t *testing.T) {
	repo := newMockRepo()
	pub := &opCapturingPublisher{}
	svc := domain.NewService[testEntity](repo, domain.WithPublisher[testEntity](pub))
	ctx := context.Background()

	require.NoError(t, svc.Create(ctx, &testEntity{ID: "1", Name: "a"}))
	require.NoError(t, svc.Update(ctx, &testEntity{ID: "1", Name: "b"}))
	require.NoError(t, svc.Delete(ctx, "1"))

	pub.mu.Lock()
	defer pub.mu.Unlock()
	require.Len(t, pub.observed, 6,
		"two pre-events per CRUD action × three actions")

	for _, obs := range pub.observed {
		assert.Equalf(t, obs.payloadOp, obs.ctxOp,
			"topic %q: payload.Op (%q) must equal OpFromCtx(ctx) (%q)",
			obs.topic, obs.payloadOp, obs.ctxOp)
	}

	// Spot-check the actual Op values too: pre-events fire in
	// Create→Update→Delete order with the canonical Op for each.
	wantOps := []domain.Op{
		domain.OpCreate, domain.OpCreate,
		domain.OpUpdate, domain.OpUpdate,
		domain.OpDelete, domain.OpDelete,
	}
	for i, obs := range pub.observed {
		assert.Equal(t, wantOps[i], obs.ctxOp, "observed[%d].ctxOp", i)
	}
}
