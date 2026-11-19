package strategy_test

import (
	"context"
	"errors"
	"math"
	"testing"

	"hop.top/kit/go/ai/llm"
	"hop.top/kit/go/ai/llm/strategy"
)

// seq returns a sampler yielding keys in order, 10 prompt + 5 completion
// tokens per call, and counts calls.
func seq(keys []string, calls *int) strategy.Sampler {
	return func(_ context.Context, i int) (strategy.Candidate, error) {
		*calls++
		k := keys[i%len(keys)]
		return strategy.Candidate{
			Key: k,
			Response: llm.Response{
				Content: k,
				Usage:   llm.Usage{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15},
			},
		}, nil
	}
}

func TestSelfConsistencyMajority(t *testing.T) {
	var calls int
	r, err := strategy.SelfConsistency(context.Background(), seq([]string{"a", "b", "a", "c", "a"}, &calls), 5)
	if err != nil {
		t.Fatal(err)
	}
	if r.Winner.Key != "a" || r.Tally["a"] != 3 || r.Tally["b"] != 1 || r.Tally["c"] != 1 {
		t.Fatalf("winner/tally: %+v", r)
	}
	if math.Abs(r.Shares["a"]-0.6) > 1e-9 || math.Abs(r.Shares["b"]-0.2) > 1e-9 {
		t.Fatalf("shares: %v", r.Shares)
	}
	if r.Calls != 5 || calls != 5 || len(r.All) != 5 {
		t.Fatalf("calls=%d/%d all=%d", r.Calls, calls, len(r.All))
	}
	if r.Usage.PromptTokens != 50 || r.Usage.CompletionTokens != 25 || r.Usage.TotalTokens != 75 {
		t.Fatalf("usage not summed: %+v", r.Usage)
	}
}

func TestVoteTieFirstSeen(t *testing.T) {
	cs := []strategy.Candidate{{Key: "x", Response: llm.Response{Content: "first x"}}, {Key: "y"}, {Key: "y"}, {Key: "x"}}
	r := strategy.Vote(cs)
	if r.Winner.Key != "x" || r.Winner.Response.Content != "first x" {
		t.Fatalf("tie must go to first seen, and Winner is its first candidate: %+v", r.Winner)
	}
	cs = []strategy.Candidate{{Key: "y"}, {Key: "x"}, {Key: "x"}, {Key: "y"}}
	if w := strategy.Vote(cs).Winner.Key; w != "y" {
		t.Fatalf("winner = %q, want y", w)
	}
}

func TestVoteEmpty(t *testing.T) {
	r := strategy.Vote(nil)
	if r.Winner.Key != "" || len(r.All) != 0 || len(r.Tally) != 0 || r.Calls != 0 {
		t.Fatalf("%+v", r)
	}
}

func TestSelfConsistencyN1EqualsSingleCall(t *testing.T) {
	var calls int
	r, err := strategy.SelfConsistency(context.Background(), seq([]string{"only"}, &calls), 1)
	if err != nil {
		t.Fatal(err)
	}
	if calls != 1 || r.Calls != 1 || r.Winner.Key != "only" || r.Shares["only"] != 1 || r.Usage.TotalTokens != 15 {
		t.Fatalf("calls=%d %+v", calls, r)
	}
}

func TestBadN(t *testing.T) {
	var calls int
	s := seq([]string{"a"}, &calls)
	for _, n := range []int{0, -1} {
		if _, err := strategy.SelfConsistency(context.Background(), s, n); !errors.Is(err, strategy.ErrBadN) {
			t.Fatalf("n=%d err=%v", n, err)
		}
		if _, err := strategy.BestOfN(context.Background(), s, n, nil); !errors.Is(err, strategy.ErrBadN) {
			t.Fatalf("bestofn n=%d err=%v", n, err)
		}
	}
	if calls != 0 {
		t.Fatalf("sampler called %d times", calls)
	}
}

func TestSamplerErrorReturnsPartial(t *testing.T) {
	boom := errors.New("boom")
	s := func(_ context.Context, i int) (strategy.Candidate, error) {
		if i == 2 {
			return strategy.Candidate{Response: llm.Response{Usage: llm.Usage{TotalTokens: 7}}}, boom
		}
		return strategy.Candidate{Key: "a", Response: llm.Response{Usage: llm.Usage{TotalTokens: 15}}}, nil
	}
	r, err := strategy.SelfConsistency(context.Background(), s, 5)
	if !errors.Is(err, boom) {
		t.Fatalf("err=%v", err)
	}
	if r.Calls != 3 || len(r.All) != 2 || r.Usage.TotalTokens != 37 {
		t.Fatalf("partial: calls=%d all=%d usage=%+v", r.Calls, len(r.All), r.Usage)
	}
}

func TestContextCancelMidRun(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	var calls int
	s := func(_ context.Context, i int) (strategy.Candidate, error) {
		calls++
		if i == 1 {
			cancel()
		}
		return strategy.Candidate{Key: "a"}, nil
	}
	r, err := strategy.SelfConsistency(ctx, s, 10)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err=%v", err)
	}
	if calls != 2 || r.Calls != 2 || len(r.All) != 2 {
		t.Fatalf("calls=%d result=%+v", calls, r)
	}
}

func TestBestOfNPicksMax(t *testing.T) {
	var calls int
	scores := map[string]float64{"a": 0.2, "b": 0.9, "c": 0.9, "d": 0.1}
	r, err := strategy.BestOfN(context.Background(), seq([]string{"a", "b", "c", "d"}, &calls), 4,
		func(_ context.Context, c strategy.Candidate) (float64, error) { return scores[c.Key], nil })
	if err != nil {
		t.Fatal(err)
	}
	if r.Winner.Key != "b" {
		t.Fatalf("max score tie goes to first seen: %q", r.Winner.Key)
	}
	if r.Winner.Confidence != 0.9 {
		t.Fatalf("winner confidence = score: %v", r.Winner.Confidence)
	}
	if r.Calls != 4 || len(r.All) != 4 || r.Usage.TotalTokens != 60 {
		t.Fatalf("%+v", r)
	}
}

func TestBestOfNScoreError(t *testing.T) {
	var calls int
	boom := errors.New("judge down")
	r, err := strategy.BestOfN(context.Background(), seq([]string{"a", "b", "c"}, &calls), 3,
		func(_ context.Context, c strategy.Candidate) (float64, error) {
			if c.Key == "b" {
				return 0, boom
			}
			return 1, nil
		})
	if !errors.Is(err, boom) {
		t.Fatalf("err=%v", err)
	}
	if calls != 2 || r.Calls != 2 || r.Usage.TotalTokens != 30 {
		t.Fatalf("stops at the failing score: calls=%d %+v", calls, r)
	}
}

func TestBestOfNNaNIsError(t *testing.T) {
	var calls int
	_, err := strategy.BestOfN(context.Background(), seq([]string{"a"}, &calls), 1,
		func(context.Context, strategy.Candidate) (float64, error) { return math.NaN(), nil })
	if !errors.Is(err, strategy.ErrBadScore) {
		t.Fatalf("err=%v", err)
	}
}

func TestBestOfNNilScore(t *testing.T) {
	var calls int
	if _, err := strategy.BestOfN(context.Background(), seq([]string{"a"}, &calls), 1, nil); !errors.Is(err, strategy.ErrNoScorer) {
		t.Fatalf("err=%v", err)
	}
}

func TestCascadeStopsAtFirstAccepted(t *testing.T) {
	var c0, c1, c2 int
	mk := func(calls *int, conf float64) strategy.Sampler {
		return func(_ context.Context, _ int) (strategy.Candidate, error) {
			*calls++
			return strategy.Candidate{Key: "k", Confidence: conf, Response: llm.Response{Usage: llm.Usage{TotalTokens: 10}}}, nil
		}
	}
	atLeast := func(min float64) func(strategy.Candidate) bool {
		return func(c strategy.Candidate) bool { return c.Confidence >= min }
	}
	r, err := strategy.Cascade(context.Background(),
		strategy.Stage{Sampler: mk(&c0, 0.3), Accept: atLeast(0.8)},
		strategy.Stage{Sampler: mk(&c1, 0.9), Accept: atLeast(0.8)},
		strategy.Stage{Sampler: mk(&c2, 1), Accept: atLeast(0.8)})
	if err != nil {
		t.Fatal(err)
	}
	if c0 != 1 || c1 != 1 || c2 != 0 {
		t.Fatalf("calls %d %d %d", c0, c1, c2)
	}
	if r.Winner.Confidence != 0.9 || r.Calls != 2 || len(r.All) != 2 || r.Usage.TotalTokens != 20 {
		t.Fatalf("%+v", r)
	}
}

func TestCascadeNilAcceptTakesFirst(t *testing.T) {
	var calls int
	r, err := strategy.Cascade(context.Background(),
		strategy.Stage{Sampler: seq([]string{"a"}, &calls)},
		strategy.Stage{Sampler: seq([]string{"b"}, &calls)})
	if err != nil || r.Winner.Key != "a" || calls != 1 {
		t.Fatalf("err=%v calls=%d %+v", err, calls, r)
	}
}

func TestCascadeExhausted(t *testing.T) {
	var calls int
	never := func(strategy.Candidate) bool { return false }
	r, err := strategy.Cascade(context.Background(),
		strategy.Stage{Sampler: seq([]string{"a"}, &calls), Accept: never},
		strategy.Stage{Sampler: seq([]string{"b"}, &calls), Accept: never})
	if !errors.Is(err, strategy.ErrNoneAccepted) {
		t.Fatalf("err=%v", err)
	}
	if r.Winner.Key != "b" || len(r.All) != 2 || r.Usage.TotalTokens != 30 {
		t.Fatalf("last candidate is returned with the error: %+v", r)
	}
}

func TestCascadeNoStages(t *testing.T) {
	if _, err := strategy.Cascade(context.Background()); !errors.Is(err, strategy.ErrNoStages) {
		t.Fatalf("err=%v", err)
	}
	if _, err := strategy.Cascade(context.Background(), strategy.Stage{}); !errors.Is(err, strategy.ErrNoStages) {
		t.Fatalf("nil sampler: err=%v", err)
	}
}

func TestCascadeStageErrorAborts(t *testing.T) {
	boom := errors.New("boom")
	var calls int
	_, err := strategy.Cascade(context.Background(),
		strategy.Stage{Sampler: func(context.Context, int) (strategy.Candidate, error) { return strategy.Candidate{}, boom }},
		strategy.Stage{Sampler: seq([]string{"b"}, &calls)})
	if !errors.Is(err, boom) || calls != 0 {
		t.Fatalf("err=%v calls=%d", err, calls)
	}
}

func TestCascadeContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	var calls int
	never := func(strategy.Candidate) bool { return false }
	first := func(ctx context.Context, i int) (strategy.Candidate, error) {
		cancel()
		return strategy.Candidate{Key: "a"}, nil
	}
	_, err := strategy.Cascade(ctx,
		strategy.Stage{Sampler: first, Accept: never},
		strategy.Stage{Sampler: seq([]string{"b"}, &calls)})
	if !errors.Is(err, context.Canceled) || calls != 0 {
		t.Fatalf("err=%v calls=%d", err, calls)
	}
}
