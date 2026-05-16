package pkl

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/core/config"
)

func testSchema() *Schema {
	return &Schema{
		ModuleName: "TestConfig",
		Fields: []FieldDef{
			{Path: "name", Type: TypeString, Required: true,
				Constraints: []Constraint{
					{Kind: ConstraintMinLen, Value: 2},
				}},
			{Path: "lang", Type: TypeStringEnum,
				Enum: []string{"go", "py", "ts"}},
			{Path: "port", Type: TypeInt,
				Constraints: []Constraint{
					{Kind: ConstraintBetween, Value: [2]int{1024, 65535}},
				}},
			{Path: "debug", Type: TypeBool},
			{Path: "timeout", Type: TypeDuration},
			{Path: "ratio", Type: TypeFloat},
		},
	}
}

func TestValidate_StringValid(t *testing.T) {
	err := ValidateValue(testSchema(), "name", "myapp")
	require.NoError(t, err)
}

func TestValidate_StringTooShort(t *testing.T) {
	err := ValidateValue(testSchema(), "name", "x")
	var cv *config.ErrConstraintViolation
	require.True(t, errors.As(err, &cv))
	assert.Equal(t, "name", cv.Key)
	assert.Contains(t, cv.Constraint, "minLen")
}

func TestValidate_EnumValid(t *testing.T) {
	err := ValidateValue(testSchema(), "lang", "go")
	require.NoError(t, err)
}

func TestValidate_EnumInvalid(t *testing.T) {
	err := ValidateValue(testSchema(), "lang", "rust")
	var cv *config.ErrConstraintViolation
	require.True(t, errors.As(err, &cv))
	assert.Equal(t, "lang", cv.Key)
	assert.Contains(t, cv.Constraint, "enum")
}

func TestValidate_IntValid(t *testing.T) {
	err := ValidateValue(testSchema(), "port", "8080")
	require.NoError(t, err)
}

func TestValidate_IntInvalid(t *testing.T) {
	err := ValidateValue(testSchema(), "port", "abc")
	var tm *config.ErrTypeMismatch
	require.True(t, errors.As(err, &tm))
	assert.Equal(t, "port", tm.Key)
	assert.Equal(t, "integer", tm.Expected)
}

func TestValidate_IntOutOfRange(t *testing.T) {
	err := ValidateValue(testSchema(), "port", "80")
	var cv *config.ErrConstraintViolation
	require.True(t, errors.As(err, &cv))
	assert.Equal(t, "port", cv.Key)
	assert.Contains(t, cv.Constraint, "between")
}

func TestValidate_BoolValid(t *testing.T) {
	err := ValidateValue(testSchema(), "debug", "true")
	require.NoError(t, err)
}

func TestValidate_BoolInvalid(t *testing.T) {
	err := ValidateValue(testSchema(), "debug", "maybe")
	var tm *config.ErrTypeMismatch
	require.True(t, errors.As(err, &tm))
	assert.Equal(t, "debug", tm.Key)
	assert.Equal(t, "boolean", tm.Expected)
}

func TestValidate_UnknownKey(t *testing.T) {
	err := ValidateValue(testSchema(), "unknown", "val")
	var uk *config.ErrUnknownKey
	require.True(t, errors.As(err, &uk))
	assert.Equal(t, "unknown", uk.Key)
	assert.NotEmpty(t, uk.ValidKeys)
}

func TestValidate_FloatValid(t *testing.T) {
	err := ValidateValue(testSchema(), "ratio", "0.5")
	require.NoError(t, err)
}

func TestValidate_FloatInvalid(t *testing.T) {
	err := ValidateValue(testSchema(), "ratio", "abc")
	var tm *config.ErrTypeMismatch
	require.True(t, errors.As(err, &tm))
	assert.Equal(t, "ratio", tm.Key)
	assert.Equal(t, "float", tm.Expected)
}

func TestValidate_NilSchema(t *testing.T) {
	err := ValidateValue(nil, "name", "val")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "nil schema")
}
