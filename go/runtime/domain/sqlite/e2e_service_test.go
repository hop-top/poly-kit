package sqlite_test

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/runtime/bus"
	"hop.top/kit/go/runtime/domain"
	"hop.top/kit/go/runtime/domain/sqlite"
	"hop.top/kit/go/storage/sqlstore"
)

// --- E2E service helpers ---

const e2eSvcTableSQL = `CREATE TABLE IF NOT EXISTS svc_items (
	id   TEXT PRIMARY KEY,
	name TEXT NOT NULL
);`

type svcItem struct {
	ID   string
	Name string
}

func (i svcItem) GetID() string { return i.ID }

func scanSvcItem(row *sql.Row) (svcItem, error) {
	var it svcItem
	err := row.Scan(&it.ID, &it.Name)
	return it, err
}

func scanSvcItemRows(rows *sql.Rows) (svcItem, error) {
	var it svcItem
	err := rows.Scan(&it.ID, &it.Name)
	return it, err
}

func bindSvcItem(it svcItem) ([]string, []any) {
	return []string{"id", "name"}, []any{it.ID, it.Name}
}

type svcValidator struct {
	err error
}

func (v *svcValidator) Validate(_ context.Context, _ svcItem) error {
	return v.err
}

// busAdapter adapts bus.Bus to domain.EventPublisher.
type busAdapter struct{ b bus.Bus }

func (a *busAdapter) Publish(ctx context.Context, topic, source string, payload any) error {
	return a.b.Publish(ctx, bus.NewEvent(bus.Topic(topic), source, payload))
}

// e2eSvcEnv bundles all real deps for service E2E tests.
type e2eSvcEnv struct {
	store *sqlstore.Store
	repo  *sqlite.SQLiteRepository[svcItem]
	audit *sqlite.SQLiteAuditRepository
	bus   bus.Bus
	svc   *domain.Service[svcItem]
}

func setupSvcEnv(t *testing.T, v domain.Validator[svcItem]) *e2eSvcEnv {
	t.Helper()

	store, err := sqlstore.Open(
		filepath.Join(t.TempDir(), "svc.db"),
		sqlstore.Options{MigrateSQL: e2eSvcTableSQL},
	)
	require.NoError(t, err)
	t.Cleanup(func() { store.Close() })

	repo := sqlite.NewSQLiteRepository[svcItem](
		store, "svc_items", scanSvcItem, scanSvcItemRows, bindSvcItem,
	)

	ar := sqlite.NewSQLiteAuditRepository(store)
	require.NoError(t, ar.CreateTable(context.Background()))

	b := bus.New()
	t.Cleanup(func() { b.Close(context.Background()) })

	opts := []domain.Option[svcItem]{
		domain.WithAudit[svcItem](ar),
		domain.WithPublisher[svcItem](&busAdapter{b: b}),
	}
	if v != nil {
		opts = append(opts, domain.WithValidation[svcItem](v))
	}

	svc := domain.NewService[svcItem](repo, opts...)

	return &e2eSvcEnv{
		store: store,
		repo:  repo,
		audit: ar,
		bus:   b,
		svc:   svc,
	}
}

// --- E2E tests (US-0008) ---

func TestE2E_Service_ValidatorRejectionBlocksCreate(t *testing.T) {
	v := &svcValidator{err: fmt.Errorf("%w: name required", domain.ErrValidation)}
	env := setupSvcEnv(t, v)
	ctx := context.Background()

	// Track bus events to confirm none fire.
	var busEvents []string
	var mu sync.Mutex
	env.bus.SubscribeAsync("kit.runtime.entity.#", func(_ context.Context, e bus.Event) {
		mu.Lock()
		busEvents = append(busEvents, string(e.Topic))
		mu.Unlock()
	})

	err := env.svc.Create(ctx, &svcItem{ID: "v1", Name: ""})
	assert.ErrorIs(t, err, domain.ErrValidation)

	// No entity persisted.
	_, err = env.repo.Get(ctx, "v1")
	assert.ErrorIs(t, err, domain.ErrNotFound)

	// No audit entries.
	entries, err := env.audit.ListEntries(ctx, "v1")
	require.NoError(t, err)
	assert.Empty(t, entries)

	// pre_validated fires before validation (its whole point); but
	// pre_persisted and post-events must not fire on a rejection.
	env.bus.Close(context.Background())
	mu.Lock()
	assert.Equal(t, []string{"kit.runtime.entity.pre_validated"}, busEvents,
		"only pre_validated should fire when validation rejects")
	mu.Unlock()
}

func TestE2E_Service_CreateAuditAndBusEvent(t *testing.T) {
	env := setupSvcEnv(t, nil)
	ctx := context.Background()

	var topics []string
	var mu sync.Mutex
	env.bus.SubscribeAsync("kit.runtime.entity.#", func(_ context.Context, e bus.Event) {
		mu.Lock()
		topics = append(topics, string(e.Topic))
		mu.Unlock()
	})

	require.NoError(t, env.svc.Create(ctx, &svcItem{ID: "s1", Name: "alpha"}))

	// Verify audit entry.
	entries, err := env.audit.ListEntries(ctx, "s1")
	require.NoError(t, err)
	require.Len(t, entries, 1)
	assert.Equal(t, "created", entries[0].Action)

	// Drain bus and verify event.
	env.bus.Close(context.Background())
	mu.Lock()
	assert.Contains(t, topics, "kit.runtime.entity.created")
	mu.Unlock()
}

func TestE2E_Service_UpdateAuditAndBusEvent(t *testing.T) {
	env := setupSvcEnv(t, nil)
	ctx := context.Background()

	var topics []string
	var mu sync.Mutex
	env.bus.SubscribeAsync("kit.runtime.entity.#", func(_ context.Context, e bus.Event) {
		mu.Lock()
		topics = append(topics, string(e.Topic))
		mu.Unlock()
	})

	require.NoError(t, env.svc.Create(ctx, &svcItem{ID: "s1", Name: "old"}))
	require.NoError(t, env.svc.Update(ctx, &svcItem{ID: "s1", Name: "new"}))

	entries, err := env.audit.ListEntries(ctx, "s1")
	require.NoError(t, err)
	require.Len(t, entries, 2)
	assert.Equal(t, "created", entries[0].Action)
	assert.Equal(t, "updated", entries[1].Action)

	env.bus.Close(context.Background())
	mu.Lock()
	assert.Contains(t, topics, "kit.runtime.entity.updated")
	mu.Unlock()
}

func TestE2E_Service_DeleteAuditBusAndGone(t *testing.T) {
	env := setupSvcEnv(t, nil)
	ctx := context.Background()

	var topics []string
	var mu sync.Mutex
	env.bus.SubscribeAsync("kit.runtime.entity.#", func(_ context.Context, e bus.Event) {
		mu.Lock()
		topics = append(topics, string(e.Topic))
		mu.Unlock()
	})

	require.NoError(t, env.svc.Create(ctx, &svcItem{ID: "s1", Name: "doomed"}))
	require.NoError(t, env.svc.Delete(ctx, "s1"))

	// Confirm entity gone.
	_, err := env.svc.Get(ctx, "s1")
	assert.ErrorIs(t, err, domain.ErrNotFound)

	// Audit trail preserved.
	entries, err := env.audit.ListEntries(ctx, "s1")
	require.NoError(t, err)
	require.Len(t, entries, 2)
	assert.Equal(t, "created", entries[0].Action)
	assert.Equal(t, "deleted", entries[1].Action)

	env.bus.Close(context.Background())
	mu.Lock()
	assert.Contains(t, topics, "kit.runtime.entity.deleted")
	mu.Unlock()
}

func TestE2E_Service_MiddlewareOrder(t *testing.T) {
	// Verify: validate -> repo -> audit -> bus
	v := &svcValidator{err: nil}
	env := setupSvcEnv(t, v)
	ctx := context.Background()

	var order []string
	var mu sync.Mutex

	// Sync bus subscriber records when bus fires.
	env.bus.Subscribe("kit.runtime.entity.#", func(_ context.Context, e bus.Event) error {
		mu.Lock()
		order = append(order, "bus:"+string(e.Topic))
		mu.Unlock()
		return nil
	})

	require.NoError(t, env.svc.Create(ctx, &svcItem{ID: "ord", Name: "test"}))

	// Repo persisted (validate passed, repo ran).
	got, err := env.repo.Get(ctx, "ord")
	require.NoError(t, err)
	assert.Equal(t, "test", got.Name)

	// Audit ran after repo.
	entries, err := env.audit.ListEntries(ctx, "ord")
	require.NoError(t, err)
	require.Len(t, entries, 1)
	assert.Equal(t, "created", entries[0].Action)

	// Bus fired after audit.
	mu.Lock()
	assert.Contains(t, order, "bus:kit.runtime.entity.created")
	mu.Unlock()

	// Now make validator fail — nothing else should run.
	v.err = domain.ErrValidation
	err = env.svc.Create(ctx, &svcItem{ID: "ord2", Name: "fail"})
	assert.ErrorIs(t, err, domain.ErrValidation)

	_, err = env.repo.Get(ctx, "ord2")
	assert.ErrorIs(t, err, domain.ErrNotFound)

	entries2, err := env.audit.ListEntries(ctx, "ord2")
	require.NoError(t, err)
	assert.Empty(t, entries2)
}
