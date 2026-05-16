package sideeffect

import (
	"context"
	"reflect"

	"hop.top/kit/go/runtime/bus"
	"hop.top/kit/go/runtime/domain"
)

// DryRunMechanism is the canonical bus.Qualifiers.Mechanism value
// the dry-run publisher tags onto event payloads. Subscribers that
// want to filter dry-run events check this value.
const DryRunMechanism = "dry_run"

// DryRunPublisher wraps a domain.EventPublisher and, when the
// publish-time ctx is dry-run via IsDryRun, augments payloads
// embedding bus.Qualifiers with Mechanism: "dry_run" before
// delegating to the wrapped publisher.
//
// Payloads that do not embed bus.Qualifiers are delegated unchanged.
// Mechanism tagging is best-effort: payload authors who want the
// tag applied must embed bus.Qualifiers (anonymous or named) in the
// struct passed to Publish, and pass the payload by pointer so the
// wrapper can mutate in place. See ADR-0017 for the embed convention
// and ADR-0019 for the dry-run rationale.
//
// The wrapper is the chosen integration point per ADR-0019: less
// invasive than mutating Publish in the bus core, fully testable in
// isolation, and keeps the bus core unaware of cli runtime mode.
//
// When ctx is NOT dry-run, the wrapper is a passthrough — no
// reflection cost, no payload mutation.
type DryRunPublisher struct {
	// Inner is the wrapped publisher receiving every Publish call.
	// nil Inner returns ErrNilInner from Publish.
	Inner domain.EventPublisher
}

// NewDryRunPublisher wraps inner so dry-run events get the
// Mechanism: "dry_run" tag automatically. nil inner is accepted
// but Publish returns ErrNilInner.
func NewDryRunPublisher(inner domain.EventPublisher) *DryRunPublisher {
	return &DryRunPublisher{Inner: inner}
}

// ErrNilInner is returned by DryRunPublisher.Publish when Inner is
// nil.
var ErrNilInner = &dryRunPubErr{"sideeffect: DryRunPublisher with nil Inner"}

type dryRunPubErr struct{ msg string }

func (e *dryRunPubErr) Error() string { return e.msg }

// Publish augments payload (when applicable) and delegates to Inner.
func (p *DryRunPublisher) Publish(ctx context.Context, topic, source string, payload any) error {
	if p.Inner == nil {
		return ErrNilInner
	}
	if !IsDryRun(ctx) {
		return p.Inner.Publish(ctx, topic, source, payload)
	}
	return p.Inner.Publish(ctx, topic, source, augmentMechanism(payload, DryRunMechanism))
}

// augmentMechanism sets Mechanism on the first bus.Qualifiers field
// found inside payload (anonymous or named embed). When the
// existing Mechanism is non-empty it is preserved (the wrapper is
// not authoritative). When the payload value is non-addressable
// (struct passed by value, not pointer) the helper returns the
// payload unchanged — we deliberately do NOT clone-and-augment so
// the subscriber pipeline sees the caller's struct identity.
// Adopters that want guaranteed tagging pass a *T pointer.
func augmentMechanism(payload any, mechanism string) any {
	if payload == nil {
		return payload
	}
	v := reflect.ValueOf(payload)
	if v.Kind() == reflect.Pointer {
		if v.IsNil() {
			return payload
		}
		v = v.Elem()
	}
	if v.Kind() != reflect.Struct {
		return payload
	}
	qualifiersType := reflect.TypeOf(bus.Qualifiers{})
	t := v.Type()
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if !f.IsExported() {
			continue
		}
		if f.Type != qualifiersType {
			continue
		}
		fv := v.Field(i)
		if !fv.CanSet() {
			return payload
		}
		q, _ := fv.Interface().(bus.Qualifiers)
		if q.Mechanism == "" {
			q.Mechanism = mechanism
			fv.Set(reflect.ValueOf(q))
		}
		return payload
	}
	return payload
}
