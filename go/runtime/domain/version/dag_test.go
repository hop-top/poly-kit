package version

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDAG_LinearChain(t *testing.T) {
	d := NewDAG()

	v1 := Version{ID: "v1", Timestamp: 1, Hash: "h1"}
	require.NoError(t, d.Append(v1))

	v2 := Version{ID: "v2", ParentIDs: []string{"v1"}, Timestamp: 2, Hash: "h2"}
	require.NoError(t, d.Append(v2))

	v3 := Version{ID: "v3", ParentIDs: []string{"v2"}, Timestamp: 3, Hash: "h3"}
	require.NoError(t, d.Append(v3))

	heads := d.Heads()
	assert.Equal(t, []string{"v3"}, heads)
	assert.False(t, d.IsBranched())
}

func TestDAG_Branch(t *testing.T) {
	d := NewDAG()
	require.NoError(t, d.Append(Version{ID: "v1", Timestamp: 1, Hash: "h1"}))
	require.NoError(t, d.Append(Version{ID: "v2", ParentIDs: []string{"v1"}, Timestamp: 2, Hash: "h2"}))
	require.NoError(t, d.Append(Version{ID: "v3", ParentIDs: []string{"v1"}, Timestamp: 3, Hash: "h3"}))

	assert.True(t, d.IsBranched())
	heads := d.Heads()
	assert.Len(t, heads, 2)
	assert.Contains(t, heads, "v2")
	assert.Contains(t, heads, "v3")
}

func TestDAG_Merge(t *testing.T) {
	d := NewDAG()
	require.NoError(t, d.Append(Version{ID: "v1", Timestamp: 1, Hash: "h1"}))
	require.NoError(t, d.Append(Version{ID: "v2", ParentIDs: []string{"v1"}, Timestamp: 2, Hash: "h2"}))
	require.NoError(t, d.Append(Version{ID: "v3", ParentIDs: []string{"v1"}, Timestamp: 3, Hash: "h3"}))
	// merge
	require.NoError(t, d.Append(Version{ID: "v4", ParentIDs: []string{"v2", "v3"}, Timestamp: 4, Hash: "h4"}))

	assert.False(t, d.IsBranched())
	assert.Equal(t, []string{"v4"}, d.Heads())
}

func TestDAG_Ancestors(t *testing.T) {
	d := NewDAG()
	require.NoError(t, d.Append(Version{ID: "v1", Timestamp: 1, Hash: "h1"}))
	require.NoError(t, d.Append(Version{ID: "v2", ParentIDs: []string{"v1"}, Timestamp: 2, Hash: "h2"}))
	require.NoError(t, d.Append(Version{ID: "v3", ParentIDs: []string{"v2"}, Timestamp: 3, Hash: "h3"}))

	anc := d.Ancestors("v3")
	assert.Len(t, anc, 2)
	assert.Contains(t, anc, "v1")
	assert.Contains(t, anc, "v2")
}

func TestDAG_CommonAncestor(t *testing.T) {
	d := NewDAG()
	require.NoError(t, d.Append(Version{ID: "v1", Timestamp: 1, Hash: "h1"}))
	require.NoError(t, d.Append(Version{ID: "v2", ParentIDs: []string{"v1"}, Timestamp: 2, Hash: "h2"}))
	require.NoError(t, d.Append(Version{ID: "v3", ParentIDs: []string{"v1"}, Timestamp: 3, Hash: "h3"}))

	ca, ok := d.CommonAncestor("v2", "v3")
	assert.True(t, ok)
	assert.Equal(t, "v1", ca)
}

func TestDAG_CommonAncestor_None(t *testing.T) {
	d := NewDAG()
	require.NoError(t, d.Append(Version{ID: "v1", Timestamp: 1, Hash: "h1"}))
	require.NoError(t, d.Append(Version{ID: "v2", Timestamp: 2, Hash: "h2"}))

	_, ok := d.CommonAncestor("v1", "v2")
	assert.False(t, ok)
}

func TestDAG_DuplicateID(t *testing.T) {
	d := NewDAG()
	require.NoError(t, d.Append(Version{ID: "v1", Timestamp: 1, Hash: "h1"}))
	err := d.Append(Version{ID: "v1", Timestamp: 2, Hash: "h2"})
	assert.Error(t, err)
}

func TestDAG_UnknownParent(t *testing.T) {
	d := NewDAG()
	err := d.Append(Version{ID: "v2", ParentIDs: []string{"v1"}, Timestamp: 2, Hash: "h2"})
	assert.Error(t, err)
}

func TestDAG_Get(t *testing.T) {
	d := NewDAG()
	require.NoError(t, d.Append(Version{ID: "v1", Timestamp: 1, Hash: "h1"}))

	v, ok := d.Get("v1")
	assert.True(t, ok)
	assert.Equal(t, "v1", v.ID)

	_, ok = d.Get("missing")
	assert.False(t, ok)
}
