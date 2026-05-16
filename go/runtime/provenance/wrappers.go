package provenance

import (
	"encoding/json"
	"fmt"
)

// Cached wraps a value that was served from a cache layer rather than
// freshly fetched. Pair with a Provenance record via Tracker.Cache
// (preferred) or NewCached (literal construction).
//
// The zero value Cached[T]{} is INVALID: no value, no provenance. The
// vet-style lint flags zero-value declarations; strict-mode runtime
// rejects emission of zero-value wrappers.
type Cached[T any] struct {
	value T
	prov  Provenance
	set   bool
}

// Synthesized wraps a value the tool derived, inferred, or defaulted
// — i.e., a value with no single upstream source of truth. Pair with
// a Provenance record via Tracker.Synthesize.
//
// The zero value Synthesized[T]{} is INVALID, same as Cached[T].
type Synthesized[T any] struct {
	value T
	prov  Provenance
	set   bool
}

// NewCached wraps v as a cache-served value. prov.Source is set to
// SourceCached if unset; an explicit SourceDefaulted is rejected
// (use NewSynthesized for defaulted values). prov.SchemaVersion is
// filled in when empty.
//
// NewCached panics if prov is structurally invalid for the Cached
// path. Adopters who need non-panicking construction can call
// Tracker.Cache directly, which returns an error.
func NewCached[T any](v T, prov Provenance) Cached[T] {
	prov = prov.fillDefaults()
	if prov.Source == "" {
		prov.Source = SourceCached
	}
	if prov.Source == SourceDefaulted {
		panic("provenance.NewCached: SourceDefaulted rejected; use NewSynthesized for defaulted values")
	}
	if err := prov.Validate(); err != nil {
		panic(fmt.Sprintf("provenance.NewCached: %v", err))
	}
	return Cached[T]{value: v, prov: prov, set: true}
}

// NewSynthesized wraps v as a tool-derived value. prov.Source is set
// to SourceInferred if unset; SourceDefaulted is allowed. SchemaVersion
// is filled in when empty.
//
// Panics on validation failure, like NewCached.
func NewSynthesized[T any](v T, prov Provenance) Synthesized[T] {
	prov = prov.fillDefaults()
	if prov.Source == "" {
		prov.Source = SourceInferred
	}
	if err := prov.Validate(); err != nil {
		panic(fmt.Sprintf("provenance.NewSynthesized: %v", err))
	}
	return Synthesized[T]{value: v, prov: prov, set: true}
}

// Value returns the wrapped value. Reading from an unset wrapper
// returns the zero value of T; check IsSet() to distinguish.
func (c Cached[T]) Value() T { return c.value }

// Provenance returns the metadata paired with this wrapper. The zero
// Provenance{} is returned for an unset Cached.
func (c Cached[T]) Provenance() Provenance { return c.prov }

// IsSet reports whether the wrapper was deliberately populated (via a
// constructor). Zero values report false.
func (c Cached[T]) IsSet() bool { return c.set }

// Value, Provenance, IsSet — mirror Cached[T].
func (s Synthesized[T]) Value() T               { return s.value }
func (s Synthesized[T]) Provenance() Provenance { return s.prov }
func (s Synthesized[T]) IsSet() bool            { return s.set }

// MarshalJSON emits the inner value only. The Provenance is captured
// at emit-time by Render(); the wrapper does not emit its own metadata
// inline. This keeps the schema of `data` stable.
func (c Cached[T]) MarshalJSON() ([]byte, error) {
	return json.Marshal(c.value)
}

// MarshalJSON mirrors Cached[T].
func (s Synthesized[T]) MarshalJSON() ([]byte, error) {
	return json.Marshal(s.value)
}

// UnmarshalJSON populates value from data and leaves prov zero, set
// false. Consumers of a CLI's output reconstruct the wrapper-without-
// provenance shape; the envelope `provenance:` block is the canonical
// source of metadata.
func (c *Cached[T]) UnmarshalJSON(data []byte) error {
	var v T
	if err := json.Unmarshal(data, &v); err != nil {
		return err
	}
	c.value = v
	c.set = false
	c.prov = Provenance{}
	return nil
}

// UnmarshalJSON mirrors Cached[T].
func (s *Synthesized[T]) UnmarshalJSON(data []byte) error {
	var v T
	if err := json.Unmarshal(data, &v); err != nil {
		return err
	}
	s.value = v
	s.set = false
	s.prov = Provenance{}
	return nil
}

// MarshalYAML mirrors MarshalJSON: emit only the inner value.
func (c Cached[T]) MarshalYAML() (any, error)      { return c.value, nil }
func (s Synthesized[T]) MarshalYAML() (any, error) { return s.value, nil }
