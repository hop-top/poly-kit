package bus

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

const streamConfDir = "../../../contracts/bus-v1/streams"

var streamConfFiles = map[string]bool{
	"offsets.json":      true,
	"patterns.json":     true,
	"handshake.json":    true,
	"control.json":      true,
	"filter.json":       true,
	"resume.json":       true,
	"cursor.json":       true,
	"inbound.json":      true,
	"outbox.json":       true,
	"event_frames.json": true,
}

// streamConfLoad decodes a vector file strictly: unknown fields and a
// version other than v1 fail the run.
func streamConfLoad(t *testing.T, name string, v any) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(filepath.FromSlash(streamConfDir), name))
	if err != nil {
		t.Fatal(err)
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	var h ceConfHeader
	_ = json.Unmarshal(raw, &h)
	if h.Version != "v1" || h.Description == "" {
		t.Fatalf("%s: version %q, want v1 with a description", name, h.Version)
	}
}

func TestStreamConformanceAllFilesConsumed(t *testing.T) {
	dir := filepath.FromSlash(streamConfDir)
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		seen[e.Name()] = true
		if !streamConfFiles[e.Name()] {
			t.Errorf("%s has no runner", e.Name())
		}
	}
	for n := range streamConfFiles {
		if !seen[n] {
			t.Errorf("%s registered but missing from the corpus", n)
		}
	}
	wire, err := os.ReadDir(filepath.Join(dir, "wire"))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range wire {
		if e.Name() == "README.md" {
			continue
		}
		if filepath.Ext(e.Name()) != ".jsonl" {
			t.Errorf("wire/%s: only .jsonl transcripts belong here", e.Name())
		} else if !streamWireFiles[e.Name()] {
			t.Errorf("wire/%s has no runner", e.Name())
		}
	}
}

// confOffset renders an optional offset for comparison with vectors.
func confOffset(ok bool, v uint64) *string {
	if !ok {
		return nil
	}
	s := formatOffset(v)
	return &s
}

func mustOffset(t *testing.T, s string) uint64 {
	t.Helper()
	v, ok := parseOffset(s)
	if !ok {
		t.Fatalf("vector offset %q is invalid", s)
	}
	return v
}

func TestStreamConformanceOffsets(t *testing.T) {
	var f struct {
		ceConfHeader
		Cases []struct {
			Name  string          `json:"name"`
			Token json.RawMessage `json:"token"`
			OK    bool            `json:"ok"`
			Value string          `json:"value"`
		} `json:"cases"`
	}
	streamConfLoad(t, "offsets.json", &f)
	for _, tc := range f.Cases {
		t.Run(tc.Name, func(t *testing.T) {
			v, ok := parseOffsetToken(tc.Token)
			if ok != tc.OK {
				t.Fatalf("ok = %v, want %v", ok, tc.OK)
			}
			if ok && formatOffset(v) != tc.Value {
				t.Errorf("value = %s, want %s", formatOffset(v), tc.Value)
			}
		})
	}
}

func TestStreamConformancePatterns(t *testing.T) {
	var f struct {
		ceConfHeader
		Cases []struct {
			Name    string `json:"name"`
			Pattern string `json:"pattern"`
			OK      bool   `json:"ok"`
		} `json:"cases"`
	}
	streamConfLoad(t, "patterns.json", &f)
	for _, tc := range f.Cases {
		t.Run(tc.Name, func(t *testing.T) {
			err := validatePattern(tc.Pattern)
			if (err == nil) != tc.OK {
				t.Fatalf("validatePattern(%q) = %v, want ok %v", tc.Pattern, err, tc.OK)
			}
			if err != nil && err.Code != CodeInvalidPattern {
				t.Errorf("code = %s, want %s", err.Code, CodeInvalidPattern)
			}
		})
	}
}

// confSub is a subscription state in vector form; Patterns nil is the
// implicit "#".
type confSub struct {
	Patterns *[]string `json:"patterns"`
	Scope    *Scope    `json:"scope"`
}

func (c confSub) state() subState {
	s := newSubState()
	if c.Patterns != nil {
		s = s.withPatterns(*c.Patterns)
	}
	s.Scope = c.Scope
	return s
}

func confSubOf(s subState) confSub {
	out := confSub{Scope: s.Scope}
	if !s.Implicit {
		p := s.sortedPatterns()
		out.Patterns = &p
	}
	return out
}

func TestStreamConformanceControl(t *testing.T) {
	type req struct {
		Type     string    `json:"type"`
		ID       string    `json:"id"`
		Patterns *[]string `json:"patterns"`
		Scope    *Scope    `json:"scope"`
	}
	var f struct {
		ceConfHeader
		Classify []struct {
			Name       string `json:"name"`
			Frame      string `json:"frame"`
			StreamLink bool   `json:"stream_link"`
			Kind       string `json:"kind"`
		} `json:"classify"`
		Acceptor []struct {
			Name    string `json:"name"`
			Frame   string `json:"frame"`
			Outcome string `json:"outcome"`
			Request *req   `json:"request"`
			Reply   *struct {
				ID   string `json:"id"`
				Code string `json:"code"`
			} `json:"reply"`
		} `json:"acceptor"`
		Dialer []struct {
			Name    string `json:"name"`
			Frame   string `json:"frame"`
			Outcome string `json:"outcome"`
			Kind    string `json:"kind"`
			Ack     *struct {
				ID string `json:"id"`
				At string `json:"at"`
			} `json:"ack"`
			Error *struct {
				ID   string `json:"id"`
				Code string `json:"code"`
			} `json:"error"`
			Reset *struct {
				Stream string `json:"stream"`
				Reason string `json:"reason"`
				After  string `json:"after"`
				Head   string `json:"head"`
			} `json:"reset"`
			Position *struct {
				Stream string `json:"stream"`
				Offset string `json:"offset"`
			} `json:"position"`
		} `json:"dialer"`
		Apply []struct {
			Name      string   `json:"name"`
			State     confSub  `json:"state"`
			Request   req      `json:"request"`
			Max       int      `json:"max"`
			OK        bool     `json:"ok"`
			Code      string   `json:"code"`
			After     *confSub `json:"after"`
			Effective *struct {
				Patterns []string `json:"patterns"`
				Scope    *Scope   `json:"scope"`
			} `json:"effective"`
		} `json:"apply"`
	}
	streamConfLoad(t, "control.json", &f)

	for _, tc := range f.Classify {
		t.Run("classify/"+tc.Name, func(t *testing.T) {
			kind, _ := classifyFrame([]byte(tc.Frame), tc.StreamLink)
			if kind != tc.Kind {
				t.Errorf("kind = %s, want %s", kind, tc.Kind)
			}
		})
	}
	for _, tc := range f.Acceptor {
		t.Run("acceptor/"+tc.Name, func(t *testing.T) {
			kind, members := classifyFrame([]byte(tc.Frame), true)
			if kind != frameKindControl {
				t.Fatalf("vector frame is not a control frame (%s)", kind)
			}
			r, reply := acceptorControl(members)
			switch tc.Outcome {
			case "request":
				if r == nil || reply != nil || tc.Request == nil {
					t.Fatalf("got request %+v reply %+v, want request", r, reply)
				}
				got := req{Type: r.Type, ID: r.ID, Scope: r.Scope}
				if r.Patterns != nil {
					p := r.Patterns
					got.Patterns = &p
				}
				if !reflect.DeepEqual(got, *tc.Request) {
					t.Errorf("request\n got  %+v\n want %+v", got, *tc.Request)
				}
			case "reply":
				if reply == nil || r != nil || tc.Reply == nil {
					t.Fatalf("got request %+v reply %+v, want reply", r, reply)
				}
				if reply.ID != tc.Reply.ID || reply.Code != tc.Reply.Code {
					t.Errorf("reply = %s/%s, want %s/%s", reply.ID, reply.Code, tc.Reply.ID, tc.Reply.Code)
				}
			case "ignore":
				if r != nil || reply != nil {
					t.Fatalf("got request %+v reply %+v, want ignore", r, reply)
				}
			default:
				t.Fatalf("vector outcome %q", tc.Outcome)
			}
		})
	}
	for _, tc := range f.Dialer {
		t.Run("dialer/"+tc.Name, func(t *testing.T) {
			kind, members := classifyFrame([]byte(tc.Frame), true)
			if kind != frameKindControl {
				t.Fatalf("vector frame is not a control frame (%s)", kind)
			}
			c, err := dialerControl(members)
			if tc.Outcome == "protocol_error" {
				if tc.Kind == "" || protocolKind(err) != tc.Kind {
					t.Fatalf("got %+v, %v, want protocol error kind %q", c, err, tc.Kind)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if c.Kind != tc.Outcome {
				t.Fatalf("kind = %s, want %s", c.Kind, tc.Outcome)
			}
			switch tc.Outcome {
			case "ack":
				if c.ID != tc.Ack.ID || formatOffset(c.At) != tc.Ack.At {
					t.Errorf("ack = %s/%d", c.ID, c.At)
				}
			case "error":
				if c.ID != tc.Error.ID || c.Code != tc.Error.Code {
					t.Errorf("error = %s/%s", c.ID, c.Code)
				}
			case "reset":
				r := c.Reset
				if r.Stream != tc.Reset.Stream || r.Reason != tc.Reset.Reason ||
					formatOffset(r.After) != tc.Reset.After || formatOffset(r.Head) != tc.Reset.Head {
					t.Errorf("reset = %+v", r)
				}
			case "position":
				p := c.Position
				if p.Stream != tc.Position.Stream || formatOffset(p.Offset) != tc.Position.Offset {
					t.Errorf("position = %+v", p)
				}
			}
		})
	}
	for _, tc := range f.Apply {
		t.Run("apply/"+tc.Name, func(t *testing.T) {
			r := ctlRequest{Type: tc.Request.Type, Scope: tc.Request.Scope}
			if tc.Request.Patterns != nil {
				r.Patterns = *tc.Request.Patterns
			}
			got, err := tc.State.state().apply(r, tc.Max)
			if !tc.OK {
				if err == nil || err.Code != tc.Code {
					t.Fatalf("err = %v, want code %s", err, tc.Code)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(confSubOf(got), *tc.After) {
				t.Errorf("state\n got  %+v\n want %+v", confSubOf(got), *tc.After)
			}
			eff := got.effective()
			if eff.Patterns == nil {
				eff.Patterns = []string{}
			}
			if !reflect.DeepEqual(eff.Patterns, tc.Effective.Patterns) || !reflect.DeepEqual(eff.Scope, tc.Effective.Scope) {
				t.Errorf("effective\n got  %+v\n want %+v", eff, *tc.Effective)
			}
		})
	}
}

func TestStreamConformanceFilter(t *testing.T) {
	var f struct {
		ceConfHeader
		Cases []struct {
			Name         string  `json:"name"`
			Subscription confSub `json:"subscription"`
			PeerOrigin   string  `json:"peer_origin"`
			Entry        struct {
				Marker      bool   `json:"marker"`
				Origin      string `json:"origin"`
				Topic       string `json:"topic"`
				WorkspaceID string `json:"workspace_id"`
			} `json:"entry"`
			Deliver bool `json:"deliver"`
		} `json:"cases"`
	}
	streamConfLoad(t, "filter.json", &f)
	for _, tc := range f.Cases {
		t.Run(tc.Name, func(t *testing.T) {
			e := LogEntry{
				Marker: tc.Entry.Marker, Origin: tc.Entry.Origin,
				Event: Event{Topic: Topic(tc.Entry.Topic), WorkspaceID: tc.Entry.WorkspaceID},
			}
			if got := entryDelivers(tc.Subscription.state(), tc.PeerOrigin, e); got != tc.Deliver {
				t.Errorf("deliver = %v, want %v", got, tc.Deliver)
			}
		})
	}
}

func TestStreamConformanceResume(t *testing.T) {
	type outcome struct {
		Status string `json:"status"`
		Reason string `json:"reason,omitempty"`
		After  string `json:"after"`
		Head   string `json:"head"`
	}
	var f struct {
		ceConfHeader
		Cases []struct {
			Name string `json:"name"`
			Log  struct {
				Stream string `json:"stream"`
				Oldest string `json:"oldest"`
				Head   string `json:"head"`
			} `json:"log"`
			Request *struct {
				Stream string `json:"stream"`
				After  string `json:"after"`
			} `json:"request"`
			Outcome *outcome `json:"outcome"`
			Cursor  string   `json:"cursor"`
		} `json:"cases"`
	}
	streamConfLoad(t, "resume.json", &f)
	for _, tc := range f.Cases {
		t.Run(tc.Name, func(t *testing.T) {
			var r *resumeReq
			if tc.Request != nil {
				r = &resumeReq{Stream: tc.Request.Stream, After: mustOffset(t, tc.Request.After)}
			}
			ro, cursor := decideResume(tc.Log.Stream, mustOffset(t, tc.Log.Oldest), mustOffset(t, tc.Log.Head), r)
			if formatOffset(cursor) != tc.Cursor {
				t.Errorf("cursor = %d, want %s", cursor, tc.Cursor)
			}
			var got *outcome
			if ro != nil {
				got = &outcome{Status: ro.Status, Reason: ro.Reason, After: formatOffset(ro.After), Head: formatOffset(ro.Head)}
			}
			if !reflect.DeepEqual(got, tc.Outcome) {
				t.Errorf("outcome\n got  %+v\n want %+v", got, tc.Outcome)
			}
		})
	}
}
