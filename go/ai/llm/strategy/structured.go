package strategy

import (
	"bytes"
	"context"
	"encoding/json"

	"hop.top/kit/go/ai/llm"
)

// StructuredSampler samples with [llm.CompleteStructured]: each call asks c
// for JSON matching rf, validated and repaired as that function does, and
// returns it as Data. Key maps Data to the value votes group by; nil keys
// on the compacted JSON text, which treats two objects with the same
// fields in a different order as different answers, so pass a Key that
// picks the field that decides. Every call sends req unchanged; set
// req.Temperature above zero or votes agree trivially.
//
// The Candidate's Response.Usage is the call's whole spend, repair
// attempts included. When the call fails, the returned Candidate still
// carries that usage, which strategies add to Result.Usage.
func StructuredSampler(
	c llm.Completer, req llm.Request, rf llm.ResponseFormat, key func(json.RawMessage) string,
) Sampler {
	return func(ctx context.Context, _ int) (Candidate, error) {
		res, err := llm.CompleteStructured(ctx, c, req, rf)
		resp := res.Response
		resp.Usage = res.Usage
		cand := Candidate{Response: resp, Data: res.Data}
		if err != nil {
			return cand, err
		}
		if key != nil {
			cand.Key = key(res.Data)
		} else {
			cand.Key = compact(res.Data)
		}
		return cand, nil
	}
}

func compact(d json.RawMessage) string {
	var b bytes.Buffer
	if err := json.Compact(&b, d); err != nil {
		return string(d)
	}
	return b.String()
}
