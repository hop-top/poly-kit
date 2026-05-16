package webhooksink_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/runtime/bus"
	webhooksink "hop.top/kit/go/runtime/notify/sinks/webhook"
)

func TestDefaultJSONTemplate(t *testing.T) {
	t.Parallel()

	tmpl := webhooksink.DefaultJSONTemplate()
	e := bus.Event{
		Topic:     "kit.test.thing.created",
		Source:    "test",
		Timestamp: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
		Payload:   map[string]any{"hello": "world"},
	}

	body, ct, err := tmpl.Render(e)
	require.NoError(t, err)
	assert.Equal(t, "application/json", ct)

	var got map[string]any
	require.NoError(t, json.Unmarshal(body, &got))
	assert.Equal(t, "kit.test.thing.created", got["topic"])
	assert.Equal(t, "test", got["source"])

	payload, ok := got["payload"].(map[string]any)
	require.True(t, ok, "payload should round-trip as a JSON object")
	assert.Equal(t, "world", payload["hello"])
}

func TestSlackTemplate_BasicRender(t *testing.T) {
	t.Parallel()

	tmpl, err := webhooksink.SlackTemplate("alert: {{.Topic}}")
	require.NoError(t, err)

	e := bus.Event{Topic: "foo.bar.baz.created"}
	body, ct, err := tmpl.Render(e)
	require.NoError(t, err)
	assert.Equal(t, "application/json", ct)

	var got struct {
		Text string `json:"text"`
	}
	require.NoError(t, json.Unmarshal(body, &got))
	assert.Equal(t, "alert: foo.bar.baz.created", got.Text)
}

func TestSlackTemplate_EscapesQuotes(t *testing.T) {
	t.Parallel()

	tmpl, err := webhooksink.SlackTemplate(`{{.Source}}`)
	require.NoError(t, err)

	// Source contains a double-quote that must survive JSON encoding.
	e := bus.Event{Source: `she said "hi"`}
	body, _, err := tmpl.Render(e)
	require.NoError(t, err)

	// The body must be valid JSON regardless of input quoting.
	var got struct {
		Text string `json:"text"`
	}
	require.NoError(t, json.Unmarshal(body, &got), "body must be valid JSON: %s", string(body))
	assert.Equal(t, `she said "hi"`, got.Text)
}

func TestSlackTemplate_BadTemplateAtConstruction(t *testing.T) {
	t.Parallel()

	// Unclosed action — text/template fails to Parse.
	_, err := webhooksink.SlackTemplate("hello {{.Topic")
	require.Error(t, err, "construction should fail fast on bad template syntax")
}

func TestSlackTemplate_RenderErrorOnUnknownField(t *testing.T) {
	t.Parallel()

	// {{.NonExistent}} parses fine but executes against bus.Event;
	// text/template returns an error rather than silently emitting
	// "<no value>" because the missing-key default is "error" via
	// Execute on a struct.
	tmpl, err := webhooksink.SlackTemplate("{{.NotAField}}")
	require.NoError(t, err, "parsing should succeed; template engine resolves at execute")

	_, _, err = tmpl.Render(bus.Event{})
	require.Error(t, err, "missing struct field should surface at Render time")
}
