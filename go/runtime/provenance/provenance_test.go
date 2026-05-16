package provenance_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"hop.top/kit/go/runtime/provenance"
)

func TestProvenance_Validate(t *testing.T) {
	now := time.Now().UTC()
	tests := []struct {
		name string
		in   provenance.Provenance
		ok   bool
	}{
		{
			name: "cached with url + fetched_at",
			in: provenance.Provenance{
				SchemaVersion: "1",
				Source:        provenance.SourceCached,
				URL:           "https://api/users/1",
				FetchedAt:     now,
			},
			ok: true,
		},
		{
			name: "inferred with version (no URL)",
			in: provenance.Provenance{
				SchemaVersion: "1",
				Source:        provenance.SourceInferred,
				Version:       "v1.2.3",
			},
			ok: true,
		},
		{
			name: "defaulted needs neither url nor version",
			in: provenance.Provenance{
				SchemaVersion: "1",
				Source:        provenance.SourceDefaulted,
			},
			ok: true,
		},
		{
			name: "cached missing fetched_at",
			in: provenance.Provenance{
				SchemaVersion: "1",
				Source:        provenance.SourceCached,
				URL:           "https://api",
			},
			ok: false,
		},
		{
			name: "inferred missing url + version",
			in: provenance.Provenance{
				SchemaVersion: "1",
				Source:        provenance.SourceInferred,
			},
			ok: false,
		},
		{
			name: "missing schema version",
			in: provenance.Provenance{
				Source: provenance.SourceCached,
				URL:    "https://api",
			},
			ok: false,
		},
		{
			name: "unknown source",
			in: provenance.Provenance{
				SchemaVersion: "1",
				Source:        "made-up",
			},
			ok: false,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.in.Validate()
			if tc.ok {
				assert.NoError(t, err)
			} else {
				assert.Error(t, err)
			}
		})
	}
}

func TestProvenance_JSON_RoundTrip(t *testing.T) {
	now := time.Date(2026, 5, 11, 12, 0, 0, 0, time.UTC)
	in := provenance.Provenance{
		SchemaVersion: "1",
		Source:        provenance.SourceCached,
		URL:           "https://api/users/1",
		FetchedAt:     now,
		Version:       "etag-abc",
	}
	raw, err := json.Marshal(in)
	require.NoError(t, err)
	var out provenance.Provenance
	require.NoError(t, json.Unmarshal(raw, &out))
	assert.Equal(t, in.SchemaVersion, out.SchemaVersion)
	assert.Equal(t, in.Source, out.Source)
	assert.Equal(t, in.URL, out.URL)
	assert.Equal(t, in.Version, out.Version)
	assert.True(t, in.FetchedAt.Equal(out.FetchedAt))
}

func TestProvenance_IsZero(t *testing.T) {
	assert.True(t, provenance.Provenance{}.IsZero())
	assert.False(t, provenance.Provenance{SchemaVersion: "1"}.IsZero())
	assert.False(t, provenance.Provenance{URL: "x"}.IsZero())
}

func TestProvenance_MarshalFillsSchemaVersion(t *testing.T) {
	in := provenance.Provenance{Source: provenance.SourceDefaulted}
	raw, err := json.Marshal(in)
	require.NoError(t, err)
	assert.Contains(t, string(raw), `"schema_version":"1"`)
}
