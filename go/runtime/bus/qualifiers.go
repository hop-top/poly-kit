package bus

import "reflect"

// Qualifiers is the payload-side struct that carries the four
// semantic axes the bus topic does not encode:
//
//   - Reason       — why the event happened (cause)
//   - Mechanism    — how it happened (transport / pathway)
//   - Property     — which attribute changed or applied
//   - Circumstance — during what context / conditions
//
// All four fields are optional. An empty Qualifiers JSON-marshals
// to "{}" because every field carries the omitempty tag.
//
// Embed Qualifiers in any payload struct to opt in:
//
//	// Anonymous embed (preferred):
//	type SnapshotReloadFailed struct {
//	    bus.Qualifiers
//	    SnapshotID string `json:"snapshot_id"`
//	}
//
//	// Named embed (also supported):
//	type SnapshotReloadFailed struct {
//	    Q          bus.Qualifiers `json:"qualifiers"`
//	    SnapshotID string         `json:"snapshot_id"`
//	}
//
// The bus Publish API does not change. Subscribers that want to
// inspect qualifiers generically use [QualifiersFrom] on the
// payload.
//
// See ADR-0017 (docs/adr/0017-bus-topic-naming-and-qualifiers.md)
// for the rationale behind keeping these axes out of the topic
// string (cardinality, subscriber pattern stability, metric
// series cap).
type Qualifiers struct {
	Reason       string `json:"reason,omitempty"`
	Mechanism    string `json:"mechanism,omitempty"`
	Property     string `json:"property,omitempty"`
	Circumstance string `json:"circumstance,omitempty"`
}

// IsZero reports whether q has no qualifier fields set. Useful for
// callers that want to skip emitting an empty Qualifiers block in
// custom serialisers.
func (q Qualifiers) IsZero() bool {
	return q.Reason == "" && q.Mechanism == "" && q.Property == "" && q.Circumstance == ""
}

// QualifiersFrom extracts a [Qualifiers] from an arbitrary payload
// value via reflection. It looks for an embedded Qualifiers field
// (anonymous OR named, exported only) at the top level of the
// payload struct. Returns the qualifiers and true on success;
// returns the zero Qualifiers and false when no embed is found or
// when payload is not a struct (or pointer to struct).
//
// QualifiersFrom does NOT recurse: only the immediate struct
// fields of payload are inspected. This keeps the helper cheap
// and predictable — adopters that nest payload types should embed
// Qualifiers at the top level of the published struct.
//
// Pointer payloads are dereferenced once. Nil pointers and non-
// struct kinds (map, slice, interface, primitive) return ok=false.
func QualifiersFrom(payload any) (Qualifiers, bool) {
	if payload == nil {
		return Qualifiers{}, false
	}
	v := reflect.ValueOf(payload)
	if v.Kind() == reflect.Pointer {
		if v.IsNil() {
			return Qualifiers{}, false
		}
		v = v.Elem()
	}
	if v.Kind() != reflect.Struct {
		return Qualifiers{}, false
	}

	qualifiersType := reflect.TypeOf(Qualifiers{})
	t := v.Type()
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if !f.IsExported() {
			continue
		}
		// Match either an anonymous embed of bus.Qualifiers or a
		// named field whose type is bus.Qualifiers.
		if f.Type == qualifiersType {
			fv := v.Field(i)
			if q, ok := fv.Interface().(Qualifiers); ok {
				return q, true
			}
		}
	}
	return Qualifiers{}, false
}
