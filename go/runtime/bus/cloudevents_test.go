package bus

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"hop.top/kit/go/core/id"
)

var ceTime = time.Date(2026, 10, 25, 12, 0, 0, 500_000_000, time.UTC)

func validCE() CloudEvent {
	return CloudEvent{
		SpecVersion: CloudEventsSpecVersion,
		ID:          "evt_01jg000000e008000000000000",
		Source:      "kit.test",
		Type:        "kit.test.order.created",
	}
}

func TestNewEvent_MintsTypeID(t *testing.T) {
	e := NewEvent("kit.test.order.created", "src", nil)
	p, err := id.Parse(e.ID)
	if err != nil {
		t.Fatalf("id %q is not a TypeID: %v", e.ID, err)
	}
	if p.Prefix != EventIDPrefix {
		t.Fatalf("prefix = %q, want %q", p.Prefix, EventIDPrefix)
	}
	if e2 := NewEvent("kit.test.order.created", "src", nil); e2.ID == e.ID {
		t.Fatal("ids must be unique per event")
	}
}

func TestToCloudEvent_Mapping(t *testing.T) {
	e := Event{
		ID:          "evt_01jg000000e008000000000000",
		Topic:       "kit.test.order.created",
		Source:      "kit.checkout",
		Timestamp:   ceTime,
		Subject:     "order-42",
		DataSchema:  "https://example.com/order.json",
		Payload:     map[string]any{"total": 3},
		WorkspaceID: "ws1",
		Extensions:  map[string]any{"tenantid": "t1", "seq": 7, "replay": true},
	}
	ce, err := ToCloudEvent(e)
	if err != nil {
		t.Fatal(err)
	}
	want := CloudEvent{
		SpecVersion:     "1.0",
		ID:              e.ID,
		Source:          "kit.checkout",
		Type:            "kit.test.order.created",
		Subject:         "order-42",
		Time:            ceTime,
		DataContentType: "application/json",
		DataSchema:      "https://example.com/order.json",
		Data:            json.RawMessage(`{"total":3}`),
		Extensions: map[string]any{
			"tenantid": "t1", "seq": int64(7), "replay": true, "workspaceid": "ws1",
		},
	}
	if !reflect.DeepEqual(ce, want) {
		t.Fatalf("got  %#v\nwant %#v", ce, want)
	}
}

func TestToCloudEvent_Defaults(t *testing.T) {
	cases := []struct {
		name    string
		payload any
		dct     string
		check   func(t *testing.T, ce CloudEvent)
	}{
		{"nil payload omits data and datacontenttype", nil, "", func(t *testing.T, ce CloudEvent) {
			if ce.Data != nil || ce.DataBase64 != nil || ce.DataContentType != "" {
				t.Fatalf("want no data, got %#v", ce)
			}
		}},
		{"bytes payload uses data_base64", []byte{0xde, 0xad}, "application/octet-stream", func(t *testing.T, ce CloudEvent) {
			if !reflect.DeepEqual(ce.DataBase64, []byte{0xde, 0xad}) || ce.Data != nil {
				t.Fatalf("got %#v", ce)
			}
			if ce.DataContentType != "application/octet-stream" {
				t.Fatalf("datacontenttype = %q", ce.DataContentType)
			}
		}},
		{"explicit datacontenttype kept", "hi", "text/plain", func(t *testing.T, ce CloudEvent) {
			if ce.DataContentType != "text/plain" || string(ce.Data) != `"hi"` {
				t.Fatalf("got %#v", ce)
			}
		}},
		{"raw JSON payload passes through", json.RawMessage(`[1,2]`), "", func(t *testing.T, ce CloudEvent) {
			if string(ce.Data) != `[1,2]` || ce.DataContentType != "application/json" {
				t.Fatalf("got %#v", ce)
			}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := NewEvent("kit.test.order.created", "src", tc.payload)
			e.DataContentType = tc.dct
			ce, err := ToCloudEvent(e)
			if err != nil {
				t.Fatal(err)
			}
			tc.check(t, ce)
		})
	}
}

func TestToCloudEvent_MintsMissingID(t *testing.T) {
	ce, err := ToCloudEvent(Event{Topic: "kit.test.order.created", Source: "src"})
	if err != nil {
		t.Fatal(err)
	}
	if p, err := id.Parse(ce.ID); err != nil || p.Prefix != EventIDPrefix {
		t.Fatalf("minted id %q: %v", ce.ID, err)
	}
	if !ce.Time.IsZero() {
		t.Fatal("zero Timestamp must leave time unset")
	}
}

func TestToCloudEvent_Errors(t *testing.T) {
	base := func() Event { return NewEvent("kit.test.order.created", "src", nil) }
	cases := []struct {
		name string
		mut  func(*Event)
		want string
	}{
		{"empty source", func(e *Event) { e.Source = "" }, "cloudevents: source: required"},
		{"empty topic", func(e *Event) { e.Topic = "" }, "cloudevents: type: required"},
		{"workspaceid in extensions", func(e *Event) { e.Extensions = map[string]any{"workspaceid": "x"} }, "cloudevents: workspaceid: reserved for the workspace id"},
		{"unencodable payload", func(e *Event) { e.Payload = func() {} }, "cloudevents: data: payload is not JSON-encodable"},
		{"bad extension name", func(e *Event) { e.Extensions = map[string]any{"tenant_id": "x"} }, "cloudevents: tenant_id: invalid extension name"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := base()
			tc.mut(&e)
			_, err := ToCloudEvent(e)
			if err == nil || err.Error() != tc.want {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
			if !errors.Is(err, ErrInvalidCloudEvent) {
				t.Fatalf("err %v does not match ErrInvalidCloudEvent", err)
			}
		})
	}
}

func TestCloudEvent_Validate(t *testing.T) {
	cases := []struct {
		name string
		mut  func(*CloudEvent)
		want string
	}{
		{"valid", func(*CloudEvent) {}, ""},
		{"missing specversion", func(c *CloudEvent) { c.SpecVersion = "" }, "cloudevents: specversion: required"},
		{"wrong specversion", func(c *CloudEvent) { c.SpecVersion = "0.3" }, `cloudevents: specversion: unsupported value "0.3"`},
		{"missing id", func(c *CloudEvent) { c.ID = "" }, "cloudevents: id: required"},
		{"missing source", func(c *CloudEvent) { c.Source = "" }, "cloudevents: source: required"},
		{"missing type", func(c *CloudEvent) { c.Type = "" }, "cloudevents: type: required"},
		{"first error wins", func(c *CloudEvent) { c.ID = ""; c.Type = "" }, "cloudevents: id: required"},
		{"data and data_base64", func(c *CloudEvent) { c.Data = json.RawMessage(`1`); c.DataBase64 = []byte{1} }, "cloudevents: data: data and data_base64 are mutually exclusive"},
		{"data not JSON", func(c *CloudEvent) { c.Data = json.RawMessage(`{`) }, "cloudevents: data: invalid JSON"},
		{"uppercase extension", func(c *CloudEvent) { c.Extensions = map[string]any{"Tenant": "x"} }, "cloudevents: Tenant: invalid extension name"},
		{"underscore extension", func(c *CloudEvent) { c.Extensions = map[string]any{"workspace_id": "x"} }, "cloudevents: workspace_id: invalid extension name"},
		{"empty extension name", func(c *CloudEvent) { c.Extensions = map[string]any{"": "x"} }, `cloudevents: "": invalid extension name`},
		{"reserved extension", func(c *CloudEvent) { c.Extensions = map[string]any{"subject": "x"} }, "cloudevents: subject: reserved attribute name"},
		{"float extension", func(c *CloudEvent) { c.Extensions = map[string]any{"ratio": 1.5} }, "cloudevents: ratio: unsupported extension value type"},
		{"map extension", func(c *CloudEvent) { c.Extensions = map[string]any{"obj": map[string]any{}} }, "cloudevents: obj: unsupported extension value type"},
		{"nil extension", func(c *CloudEvent) { c.Extensions = map[string]any{"gone": nil} }, "cloudevents: gone: unsupported extension value type"},
		{"int over int32", func(c *CloudEvent) { c.Extensions = map[string]any{"big": int64(1) << 31} }, "cloudevents: big: integer out of range"},
		{"uint over int32", func(c *CloudEvent) { c.Extensions = map[string]any{"big": uint64(1) << 63} }, "cloudevents: big: integer out of range"},
		{"extensions sorted, first wins", func(c *CloudEvent) { c.Extensions = map[string]any{"b_": 1, "a_": 1} }, "cloudevents: a_: invalid extension name"},
		{"long name accepted", func(c *CloudEvent) { c.Extensions = map[string]any{"abcdefghijklmnopqrstuvwxyz": "ok"} }, ""},
		{"int32 bounds accepted", func(c *CloudEvent) {
			c.Extensions = map[string]any{"lo": int32(-2147483648), "hi": 2147483647, "n": json.Number("5")}
		}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := validCE()
			tc.mut(&c)
			err := c.Validate()
			if tc.want == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil || err.Error() != tc.want {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
			var ce *CloudEventError
			if !errors.As(err, &ce) || !errors.Is(err, ErrInvalidCloudEvent) {
				t.Fatalf("err %T does not expose CloudEventError", err)
			}
		})
	}
}

func TestCloudEvent_MarshalJSON(t *testing.T) {
	c := validCE()
	c.Subject = "a<b"
	c.Time = ceTime
	c.DataContentType = "application/json"
	c.Data = json.RawMessage(`{ "k" : "v" }`)
	c.Extensions = map[string]any{"zeta": true, "alpha": int64(-3), "tenantid": "t1"}
	// Called directly: json.Marshal re-escapes <, > and & in
	// Marshaler output.
	got, err := c.MarshalJSON()
	if err != nil {
		t.Fatal(err)
	}
	want := `{"specversion":"1.0","id":"evt_01jg000000e008000000000000","source":"kit.test",` +
		`"type":"kit.test.order.created","subject":"a<b","time":"2026-10-25T12:00:00.5Z",` +
		`"datacontenttype":"application/json","alpha":-3,"tenantid":"t1","zeta":true,"data":{"k":"v"}}`
	if string(got) != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}

	c.Data = nil
	c.DataBase64 = []byte("hi")
	c.Extensions = nil
	got, _ = json.Marshal(c)
	if !strings.HasSuffix(string(got), `"data_base64":"aGk="}`) {
		t.Fatalf("got %s", got)
	}

	if _, err := json.Marshal(CloudEvent{}); err == nil || !strings.Contains(err.Error(), "cloudevents: specversion: required") {
		t.Fatalf("marshal of invalid event: err = %v", err)
	}
}

func TestCloudEvent_TimeNormalisedToUTC(t *testing.T) {
	c := validCE()
	c.Time = time.Date(2026, 10, 25, 8, 0, 0, 0, time.FixedZone("EDT", -4*3600))
	got, _ := json.Marshal(c)
	if !strings.Contains(string(got), `"time":"2026-10-25T12:00:00Z"`) {
		t.Fatalf("got %s", got)
	}
}

func TestCloudEvent_UnmarshalJSON(t *testing.T) {
	in := `{"specversion":"1.0","id":"x1","source":"/dek/core","type":"dek.core.stream.reset",` +
		`"time":"2026-10-25T08:00:00.5-04:00","subject":null,"datacontenttype":"application/json",` +
		`"data":{"n":1},"tenantid":"t1","dekoffset":42,"live":false,"gone":null}`
	var c CloudEvent
	if err := json.Unmarshal([]byte(in), &c); err != nil {
		t.Fatal(err)
	}
	want := CloudEvent{
		SpecVersion: "1.0", ID: "x1", Source: "/dek/core", Type: "dek.core.stream.reset",
		Time: ceTime, DataContentType: "application/json", Data: json.RawMessage(`{"n":1}`),
		Extensions: map[string]any{"tenantid": "t1", "dekoffset": int64(42), "live": false},
	}
	if !c.Time.Equal(want.Time) {
		t.Fatalf("time = %v", c.Time)
	}
	c.Time = want.Time
	if !reflect.DeepEqual(c, want) {
		t.Fatalf("got  %#v\nwant %#v", c, want)
	}
}

func TestCloudEvent_UnmarshalJSON_DataNullVsAbsent(t *testing.T) {
	head := `{"specversion":"1.0","id":"x","source":"s","type":"t"`
	for _, in := range []string{head + `}`, head + `,"data":null}`, head + `,"data_base64":null}`} {
		var c CloudEvent
		if err := json.Unmarshal([]byte(in), &c); err != nil {
			t.Fatalf("%s: %v", in, err)
		}
		if c.Data != nil || c.DataBase64 != nil {
			t.Fatalf("%s: data should be unset, got %#v", in, c)
		}
	}
	var c CloudEvent
	if err := json.Unmarshal([]byte(head+`,"data":"null"}`), &c); err != nil || string(c.Data) != `"null"` {
		t.Fatalf("string data must survive: %v %#v", err, c)
	}
}

func TestCloudEvent_UnmarshalJSON_Errors(t *testing.T) {
	head := `{"specversion":"1.0","id":"x","source":"s","type":"t"`
	cases := []struct {
		name, in, want string
	}{
		{"not JSON", `{`, "cloudevents: invalid JSON"},
		{"not an object", `[1]`, "cloudevents: not a JSON object"},
		{"null document", `null`, "cloudevents: not a JSON object"},
		{"missing id", `{"specversion":"1.0","source":"s","type":"t"}`, "cloudevents: id: required"},
		{"null id is unset", `{"specversion":"1.0","id":null,"source":"s","type":"t"}`, "cloudevents: id: required"},
		{"empty id", `{"specversion":"1.0","id":"","source":"s","type":"t"}`, "cloudevents: id: required"},
		{"numeric id", `{"specversion":"1.0","id":7,"source":"s","type":"t"}`, "cloudevents: id: must be a string"},
		{"numeric specversion", `{"specversion":1.0,"id":"x","source":"s","type":"t"}`, "cloudevents: specversion: must be a string"},
		{"spec 0.3", `{"specversion":"0.3","id":"x","source":"s","type":"t"}`, `cloudevents: specversion: unsupported value "0.3"`},
		{"empty subject", head + `,"subject":""}`, "cloudevents: subject: must not be empty"},
		{"empty datacontenttype", head + `,"datacontenttype":""}`, "cloudevents: datacontenttype: must not be empty"},
		{"empty dataschema", head + `,"dataschema":""}`, "cloudevents: dataschema: must not be empty"},
		{"bad time", head + `,"time":"yesterday"}`, "cloudevents: time: invalid RFC 3339 timestamp"},
		{"time not string", head + `,"time":0}`, "cloudevents: time: must be a string"},
		{"both data", head + `,"data":1,"data_base64":"AQ=="}`, "cloudevents: data: data and data_base64 are mutually exclusive"},
		{"bad base64", head + `,"data_base64":"!!"}`, "cloudevents: data_base64: invalid base64"},
		{"base64 not string", head + `,"data_base64":1}`, "cloudevents: data_base64: must be a string"},
		{"bad extension name", head + `,"tenant_id":"x"}`, "cloudevents: tenant_id: invalid extension name"},
		{"float extension", head + `,"ratio":1.5}`, "cloudevents: ratio: unsupported extension value type"},
		{"exponent extension", head + `,"n":1e3}`, "cloudevents: n: unsupported extension value type"},
		{"array extension", head + `,"tags":["a"]}`, "cloudevents: tags: unsupported extension value type"},
		{"int out of range", head + `,"offset":4294967296}`, "cloudevents: offset: integer out of range"},
		{"shape before value", `{"specversion":"0.3","id":7}`, "cloudevents: id: must be a string"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var c CloudEvent
			err := json.Unmarshal([]byte(tc.in), &c)
			if err == nil {
				t.Fatalf("want %q, got nil", tc.want)
			}
			// encoding/json does not wrap Unmarshaler errors when the
			// input is syntactically valid, so the text is exact.
			if got := UnmarshalCloudEventErr(tc.in); got != tc.want {
				t.Fatalf("err = %q, want %q", got, tc.want)
			}
		})
	}
}

// UnmarshalCloudEventErr returns the decode error text, bypassing
// encoding/json's syntax pre-check so malformed input reaches the decoder.
func UnmarshalCloudEventErr(in string) string {
	var c CloudEvent
	if err := c.UnmarshalJSON([]byte(in)); err != nil {
		return err.Error()
	}
	return ""
}

func TestCloudEvent_RoundTrip(t *testing.T) {
	e := Event{
		ID:              "evt_01jg000000e008000000000000",
		Topic:           "kit.test.order.created",
		Source:          "kit.checkout",
		Timestamp:       ceTime,
		Subject:         "order-42",
		DataContentType: "application/json",
		Payload:         map[string]any{"total": float64(3), "items": []any{"a"}},
		WorkspaceID:     "ws1",
		Extensions:      map[string]any{"tenantid": "t1", "dekoffset": int64(9)},
	}
	raw, err := MarshalCloudEvent(e)
	if err != nil {
		t.Fatal(err)
	}
	got, err := UnmarshalCloudEvent(raw)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, e) {
		t.Fatalf("got  %#v\nwant %#v", got, e)
	}

	bin := Event{ID: "x", Topic: "kit.test.blob.stored", Source: "s", Timestamp: ceTime, Payload: []byte("hi")}
	raw, _ = MarshalCloudEvent(bin)
	got, err = UnmarshalCloudEvent(raw)
	if err != nil || !reflect.DeepEqual(got, bin) {
		t.Fatalf("binary round trip: %v\ngot  %#v\nwant %#v", err, got, bin)
	}
}

func TestFromCloudEvent_WorkspaceExtensionMustBeString(t *testing.T) {
	c := validCE()
	c.Extensions = map[string]any{"workspaceid": int64(1)}
	if _, err := FromCloudEvent(c); err == nil || err.Error() != "cloudevents: workspaceid: must be a string" {
		t.Fatalf("err = %v", err)
	}
}
