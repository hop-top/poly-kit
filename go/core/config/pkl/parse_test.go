package pkl

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseUnionType_Simple(t *testing.T) {
	vals, ok := parseUnionType(`"go"|"py"`)
	require.True(t, ok)
	assert.Equal(t, []string{"go", "py"}, vals)
}

func TestParseUnionType_NotUnion(t *testing.T) {
	vals, ok := parseUnionType("String")
	assert.False(t, ok)
	assert.Nil(t, vals)
}

func TestParseConstraints_MinLen(t *testing.T) {
	cs := parseConstraints("length >= 2")
	require.Len(t, cs, 1)
	assert.Equal(t, ConstraintMinLen, cs[0].Kind)
	assert.Equal(t, 2, cs[0].Value)
}

func TestParseConstraints_Pattern(t *testing.T) {
	cs := parseConstraints(`matches(Regex("^[a-z]+$"))`)
	require.Len(t, cs, 1)
	assert.Equal(t, ConstraintPattern, cs[0].Kind)
	assert.Contains(t, cs[0].Value, "^[a-z]+$")
}

func TestParseConstraints_Between(t *testing.T) {
	cs := parseConstraints("isBetween(1, 100)")
	require.Len(t, cs, 1)
	assert.Equal(t, ConstraintBetween, cs[0].Kind)
	assert.Equal(t, [2]int{1, 100}, cs[0].Value)
}

func TestParseConstraints_LengthBetween(t *testing.T) {
	cs := parseConstraints("length.isBetween(3, 50)")
	require.Len(t, cs, 2)
	assert.Equal(t, ConstraintMinLen, cs[0].Kind)
	assert.Equal(t, 3, cs[0].Value)
	assert.Equal(t, ConstraintMaxLen, cs[1].Kind)
	assert.Equal(t, 50, cs[1].Value)
}

func TestParseAnnotations_GroupAndWhen(t *testing.T) {
	comments := []string{
		`/// @wizard.group "database"`,
		`/// @wizard.when lang == "go"`,
	}
	group, when, _ := parseAnnotations(comments)
	assert.Equal(t, "database", group)
	require.NotNil(t, when)
	assert.Contains(t, when.Expression, "lang")
}

func TestParseDefault_StringLiteral(t *testing.T) {
	val, computed := parseDefault(`"hello"`, TypeString)
	assert.Equal(t, "hello", val)
	assert.False(t, computed)
}

func TestParseDefault_IntLiteral(t *testing.T) {
	val, computed := parseDefault("42", TypeInt)
	assert.Equal(t, 42, val)
	assert.False(t, computed)
}

func TestParseDefault_Computed(t *testing.T) {
	val, computed := parseDefault(`"#{name}_db"`, TypeString)
	assert.Equal(t, "#{name}_db", val)
	assert.True(t, computed)
}

func TestResolveBaseType_Known(t *testing.T) {
	assert.Equal(t, TypeString, resolveBaseType("String"))
	assert.Equal(t, TypeInt, resolveBaseType("Int"))
	assert.Equal(t, TypeFloat, resolveBaseType("Float"))
	assert.Equal(t, TypeBool, resolveBaseType("Boolean"))
	assert.Equal(t, TypeDuration, resolveBaseType("Duration"))
}

func TestResolveBaseType_Unknown(t *testing.T) {
	assert.Equal(t, TypeString, resolveBaseType("FooBar"))
}
