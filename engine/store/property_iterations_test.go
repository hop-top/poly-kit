package store

import (
	"fmt"
	"os"
	"strconv"
	"testing"
)

// propertyIterationsEnv pins the iteration count of the four
// cross-backend property tests (TestVersioned_Property,
// TestVersionedBranching_Property, TestVersionedDedup_Property,
// TestVersionedPruning_Property). The Makefile exports it from
// PROPERTY_ITERATIONS on the test-go-integration target; CI sets 100
// on pull requests and leaves it empty (full count) on pushes and the
// nightly run. -short cannot carry that split: it also skips the
// testcontainer and kit-serve suites the integration target exists for.
const propertyIterationsEnv = "KIT_PROPERTY_ITERATIONS"

// shortPropertyIterations is the count under -short, the flag
// `make test-go` and the pre-push hook already pass. One tenth of the
// spec §7 floor keeps the local loop fast while still exercising every
// op kind in each generator.
const shortPropertyIterations = 100

// propertyIterations resolves how many randomized sequences a property
// test runs: propertyIterationsEnv when set, shortPropertyIterations
// under -short, else full (the count each test declares). A malformed
// override fails the test instead of falling back to full: the
// override exists to bound CI wall-clock, and a typo that silently
// restores the full count is the very run it was meant to prevent.
func propertyIterations(t *testing.T, full int) int {
	t.Helper()
	n, err := resolvePropertyIterations(os.Getenv(propertyIterationsEnv), testing.Short(), full)
	if err != nil {
		t.Fatal(err)
	}
	return n
}

// resolvePropertyIterations is the pure core of propertyIterations,
// split out so the precedence is unit-testable without -short.
func resolvePropertyIterations(raw string, short bool, full int) (int, error) {
	if raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n <= 0 {
			return 0, fmt.Errorf("%s=%q: want a positive integer", propertyIterationsEnv, raw)
		}
		return n, nil
	}
	if short {
		return shortPropertyIterations, nil
	}
	return full, nil
}

func TestResolvePropertyIterations(t *testing.T) {
	cases := []struct {
		name  string
		raw   string
		short bool
		want  int
		fail  bool
	}{
		{name: "full by default", raw: "", short: false, want: 1000},
		{name: "short", raw: "", short: true, want: shortPropertyIterations},
		{name: "env wins over full", raw: "7", short: false, want: 7},
		{name: "env wins over short", raw: "2500", short: true, want: 2500},
		{name: "zero rejected", raw: "0", fail: true},
		{name: "negative rejected", raw: "-5", fail: true},
		{name: "garbage rejected", raw: "many", fail: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := resolvePropertyIterations(tc.raw, tc.short, 1000)
			if tc.fail {
				if err == nil {
					t.Fatalf("want error, got %d", got)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got != tc.want {
				t.Fatalf("got %d, want %d", got, tc.want)
			}
		})
	}
}
