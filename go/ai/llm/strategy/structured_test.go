package strategy_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"hop.top/kit/go/ai/llm"
	"hop.top/kit/go/ai/llm/strategy"
)

type fakeCompleter struct {
	replies []string
	reqs    []llm.Request
}

func (f *fakeCompleter) Complete(_ context.Context, req llm.Request) (llm.Response, error) {
	i := len(f.reqs)
	f.reqs = append(f.reqs, req)
	return llm.Response{
		Content: f.replies[i%len(f.replies)],
		Usage:   llm.Usage{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15},
	}, nil
}

var answerSchema = json.RawMessage(`{"type":"object","properties":{"answer":{"type":"string"}},"required":["answer"],"additionalProperties":false}`)

func TestStructuredSamplerDefaultKey(t *testing.T) {
	fc := &fakeCompleter{replies: []string{`{ "answer": "42" }`}}
	s := strategy.StructuredSampler(fc, llm.Request{Messages: []llm.Message{{Role: "user", Content: "q"}}},
		llm.ResponseFormat{Name: "ans", Schema: answerSchema}, nil)
	c, err := s(context.Background(), 0)
	if err != nil {
		t.Fatal(err)
	}
	if c.Key != `{"answer":"42"}` || string(c.Data) != `{ "answer": "42" }` {
		t.Fatalf("key=%q data=%q", c.Key, c.Data)
	}
	if c.Response.Usage.TotalTokens != 15 || fc.reqs[0].ResponseFormat == nil {
		t.Fatalf("usage=%+v rf=%v", c.Response.Usage, fc.reqs[0].ResponseFormat)
	}
}

func TestStructuredSamplerKeyAndVote(t *testing.T) {
	fc := &fakeCompleter{replies: []string{`{"answer":"a"}`, `{"answer":"b"}`, `{"answer":"a"}`}}
	key := func(d json.RawMessage) string {
		var v struct{ Answer string }
		_ = json.Unmarshal(d, &v)
		return v.Answer
	}
	s := strategy.StructuredSampler(fc, llm.Request{}, llm.ResponseFormat{Schema: answerSchema}, key)
	r, err := strategy.SelfConsistency(context.Background(), s, 3)
	if err != nil {
		t.Fatal(err)
	}
	if r.Winner.Key != "a" || r.Tally["a"] != 2 || r.Usage.TotalTokens != 45 {
		t.Fatalf("%+v", r)
	}
}

func TestStructuredSamplerRepairUsageCounted(t *testing.T) {
	fc := &fakeCompleter{replies: []string{`not json`, `{"answer":"ok"}`}}
	s := strategy.StructuredSampler(fc, llm.Request{}, llm.ResponseFormat{Schema: answerSchema}, nil)
	c, err := s(context.Background(), 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(fc.reqs) != 2 || c.Response.Usage.TotalTokens != 30 {
		t.Fatalf("repair spend must be in the candidate: reqs=%d usage=%+v", len(fc.reqs), c.Response.Usage)
	}
}

func TestStructuredSamplerSchemaErrorKeepsUsage(t *testing.T) {
	fc := &fakeCompleter{replies: []string{`nope`}}
	s := strategy.StructuredSampler(fc, llm.Request{}, llm.ResponseFormat{Schema: answerSchema}, nil)
	r, err := strategy.SelfConsistency(context.Background(), s, 3)
	if !errors.Is(err, llm.ErrSchemaInvalid) {
		t.Fatalf("err=%v", err)
	}
	if r.Calls != 1 || r.Usage.TotalTokens != 45 {
		t.Fatalf("failed sample spend must be accounted: %+v", r)
	}
}
