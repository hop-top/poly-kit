package strategy

import (
	"context"
	"errors"
)

var (
	// ErrNoStages reports a cascade with no stage, or a stage with no
	// sampler.
	ErrNoStages = errors.New("strategy: cascade needs at least one stage with a sampler")
	// ErrNoneAccepted reports a cascade whose every stage was rejected. The
	// [Result] holds the last candidate as Winner and all of them in All.
	ErrNoneAccepted = errors.New("strategy: no cascade stage accepted")
)

// Stage is one rung of a [Cascade].
type Stage struct {
	Sampler Sampler
	// Accept decides whether to stop at this stage's candidate; nil
	// accepts every candidate.
	Accept func(Candidate) bool
}

// Cascade tries stages in order, cheapest first, and stops at the first
// candidate its stage accepts; later stages are never called. Each stage is
// called once, with its index as i. When none accepts it returns
// [ErrNoneAccepted] with the last candidate as Winner. A stage error ends
// the cascade rather than escalating: wrap the sampler to turn a failure
// into a low-confidence candidate when escalation is wanted.
//
// A cascade only orders samplers; it does not know where they run. Build a
// stage's client with the locality the caller allows: escalating from a
// local stage to a cloud one under [llm.LocalOnly] is refused when the
// cloud client is resolved, before any stage runs.
func Cascade(ctx context.Context, stages ...Stage) (Result, error) {
	if len(stages) == 0 {
		return Result{}, ErrNoStages
	}
	for _, st := range stages {
		if st.Sampler == nil {
			return Result{}, ErrNoStages
		}
	}
	var r Result
	for i, st := range stages {
		if err := ctx.Err(); err != nil {
			return r, err
		}
		c, err := call(ctx, st.Sampler, i, &r)
		if err != nil {
			return r, err
		}
		r.All = append(r.All, c)
		r.Winner = c
		if st.Accept == nil || st.Accept(c) {
			return r, nil
		}
	}
	return r, ErrNoneAccepted
}
