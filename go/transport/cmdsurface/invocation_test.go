package cmdsurface

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestInvocation_JSONRoundTrip(t *testing.T) {
	now := time.Date(2026, 5, 12, 10, 30, 0, 0, time.UTC)
	in := Invocation{
		Path: []string{"widget", "add"},
		Args: []string{"--", "trailing"},
		Flags: map[string]any{
			"name": "thing",
			"qty":  float64(3),
			"dry":  true,
		},
		Meta: Meta{
			Caller:      "user:alice",
			Surface:     SurfaceREST,
			TraceID:     "trace-abc",
			RequestedAt: now,
			Extra:       map[string]string{"request_id": "req-1"},
		},
	}
	b, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var out Invocation
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !reflect.DeepEqual(in, out) {
		t.Fatalf("round-trip mismatch:\n got=%#v\nwant=%#v", out, in)
	}
}

func TestResult_JSONRoundTrip(t *testing.T) {
	in := Result{ExitCode: 1, Stdout: "ok\n", Stderr: "warn\n", Data: map[string]any{"id": "x"}}
	b, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var out Result
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.ExitCode != in.ExitCode || out.Stdout != in.Stdout || out.Stderr != in.Stderr {
		t.Fatalf("plain fields differ: %#v vs %#v", out, in)
	}
	// Data round-trips through generic any → map[string]any.
	gotData, ok := out.Data.(map[string]any)
	if !ok {
		t.Fatalf("Data type: %T", out.Data)
	}
	if gotData["id"] != "x" {
		t.Fatalf("Data round-trip: got %v", gotData)
	}
}

func TestEvent_JSONRoundTrip(t *testing.T) {
	now := time.Date(2026, 5, 12, 11, 0, 0, 0, time.UTC)
	in := Event{Kind: "stdout", Data: "hello", At: now}
	b, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var out Event
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.Kind != in.Kind || out.Data != in.Data || !out.At.Equal(in.At) {
		t.Fatalf("Event round-trip differs: %#v vs %#v", out, in)
	}
}

func TestInvocation_String(t *testing.T) {
	tests := []struct {
		name string
		inv  Invocation
		want []string // substrings that must appear
		skip []string // substrings that must NOT appear
	}{
		{
			name: "full",
			inv: Invocation{
				Path:  []string{"widget", "add"},
				Args:  []string{"a", "b"},
				Flags: map[string]any{"name": "x", "qty": 3},
				Meta:  Meta{Caller: "user:alice", Surface: SurfaceREST, TraceID: "t1"},
			},
			want: []string{
				"rest ", "widget add", "args=[a b]",
				"flags={name=x qty=3}",
				"caller=user:alice", "trace=t1",
			},
		},
		{
			name: "minimal",
			inv:  Invocation{Path: []string{"ping"}, Meta: Meta{Surface: SurfaceLib}},
			want: []string{"lib ping"},
			skip: []string{"flags=", "args=", "caller=", "trace="},
		},
		{
			name: "root_no_surface",
			inv:  Invocation{},
			want: []string{"(root)"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.inv.String()
			for _, s := range tc.want {
				if !strings.Contains(got, s) {
					t.Errorf("missing %q in %q", s, got)
				}
			}
			for _, s := range tc.skip {
				if strings.Contains(got, s) {
					t.Errorf("unexpected %q in %q", s, got)
				}
			}
		})
	}
}

func TestSurface_IsValid(t *testing.T) {
	for _, s := range AllSurfaces() {
		if !s.IsValid() {
			t.Errorf("Surface(%q) should be valid", s)
		}
	}
	if Surface("graphql").IsValid() {
		t.Errorf("unknown surface should not be valid")
	}
}
