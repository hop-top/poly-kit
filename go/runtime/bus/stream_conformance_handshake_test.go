package bus

import (
	"encoding/json"
	"reflect"
	"testing"
)

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
			Mine   int      `json:"mine"`
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
			a, err := parseAck([]byte(tc.Frame), tc.Mine)
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
