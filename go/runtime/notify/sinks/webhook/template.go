package webhooksink

import (
	"bytes"
	"encoding/json"
	"fmt"
	"text/template"

	"hop.top/kit/go/runtime/bus"
)

// Template renders a bus.Event into the bytes that the webhook
// transport POSTs as the request body. Render returns both the body
// bytes and the Content-Type header value the sink should emit.
//
// Implementations must be safe for concurrent use; the sink calls
// Render once per Drain on potentially many goroutines.
type Template interface {
	// Render returns the body bytes plus the content-type to set on
	// the outgoing request.
	Render(e bus.Event) (body []byte, contentType string, err error)
}

// jsonEventTemplate marshals the entire bus.Event as JSON. This is
// the default when no WithTemplate option is supplied; it matches the
// shape JSONLSink writes so a webhook receiver can be a generic
// event-collector endpoint.
type jsonEventTemplate struct{}

// DefaultJSONTemplate marshals the bus.Event as JSON with content-type
// application/json. The output is a single JSON object (not a JSONL
// line — webhook bodies are one event per request).
func DefaultJSONTemplate() Template { return jsonEventTemplate{} }

func (jsonEventTemplate) Render(e bus.Event) ([]byte, string, error) {
	body, err := json.Marshal(e)
	if err != nil {
		return nil, "", fmt.Errorf("webhook: marshal event: %w", err)
	}
	return body, "application/json", nil
}

// slackTemplate wraps a parsed text/template and emits the Slack
// incoming-webhook shape: {"text": "<rendered>"}. Parsing happens at
// construction time so callers fail fast on bad templates.
type slackTemplate struct {
	tmpl *template.Template
}

// SlackTemplate parses tmpl as a text/template (executed against the
// bus.Event passed to Render) and returns a Template that emits
// `{"text": "<rendered>"}` with content-type application/json. The
// rendered string is JSON-encoded by encoding/json so quotes and
// control characters are escaped correctly.
//
// SlackTemplate parses at construction time and returns an error on
// invalid template syntax — callers fail fast rather than discover
// the bad template on the first event.
func SlackTemplate(tmpl string) (Template, error) {
	t, err := template.New("slack").Parse(tmpl)
	if err != nil {
		return nil, fmt.Errorf("webhook: parse slack template: %w", err)
	}
	return slackTemplate{tmpl: t}, nil
}

func (s slackTemplate) Render(e bus.Event) ([]byte, string, error) {
	var rendered bytes.Buffer
	if err := s.tmpl.Execute(&rendered, e); err != nil {
		return nil, "", fmt.Errorf("webhook: execute slack template: %w", err)
	}
	body, err := json.Marshal(struct {
		Text string `json:"text"`
	}{Text: rendered.String()})
	if err != nil {
		return nil, "", fmt.Errorf("webhook: marshal slack body: %w", err)
	}
	return body, "application/json", nil
}
