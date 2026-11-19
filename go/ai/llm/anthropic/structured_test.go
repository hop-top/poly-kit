package anthropic_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/ai/llm"
	"hop.top/kit/go/ai/llm/internal/transporttest"
)

func TestStructured_WireOutputConfig(t *testing.T) {
	fake := transporttest.NewFake(t, "application/json",
		string(anthropicResponse(transporttest.VerdictOK, "end_turn", 4, 6)))
	p, err := llm.Resolve("anthropic://claude-sonnet-4-5?api_key=k&base_url=" + fake.URL)
	require.NoError(t, err)
	defer p.Close()

	_, err = p.(llm.Completer).Complete(context.Background(), llm.Request{
		Messages:       []llm.Message{{Role: "user", Content: "judge"}},
		ResponseFormat: &llm.ResponseFormat{Name: "verdict", Schema: json.RawMessage(transporttest.VerdictSchema)},
	})
	require.NoError(t, err)

	var body struct {
		OutputConfig struct {
			Format struct {
				Type   string          `json:"type"`
				Schema json.RawMessage `json:"schema"`
			} `json:"format"`
		} `json:"output_config"`
		Tools json.RawMessage `json:"tools"`
	}
	require.NoError(t, json.Unmarshal(fake.Body(), &body))
	assert.Equal(t, "json_schema", body.OutputConfig.Format.Type)
	assert.JSONEq(t, transporttest.VerdictSchema, string(body.OutputConfig.Format.Schema))
	assert.Empty(t, body.Tools, "native mode, not a forced tool call")

	_, err = p.(llm.Completer).Complete(context.Background(), llm.Request{
		Messages: []llm.Message{{Role: "user", Content: "judge"}},
	})
	require.NoError(t, err)
	assert.NotContains(t, string(fake.Body()), "output_config")
}

func TestStructured_BadFormatNotSent(t *testing.T) {
	fake := transporttest.NewFake(t, "application/json", string(anthropicResponse("x", "end_turn", 1, 1)))
	p, err := llm.Resolve("anthropic://claude-sonnet-4-5?api_key=k&base_url=" + fake.URL)
	require.NoError(t, err)
	defer p.Close()
	_, err = p.(llm.Completer).Complete(context.Background(), llm.Request{
		Messages:       []llm.Message{{Role: "user", Content: "judge"}},
		ResponseFormat: &llm.ResponseFormat{Schema: json.RawMessage(`[]`)},
	})
	require.Error(t, err)
	assert.Nil(t, fake.Body())
}

func TestStructured_XRRReplay(t *testing.T) {
	var res llm.StructuredResult
	transporttest.Cassette{
		Dir:         filepath.Join("testdata", "xrr-structured"),
		Env:         "ANTHROPIC_STRUCTURED_XRR_RECORD",
		ContentType: "application/json",
		Reply:       string(anthropicResponse(transporttest.VerdictOK, "end_turn", 4, 6)),
		URI:         func(base string) string { return "anthropic://claude-sonnet-4-5?api_key=k&base_url=" + base },
	}.Run(t, func(p llm.Provider) error {
		var err error
		res, err = llm.CompleteStructured(context.Background(), p.(llm.Completer),
			llm.Request{Messages: []llm.Message{{Role: "user", Content: "judge"}}},
			llm.ResponseFormat{Name: "verdict", Schema: json.RawMessage(transporttest.VerdictSchema)})
		return err
	})
	assert.JSONEq(t, transporttest.VerdictOK, string(res.Data))
	assert.Equal(t, 10, res.Usage.TotalTokens)
}
