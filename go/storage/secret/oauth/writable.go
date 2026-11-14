package oauth

import "hop.top/kit/go/storage/secret"

// Writability is the optional method a backend implements to declare that
// Set overwrites in place and Get reads the latest write, so a rotated
// refresh token survives. A backend without the method is not writable.
// Probing with a test write is deliberately not offered: it has side effects.
type Writability interface {
	Writable() bool
}

// IsWritable reports whether store declares write-back support.
func IsWritable(store secret.Store) bool {
	w, ok := store.(Writability)
	return ok && w.Writable()
}

// RequireWritable returns a *NotWritableError naming backend (a short label
// chosen by the caller) unless store is writable.
func RequireWritable(store secret.Store, backend string) error {
	if IsWritable(store) {
		return nil
	}
	return &NotWritableError{Backend: backend}
}
