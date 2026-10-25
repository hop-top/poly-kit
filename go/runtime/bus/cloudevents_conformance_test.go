package bus

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"hop.top/kit/go/core/id"
)

// Runs the CloudEvents corpus in contracts/bus-v1/cloudevents. Go is
// canonical: a failure means the code drifted, not the vector.

const ceConfDir = "../../../contracts/bus-v1/cloudevents"

var ceConfFiles = map[string]bool{
	"decode.json": true,
	"encode.json": true,
	"frames.json": true,
}

type ceConfHeader struct {
	Schema      string `json:"$schema,omitempty"`
	ID          string `json:"$id,omitempty"`
	Version     string `json:"version"`
	Description string `json:"description"`
}

// ceConfEvent is a kit Event in vector form.
type ceConfEvent struct {
	ID              string                     `json:"id"`
	Topic           string                     `json:"topic"`
	Source          string                     `json:"source"`
	Timestamp       string                     `json:"timestamp"`
	Subject         string                     `json:"subject"`
	DataContentType string                     `json:"datacontenttype"`
	DataSchema      string                     `json:"dataschema"`
	Payload         json.RawMessage            `json:"payload"`
	PayloadBase64   *string                    `json:"payload_base64"`
	WorkspaceID     string                     `json:"workspace_id"`
	Extensions      map[string]json.RawMessage `json:"extensions"`
}

func (v ceConfEvent) event(t *testing.T) Event {
	t.Helper()
	e := Event{
		ID: v.ID, Topic: Topic(v.Topic), Source: v.Source, Subject: v.Subject,
		DataContentType: v.DataContentType, DataSchema: v.DataSchema, WorkspaceID: v.WorkspaceID,
	}
	if v.Timestamp != "" {
		ts, err := time.Parse(time.RFC3339Nano, v.Timestamp)
		if err != nil {
			t.Fatalf("vector timestamp %q: %v", v.Timestamp, err)
		}
		e.Timestamp = ts
	}
	if v.Payload != nil {
		if err := json.Unmarshal(v.Payload, &e.Payload); err != nil {
			t.Fatalf("vector payload: %v", err)
		}
	}
	if v.PayloadBase64 != nil {
		b, err := base64.StdEncoding.DecodeString(*v.PayloadBase64)
		if err != nil {
			t.Fatalf("vector payload_base64: %v", err)
		}
		e.Payload = b
	}
	if v.Extensions != nil {
		e.Extensions = map[string]any{}
		for k, raw := range v.Extensions {
			dec := json.NewDecoder(bytes.NewReader(raw))
			dec.UseNumber()
			var x any
			if err := dec.Decode(&x); err != nil {
				t.Fatalf("vector extension %s: %v", k, err)
			}
			if n, ok := x.(json.Number); ok {
				if i, err := n.Int64(); err == nil {
					x = i
				}
			}
			e.Extensions[k] = x
		}
	}
	return e
}

// assertEvent compares got with the vector, timestamps as instants.
func (v ceConfEvent) assertEvent(t *testing.T, got Event) {
	t.Helper()
	want := v.event(t)
	if !got.Timestamp.Equal(want.Timestamp) {
		t.Errorf("timestamp = %v, want %v", got.Timestamp, want.Timestamp)
	}
	got.Timestamp, want.Timestamp = time.Time{}, time.Time{}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("event\n got  %#v\n want %#v", got, want)
	}
}

func ceConfLoad(t *testing.T, name string, v any) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(filepath.FromSlash(ceConfDir), name))
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
	if h.Version != "v1" {
		t.Fatalf("%s: version %q, want v1", name, h.Version)
	}
}

func assertJSONEqual(t *testing.T, got []byte, want json.RawMessage) {
	t.Helper()
	var g, w any
	if err := json.Unmarshal(got, &g); err != nil {
		t.Fatalf("output is not JSON: %s", got)
	}
	if err := json.Unmarshal(want, &w); err != nil {
		t.Fatalf("vector is not JSON: %s", want)
	}
	if !reflect.DeepEqual(g, w) {
		t.Errorf("JSON\n got  %s\n want %s", got, want)
	}
}

func TestCloudEventsConformanceAllFilesConsumed(t *testing.T) {
	entries, err := os.ReadDir(filepath.FromSlash(ceConfDir))
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, e := range entries {
		if filepath.Ext(e.Name()) != ".json" {
			continue
		}
		seen[e.Name()] = true
		if !ceConfFiles[e.Name()] {
			t.Errorf("%s has no runner", e.Name())
		}
	}
	for n := range ceConfFiles {
		if !seen[n] {
			t.Errorf("%s registered but missing from the corpus", n)
		}
	}
}

func TestCloudEventsConformanceDecode(t *testing.T) {
	var f struct {
		ceConfHeader
		Cases []struct {
			Name       string          `json:"name"`
			Input      string          `json:"input"`
			OK         bool            `json:"ok"`
			Error      string          `json:"error"`
			Canonical  json.RawMessage `json:"canonical"`
			Event      *ceConfEvent    `json:"event"`
			EventError string          `json:"event_error"`
		} `json:"cases"`
	}
	ceConfLoad(t, "decode.json", &f)
	for _, tc := range f.Cases {
		t.Run(tc.Name, func(t *testing.T) {
			switch {
			case tc.OK && tc.Error != "":
				t.Fatal("vector: ok case with error")
			case tc.OK && tc.Canonical == nil:
				t.Fatal("vector: ok case without canonical")
			case !tc.OK && (tc.Canonical != nil || tc.Event != nil || tc.EventError != ""):
				t.Fatal("vector: failing case with canonical, event or event_error")
			case tc.Event != nil && tc.EventError != "":
				t.Fatal("vector: event and event_error together")
			}
			var c CloudEvent
			err := c.UnmarshalJSON([]byte(tc.Input))
			if !tc.OK {
				if tc.Error == "" {
					t.Fatal("failing case without error")
				}
				if err == nil || err.Error() != tc.Error {
					t.Fatalf("err = %v, want %q", err, tc.Error)
				}
				return
			}
			if err != nil {
				t.Fatalf("decode: %v", err)
			}
			out, err := c.MarshalJSON()
			if err != nil {
				t.Fatalf("re-encode: %v", err)
			}
			assertJSONEqual(t, out, tc.Canonical)
			e, err := FromCloudEvent(c)
			if tc.EventError != "" {
				if err == nil || err.Error() != tc.EventError {
					t.Fatalf("event err = %v, want %q", err, tc.EventError)
				}
				return
			}
			if err != nil {
				t.Fatalf("to event: %v", err)
			}
			if tc.Event != nil {
				tc.Event.assertEvent(t, e)
			}
		})
	}
}

func TestCloudEventsConformanceEncode(t *testing.T) {
	var f struct {
		ceConfHeader
		Cases []struct {
			Name       string          `json:"name"`
			Event      ceConfEvent     `json:"event"`
			OK         bool            `json:"ok"`
			Error      string          `json:"error"`
			CloudEvent json.RawMessage `json:"cloudevent"`
			Text       string          `json:"text"`
			MintID     bool            `json:"mint_id"`
		} `json:"cases"`
	}
	ceConfLoad(t, "encode.json", &f)
	for _, tc := range f.Cases {
		t.Run(tc.Name, func(t *testing.T) {
			switch {
			case tc.OK && (tc.Error != "" || tc.CloudEvent == nil):
				t.Fatal("vector: ok case needs cloudevent and no error")
			case !tc.OK && (tc.CloudEvent != nil || tc.Text != "" || tc.MintID):
				t.Fatal("vector: failing case with cloudevent, text or mint_id")
			}
			c, err := ToCloudEvent(tc.Event.event(t))
			if !tc.OK {
				if tc.Error == "" {
					t.Fatal("failing case without error")
				}
				if err == nil || err.Error() != tc.Error {
					t.Fatalf("err = %v, want %q", err, tc.Error)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if tc.MintID {
				p, err := id.Parse(c.ID)
				if err != nil || p.Prefix != EventIDPrefix || !strings.HasPrefix(c.ID, "evt_") {
					t.Fatalf("minted id %q: %v", c.ID, err)
				}
			}
			out, err := c.MarshalJSON()
			if err != nil {
				t.Fatal(err)
			}
			if tc.Text != "" && string(out) != tc.Text {
				t.Errorf("text\n got  %s\n want %s", out, tc.Text)
			}
			if tc.MintID {
				var m map[string]any
				_ = json.Unmarshal(out, &m)
				delete(m, "id")
				out, _ = json.Marshal(m)
			}
			assertJSONEqual(t, out, tc.CloudEvent)
		})
	}
}

func TestCloudEventsConformanceFrames(t *testing.T) {
	var f struct {
		ceConfHeader
		Decode []struct {
			Name   string       `json:"name"`
			Frame  string       `json:"frame"`
			OK     bool         `json:"ok"`
			Kind   string       `json:"kind"`
			Origin *string      `json:"origin"`
			Event  *ceConfEvent `json:"event"`
		} `json:"decode"`
		Encode []struct {
			Name   string          `json:"name"`
			Format string          `json:"format"`
			Origin string          `json:"origin"`
			Event  ceConfEvent     `json:"event"`
			Frame  json.RawMessage `json:"frame"`
		} `json:"encode"`
	}
	ceConfLoad(t, "frames.json", &f)
	for _, tc := range f.Decode {
		t.Run("decode/"+tc.Name, func(t *testing.T) {
			switch {
			case tc.OK && (tc.Kind == "" || tc.Origin == nil):
				t.Fatal("vector: ok frame needs kind and origin")
			case !tc.OK && (tc.Kind != "" || tc.Origin != nil || tc.Event != nil):
				t.Fatal("vector: dropped frame with kind, origin or event")
			}
			e, origin, err := decodeFrame([]byte(tc.Frame))
			if !tc.OK {
				if err == nil {
					t.Fatal("want frame dropped")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if kind, _, _ := frameKind([]byte(tc.Frame)); kind != tc.Kind {
				t.Errorf("kind = %s, want %s", kind, tc.Kind)
			}
			if origin != *tc.Origin {
				t.Errorf("origin = %q, want %q", origin, *tc.Origin)
			}
			if tc.Event != nil {
				tc.Event.assertEvent(t, e)
			}
		})
	}
	for _, tc := range f.Encode {
		t.Run("encode/"+tc.Name, func(t *testing.T) {
			format := map[string]FrameFormat{"kit": FrameKit, "cloudevents": FrameCloudEvents}
			ff, ok := format[tc.Format]
			if !ok {
				t.Fatalf("unknown format %q", tc.Format)
			}
			out, err := encodeFrame(ff, tc.Event.event(t), tc.Origin)
			if err != nil {
				t.Fatal(err)
			}
			assertJSONEqual(t, out, tc.Frame)
		})
	}
}
