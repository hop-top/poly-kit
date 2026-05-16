package provenance_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"hop.top/kit/go/runtime/provenance"
)

func TestNormalize_HTTP(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"https://API.example.com/users/1", "https://api.example.com/users/1"},
		{"https://api.example.com:443/x", "https://api.example.com/x"},
		{"http://example.com:80/y", "http://example.com/y"},
		{"https://api/x?b=2&a=1", "https://api/x?a=1&b=2"},
		{"https://api/x#frag", "https://api/x"},
	}
	for _, tc := range tests {
		got, err := provenance.Normalize(tc.in)
		require.NoError(t, err, tc.in)
		assert.Equal(t, tc.want, got, tc.in)
	}
}

func TestNormalize_NonHTTP_PreservesScheme(t *testing.T) {
	tests := []string{
		"doc://scoring/v1",
		"exec://git",
		"sql://users/by-id",
	}
	for _, in := range tests {
		got, err := provenance.Normalize(in)
		require.NoError(t, err, in)
		assert.Equal(t, in, got, in)
	}
}

func TestNormalize_Empty(t *testing.T) {
	_, err := provenance.Normalize("")
	assert.Error(t, err)
}
