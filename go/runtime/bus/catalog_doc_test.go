package bus_test

import (
	"strings"
	"testing"

	"hop.top/kit/go/runtime/bus"
)

// TestDocumentedDefaultTopicsValidate pins the topic catalog published in
// docs/adopters/reference/domain-events.md. Every default topic kit ships
// must satisfy the construction-time contract, including the past-tense
// action rule. A default renamed in code without updating the catalog
// fails here.
func TestDocumentedDefaultTopicsValidate(t *testing.T) {
	documented := []bus.Topic{
		// go/ai/llm
		"kit.ai.request.started",
		"kit.ai.response.received",
		"kit.ai.request.errored",
		"kit.ai.fallback.applied",
		"kit.ai.route.selected",
		"kit.ai.eva.evaluated",
		// go/runtime/domain Service[T]
		"kit.runtime.entity.pre_validated",
		"kit.runtime.entity.pre_persisted",
		"kit.runtime.entity.created",
		"kit.runtime.entity.updated",
		"kit.runtime.entity.deleted",
		// go/runtime/domain StateMachine
		"kit.runtime.state.pre_transitioned",
		"kit.runtime.state.post_transitioned",
		// go/core/stage
		"kit.runtime.stage.proposed",
		"kit.runtime.stage.transitioned",
		"kit.runtime.stage.entered",
		"kit.runtime.stage.expired",
		"kit.runtime.stage.violated",
		// go/core/upgrade
		"kit.core.upgrade.released",
		"kit.core.upgrade.downloaded",
		"kit.core.upgrade.installed",
		"kit.core.upgrade.snoozed",
		// go/core/breaker
		"kit.core.breaker.tripped",
		"kit.core.breaker.opened",
		"kit.core.breaker.closed",
		"kit.core.breaker.half_opened",
		// go/core/config
		"kit.config.snapshot.reloaded",
		"kit.config.snapshot.reload_failed",
		// go/transport/api
		"kit.api.request.started",
		"kit.api.request.ended",
	}

	for _, topic := range documented {
		if err := bus.Validate(topic); err != nil {
			t.Errorf("Validate(%q): %v", topic, err)
		}
		if err := bus.ValidateTopic(topic); err != nil {
			t.Errorf("ValidateTopic(%q): %v", topic, err)
		}
	}
}

// TestRetiredAPITopicsAreNonConformant documents why the pre-kit
// api.request.start / .end topics were removed: both fail the published
// contract. Guards against a well-meaning revert.
func TestRetiredAPITopicsAreNonConformant(t *testing.T) {
	for _, topic := range []bus.Topic{"api.request.start", "api.request.end"} {
		if err := bus.Validate(topic); err == nil {
			t.Errorf("Validate(%q) = nil, want error (3 segments)", topic)
		}
	}
	// Even at 4 segments the present-tense action fails construction-time
	// validation.
	if err := bus.ValidateTopic("kit.api.request.start"); err == nil {
		t.Error("ValidateTopic(kit.api.request.start) = nil, want past-tense error")
	}
}

// TestTopicLengthCap pins the 128-character cap documented in
// docs/adopters/reference/bus-api.md. Other language ports enforce the
// same bound, so changing it silently desynchronizes them.
func TestTopicLengthCap(t *testing.T) {
	// 4 segments, exactly 128 chars total.
	// 4 segments of 31 chars + 3 dots = 127; pad one segment to reach 128.
	seg := strings.Repeat("a", 31)
	atCap := bus.Topic(seg + "." + seg + "." + seg + "." + seg + "a")
	if len(atCap) != 128 {
		t.Fatalf("test setup: len = %d, want 128", len(atCap))
	}
	if err := bus.Validate(atCap); err != nil {
		t.Errorf("Validate(128 chars) = %v, want nil", err)
	}

	overCap := bus.Topic(string(atCap) + "a")
	if err := bus.Validate(overCap); err == nil {
		t.Error("Validate(129 chars) = nil, want length error")
	}
}

// TestWildcardsRejectedInPublishedTopics pins the rule that wildcards are
// valid only in subscribe patterns, never in a published topic.
func TestWildcardsRejectedInPublishedTopics(t *testing.T) {
	for _, topic := range []bus.Topic{
		"kit.ai.request.*",
		"kit.ai.*.started",
		"kit.ai.request.#",
		"*.ai.request.started",
	} {
		if err := bus.Validate(topic); err == nil {
			t.Errorf("Validate(%q) = nil, want wildcard rejection", topic)
		}
	}

	// The same wildcards remain legal as subscribe patterns.
	if !bus.Topic("kit.ai.request.started").Match("kit.ai.request.*") {
		t.Error("kit.ai.request.* should match as a subscribe pattern")
	}
	if !bus.Topic("kit.ai.request.started").Match("kit.ai.#") {
		t.Error("kit.ai.# should match as a subscribe pattern")
	}
}
