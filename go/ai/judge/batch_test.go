package judge_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"hop.top/kit/go/ai/judge"
	"hop.top/kit/go/ai/judge/judgetest"
)

func cases(n int) []judge.Case {
	cs := make([]judge.Case, n)
	for i := range cs {
		cs[i] = judge.Case{
			State:     map[string]any{"i": i},
			Questions: []judge.Question{{Name: "q", Kind: judge.KindYesNo}},
		}
	}
	return cs
}

// echo returns a verdict whose Model carries the case index.
func echo(c judge.Case) judge.Verdict {
	return judge.Verdict{Model: fmt.Sprint(c.State["i"])}
}

func TestBatchOrderKeptUnderParallelism(t *testing.T) {
	cs := cases(32)
	f := &judgetest.Fake{Fn: func(ctx context.Context, c judge.Case) (judge.Verdict, error) {
		// later cases finish first
		time.Sleep(time.Duration(32-c.State["i"].(int)) * time.Millisecond)
		return echo(c), nil
	}}
	vs, err := judge.Batch(context.Background(), f, cs, 8)
	if err != nil {
		t.Fatal(err)
	}
	for i, v := range vs {
		if v.Model != fmt.Sprint(i) {
			t.Fatalf("verdict %d = %q: order not kept", i, v.Model)
		}
	}
}

func TestBatchParallelBound(t *testing.T) {
	var cur, peak atomic.Int32
	f := &judgetest.Fake{Fn: func(ctx context.Context, c judge.Case) (judge.Verdict, error) {
		n := cur.Add(1)
		for {
			p := peak.Load()
			if n <= p || peak.CompareAndSwap(p, n) {
				break
			}
		}
		time.Sleep(5 * time.Millisecond)
		cur.Add(-1)
		return echo(c), nil
	}}
	if _, err := judge.Batch(context.Background(), f, cases(20), 3); err != nil {
		t.Fatal(err)
	}
	if got := peak.Load(); got != 3 {
		t.Fatalf("peak concurrency = %d, want 3", got)
	}
	peak.Store(0)
	if _, err := judge.Batch(context.Background(), f, cases(5), 0); err != nil {
		t.Fatal(err)
	}
	if got := peak.Load(); got != 1 {
		t.Fatalf("parallel=0 peak = %d, want 1", got)
	}
}

func TestBatchJudgePath(t *testing.T) {
	bf := judgetest.NewBatch(&judgetest.Fake{Fn: func(_ context.Context, c judge.Case) (judge.Verdict, error) {
		return echo(c), nil
	}})
	vs, err := judge.Batch(context.Background(), bf, cases(6), 4)
	if err != nil {
		t.Fatal(err)
	}
	if got := bf.Batches(); len(got) != 1 || got[0] != 6 {
		t.Fatalf("batches = %v, want [6]", got)
	}
	for i, v := range vs {
		if v.Model != fmt.Sprint(i) {
			t.Fatalf("verdict %d = %q", i, v.Model)
		}
	}
}

type shortBatch struct{}

func (shortBatch) Judge(context.Context, judge.Case) (judge.Verdict, error) {
	return judge.Verdict{}, nil
}
func (shortBatch) JudgeBatch(context.Context, []judge.Case) ([]judge.Verdict, error) {
	return []judge.Verdict{{}}, nil
}

func TestBatchJudgeLengthMismatch(t *testing.T) {
	if _, err := judge.Batch(context.Background(), shortBatch{}, cases(3), 1); err == nil {
		t.Fatal("want length mismatch error")
	}
}

func TestBatchValidatesFirst(t *testing.T) {
	f := judgetest.Fixed(judge.Verdict{})
	cs := cases(3)
	cs[1].Questions = nil
	_, err := judge.Batch(context.Background(), f, cs, 2)
	if !errors.Is(err, judge.ErrInvalid) {
		t.Fatalf("err = %v, want ErrInvalid", err)
	}
	if n := len(f.Calls()); n != 0 {
		t.Fatalf("%d judge calls despite invalid case", n)
	}
}

func TestBatchFirstErrorCancelsRest(t *testing.T) {
	boom := errors.New("boom")
	var started atomic.Int32
	f := &judgetest.Fake{Fn: func(ctx context.Context, c judge.Case) (judge.Verdict, error) {
		started.Add(1)
		if c.State["i"].(int) == 0 {
			return judge.Verdict{}, boom
		}
		<-ctx.Done()
		return judge.Verdict{}, ctx.Err()
	}}
	_, err := judge.Batch(context.Background(), f, cases(50), 2)
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want boom", err)
	}
	if n := started.Load(); n >= 50 {
		t.Fatalf("all %d cases started despite early error", n)
	}
}

func TestBatchContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	var once sync.Once
	var started atomic.Int32
	f := &judgetest.Fake{Fn: func(ctx context.Context, c judge.Case) (judge.Verdict, error) {
		started.Add(1)
		once.Do(cancel)
		<-ctx.Done()
		return judge.Verdict{}, ctx.Err()
	}}
	_, err := judge.Batch(ctx, f, cases(50), 2)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if n := started.Load(); n >= 50 {
		t.Fatalf("%d cases started after cancel", n)
	}

	pre, precancel := context.WithCancel(context.Background())
	precancel()
	f2 := judgetest.Fixed(judge.Verdict{})
	if _, err := judge.Batch(pre, f2, cases(3), 2); !errors.Is(err, context.Canceled) {
		t.Fatalf("pre-canceled err = %v", err)
	}
	if n := len(f2.Calls()); n != 0 {
		t.Fatalf("%d calls on pre-canceled ctx", n)
	}
}

func TestBatchEmpty(t *testing.T) {
	vs, err := judge.Batch(context.Background(), judgetest.Fixed(judge.Verdict{}), nil, 2)
	if err != nil || len(vs) != 0 {
		t.Fatalf("vs=%v err=%v", vs, err)
	}
}
