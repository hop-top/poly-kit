// Package strategy runs a model several times and combines the results:
// self-consistency voting, best-of-n selection and cheap-to-strong
// cascades.
//
// A [Sampler] is one call: the caller closes over the client, request and
// model, and gets the call index so it can vary temperature, seed or model
// per call. [StructuredSampler] builds one over [llm.CompleteStructured].
// Strategies never touch a client, so they work over any backend and any
// answer shape.
//
// Calls run one after another, in order, so a run is deterministic and a
// canceled context stops it before the next call. A sampler or scorer
// error ends the run: the returned [Result] still holds what was spent and
// collected so far, so a caller can account for it.
//
// Judgments with probabilities over a label set belong to
// go/ai/judge/llmjudge, which has its own votes mode.
package strategy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"

	"hop.top/kit/go/ai/llm"
)

var (
	// ErrBadN reports a sample count below 1.
	ErrBadN = errors.New("strategy: n must be at least 1")
	// ErrNoScorer reports a nil score function.
	ErrNoScorer = errors.New("strategy: nil score function")
	// ErrBadScore reports a score that is NaN.
	ErrBadScore = errors.New("strategy: score is not a number")
)

// Candidate is one sampled answer.
type Candidate struct {
	// Response is the model reply. Its Usage is the whole spend of the
	// call, repairs included.
	Response llm.Response
	// Data is the parsed answer, when the sampler produced one.
	Data json.RawMessage
	// Key groups equal answers; votes count per Key. Empty is a key like
	// any other.
	Key string
	// Confidence is the sampler's own estimate in [0,1], read by a
	// cascade's Accept; zero when unknown. [BestOfN] sets it to the score.
	Confidence float64
}

// Sampler makes call i of a run; i counts from 0 (from the stage index in
// a [Cascade]).
type Sampler func(ctx context.Context, i int) (Candidate, error)

// Result is what a strategy returns, also alongside an error.
type Result struct {
	// Winner is the chosen candidate; zero when none was chosen.
	Winner Candidate
	// Tally is the vote count per Key; Shares each Key's share of All.
	// Set by [SelfConsistency] and [Vote] only.
	Tally  map[string]int
	Shares map[string]float64
	// All is every candidate collected, in call order.
	All []Candidate
	// Usage sums the Usage of every call made, failed ones included.
	Usage llm.Usage
	// Calls is the number of sampler calls made, failed ones included;
	// scorer calls are not counted.
	Calls int
}

// SelfConsistency samples n times and returns the Key most candidates
// agree on. A tie goes to the Key seen first; Winner is the first
// candidate with that Key. Shares are vote probabilities. n < 1 returns
// [ErrBadN].
func SelfConsistency(ctx context.Context, s Sampler, n int) (Result, error) {
	if n < 1 {
		return Result{}, ErrBadN
	}
	var r Result
	if err := collect(ctx, s, n, &r); err != nil {
		return r, err
	}
	return vote(r.All, r.Usage, r.Calls), nil
}

// Vote tallies cs as [SelfConsistency] does, with no calls made.
// Usage sums cs's Response.Usage and Calls is len(cs).
func Vote(cs []Candidate) Result {
	var u llm.Usage
	for _, c := range cs {
		u = addUsage(u, c.Response.Usage)
	}
	return vote(cs, u, len(cs))
}

func vote(cs []Candidate, u llm.Usage, calls int) Result {
	r := Result{All: cs, Usage: u, Calls: calls}
	if len(cs) == 0 {
		return r
	}
	r.Tally = make(map[string]int)
	r.Shares = make(map[string]float64)
	max := 0
	for _, c := range cs {
		r.Tally[c.Key]++
		if r.Tally[c.Key] > max {
			max = r.Tally[c.Key]
		}
	}
	// First candidate whose key has the top count: a tie goes to the key
	// seen first.
	for _, c := range cs {
		if r.Tally[c.Key] == max {
			r.Winner = c
			break
		}
	}
	for k, v := range r.Tally {
		r.Shares[k] = float64(v) / float64(len(cs))
	}
	return r
}

// BestOfN samples n times, scores each candidate and returns the highest
// score; a tie goes to the first. The winner's Confidence (and its entry
// in All) is its score. A score error or NaN ends the run with the usage
// so far. n < 1 returns [ErrBadN]; a nil score, [ErrNoScorer]. Tokens a
// scorer spends are not in Usage.
func BestOfN(
	ctx context.Context, s Sampler, n int,
	score func(context.Context, Candidate) (float64, error),
) (Result, error) {
	if n < 1 {
		return Result{}, ErrBadN
	}
	if score == nil {
		return Result{}, ErrNoScorer
	}
	var r Result
	best := -1
	for i := 0; i < n; i++ {
		if err := ctx.Err(); err != nil {
			return r, err
		}
		c, err := call(ctx, s, i, &r)
		if err != nil {
			return r, err
		}
		sc, err := score(ctx, c)
		if err != nil {
			return r, fmt.Errorf("strategy: score candidate %d: %w", i, err)
		}
		if math.IsNaN(sc) {
			return r, fmt.Errorf("%w: candidate %d", ErrBadScore, i)
		}
		c.Confidence = sc
		r.All = append(r.All, c)
		if best < 0 || sc > r.All[best].Confidence {
			best = len(r.All) - 1
		}
	}
	r.Winner = r.All[best]
	return r, nil
}

// collect makes n calls into r.
func collect(ctx context.Context, s Sampler, n int, r *Result) error {
	for i := 0; i < n; i++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		c, err := call(ctx, s, i, r)
		if err != nil {
			return err
		}
		r.All = append(r.All, c)
	}
	return nil
}

// call makes call i, counting it and its usage in r even when it fails.
func call(ctx context.Context, s Sampler, i int, r *Result) (Candidate, error) {
	c, err := s(ctx, i)
	r.Calls++
	r.Usage = addUsage(r.Usage, c.Response.Usage)
	return c, err
}

func addUsage(a, b llm.Usage) llm.Usage {
	return llm.Usage{
		PromptTokens:        a.PromptTokens + b.PromptTokens,
		CompletionTokens:    a.CompletionTokens + b.CompletionTokens,
		CacheReadTokens:     a.CacheReadTokens + b.CacheReadTokens,
		CacheCreationTokens: a.CacheCreationTokens + b.CacheCreationTokens,
		TotalTokens:         a.TotalTokens + b.TotalTokens,
	}
}
