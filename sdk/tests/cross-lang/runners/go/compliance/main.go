// Command compliance is the Go runner for the cross-language compliance
// conformance harness.
//
// Go is the REFERENCE implementation: F13 ("Consenting Telemetry") landed
// here first and the TS / Python ports were aligned to it. The runner runs
// the checker against the shared opt-in fixture and emits the observed
// score, denominator, and per-factor status as a single stable JSON object
// to KIT_CROSS_LANG_COMPLIANCE_OUT.
//
// Only the STATIC pass runs (empty binary path). Runtime checks execute a
// binary, which no port could agree on across languages, and F13 is a
// static check in every port anyway.
//
// Keys are emitted sorted, and `factors` is an object keyed by factor
// number rather than a list, so a port that reorders its results without
// changing any status still compares equal. Order is not the subject here
// — the score, the denominator, and the per-factor verdicts are.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"

	"hop.top/kit/go/core/compliance"
)

type observation struct {
	Lang    string            `json:"lang"`
	Score   int               `json:"score"`
	Total   int               `json:"total"`
	Factors map[string]string `json:"factors"`
	Names   map[string]string `json:"names"`
}

func main() {
	fixture := os.Getenv("KIT_CROSS_LANG_COMPLIANCE_FIXTURE")
	out := os.Getenv("KIT_CROSS_LANG_COMPLIANCE_OUT")
	if fixture == "" || out == "" {
		fmt.Fprintln(os.Stderr,
			"KIT_CROSS_LANG_COMPLIANCE_FIXTURE and _OUT must be set")
		os.Exit(2)
	}

	report, err := compliance.Run("", fixture)
	if err != nil {
		fmt.Fprintf(os.Stderr, "compliance.Run: %v\n", err)
		os.Exit(1)
	}

	obs := observation{
		Lang:    "go",
		Score:   report.Score,
		Total:   report.Total,
		Factors: map[string]string{},
		Names:   map[string]string{},
	}
	for _, r := range report.Results {
		key := strconv.Itoa(int(r.Factor))
		obs.Factors[key] = r.Status
		obs.Names[key] = r.Name
	}

	// MarshalIndent sorts map keys, which is the stability we want.
	b, err := json.MarshalIndent(obs, "", "  ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "marshal: %v\n", err)
		os.Exit(1)
	}
	if err := os.WriteFile(out, append(b, '\n'), 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "write: %v\n", err)
		os.Exit(1)
	}
}
