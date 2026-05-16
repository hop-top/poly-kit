// Package domain wraps a Repository with optional middleware
// (validation, auditing, event publishing) plus the canonical CRUD
// vocabulary that flows through pre-events.
//
// # Op vocabulary
//
// Service.{Create,Update,Delete} auto-inject the canonical Op into the
// context they hand to Validator and to pre-event subscribers, so a
// validator that needs to dispatch by verb can read it without the
// caller threading it manually:
//
//	func (v *myValidator) Validate(ctx context.Context, e Entity) error {
//	    if domain.OpFromCtx(ctx) != domain.OpCreate {
//	        return nil // create-only invariants
//	    }
//	    // ...
//	}
//
// Validators that don't read Op behave as before — empty Op is the
// documented "no canonical verb in flight" sentinel.
//
// Adopters that need finer-grained verbs (archive vs unarchive both
// being Update at the kit level) layer a free-form sub-verb name with
// WithSubOp/SubOpFromCtx. Kit ships no SubOp constants; adopters own
// their sub-verb vocabulary.
//
// Vocabulary alignment: the Op value carried in ctx is the same string
// populated on PreEntityPayload.Op, so a CEL policy on
// kit.runtime.entity.pre_validated reading payload.op == "delete" sees
// the identical string the validator reads via OpFromCtx.
package domain

import "context"

// Validator validates an entity before persistence operations.
//
// Op + SubOp are available on ctx during validation. Use OpFromCtx to
// dispatch by canonical CRUD verb (OpCreate/OpUpdate/OpDelete) and
// SubOpFromCtx to read an adopter-defined sub-verb name layered on top
// of OpUpdate (e.g. "archive", "unarchive", "heartbeat"). Validators
// that don't read either behave unchanged.
type Validator[T Entity] interface {
	// Validate checks entity invariants. Returns ErrValidation (or a
	// wrapped form) when the entity is invalid.
	Validate(ctx context.Context, entity T) error
}

// opCtxKey is the unexported ctx key for the canonical CRUD Op carried
// by Service.{Create,Update,Delete}. Unexported struct types are the
// standard Go context-key idiom — they avoid collision with any other
// package's ctx values.
type opCtxKey struct{}

// subOpCtxKey is the unexported ctx key for the adopter-defined
// sub-verb name layered onto the canonical Op.
type subOpCtxKey struct{}

// WithOp returns a child ctx carrying op as the canonical CRUD verb
// driving the in-flight Service operation.
//
// Service.{Create,Update,Delete} call this automatically before
// invoking the Validator and before publishing pre-events, so adopters
// do NOT thread it manually for the canonical CRUD verbs. Callers that
// set WithOp(ctx, ...) before invoking Service see their value
// overwritten by Service — the canonical Op is determined by which
// method is running, not by the caller. Use WithSubOp instead for
// adopter-specific verbs that are technically Update (e.g. "archive",
// "unarchive", "cancel") — Op stays canonical CRUD.
func WithOp(ctx context.Context, op Op) context.Context {
	return context.WithValue(ctx, opCtxKey{}, op)
}

// OpFromCtx returns the canonical CRUD verb driving the in-flight
// Service operation, or empty Op if ctx was not produced by Service.
// Empty is the documented "no canonical verb in flight" sentinel —
// Validators that don't read Op behave as before.
func OpFromCtx(ctx context.Context) Op {
	if v, ok := ctx.Value(opCtxKey{}).(Op); ok {
		return v
	}
	return Op("")
}

// WithSubOp returns a child ctx carrying a free-form sub-verb name
// layered onto the canonical Op. Use for adopter-specific verbs that
// are technically Update at the kit level (e.g. "archive",
// "unarchive", "heartbeat"). Kit ships no SubOp constants; adopters
// define their own vocabulary.
func WithSubOp(ctx context.Context, name string) context.Context {
	return context.WithValue(ctx, subOpCtxKey{}, name)
}

// SubOpFromCtx returns the adopter-defined sub-verb name carried in
// ctx, or empty string if none was set. Empty is the documented
// "plain Update, no sub-verb" signal.
func SubOpFromCtx(ctx context.Context) string {
	if v, ok := ctx.Value(subOpCtxKey{}).(string); ok {
		return v
	}
	return ""
}
