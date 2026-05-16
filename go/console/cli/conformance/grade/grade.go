// Package grade implements the "kit conformance grade" CLI leaf.
//
// The leaf wraps go/conformance/client: it loads a cassette dir,
// uploads it to the configured svc URL, prints the verdict (human or
// JSON), and optionally posts a PR comment / Checks API status. The
// leaf is the canonical adopter-facing surface; library callers
// (Go test binaries) go through hop.top/kit/go/conformance/client
// directly.
package grade

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"hop.top/kit/go/conformance/client"
	"hop.top/kit/go/console/cli"
	"hop.top/kit/go/console/output"
)

// Cmd returns the "kit conformance grade" cobra leaf. The parent
// conformance.Cmd attaches it; cli.SetExemptValidation is applied at
// the parent level for kit-internal annotation exemption (matching
// verify-no-leak / verify-stories).
func Cmd() *cobra.Command {
	var f gradeFlags
	// Leaf-local viper so output.RegisterFlags binds against a viper
	// independent of any Root the leaf is mounted under. Tests that
	// drive Cmd() in isolation get a fully-wired --format/--output set
	// without needing a fake Root.
	v := viper.New()
	cmd := &cobra.Command{
		Use:   "grade <cassette-dir>",
		Short: "Upload a cassette and fetch its conformance grade",
		Long: `Upload a locally-recorded cassette to a hop.top/kit
conformance grading service and surface the verdict.

The cassette dir must contain a manifest.yaml describing the scenario,
story, and per-step capture files (see harness output). Override
manifest fields via --scenario-id / --story / --tier.

This leaf is the canonical adopter-facing surface for the 12fcc
service. Library callers embedded inside ` + "`go test`" + ` should
import hop.top/kit/go/conformance/client directly to keep failures
on the same go-test exit code.

Authentication uses the bearer token in KIT_CONFORMANCE_TOKEN (or
--token). --pr-comment / --status-check additionally consume
GITHUB_TOKEN to post to the PR UI; missing GITHUB_TOKEN warns but
does not fail the grade. The grade verdict is the gate; the comment
is convenience.`,
		Args: cobra.ExactArgs(1),
		Example: `  kit conformance grade ./testdata/cassettes/conformance --service https://12fcc.example.com
  kit conformance grade ./cassette --format=json --pr-comment --status-check
  kit conformance grade ./cassette --scenario-id foo.bar --tier 2`,
		RunE: func(cmd *cobra.Command, args []string) error {
			f.cassetteDir = args[0]
			// CI auto-flip: --format=json when CI=<truthy> and the
			// adopter did not explicitly pass --format.
			formatChanged := false
			if pf := cmd.Flags().Lookup("format"); pf != nil {
				formatChanged = pf.Changed
			}
			switch {
			case !formatChanged && os.Getenv("CI") != "":
				v.Set("format", output.JSON)
			case !formatChanged:
				// Default to human for adopter-facing invocations.
				v.Set("format", output.Human)
			}
			return run(cmd, v, f)
		},
	}
	cmd.Flags().StringVar(&f.service, "service", "", "grade service URL (env: KIT_CONFORMANCE_SERVICE)")
	cmd.Flags().StringVar(&f.token, "token", "", "bearer token (env: KIT_CONFORMANCE_TOKEN)")
	cmd.Flags().StringVar(&f.scenarioID, "scenario-id", "", "override manifest scenario_id")
	cmd.Flags().StringVar(&f.story, "story", "", "override manifest story_path")
	cmd.Flags().IntVar(&f.tier, "tier", 1, "requested grade tier (1/2/3)")
	cmd.Flags().BoolVar(&f.prComment, "pr-comment", false, "post a GitHub PR comment (requires GITHUB_TOKEN)")
	cmd.Flags().BoolVar(&f.statusCheck, "status-check", false, "post a GitHub Checks API status (requires GITHUB_TOKEN)")
	cmd.Flags().DurationVar(&f.timeout, "timeout", 5*time.Minute, "total per-Grade ctx budget")
	cmd.Flags().IntVar(&f.retries, "retries", 3, "max retry attempts on transient svc errors")
	output.RegisterFlags(cmd, v)
	cmd.Flags().Int64Var(&f.maxCassette, "max-cassette-size", client.DefaultMaxCassetteSize, "max packed cassette bytes")

	// Full annotation surface: declare side-effect (network),
	// idempotent (re-grading is safe), and ship examples + next-steps.
	cli.SetSideEffect(cmd, cli.SideEffectWriteShared)
	cli.SetIdempotency(cmd, cli.IdempotencyYes)
	_ = cli.SetExamples(cmd, []cli.Example{
		{Title: "grade a recorded cassette", Command: "kit conformance grade ./testdata/cassettes/foo --service https://12fcc.example.com"},
		{Title: "post a status check from CI", Command: "kit conformance grade ./cassette --format=json --status-check"},
	})
	_ = cli.SetNextSteps(cmd, []cli.NextStep{
		{When: "on fail", Suggest: "inspect facets/findings; re-run the harness if cassette is stale"},
		{When: "on ungradable", Suggest: "verify story_content_hash matches the live story file"},
	})
	return cmd
}

// gradeFlags is the parsed flag set; field names match the
// design.md §3 surface. --format and --output are handled by
// output.RegisterFlags / output.Dispatch; they intentionally do not
// appear here.
type gradeFlags struct {
	cassetteDir string
	service     string
	token       string
	scenarioID  string
	story       string
	tier        int
	prComment   bool
	statusCheck bool
	timeout     time.Duration
	retries     int
	maxCassette int64
}

// run drives the leaf: validate flags, construct a client, call
// Grade, render, post side-effects.
func run(cmd *cobra.Command, v *viper.Viper, f gradeFlags) error {
	// Validation block. All Class A "won't even try" errors land here.
	// --format is validated downstream by output.Dispatch; reject only
	// the legacy values the prior local flag accepted so adopters who
	// were on `--format=yaml` get a clear message instead of a silent
	// shape switch. (Prior to consolidation only human|json were valid.)
	if f.tier < 1 || f.tier > 3 {
		return usageError(fmt.Sprintf("--tier must be 1/2/3, got %d", f.tier))
	}
	if f.retries < 1 {
		return usageError("--retries must be >= 1")
	}
	if f.cassetteDir == "" {
		return usageError("cassette directory argument is required")
	}

	service := f.service
	if service == "" {
		service = os.Getenv("KIT_CONFORMANCE_SERVICE")
	}
	if service == "" {
		return usageError("--service URL is required (or set KIT_CONFORMANCE_SERVICE)")
	}
	token := f.token
	if token == "" {
		token = os.Getenv("KIT_CONFORMANCE_TOKEN")
	}

	// Build client.
	c, err := client.New(service,
		client.WithToken(token),
		client.WithMaxAttempts(f.retries),
		client.WithMaxCassetteSize(f.maxCassette),
	)
	if err != nil {
		return err
	}

	// Run with the configured timeout.
	ctx, cancel := context.WithTimeout(cmd.Context(), f.timeout)
	defer cancel()

	res, err := c.Grade(ctx, client.GradeRequest{
		CassetteDir: f.cassetteDir,
		ScenarioID:  f.scenarioID,
		StoryPath:   f.story,
		Tier:        f.tier,
	})
	if err != nil {
		// Render before bubbling so JSON consumers always see *some*
		// structured output on stderr; the error envelope is the carry.
		return err
	}

	// Render verdict (Class B is encoded into the result; the leaf
	// maps result.Verdict to an exit-code-bearing sentinel below).
	// output.Dispatch resolves --format (incl. CI auto-flip via the
	// leaf-local viper) and --output (file vs stdout) for us.
	report := &gradeReport{Result: res}
	if err := output.Dispatch(cmd, v, report); err != nil {
		if strings.Contains(err.Error(), "unknown output format") {
			return usageError(err.Error())
		}
		return err
	}

	// Side-effect posting (best-effort; failures warn-only).
	if f.prComment {
		if err := postPRComment(ctx, res); err != nil {
			fmt.Fprintf(cmd.ErrOrStderr(), "warn: pr-comment failed: %v\n", err)
		}
	}
	if f.statusCheck {
		if err := postStatusCheck(ctx, res); err != nil {
			fmt.Fprintf(cmd.ErrOrStderr(), "warn: status-check failed: %v\n", err)
		}
	}

	// Map verdict to exit code via sentinels.
	switch res.Verdict {
	case client.VerdictPass:
		return nil
	case client.VerdictFail:
		return client.GradeFailError(res.ScenarioID, res.Reason)
	case client.VerdictUngradable:
		return client.GradeUngradableError(res.ScenarioID, res.Reason)
	}
	return usageError(fmt.Sprintf("unknown verdict %q", res.Verdict))
}

// usageError mirrors conformance.UsageError so the kit RunE
// middleware exits with code 3.
func usageError(detail string) error {
	return &output.Error{
		Code:     "USAGE",
		Message:  "conformance grade: " + detail,
		ExitCode: 3,
	}
}
