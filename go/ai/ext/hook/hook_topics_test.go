package hook

import (
	"context"
	"strings"
	"testing"

	"hop.top/kit/go/runtime/bus"
)

// TestDefaultTopicPrefixUnchanged confirms the existing
// "kit.ext.hook.<action>" topic shape is preserved when NewBus is called
// without options.
func TestDefaultTopicPrefixUnchanged(t *testing.T) {
	b := NewBus()

	got := make(chan bus.Event, 1)
	b.Inner().SubscribeAsync("kit.ext.hook.#", func(_ context.Context, e bus.Event) {
		got <- e
	})

	if err := b.Dispatch(context.Background(), Hook("started"), nil); err != nil {
		t.Fatalf("dispatch error: %v", err)
	}

	ev := <-got
	if string(ev.Topic) != "kit.ext.hook.started" {
		t.Fatalf("expected kit.ext.hook.started, got %s", ev.Topic)
	}
	if ev.Source != "kit.ext.hook" {
		t.Fatalf("expected source kit.ext.hook, got %s", ev.Source)
	}
}

// TestWithHookTopicPrefixCustom confirms an adopter-supplied prefix
// (without trailing dot) is used to compose the topic.
func TestWithHookTopicPrefixCustom(t *testing.T) {
	b := NewBus(WithHookTopicPrefix("myapp.hooks.lifecycle"))

	got := make(chan bus.Event, 1)
	b.Inner().SubscribeAsync("myapp.hooks.lifecycle.#", func(_ context.Context, e bus.Event) {
		got <- e
	})

	if err := b.Dispatch(context.Background(), Hook("started"), nil); err != nil {
		t.Fatalf("dispatch error: %v", err)
	}

	ev := <-got
	if string(ev.Topic) != "myapp.hooks.lifecycle.started" {
		t.Fatalf("expected myapp.hooks.lifecycle.started, got %s", ev.Topic)
	}
	if ev.Source != "myapp.hooks.lifecycle" {
		t.Fatalf("expected source myapp.hooks.lifecycle, got %s", ev.Source)
	}
}

// TestWithHookTopicPrefixTrailingDotNormalized confirms a prefix passed
// with an explicit trailing dot is normalized to the same result as one
// without.
func TestWithHookTopicPrefixTrailingDotNormalized(t *testing.T) {
	b := NewBus(WithHookTopicPrefix("myapp.hooks.lifecycle."))

	got := make(chan bus.Event, 1)
	b.Inner().SubscribeAsync("myapp.hooks.lifecycle.#", func(_ context.Context, e bus.Event) {
		got <- e
	})

	if err := b.Dispatch(context.Background(), Hook("started"), nil); err != nil {
		t.Fatalf("dispatch error: %v", err)
	}

	ev := <-got
	if string(ev.Topic) != "myapp.hooks.lifecycle.started" {
		t.Fatalf("expected myapp.hooks.lifecycle.started, got %s", ev.Topic)
	}
}

// TestWithHookTopicPrefixPanicsOnInvalid covers all rejection paths for
// the construction-time validator: wrong segment count, empty input,
// uppercase segments, illegal characters, and embedded empty segments.
func TestWithHookTopicPrefixPanicsOnInvalid(t *testing.T) {
	cases := []struct {
		name    string
		prefix  string
		wantSub string
	}{
		{"two segments", "myapp.hooks", "expected 3"},
		{"two segments trailing dot", "myapp.hooks.", "expected 3"},
		{"four segments", "myapp.hooks.lifecycle.extra", "expected 3"},
		{"uppercase", "MyApp.hooks.lifecycle", "lowercase"},
		{"hyphen", "my-app.hooks.lifecycle", "lowercase"},
		{"empty", "", "empty"},
		{"only dot", ".", "empty"},
		{"empty middle segment", "myapp..lifecycle", "empty segment"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			defer func() {
				r := recover()
				if r == nil {
					t.Fatalf("expected panic for prefix %q", tc.prefix)
				}
				msg, ok := r.(string)
				if !ok {
					t.Fatalf("expected string panic, got %T: %v", r, r)
				}
				if !strings.Contains(msg, tc.wantSub) {
					t.Fatalf("panic %q does not contain %q", msg, tc.wantSub)
				}
			}()
			_ = WithHookTopicPrefix(tc.prefix)
		})
	}
}

// TestDefaultTopicPrefixConstant pins the exported constant so adopters
// can reference it without breakage if they want to compose their own
// prefix from the default.
func TestDefaultTopicPrefixConstant(t *testing.T) {
	if DefaultTopicPrefix != "kit.ext.hook." {
		t.Fatalf("DefaultTopicPrefix changed: %q", DefaultTopicPrefix)
	}
}
