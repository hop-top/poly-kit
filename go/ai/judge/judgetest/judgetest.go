// Package judgetest is a deterministic fake judge.Judge for tests:
// scripted or computed verdicts plus a call log. No network, no clock.
package judgetest

import (
	"context"
	"errors"
	"sync"

	"hop.top/kit/go/ai/judge"
)

// ErrScriptExhausted is returned when a Scripted fake runs out of results.
var ErrScriptExhausted = errors.New("judgetest: script exhausted")

// Result is one scripted outcome.
type Result struct {
	Verdict judge.Verdict
	Err     error
}

// Fake implements judge.Judge. Resolution order per call: Fn if set, else
// the next Script entry, else ErrScriptExhausted. Safe for concurrent use.
type Fake struct {
	// Script results are consumed in call order.
	Script []Result
	// Fn computes a verdict from the case; takes precedence over Script.
	Fn func(ctx context.Context, c judge.Case) (judge.Verdict, error)

	mu    sync.Mutex
	next  int
	calls []judge.Case
}

// Fixed returns a Fake answering every call with v.
func Fixed(v judge.Verdict) *Fake {
	return &Fake{Fn: func(context.Context, judge.Case) (judge.Verdict, error) { return v, nil }}
}

// Scripted returns a Fake answering call n with vs[n].
func Scripted(vs ...judge.Verdict) *Fake {
	f := &Fake{}
	for _, v := range vs {
		f.Script = append(f.Script, Result{Verdict: v})
	}
	return f
}

// Judge records c, then returns the resolved result. A canceled ctx wins
// over scripted results (and is not consumed from the script).
func (f *Fake) Judge(ctx context.Context, c judge.Case) (judge.Verdict, error) {
	if err := ctx.Err(); err != nil {
		return judge.Verdict{}, err
	}
	f.mu.Lock()
	f.calls = append(f.calls, c)
	fn := f.Fn
	var r Result
	var have bool
	if fn == nil && f.next < len(f.Script) {
		r, have = f.Script[f.next], true
		f.next++
	}
	f.mu.Unlock()

	switch {
	case fn != nil:
		return fn(ctx, c)
	case have:
		return r.Verdict, r.Err
	}
	return judge.Verdict{}, ErrScriptExhausted
}

// Calls returns a copy of the call log, in arrival order.
func (f *Fake) Calls() []judge.Case {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]judge.Case(nil), f.calls...)
}

// BatchFake is a Fake that also implements judge.BatchJudge. JudgeBatch
// answers each case through the embedded Fake, in order, and logs one
// batch size.
type BatchFake struct {
	*Fake

	bmu     sync.Mutex
	batches []int
}

// NewBatch wraps f.
func NewBatch(f *Fake) *BatchFake { return &BatchFake{Fake: f} }

// JudgeBatch implements judge.BatchJudge.
func (b *BatchFake) JudgeBatch(ctx context.Context, cs []judge.Case) ([]judge.Verdict, error) {
	b.bmu.Lock()
	b.batches = append(b.batches, len(cs))
	b.bmu.Unlock()
	out := make([]judge.Verdict, len(cs))
	for i, c := range cs {
		v, err := b.Judge(ctx, c)
		if err != nil {
			return nil, err
		}
		out[i] = v
	}
	return out, nil
}

// Batches returns the size of each JudgeBatch call, in order.
func (b *BatchFake) Batches() []int {
	b.bmu.Lock()
	defer b.bmu.Unlock()
	return append([]int(nil), b.batches...)
}

// Choice builds a valid KindChoice/KindYesNo answer: Value is the argmax
// of probs over order (ties to earlier), Confidence = P(Value).
func Choice(probs map[string]float64, order ...string) judge.Answer {
	v := judge.Argmax(probs, order)
	return judge.Answer{
		Value: v, Abstained: v == judge.LabelAbstain,
		Probs: probs, Confidence: probs[v], Source: "judgetest",
	}
}

// Score builds a valid KindScore answer over n anchors; probs keys are
// anchor indices. Value is the modal index; Score the expected value.
func Score(probs map[string]float64, n int) judge.Answer {
	order := judge.Labels(judge.Question{Kind: judge.KindScore, Anchors: make([]string, n)})
	v := judge.Argmax(probs, order)
	return judge.Answer{
		Value: v, Probs: probs, Confidence: probs[v],
		Score: judge.ScoreEV(probs, n), Source: "judgetest",
	}
}

// Abstain builds an abstained answer.
func Abstain() judge.Answer {
	return judge.Answer{
		Value: judge.LabelAbstain, Abstained: true,
		Probs:      map[string]float64{judge.LabelAbstain: 1},
		Confidence: 1, Source: "judgetest",
	}
}

// Grounding builds a valid located KindGrounding answer; conf 0 is unknown.
func Grounding(b judge.Box, conf float64) judge.Answer {
	return judge.Answer{Box: &b, Confidence: conf, Source: "judgetest"}
}

// GroundingAbstain builds an abstained KindGrounding answer (target absent).
func GroundingAbstain() judge.Answer {
	return judge.Answer{Value: judge.LabelAbstain, Abstained: true, Source: "judgetest"}
}
