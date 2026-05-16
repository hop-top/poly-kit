package toolspec

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/storage/sqlstore"
)

// mockSource is a trivial Source for testing.
type mockSource struct {
	spec  *ToolSpec
	err   error
	calls int
}

func (m *mockSource) Resolve(_ string) (*ToolSpec, error) {
	m.calls++
	return m.spec, m.err
}

func TestRegistry_SingleSource(t *testing.T) {
	src := &mockSource{spec: &ToolSpec{
		Name:  "kubectl",
		Flags: []Flag{{Name: "--namespace"}},
	}}
	reg := NewRegistry(WithSource(src))

	got, err := reg.Resolve("kubectl")
	require.NoError(t, err)
	assert.Equal(t, "kubectl", got.Name)
	assert.Len(t, got.Flags, 1)
	assert.Equal(t, 1, src.calls)
}

func TestRegistry_MergeOrder(t *testing.T) {
	first := &mockSource{spec: &ToolSpec{
		Name:  "git",
		Flags: []Flag{{Name: "--verbose"}},
	}}
	second := &mockSource{spec: &ToolSpec{
		Name:     "git-alt",
		Flags:    []Flag{{Name: "--quiet"}},
		Commands: []Command{{Name: "push"}},
	}}

	reg := NewRegistry(WithSource(first), WithSource(second))

	got, err := reg.Resolve("git")
	require.NoError(t, err)

	// Name comes from first source.
	assert.Equal(t, "git", got.Name)
	// Flags come from first source (non-empty, so second is ignored).
	assert.Equal(t, []Flag{{Name: "--verbose"}}, got.Flags)
	// Commands filled by second source (first had none).
	assert.Equal(t, []Command{{Name: "push"}}, got.Commands)
}

func TestRegistry_CacheHit(t *testing.T) {
	store := openTestStore(t, 10*time.Minute)
	src := &mockSource{spec: &ToolSpec{Name: "docker"}}
	reg := NewRegistry(WithSource(src), WithCache(store))

	// First call populates cache.
	got1, err := reg.Resolve("docker")
	require.NoError(t, err)
	assert.Equal(t, "docker", got1.Name)
	assert.Equal(t, 1, src.calls)

	// Second call returns from cache; source not queried again.
	got2, err := reg.Resolve("docker")
	require.NoError(t, err)
	assert.Equal(t, "docker", got2.Name)
	assert.Equal(t, 1, src.calls, "source should not be called on cache hit")
}

func TestRegistry_CacheMiss(t *testing.T) {
	store := openTestStore(t, 10*time.Minute)
	src := &mockSource{spec: &ToolSpec{Name: "npm"}}
	reg := NewRegistry(WithSource(src), WithCache(store))

	// Resolve "npm" to populate cache.
	_, err := reg.Resolve("npm")
	require.NoError(t, err)
	assert.Equal(t, 1, src.calls)

	// Resolve a different tool — cache miss, source queried again.
	got, err := reg.Resolve("yarn")
	require.NoError(t, err)
	assert.Equal(t, "npm", got.Name) // mockSource always returns same spec
	assert.Equal(t, 2, src.calls)
}

func TestRegistry_CacheExpiry(t *testing.T) {
	// Store with 1ms TTL — entries expire almost immediately.
	store := openTestStore(t, 1*time.Millisecond)
	src := &mockSource{spec: &ToolSpec{Name: "cargo"}}
	reg := NewRegistry(WithSource(src), WithCache(store))

	_, err := reg.Resolve("cargo")
	require.NoError(t, err)
	assert.Equal(t, 1, src.calls)

	// Wait for TTL to expire.
	time.Sleep(5 * time.Millisecond)

	_, err = reg.Resolve("cargo")
	require.NoError(t, err)
	assert.Equal(t, 2, src.calls, "source should be called after TTL expiry")
}

func TestRegistry_EmptyResultSetsName(t *testing.T) {
	src := &mockSource{spec: nil}
	reg := NewRegistry(WithSource(src))

	got, err := reg.Resolve("missing")
	require.NoError(t, err)
	assert.Equal(t, "missing", got.Name, "empty result should have tool name")
}

// openTestStore returns a Store backed by a temp SQLite DB.
func openTestStore(t *testing.T, ttl time.Duration) *sqlstore.Store {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.db")
	s, err := sqlstore.Open(path, sqlstore.Options{TTL: ttl})
	require.NoError(t, err)
	t.Cleanup(func() { s.Close() })
	return s
}
