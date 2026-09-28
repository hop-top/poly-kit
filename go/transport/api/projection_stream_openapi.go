package api

import (
	"net/http"

	"github.com/danielgtaylor/huma/v2"
)

// streamResponseDescription documents the frames a streaming
// operation emits. OpenAPI 3.1 has no schema for an event stream's
// frames, so the vocabulary is prose here and in the discovery
// document.
const streamResponseDescription = "Server-sent events. Each `event` frame carries " +
	"one output event (kind, data, at). The stream ends with exactly one " +
	"terminal frame: `result` (the command's result plus `status`, the HTTP " +
	"status the request/reply route would have answered) or `error` (an " +
	"APIError). `: ping` comments keep an idle stream alive. A request refused " +
	"before anything streamed is answered with the request/reply route's " +
	"status and JSON body instead."

// describeStreamOp adds a command's streaming operation to the spec.
// It takes the parameters the command's own operation takes, on the
// same method, and answers text/event-stream.
func describeStreamOp(spec *huma.OpenAPI, d CommandDescriptor) {
	responses := commandResponses(spec, d)
	responses["200"] = &huma.Response{
		Description: streamResponseDescription,
		Content: map[string]*huma.MediaType{
			"text/event-stream": {Schema: &huma.Schema{Type: "string"}},
		},
	}
	op := &huma.Operation{
		OperationID: StreamOperationIDFor(d.Path),
		Method:      d.Method(),
		Path:        d.StreamRoute(),
		Summary:     summaryOf(d) + " (stream)",
		Description: d.Description,
		Tags:        []string{"commands"},
		Responses:   responses,
	}
	if d.Method() == http.MethodGet {
		op.Parameters = append(op.Parameters, queryParamsFor(d)...)
	} else {
		op.RequestBody = requestBodyFor(d)
	}
	spec.AddOperation(op)
}

// minimalStreamOp is a streaming operation's entry in the minimal
// spec.
func minimalStreamOp(d CommandDescriptor) map[string]any {
	return map[string]any{
		"operationId": StreamOperationIDFor(d.Path),
		"summary":     summaryOf(d) + " (stream)",
		"tags":        []string{"commands"},
		"responses": map[string]any{
			"200": map[string]any{
				"description": streamResponseDescription,
				"content":     map[string]any{"text/event-stream": map[string]any{}},
			},
		},
	}
}
