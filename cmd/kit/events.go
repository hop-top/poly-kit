package main

import (
	"context"
	"fmt"
	"strings"

	"hop.top/kit/engine/store"
	"hop.top/kit/go/runtime/bus"
)

// Bus topic constants for kit-engine document mutations.
//
// Per cli-conventions §9, tools emit events under
// `<tool>.<noun>.<verb>`. For the kit engine, "kit.engine" is the tool
// namespace, "document" is the noun, and the verb is the lifecycle.
//
// These constants name the default topics; adopters wiring
// registerDocumentRoutes can override them with WithTopicPrefix /
// WithTopics, mirroring the option pattern already established in
// transport/api (T-0122) and core/upgrade.
const (
	TopicDocumentCreated bus.Topic = "kit.engine.document.created"
	TopicDocumentUpdated bus.Topic = "kit.engine.document.updated"
	TopicDocumentDeleted bus.Topic = "kit.engine.document.deleted"

	// EventSource identifies the emitter of document.* events.
	EventSource = "kit.engine"
)

// DocumentTopics names the bus topics emitted on document mutation. All
// topics MUST conform to the kit 4-segment past-tense convention
// (source.category.object.action) — see bus.ValidateTopic.
type DocumentTopics struct {
	Created bus.Topic
	Updated bus.Topic
	Deleted bus.Topic
}

// DefaultDocumentTopics is the kit baseline used when no override is
// supplied. Each topic conforms to bus.ValidateTopic.
var DefaultDocumentTopics = DocumentTopics{
	Created: TopicDocumentCreated,
	Updated: TopicDocumentUpdated,
	Deleted: TopicDocumentDeleted,
}

// documentActions is the canonical action list passed to
// bus.PrefixTopics when expanding a 3-segment prefix. Order is fixed so
// error messages from PrefixTopics report a predictable first-failing
// action.
var documentActions = []string{"created", "updated", "deleted"}

// EventConfig holds the resolved event-publishing configuration used by
// registerDocumentRoutes.
type EventConfig struct {
	topics DocumentTopics
	source string
}

// EventOption configures document-event publishing.
type EventOption func(*EventConfig)

// newEventConfig returns an EventConfig seeded with DefaultDocumentTopics
// and the default source, then applies opts.
func newEventConfig(opts ...EventOption) *EventConfig {
	c := &EventConfig{
		topics: DefaultDocumentTopics,
		source: EventSource,
	}
	for _, o := range opts {
		o(c)
	}
	return c
}

// WithTopicPrefix sets all three document lifecycle topics from a
// 3-segment prefix of the form "source.category.object". Composed
// topics are "<prefix>.created", "<prefix>.updated", "<prefix>.deleted".
//
// The first two prefix segments are also used as the event Source
// (e.g. "myapp.engine.document" → source "myapp.engine"), so
// downstream subscribers can route on source as well as topic.
//
// Example:
//
//	registerDocumentRoutes(r, vds, b, WithTopicPrefix("myapp.engine.document"))
//
// Panics if prefix fails bus.PrefixTopics validation. Constructors are
// wired at boot, so a misconfigured prefix is a programmer error —
// fail-loud is preferred over silent default fallback that would hide
// subscribers missing events at runtime.
func WithTopicPrefix(prefix string) EventOption {
	tm, err := bus.PrefixTopics(prefix, documentActions)
	if err != nil {
		panic(fmt.Sprintf("kit.WithTopicPrefix(%q): %v", prefix, err))
	}
	// Derive source from the first two prefix segments
	// (source.category) so events still carry a semantic emitter that
	// matches the topic namespace.
	source := EventSource
	parts := strings.Split(prefix, ".")
	if len(parts) >= 2 {
		source = parts[0] + "." + parts[1]
	}
	return func(c *EventConfig) {
		c.topics = DocumentTopics{
			Created: tm["created"],
			Updated: tm["updated"],
			Deleted: tm["deleted"],
		}
		c.source = source
	}
}

// WithTopics replaces individual document lifecycle topics. Empty
// bus.Topic fields keep the corresponding DefaultDocumentTopics value,
// so callers can override one action without restating the others.
// Non-empty topics are validated; invalid topics panic at construction
// time (see WithTopicPrefix).
//
// Example:
//
//	registerDocumentRoutes(r, vds, b, WithTopics(DocumentTopics{
//	    Created: "myapp.engine.document.created",
//	}))
func WithTopics(t DocumentTopics) EventOption {
	return func(c *EventConfig) {
		if t.Created != "" {
			if err := bus.ValidateTopic(t.Created); err != nil {
				panic(fmt.Sprintf("kit.WithTopics Created: %v", err))
			}
			c.topics.Created = t.Created
		}
		if t.Updated != "" {
			if err := bus.ValidateTopic(t.Updated); err != nil {
				panic(fmt.Sprintf("kit.WithTopics Updated: %v", err))
			}
			c.topics.Updated = t.Updated
		}
		if t.Deleted != "" {
			if err := bus.ValidateTopic(t.Deleted); err != nil {
				panic(fmt.Sprintf("kit.WithTopics Deleted: %v", err))
			}
			c.topics.Deleted = t.Deleted
		}
	}
}

// WithEventSource overrides the Source string set on every emitted
// event. By default the source is "kit.engine" (or derived from the
// first two prefix segments when WithTopicPrefix is also applied).
//
// Apply this option AFTER WithTopicPrefix when both are supplied;
// option order is significant.
func WithEventSource(source string) EventOption {
	return func(c *EventConfig) { c.source = source }
}

// DocumentEventPayload describes a document mutation. It mirrors the
// fields the existing store.Document exposes; sibling tools can rely
// on `type` + `id` to address the document and on `updated_at` for
// causal ordering of writes against the same engine instance.
//
// VersionID and Seq let subscribers (sync, audit, replay) address the
// specific version that the mutation produced rather than the latest
// state of the document. They are zero-valued on Delete events because
// the document is gone post-write — there is no new version to point
// at.
type DocumentEventPayload struct {
	Type      string `json:"type"`
	ID        string `json:"id"`
	CreatedAt string `json:"created_at,omitempty"`
	UpdatedAt string `json:"updated_at,omitempty"`
	VersionID string `json:"version_id,omitempty"`
	Seq       int    `json:"seq,omitempty"`
}

// payloadFromDoc projects a store.Document and the [store.Version]
// produced by the same write into the wire payload. Callers without a
// Version row (e.g. delete) should pass a zero Version{}; the
// version_id and seq fields will be omitted on the wire.
func payloadFromDoc(doc store.Document, ver store.Version) DocumentEventPayload {
	return DocumentEventPayload{
		Type:      doc.Type,
		ID:        doc.ID,
		CreatedAt: doc.CreatedAt,
		UpdatedAt: doc.UpdatedAt,
		VersionID: ver.VersionID,
		Seq:       ver.Seq,
	}
}

// publishDocEvent is a best-effort publisher used by the document
// HTTP handlers. A nil bus is a no-op so tests/embeddings can omit
// it; publish errors are intentionally swallowed because lifecycle
// events must never affect the HTTP response.
func publishDocEvent(ctx context.Context, b bus.Bus, topic bus.Topic, source string, payload DocumentEventPayload) {
	if b == nil {
		return
	}
	_ = b.Publish(ctx, bus.NewEvent(topic, source, payload))
}
