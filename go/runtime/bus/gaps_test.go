package bus_test

// Gap tests for `hop.top/kit/go/runtime/bus`. Same convention as
// go/console/cli/gaps_test.go — Skip + pin until the gap is closed.
// Surfaced by the dpkms review.

import (
	"os"
	"testing"

	"github.com/stretchr/testify/require"
	"hop.top/kit/go/runtime/bus"
)

// Gap: kit/runtime/bus has no AuthFromEnv() helper.
//
// Today bus exposes ModeFromEnv() (config.go:55) for enforcement
// mode and StaticTokenAuth{Token_} (network_auth.go:9) for the auth
// shape — but adopters that want "read DPKMS_BUS_TOKEN, fall back to
// BUS_TOKEN, build a StaticTokenAuth" must hand-roll that bridge.
// Surfaced by dpkms hand-rolling the env read in its bus wiring.
//
// Desired API:
//
//	auth, ok := bus.AuthFromEnv("DPKMS_BUS_TOKEN", "BUS_TOKEN")
//	if !ok { /* no token configured */ }
//	// auth is a *StaticTokenAuth with Token_ populated from the
//	// first non-empty env var in the list.
func TestGap_BusAuthFromEnv_Missing(t *testing.T) {
	t.Run("first var found", func(t *testing.T) {
		t.Setenv("DPKMS_BUS_TOKEN", "tok-1")
		t.Setenv("BUS_TOKEN", "tok-fallback")

		auth, ok := bus.AuthFromEnv("DPKMS_BUS_TOKEN", "BUS_TOKEN")
		require.True(t, ok)
		require.NotNil(t, auth)
		tok, err := auth.Token()
		require.NoError(t, err)
		require.Equal(t, "tok-1", tok)
	})

	t.Run("fallback to second", func(t *testing.T) {
		// Ensure first env is empty; second is set.
		t.Setenv("DPKMS_BUS_TOKEN", "")
		t.Setenv("BUS_TOKEN", "tok-fallback")

		auth, ok := bus.AuthFromEnv("DPKMS_BUS_TOKEN", "BUS_TOKEN")
		require.True(t, ok)
		require.NotNil(t, auth)
		tok, err := auth.Token()
		require.NoError(t, err)
		require.Equal(t, "tok-fallback", tok)
	})

	t.Run("all empty returns false", func(t *testing.T) {
		t.Setenv("DPKMS_BUS_TOKEN", "")
		t.Setenv("BUS_TOKEN", "")

		auth, ok := bus.AuthFromEnv("DPKMS_BUS_TOKEN", "BUS_TOKEN")
		require.False(t, ok)
		require.Nil(t, auth)
	})

	// pin: env contract used by the helper still works the same way.
	require.Equal(t, "", os.Getenv("DPKMS_BUS_TOKEN"))
}

// pin: StaticTokenAuth is the surface AuthFromEnv returns.
var _ = bus.StaticTokenAuth{Token_: "_"}
