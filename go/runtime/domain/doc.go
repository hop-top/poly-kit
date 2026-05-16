// Package domain provides generic building blocks for domain-driven design:
// entity interfaces, repositories, state machines with event hooks, validation,
// auditing, and a composable service layer with middleware options.
//
// # Service[T] events
//
// Service[T] publishes up to five events per CRUD operation through a
// configured EventPublisher. Two are synchronous, veto-able pre-events;
// three are best-effort, fire-and-forget post-events.
//
// Pre-events (synchronous, veto-able). A non-nil subscriber error
// aborts the operation before any state mutation. The error is
// wrapped as "pre-validated veto: <err>" or "pre-persisted veto: <err>".
//
//   - kit.runtime.entity.pre_validated — fires BEFORE validation. Use
//     for intent/access gates that should reject regardless of payload
//     validity (e.g. "only admin may attempt delete, even with a
//     malformed payload"). Payload.Entity holds RAW caller input.
//   - kit.runtime.entity.pre_persisted — fires AFTER validation,
//     BEFORE the repo write. Use for business rules that need
//     validated data (e.g. "delete requires --note AND status must be
//     DONE/SKIPPED"). Payload.Entity holds the VALIDATED entity.
//
// Post-events (fire-and-forget). Subscriber errors are intentionally
// swallowed — these topics notify, they don't gate.
//
//   - kit.runtime.entity.created — after a successful repo.Create
//   - kit.runtime.entity.updated — after a successful repo.Update
//   - kit.runtime.entity.deleted — after a successful repo.Delete
//
// Pre-events are SHARED per phase — there's one PreValidated topic and
// one PrePersisted topic across all CRUD ops. The PreEntityPayload.Op
// field discriminates create / update / delete so a single subscriber
// can fan out via predicates like payload.op == "delete".
//
// Adopters override topic strings via WithTopicPrefix or WithTopics,
// and the StateMachine helper exposes the same pattern for state
// transitions (kit.runtime.state.pre_transitioned /
// kit.runtime.state.post_transitioned).
//
// This package is intentionally dependency-free within the kit module.
// SQLite implementations live in the domain/sqlite subpackage.
package domain
