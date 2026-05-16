package domain

import (
	"context"
	"fmt"
	"time"
)

// Op identifies the CRUD operation that triggered a pre-event. It
// appears on PreEntityPayload so a single subscriber can fan out
// across create/update/delete via predicates like payload.op == "delete".
type Op string

// Op constants emitted on PreEntityPayload.
const (
	OpCreate Op = "create"
	OpUpdate Op = "update"
	OpDelete Op = "delete"
)

// Phase identifies which pre-event seam fired. Subscribers that
// listen to a single shared topic per phase can still discriminate
// at the payload level, e.g. for diagnostics.
type Phase string

// Phase constants emitted on PreEntityPayload.
const (
	PhasePreValidated Phase = "pre_validated"
	PhasePrePersisted Phase = "pre_persisted"
)

// PreEntityPayload is the payload for synchronous, veto-able pre-events
// emitted by Service[T] before validation and before repo writes.
//
// Field semantics by phase:
//
//   - PhasePreValidated: Entity holds the RAW input as passed by the
//     caller. It may be malformed; validation has not run yet.
//   - PhasePrePersisted: Entity holds the VALIDATED entity. Validation
//     (if a validator is configured) has succeeded; sane invariants hold.
//
// For OpDelete the caller passes only an ID — Entity is nil and
// EntityID carries the target identifier. For OpCreate / OpUpdate
// EntityID is populated from (*entity).GetID() once the entity is in
// hand and Entity holds the entity value (de-referenced from the
// caller's pointer for ergonomics on the subscriber side).
type PreEntityPayload struct {
	Op       Op
	Phase    Phase
	EntityID string
	Entity   any
}

// Option configures a Service via functional options.
type Option[T Entity] func(*Service[T])

// WithAudit attaches an audit repository to the service.
func WithAudit[T Entity](ar AuditRepository) Option[T] {
	return func(s *Service[T]) { s.audit = ar }
}

// WithPublisher attaches an EventPublisher for domain events.
func WithPublisher[T Entity](pub EventPublisher) Option[T] {
	return func(s *Service[T]) { s.pub = pub }
}

// WithValidation attaches a validator to the service.
func WithValidation[T Entity](v Validator[T]) Option[T] {
	return func(s *Service[T]) { s.validator = v }
}

// Service wraps a Repository with optional middleware: validation, auditing,
// event publishing.
//
// Each CRUD method publishes up to five events through the configured
// EventPublisher (when one is attached). The pre-events are SYNCHRONOUS
// gates: a non-nil error from any subscriber vetoes the operation and
// is wrapped as either "pre-validated veto: <err>" or "pre-persisted
// veto: <err>". The post-events (Created/Updated/Deleted) keep their
// fire-and-forget semantics — subscriber errors are intentionally
// swallowed because post events are notifications, not gates.
//
// Event ordering for Create / Update:
//
//  1. PreValidated  (sync, raw input, veto-able)
//  2. validator.Validate (if configured)
//  3. PrePersisted  (sync, validated entity, veto-able)
//  4. repo.Create / repo.Update
//  5. audit + post event (Created / Updated)
//
// Delete has no payload to validate, so step 2 is a no-op but the seams
// stay symmetric: PreValidated fires with EntityID, then PrePersisted
// fires before the repo write.
type Service[T Entity] struct {
	repo      Repository[T]
	audit     AuditRepository
	pub       EventPublisher
	validator Validator[T]
	topics    Topics
}

// NewService creates a Service around the given repository.
// Additional middleware is configured via Option functions.
func NewService[T Entity](repo Repository[T], opts ...Option[T]) *Service[T] {
	s := &Service[T]{repo: repo, topics: DefaultTopics}
	for _, o := range opts {
		o(s)
	}
	return s
}

// Create publishes pre-events, validates, persists, audits, and
// publishes the post-event. Subscriber errors on the pre-events veto
// the operation before any state mutation.
func (s *Service[T]) Create(ctx context.Context, entity *T) error {
	ctx = WithOp(ctx, OpCreate)
	id := (*entity).GetID()
	if err := s.publishPreValidated(ctx, OpCreate, id, *entity); err != nil {
		return err
	}
	if s.validator != nil {
		if err := s.validator.Validate(ctx, *entity); err != nil {
			return err
		}
	}
	if err := s.publishPrePersisted(ctx, OpCreate, id, *entity); err != nil {
		return err
	}
	if err := s.repo.Create(ctx, entity); err != nil {
		return err
	}
	id = (*entity).GetID()
	s.auditAction(ctx, id, "created")
	s.publishEvent(ctx, string(s.topics.Created), id)
	return nil
}

// Get retrieves an entity by ID.
func (s *Service[T]) Get(ctx context.Context, id string) (*T, error) {
	return s.repo.Get(ctx, id)
}

// List retrieves entities matching the query.
func (s *Service[T]) List(ctx context.Context, q Query) ([]T, error) {
	return s.repo.List(ctx, q)
}

// Update publishes pre-events, validates, persists, audits, and
// publishes the post-event. Subscriber errors on the pre-events veto
// the operation before any state mutation.
func (s *Service[T]) Update(ctx context.Context, entity *T) error {
	ctx = WithOp(ctx, OpUpdate)
	id := (*entity).GetID()
	if err := s.publishPreValidated(ctx, OpUpdate, id, *entity); err != nil {
		return err
	}
	if s.validator != nil {
		if err := s.validator.Validate(ctx, *entity); err != nil {
			return err
		}
	}
	if err := s.publishPrePersisted(ctx, OpUpdate, id, *entity); err != nil {
		return err
	}
	if err := s.repo.Update(ctx, entity); err != nil {
		return err
	}
	id = (*entity).GetID()
	s.auditAction(ctx, id, "updated")
	s.publishEvent(ctx, string(s.topics.Updated), id)
	return nil
}

// Delete publishes pre-events, removes the entity, audits, and
// publishes the post-event. Subscriber errors on the pre-events veto
// the operation before any state mutation. Delete has no payload to
// validate, so PreEntityPayload.Entity is nil for both pre-phases.
func (s *Service[T]) Delete(ctx context.Context, id string) error {
	ctx = WithOp(ctx, OpDelete)
	if err := s.publishPreValidated(ctx, OpDelete, id, nil); err != nil {
		return err
	}
	if err := s.publishPrePersisted(ctx, OpDelete, id, nil); err != nil {
		return err
	}
	if err := s.repo.Delete(ctx, id); err != nil {
		return err
	}
	s.auditAction(ctx, id, "deleted")
	s.publishEvent(ctx, string(s.topics.Deleted), id)
	return nil
}

// publishPreValidated fires the synchronous pre-validation event.
// A non-nil subscriber error is wrapped as "pre-validated veto: <err>"
// and aborts the caller before validation runs.
func (s *Service[T]) publishPreValidated(ctx context.Context, op Op, id string, entity any) error {
	if s.pub == nil {
		return nil
	}
	payload := PreEntityPayload{
		Op:       op,
		Phase:    PhasePreValidated,
		EntityID: id,
		Entity:   entity,
	}
	if err := s.pub.Publish(ctx, string(s.topics.PreValidated), "domain.service", payload); err != nil {
		return fmt.Errorf("pre-validated veto: %w", err)
	}
	return nil
}

// publishPrePersisted fires the synchronous pre-persist event after
// validation has succeeded but before the repo mutation. A non-nil
// subscriber error is wrapped as "pre-persisted veto: <err>" and
// aborts the caller before any state mutation.
func (s *Service[T]) publishPrePersisted(ctx context.Context, op Op, id string, entity any) error {
	if s.pub == nil {
		return nil
	}
	payload := PreEntityPayload{
		Op:       op,
		Phase:    PhasePrePersisted,
		EntityID: id,
		Entity:   entity,
	}
	if err := s.pub.Publish(ctx, string(s.topics.PrePersisted), "domain.service", payload); err != nil {
		return fmt.Errorf("pre-persisted veto: %w", err)
	}
	return nil
}

func (s *Service[T]) auditAction(ctx context.Context, entityID, action string) {
	if s.audit == nil {
		return
	}
	_ = s.audit.AddEntry(ctx, &AuditEntry{
		EntityID:  entityID,
		Timestamp: time.Now().UTC().Format(time.RFC3339),
		Action:    action,
	})
}

// publishEvent does a best-effort publish of a post-domain event.
// Subscriber errors are intentionally swallowed — post events are
// notifications, not gates. Use the pre-event seams (PreValidated /
// PrePersisted) when subscriber errors must veto the operation.
func (s *Service[T]) publishEvent(ctx context.Context, topic, entityID string) {
	if s.pub == nil {
		return
	}
	_ = s.pub.Publish(ctx, topic, "domain.service", map[string]string{"id": entityID})
}
