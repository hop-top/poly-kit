package judge

import (
	"context"
	"fmt"
	"sync"
)

// Batch judges cs with j; results[i] answers cs[i].
//
// Every case is Validate-d first; nothing runs if any fails. When j is a
// BatchJudge it gets all cases in one call (parallel ignored) and must
// return len(cs) verdicts. Otherwise up to parallel (min 1) Judge calls run
// concurrently. The first error cancels the remaining work and is returned
// (wrapped with the case index); no partial results. A canceled ctx stops
// dispatch and returns ctx.Err().
func Batch(ctx context.Context, j Judge, cs []Case, parallel int) ([]Verdict, error) {
	if len(cs) == 0 {
		return nil, ctx.Err()
	}
	for i, c := range cs {
		if err := Validate(c); err != nil {
			return nil, fmt.Errorf("case %d: %w", i, err)
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if bj, ok := j.(BatchJudge); ok {
		vs, err := bj.JudgeBatch(ctx, cs)
		if err != nil {
			return nil, err
		}
		if len(vs) != len(cs) {
			return nil, fmt.Errorf("judge: batch returned %d verdicts for %d cases", len(vs), len(cs))
		}
		return vs, nil
	}
	if parallel < 1 {
		parallel = 1
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	out := make([]Verdict, len(cs))
	var (
		wg       sync.WaitGroup
		mu       sync.Mutex
		firstErr error
		sem      = make(chan struct{}, parallel)
	)
	fail := func(err error) {
		mu.Lock()
		if firstErr == nil {
			firstErr = err
			cancel()
		}
		mu.Unlock()
	}
dispatch:
	for i := range cs {
		select {
		case sem <- struct{}{}:
		case <-ctx.Done():
			break dispatch
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			v, err := j.Judge(ctx, cs[i])
			if err != nil {
				fail(fmt.Errorf("case %d: %w", i, err))
				return
			}
			out[i] = v
		}()
	}
	wg.Wait()
	if firstErr != nil {
		return nil, firstErr
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return out, nil
}
