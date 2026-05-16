//go:build parity

package cli_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/console/cli"
)

// TestParityTextFlagAppend: +value appends to existing text on a new line.
func TestParityTextFlagAppend(t *testing.T) {
	t.Parallel()
	var tf cli.TextFlag
	require.NoError(t, tf.Set("base"))
	require.NoError(t, tf.Set("+appended"))
	assert.Equal(t, "base\nappended", tf.Value())
}

// TestParityTextFlagAppendToEmpty: +value on empty text sets without leading newline.
func TestParityTextFlagAppendToEmpty(t *testing.T) {
	t.Parallel()
	var tf cli.TextFlag
	require.NoError(t, tf.Set("+first"))
	assert.Equal(t, "first", tf.Value())
}

// TestParityTextFlagAppendInline: +=value appends inline without newline.
func TestParityTextFlagAppendInline(t *testing.T) {
	t.Parallel()
	var tf cli.TextFlag
	require.NoError(t, tf.Set("hello"))
	require.NoError(t, tf.Set("+= world"))
	assert.Equal(t, "hello world", tf.Value())
}

// TestParityTextFlagAppendMultiple: repeated +value accumulates lines.
func TestParityTextFlagAppendMultiple(t *testing.T) {
	t.Parallel()
	var tf cli.TextFlag
	require.NoError(t, tf.Set("line1"))
	require.NoError(t, tf.Set("+line2"))
	require.NoError(t, tf.Set("+line3"))
	assert.Equal(t, "line1\nline2\nline3", tf.Value())
}

// TestParityTextFlagPrepend: ^value prepends to existing text on a new line.
func TestParityTextFlagPrepend(t *testing.T) {
	t.Parallel()
	var tf cli.TextFlag
	require.NoError(t, tf.Set("base"))
	require.NoError(t, tf.Set("^prepended"))
	assert.Equal(t, "prepended\nbase", tf.Value())
}

// TestParityTextFlagPrependToEmpty: ^value on empty text sets without trailing newline.
func TestParityTextFlagPrependToEmpty(t *testing.T) {
	t.Parallel()
	var tf cli.TextFlag
	require.NoError(t, tf.Set("^first"))
	assert.Equal(t, "first", tf.Value())
}

// TestParityTextFlagPrependInline: ^=value prepends inline without newline.
func TestParityTextFlagPrependInline(t *testing.T) {
	t.Parallel()
	var tf cli.TextFlag
	require.NoError(t, tf.Set("world"))
	require.NoError(t, tf.Set("^=hello "))
	assert.Equal(t, "hello world", tf.Value())
}

// TestParityTextFlagReplace: =value replaces entirely.
func TestParityTextFlagReplace(t *testing.T) {
	t.Parallel()
	var tf cli.TextFlag
	require.NoError(t, tf.Set("old"))
	require.NoError(t, tf.Set("=new"))
	assert.Equal(t, "new", tf.Value())
}

// TestParityTextFlagReplaceClear: = with no value clears the text.
func TestParityTextFlagReplaceClear(t *testing.T) {
	t.Parallel()
	var tf cli.TextFlag
	require.NoError(t, tf.Set("something"))
	require.NoError(t, tf.Set("="))
	assert.Equal(t, "", tf.Value())
}

// TestParityTextFlagReplaceDefault: bare value (no prefix) replaces entirely.
func TestParityTextFlagReplaceDefault(t *testing.T) {
	t.Parallel()
	var tf cli.TextFlag
	require.NoError(t, tf.Set("first"))
	require.NoError(t, tf.Set("second"))
	assert.Equal(t, "second", tf.Value())
}

// TestParityTextFlagMixedOps: append then prepend then replace — final state correct.
func TestParityTextFlagMixedOps(t *testing.T) {
	t.Parallel()
	var tf cli.TextFlag
	require.NoError(t, tf.Set("base"))
	require.NoError(t, tf.Set("+tail"))
	require.NoError(t, tf.Set("^head"))
	assert.Equal(t, "head\nbase\ntail", tf.Value(),
		"append + prepend must compose correctly")

	require.NoError(t, tf.Set("=replaced"))
	assert.Equal(t, "replaced", tf.Value(),
		"= must discard all prior content")
}
