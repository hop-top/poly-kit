package config

import (
	"fmt"
	"strings"
)

// ErrUnknownKey is returned when a key is not defined in the schema.
type ErrUnknownKey struct {
	Key       string
	ValidKeys []string
}

func (e *ErrUnknownKey) Error() string {
	return fmt.Sprintf("unknown key %q; valid keys: %s",
		e.Key, strings.Join(e.ValidKeys, ", "))
}

// ErrTypeMismatch is returned when a value doesn't match the schema type.
type ErrTypeMismatch struct {
	Key      string
	Expected string
	Got      string
}

func (e *ErrTypeMismatch) Error() string {
	return fmt.Sprintf("key %q expects %s, got %q",
		e.Key, e.Expected, e.Got)
}

// ErrConstraintViolation is returned when a value violates a schema
// constraint.
type ErrConstraintViolation struct {
	Key        string
	Constraint string
	Value      string
}

func (e *ErrConstraintViolation) Error() string {
	return fmt.Sprintf("key %q violates constraint %s: %s",
		e.Key, e.Constraint, e.Value)
}
