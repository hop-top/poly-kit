// Story: as a security reviewer, I want all entity writes
// (Create/Update/Delete) to require an authenticated principal
// (role != "") so that anonymous mutations are rejected at the
// kit-runtime layer, not just at the transport edge.
//
// Background: relying on "every front-end checks auth" is fragile
// — a forgotten check on an internal CLI or MCP tool can let
// unattributed mutations through. Pinning the requirement at the
// kit guard layer makes the rule defense-in-depth: anything wired
// through kit/runtime gets the gate for free.
//
// Acceptance:
//
//	Given a policy that requires principal.role != "" on pre_validated
//	When  Create / Update / Delete run with an empty-role principal
//	Then  every operation is denied and the repo is untouched
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

// engineWithAnonymousPrincipal is the shared setup for the three
// scenarios in this story. Each scenario uses its own bus + repo so
// they remain parallel-safe.
func engineWithAnonymousPrincipal(t *testing.T) (*policy.Engine, bus.Bus, *memTaskRepo, *domain.Service[task]) {
	t.Helper()

	cfg, err := policy.LoadConfig(filepath.Join("testdata", "writes-need-role.yaml"))
	require.NoError(t, err)
	eng, err := withcel.New(cfg, policy.WithPrincipalResolver(staticPrincipal(
		policy.Principal{ID: "", Role: "", Source: "none"},
	)))
	require.NoError(t, err)

	b := bus.New()
	t.Cleanup(func() { _ = b.Close(context.Background()) })
	t.Cleanup(policy.Wire(b, eng))

	repo := &memTaskRepo{}
	svc := domain.NewService[task](repo, domain.WithPublisher[task](&busAdapter{b: b}))
	return eng, b, repo, svc
}

func TestStory_WritesNeedAuthenticatedRole_DeniesAnonymousCreate(t *testing.T) {
	t.Parallel()

	_, _, repo, svc := engineWithAnonymousPrincipal(t)

	err := svc.Create(context.Background(), &task{ID: "t1", Title: "kickoff"})
	require.Error(t, err)
	assert.True(t, errors.Is(err, domain.ErrConflict))
	assert.Contains(t, err.Error(), "authenticated principal")
	assert.Empty(t, repo.created)
}

func TestStory_WritesNeedAuthenticatedRole_DeniesAnonymousUpdate(t *testing.T) {
	t.Parallel()

	_, _, repo, svc := engineWithAnonymousPrincipal(t)

	err := svc.Update(context.Background(), &task{ID: "t1", Title: "renamed"})
	require.Error(t, err)
	assert.True(t, errors.Is(err, domain.ErrConflict))
	assert.Empty(t, repo.updated)
}

func TestStory_WritesNeedAuthenticatedRole_DeniesAnonymousDelete(t *testing.T) {
	t.Parallel()

	_, _, repo, svc := engineWithAnonymousPrincipal(t)

	err := svc.Delete(context.Background(), "t1")
	require.Error(t, err)
	assert.True(t, errors.Is(err, domain.ErrConflict))
	assert.Empty(t, repo.deleted)
}

func TestStory_WritesNeedAuthenticatedRole_AllowsAuthenticatedCreate(t *testing.T) {
	t.Parallel()

	cfg, err := policy.LoadConfig(filepath.Join("testdata", "writes-need-role.yaml"))
	require.NoError(t, err)
	eng, err := withcel.New(cfg, policy.WithPrincipalResolver(staticPrincipal(
		policy.Principal{ID: "alice", Role: "engineer", Source: "ctx"},
	)))
	require.NoError(t, err)

	b := bus.New()
	t.Cleanup(func() { _ = b.Close(context.Background()) })
	t.Cleanup(policy.Wire(b, eng))

	repo := &memTaskRepo{}
	svc := domain.NewService[task](repo, domain.WithPublisher[task](&busAdapter{b: b}))

	require.NoError(t, svc.Create(context.Background(), &task{ID: "t1", Title: "kickoff"}))
	require.Len(t, repo.created, 1)
}
