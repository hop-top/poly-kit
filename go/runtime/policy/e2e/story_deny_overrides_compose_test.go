// Story: as an ops lead, I want multiple matching policies on the
// same topic to compose with deny-overrides (any deny wins) so that
// the most restrictive rule always applies.
//
// Background: composition matters because policies accumulate over
// time — one policy gates auth, another gates change-freeze
// windows, a third enforces audit notes. Reading them as an "any
// deny wins" set keeps each policy self-contained: an author
// reasoning about a single rule never has to think about how it
// interacts with other rules. The engine evaluates EVERY matching
// policy (no short-circuit) so audit hooks see the full set; the
// FIRST denying policy supplies the surfaced error message.
//
// Acceptance:
//
//	Given two matching policies that both ALLOW
//	When  Create runs
//	Then  it succeeds
//
//	Given two matching policies where the second DENIES
//	When  Create runs
//	Then  it is rejected with the first-denying policy's message
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

func TestStory_DenyOverridesCompose_AllAllowAllows(t *testing.T) {
	t.Parallel()

	cfg, err := policy.LoadConfig(filepath.Join("testdata", "multi-policy-deny-wins.yaml"))
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

	// Authenticated principal AND no freeze flag → both policies allow.
	require.NoError(t, svc.Create(context.Background(), &task{ID: "t1", Title: "kickoff"}))
	require.Len(t, repo.created, 1)
}

func TestStory_DenyOverridesCompose_FreezeDenies(t *testing.T) {
	t.Parallel()

	cfg, err := policy.LoadConfig(filepath.Join("testdata", "multi-policy-deny-wins.yaml"))
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

	// Auth passes; freeze flag is on → second policy denies.
	ctx := context.WithValue(context.Background(), policy.ContextAttrsKey, map[string]any{
		"request_attrs": map[string]any{"freeze": true},
	})
	err = svc.Create(ctx, &task{ID: "t1", Title: "kickoff"})
	require.Error(t, err)
	assert.True(t, errors.Is(err, domain.ErrConflict))
	assert.Contains(t, err.Error(), "change freeze window")
	assert.Empty(t, repo.created)
}

func TestStory_DenyOverridesCompose_FirstDenyWins(t *testing.T) {
	t.Parallel()

	// Both rules deny: anonymous principal trips writes-need-role,
	// and the freeze flag trips writes-blocked-during-freeze. The
	// FIRST denying policy in YAML order supplies the message.
	cfg, err := policy.LoadConfig(filepath.Join("testdata", "multi-policy-deny-wins.yaml"))
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

	ctx := context.WithValue(context.Background(), policy.ContextAttrsKey, map[string]any{
		"request_attrs": map[string]any{"freeze": true},
	})
	err = svc.Create(ctx, &task{ID: "t1", Title: "kickoff"})
	require.Error(t, err)
	assert.True(t, errors.Is(err, domain.ErrConflict))
	assert.Contains(t, err.Error(), "authenticated principal",
		"first denying policy in YAML order supplies the message")

	var pde *policy.PolicyDeniedError
	require.True(t, errors.As(err, &pde))
	assert.Equal(t, "writes-need-authenticated-role", pde.PolicyName)
}
