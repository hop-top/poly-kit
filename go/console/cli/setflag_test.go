package cli

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSetFlag_AppendDefault(t *testing.T) {
	var sf SetFlag
	require.NoError(t, sf.Set("feat"))
	require.NoError(t, sf.Set("docs"))
	assert.Equal(t, []string{"feat", "docs"}, sf.Values())
}

func TestSetFlag_AppendExplicit(t *testing.T) {
	var sf SetFlag
	require.NoError(t, sf.Set("+feat"))
	require.NoError(t, sf.Set("+docs"))
	assert.Equal(t, []string{"feat", "docs"}, sf.Values())
}

func TestSetFlag_Remove(t *testing.T) {
	sf := SetFlag{items: []string{"feat", "bug", "docs"}}
	require.NoError(t, sf.Set("-bug"))
	assert.Equal(t, []string{"feat", "docs"}, sf.Values())
}

func TestSetFlag_RemoveNonexistent(t *testing.T) {
	sf := SetFlag{items: []string{"feat"}}
	require.NoError(t, sf.Set("-nope"))
	assert.Equal(t, []string{"feat"}, sf.Values())
}

func TestSetFlag_ReplaceAll(t *testing.T) {
	sf := SetFlag{items: []string{"old1", "old2"}}
	require.NoError(t, sf.Set("=new1,new2"))
	assert.Equal(t, []string{"new1", "new2"}, sf.Values())
}

func TestSetFlag_ClearAll(t *testing.T) {
	sf := SetFlag{items: []string{"a", "b", "c"}}
	require.NoError(t, sf.Set("="))
	assert.Empty(t, sf.Values())
}

func TestSetFlag_String(t *testing.T) {
	sf := SetFlag{items: []string{"a", "b"}}
	assert.Equal(t, "a,b", sf.String())
}

func TestSetFlag_StringEmpty(t *testing.T) {
	var sf SetFlag
	assert.Equal(t, "", sf.String())
}

func TestSetFlag_Type(t *testing.T) {
	var sf SetFlag
	assert.Equal(t, "set", sf.Type())
}

func TestSetFlag_NoDuplicates(t *testing.T) {
	var sf SetFlag
	require.NoError(t, sf.Set("feat"))
	require.NoError(t, sf.Set("feat"))
	assert.Equal(t, []string{"feat"}, sf.Values())
}

func TestSetFlag_MixedOperations(t *testing.T) {
	var sf SetFlag
	require.NoError(t, sf.Set("a"))
	require.NoError(t, sf.Set("b"))
	require.NoError(t, sf.Set("c"))
	require.NoError(t, sf.Set("-b"))
	require.NoError(t, sf.Set("+d"))
	assert.Equal(t, []string{"a", "c", "d"}, sf.Values())
}

func TestSetFlag_ReplaceAfterAppend(t *testing.T) {
	var sf SetFlag
	require.NoError(t, sf.Set("a"))
	require.NoError(t, sf.Set("b"))
	require.NoError(t, sf.Set("=x"))
	assert.Equal(t, []string{"x"}, sf.Values())
}

func TestSetFlag_EscapeLiteralPlus(t *testing.T) {
	var sf SetFlag
	require.NoError(t, sf.Set("=+ppl"))
	assert.Equal(t, []string{"+ppl"}, sf.Values())
}

func TestSetFlag_EscapeLiteralMinus(t *testing.T) {
	var sf SetFlag
	require.NoError(t, sf.Set("=-negative"))
	assert.Equal(t, []string{"-negative"}, sf.Values())
}

func TestSetFlag_EscapeLiteralEquals(t *testing.T) {
	var sf SetFlag
	require.NoError(t, sf.Set("==equals"))
	assert.Equal(t, []string{"=equals"}, sf.Values())
}

func TestSetFlag_CommaInAppend(t *testing.T) {
	var sf SetFlag
	require.NoError(t, sf.Set("a,b"))
	assert.Equal(t, []string{"a", "b"}, sf.Values())
}
