package idemstore_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"hop.top/kit/go/console/cli/idemstore"
)

func TestIdemstore_Memory_RoundTrip(t *testing.T) {
	s := idemstore.Memory()
	defer s.Close()

	ctx := context.Background()

	_, hit, err := s.Lookup(ctx, "k")
	require.NoError(t, err)
	assert.False(t, hit)

	require.NoError(t, s.Record(ctx, "k", idemstore.Result{
		Output: []byte("mem"), ExitCode: 0,
	}))

	got, hit, err := s.Lookup(ctx, "k")
	require.NoError(t, err)
	require.True(t, hit)
	assert.Equal(t, []byte("mem"), got.Output)
	assert.Equal(t, "k", got.Key)
}
