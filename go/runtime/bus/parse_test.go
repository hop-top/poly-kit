package bus_test

import (
	"errors"
	"testing"

	"hop.top/kit/go/runtime/bus"
)

func TestParseTopic_NoModifier(t *testing.T) {
	b, action, err := bus.ParseTopic("kit.config.snapshot.reloaded")
	if err != nil {
		t.Fatalf("ParseTopic returned error: %v", err)
	}
	if action != "reloaded" {
		t.Errorf("action: got %q want %q", action, "reloaded")
	}
	if b.SourceSeg() != "kit" || b.CategorySeg() != "config" ||
		b.ObjectSeg() != "snapshot" || b.ModifierSeg() != "" {
		t.Errorf("segments: got source=%q category=%q object=%q modifier=%q",
			b.SourceSeg(), b.CategorySeg(), b.ObjectSeg(), b.ModifierSeg())
	}
}

func TestParseTopic_WithModifier(t *testing.T) {
	b, action, err := bus.ParseTopic("kit.config.snapshot_reload.failed")
	if err != nil {
		t.Fatalf("ParseTopic returned error: %v", err)
	}
	if action != "failed" {
		t.Errorf("action: got %q want %q", action, "failed")
	}
	if b.ObjectSeg() != "snapshot" {
		t.Errorf("object: got %q want %q", b.ObjectSeg(), "snapshot")
	}
	if b.ModifierSeg() != "reload" {
		t.Errorf("modifier: got %q want %q", b.ModifierSeg(), "reload")
	}
}

func TestParseTopic_MultiUnderscoreModifier(t *testing.T) {
	// Per ADR-0017: split on FIRST underscore. The remainder
	// (including additional underscores) is the modifier.
	b, action, err := bus.ParseTopic("kit.config.snapshot_partial_reload.failed")
	if err != nil {
		t.Fatalf("ParseTopic returned error: %v", err)
	}
	if action != "failed" {
		t.Errorf("action: got %q want %q", action, "failed")
	}
	if b.ObjectSeg() != "snapshot" {
		t.Errorf("object: got %q want %q", b.ObjectSeg(), "snapshot")
	}
	if b.ModifierSeg() != "partial_reload" {
		t.Errorf("modifier: got %q want %q", b.ModifierSeg(), "partial_reload")
	}
}

func TestParseTopic_RoundTrip(t *testing.T) {
	cases := []bus.Topic{
		"kit.runtime.entity.created",
		"kit.config.snapshot.reloaded",
		"kit.config.snapshot_reload.failed",
		"kit.config.snapshot_partial_reload.failed",
		"myapp.core.breaker.tripped",
		"crm.sales.deal.created",
	}
	for _, want := range cases {
		want := want
		t.Run(string(want), func(t *testing.T) {
			b, action, err := bus.ParseTopic(string(want))
			if err != nil {
				t.Fatalf("ParseTopic(%q): %v", want, err)
			}
			got := b.Action(action)
			if got != want {
				t.Fatalf("round-trip: got %q want %q", got, want)
			}
		})
	}
}

func TestParseTopic_BuilderRoundTrip(t *testing.T) {
	// Construct via TopicOf, parse, re-render via Action — should
	// produce an identical wire string.
	original := bus.TopicOf("kit", "config", "snapshot").
		Mod("reload").
		Action("failed")

	b, action, err := bus.ParseTopic(string(original))
	if err != nil {
		t.Fatalf("ParseTopic: %v", err)
	}
	got := b.Action(action)
	if got != original {
		t.Fatalf("builder round-trip: got %q want %q", got, original)
	}
}

func TestParseTopic_InvalidReturnsTypedError(t *testing.T) {
	cases := []string{
		"",                              // empty
		"kit.config.snapshot",           // 3 segments
		"kit.config.snapshot.reload.x",  // 5 segments
		"Kit.config.snapshot.reloaded",  // uppercase
		"kit.config.snapshot.create",    // not past tense
		"kit.config.snapshot.re-loaded", // punctuation
	}
	for _, in := range cases {
		in := in
		t.Run(in, func(t *testing.T) {
			_, _, err := bus.ParseTopic(in)
			if err == nil {
				t.Fatalf("expected error for %q", in)
			}
			var ite *bus.InvalidTopicError
			if !errors.As(err, &ite) {
				t.Fatalf("error does not wrap *InvalidTopicError: %v (%T)", err, err)
			}
		})
	}
}

func TestParseTopic_RetargetWithMod(t *testing.T) {
	// Parse, change the modifier, re-render. Confirms the builder
	// returned by ParseTopic is mutable through Mod.
	b, action, err := bus.ParseTopic("kit.config.snapshot.reloaded")
	if err != nil {
		t.Fatalf("ParseTopic: %v", err)
	}
	got := b.Mod("retry").Action(action)
	want := bus.Topic("kit.config.snapshot_retry.reloaded")
	if got != want {
		t.Fatalf("retarget: got %q want %q", got, want)
	}
}
