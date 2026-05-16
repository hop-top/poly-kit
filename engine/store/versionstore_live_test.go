package store

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestInMemorySetLive_HappyPath: SetLive(false) on a head version
// flips Live to false in subsequent ListVersions output. Idempotent.
// SetLive(true) restores. Sanity check for T-0422 in-memory plumbing.
func TestInMemorySetLive_HappyPath(t *testing.T) {
	ctx := context.Background()
	vs := NewInMemoryVersionStore()

	v1, err := vs.AppendVersion(ctx, "note", "n1", json.RawMessage(`{"v":1}`), nil)
	require.NoError(t, err)
	require.True(t, v1.Live, "fresh version is born live")

	// SetLive(false): mark it dead.
	require.NoError(t, vs.SetLive(ctx, "note", "n1", v1.VersionID, false))
	versions, err := vs.ListVersions(ctx, "note", "n1")
	require.NoError(t, err)
	require.Len(t, versions, 1)
	assert.False(t, versions[0].Live, "after SetLive(false), Live=false")

	// Idempotent: second SetLive(false) is a no-op success.
	require.NoError(t, vs.SetLive(ctx, "note", "n1", v1.VersionID, false))

	// SetLive(true): restore.
	require.NoError(t, vs.SetLive(ctx, "note", "n1", v1.VersionID, true))
	versions, err = vs.ListVersions(ctx, "note", "n1")
	require.NoError(t, err)
	assert.True(t, versions[0].Live, "after SetLive(true), Live=true")

	// Idempotent the other way too.
	require.NoError(t, vs.SetLive(ctx, "note", "n1", v1.VersionID, true))
}

// TestInMemorySetLive_NonHead: SetLive on a non-head returns
// ErrNotAHead. The bit is only meaningful on heads (the prune
// algorithm only consults heads' liveness).
func TestInMemorySetLive_NonHead(t *testing.T) {
	ctx := context.Background()
	vs := NewInMemoryVersionStore()

	v1, err := vs.AppendVersion(ctx, "note", "n1", json.RawMessage(`{"v":1}`), nil)
	require.NoError(t, err)
	v2, err := vs.AppendVersion(ctx, "note", "n1", json.RawMessage(`{"v":2}`), []string{v1.VersionID})
	require.NoError(t, err)

	// v1 has child v2 → not a head.
	err = vs.SetLive(ctx, "note", "n1", v1.VersionID, false)
	assert.ErrorIs(t, err, ErrNotAHead, "SetLive on non-head must return ErrNotAHead")

	// v2 is a head → ok.
	require.NoError(t, vs.SetLive(ctx, "note", "n1", v2.VersionID, false))
}

// TestInMemorySetLive_UnknownVersion: SetLive on an unknown version
// returns a non-nil error (does NOT silently no-op).
func TestInMemorySetLive_UnknownVersion(t *testing.T) {
	ctx := context.Background()
	vs := NewInMemoryVersionStore()

	_, err := vs.AppendVersion(ctx, "note", "n1", json.RawMessage(`{"v":1}`), nil)
	require.NoError(t, err)

	err = vs.SetLive(ctx, "note", "n1", "v_does_not_exist", false)
	assert.Error(t, err)
}

// TestVersionJSONLiveOmitOnTrue verifies the wire-format invariant:
// Live=true → omitted (backward-compat); Live=false → emitted as
// "live": false.
func TestVersionJSONLiveOmitOnTrue(t *testing.T) {
	v := Version{
		Type:      "note",
		ID:        "n1",
		VersionID: "v1",
		Seq:       1,
		Data:      json.RawMessage(`{"v":1}`),
		CreatedAt: "2026-05-04T00:00:00Z",
		Live:      true,
	}
	b, err := json.Marshal(v)
	require.NoError(t, err)
	assert.NotContains(t, string(b), `"live"`, "Live=true must be omitted from JSON")

	v.Live = false
	b, err = json.Marshal(v)
	require.NoError(t, err)
	assert.Contains(t, string(b), `"live":false`, "Live=false must be emitted as \"live\":false")
}

// TestVersionJSONLiveDefaultOnUnmarshal: an absent "live" field on
// input is parsed as Live=true.
func TestVersionJSONLiveDefaultOnUnmarshal(t *testing.T) {
	in := `{"type":"note","id":"n1","version_id":"v1","seq":1,"data":{"v":1},"created_at":"2026-05-04T00:00:00Z"}`
	var v Version
	require.NoError(t, json.Unmarshal([]byte(in), &v))
	assert.True(t, v.Live, "absent \"live\" field parses as Live=true")

	in = `{"type":"note","id":"n1","version_id":"v1","seq":1,"data":{"v":1},"created_at":"2026-05-04T00:00:00Z","live":false}`
	require.NoError(t, json.Unmarshal([]byte(in), &v))
	assert.False(t, v.Live, "explicit \"live\":false parses as Live=false")
}

// TestSentinels: ErrNotAHead and ErrCannotAbandonLastLiveHead are
// distinguishable via errors.Is.
func TestSentinels(t *testing.T) {
	require.True(t, errors.Is(ErrNotAHead, ErrNotAHead))
	require.True(t, errors.Is(ErrCannotAbandonLastLiveHead, ErrCannotAbandonLastLiveHead))
	require.False(t, errors.Is(ErrNotAHead, ErrCannotAbandonLastLiveHead))
}
