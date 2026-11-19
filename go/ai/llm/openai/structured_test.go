package openai

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

func structuredFake(t *testing.T) *transporttest.Fake {
	t.Helper()
	return transporttest.NewFake(t, "application/json", completionJSON(transporttest.VerdictOK, "assistant", "stop"))
}

func completeWith(t *testing.T, uri string, rf *llm.ResponseFormat) (llm.Response, error) {
	t.Helper()
	p, err := llm.Resolve(uri)
	require.NoError(t, err)
	defer p.Close()
	return p.(llm.Completer).Complete(context.Background(), llm.Request{
		Messages:       []llm.Message{{Role: "user", Content: "judge"}},
		ResponseFormat: rf,
	})
}

func wireBody(t *testing.T, f *transporttest.Fake) map[string]json.RawMessage {
	t.Helper()
	var m map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(f.Body(), &m))
	return m
}

func TestStructured_WireJSONSchema(t *testing.T) {
	fake := structuredFake(t)
	_, err := completeWith(t, "openai://gpt-4o?api_key=k&base_url="+fake.URL, &llm.ResponseFormat{
		Name: "verdict", Schema: json.RawMessage(transporttest.VerdictSchema), Strict: true,
	})
	require.NoError(t, err)

	var rf struct {
		Type       string `json:"type"`
		JSONSchema struct {
			Name   string          `json:"name"`
			Strict *bool           `json:"strict"`
			Schema json.RawMessage `json:"schema"`
		} `json:"json_schema"`
	}
	require.NoError(t, json.Unmarshal(wireBody(t, fake)["response_format"], &rf))
	assert.Equal(t, "json_schema", rf.Type)
	assert.Equal(t, "verdict", rf.JSONSchema.Name)
	require.NotNil(t, rf.JSONSchema.Strict)
	assert.True(t, *rf.JSONSchema.Strict)
	assert.JSONEq(t, transporttest.VerdictSchema, string(rf.JSONSchema.Schema))
}

func TestStructured_WireDefaultsAndAbsence(t *testing.T) {
	fake := structuredFake(t)
	_, err := completeWith(t, "openai://gpt-4o?api_key=k&base_url="+fake.URL,
		&llm.ResponseFormat{Schema: json.RawMessage(transporttest.VerdictSchema)})
	require.NoError(t, err)
	var rf struct {
		JSONSchema map[string]json.RawMessage `json:"json_schema"`
	}
	require.NoError(t, json.Unmarshal(wireBody(t, fake)["response_format"], &rf))
	assert.JSONEq(t, `"output"`, string(rf.JSONSchema["name"]), "default name")
	assert.NotContains(t, rf.JSONSchema, "strict", "strict is sent only when set")

	_, err = completeWith(t, "openai://gpt-4o?api_key=k&base_url="+fake.URL, nil)
	require.NoError(t, err)
	assert.NotContains(t, wireBody(t, fake), "response_format")
}

func TestStructured_ParamJSONObject(t *testing.T) {
	fake := structuredFake(t)
	_, err := completeWith(t, "openai://m?api_key=k&response_format=json_object&base_url="+fake.URL,
		&llm.ResponseFormat{Schema: json.RawMessage(transporttest.VerdictSchema)})
	require.NoError(t, err)
	body := wireBody(t, fake)
	assert.JSONEq(t, `{"type":"json_object"}`, string(body["response_format"]))
	assert.Contains(t, string(body["messages"]), "JSON Schema", "the schema goes in a system message")
	assert.Contains(t, string(body["messages"]), "additionalProperties")
}

func TestStructured_ParamNone(t *testing.T) {
	fake := structuredFake(t)
	_, err := completeWith(t, "openai://m?api_key=k&response_format=none&base_url="+fake.URL,
		&llm.ResponseFormat{Schema: json.RawMessage(transporttest.VerdictSchema)})
	require.NoError(t, err)
	body := wireBody(t, fake)
	assert.NotContains(t, body, "response_format")
	assert.Contains(t, string(body["messages"]), "additionalProperties")
}

func TestStructured_ParamInvalid(t *testing.T) {
	_, err := llm.Resolve("openai://m?api_key=k&response_format=xml")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "response_format")
}

func TestStructured_BadFormatNotSent(t *testing.T) {
	fake := structuredFake(t)
	_, err := completeWith(t, "openai://m?api_key=k&base_url="+fake.URL,
		&llm.ResponseFormat{Name: "bad name", Schema: json.RawMessage(`{}`)})
	require.Error(t, err)
	assert.Nil(t, fake.Body())
}

func TestStructured_XRRReplay(t *testing.T) {
	var res llm.StructuredResult
	transporttest.Cassette{
		Dir:         filepath.Join("testdata", "xrr-structured"),
		Env:         "OPENAI_STRUCTURED_XRR_RECORD",
		ContentType: "application/json",
		Reply:       completionJSON(transporttest.VerdictOK, "assistant", "stop"),
		URI:         func(base string) string { return "openai://gpt-4o?api_key=k&base_url=" + base },
	}.Run(t, func(p llm.Provider) error {
		var err error
		res, err = llm.CompleteStructured(context.Background(), p.(llm.Completer),
			llm.Request{Messages: []llm.Message{{Role: "user", Content: "judge"}}},
			llm.ResponseFormat{Name: "verdict", Schema: json.RawMessage(transporttest.VerdictSchema), Strict: true})
		return err
	})
	assert.JSONEq(t, transporttest.VerdictOK, string(res.Data))
	assert.Equal(t, 30, res.Usage.TotalTokens)
}
