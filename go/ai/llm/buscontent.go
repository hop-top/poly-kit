package llm

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"slices"

	"hop.top/kit/go/core/redact"
)

// cloneRequest copies req deeply enough that a content transform may
// rewrite any message field without touching the caller's request.
func cloneRequest(req Request) Request {
	out := req
	out.Messages = make([]Message, len(req.Messages))
	for i, m := range req.Messages {
		m.Parts = slices.Clone(m.Parts)
		m.ToolCalls = slices.Clone(m.ToolCalls)
		m.Thinking = slices.Clone(m.Thinking)
		out.Messages[i] = m
	}
	out.StopSequences = slices.Clone(req.StopSequences)
	if req.Extensions != nil {
		out.Extensions = make(map[string]any, len(req.Extensions))
		for k, v := range req.Extensions {
			out.Extensions[k] = v
		}
	}
	return out
}

// cloneResponse is [cloneRequest] for a response.
func cloneResponse(resp Response) Response {
	out := resp
	out.OutputParts = slices.Clone(resp.OutputParts)
	out.Thinking = slices.Clone(resp.Thinking)
	return out
}

// RedactRequest returns a [WithRequestContent] transform that passes
// every text of the request through r: message Content, text parts,
// tool call arguments (every JSON string, keys included, on its decoded
// text; arguments that are not JSON, or that still match after that,
// become the placeholder "REDACTED") and
// thinking text. What r cannot vouch for is
// left out: Extensions, media sources (a part keeps its type and MIME
// type, not its bytes) and thinking signatures. Tool names, roles and
// the model stay.
func RedactRequest(r *redact.Redactor) func(Request) Request {
	return func(req Request) Request {
		req.Extensions = nil
		for i := range req.Messages {
			m := &req.Messages[i]
			m.Content = r.Apply(m.Content)
			m.Parts = redactParts(r, m.Parts)
			m.ToolCalls = redactCalls(r, m.ToolCalls)
			m.Thinking = redactThinking(r, m.Thinking)
		}
		return req
	}
}

// RedactResponse is [RedactRequest] for the RequestEnd payload's
// response: Content, text output parts and thinking text through r;
// media and thinking signatures left out.
func RedactResponse(r *redact.Redactor) func(Response) Response {
	return func(resp Response) Response {
		resp.Content = r.Apply(resp.Content)
		resp.OutputParts = redactParts(r, resp.OutputParts)
		resp.Thinking = redactThinking(r, resp.Thinking)
		return resp
	}
}

func redactParts(r *redact.Redactor, parts []ContentPart) []ContentPart {
	for i := range parts {
		p := &parts[i]
		p.Text = r.Apply(p.Text)
		p.Source = nil
		p.Metadata = nil
	}
	return parts
}

func redactCalls(r *redact.Redactor, calls []ToolCall) []ToolCall {
	for i := range calls {
		c := &calls[i]
		c.ProviderData = nil
		if len(c.Arguments) > 0 {
			c.Arguments = redactJSON(r, c.Arguments)
		}
	}
	return calls
}

// redactJSON masks raw, a JSON value, with r, failing closed:
//
//   - every string, key and number is masked on its decoded text, so a
//     secret needing escapes on the wire (a quote, a backslash, a \u
//     escape) still matches; a number r masks becomes the masked text;
//   - each object member with a string or number value is always also
//     checked in key context, as "key":value and key=value, for rules
//     that need the key beside the value, whether or not the key or the
//     value alone matches; a member r flags there gets its value
//     replaced whole, and if it still matches with the value replaced
//     the whole value is the placeholder;
//   - members keep their order (two keys masked to the same text keep
//     the first one's place and the later value), so the final check
//     sees the input's adjacency;
//   - the input, written back compact in order, is scanned too: a match
//     that is not inside one token, or inside one member the key-context
//     check replaced, could be broken up by masking a neighbor, so it
//     fails the whole value;
//   - if raw does not decode, or r still finds anything in the result,
//     the whole value is the fixed placeholder "REDACTED". Nothing is
//     ever derived from raw's wire text.
func redactJSON(r *redact.Redactor, raw json.RawMessage) json.RawMessage {
	placeholder, _ := json.Marshal(redacted)
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	v, err := decodeOrdered(dec)
	if err != nil {
		return placeholder
	}
	if _, err := dec.Token(); err != io.EOF {
		return placeholder
	}
	w := &argsWalk{r: r}
	out, ok := w.value(v)
	if !ok {
		return placeholder
	}
	for _, s := range r.Spans(w.compact.String()) {
		if !w.covers(s) {
			return placeholder
		}
	}
	var buf bytes.Buffer
	writeOrdered(&buf, out)
	if r.Apply(buf.String()) != buf.String() {
		return placeholder
	}
	return buf.Bytes()
}

// member is one object member, in input order.
type member struct {
	key   string
	value any
}

// orderedObject is a decoded JSON object that keeps member order.
type orderedObject []member

// decodeOrdered reads one JSON value from dec: objects as
// orderedObject, numbers as json.Number.
func decodeOrdered(dec *json.Decoder) (any, error) {
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	switch t := tok.(type) {
	case json.Delim:
		switch t {
		case '{':
			obj := orderedObject{}
			for dec.More() {
				kt, err := dec.Token()
				if err != nil {
					return nil, err
				}
				k, ok := kt.(string)
				if !ok {
					return nil, errors.New("object key is not a string")
				}
				v, err := decodeOrdered(dec)
				if err != nil {
					return nil, err
				}
				obj = append(obj, member{k, v})
			}
			if _, err := dec.Token(); err != nil {
				return nil, err
			}
			return obj, nil
		case '[':
			arr := []any{}
			for dec.More() {
				v, err := decodeOrdered(dec)
				if err != nil {
					return nil, err
				}
				arr = append(arr, v)
			}
			if _, err := dec.Token(); err != nil {
				return nil, err
			}
			return arr, nil
		}
		return nil, errors.New("unexpected delimiter")
	}
	return tok, nil // string, json.Number, bool, nil
}

// writeOrdered encodes v compact, objects in member order.
func writeOrdered(buf *bytes.Buffer, v any) {
	switch x := v.(type) {
	case orderedObject:
		buf.WriteByte('{')
		for i, m := range x {
			if i > 0 {
				buf.WriteByte(',')
			}
			k, _ := json.Marshal(m.key)
			buf.Write(k)
			buf.WriteByte(':')
			writeOrdered(buf, m.value)
		}
		buf.WriteByte('}')
	case []any:
		buf.WriteByte('[')
		for i, e := range x {
			if i > 0 {
				buf.WriteByte(',')
			}
			writeOrdered(buf, e)
		}
		buf.WriteByte(']')
	case json.Number:
		buf.WriteString(x.String())
	default:
		b, _ := json.Marshal(x)
		buf.Write(b)
	}
}

// argsWalk masks a decoded value while writing the input back compact,
// in order, noting the ranges of it the masking clears on its own: each
// scalar token, and each member the key-context check replaced.
type argsWalk struct {
	r       *redact.Redactor
	compact bytes.Buffer
	cleared [][2]int
}

// covers reports whether s lies inside one cleared range.
func (w *argsWalk) covers(s redact.Span) bool {
	for _, c := range w.cleared {
		if c[0] <= s.Start && s.End <= c[1] {
			return true
		}
	}
	return false
}

// token writes a scalar token, noting its range as cleared.
func (w *argsWalk) token(v any) {
	start := w.compact.Len()
	writeOrdered(&w.compact, v)
	w.cleared = append(w.cleared, [2]int{start, w.compact.Len()})
}

// value returns v masked; false fails the whole value closed.
func (w *argsWalk) value(v any) (any, bool) {
	switch x := v.(type) {
	case nil, bool:
		writeOrdered(&w.compact, x)
		return x, true
	case string, json.Number:
		w.token(x)
		return maskScalar(w.r, x), true
	case []any:
		w.compact.WriteByte('[')
		out := make([]any, 0, len(x))
		for i, e := range x {
			if i > 0 {
				w.compact.WriteByte(',')
			}
			m, ok := w.value(e)
			if !ok {
				return nil, false
			}
			out = append(out, m)
		}
		w.compact.WriteByte(']')
		return out, true
	case orderedObject:
		w.compact.WriteByte('{')
		out := orderedObject{}
		for i, mem := range x {
			if i > 0 {
				w.compact.WriteByte(',')
			}
			start := w.compact.Len()
			w.token(mem.key)
			w.compact.WriteByte(':')
			var masked any
			switch e := mem.value.(type) {
			case string, json.Number:
				w.token(e)
				if inContext(w.r, mem.key, e) {
					// Replaced whole; still matching with the value
					// gone means the key holds it.
					if inContext(w.r, mem.key, redacted) {
						return nil, false
					}
					w.cleared = append(w.cleared, [2]int{start, w.compact.Len()})
					masked = redacted
				} else {
					masked = maskScalar(w.r, e)
				}
			default:
				m, ok := w.value(e)
				if !ok {
					return nil, false
				}
				masked = m
			}
			key := w.r.Apply(mem.key)
			if i := slices.IndexFunc(out, func(m member) bool { return m.key == key }); i >= 0 {
				out[i].value = masked
			} else {
				out = append(out, member{key, masked})
			}
		}
		w.compact.WriteByte('}')
		return out, true
	}
	return nil, false // not a decoded JSON value: fail closed
}

// maskScalar masks a string or number on its decoded text; a number r
// masks becomes the masked text. Bools and nulls hold nothing.
func maskScalar(r *redact.Redactor, v any) any {
	switch x := v.(type) {
	case string:
		return r.Apply(x)
	case json.Number:
		if m := r.Apply(x.String()); m != x.String() {
			return m
		}
		return x
	}
	return v
}

// inContext reports whether r matches the member k with the scalar e
// rendered beside its key, as "key":value or key=value. Always checked,
// whether or not the key or value alone matches.
func inContext(r *redact.Redactor, k string, e any) bool {
	var text string
	switch x := e.(type) {
	case string:
		text = x
	case json.Number:
		text = x.String()
	default:
		return false
	}
	jk, _ := json.Marshal(k)
	je, _ := json.Marshal(e)
	for _, s := range []string{string(jk) + ":" + string(je), k + "=" + text} {
		if r.Apply(s) != s {
			return true
		}
	}
	return false
}

func redactThinking(r *redact.Redactor, tbs []ThinkingBlock) []ThinkingBlock {
	for i := range tbs {
		tbs[i].Thinking = r.Apply(tbs[i].Thinking)
		tbs[i].Signature = ""
		tbs[i].Redacted = ""
	}
	return tbs
}
