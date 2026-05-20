package telemetry

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestParseMode(t *testing.T) {
	cases := map[string]struct {
		want Mode
		ok   bool
	}{
		"":        {ModeOff, true},
		"off":     {ModeOff, true},
		"OFF":     {ModeOff, true},
		"  Off ":  {ModeOff, true},
		"anon":    {ModeAnon, true},
		"ANON":    {ModeAnon, true},
		"  Anon ": {ModeAnon, true},
		"full":    {ModeFull, true},
		"FULL":    {ModeFull, true},
		"garbage": {ModeOff, false},
	}
	for in, exp := range cases {
		got, ok := ParseMode(in)
		assert.Equal(t, exp.want, got, in)
		assert.Equal(t, exp.ok, ok, in)
	}
}

func TestString(t *testing.T) {
	// Round-trip: ParseMode(m.String()) == m for each defined Mode.
	for _, m := range []Mode{ModeOff, ModeAnon, ModeFull} {
		got, ok := ParseMode(m.String())
		assert.True(t, ok, m.String())
		assert.Equal(t, m, got, m.String())
	}
	assert.Equal(t, "unknown", Mode(99).String())
}

func TestCurrentMode_DefaultOff(t *testing.T) {
	resetForTest()
	t.Setenv("KIT_TELEMETRY_MODE", "")
	assert.Equal(t, ModeOff, CurrentMode())
}

func TestCurrentMode_EnvKit(t *testing.T) {
	resetForTest()
	t.Setenv("KIT_TELEMETRY_MODE", "anon")
	assert.Equal(t, ModeAnon, CurrentMode())
}

func TestCurrentMode_EnvKitFull(t *testing.T) {
	resetForTest()
	t.Setenv("KIT_TELEMETRY_MODE", "full")
	assert.Equal(t, ModeFull, CurrentMode())
}

func TestCurrentMode_EnvAppWins(t *testing.T) {
	resetForTest()
	SetAppPrefix("spaced")
	t.Setenv("SPACED_TELEMETRY_MODE", "full")
	t.Setenv("KIT_TELEMETRY_MODE", "anon")
	assert.Equal(t, ModeFull, CurrentMode())
}

func TestCurrentMode_EnvAppFallsBackToKit(t *testing.T) {
	resetForTest()
	SetAppPrefix("spaced")
	t.Setenv("SPACED_TELEMETRY_MODE", "")
	t.Setenv("KIT_TELEMETRY_MODE", "full")
	assert.Equal(t, ModeFull, CurrentMode())
}

func TestCurrentMode_InvalidEnvFallsThrough(t *testing.T) {
	resetForTest()
	SetAppPrefix("spaced")
	t.Setenv("SPACED_TELEMETRY_MODE", "garbage")
	t.Setenv("KIT_TELEMETRY_MODE", "anon")
	// App-prefix env is invalid; fall through to KIT_TELEMETRY_MODE.
	assert.Equal(t, ModeAnon, CurrentMode())
}

func TestSetMode_OverridesEnv(t *testing.T) {
	resetForTest()
	t.Setenv("KIT_TELEMETRY_MODE", "anon")
	SetMode(ModeFull)
	// SetMode locks in; env is permanently ignored thereafter.
	assert.Equal(t, ModeFull, CurrentMode())
	// And a re-read still respects the override.
	assert.Equal(t, ModeFull, CurrentMode())
}

func TestSetMode_BeforeFirstRead(t *testing.T) {
	resetForTest()
	SetMode(ModeAnon)
	t.Setenv("KIT_TELEMETRY_MODE", "full")
	// SetMode ran before any CurrentMode call; env must not apply.
	assert.Equal(t, ModeAnon, CurrentMode())
}

func TestWithMode(t *testing.T) {
	resetForTest()
	SetMode(ModeOff)
	ctx := WithMode(context.Background(), ModeFull)
	assert.Equal(t, ModeFull, CurrentModeFromContext(ctx))
}

func TestWithMode_NilCtx(t *testing.T) {
	resetForTest()
	//nolint:staticcheck // intentionally exercising nil-ctx safety
	ctx := WithMode(nil, ModeAnon)
	assert.Equal(t, ModeAnon, CurrentModeFromContext(ctx))
}

func TestCurrentModeFromContext_FallsBack(t *testing.T) {
	resetForTest()
	SetMode(ModeAnon)
	assert.Equal(t, ModeAnon, CurrentModeFromContext(context.Background()))
}

func TestCurrentModeFromContext_NilCtx(t *testing.T) {
	resetForTest()
	SetMode(ModeFull)
	//nolint:staticcheck // intentionally exercising nil-ctx safety
	assert.Equal(t, ModeFull, CurrentModeFromContext(nil))
}

func TestSetAppPrefix_TrimsAndStores(t *testing.T) {
	resetForTest()
	SetAppPrefix("  spaced  ")
	assert.Equal(t, "spaced", CurrentAppPrefix())
	SetAppPrefix("")
	assert.Equal(t, "", CurrentAppPrefix())
}

func TestConcurrent_FirstCallRace(t *testing.T) {
	resetForTest()
	t.Setenv("KIT_TELEMETRY_MODE", "anon")

	const N = 100
	var wg sync.WaitGroup
	results := make([]Mode, N)
	wg.Add(N)
	for i := 0; i < N; i++ {
		go func(idx int) {
			defer wg.Done()
			results[idx] = CurrentMode()
		}(i)
	}
	wg.Wait()

	// All goroutines must observe the same Mode (no torn read, no
	// stale ModeOff sneaking past the CompareAndSwap fence).
	for i, m := range results {
		assert.Equal(t, ModeAnon, m, "goroutine %d saw %s", i, m)
	}
}
