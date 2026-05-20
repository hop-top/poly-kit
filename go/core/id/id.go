package id

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/google/uuid"
	typeid "go.jetify.com/typeid"
)

// Prefixer is the type-level token used by [Typed] to bind a Go type to a
// fixed TypeID prefix. Implementations have a single Prefix() string method
// and are typically zero-sized structs:
//
//	type taskPrefix struct{}
//	func (taskPrefix) Prefix() string { return "task" }
//
//	type TaskID = id.Typed[taskPrefix]
//
// Prefixer is a method-set constraint rather than a struct embedding because
// it composes cleanly with Go's generics: the type parameter alone tells the
// compiler which prefix to enforce, no embedded field required.
type Prefixer interface {
	Prefix() string
}

// Parsed is the round-trip result of [Parse]: the literal prefix and the
// underlying UUIDv7 backing the suffix.
//
// Parsed is intentionally a plain struct (not the wire form). The wire form
// is always the canonical string returned by [New], [MustNew], and
// [Typed.String].
type Parsed struct {
	Prefix string
	UUID   uuid.UUID
}

// New generates a fresh TypeID with the given prefix and returns its
// canonical string form ("prefix_<26-char-base32>"). The suffix is a freshly
// minted UUIDv7, so successive calls produce K-sortable IDs.
//
// An empty prefix is allowed and produces a bare 26-char string, matching
// the upstream spec.
func New(prefix string) (string, error) {
	tid, err := typeid.WithPrefix(prefix)
	if err != nil {
		return "", err
	}
	return tid.String(), nil
}

// MustNew is the panic-on-error variant of [New]. Intended for init-time
// callers and tests where an invalid prefix is a programmer error, not a
// runtime condition.
func MustNew(prefix string) string {
	s, err := New(prefix)
	if err != nil {
		panic(err)
	}
	return s
}

// Parse decodes a canonical TypeID string back into its prefix and the
// underlying [uuid.UUID]. Returns an error for invalid prefixes, invalid
// suffixes, or strings that don't match the canonical form.
func Parse(s string) (Parsed, error) {
	tid, err := typeid.FromString(s)
	if err != nil {
		return Parsed{}, err
	}
	// typeid v1's UUID() returns the hex string ("01940000-0000-7000-...").
	// Re-parse into google/uuid so callers don't have to depend on the
	// upstream's gofrs/uuid type.
	uid, err := uuid.Parse(tid.UUID())
	if err != nil {
		return Parsed{}, fmt.Errorf("id: decode uuid from typeid suffix: %w", err)
	}
	return Parsed{Prefix: tid.Prefix(), UUID: uid}, nil
}

// Typed is a generic newtype that binds a TypeID to a compile-time prefix
// supplied by T. Use it to define per-entity ID types:
//
//	type taskPrefix struct{}
//	func (taskPrefix) Prefix() string { return "task" }
//
//	type TaskID = id.Typed[taskPrefix]
//
//	tid := id.NewTyped[taskPrefix]()      // task_01j…
//	parsed, _ := id.ParseTyped[taskPrefix]("task_01j…")
//
// Typed marshals to JSON as the bare canonical string (not a {prefix,uuid}
// object). On unmarshal, the wire prefix must equal T's Prefix(); a
// mismatch is an error.
type Typed[T Prefixer] struct {
	// s is the canonical string. The zero value is a zero TypeID with the
	// prefix of T, which is what the upstream library models too.
	s string
}

// NewTyped generates a fresh Typed[T] with T's prefix. It is the typed
// counterpart of [New].
func NewTyped[T Prefixer]() (Typed[T], error) {
	var t T
	s, err := New(t.Prefix())
	if err != nil {
		return Typed[T]{}, err
	}
	return Typed[T]{s: s}, nil
}

// MustNewTyped is the panic-on-error variant of [NewTyped].
func MustNewTyped[T Prefixer]() Typed[T] {
	tid, err := NewTyped[T]()
	if err != nil {
		panic(err)
	}
	return tid
}

// ParseTyped parses a canonical TypeID string into a Typed[T], requiring the
// wire prefix to equal T's Prefix(). A mismatch returns an error so callers
// can't silently coerce e.g. an invoice_… into a TaskID.
func ParseTyped[T Prefixer](s string) (Typed[T], error) {
	parsed, err := Parse(s)
	if err != nil {
		return Typed[T]{}, err
	}
	var t T
	want := t.Prefix()
	if parsed.Prefix != want {
		return Typed[T]{}, fmt.Errorf("id: prefix mismatch: want %q, got %q", want, parsed.Prefix)
	}
	return Typed[T]{s: s}, nil
}

// String returns the canonical TypeID string.
func (t Typed[T]) String() string {
	if t.s != "" {
		return t.s
	}
	// Zero value: synthesize a zero TypeID with T's prefix so logging /
	// formatting still produces a meaningful string.
	var p T
	prefix := p.Prefix()
	if prefix == "" {
		return strings.Repeat("0", 26)
	}
	return prefix + "_" + strings.Repeat("0", 26)
}

// Prefix returns the literal prefix of T.
func (t Typed[T]) Prefix() string {
	var p T
	return p.Prefix()
}

// UUID returns the UUIDv7 backing the suffix.
func (t Typed[T]) UUID() (uuid.UUID, error) {
	parsed, err := Parse(t.String())
	if err != nil {
		return uuid.UUID{}, err
	}
	return parsed.UUID, nil
}

// IsZero reports whether t is the zero Typed[T] (no suffix assigned).
func (t Typed[T]) IsZero() bool {
	return t.s == ""
}

// MarshalJSON emits the canonical TypeID string as a JSON string. This is
// the kit wire form: tools deserialising bus events and REST payloads see
// "task_01j…", never a {prefix,uuid} object.
func (t Typed[T]) MarshalJSON() ([]byte, error) {
	return json.Marshal(t.String())
}

// UnmarshalJSON accepts a JSON string, parses it as a canonical TypeID, and
// enforces that the wire prefix equals T's Prefix(). Any mismatch or
// malformed suffix is an error.
func (t *Typed[T]) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return fmt.Errorf("id: typed json: %w", err)
	}
	parsed, err := ParseTyped[T](s)
	if err != nil {
		return err
	}
	*t = parsed
	return nil
}
