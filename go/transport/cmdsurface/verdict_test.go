package cmdsurface

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestVerdict_AsksTheGateOnAProbeAndRunsNothing(t *testing.T) {
	calls := 0
	var probed bool
	b := New(newBridgeTree(),
		WithRunner(countingRunner(&calls, nil)),
		WithPermission(func(ctx context.Context, meta Meta, leaf *Leaf) PermissionDecision {
			probed = IsProbe(ctx)
			return denyCaller("mallory")(ctx, meta, leaf)
		}))
	b.Expose("*", SurfaceREST)
	leaf, err := b.resolveLeaf([]string{"widget", "add"})
	require.NoError(t, err)

	err = b.Verdict(context.Background(), Meta{Surface: SurfaceREST, Caller: "mallory", Established: EstablishedVerified}, leaf)
	require.ErrorIs(t, err, ErrPermissionDenied)
	assert.True(t, probed, "the gate is told it is a probe")
	assert.Equal(t, 0, calls)

	assert.NoError(t, b.Verdict(context.Background(), Meta{Surface: SurfaceREST, Caller: "alice"}, leaf))

	_, err = b.Invoke(context.Background(), Invocation{Path: []string{"widget", "add"}, Meta: Meta{Surface: SurfaceREST}})
	require.NoError(t, err)
	assert.False(t, probed, "a call is not a probe")
}

func TestVerdict_ScopeCheckAnswersFirst(t *testing.T) {
	root := newBridgeTree()
	b := New(root)
	b.Expose("*", SurfaceREST)
	leaf, err := b.resolveLeaf([]string{"widget", "add"})
	require.NoError(t, err)
	leaf.Class.Permissions = []string{"widgets:write"}

	err = b.Verdict(context.Background(), Meta{Surface: SurfaceREST, Established: EstablishedVerified}, leaf)
	require.ErrorIs(t, err, ErrInsufficientScope)
	assert.Equal(t, ReasonInsufficientScope, verdictReason(err))
	assert.Equal(t, ReasonPermissionDenied, verdictReason(errors.Join(ErrPermissionDenied)))
	assert.Empty(t, verdictReason(nil))
}

func TestAdmittedMetaReachesTheRun(t *testing.T) {
	var seen Meta
	var ok bool
	b := New(newBridgeTree(), WithRunner(&fakeRunner{run: func(ctx context.Context, _ Invocation) (Result, error) {
		seen, ok = AdmittedMeta(ctx)
		return Result{}, nil
	}}))
	b.Expose("*", SurfaceREST)
	_, err := b.Invoke(context.Background(), Invocation{
		Path: []string{"widget", "add"},
		Meta: Meta{Surface: SurfaceREST, Caller: "alice", Established: EstablishedVerified},
	})
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, "alice", seen.Caller)
	assert.Equal(t, EstablishedVerified, seen.Established)

	_, ok = AdmittedMeta(context.Background())
	assert.False(t, ok)
}

func TestMetaScopes_OnlyAVerifiedCredentialHoldsScopes(t *testing.T) {
	extra := map[string]string{"scopes": "a, b"}
	assert.Equal(t, []string{"a", "b"}, Meta{Established: EstablishedVerified, Extra: extra}.Scopes())
	assert.Nil(t, Meta{Established: EstablishedTransport, Extra: extra}.Scopes())
	assert.Nil(t, Meta{Extra: extra}.Scopes())
}
