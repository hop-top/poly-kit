// Package badge implements the "kit conformance badge" CLI leaf.
//
// The leaf is a thin shell over hop.top/kit/go/conformance/badge: it
// reads a per-factor matrix from a JSON file (or emits an ungradable
// seed), runs Verdict, and writes the shields.io endpoint-badge JSON
// to the configured output path.
//
// Two modes:
//
//   - regenerate from matrix:
//     kit conformance badge --matrix=docs/12-factor-matrix.json
//   - emit ungradable seed (used by kit init / scaffold):
//     kit conformance badge --emit-seed
//
// Output defaults to .12fcc.json in CWD; override with -o / --output.
// The leaf never reaches the network; library callers writing custom
// regen pipelines import hop.top/kit/go/conformance/badge directly.
package badge

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"

	"hop.top/kit/go/conformance/badge"
	"hop.top/kit/go/console/cli"
)

// Cmd returns the "kit conformance badge" cobra leaf. The parent
// conformance.Cmd attaches it; cli.SetExemptValidation is applied at
// the parent level when needed.
func Cmd() *cobra.Command {
	var f badgeFlags
	cmd := &cobra.Command{
		Use:   "badge",
		Short: "Write the shields.io endpoint-badge JSON for a 12fcc matrix",
		Long: `Generate the .12fcc.json file consumed by the shields.io
endpoint badge embedded in the project's README.

Two modes:

  • --matrix=<path>  reads a per-factor matrix JSON and emits the
                    verdict-driven badge JSON. Typically driven by the
                    same test that regenerates docs/12-factor-conformance.md.

  • --emit-seed     writes an ungradable (lightgrey) badge JSON. Used
                    by kit init so a freshly scaffolded project's badge
                    renders grey from day 1 instead of broken.

The output path defaults to .12fcc.json in the current directory;
override with -o / --output.

The matrix file is a JSON object with the shape:

  {
    "schemaVersion": 1,
    "factors": [
      {"n": 1, "name": "Capability Introspection",
       "tier": "must", "status": "pass", "evidence": "..."},
      ...12 entries total...
    ]
  }

tier is one of "must" | "should" | "may"; status is one of
"pass" | "fail" | "skip".`,
		Args: cobra.NoArgs,
		Example: `  kit conformance badge --emit-seed
  kit conformance badge --matrix=docs/12-factor-matrix.json
  kit conformance badge --matrix=matrix.json --output=public/badge.json`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return run(cmd, f)
		},
	}
	cmd.Flags().StringVar(&f.matrix, "matrix", "", "path to per-factor matrix JSON")
	cmd.Flags().BoolVar(&f.emitSeed, "emit-seed", false, "write an ungradable seed (skips --matrix)")
	cmd.Flags().StringVarP(&f.output, "output", "o", ".12fcc.json", "output path for the shields endpoint JSON")

	cli.SetSideEffect(cmd, cli.SideEffectWriteLocal)
	cli.SetIdempotency(cmd, cli.IdempotencyYes)
	_ = cli.SetExamples(cmd, []cli.Example{
		{Title: "seed an ungradable badge on scaffold",
			Command: "kit conformance badge --emit-seed"},
		{Title: "regenerate from a matrix file",
			Command: "kit conformance badge --matrix=docs/12-factor-matrix.json"},
	})
	_ = cli.SetNextSteps(cmd, []cli.NextStep{
		{When: "after kit init", Suggest: "commit .12fcc.json alongside the README so the badge renders grey until the first conformance run"},
		{When: "on regen", Suggest: "diff .12fcc.json before commit; verdict drift is the badge color change"},
	})
	return cmd
}

type badgeFlags struct {
	matrix   string
	emitSeed bool
	output   string
}

// matrixFile is the JSON-side schema for the --matrix input. Tier /
// Status are decoded as strings ("must"/"pass"/...) so the file is
// human-editable and schema-stable independent of Go enum integers.
type matrixFile struct {
	SchemaVersion int           `json:"schemaVersion"`
	Factors       []matrixEntry `json:"factors"`
}

type matrixEntry struct {
	N        int    `json:"n"`
	Name     string `json:"name"`
	Tier     string `json:"tier"`
	Status   string `json:"status"`
	Evidence string `json:"evidence,omitempty"`
}

func run(cmd *cobra.Command, f badgeFlags) error {
	if f.emitSeed && f.matrix != "" {
		return errors.New("badge: --emit-seed and --matrix are mutually exclusive")
	}
	if !f.emitSeed && f.matrix == "" {
		return errors.New("badge: either --matrix=<path> or --emit-seed is required")
	}

	var rep badge.Report
	if !f.emitSeed {
		parsed, err := loadMatrix(f.matrix)
		if err != nil {
			return err
		}
		rep = parsed
	}

	out, err := os.Create(f.output)
	if err != nil {
		return fmt.Errorf("badge: create %s: %w", f.output, err)
	}
	defer out.Close()

	if err := badge.WriteJSON(out, rep); err != nil {
		return err
	}
	fmt.Fprintf(cmd.OutOrStdout(), "wrote %s\n", f.output)
	return nil
}

func loadMatrix(path string) (badge.Report, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return badge.Report{}, fmt.Errorf("badge: read matrix %s: %w", path, err)
	}
	var mf matrixFile
	if err := json.Unmarshal(raw, &mf); err != nil {
		return badge.Report{}, fmt.Errorf("badge: parse matrix %s: %w", path, err)
	}
	rep := badge.Report{
		SchemaVersion: mf.SchemaVersion,
		Factors:       make([]badge.Factor, 0, len(mf.Factors)),
	}
	for i, e := range mf.Factors {
		tier, err := parseTier(e.Tier)
		if err != nil {
			return badge.Report{}, fmt.Errorf("badge: factor[%d] tier: %w", i, err)
		}
		status, err := parseStatus(e.Status)
		if err != nil {
			return badge.Report{}, fmt.Errorf("badge: factor[%d] status: %w", i, err)
		}
		rep.Factors = append(rep.Factors, badge.Factor{
			N:        e.N,
			Name:     e.Name,
			Tier:     tier,
			Status:   status,
			Evidence: e.Evidence,
		})
	}
	return rep, nil
}

func parseTier(s string) (badge.Tier, error) {
	switch s {
	case "must", "MUST":
		return badge.Must, nil
	case "should", "SHOULD":
		return badge.Should, nil
	case "may", "MAY", "":
		return badge.May, nil
	}
	return 0, fmt.Errorf("unknown tier %q (want must|should|may)", s)
}

func parseStatus(s string) (badge.Status, error) {
	switch s {
	case "pass", "PASS":
		return badge.Pass, nil
	case "fail", "FAIL":
		return badge.Fail, nil
	case "skip", "SKIP":
		return badge.Skip, nil
	}
	return 0, fmt.Errorf("unknown status %q (want pass|fail|skip)", s)
}

// ensure cmd.OutOrStdout is bound; package io kept to avoid unused
// import noise when run() ever needs streaming output.
var _ io.Writer = os.Stdout
