package bus

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"sort"
	"time"
)

// MarshalJSON encodes c in CloudEvents 1.0 structured mode. Members come
// in a fixed order: specversion, id, source, type, subject, time,
// datacontenttype, dataschema, extensions by ascending name, then data
// or data_base64. Unset attributes are omitted, time is UTC RFC 3339
// with trailing fractional zeros trimmed, and strings are not
// HTML-escaped. (json.Marshal re-escapes <, > and & in Marshaler
// output; call MarshalJSON directly for the canonical bytes.)
func (c CloudEvent) MarshalJSON() ([]byte, error) {
	if err := c.Validate(); err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	buf.WriteByte('{')
	first := true
	member := func(name string, raw []byte) {
		if !first {
			buf.WriteByte(',')
		}
		first = false
		key, _ := marshalNoEscape(name)
		buf.Write(key)
		buf.WriteByte(':')
		buf.Write(raw)
	}
	str := func(name, v string) {
		if v != "" {
			raw, _ := marshalNoEscape(v)
			member(name, raw)
		}
	}
	str("specversion", c.SpecVersion)
	str("id", c.ID)
	str("source", c.Source)
	str("type", c.Type)
	str("subject", c.Subject)
	if !c.Time.IsZero() {
		str("time", c.Time.UTC().Format(time.RFC3339Nano))
	}
	str("datacontenttype", c.DataContentType)
	str("dataschema", c.DataSchema)
	for _, name := range sortedKeys(c.Extensions) {
		v, _ := extensionValue(name, c.Extensions[name]) // validated above
		raw, _ := marshalNoEscape(v)
		member(name, raw)
	}
	switch {
	case c.Data != nil:
		var compact bytes.Buffer
		if err := json.Compact(&compact, c.Data); err != nil {
			return nil, &CloudEventError{Attribute: "data", Reason: "invalid JSON", Err: err}
		}
		member("data", compact.Bytes())
	case c.DataBase64 != nil:
		raw, _ := marshalNoEscape(base64.StdEncoding.EncodeToString(c.DataBase64))
		member("data_base64", raw)
	}
	buf.WriteByte('}')
	return buf.Bytes(), nil
}

// ceStringAttrs are the string-typed context attributes, in the order
// the decoder checks them.
var ceStringAttrs = []string{
	"specversion", "id", "source", "type", "subject", "time",
	"datacontenttype", "dataschema",
}

// UnmarshalJSON decodes a CloudEvents 1.0 structured-mode document.
//
// A JSON null member counts as unset. Checks run in two passes and the
// first failure is returned: a shape pass (document, then the string
// attributes in ceStringAttrs order, then data/data_base64, then
// extensions by ascending name), then [CloudEvent.Validate].
func (c *CloudEvent) UnmarshalJSON(data []byte) error {
	if !json.Valid(data) {
		return ceErr("", "invalid JSON")
	}
	var members map[string]json.RawMessage
	if err := json.Unmarshal(data, &members); err != nil || members == nil {
		return &CloudEventError{Reason: "not a JSON object", Err: err}
	}
	for k, v := range members {
		if string(v) == "null" {
			delete(members, k)
		}
	}

	var out CloudEvent
	strs := make(map[string]string, len(ceStringAttrs))
	for _, name := range ceStringAttrs {
		raw, ok := members[name]
		if !ok {
			continue
		}
		delete(members, name)
		var s string
		if err := json.Unmarshal(raw, &s); err != nil {
			return ceErr(name, "must be a string")
		}
		switch name {
		case "subject", "datacontenttype", "dataschema", "time":
			if s == "" {
				return ceErr(name, "must not be empty")
			}
		}
		if name == "time" {
			t, err := parseCETime(s)
			if err != nil {
				return &CloudEventError{Attribute: "time", Reason: "invalid RFC 3339 timestamp", Err: err}
			}
			out.Time = t
		}
		strs[name] = s
	}
	out.SpecVersion = strs["specversion"]
	out.ID = strs["id"]
	out.Source = strs["source"]
	out.Type = strs["type"]
	out.Subject = strs["subject"]
	out.DataContentType = strs["datacontenttype"]
	out.DataSchema = strs["dataschema"]

	rawData, hasData := members["data"]
	rawB64, hasB64 := members["data_base64"]
	delete(members, "data")
	delete(members, "data_base64")
	if hasData && hasB64 {
		return ceErr("data", "data and data_base64 are mutually exclusive")
	}
	if hasData {
		var compact bytes.Buffer
		_ = json.Compact(&compact, rawData) // document already valid
		out.Data = json.RawMessage(compact.Bytes())
	}
	if hasB64 {
		var s string
		if err := json.Unmarshal(rawB64, &s); err != nil {
			return ceErr("data_base64", "must be a string")
		}
		b, err := decodeCEBase64(s)
		if err != nil {
			return &CloudEventError{Attribute: "data_base64", Reason: "invalid base64", Err: err}
		}
		out.DataBase64 = b
	}

	names := make([]string, 0, len(members))
	for k := range members {
		names = append(names, k)
	}
	sort.Strings(names)
	for _, name := range names {
		if !validExtensionName(name) {
			return ceErr(extensionLabel(name), "invalid extension name")
		}
		v, err := decodeExtension(name, members[name])
		if err != nil {
			return err
		}
		if out.Extensions == nil {
			out.Extensions = make(map[string]any, len(names))
		}
		out.Extensions[name] = v
	}

	if err := out.Validate(); err != nil {
		return err
	}
	*c = out
	return nil
}

// decodeExtension turns one JSON extension value into string, bool or
// int64.
func decodeExtension(name string, raw json.RawMessage) (any, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, ceErr(name, "unsupported extension value type")
	}
	return extensionValue(name, v)
}

// marshalNoEscape is json.Marshal without HTML escaping, so <, > and &
// stay literal as in other kit SDKs.
func marshalNoEscape(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(buf.Bytes(), []byte("\n")), nil
}
