// Story: as an ops lead, I want delete operations to require a
// non-empty `--note` so that we have an audit trail explaining why
// a record was removed.
//
// Background: deletes are destructive and cannot be undone via the
// state machine. An audit note ("cleanup of stale fixtures",
// "GDPR right-to-erasure request #1234") makes the destructive event
// reviewable after the fact. The host tool exposes a `--note` flag
// and stuffs it into ctx via policy.ContextAttrsKey so the policy
// engine can read it from CEL as `context.note`.
//
// Acceptance:
//
//	Given a policy that requires context.note != "" for delete ops
//	When  Delete is called WITHOUT a note in ctx
//	Then  the operation is denied and nothing is deleted
//	When  Delete is called WITH a note in ctx
//	Then  the operation succeeds and the entity is removed
package e2e_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/runtime/bus"
	"hop.top/kit/go/runtime/domain"
	"hop.top/kit/go/runtime/policy"
	"hop.top/kit/go/runtime/policy/withcel"
)

func TestStory_DeleteRequiresNote_DeniesWithoutNote(t *testing.T) {
	t.Parallel()

	cfg, err := policy.LoadConfig(filepath.Join("testdata", "delete-requires-note.yaml"))
	require.NoError(t, err)
	eng, err := withcel.New(cfg, policy.WithPrincipalResolver(staticPrincipal(
		policy.Principal{ID: "ops", Role: "admin", Source: "ctx"},
	)))
	require.NoError(t, err)

	b := bus.New()
	t.Cleanup(func() { _ = b.Close(context.Background()) })
	t.Cleanup(policy.Wire(b, eng))

	repo := &memTaskRepo{}
	svc := domain.NewService[task](repo, domain.WithPublisher[task](&busAdapter{b: b}))

	err = svc.Delete(context.Background(), "task-42")
	require.Error(t, err)
	assert.True(t, errors.Is(err, domain.ErrConflict))
	assert.Contains(t, err.Error(), "delete requires --note")
	assert.Empty(t, repo.deleted, "veto must abort before repo.Delete runs")
}

func TestStory_DeleteRequiresNote_AllowsWithNote(t *testing.T) {
	t.Parallel()

	cfg, err := policy.LoadConfig(filepath.Join("testdata", "delete-requires-note.yaml"))
	require.NoError(t, err)
	eng, err := withcel.New(cfg, policy.WithPrincipalResolver(staticPrincipal(
		policy.Principal{ID: "ops", Role: "admin", Source: "ctx"},
	)))
	require.NoError(t, err)

	b := bus.New()
	t.Cleanup(func() { _ = b.Close(context.Background()) })
	t.Cleanup(policy.Wire(b, eng))

	repo := &memTaskRepo{}
	svc := domain.NewService[task](repo, domain.WithPublisher[task](&busAdapter{b: b}))

	// The host tool reads the --note flag from CLI args and stuffs
	// it into ctx via policy.ContextAttrsKey before invoking kit.
	ctx := context.WithValue(context.Background(), policy.ContextAttrsKey, map[string]any{
		"note": "cleanup of stale fixture per ops review 2026-Q2",
	})

	require.NoError(t, svc.Delete(ctx, "task-42"))
	assert.Equal(t, []string{"task-42"}, repo.deleted)
}

func TestStory_DeleteRequiresNote_NonDeleteOpsUnaffected(t *testing.T) {
	t.Parallel()

	// The policy targets pre_persisted and only fires on delete; a
	// Create with no note must still succeed (the `payload.Op !=
	// "delete" || ...` short-circuit lets non-deletes through).
	cfg, err := policy.LoadConfig(filepath.Join("testdata", "delete-requires-note.yaml"))
	require.NoError(t, err)
	eng, err := withcel.New(cfg, policy.WithPrincipalResolver(staticPrincipal(
		policy.Principal{ID: "ops", Role: "admin", Source: "ctx"},
	)))
	require.NoError(t, err)

	b := bus.New()
	t.Cleanup(func() { _ = b.Close(context.Background()) })
	t.Cleanup(policy.Wire(b, eng))

	repo := &memTaskRepo{}
	svc := domain.NewService[task](repo, domain.WithPublisher[task](&busAdapter{b: b}))

	require.NoError(t, svc.Create(context.Background(), &task{ID: "task-1", Title: "kickoff"}))
	require.Len(t, repo.created, 1)
}
