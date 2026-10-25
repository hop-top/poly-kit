package bus

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sort"
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
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if len(wire) != len(streamWireFiles) {
		t.Errorf("wire: %d transcripts, %d runners", len(wire), len(streamWireFiles))
	}
	for _, e := range wire {
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
				if err == nil {
					t.Fatalf("got %+v, want protocol error", c)
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

type confGap struct {
	Reason string `json:"reason"`
	Stream string `json:"stream"`
	After  string `json:"after"`
	Head   string `json:"head"`
}

func confGapOf(g *gapInfo) *confGap {
	if g == nil {
		return nil
	}
	p := g.payload("")
	return &confGap{Reason: p["reason"].(string), Stream: p["stream"].(string), After: p["after"].(string), Head: p["head"].(string)}
}

type confPos struct {
	Stream string `json:"stream"`
	Offset string `json:"offset"`
}

func TestStreamConformanceCursor(t *testing.T) {
	var f struct {
		ceConfHeader
		Cases []struct {
			Name      string           `json:"name"`
			Seed      *confPos         `json:"seed"`
			Reconnect bool             `json:"reconnect"`
			Link      string           `json:"link"`
			Ack       *json.RawMessage `json:"ack"`
			Frames    []struct {
				Event    json.RawMessage `json:"event"`
				Reset    json.RawMessage `json:"reset"`
				Position json.RawMessage `json:"position"`
			} `json:"frames"`
			Expect struct {
				LinkUp struct {
					Resume      string   `json:"resume"`
					Invalidated *confGap `json:"invalidated"`
				} `json:"link_up"`
				Frames   []json.RawMessage `json:"frames"`
				Position *confPos          `json:"position"`
			} `json:"expect"`
		} `json:"cases"`
	}
	streamConfLoad(t, "cursor.json", &f)
	for _, tc := range f.Cases {
		t.Run(tc.Name, func(t *testing.T) {
			var c dialerCursor
			if tc.Seed != nil {
				c = dialerCursor{Stream: tc.Seed.Stream, Offset: mustOffset(t, tc.Seed.Offset), Held: true}
			}
			var ack *ackFrame
			switch tc.Link {
			case "stream":
				// The vector's ack is the reply body minus type/caps; wrap it
				// into a real extended ack and decode it.
				var body map[string]json.RawMessage
				if err := json.Unmarshal(*tc.Ack, &body); err != nil {
					t.Fatal(err)
				}
				var si map[string]any
				_ = json.Unmarshal(body["stream"], &si)
				si["heartbeat_ms"] = 1000
				frame := map[string]any{"type": "auth_ok", "caps": map[string]any{"streams": 1, "frames": []string{"kit"}}, "stream": si}
				if r, ok := body["resume"]; ok {
					frame["session"] = map[string]json.RawMessage{"resume": r}
				}
				raw, _ := json.Marshal(frame)
				a, err := parseAck(raw)
				if err != nil {
					t.Fatalf("ack: %v", err)
				}
				ack = &a
			case "legacy":
			default:
				t.Fatalf("vector link %q", tc.Link)
			}
			resume, gap := c.linkUp(ack, tc.Reconnect)
			if resume != tc.Expect.LinkUp.Resume {
				t.Errorf("resume = %s, want %s", resume, tc.Expect.LinkUp.Resume)
			}
			if got := confGapOf(gap); !reflect.DeepEqual(got, tc.Expect.LinkUp.Invalidated) {
				t.Errorf("link-up invalidated\n got  %+v\n want %+v", got, tc.Expect.LinkUp.Invalidated)
			}
			if len(tc.Frames) != len(tc.Expect.Frames) {
				t.Fatal("vector: frames and expectations differ in length")
			}
			for i, fr := range tc.Frames {
				var got any
				switch {
				case len(fr.Event) > 0:
					deliver, err := c.onEventOffset(fr.Event)
					switch {
					case err != nil:
						got = "protocol_error"
					case deliver:
						got = "deliver"
					default:
						got = "drop"
					}
				case len(fr.Reset) > 0:
					members := map[string]json.RawMessage{}
					_ = json.Unmarshal(fr.Reset, &members)
					members["type"] = json.RawMessage(`"reset"`)
					dc, err := dialerControl(members)
					if err == nil {
						var g *gapInfo
						g, err = c.onReset(dc.Reset)
						if err == nil {
							got = map[string]any{"invalidated": confGapOf(g)}
						}
					}
					if err != nil {
						got = "protocol_error"
					}
				case len(fr.Position) > 0:
					members := map[string]json.RawMessage{}
					_ = json.Unmarshal(fr.Position, &members)
					members["type"] = json.RawMessage(`"position"`)
					dc, err := dialerControl(members)
					if err == nil {
						err = c.onPosition(dc.Position)
					}
					got = "ok"
					if err != nil {
						got = "protocol_error"
					}
				default:
					t.Fatal("vector: empty frame")
				}
				gotJSON, _ := json.Marshal(got)
				assertJSONEqual(t, gotJSON, tc.Expect.Frames[i])
			}
			var pos *confPos
			if c.Held {
				pos = &confPos{Stream: c.Stream, Offset: formatOffset(c.Offset)}
			}
			if !reflect.DeepEqual(pos, tc.Expect.Position) {
				t.Errorf("position\n got  %+v\n want %+v", pos, tc.Expect.Position)
			}
		})
	}
}

func TestStreamConformanceInbound(t *testing.T) {
	var f struct {
		ceConfHeader
		Cases []struct {
			Name     string `json:"name"`
			After    string `json:"after"`
			Capacity int    `json:"capacity"`
			Ops      []struct {
				Recv    *string `json:"recv"`
				Deliver *int    `json:"deliver"`
			} `json:"ops"`
			Delivered   []string `json:"delivered"`
			Overflow    bool     `json:"overflow"`
			ResumeAfter string   `json:"resume_after"`
		} `json:"cases"`
	}
	streamConfLoad(t, "inbound.json", &f)
	for _, tc := range f.Cases {
		t.Run(tc.Name, func(t *testing.T) {
			q := newInboundQueue(tc.Capacity, mustOffset(t, tc.After))
			delivered := []string{}
			for _, op := range tc.Ops {
				switch {
				case op.Recv != nil:
					q.recv(mustOffset(t, *op.Recv))
				case op.Deliver != nil:
					for _, o := range q.deliver(*op.Deliver) {
						delivered = append(delivered, formatOffset(o))
					}
				}
			}
			if !reflect.DeepEqual(delivered, tc.Delivered) {
				t.Errorf("delivered = %v, want %v", delivered, tc.Delivered)
			}
			if q.overflowed != tc.Overflow {
				t.Errorf("overflow = %v, want %v", q.overflowed, tc.Overflow)
			}
			if formatOffset(q.resumeAfter()) != tc.ResumeAfter {
				t.Errorf("resume after = %d, want %s", q.resumeAfter(), tc.ResumeAfter)
			}
		})
	}
}

func TestStreamConformanceOutbox(t *testing.T) {
	var f struct {
		ceConfHeader
		Cases []struct {
			Name     string `json:"name"`
			Capacity int    `json:"capacity"`
			Outbox   bool   `json:"outbox"`
			Ops      []struct {
				Publish    *string `json:"publish"`
				Connect    bool    `json:"connect"`
				Disconnect bool    `json:"disconnect"`
			} `json:"ops"`
			Sent    []string `json:"sent"`
			Dropped uint64   `json:"dropped"`
		} `json:"cases"`
	}
	streamConfLoad(t, "outbox.json", &f)
	for _, tc := range f.Cases {
		t.Run(tc.Name, func(t *testing.T) {
			log := NewRingLog("S", tc.Capacity)
			ob := newSendCursor(log, tc.Outbox)
			sent := []string{}
			var dropped uint64
			connected := false
			drain := func() {
				entries, gap := ob.next(log, 1<<20)
				dropped += gap
				for _, e := range entries {
					sent = append(sent, string(e.Event.Topic))
				}
			}
			for _, op := range tc.Ops {
				switch {
				case op.Publish != nil:
					log.Append(LogEntry{Event: Event{Topic: Topic(*op.Publish)}})
					if connected {
						drain()
					}
				case op.Connect:
					connected = true
					ob.linkUp(log)
					drain()
				case op.Disconnect:
					connected = false
				}
			}
			if !reflect.DeepEqual(sent, tc.Sent) {
				t.Errorf("sent = %v, want %v", sent, tc.Sent)
			}
			if dropped != tc.Dropped {
				t.Errorf("dropped = %d, want %d", dropped, tc.Dropped)
			}
		})
	}
}

func TestStreamConformanceHandshake(t *testing.T) {
	type caps struct {
		Streams int      `json:"streams"`
		Frames  []string `json:"frames"`
	}
	type session struct {
		Subscribe *[]string `json:"subscribe"`
		Scope     *Scope    `json:"scope"`
		Resume    *struct {
			Stream string `json:"stream"`
			After  string `json:"after"`
		} `json:"resume"`
	}
	type stream struct {
		ID          string `json:"id"`
		Oldest      string `json:"oldest"`
		Head        string `json:"head"`
		HeartbeatMs int64  `json:"heartbeat_ms"`
	}
	type outcome struct {
		Status string `json:"status"`
		Reason string `json:"reason,omitempty"`
		After  string `json:"after"`
		Head   string `json:"head"`
	}
	var f struct {
		ceConfHeader
		Auth []struct {
			Name    string   `json:"name"`
			Frame   string   `json:"frame"`
			OK      bool     `json:"ok"`
			Token   string   `json:"token"`
			Caps    *caps    `json:"caps"`
			Origin  string   `json:"origin"`
			Session *session `json:"session"`
			Error   string   `json:"error"`
		} `json:"auth"`
		Ack []struct {
			Name   string   `json:"name"`
			Frame  string   `json:"frame"`
			OK     bool     `json:"ok"`
			Kind   string   `json:"kind"`
			Caps   *caps    `json:"caps"`
			Origin string   `json:"origin"`
			Stream *stream  `json:"stream"`
			Resume *outcome `json:"resume"`
			Error  *struct {
				Code string `json:"code"`
			} `json:"error"`
		} `json:"ack"`
		Negotiate []struct {
			Name       string `json:"name"`
			Mine       caps   `json:"mine"`
			Peer       *caps  `json:"peer"`
			Handshake  bool   `json:"handshake"`
			Configured string `json:"configured"`
			Streams    int    `json:"streams"`
			Format     string `json:"format"`
		} `json:"negotiate"`
		AckEncode []struct {
			Name       string          `json:"name"`
			DialerCaps bool            `json:"dialer_caps"`
			Caps       caps            `json:"caps"`
			Origin     string          `json:"origin"`
			Stream     *stream         `json:"stream"`
			Resume     *outcome        `json:"resume"`
			Frame      json.RawMessage `json:"frame"`
		} `json:"ack_encode"`
	}
	streamConfLoad(t, "handshake.json", &f)
	capsOf := func(c *streamCaps) *caps {
		if c == nil {
			return nil
		}
		fr := c.Frames
		if fr == nil {
			fr = []string{}
		}
		return &caps{Streams: c.Streams, Frames: fr}
	}
	for _, tc := range f.Auth {
		t.Run("auth/"+tc.Name, func(t *testing.T) {
			a, err := parseAuthFrame([]byte(tc.Frame))
			if !tc.OK {
				if err == nil || err.Code != tc.Error {
					t.Fatalf("err = %v, want %s", err, tc.Error)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if a.Token != tc.Token || a.Origin != tc.Origin {
				t.Errorf("token/origin = %q/%q", a.Token, a.Origin)
			}
			if got := capsOf(a.Caps); !reflect.DeepEqual(got, tc.Caps) {
				t.Errorf("caps\n got  %+v\n want %+v", got, tc.Caps)
			}
			var got *session
			if a.Session != nil {
				got = &session{Scope: a.Session.Scope}
				if a.Session.Subscribe != nil {
					s := a.Session.Subscribe
					got.Subscribe = &s
				}
				if r := a.Session.Resume; r != nil {
					got.Resume = &struct {
						Stream string `json:"stream"`
						After  string `json:"after"`
					}{r.Stream, formatOffset(r.After)}
				}
			}
			if !reflect.DeepEqual(got, tc.Session) {
				g, _ := json.Marshal(got)
				w, _ := json.Marshal(tc.Session)
				t.Errorf("session\n got  %s\n want %s", g, w)
			}
		})
	}
	for _, tc := range f.Ack {
		t.Run("ack/"+tc.Name, func(t *testing.T) {
			a, err := parseAck([]byte(tc.Frame))
			if !tc.OK {
				if err == nil {
					t.Fatalf("got %+v, want failure", a)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if a.Kind != tc.Kind {
				t.Fatalf("kind = %s, want %s", a.Kind, tc.Kind)
			}
			if tc.Kind == "session_error" {
				if a.Err == nil || a.Err.Code != tc.Error.Code {
					t.Errorf("session error = %+v", a.Err)
				}
				return
			}
			if got := capsOf(a.Caps); !reflect.DeepEqual(got, tc.Caps) {
				t.Errorf("caps = %+v, want %+v", got, tc.Caps)
			}
			if a.Origin != tc.Origin {
				t.Errorf("origin = %q", a.Origin)
			}
			var st *stream
			if a.Stream != nil {
				st = &stream{a.Stream.ID, formatOffset(a.Stream.Oldest), formatOffset(a.Stream.Head), a.Stream.HeartbeatMs}
			}
			if !reflect.DeepEqual(st, tc.Stream) {
				t.Errorf("stream = %+v, want %+v", st, tc.Stream)
			}
			var ro *outcome
			if a.Resume != nil {
				ro = &outcome{a.Resume.Status, a.Resume.Reason, formatOffset(a.Resume.After), formatOffset(a.Resume.Head)}
			}
			if !reflect.DeepEqual(ro, tc.Resume) {
				t.Errorf("resume = %+v, want %+v", ro, tc.Resume)
			}
		})
	}
	formats := map[string]FrameFormat{"kit": FrameKit, "cloudevents": FrameCloudEvents}
	for _, tc := range f.Negotiate {
		t.Run("negotiate/"+tc.Name, func(t *testing.T) {
			var peer *streamCaps
			if tc.Peer != nil {
				peer = &streamCaps{Streams: tc.Peer.Streams, Frames: tc.Peer.Frames}
			}
			v, ff := negotiate(streamCaps{Streams: tc.Mine.Streams, Frames: tc.Mine.Frames}, peer, tc.Handshake, formats[tc.Configured])
			if v != tc.Streams || ff.String() != tc.Format {
				t.Errorf("negotiate = %d/%s, want %d/%s", v, ff, tc.Streams, tc.Format)
			}
		})
	}
	for _, tc := range f.AckEncode {
		t.Run("ack_encode/"+tc.Name, func(t *testing.T) {
			var si *streamInfo
			if tc.Stream != nil {
				si = &streamInfo{ID: tc.Stream.ID, Oldest: mustOffset(t, tc.Stream.Oldest), Head: mustOffset(t, tc.Stream.Head), HeartbeatMs: tc.Stream.HeartbeatMs}
			}
			var ro *resumeOutcome
			if tc.Resume != nil {
				ro = &resumeOutcome{Status: tc.Resume.Status, Reason: tc.Resume.Reason, After: mustOffset(t, tc.Resume.After), Head: mustOffset(t, tc.Resume.Head)}
			}
			out := encodeAck(tc.DialerCaps, streamCaps{Streams: tc.Caps.Streams, Frames: tc.Caps.Frames}, tc.Origin, si, ro)
			assertJSONEqual(t, out, tc.Frame)
			if !tc.DialerCaps && string(out) != `{"type":"auth_ok"}` {
				t.Errorf("plain ack bytes = %s", out)
			}
		})
	}
}

func TestStreamConformanceEventFrames(t *testing.T) {
	var f struct {
		ceConfHeader
		Encode []struct {
			Name   string          `json:"name"`
			Format string          `json:"format"`
			Origin string          `json:"origin"`
			Offset *string         `json:"offset"`
			Event  ceConfEvent     `json:"event"`
			Frame  json.RawMessage `json:"frame"`
		} `json:"encode"`
		Decode []struct {
			Name       string       `json:"name"`
			Frame      string       `json:"frame"`
			StreamLink bool         `json:"stream_link"`
			Outcome    string       `json:"outcome"`
			Origin     *string      `json:"origin"`
			Offset     *string      `json:"offset"`
			Event      *ceConfEvent `json:"event"`
		} `json:"decode"`
	}
	streamConfLoad(t, "event_frames.json", &f)
	formats := map[string]FrameFormat{"kit": FrameKit, "cloudevents": FrameCloudEvents}
	for _, tc := range f.Encode {
		t.Run("encode/"+tc.Name, func(t *testing.T) {
			var off uint64
			if tc.Offset != nil {
				off = mustOffset(t, *tc.Offset)
			}
			out, err := encodeFrameOffset(formats[tc.Format], tc.Event.event(t), tc.Origin, off, tc.Offset != nil)
			if err != nil {
				t.Fatal(err)
			}
			assertJSONEqual(t, out, tc.Frame)
		})
	}
	for _, tc := range f.Decode {
		t.Run("decode/"+tc.Name, func(t *testing.T) {
			e, origin, off, err := decodeEventFrame([]byte(tc.Frame), tc.StreamLink)
			var got string
			switch {
			case err == nil:
				got = "event"
			case isProtocolError(err):
				got = "protocol_error"
			default:
				got = "drop"
			}
			if got != tc.Outcome {
				t.Fatalf("outcome = %s (%v), want %s", got, err, tc.Outcome)
			}
			if got != "event" {
				return
			}
			if tc.Origin == nil || origin != *tc.Origin {
				t.Errorf("origin = %q, want %v", origin, tc.Origin)
			}
			if g := confOffset(off.ok, off.value); !reflect.DeepEqual(g, tc.Offset) {
				t.Errorf("offset = %v, want %v", g, tc.Offset)
			}
			if tc.Event != nil {
				tc.Event.assertEvent(t, e)
			}
		})
	}
}

// sortedCopy is a test helper for set comparisons.
func sortedCopy(s []string) []string {
	out := append([]string(nil), s...)
	sort.Strings(out)
	return out
}
