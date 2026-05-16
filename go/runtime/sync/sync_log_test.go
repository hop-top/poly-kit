package sync

import (
	"bytes"
	"context"
	"errors"
	"testing"

	charmlog "charm.land/log/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"hop.top/kit/go/runtime/domain"
)

// errRepo is a repro that fails Delete/Update/Create with sentinel errors,
// used to trigger replicator's apply-path error log lines.
type errRepo struct {
	deleteErr error
	updateErr error
	createErr error
}

func (e *errRepo) Create(_ context.Context, _ *replTestEntity) error {
	return e.createErr
}
func (e *errRepo) Get(_ context.Context, _ string) (*replTestEntity, error) {
	return nil, domain.ErrNotFound
}
func (e *errRepo) List(_ context.Context, _ domain.Query) ([]replTestEntity, error) {
	return nil, nil
}
func (e *errRepo) Update(_ context.Context, _ *replTestEntity) error {
	return e.updateErr
}
func (e *errRepo) Delete(_ context.Context, _ string) error {
	return e.deleteErr
}

// TestReplicator_WithLogger_CapturesApplyDeleteError verifies that
// WithLogger routes apply-path errors through the injected logger and
// that key-value pairs (entity_id, err) reach the buffer.
func TestReplicator_WithLogger_CapturesApplyDeleteError(t *testing.T) {
	var buf bytes.Buffer
	logger := charmlog.NewWithOptions(&buf, charmlog.Options{
		Level: charmlog.ErrorLevel,
	})

	repo := &errRepo{deleteErr: errors.New("boom delete")}
	rep := NewReplicator[replTestEntity](repo,
		WithLogger[replTestEntity](logger),
	)

	// Trigger the delete branch: After == nil routes through repo.Delete.
	rep.applyDiff(context.Background(), Diff{
		EntityID:  "e-del",
		Operation: OpDelete,
		Timestamp: Timestamp{Physical: 1, NodeID: "local"},
	})

	out := buf.String()
	require.NotEmpty(t, out, "logger should have captured error output")
	assert.Contains(t, out, "sync: apply delete failed")
	assert.Contains(t, out, "entity_id")
	assert.Contains(t, out, "e-del")
	assert.Contains(t, out, "boom delete")
}

// TestReplicator_WithLogger_CapturesApplyCreateError exercises the
// update-then-create fallback path on the apply side.
func TestReplicator_WithLogger_CapturesApplyCreateError(t *testing.T) {
	var buf bytes.Buffer
	logger := charmlog.NewWithOptions(&buf, charmlog.Options{
		Level: charmlog.ErrorLevel,
	})

	repo := &errRepo{
		updateErr: domain.ErrNotFound,
		createErr: errors.New("boom create"),
	}
	rep := NewReplicator[replTestEntity](repo,
		WithLogger[replTestEntity](logger),
	)

	rep.applyDiff(context.Background(), Diff{
		EntityID:  "e-new",
		Operation: OpCreate,
		Timestamp: Timestamp{Physical: 1, NodeID: "local"},
		After:     []byte(`{"id":"e-new","name":"x"}`),
	})

	out := buf.String()
	require.NotEmpty(t, out)
	assert.Contains(t, out, "sync: apply create failed")
	assert.Contains(t, out, "e-new")
	assert.Contains(t, out, "boom create")
}

// TestReplicator_WithLogger_CapturesUnmarshalError verifies the JSON
// decode failure branch routes through the configured logger.
func TestReplicator_WithLogger_CapturesUnmarshalError(t *testing.T) {
	var buf bytes.Buffer
	logger := charmlog.NewWithOptions(&buf, charmlog.Options{
		Level: charmlog.ErrorLevel,
	})

	rep := NewReplicator[replTestEntity](newReplMemRepo(),
		WithLogger[replTestEntity](logger),
	)

	rep.applyDiff(context.Background(), Diff{
		EntityID:  "e-bad",
		Operation: OpUpdate,
		Timestamp: Timestamp{Physical: 1, NodeID: "local"},
		After:     []byte(`{not-json`),
	})

	out := buf.String()
	require.NotEmpty(t, out)
	assert.Contains(t, out, "sync: unmarshal diff failed")
	assert.Contains(t, out, "e-bad")
}

// TestReplicator_DefaultLoggerSet verifies NewReplicator wires a
// non-nil default logger when WithLogger is omitted.
func TestReplicator_DefaultLoggerSet(t *testing.T) {
	rep := NewReplicator[replTestEntity](newReplMemRepo())
	assert.NotNil(t, rep.logger, "default logger should be non-nil")
}
