package bus

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
)

func TestMode_String(t *testing.T) {
	tests := []struct {
		m    Mode
		want string
	}{
		{ModeOff, "off"},
		{ModeWarn, "warn"},
		{ModeStrict, "strict"},
		{Mode(99), "mode(99)"},
	}
	for _, tc := range tests {
		if got := tc.m.String(); got != tc.want {
			t.Errorf("Mode(%d).String() = %q, want %q", int(tc.m), got, tc.want)
		}
	}
}

func TestValidate_Valid(t *testing.T) {
	cases := []Topic{
		"crm.sales.deal.created",
		"a.b.c.d",
		"app1.cat2.obj3.act4",
		"snake_case.with_under.score_ok.action_x",
	}
	for _, tc := range cases {
		if err := Validate(tc); err != nil {
			t.Errorf("Validate(%q) unexpected error: %v", tc, err)
		}
	}
}

func TestValidate_Invalid(t *testing.T) {
	cases := []struct {
		topic  Topic
		reason string // substring expected in error message
	}{
		{"", "empty"},
		{"too.few.segments", "expected 4 segments"},
		{"a.b.c", "expected 4 segments"},
		{"a.b.c.d.e", "expected 4 segments"},
		{"crm.sales..created", "empty segment"},
		{"CRM.sales.deal.created", "invalid character"},
		{"crm.sales.deal.Created", "invalid character"},
		{"1crm.sales.deal.created", "must start with"},
		{"_crm.sales.deal.created", "must start with"},
		{"crm.sales.deal.created!", "invalid character"},
		{"crm.*.deal.created", "wildcards"},
		{"crm.sales.deal.#", "wildcards"},
		{Topic(strings.Repeat("a", 130) + ".bbb.ccc.ddd"), "exceeds max"},
	}
	for _, tc := range cases {
		err := Validate(tc.topic)
		if err == nil {
			t.Errorf("Validate(%q) expected error, got nil", tc.topic)
			continue
		}
		if !errors.Is(err, ErrInvalidTopic) {
			t.Errorf("Validate(%q) error %v not Is ErrInvalidTopic", tc.topic, err)
		}
		var ite *InvalidTopicError
		if !errors.As(err, &ite) {
			t.Errorf("Validate(%q) error %v not As *InvalidTopicError", tc.topic, err)
			continue
		}
		if ite.Topic != tc.topic {
			t.Errorf("Validate(%q): error.Topic = %q, want %q", tc.topic, ite.Topic, tc.topic)
		}
		if !strings.Contains(err.Error(), tc.reason) {
			t.Errorf("Validate(%q): error %q missing reason substring %q", tc.topic, err.Error(), tc.reason)
		}
	}
}

func TestPublish_ModeOff_AllowsInvalidTopics(t *testing.T) {
	var reported atomic.Int32
	b := New(WithEnforce(ModeOff), WithInvalidTopicReporter(func(error) {
		reported.Add(1)
	}))
	defer func() { _ = b.Close(context.Background()) }()

	var got atomic.Int32
	b.Subscribe("bad.topic", func(_ context.Context, _ Event) error {
		got.Add(1)
		return nil
	})

	if err := b.Publish(context.Background(), NewEvent("bad.topic", "src", nil)); err != nil {
		t.Fatalf("publish: %v", err)
	}
	if got.Load() != 1 {
		t.Errorf("delivered = %d, want 1", got.Load())
	}
	if reported.Load() != 0 {
		t.Errorf("reporter called %d times, want 0 in ModeOff", reported.Load())
	}
}

func TestPublish_ModeWarn_DeliversAndReports(t *testing.T) {
	var reported atomic.Int32
	var lastErr atomic.Value
	b := New(WithEnforce(ModeWarn), WithInvalidTopicReporter(func(err error) {
		reported.Add(1)
		lastErr.Store(err)
	}))
	defer func() { _ = b.Close(context.Background()) }()

	var got atomic.Int32
	b.Subscribe("bad.topic", func(_ context.Context, _ Event) error {
		got.Add(1)
		return nil
	})

	if err := b.Publish(context.Background(), NewEvent("bad.topic", "src", nil)); err != nil {
		t.Fatalf("publish: %v", err)
	}
	if got.Load() != 1 {
		t.Errorf("delivered = %d, want 1 (warn must still deliver)", got.Load())
	}
	if reported.Load() != 1 {
		t.Errorf("reporter calls = %d, want 1", reported.Load())
	}
	stored, _ := lastErr.Load().(error)
	if !errors.Is(stored, ErrInvalidTopic) {
		t.Errorf("reporter received %v, want ErrInvalidTopic", stored)
	}

	// Valid topic — reporter must not fire.
	b.Subscribe("a.b.c.d", func(_ context.Context, _ Event) error { return nil })
	if err := b.Publish(context.Background(), NewEvent("a.b.c.d", "src", nil)); err != nil {
		t.Fatalf("publish valid: %v", err)
	}
	if reported.Load() != 1 {
		t.Errorf("reporter calls after valid publish = %d, want still 1", reported.Load())
	}
}

func TestPublish_ModeStrict_RejectsInvalid(t *testing.T) {
	var reported atomic.Int32
	b := New(WithEnforce(ModeStrict), WithInvalidTopicReporter(func(error) {
		reported.Add(1)
	}))
	defer func() { _ = b.Close(context.Background()) }()

	var got atomic.Int32
	b.Subscribe("bad.topic", func(_ context.Context, _ Event) error {
		got.Add(1)
		return nil
	})

	err := b.Publish(context.Background(), NewEvent("bad.topic", "src", nil))
	if err == nil {
		t.Fatal("publish: expected error in ModeStrict, got nil")
	}
	if !errors.Is(err, ErrInvalidTopic) {
		t.Errorf("err = %v, want errors.Is ErrInvalidTopic", err)
	}
	if got.Load() != 0 {
		t.Errorf("delivered = %d, want 0 (strict must veto)", got.Load())
	}
	if reported.Load() != 1 {
		t.Errorf("reporter calls = %d, want 1", reported.Load())
	}

	// Valid topic — must succeed.
	b.Subscribe("a.b.c.d", func(_ context.Context, _ Event) error {
		got.Add(1)
		return nil
	})
	if err := b.Publish(context.Background(), NewEvent("a.b.c.d", "src", nil)); err != nil {
		t.Fatalf("publish valid: %v", err)
	}
	if got.Load() != 1 {
		t.Errorf("delivered after valid publish = %d, want 1", got.Load())
	}
}

func TestPublish_AdapterBus_ModeStrict(t *testing.T) {
	// Exercise the adapterBus path (not memBus) by passing an
	// explicit adapter. NewMemoryAdapter is a typed Adapter.
	a := NewMemoryAdapter()
	b := New(WithAdapter(a), WithEnforce(ModeStrict))
	defer func() { _ = b.Close(context.Background()) }()

	err := b.Publish(context.Background(), NewEvent("only.three.parts", "src", nil))
	if !errors.Is(err, ErrInvalidTopic) {
		t.Errorf("adapterBus strict: err = %v, want ErrInvalidTopic", err)
	}
}

func TestPublish_DefaultMode_IsWarn(t *testing.T) {
	// No WithEnforce → default is Warn → invalid topics are
	// delivered (no reporter, so violations silently drop).
	b := New()
	defer func() { _ = b.Close(context.Background()) }()

	var got atomic.Int32
	b.Subscribe("bad.topic", func(_ context.Context, _ Event) error {
		got.Add(1)
		return nil
	})
	if err := b.Publish(context.Background(), NewEvent("bad.topic", "src", nil)); err != nil {
		t.Fatalf("publish: %v", err)
	}
	if got.Load() != 1 {
		t.Errorf("default-mode delivered = %d, want 1", got.Load())
	}
}

func TestWithInvalidTopicReporter_NilIsHandled(t *testing.T) {
	// Constructing a strict bus with a nil reporter must still
	// reject invalid topics without panicking.
	b := New(WithEnforce(ModeStrict), WithInvalidTopicReporter(nil))
	defer func() { _ = b.Close(context.Background()) }()

	err := b.Publish(context.Background(), NewEvent("bad.topic", "src", nil))
	if !errors.Is(err, ErrInvalidTopic) {
		t.Errorf("err = %v, want ErrInvalidTopic", err)
	}
}
