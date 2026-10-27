package llm

import (
	"bytes"
	"encoding/json"
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
// text; arguments that are not JSON become one masked JSON string) and
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

// redactJSON passes every string of raw, keys included, through r on
// its decoded text, so a secret that needs escapes on the wire (a
// quote, a backslash, a \u escape) still matches. Raw that does not
// decode goes out as r's mask of the whole text, as a JSON string;
// nothing passes unmasked.
func redactJSON(r *redact.Redactor, raw json.RawMessage) json.RawMessage {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err == nil && !dec.More() {
		if out, err := json.Marshal(redactValue(r, v)); err == nil {
			return out
		}
	}
	quoted, _ := json.Marshal(r.Apply(string(raw)))
	return quoted
}

// redactValue masks every string in a decoded JSON value. Two keys
// masked to the same text keep one value: the copy only describes.
func redactValue(r *redact.Redactor, v any) any {
	switch x := v.(type) {
	case string:
		return r.Apply(x)
	case []any:
		for i := range x {
			x[i] = redactValue(r, x[i])
		}
		return x
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, e := range x {
			out[r.Apply(k)] = redactValue(r, e)
		}
		return out
	}
	return v
}

func redactThinking(r *redact.Redactor, tbs []ThinkingBlock) []ThinkingBlock {
	for i := range tbs {
		tbs[i].Thinking = r.Apply(tbs[i].Thinking)
		tbs[i].Signature = ""
		tbs[i].Redacted = ""
	}
	return tbs
}
