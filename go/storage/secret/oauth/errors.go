package oauth

import (
	"errors"
	"fmt"
)

// Error taxonomy shared by every grant. Callers match with errors.Is; the
// producing layer wraps with %w and never puts credential values in the
// message.
var (
	// ErrCredentialRejected: the token endpoint refused the credential
	// (bad secret, revoked key, invalid assertion). Retrying cannot help.
	ErrCredentialRejected = errors.New("oauth: credential rejected")
	// ErrNeedsReconsent: the grant is no longer usable without the user
	// granting access again (expired or revoked refresh token).
	ErrNeedsReconsent = errors.New("oauth: needs re-consent")
	// ErrNotWritable: the grant needs write-back (rotation) and the
	// backend does not declare itself writable.
	ErrNotWritable = errors.New("oauth: store not writable")
	// ErrTransient: network or server failure; retrying may succeed.
	ErrTransient = errors.New("oauth: transient failure")
)

// ValidationError reports an invalid grant or cached token. It names the
// offending field and a fixed reason; it never carries the value.
type ValidationError struct {
	// Field is the JSON field name, or empty for a whole-document fault.
	Field string
	// Reason is fixed text.
	Reason string
}

func (e *ValidationError) Error() string {
	if e.Field == "" {
		return "oauth: invalid envelope: " + e.Reason
	}
	return fmt.Sprintf("oauth: invalid envelope: field %s: %s", e.Field, e.Reason)
}

// NotWritableError names the backend that refused a rotating grant. It
// matches ErrNotWritable.
type NotWritableError struct {
	Backend string
}

func (e *NotWritableError) Error() string {
	return "oauth: store not writable: backend " + e.Backend
}

func (e *NotWritableError) Is(target error) bool { return target == ErrNotWritable }

func invalid(field, reason string) error {
	return &ValidationError{Field: field, Reason: reason}
}
