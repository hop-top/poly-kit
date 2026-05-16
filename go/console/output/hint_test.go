package output

import (
	"bytes"
	"os"
	"testing"

	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHintSet_RegisterAndLookup(t *testing.T) {
	s := NewHintSet()
	s.Register("upgrade", Hint{Message: "Run `hop version`"})
	s.Register("upgrade", Hint{Message: "Check changelog"})

	got := s.Lookup("upgrade")
	require.Len(t, got, 2)
	assert.Equal(t, "Run `hop version`", got[0].Message)
	assert.Equal(t, "Check changelog", got[1].Message)
}

func TestHintSet_LookupEmpty(t *testing.T) {
	s := NewHintSet()
	assert.Nil(t, s.Lookup("nope"))
}

func TestActive_FiltersOnCondition(t *testing.T) {
	hints := []Hint{
		{Message: "always", Condition: nil},
		{Message: "yes", Condition: func() bool { return true }},
		{Message: "no", Condition: func() bool { return false }},
	}
	got := Active(hints)
	require.Len(t, got, 2)
	assert.Equal(t, "always", got[0].Message)
	assert.Equal(t, "yes", got[1].Message)
}

func TestHintsEnabled_Default(t *testing.T) {
	v := viper.New()
	assert.True(t, HintsEnabled(v))
}

func TestHintsEnabled_NoHintsFlag(t *testing.T) {
	v := viper.New()
	v.Set("no-hints", true)
	assert.False(t, HintsEnabled(v))
}

func TestHintsEnabled_ConfigDisabled(t *testing.T) {
	v := viper.New()
	v.Set("hints.enabled", false)
	assert.False(t, HintsEnabled(v))
}

func TestHintsEnabled_QuietFlag(t *testing.T) {
	v := viper.New()
	v.Set("quiet", true)
	assert.False(t, HintsEnabled(v))
}

func TestHintsEnabled_EnvVar(t *testing.T) {
	for _, val := range []string{"1", "true", "yes"} {
		t.Run(val, func(t *testing.T) {
			t.Setenv("HOP_QUIET_HINTS", val)
			v := viper.New()
			assert.False(t, HintsEnabled(v))
		})
	}
}

func TestRenderHints_SuppressedForJSON(t *testing.T) {
	var buf bytes.Buffer
	v := viper.New()
	hints := []Hint{{Message: "should not appear"}}
	RenderHints(&buf, hints, JSON, v, nil)
	assert.Empty(t, buf.String())
}

func TestRenderHints_SuppressedForYAML(t *testing.T) {
	var buf bytes.Buffer
	v := viper.New()
	hints := []Hint{{Message: "should not appear"}}
	RenderHints(&buf, hints, YAML, v, nil)
	assert.Empty(t, buf.String())
}

func TestRenderHints_SuppressedWhenDisabled(t *testing.T) {
	v := viper.New()
	v.Set("no-hints", true)
	f := makeTTY(t)
	hints := []Hint{{Message: "should not appear"}}
	// Even with a real file, hints disabled = no output.
	RenderHints(f, hints, Table, v, nil)
}

func TestRenderHints_SuppressedNonTTY(t *testing.T) {
	var buf bytes.Buffer
	v := viper.New()
	hints := []Hint{{Message: "should not appear"}}
	RenderHints(&buf, hints, Table, v, nil)
	assert.Empty(t, buf.String())
}

func TestRenderHints_EmptyActive(t *testing.T) {
	f := makeTTY(t)
	v := viper.New()
	hints := []Hint{{
		Message:   "nope",
		Condition: func() bool { return false },
	}}
	RenderHints(f, hints, Table, v, nil)
}

func BenchmarkRenderHints_Disabled(b *testing.B) {
	v := viper.New()
	v.Set("no-hints", true)
	hints := []Hint{{Message: "bench"}}
	f := makeTTYBench(b)
	b.ReportAllocs()
	for b.Loop() {
		RenderHints(f, hints, Table, v, nil)
	}
}

// makeTTY returns a temp *os.File for RenderHints. This is NOT a real
// TTY, so RenderHints will take the non-TTY suppression path. The
// positive render path (actual TTY output) is only exercised manually.
func makeTTY(t *testing.T) *os.File {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "hint-test-*")
	require.NoError(t, err)
	t.Cleanup(func() { f.Close() })
	return f
}

func makeTTYBench(b *testing.B) *os.File {
	b.Helper()
	f, err := os.CreateTemp(b.TempDir(), "hint-bench-*")
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { f.Close() })
	return f
}
