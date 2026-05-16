package bus

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestJSONLSink_WritesValidJSONL(t *testing.T) {
	var buf bytes.Buffer
	sink := NewJSONLSink(&buf)

	ts := time.Date(2025, 1, 15, 10, 30, 0, 0, time.UTC)
	events := []Event{
		{Topic: "llm.request", Source: "client", Timestamp: ts, Payload: map[string]string{"model": "gpt-4"}},
		{Topic: "tool.exec", Source: "runner", Timestamp: ts.Add(time.Second), Payload: 42},
	}

	ctx := context.Background()
	for _, e := range events {
		if err := sink.Drain(ctx, e); err != nil {
			t.Fatalf("Drain: %v", err)
		}
	}
	if err := sink.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("want 2 lines, got %d", len(lines))
	}

	for i, line := range lines {
		var rec map[string]any
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			t.Fatalf("line %d: invalid JSON: %v", i, err)
		}
		if _, ok := rec["topic"]; !ok {
			t.Errorf("line %d: missing 'topic' key", i)
		}
		if _, ok := rec["source"]; !ok {
			t.Errorf("line %d: missing 'source' key", i)
		}
		if _, ok := rec["timestamp"]; !ok {
			t.Errorf("line %d: missing 'timestamp' key", i)
		}
	}

	// Verify first line content.
	var first map[string]any
	_ = json.Unmarshal([]byte(lines[0]), &first)
	if first["topic"] != "llm.request" {
		t.Errorf("topic = %v, want llm.request", first["topic"])
	}
	if first["source"] != "client" {
		t.Errorf("source = %v, want client", first["source"])
	}
}

func TestJSONLSinkFile_AppendsDoesNotTruncate(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "events.jsonl")

	ts := time.Date(2025, 1, 15, 10, 30, 0, 0, time.UTC)
	ctx := context.Background()

	// First write session.
	s1, err := NewJSONLSinkFile(path)
	if err != nil {
		t.Fatalf("NewJSONLSinkFile: %v", err)
	}
	if err := s1.Drain(ctx, Event{Topic: "a", Source: "s", Timestamp: ts}); err != nil {
		t.Fatalf("Drain: %v", err)
	}
	if err := s1.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Second write session — must append.
	s2, err := NewJSONLSinkFile(path)
	if err != nil {
		t.Fatalf("NewJSONLSinkFile: %v", err)
	}
	if err := s2.Drain(ctx, Event{Topic: "b", Source: "s", Timestamp: ts}); err != nil {
		t.Fatalf("Drain: %v", err)
	}
	if err := s2.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	data, _ := os.ReadFile(path)
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 2 {
		t.Fatalf("want 2 lines after two sessions, got %d: %s", len(lines), string(data))
	}
}

func TestJSONLSink_CloseFlushes(t *testing.T) {
	var buf bytes.Buffer
	sink := NewJSONLSink(&buf)

	ctx := context.Background()
	ts := time.Now()
	_ = sink.Drain(ctx, Event{Topic: "x", Source: "s", Timestamp: ts})

	// Before close, buffered writer may not have flushed.
	beforeClose := buf.String()

	if err := sink.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	afterClose := buf.String()
	if afterClose == "" {
		t.Fatal("expected data after Close, got empty")
	}
	// If bufio flushed eagerly the lengths match; either way afterClose must
	// contain a complete JSON line.
	if len(afterClose) < len(beforeClose) {
		t.Fatal("Close should not lose data")
	}

	var rec map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(afterClose)), &rec); err != nil {
		t.Fatalf("invalid JSON after flush: %v", err)
	}
}

func TestJSONLSink_NilPayload(t *testing.T) {
	var buf bytes.Buffer
	sink := NewJSONLSink(&buf)

	ctx := context.Background()
	ts := time.Date(2025, 1, 15, 10, 30, 0, 0, time.UTC)
	if err := sink.Drain(ctx, Event{Topic: "t", Source: "s", Timestamp: ts, Payload: nil}); err != nil {
		t.Fatalf("Drain: %v", err)
	}
	if err := sink.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	line := strings.TrimSpace(buf.String())
	var rec map[string]any
	if err := json.Unmarshal([]byte(line), &rec); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	// With omitempty, nil payload should be absent from JSON.
	if _, exists := rec["payload"]; exists {
		t.Errorf("expected payload key to be omitted for nil, got %v", rec["payload"])
	}
}

func TestJSONLSink_DrainAfterClose(t *testing.T) {
	var buf bytes.Buffer
	sink := NewJSONLSink(&buf)
	_ = sink.Close()

	err := sink.Drain(context.Background(), Event{Topic: "t", Source: "s", Timestamp: time.Now()})
	if err == nil {
		t.Fatal("expected error draining after close")
	}
}
