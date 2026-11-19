package openai

import (
	"fmt"
	"strings"

	oai "github.com/openai/openai-go"
	"github.com/openai/openai-go/packages/param"
	"github.com/openai/openai-go/shared"

	"hop.top/kit/go/ai/llm"
)

// ResponseFormatParam is the provider param that picks how
// [llm.Request.ResponseFormat] goes out, for OpenAI-compatible servers
// that lack json_schema.
const ResponseFormatParam = "response_format"

// How a schema constrains the reply; values of [ResponseFormatParam].
const (
	// formatJSONSchema (default) sends response_format json_schema.
	formatJSONSchema = "json_schema"
	// formatJSONObject sends response_format json_object, for servers
	// with JSON mode only, and puts the schema in a system message.
	formatJSONObject = "json_object"
	// formatNone sends no response_format, for servers with neither,
	// and puts the schema in a system message; the repair loop of
	// [llm.CompleteStructured] does the enforcing.
	formatNone = "none"
)

func formatModeOf(params map[string]string) (string, error) {
	v, ok := params[ResponseFormatParam]
	if !ok {
		return formatJSONSchema, nil
	}
	switch m := strings.TrimSpace(v); m {
	case formatJSONSchema, formatJSONObject, formatNone:
		return m, nil
	}
	return "", fmt.Errorf("openai: %s must be %q, %q or %q, got %q",
		ResponseFormatParam, formatJSONSchema, formatJSONObject, formatNone, v)
}

// applyResponseFormat maps rf onto p, per the adapter's format mode.
func (a *Adapter) applyResponseFormat(p *oai.ChatCompletionNewParams, rf *llm.ResponseFormat) error {
	if rf == nil {
		return nil
	}
	if err := llm.CheckResponseFormat(rf); err != nil {
		return fmt.Errorf("%s: %w", a.scheme, err)
	}
	switch a.format {
	case formatJSONSchema, "":
		js := shared.ResponseFormatJSONSchemaJSONSchemaParam{
			Name:   rf.EffectiveName(),
			Schema: rf.Schema,
		}
		if rf.Strict {
			js.Strict = param.NewOpt(true)
		}
		p.ResponseFormat = oai.ChatCompletionNewParamsResponseFormatUnion{
			OfJSONSchema: &shared.ResponseFormatJSONSchemaParam{JSONSchema: js},
		}
		return nil
	case formatJSONObject:
		p.ResponseFormat = oai.ChatCompletionNewParamsResponseFormatUnion{
			OfJSONObject: &shared.ResponseFormatJSONObjectParam{},
		}
	}
	p.Messages = append([]oai.ChatCompletionMessageParamUnion{
		oai.SystemMessage("Reply with a single JSON value and nothing else, matching this JSON Schema:\n" +
			string(rf.Schema)),
	}, p.Messages...)
	return nil
}
