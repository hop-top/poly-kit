package strategy

import (
	"context"
	"math"
	"strconv"
	"strings"
)

const (
	defaultSamples = 5
	defaultStages  = 2
	defaultMinConf = 0.8
	maxLookupN     = 1000
)

// Lookup resolves a strategy name, as found in a "strategy:" config string,
// to a runner builder. It reports false for an unknown or malformed name.
// Names are trimmed and case-insensitive:
//
//   - "self-consistency[:N]": [SelfConsistency] over N samples, default 5
//   - "cascade[:N[:MIN]]": [Cascade] over N stages, default 2, each calling
//     the sampler with its stage index and accepting a candidate whose
//     Confidence is at least MIN, default 0.8; the sampler picks the model
//     from the index
//
// N is 1 to 1000; MIN is in [0,1]. [BestOfN] needs a scorer a string cannot
// carry, so it is not here.
func Lookup(name string) (func(Sampler) func(ctx context.Context) (Result, error), bool) {
	parts := strings.Split(strings.ToLower(strings.TrimSpace(name)), ":")
	switch parts[0] {
	case "self-consistency":
		if len(parts) > 2 {
			return nil, false
		}
		n := defaultSamples
		if len(parts) == 2 {
			var ok bool
			if n, ok = parseN(parts[1]); !ok {
				return nil, false
			}
		}
		return func(s Sampler) func(context.Context) (Result, error) {
			return func(ctx context.Context) (Result, error) { return SelfConsistency(ctx, s, n) }
		}, true
	case "cascade":
		if len(parts) > 3 {
			return nil, false
		}
		n, min := defaultStages, defaultMinConf
		if len(parts) >= 2 {
			var ok bool
			if n, ok = parseN(parts[1]); !ok {
				return nil, false
			}
		}
		if len(parts) == 3 {
			f, err := strconv.ParseFloat(parts[2], 64)
			if err != nil || math.IsNaN(f) || f < 0 || f > 1 {
				return nil, false
			}
			min = f
		}
		accept := func(c Candidate) bool { return c.Confidence >= min }
		return func(s Sampler) func(context.Context) (Result, error) {
			stages := make([]Stage, n)
			for i := range stages {
				stages[i] = Stage{Sampler: s, Accept: accept}
			}
			return func(ctx context.Context) (Result, error) { return Cascade(ctx, stages...) }
		}, true
	}
	return nil, false
}

func parseN(s string) (int, bool) {
	n, err := strconv.Atoi(s)
	if err != nil || n < 1 || n > maxLookupN {
		return 0, false
	}
	return n, true
}
