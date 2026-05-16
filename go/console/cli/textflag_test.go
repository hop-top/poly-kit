package cli

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTextFlag_ReplaceDefault(t *testing.T) {
	var tf TextFlag
	require.NoError(t, tf.Set("hello"))
	assert.Equal(t, "hello", tf.Value())
	require.NoError(t, tf.Set("world"))
	assert.Equal(t, "world", tf.Value())
}

func TestTextFlag_ReplaceExplicit(t *testing.T) {
	tf := TextFlag{text: "old"}
	require.NoError(t, tf.Set("=new"))
	assert.Equal(t, "new", tf.Value())
}

func TestTextFlag_AppendNewLine(t *testing.T) {
	tf := TextFlag{text: "first"}
	require.NoError(t, tf.Set("+second"))
	assert.Equal(t, "first\nsecond", tf.Value())
}

func TestTextFlag_AppendNewLineMultiple(t *testing.T) {
	tf := TextFlag{text: "line1"}
	require.NoError(t, tf.Set("+line2"))
	require.NoError(t, tf.Set("+line3"))
	assert.Equal(t, "line1\nline2\nline3", tf.Value())
}

func TestTextFlag_AppendInline(t *testing.T) {
	tf := TextFlag{text: "hello"}
	require.NoError(t, tf.Set("+= world"))
	assert.Equal(t, "hello world", tf.Value())
}

func TestTextFlag_PrependNewLine(t *testing.T) {
	tf := TextFlag{text: "second"}
	require.NoError(t, tf.Set("^first"))
	assert.Equal(t, "first\nsecond", tf.Value())
}

func TestTextFlag_PrependInline(t *testing.T) {
	tf := TextFlag{text: "world"}
	require.NoError(t, tf.Set("^=hello "))
	assert.Equal(t, "hello world", tf.Value())
}

func TestTextFlag_Clear(t *testing.T) {
	tf := TextFlag{text: "something"}
	require.NoError(t, tf.Set("="))
	assert.Equal(t, "", tf.Value())
}

func TestTextFlag_AppendToEmpty(t *testing.T) {
	var tf TextFlag
	require.NoError(t, tf.Set("+line"))
	assert.Equal(t, "line", tf.Value())
}

func TestTextFlag_PrependToEmpty(t *testing.T) {
	var tf TextFlag
	require.NoError(t, tf.Set("^line"))
	assert.Equal(t, "line", tf.Value())
}

func TestTextFlag_String(t *testing.T) {
	tf := TextFlag{text: "hello"}
	assert.Equal(t, "hello", tf.String())
}

func TestTextFlag_Type(t *testing.T) {
	var tf TextFlag
	assert.Equal(t, "text", tf.Type())
}

func TestTextFlag_EscapeLiteralPlus(t *testing.T) {
	var tf TextFlag
	require.NoError(t, tf.Set("=+ppl"))
	assert.Equal(t, "+ppl", tf.Value())
}

func TestTextFlag_EscapeLiteralCaret(t *testing.T) {
	var tf TextFlag
	require.NoError(t, tf.Set("=^weird"))
	assert.Equal(t, "^weird", tf.Value())
}

func TestTextFlag_EscapeLiteralEquals(t *testing.T) {
	var tf TextFlag
	require.NoError(t, tf.Set("==equals"))
	assert.Equal(t, "=equals", tf.Value())
}

func TestTextFlag_MixedOperations(t *testing.T) {
	var tf TextFlag
	require.NoError(t, tf.Set("base"))
	require.NoError(t, tf.Set("+appended"))
	require.NoError(t, tf.Set("^prepended"))
	assert.Equal(t, "prepended\nbase\nappended", tf.Value())
}

func TestTextFlag_SetEmptyClears(t *testing.T) {
	tf := TextFlag{text: "something"}
	require.NoError(t, tf.Set(""))
	assert.Equal(t, "", tf.Value())
}
