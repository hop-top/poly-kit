package bus

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// Runs the shared sink corpus in contracts/bus-v1/sinks: the bytes the
// stdout and JSONL sinks write, and the env-sink reporter lines. Set
// UPDATE_SINK_GOLDENS=1 to rewrite the .txt goldens from this runner.

const sinkConfDir = "../../../contracts/bus-v1/sinks"

type sinkConfEvent struct {
	Topic     string          `json:"topic"`
	Source    string          `json:"source"`
	Timestamp string          `json:"timestamp"`
	Payload   json.RawMessage `json:"payload"`
}

// sinkConfValue decodes raw as Go code would build it by hand: integers
// as int64 (or uint64 above that), other numbers as float64.
func sinkConfValue(t *testing.T, raw json.RawMessage) any {
	t.Helper()
	if len(raw) == 0 {
		return nil
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		t.Fatalf("payload %s: %v", raw, err)
	}
	return sinkConfNumbers(t, v)
}

func sinkConfNumbers(t *testing.T, v any) any {
	switch x := v.(type) {
	case json.Number:
		if i, err := strconv.ParseInt(string(x), 10, 64); err == nil {
			return i
		}
		if u, err := strconv.ParseUint(string(x), 10, 64); err == nil {
			return u
		}
		f, err := x.Float64()
		if err != nil {
			t.Fatalf("number %s: %v", x, err)
		}
		return f
	case []any:
		for i := range x {
			x[i] = sinkConfNumbers(t, x[i])
		}
	case map[string]any:
		for k := range x {
			x[k] = sinkConfNumbers(t, x[k])
		}
	}
	return v
}

func sinkConfRead(t *testing.T, name string, into any) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(sinkConfDir, name))
	if err != nil {
		t.Fatal(err)
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(into); err != nil {
		t.Fatalf("%s: %v", name, err)
	}
}

func sinkConfGolden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join(sinkConfDir, name)
	if os.Getenv("UPDATE_SINK_GOLDENS") == "1" {
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got != string(want) {
		t.Errorf("%s drifted\n got: %q\nwant: %q", name, got, want)
	}
}

func TestSinkConformance(t *testing.T) {
	var corpus struct {
		Version     string          `json:"version"`
		Description string          `json:"description"`
		Events      []sinkConfEvent `json:"events"`
	}
	sinkConfRead(t, "events.json", &corpus)
	if corpus.Version != "v1" || len(corpus.Events) == 0 {
		t.Fatalf("events.json: version %q, %d events", corpus.Version, len(corpus.Events))
	}

	var out, jsonl bytes.Buffer
	stdout := NewStdoutSinkWriter(&out)
	js := NewJSONLSink(&jsonl)
	ctx := context.Background()
	for _, c := range corpus.Events {
		ts, err := time.Parse(time.RFC3339Nano, c.Timestamp)
		if err != nil {
			t.Fatalf("%s: %v", c.Topic, err)
		}
		e := Event{Topic: Topic(c.Topic), Source: c.Source, Timestamp: ts, Payload: sinkConfValue(t, c.Payload)}
		if err := stdout.Drain(ctx, e); err != nil {
			t.Fatal(err)
		}
		if err := js.Drain(ctx, e); err != nil {
			t.Fatal(err)
		}
	}
	if err := js.Close(); err != nil {
		t.Fatal(err)
	}
	sinkConfGolden(t, "stdout.txt", out.String())
	sinkConfGolden(t, "jsonl.txt", jsonl.String())

	var cases struct {
		Version     string `json:"version"`
		Description string `json:"description"`
		Cases       []struct {
			Kind string `json:"kind"`
			Path string `json:"path"`
		} `json:"cases"`
	}
	sinkConfRead(t, "env_cases.json", &cases)
	var reports []string
	SetEnvSinkErrReporter(func(err error) { reports = append(reports, err.Error()) })
	t.Cleanup(func() { SetEnvSinkErrReporter(nil) })
	for _, c := range cases.Cases {
		t.Setenv(EnvSinkKind, c.Kind)
		t.Setenv(EnvSinkPath, c.Path)
		if sinks := envSinksFromEnv(); len(sinks) > 0 {
			t.Fatalf("case %q/%q attached a sink", c.Kind, c.Path)
		}
	}
	sinkConfGolden(t, "env_reports.txt", strings.Join(reports, "\n")+"\n")
}
