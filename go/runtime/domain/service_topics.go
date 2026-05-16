package domain

import (
	"fmt"

	"hop.top/kit/go/runtime/bus"
)

// Topics holds the per-phase topic strings emitted by Service[T].
//
// Service[T] publishes five events around each CRUD action:
//
//   - PreValidated: synchronous, fires BEFORE validation. Subscriber
//     errors veto the operation before validation runs. Use case: gate
//     intent/access regardless of payload validity ("only admin may
//     attempt delete, even with malformed payload").
//   - PrePersisted: synchronous, fires AFTER validation, BEFORE the
//     repo write. Subscriber errors veto the operation before any
//     mutation. Use case: business rules that need validated data
//     ("delete requires --note AND status must be DONE/SKIPPED").
//   - Created/Updated/Deleted: best-effort post events fired after a
//     successful repo write. Subscriber errors are intentionally
//     swallowed — post events are notifications, not gates.
//
// Pre-events are shared per service (not per CRUD action). The payload
// (PreEntityPayload) carries an Op field so subscribers can discriminate
// create vs update vs delete via predicates like payload.op == "delete".
//
// Adopters override individual phases with WithTopics or replace all
// five with WithTopicPrefix.
type Topics struct {
	PreValidated bus.Topic
	PrePersisted bus.Topic
	Created      bus.Topic
	Updated      bus.Topic
	Deleted      bus.Topic
}

// DefaultTopics is the kit baseline used when no override is supplied.
// Each topic conforms to the kit 4-segment past-tense convention and
// would pass bus.ValidateTopic.
var DefaultTopics = Topics{
	PreValidated: "kit.runtime.entity.pre_validated",
	PrePersisted: "kit.runtime.entity.pre_persisted",
	Created:      "kit.runtime.entity.created",
	Updated:      "kit.runtime.entity.updated",
	Deleted:      "kit.runtime.entity.deleted",
}

// crudActions is the canonical action list passed to bus.PrefixTopics
// when expanding a 3-segment prefix. Order is fixed so error messages
// from PrefixTopics report a predictable first-failing action.
var crudActions = []string{"pre_validated", "pre_persisted", "created", "updated", "deleted"}

// WithTopicPrefix sets all five Service topics from a 3-segment
// prefix of the form "source.category.object". The composed topics
// are "<prefix>.pre_validated", "<prefix>.pre_persisted",
// "<prefix>.created", "<prefix>.updated", "<prefix>.deleted".
//
// Example:
//
//	domain.NewService(repo, domain.WithTopicPrefix[Workspace]("wsm.runtime.workspace"))
//
// Panics if prefix fails bus.PrefixTopics validation. Constructors
// are wired at boot, so a misconfigured prefix is a programmer error
// — fail-loud is preferred over silent default fallback that would
// hide subscribers missing events at runtime.
func WithTopicPrefix[T Entity](prefix string) Option[T] {
	tm, err := bus.PrefixTopics(prefix, crudActions)
	if err != nil {
		panic(fmt.Sprintf("domain.WithTopicPrefix(%q): %v", prefix, err))
	}
	t := Topics{
		PreValidated: tm["pre_validated"],
		PrePersisted: tm["pre_persisted"],
		Created:      tm["created"],
		Updated:      tm["updated"],
		Deleted:      tm["deleted"],
	}
	return func(s *Service[T]) { s.topics = t }
}

// WithTopics replaces individual phase topics. Empty bus.Topic
// fields keep the corresponding DefaultTopics value, so callers can
// override a single phase without restating the others.
//
// Example:
//
//	domain.NewService(repo, domain.WithTopics[Workspace](domain.Topics{
//	    Created: "wsm.runtime.workspace.created",
//	}))
func WithTopics[T Entity](t Topics) Option[T] {
	if t.PreValidated == "" {
		t.PreValidated = DefaultTopics.PreValidated
	}
	if t.PrePersisted == "" {
		t.PrePersisted = DefaultTopics.PrePersisted
	}
	if t.Created == "" {
		t.Created = DefaultTopics.Created
	}
	if t.Updated == "" {
		t.Updated = DefaultTopics.Updated
	}
	if t.Deleted == "" {
		t.Deleted = DefaultTopics.Deleted
	}
	return func(s *Service[T]) { s.topics = t }
}
