package config_test

import (
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"

	"hop.top/kit/go/core/config"
)

func TestErrUnknownKey_Error(t *testing.T) {
	e := &config.ErrUnknownKey{Key: "foo.bar", ValidKeys: []string{"a", "b"}}
	assert.Equal(t, `unknown key "foo.bar"; valid keys: a, b`, e.Error())
}

func TestErrTypeMismatch_Error(t *testing.T) {
	e := &config.ErrTypeMismatch{Key: "port", Expected: "int", Got: "hello"}
	assert.Equal(t, `key "port" expects int, got "hello"`, e.Error())
}

func TestErrConstraintViolation_Error(t *testing.T) {
	e := &config.ErrConstraintViolation{
		Key:        "name",
		Constraint: "minLen(2)",
		Value:      "x",
	}
	assert.Equal(t, `key "name" violates constraint minLen(2): x`, e.Error())
}

func TestErrUnknownKey_As(t *testing.T) {
	orig := &config.ErrUnknownKey{Key: "k", ValidKeys: []string{"v"}}
	wrapped := fmt.Errorf("wrap: %w", orig)

	var target *config.ErrUnknownKey
	assert.True(t, errors.As(wrapped, &target))
	assert.Equal(t, "k", target.Key)
}

func TestErrTypeMismatch_As(t *testing.T) {
	orig := &config.ErrTypeMismatch{Key: "k", Expected: "int", Got: "str"}
	wrapped := fmt.Errorf("wrap: %w", orig)

	var target *config.ErrTypeMismatch
	assert.True(t, errors.As(wrapped, &target))
	assert.Equal(t, "int", target.Expected)
}
