package cmdsurface

import (
	"context"
	"errors"

	"hop.top/kit/go/transport/api"
)

// ErrBusSinkNoPublisher is returned by BusSink.Emit when Publisher
// is nil.
var ErrBusSinkNoPublisher = errors.New("cmdsurface: BusSink has no Publisher")

// ErrBusSinkNoTopic is returned by BusSink.Emit when neither Topic
// nor TopicFn resolves a non-empty topic.
var ErrBusSinkNoTopic = errors.New("cmdsurface: BusSink has no topic")

// BusSink publishes invocation outcomes to a topic via an
// api.EventPublisher (Kafka, NATS, in-process bus, …). The payload
// is the same envelope WebhookSink uses, marshaled by the publisher
// implementation.
type BusSink struct {
	// Publisher delivers payloads to the underlying bus. Required.
	Publisher api.EventPublisher
	// Topic is the exact topic name used when TopicFn is nil.
	Topic string
	// TopicFn, when set, computes the topic from the Invocation. It
	// overrides Topic; an empty return value falls back to Topic.
	TopicFn func(Invocation) string
	// Source identifies the producer in the published event (kept
	// separate from Topic so consumers can distinguish two surfaces
	// publishing onto the same topic). Empty defaults to "cmdsurface".
	Source string
}

// busSinkEnvelope mirrors the webhook envelope so consumers can use
// the same decoding logic for either transport.
type busSinkEnvelope struct {
	Invocation Invocation `json:"invocation"`
	Result     Result     `json:"result"`
	Error      *string    `json:"error"`
}

// Emit publishes the envelope to the resolved topic. Returns the
// publisher's error verbatim, or one of the Err* sentinels when
// configuration is incomplete.
func (b *BusSink) Emit(ctx context.Context, inv Invocation, res Result, callErr error) error {
	if b.Publisher == nil {
		return ErrBusSinkNoPublisher
	}
	topic := b.Topic
	if b.TopicFn != nil {
		if t := b.TopicFn(inv); t != "" {
			topic = t
		}
	}
	if topic == "" {
		return ErrBusSinkNoTopic
	}
	source := b.Source
	if source == "" {
		source = "cmdsurface"
	}
	env := busSinkEnvelope{Invocation: inv, Result: res}
	if callErr != nil {
		s := callErr.Error()
		env.Error = &s
	}
	return b.Publisher.Publish(ctx, topic, source, env)
}
