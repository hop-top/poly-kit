package cmdsurface

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"testing"
)

// sinkCaptureHandler is a slog.Handler that records every Handle
// call so tests can assert on attrs and level.
type sinkCaptureHandler struct {
	mu      sync.Mutex
	records []slog.Record
	min     slog.Level
}

func (h *sinkCaptureHandler) Enabled(_ context.Context, l slog.Level) bool {
	return l >= h.min
}

func (h *sinkCaptureHandler) Handle(_ context.Context, r slog.Record) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.records = append(h.records, r.Clone())
	return nil
}

func (h *sinkCaptureHandler) WithAttrs(_ []slog.Attr) slog.Handler { return h }
func (h *sinkCaptureHandler) WithGroup(_ string) slog.Handler      { return h }

func (h *sinkCaptureHandler) attrsOf(i int) map[string]any {
	h.mu.Lock()
	defer h.mu.Unlock()
	m := map[string]any{}
	h.records[i].Attrs(func(a slog.Attr) bool {
		m[a.Key] = a.Value.Any()
		return true
	})
	return m
}

func TestLogSink_EmitFieldsAtInfo(t *testing.T) {
	h := &sinkCaptureHandler{min: slog.LevelInfo}
	s := &LogSink{Handler: h}
	inv := Invocation{
		Path: []string{"widget", "add"},
		Meta: Meta{
			Surface: SurfaceREST,
			TraceID: "tr-1",
			Caller:  "alice",
		},
	}
	res := Result{ExitCode: 0, Stdout: "skipped", Stderr: "also-skipped"}
	if err := s.Emit(context.Background(), inv, res, nil); err != nil {
		t.Fatalf("Emit error: %v", err)
	}
	if len(h.records) != 1 {
		t.Fatalf("got %d records, want 1", len(h.records))
	}
	a := h.attrsOf(0)
	if a["path"] != "widget add" {
		t.Errorf("path=%v", a["path"])
	}
	if a["surface"] != "rest" {
		t.Errorf("surface=%v", a["surface"])
	}
	if v, ok := a["exit_code"].(int64); !ok || v != 0 {
		t.Errorf("exit_code=%v (%T)", a["exit_code"], a["exit_code"])
	}
	if a["trace_id"] != "tr-1" {
		t.Errorf("trace_id=%v", a["trace_id"])
	}
	if a["caller"] != "alice" {
		t.Errorf("caller=%v", a["caller"])
	}
	if _, ok := a["stdout"]; ok {
		t.Errorf("stdout should be omitted at Info level")
	}
	if _, ok := a["stderr"]; ok {
		t.Errorf("stderr should be omitted at Info level")
	}
	if _, ok := a["error"]; ok {
		t.Errorf("error should be omitted when err==nil")
	}
	if h.records[0].Level != slog.LevelInfo {
		t.Errorf("level=%v want=Info", h.records[0].Level)
	}
}

func TestLogSink_ErrorAttrIncluded(t *testing.T) {
	h := &sinkCaptureHandler{min: slog.LevelInfo}
	s := &LogSink{Handler: h}
	err := errors.New("nope")
	_ = s.Emit(context.Background(), Invocation{Path: []string{"x"}}, Result{ExitCode: 2}, err)
	a := h.attrsOf(0)
	if a["error"] != "nope" {
		t.Errorf("error=%v", a["error"])
	}
	if v, _ := a["exit_code"].(int64); v != 2 {
		t.Errorf("exit_code=%v", a["exit_code"])
	}
}

func TestLogSink_DebugIncludesStdStreams(t *testing.T) {
	h := &sinkCaptureHandler{min: slog.LevelDebug}
	s := &LogSink{Handler: h, Level: slog.LevelDebug}
	inv := Invocation{Path: []string{"x"}, Meta: Meta{Surface: SurfaceCLI}}
	res := Result{Stdout: "hello", Stderr: "warn"}
	_ = s.Emit(context.Background(), inv, res, nil)
	a := h.attrsOf(0)
	if a["stdout"] != "hello" {
		t.Errorf("stdout=%v", a["stdout"])
	}
	if a["stderr"] != "warn" {
		t.Errorf("stderr=%v", a["stderr"])
	}
	if h.records[0].Level != slog.LevelDebug {
		t.Errorf("level=%v want=Debug", h.records[0].Level)
	}
}

func TestLogSink_DisabledLevelSkipped(t *testing.T) {
	h := &sinkCaptureHandler{min: slog.LevelWarn}
	s := &LogSink{Handler: h, Level: slog.LevelInfo}
	if err := s.Emit(context.Background(), Invocation{Path: []string{"x"}}, Result{}, nil); err != nil {
		t.Fatalf("Emit err: %v", err)
	}
	if len(h.records) != 0 {
		t.Errorf("expected 0 records, got %d", len(h.records))
	}
}

func TestLogSink_NilHandlerFallsBackToDefault(t *testing.T) {
	// We don't observe the global default to avoid global state in
	// tests; we just make sure the call does not panic.
	s := &LogSink{}
	if err := s.Emit(context.Background(), Invocation{Path: []string{"x"}}, Result{}, nil); err != nil {
		t.Fatalf("Emit err: %v", err)
	}
}
