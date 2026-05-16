package cmdsurface

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
)

func TestFileSink_DefaultFormat(t *testing.T) {
	var buf bytes.Buffer
	s := &FileSink{W: &buf}
	inv := Invocation{
		Path: []string{"widget", "add"},
		Meta: Meta{Surface: SurfaceREST, TraceID: "tr", Caller: "alice"},
	}
	if err := s.Emit(context.Background(), inv, Result{ExitCode: 2}, errors.New("boom")); err != nil {
		t.Fatalf("Emit err: %v", err)
	}
	line := buf.String()
	if !strings.HasSuffix(line, "\n") {
		t.Fatalf("missing trailing newline: %q", line)
	}
	var rec map[string]any
	if err := json.Unmarshal([]byte(strings.TrimRight(line, "\n")), &rec); err != nil {
		t.Fatalf("not JSON: %v", err)
	}
	if rec["path"] != "widget add" {
		t.Errorf("path=%v", rec["path"])
	}
	if rec["surface"] != "rest" {
		t.Errorf("surface=%v", rec["surface"])
	}
	if v, _ := rec["exit_code"].(float64); v != 2 {
		t.Errorf("exit_code=%v", rec["exit_code"])
	}
	if rec["error"] != "boom" {
		t.Errorf("error=%v", rec["error"])
	}
	if rec["trace_id"] != "tr" {
		t.Errorf("trace_id=%v", rec["trace_id"])
	}
	if rec["caller"] != "alice" {
		t.Errorf("caller=%v", rec["caller"])
	}
	if _, ok := rec["at"]; !ok {
		t.Errorf("at missing")
	}
}

func TestFileSink_OmitsErrorWhenNil(t *testing.T) {
	var buf bytes.Buffer
	s := &FileSink{W: &buf}
	_ = s.Emit(context.Background(), Invocation{Path: []string{"x"}}, Result{}, nil)
	var rec map[string]any
	if err := json.Unmarshal(buf.Bytes(), &rec); err != nil {
		t.Fatalf("not JSON: %v", err)
	}
	if _, ok := rec["error"]; ok {
		t.Errorf("error key should be omitted when err==nil")
	}
}

func TestFileSink_CustomFormatter(t *testing.T) {
	var buf bytes.Buffer
	s := &FileSink{
		W: &buf,
		Format: func(inv Invocation, _ Result, _ error) ([]byte, error) {
			return []byte("PATH=" + joinPath(inv.Path)), nil
		},
	}
	_ = s.Emit(context.Background(), Invocation{Path: []string{"a", "b"}}, Result{}, nil)
	got := buf.String()
	if got != "PATH=a b\n" {
		t.Errorf("custom format wrote %q", got)
	}
}

func TestFileSink_FormatterErrorReturned(t *testing.T) {
	want := errors.New("nope")
	s := &FileSink{
		W:      &bytes.Buffer{},
		Format: func(_ Invocation, _ Result, _ error) ([]byte, error) { return nil, want },
	}
	got := s.Emit(context.Background(), Invocation{}, Result{}, nil)
	if !errors.Is(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

// sinkFakeWriter writes nothing but tracks call count.
type sinkFakeWriter struct {
	mu      sync.Mutex
	chunks  [][]byte
	wantErr error
}

func (w *sinkFakeWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.wantErr != nil {
		return 0, w.wantErr
	}
	c := make([]byte, len(p))
	copy(c, p)
	w.chunks = append(w.chunks, c)
	return len(p), nil
}

func TestFileSink_WriteErrorPropagated(t *testing.T) {
	want := errors.New("disk full")
	s := &FileSink{W: &sinkFakeWriter{wantErr: want}}
	if got := s.Emit(context.Background(), Invocation{}, Result{}, nil); !errors.Is(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestFileSink_ConcurrentWritesDoNotInterleave(t *testing.T) {
	var buf bytes.Buffer
	s := &FileSink{W: &buf}
	const n = 100
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		i := i
		go func() {
			defer wg.Done()
			inv := Invocation{
				Path: []string{"w"},
				Meta: Meta{Surface: SurfaceCLI, TraceID: fmt.Sprintf("t-%03d", i)},
			}
			_ = s.Emit(context.Background(), inv, Result{ExitCode: i}, nil)
		}()
	}
	wg.Wait()

	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	if len(lines) != n {
		t.Fatalf("got %d lines, want %d", len(lines), n)
	}
	seen := map[string]bool{}
	for _, line := range lines {
		var rec map[string]any
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			t.Fatalf("garbled line %q: %v", line, err)
		}
		tr, _ := rec["trace_id"].(string)
		if !strings.HasPrefix(tr, "t-") {
			t.Fatalf("bad trace_id %q", tr)
		}
		if seen[tr] {
			t.Fatalf("dup trace_id %q", tr)
		}
		seen[tr] = true
	}
	if len(seen) != n {
		t.Errorf("got %d unique trace_ids, want %d", len(seen), n)
	}
}
