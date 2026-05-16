package hay

import (
	"fmt"
	"sort"
)

type AmbiguousAction int

const (
	ActionList AmbiguousAction = iota
	ActionPick
)

type Policy struct {
	Action AmbiguousAction
	Fail   bool
}

type ScoreFn[T any] func(query string, item T) int

type StaleFn[T any] func(item T) bool

type BonusFn[T any] func(item T) int

type Scored[T any] struct {
	Item  T
	Score int
}

type Result[T any] struct {
	Winner     T
	Candidates []Scored[T]
	Stale      []T
	Ambiguous  bool
}

type Options[T any] struct {
	Score         ScoreFn[T]
	Stale         StaleFn[T]
	Policy        Policy
	TieMargin     int
	MaxCandidates int
	Bonus         BonusFn[T]
}

type ErrAmbiguous[T any] struct {
	Query      string
	Candidates []Scored[T]
	Stale      []T
}

func (e *ErrAmbiguous[T]) Error() string {
	return fmt.Sprintf("hay: %q matches %d candidates", e.Query, len(e.Candidates))
}

type ErrNoMatch struct {
	Query string
	Stale int
}

func (e *ErrNoMatch) Error() string {
	if e.Stale > 0 {
		return fmt.Sprintf("hay: no match for %q (%d stale entries skipped)", e.Query, e.Stale)
	}
	return fmt.Sprintf("hay: no match for %q", e.Query)
}

type ErrVanished struct {
	Query string
	Path  string
}

func (e *ErrVanished) Error() string {
	return fmt.Sprintf("hay: matched file disappeared during lookup for %q: %s", e.Query, e.Path)
}

func Resolve[T any](query string, corpus []T, opts Options[T]) (Result[T], error) {
	var stale []T
	live := corpus

	if opts.Stale != nil {
		live = make([]T, 0, len(corpus))
		for _, item := range corpus {
			if opts.Stale(item) {
				stale = append(stale, item)
			} else {
				live = append(live, item)
			}
		}
	}

	var scored []Scored[T]
	for _, item := range live {
		s := opts.Score(query, item)
		if opts.Bonus != nil {
			s += opts.Bonus(item)
		}
		if s > 0 {
			scored = append(scored, Scored[T]{Item: item, Score: s})
		}
	}

	if len(scored) == 0 {
		return Result[T]{}, &ErrNoMatch{Query: query, Stale: len(stale)}
	}

	sort.Slice(scored, func(i, j int) bool {
		return scored[i].Score > scored[j].Score
	})

	ambiguous := len(scored) > 1 && (scored[0].Score-scored[1].Score) < opts.TieMargin

	maxC := opts.MaxCandidates
	if maxC <= 0 {
		maxC = 10
	}

	if ambiguous {
		cands := scored
		if len(cands) > maxC {
			cands = cands[:maxC]
		}

		switch {
		case opts.Policy.Action == ActionList && opts.Policy.Fail:
			return Result[T]{}, &ErrAmbiguous[T]{
				Query:      query,
				Candidates: cands,
				Stale:      stale,
			}
		case opts.Policy.Action == ActionList && !opts.Policy.Fail:
			return Result[T]{
				Winner:     scored[0].Item,
				Candidates: cands,
				Stale:      stale,
				Ambiguous:  true,
			}, nil
		case opts.Policy.Action == ActionPick && opts.Policy.Fail:
			return Result[T]{
				Winner:     scored[0].Item,
				Candidates: cands,
				Stale:      stale,
				Ambiguous:  true,
			}, nil
		default: // ActionPick + !Fail
			return Result[T]{
				Winner:     scored[0].Item,
				Candidates: cands,
				Stale:      stale,
			}, nil
		}
	}

	if len(scored) > maxC {
		scored = scored[:maxC]
	}

	return Result[T]{
		Winner:     scored[0].Item,
		Candidates: scored,
		Stale:      stale,
	}, nil
}
