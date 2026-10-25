package bus

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"time"

	"hop.top/kit/go/core/id"
)

// CloudEventsSpecVersion is the only CloudEvents spec version kit
// encodes and accepts.
const CloudEventsSpecVersion = "1.0"

// EventIDPrefix is the TypeID prefix of ids minted for events
// ("evt_<26-char base32>", a UUIDv7 underneath).
const EventIDPrefix = "evt"

// WorkspaceExtension is the CloudEvents extension attribute that carries
// Event.WorkspaceID. "workspace_id" is not a legal extension name, so
// the CloudEvents form drops the underscore.
const WorkspaceExtension = "workspaceid"

// jsonContentType is the datacontenttype kit stamps on JSON data.
const jsonContentType = "application/json"

// ErrInvalidCloudEvent matches every [*CloudEventError] via errors.Is.
var ErrInvalidCloudEvent = errors.New("cloudevents: invalid event")

// CloudEventError reports the first CloudEvents rule an event breaks.
// Its text is part of the wire contract: other kit SDKs produce the
// same strings for the same input.
type CloudEventError struct {
	// Attribute is the offending attribute or extension name; empty
	// for document-level errors (invalid JSON, not an object).
	Attribute string
	// Reason is the rule broken, e.g. "required".
	Reason string
	// Err is an underlying cause, kept out of the text.
	Err error
}

func (e *CloudEventError) Error() string {
	if e.Attribute == "" {
		return "cloudevents: " + e.Reason
	}
	return "cloudevents: " + e.Attribute + ": " + e.Reason
}

// Is makes errors.Is(err, ErrInvalidCloudEvent) hold.
func (e *CloudEventError) Is(target error) bool { return target == ErrInvalidCloudEvent }

// Unwrap returns the underlying cause, if any.
func (e *CloudEventError) Unwrap() error { return e.Err }

func ceErr(attr, reason string) error { return &CloudEventError{Attribute: attr, Reason: reason} }

// CloudEvent is a CloudEvents 1.0 event in kit's in-memory form.
//
// Empty strings, a zero Time and nil Data/DataBase64 mean "unset".
// Extension values are string, bool or an integer in the CloudEvents
// Integer range (int32); decoding yields string, bool or int64.
type CloudEvent struct {
	SpecVersion     string
	ID              string
	Source          string
	Type            string
	Subject         string
	Time            time.Time
	DataContentType string
	DataSchema      string
	// Data is the JSON value of the "data" member.
	Data json.RawMessage
	// DataBase64 is the decoded "data_base64" member (binary data).
	DataBase64 []byte
	Extensions map[string]any
}

// reservedAttrs are the context attribute names an extension may not
// take.
var reservedAttrs = map[string]bool{
	"specversion": true, "id": true, "source": true, "type": true,
	"subject": true, "time": true, "datacontenttype": true,
	"dataschema": true, "data": true,
}

// Validate checks c against CloudEvents 1.0 and returns the first
// violation, in this order: specversion, id, source, type, data,
// then extensions by ascending name.
func (c CloudEvent) Validate() error {
	switch {
	case c.SpecVersion == "":
		return ceErr("specversion", "required")
	case c.SpecVersion != CloudEventsSpecVersion:
		return ceErr("specversion", fmt.Sprintf("unsupported value %q", c.SpecVersion))
	case c.ID == "":
		return ceErr("id", "required")
	case c.Source == "":
		return ceErr("source", "required")
	case c.Type == "":
		return ceErr("type", "required")
	case c.Data != nil && c.DataBase64 != nil:
		return ceErr("data", "data and data_base64 are mutually exclusive")
	case c.Data != nil && !json.Valid(c.Data):
		return ceErr("data", "invalid JSON")
	}
	for _, name := range sortedKeys(c.Extensions) {
		if _, err := extensionValue(name, c.Extensions[name]); err != nil {
			return err
		}
	}
	return nil
}

// validExtensionName reports whether name is lowercase ASCII letters and
// digits only. CloudEvents recommends at most 20 characters; longer
// names are accepted.
func validExtensionName(name string) bool {
	if name == "" {
		return false
	}
	for i := 0; i < len(name); i++ {
		ch := name[i]
		if (ch < 'a' || ch > 'z') && (ch < '0' || ch > '9') {
			return false
		}
	}
	return true
}

// extensionValue checks one extension and returns its canonical value:
// string, bool or int64.
func extensionValue(name string, v any) (any, error) {
	if !validExtensionName(name) {
		return nil, ceErr(extensionLabel(name), "invalid extension name")
	}
	if reservedAttrs[name] {
		return nil, ceErr(name, "reserved attribute name")
	}
	var n int64
	switch x := v.(type) {
	case string, bool:
		return x, nil
	case int:
		n = int64(x)
	case int8:
		n = int64(x)
	case int16:
		n = int64(x)
	case int32:
		n = int64(x)
	case int64:
		n = x
	case uint, uint8, uint16, uint32, uint64:
		u := reflectUint(x)
		if u > math.MaxInt32 {
			return nil, ceErr(name, "integer out of range")
		}
		n = int64(u)
	case json.Number:
		if !isJSONInteger(string(x)) {
			return nil, ceErr(name, "unsupported extension value type")
		}
		i, err := x.Int64()
		if err != nil {
			return nil, ceErr(name, "integer out of range")
		}
		n = i
	default:
		return nil, ceErr(name, "unsupported extension value type")
	}
	if n < math.MinInt32 || n > math.MaxInt32 {
		return nil, ceErr(name, "integer out of range")
	}
	return n, nil
}

// extensionLabel names an extension in error text; the empty name
// prints as "" so the message stays unambiguous.
func extensionLabel(name string) string {
	if name == "" {
		return `""`
	}
	return name
}

func reflectUint(v any) uint64 {
	switch x := v.(type) {
	case uint:
		return uint64(x)
	case uint8:
		return uint64(x)
	case uint16:
		return uint64(x)
	case uint32:
		return uint64(x)
	default:
		return v.(uint64)
	}
}

// isJSONInteger reports whether s is a JSON number with no fraction or
// exponent: -?(0|[1-9][0-9]*).
func isJSONInteger(s string) bool {
	if s != "" && s[0] == '-' {
		s = s[1:]
	}
	if s == "" || (s[0] == '0' && len(s) > 1) {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// ToCloudEvent maps a kit Event to a CloudEvent.
//
//   - id: Event.ID, or a freshly minted "evt_" TypeID when empty
//   - type: the topic, verbatim; source, subject, dataschema verbatim
//   - time: Event.Timestamp; a zero Timestamp leaves time unset
//   - data: nil Payload leaves data unset; []byte goes to data_base64;
//     anything else is JSON-encoded into data
//   - datacontenttype: Event.DataContentType, else "application/json"
//     when data is set
//   - extensions: Event.Extensions, plus workspaceid from WorkspaceID
func ToCloudEvent(e Event) (CloudEvent, error) {
	c := CloudEvent{
		SpecVersion:     CloudEventsSpecVersion,
		ID:              e.ID,
		Source:          e.Source,
		Type:            string(e.Topic),
		Subject:         e.Subject,
		Time:            e.Timestamp,
		DataContentType: e.DataContentType,
		DataSchema:      e.DataSchema,
	}
	if c.ID == "" {
		c.ID = id.MustNew(EventIDPrefix)
	}
	switch p := e.Payload.(type) {
	case nil:
	case []byte:
		if p != nil {
			c.DataBase64 = p
		}
	default:
		raw, err := marshalNoEscape(p)
		if err != nil {
			return CloudEvent{}, &CloudEventError{Attribute: "data", Reason: "payload is not JSON-encodable", Err: err}
		}
		if string(raw) != "null" {
			c.Data = raw
		}
	}
	if c.DataContentType == "" && c.Data != nil {
		c.DataContentType = jsonContentType
	}
	if len(e.Extensions) > 0 || e.WorkspaceID != "" {
		c.Extensions = make(map[string]any, len(e.Extensions)+1)
	}
	for _, name := range sortedKeys(e.Extensions) {
		if name == WorkspaceExtension {
			return CloudEvent{}, ceErr(WorkspaceExtension, "reserved for the workspace id")
		}
		v, err := extensionValue(name, e.Extensions[name])
		if err != nil {
			return CloudEvent{}, err
		}
		c.Extensions[name] = v
	}
	if e.WorkspaceID != "" {
		c.Extensions[WorkspaceExtension] = e.WorkspaceID
	}
	if err := c.Validate(); err != nil {
		return CloudEvent{}, err
	}
	return c, nil
}

// FromCloudEvent maps a CloudEvent back to a kit Event: the inverse of
// [ToCloudEvent]. JSON data decodes the way network frames always have
// (objects to map[string]any, numbers to float64); data_base64 becomes
// a []byte Payload. The workspaceid extension moves to WorkspaceID; all
// other extensions are kept.
func FromCloudEvent(c CloudEvent) (Event, error) {
	if err := c.Validate(); err != nil {
		return Event{}, err
	}
	e := Event{
		ID:              c.ID,
		Topic:           Topic(c.Type),
		Source:          c.Source,
		Timestamp:       c.Time,
		Subject:         c.Subject,
		DataContentType: c.DataContentType,
		DataSchema:      c.DataSchema,
	}
	switch {
	case c.DataBase64 != nil:
		e.Payload = c.DataBase64
	case c.Data != nil:
		if err := json.Unmarshal(c.Data, &e.Payload); err != nil {
			return Event{}, &CloudEventError{Attribute: "data", Reason: "invalid JSON", Err: err}
		}
	}
	for _, name := range sortedKeys(c.Extensions) {
		v, _ := extensionValue(name, c.Extensions[name]) // validated above
		if name == WorkspaceExtension {
			ws, ok := v.(string)
			if !ok {
				return Event{}, ceErr(WorkspaceExtension, "must be a string")
			}
			e.WorkspaceID = ws
			continue
		}
		if e.Extensions == nil {
			e.Extensions = make(map[string]any, len(c.Extensions))
		}
		e.Extensions[name] = v
	}
	return e, nil
}

// MarshalCloudEvent encodes e as a CloudEvents 1.0 structured-mode JSON
// document.
func MarshalCloudEvent(e Event) ([]byte, error) {
	c, err := ToCloudEvent(e)
	if err != nil {
		return nil, err
	}
	return c.MarshalJSON()
}

// UnmarshalCloudEvent decodes a CloudEvents 1.0 structured-mode JSON
// document into a kit Event.
func UnmarshalCloudEvent(data []byte) (Event, error) {
	var c CloudEvent
	if err := c.UnmarshalJSON(data); err != nil {
		return Event{}, err
	}
	return FromCloudEvent(c)
}
