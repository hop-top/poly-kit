package bus

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"
)

var testTime = time.Date(2025, 3, 15, 10, 30, 45, 0, time.UTC)

func TestStdoutSink_OutputFormat(t *testing.T) {
	var buf bytes.Buffer
	sink := NewStdoutSinkWriter(&buf)

	e := Event{
		Topic:     "llm.request",
		Source:    "llm.client",
		Timestamp: testTime,
		Payload:   "hello world",
	}

	if err := sink.Drain(context.Background(), e); err != nil {
		t.Fatalf("Drain: %v", err)
	}

	want := "[2025-03-15T10:30:45] llm.request llm.client: hello world\n"
	if got := buf.String(); got != want {
		t.Errorf("output mismatch\n got: %q\nwant: %q", got, want)
	}
}

func TestStdoutSink_PayloadTruncation(t *testing.T) {
	var buf bytes.Buffer
	sink := NewStdoutSinkWriter(&buf)

	long := strings.Repeat("x", 200)
	e := Event{
		Topic:     "big.payload",
		Source:    "test",
		Timestamp: testTime,
		Payload:   long,
	}

	if err := sink.Drain(context.Background(), e); err != nil {
		t.Fatalf("Drain: %v", err)
	}

	out := buf.String()
	// Payload portion should be 120 chars + "..."
	wantSuffix := strings.Repeat("x", 120) + "...\n"
	if !strings.HasSuffix(out, wantSuffix) {
		t.Errorf("expected truncated payload ending with '...', got: %q", out)
	}
}

func TestStdoutSink_NilPayload(t *testing.T) {
	var buf bytes.Buffer
	sink := NewStdoutSinkWriter(&buf)

	e := Event{
		Topic:     "test.nil",
		Source:    "src",
		Timestamp: testTime,
		Payload:   nil,
	}

	if err := sink.Drain(context.Background(), e); err != nil {
		t.Fatalf("Drain: %v", err)
	}

	want := "[2025-03-15T10:30:45] test.nil src: <nil>\n"
	if got := buf.String(); got != want {
		t.Errorf("nil payload mismatch\n got: %q\nwant: %q", got, want)
	}
}

func TestStdoutSink_EmptySource(t *testing.T) {
	var buf bytes.Buffer
	sink := NewStdoutSinkWriter(&buf)

	e := Event{
		Topic:     "test.empty",
		Source:    "",
		Timestamp: testTime,
		Payload:   42,
	}

	if err := sink.Drain(context.Background(), e); err != nil {
		t.Fatalf("Drain: %v", err)
	}

	want := "[2025-03-15T10:30:45] test.empty : 42\n"
	if got := buf.String(); got != want {
		t.Errorf("empty source mismatch\n got: %q\nwant: %q", got, want)
	}
}

func TestStdoutSink_CloseNoop(t *testing.T) {
	sink := NewStdoutSink()
	if err := sink.Close(); err != nil {
		t.Fatalf("Close returned error: %v", err)
	}
}
