package bus

import "testing"

func TestTopicFilter_NilAllowPassesAll(t *testing.T) {
	f := TopicFilter{}
	if !f.Match("anything.here") {
		t.Error("nil allow should pass all topics")
	}
}

func TestTopicFilter_DenyBlocksFirst(t *testing.T) {
	f := TopicFilter{
		Allow: []string{"task.#"},
		Deny:  []string{"task.secret"},
	}
	if f.Match("task.secret") {
		t.Error("deny should block even when allow matches")
	}
	if !f.Match("task.created") {
		t.Error("non-denied allow topic should pass")
	}
}

func TestTopicFilter_AllowFilters(t *testing.T) {
	f := TopicFilter{
		Allow: []string{"task.*", "track.*"},
	}
	if !f.Match("task.created") {
		t.Error("task.created should match task.*")
	}
	if !f.Match("track.updated") {
		t.Error("track.updated should match track.*")
	}
	if f.Match("llm.request") {
		t.Error("llm.request should not match allow list")
	}
}

func TestTopicFilter_DenyOnly(t *testing.T) {
	f := TopicFilter{
		Deny: []string{"internal.#"},
	}
	if f.Match("internal.debug") {
		t.Error("internal.debug should be denied")
	}
	if !f.Match("task.created") {
		t.Error("non-denied topic should pass with nil allow")
	}
}

func TestTopicFilter_EmptyAllowBlocksAll(t *testing.T) {
	f := TopicFilter{
		Allow: []string{},
	}
	if f.Match("anything") {
		t.Error("empty allow list should block all")
	}
}

func TestTopicFilter_GlobPatterns(t *testing.T) {
	f := TopicFilter{
		Allow: []string{"llm.#"},
		Deny:  []string{"llm.internal.*"},
	}
	if !f.Match("llm.request") {
		t.Error("llm.request should pass")
	}
	if !f.Match("llm.request.start") {
		t.Error("llm.request.start should pass with #")
	}
	if f.Match("llm.internal.debug") {
		t.Error("llm.internal.debug should be denied")
	}
}
