package llm_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/ai/llm"
	llmerrors "hop.top/kit/go/ai/llm/errors"
	"hop.top/kit/go/runtime/bus"
)

const verdictSchema = `{
  "type": "object",
  "properties": {
    "label": {"type": "string", "enum": ["pass", "fail"]},
    "score": {"type": "integer", "minimum": 0, "maximum": 10}
  },
  "required": ["label", "score"],
  "additionalProperties": false
}`

// scripted answers each Complete with the next canned reply and
// records the requests it saw.
type scripted struct {
	replies []string
	seen    []llm.Request
}

func (s *scripted) Complete(_ context.Context, req llm.Request) (llm.Response, error) {
	s.seen = append(s.seen, req)
	i := len(s.seen) - 1
	if i >= len(s.replies) {
		return llm.Response{}, errors.New("script exhausted")
	}
	return llm.Response{
		Content: s.replies[i],
		Role:    "assistant",
		Usage:   llm.Usage{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15},
	}, nil
}

func verdictFormat() llm.ResponseFormat {
	return llm.ResponseFormat{Schema: json.RawMessage(verdictSchema)}
}

func userReq() llm.Request {
	return llm.Request{Messages: []llm.Message{{Role: "user", Content: "judge"}}}
}

func TestCompleteStructured_ValidFirstTry(t *testing.T) {
	c := &scripted{replies: []string{`{"label":"pass","score":7}`}}
	res, err := llm.CompleteStructured(context.Background(), c, userReq(), verdictFormat())
	require.NoError(t, err)
	assert.JSONEq(t, `{"label":"pass","score":7}`, string(res.Data))
	assert.Equal(t, 1, res.Attempts)
	assert.Equal(t, 15, res.Usage.TotalTokens)
	require.Len(t, c.seen, 1)
	rf := c.seen[0].ResponseFormat
	require.NotNil(t, rf, "request carries the response format")
	assert.Equal(t, "output", rf.Name, "default name")
}

func TestCompleteStructured_RepairsInvalidOutput(t *testing.T) {
	c := &scripted{replies: []string{
		`{"label":"pass"}`,
		`{"label":"pass","score":3}`,
	}}
	res, err := llm.CompleteStructured(context.Background(), c, userReq(), verdictFormat())
	require.NoError(t, err)
	assert.Equal(t, 2, res.Attempts)
	assert.JSONEq(t, `{"label":"pass","score":3}`, string(res.Data))
	assert.Equal(t, 30, res.Usage.TotalTokens, "usage sums both attempts")
	assert.Equal(t, 15, res.Response.Usage.TotalTokens, "embedded usage is the last attempt")

	// Second request: original + bad output as assistant + violations.
	require.Len(t, c.seen, 2)
	msgs := c.seen[1].Messages
	require.Len(t, msgs, 3)
	assert.Equal(t, llm.Message{Role: "assistant", Content: `{"label":"pass"}`}, msgs[1])
	assert.Equal(t, "user", msgs[2].Role)
	assert.Contains(t, msgs[2].Content, "score")
	assert.Contains(t, msgs[2].Content, "corrected JSON only")
	// The caller's request is untouched.
	assert.Len(t, userReq().Messages, 1)
}

func TestCompleteStructured_ExhaustedIsSchemaError(t *testing.T) {
	c := &scripted{replies: []string{`{"label":"x","score":1}`, `nope`, `{"label":"pass","score":99}`}}
	res, err := llm.CompleteStructured(context.Background(), c, userReq(), verdictFormat())
	require.Error(t, err)
	assert.True(t, errors.Is(err, llm.ErrSchemaInvalid))
	var se *llm.SchemaError
	require.True(t, errors.As(err, &se))
	assert.Equal(t, 3, se.Attempts, "1 try + default 2 repairs")
	assert.Equal(t, `{"label":"pass","score":99}`, se.Last)
	require.NotEmpty(t, se.Violations)
	assert.Contains(t, se.Violations[0], "/score")
	assert.Equal(t, 45, res.Usage.TotalTokens, "usage of every attempt is reported")
	assert.Equal(t, 3, res.Attempts)
	assert.False(t, llmerrors.IsFallbackable(err), "a schema error does not fall back")
}

func TestCompleteStructured_MaxRepairs(t *testing.T) {
	c := &scripted{replies: []string{`{}`, `{}`, `{}`, `{}`}}
	_, err := llm.CompleteStructured(context.Background(), c, userReq(), verdictFormat(), llm.MaxRepairs(0))
	var se *llm.SchemaError
	require.ErrorAs(t, err, &se)
	assert.Equal(t, 1, se.Attempts)
	assert.Len(t, c.seen, 1)

	c = &scripted{replies: []string{`{}`, `{}`, `{}`, `{}`}}
	_, err = llm.CompleteStructured(context.Background(), c, userReq(), verdictFormat(), llm.MaxRepairs(3))
	require.ErrorAs(t, err, &se)
	assert.Equal(t, 4, se.Attempts)
}

func TestCompleteStructured_EnumViolation(t *testing.T) {
	c := &scripted{replies: []string{`{"label":"maybe","score":1}`}}
	_, err := llm.CompleteStructured(context.Background(), c, userReq(), verdictFormat(), llm.MaxRepairs(0))
	var se *llm.SchemaError
	require.ErrorAs(t, err, &se)
	require.Len(t, se.Violations, 1)
	assert.Contains(t, se.Violations[0], "/label")
	assert.Contains(t, se.Violations[0], `must be one of "pass", "fail"`)
}

func TestCompleteStructured_FencedOutputNeedsLenient(t *testing.T) {
	fenced := "Sure! Here it is:\n```json\n{\"label\":\"fail\",\"score\":2}\n```\nHope that helps."

	// Strict (default): fenced prose is not JSON; one repair fixes it.
	c := &scripted{replies: []string{fenced, `{"label":"fail","score":2}`}}
	res, err := llm.CompleteStructured(context.Background(), c, userReq(), verdictFormat())
	require.NoError(t, err)
	assert.Equal(t, 2, res.Attempts)

	// Lenient: one attempt, fence stripped.
	c = &scripted{replies: []string{fenced}}
	res, err = llm.CompleteStructured(context.Background(), c, userReq(), verdictFormat(), llm.Lenient())
	require.NoError(t, err)
	assert.Equal(t, 1, res.Attempts)
	assert.JSONEq(t, `{"label":"fail","score":2}`, string(res.Data))
}

func TestCompleteStructured_LenientLeadingProse(t *testing.T) {
	c := &scripted{replies: []string{`The verdict: {"label":"pass","score":9} done`}}
	res, err := llm.CompleteStructured(context.Background(), c, userReq(), verdictFormat(), llm.Lenient())
	require.NoError(t, err)
	assert.JSONEq(t, `{"label":"pass","score":9}`, string(res.Data))
}

func TestCompleteStructured_LenientDoesNotRepair(t *testing.T) {
	c := &scripted{replies: []string{`{"label":"pass"}`, `{"label":"pass","score":1}`}}
	_, err := llm.CompleteStructured(context.Background(), c, userReq(), verdictFormat(), llm.Lenient())
	var se *llm.SchemaError
	require.ErrorAs(t, err, &se)
	assert.Equal(t, 1, se.Attempts)

	// An explicit MaxRepairs re-enables the re-ask.
	c = &scripted{replies: []string{`{"label":"pass"}`, `{"label":"pass","score":1}`}}
	res, err := llm.CompleteStructured(context.Background(), c, userReq(), verdictFormat(),
		llm.Lenient(), llm.MaxRepairs(1))
	require.NoError(t, err)
	assert.Equal(t, 2, res.Attempts)
}

func TestCompleteStructured_RejectsBadFormat(t *testing.T) {
	c := &scripted{}
	for name, rf := range map[string]llm.ResponseFormat{
		"empty schema": {},
		"not object":   {Schema: json.RawMessage(`[1]`)},
		"bad name":     {Name: "has space", Schema: json.RawMessage(verdictSchema)},
		"bad schema":   {Schema: json.RawMessage(`{"type":"nonsense"}`)},
		"remote ref":   {Schema: json.RawMessage(`{"$ref":"https://example.invalid/s.json"}`)},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := llm.CompleteStructured(context.Background(), c, userReq(), rf)
			require.Error(t, err)
			assert.False(t, errors.Is(err, llm.ErrSchemaInvalid))
		})
	}
	assert.Empty(t, c.seen, "nothing is sent for an unusable format")
}

func TestCompleteStructured_CompletionErrorKeepsSpend(t *testing.T) {
	c := &scripted{replies: []string{`{}`}} // second call: script exhausted
	res, err := llm.CompleteStructured(context.Background(), c, userReq(), verdictFormat())
	require.Error(t, err)
	assert.False(t, errors.Is(err, llm.ErrSchemaInvalid))
	assert.Equal(t, 15, res.Usage.TotalTokens)
	assert.Equal(t, 2, res.Attempts)
}

func TestCompleteStructured_CanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	c := &scripted{}
	_, err := llm.CompleteStructured(ctx, c, userReq(), verdictFormat())
	assert.ErrorIs(t, err, context.Canceled)
	assert.Empty(t, c.seen)
}

type verdict struct {
	Label string `json:"label" jsonschema:"enum=pass,enum=fail"`
	Score int    `json:"score"`
	Note  string `json:"note,omitempty"`
}

func TestCompleteInto(t *testing.T) {
	c := &scripted{replies: []string{`{"label":"maybe","score":1}`, `{"label":"pass","score":4,"note":"ok"}`}}
	v, res, err := llm.CompleteInto[verdict](context.Background(), c, userReq(), llm.Strict())
	require.NoError(t, err)
	assert.Equal(t, verdict{Label: "pass", Score: 4, Note: "ok"}, v)
	assert.Equal(t, 2, res.Attempts)

	rf := c.seen[0].ResponseFormat
	require.NotNil(t, rf)
	assert.Equal(t, "verdict", rf.Name)
	assert.True(t, rf.Strict)
	var doc map[string]any
	require.NoError(t, json.Unmarshal(rf.Schema, &doc))
	assert.Equal(t, "object", doc["type"])
	assert.Equal(t, false, doc["additionalProperties"])
	assert.ElementsMatch(t, []any{"label", "score"}, doc["required"], "omitempty is optional")
	assert.NotContains(t, doc, "$schema")
	assert.NotContains(t, doc, "$ref")
}

func TestCompleteInto_Pointer(t *testing.T) {
	c := &scripted{replies: []string{`{"label":"pass","score":4}`}}
	v, _, err := llm.CompleteInto[*verdict](context.Background(), c, userReq())
	require.NoError(t, err)
	require.NotNil(t, v)
	assert.Equal(t, "pass", v.Label)
}

func TestCompleteInto_NotAStruct(t *testing.T) {
	_, _, err := llm.CompleteInto[int](context.Background(), &scripted{}, userReq())
	require.Error(t, err)
	_, _, err = llm.CompleteInto[any](context.Background(), &scripted{}, userReq())
	require.Error(t, err)
}

func TestCompleteInto_SchemaError(t *testing.T) {
	c := &scripted{replies: []string{`x`}}
	v, res, err := llm.CompleteInto[verdict](context.Background(), c, userReq(), llm.MaxRepairs(0))
	assert.True(t, errors.Is(err, llm.ErrSchemaInvalid))
	assert.Zero(t, v)
	assert.Equal(t, 15, res.Usage.TotalTokens)
}

func TestValidateJSON(t *testing.T) {
	schema := json.RawMessage(verdictSchema)

	v, err := llm.ValidateJSON(schema, json.RawMessage(`{"label":"pass","score":1}`))
	require.NoError(t, err)
	assert.Empty(t, v)

	v, err = llm.ValidateJSON(schema, json.RawMessage(`{"label":"pass","score":1,"x":1}`))
	require.NoError(t, err)
	require.Len(t, v, 1, "additionalProperties false")

	v, err = llm.ValidateJSON(schema, json.RawMessage(`{"label":1,"score":"a"}`))
	require.NoError(t, err)
	assert.Len(t, v, 2)
	assert.IsIncreasing(t, v, "sorted")

	v, err = llm.ValidateJSON(schema, json.RawMessage(`{"label":"pass","score":1} trailing`))
	require.NoError(t, err)
	require.Len(t, v, 1)
	assert.Contains(t, v[0], "not valid JSON")

	v, err = llm.ValidateJSON(schema, nil)
	require.NoError(t, err)
	require.Len(t, v, 1)

	_, err = llm.ValidateJSON(json.RawMessage(`{"type":`), json.RawMessage(`{}`))
	require.Error(t, err)
}

func TestValidateJSON_LocalRefs(t *testing.T) {
	schema := json.RawMessage(`{"type":"object","properties":{"a":{"$ref":"#/$defs/n"}},
		"$defs":{"n":{"type":"integer"}}}`)
	v, err := llm.ValidateJSON(schema, json.RawMessage(`{"a":"x"}`))
	require.NoError(t, err)
	assert.Len(t, v, 1)
}

func TestValidateJSON_LargeIntegersExact(t *testing.T) {
	schema := json.RawMessage(`{"type":"object","properties":{"n":{"type":"integer"}}}`)
	v, err := llm.ValidateJSON(schema, json.RawMessage(`{"n":12345678901234567890}`))
	require.NoError(t, err)
	assert.Empty(t, v)
}

func TestCheckResponseFormat(t *testing.T) {
	assert.NoError(t, llm.CheckResponseFormat(nil))
	assert.NoError(t, llm.CheckResponseFormat(&llm.ResponseFormat{Schema: json.RawMessage(`{}`)}))
	assert.Error(t, llm.CheckResponseFormat(&llm.ResponseFormat{Name: "a/b", Schema: json.RawMessage(`{}`)}))
	assert.Error(t, llm.CheckResponseFormat(&llm.ResponseFormat{}))
}

func TestRequestStarted_FlagsResponseFormatWithoutSchema(t *testing.T) {
	b := bus.New()
	defer func() { _ = b.Close(context.Background()) }()
	got := capture(t, b, string(llm.TopicRequestStart))

	c := llm.NewClient(&fullProvider{resp: llm.Response{Content: `{"label":"pass","score":1}`}}, llm.WithBus(b))
	_, err := llm.CompleteStructured(context.Background(), c, userReq(),
		llm.ResponseFormat{Schema: json.RawMessage(`{"type":"object","title":"hunter2"}`)})
	require.NoError(t, err)

	require.Len(t, got(), 1)
	p, ok := got()[0].(llm.RequestStartPayload)
	require.True(t, ok)
	assert.True(t, p.ResponseFormat)
	raw, err := json.Marshal(p)
	require.NoError(t, err)
	assert.Contains(t, string(raw), `"response_format":true`)
	assert.NotContains(t, string(raw), "hunter2", "the schema is not published")

	// Absent on a plain request: the payload is unchanged.
	_, err = c.Complete(context.Background(), userReq())
	require.NoError(t, err)
	raw, err = json.Marshal(got()[1])
	require.NoError(t, err)
	assert.NotContains(t, string(raw), "response_format")
}

func TestCloneRequestKeepsResponseFormatApart(t *testing.T) {
	var seen *llm.ResponseFormat
	b := bus.New()
	defer func() { _ = b.Close(context.Background()) }()
	c := llm.NewClient(&fullProvider{}, llm.WithBus(b), llm.WithRequestContent(func(r llm.Request) llm.Request {
		seen = r.ResponseFormat
		r.ResponseFormat.Schema[0] = 'X' // a transform may rewrite its copy
		return r
	}))
	rf := &llm.ResponseFormat{Schema: json.RawMessage(`{}`)}
	_, err := c.Complete(context.Background(), llm.Request{ResponseFormat: rf})
	require.NoError(t, err)
	require.NotNil(t, seen)
	assert.Equal(t, `{}`, string(rf.Schema), "the caller's schema is untouched")
}
