package domain_test

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/runtime/domain"
)

// driveCRUD runs Create/Update/Delete on svc with a fresh entity so
// each topic-override test produces exactly nine published events:
// for each CRUD action {pre_validated, pre_persisted, <action>}, in
// CRUD order {create, update, delete}.
func driveCRUD(t *testing.T, svc *domain.Service[testEntity]) {
	t.Helper()
	ctx := context.Background()
	e := &testEntity{ID: "1", Name: "a"}
	require.NoError(t, svc.Create(ctx, e))
	e.Name = "b"
	require.NoError(t, svc.Update(ctx, e))
	require.NoError(t, svc.Delete(ctx, "1"))
}

// expandPrefix returns the expected topic stream for a 3-segment prefix
// after a full Create/Update/Delete cycle.
func expandPrefix(prefix string) []string {
	return []string{
		prefix + ".pre_validated",
		prefix + ".pre_persisted",
		prefix + ".created",
		prefix + ".pre_validated",
		prefix + ".pre_persisted",
		prefix + ".updated",
		prefix + ".pre_validated",
		prefix + ".pre_persisted",
		prefix + ".deleted",
	}
}

func TestService_DefaultTopicsUnchanged(t *testing.T) {
	repo := newMockRepo()
	pub := &mockPublisher{}
	svc := domain.NewService[testEntity](repo, domain.WithPublisher[testEntity](pub))

	driveCRUD(t, svc)

	assert.Equal(t, expandPrefix("kit.runtime.entity"), pub.topics())
}

func TestService_WithTopicPrefix_Workspace(t *testing.T) {
	repo := newMockRepo()
	pub := &mockPublisher{}
	svc := domain.NewService[testEntity](
		repo,
		domain.WithPublisher[testEntity](pub),
		domain.WithTopicPrefix[testEntity]("wsm.runtime.workspace"),
	)

	driveCRUD(t, svc)

	assert.Equal(t, expandPrefix("wsm.runtime.workspace"), pub.topics())
}

func TestService_WithTopicPrefix_DefaultRoundTrip(t *testing.T) {
	repo := newMockRepo()
	pub := &mockPublisher{}
	svc := domain.NewService[testEntity](
		repo,
		domain.WithPublisher[testEntity](pub),
		domain.WithTopicPrefix[testEntity]("kit.runtime.entity"),
	)

	driveCRUD(t, svc)

	assert.Equal(t, expandPrefix("kit.runtime.entity"), pub.topics())
}

func TestService_WithTopics_PartialOverride(t *testing.T) {
	repo := newMockRepo()
	pub := &mockPublisher{}
	svc := domain.NewService[testEntity](
		repo,
		domain.WithPublisher[testEntity](pub),
		domain.WithTopics[testEntity](domain.Topics{
			Created: "myapp.x.y.created",
		}),
	)

	driveCRUD(t, svc)

	// Created overridden; the rest fall back to DefaultTopics.
	assert.Equal(t, []string{
		"kit.runtime.entity.pre_validated",
		"kit.runtime.entity.pre_persisted",
		"myapp.x.y.created",
		"kit.runtime.entity.pre_validated",
		"kit.runtime.entity.pre_persisted",
		"kit.runtime.entity.updated",
		"kit.runtime.entity.pre_validated",
		"kit.runtime.entity.pre_persisted",
		"kit.runtime.entity.deleted",
	}, pub.topics())
}

func TestService_WithTopics_AllOverridden(t *testing.T) {
	repo := newMockRepo()
	pub := &mockPublisher{}
	svc := domain.NewService[testEntity](
		repo,
		domain.WithPublisher[testEntity](pub),
		domain.WithTopics[testEntity](domain.Topics{
			PreValidated: "myapp.x.y.pre_validated",
			PrePersisted: "myapp.x.y.pre_persisted",
			Created:      "myapp.x.y.created",
			Updated:      "myapp.x.y.updated",
			Deleted:      "myapp.x.y.deleted",
		}),
	)

	driveCRUD(t, svc)

	assert.Equal(t, expandPrefix("myapp.x.y"), pub.topics())
}

func TestService_WithTopicPrefix_InvalidPanics(t *testing.T) {
	defer func() {
		r := recover()
		require.NotNil(t, r, "expected panic for non-3-segment prefix")
		msg, ok := r.(string)
		require.True(t, ok, "panic value should be a string, got %T", r)
		assert.Contains(t, msg, "WithTopicPrefix")
		assert.Contains(t, msg, "invalid")
		// Confirm the underlying bus error reaches the caller verbatim
		// so adopters can diagnose without reading domain source.
		assert.True(t,
			strings.Contains(msg, "segments") || strings.Contains(msg, "expected 3"),
			"panic %q should explain the segment-count violation", msg)
	}()

	_ = domain.WithTopicPrefix[testEntity]("invalid")
}

func TestDefaultTopics_ValuesStable(t *testing.T) {
	// Guard against accidental edits to the kit baseline. Adopters
	// rely on these exact strings as the fallback when WithTopics
	// is partial.
	assert.Equal(t, "kit.runtime.entity.pre_validated", string(domain.DefaultTopics.PreValidated))
	assert.Equal(t, "kit.runtime.entity.pre_persisted", string(domain.DefaultTopics.PrePersisted))
	assert.Equal(t, "kit.runtime.entity.created", string(domain.DefaultTopics.Created))
	assert.Equal(t, "kit.runtime.entity.updated", string(domain.DefaultTopics.Updated))
	assert.Equal(t, "kit.runtime.entity.deleted", string(domain.DefaultTopics.Deleted))
}
