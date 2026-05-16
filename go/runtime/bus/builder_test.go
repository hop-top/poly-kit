package bus_test

import (
	"errors"
	"testing"

	"hop.top/kit/go/runtime/bus"
)

func TestTopicOf_NoModifier(t *testing.T) {
	got := bus.TopicOf("kit", "config", "snapshot").Action("reloaded")
	want := bus.Topic("kit.config.snapshot.reloaded")
	if got != want {
		t.Fatalf("TopicOf no-modifier: got %q want %q", got, want)
	}
}

func TestTopicOf_WithModifier(t *testing.T) {
	got := bus.TopicOf("kit", "config", "snapshot").Mod("reload").Action("failed")
	want := bus.Topic("kit.config.snapshot_reload.failed")
	if got != want {
		t.Fatalf("TopicOf with-modifier: got %q want %q", got, want)
	}
}

func TestTopicOf_ModLastWins(t *testing.T) {
	// Documented behavior: last Mod wins. Build with two Mod
	// calls and assert the second value lands on the wire.
	got := bus.TopicOf("kit", "config", "snapshot").
		Mod("reload").
		Mod("retry").
		Action("failed")
	want := bus.Topic("kit.config.snapshot_retry.failed")
	if got != want {
		t.Fatalf("Mod last-wins: got %q want %q", got, want)
	}
}

func TestTopicOf_ModEmptyClears(t *testing.T) {
	got := bus.TopicOf("kit", "config", "snapshot").
		Mod("reload").
		Mod(""). // explicit clear
		Action("reloaded")
	want := bus.Topic("kit.config.snapshot.reloaded")
	if got != want {
		t.Fatalf("Mod empty-clears: got %q want %q", got, want)
	}
}

func TestTopicOf_PanicsOnUppercase(t *testing.T) {
	defer func() {
		r := recover()
		if r == nil {
			t.Fatalf("expected panic on uppercase action")
		}
		err, ok := r.(error)
		if !ok {
			t.Fatalf("expected error in panic, got %T", r)
		}
		var ite *bus.InvalidTopicError
		if !errors.As(err, &ite) {
			t.Fatalf("panic error does not wrap *InvalidTopicError: %v", err)
		}
	}()
	bus.TopicOf("kit", "config", "snapshot").Action("Reloaded")
}

func TestTopicOf_PanicsOnPunctuation(t *testing.T) {
	defer func() {
		r := recover()
		if r == nil {
			t.Fatalf("expected panic on punctuation in modifier")
		}
	}()
	bus.TopicOf("kit", "config", "snapshot").Mod("re-load").Action("failed")
}

func TestTopicOf_PanicsOnNonPastTense(t *testing.T) {
	defer func() {
		r := recover()
		if r == nil {
			t.Fatalf("expected panic on non-past-tense action")
		}
	}()
	// "create" is present tense — not in pastTenseWhitelist, doesn't end in "ed".
	bus.TopicOf("kit", "config", "snapshot").Action("create")
}

func TestPrefixedTopicOf_NoModifier(t *testing.T) {
	got := bus.PrefixedTopicOf("kit", "config", "snapshot").Action("reloaded")
	want := bus.Topic("kit.config.snapshot.reloaded")
	if got != want {
		t.Fatalf("PrefixedTopicOf: got %q want %q", got, want)
	}
}

func TestPrefixedTopicOf_WithModifier(t *testing.T) {
	got := bus.PrefixedTopicOf("kit", "config", "snapshot", "reload").Action("failed")
	want := bus.Topic("kit.config.snapshot_reload.failed")
	if got != want {
		t.Fatalf("PrefixedTopicOf with mod: got %q want %q", got, want)
	}
}

func TestPrefixedTopicOf_LastModifierWins(t *testing.T) {
	got := bus.PrefixedTopicOf("kit", "config", "snapshot", "reload", "retry").Action("failed")
	want := bus.Topic("kit.config.snapshot_retry.failed")
	if got != want {
		t.Fatalf("PrefixedTopicOf variadic last-wins: got %q want %q", got, want)
	}
}

func TestTopicBuilder_SegmentAccessors(t *testing.T) {
	b := bus.TopicOf("kit", "config", "snapshot").Mod("reload")
	if got := b.SourceSeg(); got != "kit" {
		t.Errorf("SourceSeg: got %q want %q", got, "kit")
	}
	if got := b.CategorySeg(); got != "config" {
		t.Errorf("CategorySeg: got %q want %q", got, "config")
	}
	if got := b.ObjectSeg(); got != "snapshot" {
		t.Errorf("ObjectSeg: got %q want %q", got, "snapshot")
	}
	if got := b.ModifierSeg(); got != "reload" {
		t.Errorf("ModifierSeg: got %q want %q", got, "reload")
	}
}

func TestTopicBuilder_SegmentReplacements(t *testing.T) {
	got := bus.TopicOf("kit", "config", "snapshot").
		Source("myapp").
		Category("runtime").
		Object("user").
		Action("created")
	want := bus.Topic("myapp.runtime.user.created")
	if got != want {
		t.Fatalf("segment replacement: got %q want %q", got, want)
	}
}
