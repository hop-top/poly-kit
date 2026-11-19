package calibrate

import (
	"context"

	"hop.top/kit/go/ai/judge"
)

// Calibrated wraps j so every returned Answer is calibrated per p. The
// result also implements judge.BatchJudge when j does. Invalid params are
// reported on the first call, not at wrap time.
func Calibrated(j judge.Judge, p Params) judge.Judge {
	c := &calibrated{inner: j, params: p}
	if bj, ok := j.(judge.BatchJudge); ok {
		return &calibratedBatch{calibrated: c, batch: bj}
	}
	return c
}

type calibrated struct {
	inner  judge.Judge
	params Params
}

func (c *calibrated) Judge(ctx context.Context, cs judge.Case) (judge.Verdict, error) {
	if err := c.params.Validate(); err != nil {
		return judge.Verdict{}, err
	}
	v, err := c.inner.Judge(ctx, cs)
	if err != nil {
		return v, err
	}
	return c.apply(cs, v)
}

func (c *calibrated) apply(cs judge.Case, v judge.Verdict) (judge.Verdict, error) {
	out := v
	out.Answers = make(map[string]judge.Answer, len(v.Answers))
	for k, a := range v.Answers {
		out.Answers[k] = a
	}
	for _, q := range cs.Questions {
		a, ok := v.Answers[q.Name]
		if !ok {
			continue
		}
		na, err := c.params.Apply(q, a)
		if err != nil {
			return judge.Verdict{}, err
		}
		out.Answers[q.Name] = na
	}
	return out, nil
}

type calibratedBatch struct {
	*calibrated
	batch judge.BatchJudge
}

func (c *calibratedBatch) JudgeBatch(ctx context.Context, cs []judge.Case) ([]judge.Verdict, error) {
	if err := c.params.Validate(); err != nil {
		return nil, err
	}
	vs, err := c.batch.JudgeBatch(ctx, cs)
	if err != nil {
		return nil, err
	}
	if len(vs) != len(cs) {
		return vs, nil // length mismatch is Batch's to report
	}
	out := make([]judge.Verdict, len(vs))
	for i := range vs {
		if out[i], err = c.apply(cs[i], vs[i]); err != nil {
			return nil, err
		}
	}
	return out, nil
}
