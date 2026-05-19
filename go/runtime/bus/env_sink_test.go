package bus

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestEnvBusSinkJsonl_RoutesEvents pins the documented contract: when
// KIT_BUS_SINK=jsonl and KIT_BUS_SINK_PATH=<file> are both set,
// bus.New() returns a bus whose Publish drains into the file as JSONL.
// Adopters (T-0700 compliance harness, ops sidecars) rely on this
// shape so the bus pkg owns the env contract, not each binary.
func TestEnvBusSinkJsonl_RoutesEvents(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "events.jsonl")
	t.Setenv(EnvSinkKind, "jsonl")
	t.Setenv(EnvSinkPath, path)

	b := New()
	t.Cleanup(func() {
		_ = b.Close(context.Background())
	})

	want := NewEvent("kit.test.thing.recorded", "test", map[string]any{"k": "v"})
	if err := b.Publish(context.Background(), want); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	// Close flushes the JSONL writer (TeeBus.Close → JSONLSink.Close).
	if err := b.Close(context.Background()); err != nil {
		t.Fatalf("Close: %v", err)
	}

	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open jsonl: %v", err)
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	if !sc.Scan() {
		t.Fatalf("jsonl file empty: %s", path)
	}
	line := sc.Text()
	var rec map[string]any
	if err := json.Unmarshal([]byte(line), &rec); err != nil {
		t.Fatalf("unmarshal jsonl line %q: %v", line, err)
	}
	if got := rec["topic"]; got != "kit.test.thing.recorded" {
		t.Errorf("jsonl topic = %v, want kit.test.thing.recorded", got)
	}
	if got := rec["source"]; got != "test" {
		t.Errorf("jsonl source = %v, want test", got)
	}
}

// TestEnvBusSinkUnset_NoExtraSink covers the common case: with no env
// vars set, bus.New() returns the bare in-memory bus (no TeeBus wrap,
// no Sink overhead). Pinned via type-assertion: a TeeBus wrap is
// observable by trying to assert *memBus on the result.
func TestEnvBusSinkUnset_NoExtraSink(t *testing.T) {
	// Explicitly unset in case the test process inherits one.
	t.Setenv(EnvSinkKind, "")
	t.Setenv(EnvSinkPath, "")

	b := New()
	t.Cleanup(func() {
		_ = b.Close(context.Background())
	})

	if _, isTee := b.(*TeeBus); isTee {
		t.Errorf("bus.New() with no env returned *TeeBus; want bare *memBus")
	}
	if _, isMem := b.(*memBus); !isMem {
		t.Errorf("bus.New() with no env returned %T; want *memBus", b)
	}
}

// TestEnvBusSinkJsonl_MissingPath_Skipped covers the operator-mistake
// path: KIT_BUS_SINK=jsonl without KIT_BUS_SINK_PATH must skip the
// sink (no TeeBus wrap) and report the misconfiguration so the warning
// surfaces in logs rather than silently dropping events.
func TestEnvBusSinkJsonl_MissingPath_Skipped(t *testing.T) {
	t.Setenv(EnvSinkKind, "jsonl")
	t.Setenv(EnvSinkPath, "")

	var reported []string
	SetEnvSinkErrReporter(func(err error) {
		reported = append(reported, err.Error())
	})
	t.Cleanup(func() { SetEnvSinkErrReporter(nil) })

	b := New()
	t.Cleanup(func() {
		_ = b.Close(context.Background())
	})

	if _, isTee := b.(*TeeBus); isTee {
		t.Errorf("misconfigured env produced *TeeBus; want bare bus")
	}
	if len(reported) == 0 {
		t.Errorf("missing path: expected a reporter call; got none")
	} else if !strings.Contains(reported[0], EnvSinkPath) {
		t.Errorf("reporter message missing %q: %q", EnvSinkPath, reported[0])
	}
}

// TestEnvBusSinkUnknownKind_Skipped covers the typo path: an unknown
// kind must skip the sink and report — silent acceptance would
// produce a bus that looks healthy but emits nowhere.
func TestEnvBusSinkUnknownKind_Skipped(t *testing.T) {
	t.Setenv(EnvSinkKind, "parquet") // not implemented
	t.Setenv(EnvSinkPath, "/tmp/should-not-be-touched")

	var reported []string
	SetEnvSinkErrReporter(func(err error) {
		reported = append(reported, err.Error())
	})
	t.Cleanup(func() { SetEnvSinkErrReporter(nil) })

	b := New()
	t.Cleanup(func() {
		_ = b.Close(context.Background())
	})

	if _, isTee := b.(*TeeBus); isTee {
		t.Errorf("unknown kind produced *TeeBus; want bare bus")
	}
	if len(reported) == 0 {
		t.Errorf("unknown kind: expected a reporter call; got none")
	} else if !strings.Contains(reported[0], "parquet") {
		t.Errorf("reporter message missing %q: %q", "parquet", reported[0])
	}
}
