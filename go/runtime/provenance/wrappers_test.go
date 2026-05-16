package provenance_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"hop.top/kit/go/runtime/provenance"
)

func TestCached_Constructor_Accessors(t *testing.T) {
	now := time.Now().UTC()
	c := provenance.NewCached("hello", provenance.Provenance{
		Source:    provenance.SourceCached,
		URL:       "https://api",
		FetchedAt: now,
	})
	assert.Equal(t, "hello", c.Value())
	assert.True(t, c.IsSet())
	assert.Equal(t, "https://api", c.Provenance().URL)
	assert.Equal(t, "1", c.Provenance().SchemaVersion)
}

func TestSynthesized_Constructor_Accessors(t *testing.T) {
	s := provenance.NewSynthesized(42, provenance.Provenance{
		Source:  provenance.SourceInferred,
		URL:     "algo://score/v1",
		Version: "v1",
	})
	assert.Equal(t, 42, s.Value())
	assert.True(t, s.IsSet())
	assert.Equal(t, provenance.SourceInferred, s.Provenance().Source)
}

func TestSynthesized_AllowsDefaulted(t *testing.T) {
	s := provenance.NewSynthesized("dark", provenance.Provenance{
		Source: provenance.SourceDefaulted,
	})
	assert.True(t, s.IsSet())
	assert.Equal(t, provenance.SourceDefaulted, s.Provenance().Source)
}

func TestCached_RejectsDefaulted(t *testing.T) {
	assert.Panics(t, func() {
		_ = provenance.NewCached("x", provenance.Provenance{Source: provenance.SourceDefaulted})
	})
}

func TestZeroValueWrappers_IsSetFalse(t *testing.T) {
	var c provenance.Cached[string]
	var s provenance.Synthesized[int]
	assert.False(t, c.IsSet())
	assert.False(t, s.IsSet())
	assert.Equal(t, "", c.Value())
	assert.Equal(t, 0, s.Value())
}

func TestCached_MarshalJSON_EmitsInnerValue(t *testing.T) {
	c := provenance.NewCached("hello", provenance.Provenance{
		Source:    provenance.SourceCached,
		URL:       "https://api",
		FetchedAt: time.Now().UTC(),
	})
	raw, err := json.Marshal(c)
	require.NoError(t, err)
	assert.Equal(t, `"hello"`, string(raw))
}

func TestSynthesized_MarshalJSON_EmitsInnerValue(t *testing.T) {
	s := provenance.NewSynthesized(42, provenance.Provenance{
		Source:  provenance.SourceInferred,
		Version: "v1",
	})
	raw, err := json.Marshal(s)
	require.NoError(t, err)
	assert.Equal(t, "42", string(raw))
}

func TestCached_UnmarshalJSON_LeavesProvenanceZero(t *testing.T) {
	var c provenance.Cached[string]
	require.NoError(t, json.Unmarshal([]byte(`"world"`), &c))
	assert.Equal(t, "world", c.Value())
	assert.False(t, c.IsSet())
	assert.True(t, c.Provenance().IsZero())
}

func TestSynthesized_UnmarshalJSON_LeavesProvenanceZero(t *testing.T) {
	var s provenance.Synthesized[int]
	require.NoError(t, json.Unmarshal([]byte(`7`), &s))
	assert.Equal(t, 7, s.Value())
	assert.False(t, s.IsSet())
}

func TestWrapperFillsSchemaVersion(t *testing.T) {
	c := provenance.NewCached("x", provenance.Provenance{
		Source:    provenance.SourceCached,
		URL:       "https://api",
		FetchedAt: time.Now().UTC(),
	})
	assert.Equal(t, "1", c.Provenance().SchemaVersion)
}
