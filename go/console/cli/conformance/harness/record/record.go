// Package record implements the "kit conformance harness record" CLI
// leaf: the cassette recorder that turns a scenario plus a target
// binary into an svc-gradable cassette directory.
//
// The leaf wraps hop.top/kit/go/conformance/recorder: it parses and
// validates the scenario, resolves the story source, executes every
// scenario step against the target binary as a real subprocess, and
// writes manifest.yaml + story.yaml + steps/<id>/{result.json,
// stdout.txt,stderr.txt} ready for `kit conformance grade`.
package record

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"hop.top/kit/go/conformance/recorder"
	"hop.top/kit/go/conformance/scenario"
	"hop.top/kit/go/console/cli"
	"hop.top/kit/go/console/output"
)

// Group returns the "harness" command group with the record leaf
// attached. The parent conformance command mounts this group.
func Group() *cobra.Command {
	group := &cobra.Command{
		Use:   "harness",
		Short: "Record and replay conformance cassettes",
		Args:  cobra.NoArgs,
	}
	cli.SetHierarchical(group)
	group.AddCommand(Cmd())
	return group
}

// Cmd returns the "record" cobra leaf.
func Cmd() *cobra.Command {
	var f recordFlags
	// Leaf-local viper so output.RegisterFlags binds independently of
	// any Root the leaf is mounted under (mirrors the grade leaf).
	v := viper.New()
	cmd := &cobra.Command{
		Use:   "record",
		Short: "Record a gradable cassette from real binary runs",
		Long: `Execute a conformance scenario's steps against a target
binary and record the results as an uploadable cassette.

Every step in the scenario's steps[] runs as a real subprocess of
--binary; exit code, stdout, stderr, and duration are captured
verbatim — nothing is synthesized. Steps inherit the scenario's
per-step env/stdin/delay, and each subprocess sees XRR_CASSETTE_DIR +
XRR_MODE=record so xrr-instrumented binaries persist adapter traffic
into the per-step cassette dir.

The output directory receives the svc upload layout:

  manifest.yaml                   schema_version, binary, recorder,
                                  recorded_at, story_ref.content_hash
  story.yaml                      byte-exact copy of the story source
  steps/<id>/result.json          {"exit_code": N, "duration_ms": M}
  steps/<id>/stdout.txt           stdout as observed
  steps/<id>/stderr.txt           stderr as observed

The story source resolves from the scenario's story_ref.story_path
(relative to the scenario file, walking ancestor directories) or from
--story. Recording refuses to proceed when the story bytes do not
hash to the scenario's story_ref.content_hash.

The manifest's scenario_id/scenario_version derive automatically when
the scenario file sits in a library layout
(scenarios/<ns>/<id>/<version>/scenario.yaml); override with
--scenario-ref <ns>/<id>[@<version>].

Exit codes:

  0  cassette recorded
  1  scenario schema_version unsupported by this binary
  2  scenario parse or validation error
  3  usage error (missing flags, output dir conflicts)
  4  io error (binary missing, step execution or write failure)
  5  story error (source not found, content-hash mismatch)`,
		Args: cobra.NoArgs,
		Example: `  kit conformance harness record --scenario ./scenarios/acme/status-json/1.0.0/scenario.yaml --binary ./bin/acme --out ./cassettes/status-json
  kit conformance harness record --scenario ./scenario.yaml --binary ./bin/acme --out ./out --story ./stories/status-json.yaml --workdir ./staged`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			// CI auto-flip: --format=json under CI unless explicitly set
			// (mirrors the grade leaf).
			formatChanged := false
			if pf := cmd.Flags().Lookup("format"); pf != nil {
				formatChanged = pf.Changed
			}
			switch {
			case !formatChanged && os.Getenv("CI") != "":
				v.Set("format", output.JSON)
			case !formatChanged:
				v.Set("format", output.Human)
			}
			return run(cmd, v, f)
		},
	}
	cmd.Flags().StringVar(&f.scenarioPath, "scenario", "", "scenario YAML to record (required)")
	cmd.Flags().StringVar(&f.binary, "binary", "", "target binary the steps execute (required)")
	cmd.Flags().StringVar(&f.out, "out", "", "cassette output directory (required)")
	cmd.Flags().StringVar(&f.story, "story", "", "story source file (default: resolve story_ref.story_path)")
	cmd.Flags().StringVar(&f.workdir, "workdir", "", "working directory steps run in (default: fresh temp dir)")
	cmd.Flags().StringVar(&f.scenarioRef, "scenario-ref", "", "manifest scenario ref as <ns>/<id>[@<version>]")
	cmd.Flags().StringVar(&f.binaryVersion, "binary-version", "", "binary version stamp for the manifest (git SHA, semver)")
	cmd.Flags().DurationVar(&f.stepTimeout, "step-timeout", 2*time.Minute, "per-step subprocess time budget")
	cmd.Flags().BoolVar(&f.force, "force", false, "replace an existing recording at --out (must contain manifest.yaml)")
	output.RegisterFlags(cmd, v)

	// Full annotation surface: recording writes the out dir and runs
	// the target binary, whose own effects are scenario-defined; a
	// re-run re-executes the binary, so the leaf is not idempotent.
	cli.SetSideEffect(cmd, cli.SideEffectWrite)
	cli.SetIdempotency(cmd, cli.IdempotencyNo)
	_ = cli.SetExamples(cmd, []cli.Example{
		{Title: "record a scenario from a library layout", Command: "kit conformance harness record --scenario ./e2e/conformance/scenarios/acme/status-json/1.0.0/scenario.yaml --binary ./bin/acme --out ./e2e/conformance/cassettes/status-json"},
		{Title: "re-record over a prior cassette", Command: "kit conformance harness record --scenario ./scenario.yaml --binary ./bin/acme --out ./cassette --force"},
	})
	_ = cli.SetNextSteps(cmd, []cli.NextStep{
		{When: "on success", Suggest: "grade the cassette: kit conformance grade <out> --service <url>"},
		{When: "on story hash mismatch", Suggest: "update the scenario's story_ref.content_hash to the story file's sha256"},
	})
	return cmd
}

// recordFlags is the parsed flag set for the record leaf.
type recordFlags struct {
	scenarioPath  string
	binary        string
	out           string
	story         string
	workdir       string
	scenarioRef   string
	binaryVersion string
	stepTimeout   time.Duration
	force         bool
}

// run drives the leaf: validate flags, load scenario + story, record,
// render the summary.
func run(cmd *cobra.Command, v *viper.Viper, f recordFlags) error {
	if f.scenarioPath == "" {
		return usageError("--scenario is required")
	}
	if f.binary == "" {
		return usageError("--binary is required")
	}
	if f.out == "" {
		return usageError("--out is required")
	}

	// Scenario parse + validate. Exit codes follow the scenario
	// library's CLI contract: schema unsupported = 1, parse error = 2,
	// validation error = 2.
	sc, err := scenario.ParseFile(f.scenarioPath)
	if err != nil {
		if scenario.IsSchemaUnsupported(err) {
			return output.WrapError(err, "SCENARIO_SCHEMA_UNSUPPORTED", 1)
		}
		return output.WrapError(err, "SCENARIO_PARSE_ERROR", 2)
	}
	if err := scenario.Validate(sc); err != nil {
		return output.WrapError(err, "SCENARIO_INVALID", 2)
	}

	// Story resolution + the recorder's content-hash guard.
	storyBytes, _, err := recorder.ResolveStory(f.scenarioPath, sc.StoryRef.StoryPath, f.story)
	if err != nil {
		return output.WrapError(err, "STORY_NOT_FOUND", 5)
	}

	// Manifest scenario ref: explicit flag wins, else derive from a
	// library-shaped path.
	refID, refVersion := recorder.DeriveRef(f.scenarioPath, sc)
	if f.scenarioRef != "" {
		refID, refVersion = splitRef(f.scenarioRef)
		if refID == "" {
			return usageError(fmt.Sprintf("--scenario-ref %q is malformed (want <ns>/<id>[@<version>])", f.scenarioRef))
		}
	}

	// Target binary must exist up front so the failure is a clean io
	// error, not a per-step surprise.
	absBinary, err := filepath.Abs(f.binary)
	if err != nil {
		return ioError(fmt.Sprintf("resolve --binary %q: %v", f.binary, err))
	}
	if st, err := os.Stat(absBinary); err != nil || st.IsDir() {
		return ioError(fmt.Sprintf("--binary %q is not an executable file", f.binary))
	}

	// Working directory: adopter-staged or fresh temp.
	workdir := f.workdir
	if workdir != "" {
		if st, err := os.Stat(workdir); err != nil || !st.IsDir() {
			return usageError(fmt.Sprintf("--workdir %q is not a directory", f.workdir))
		}
	} else {
		tmp, err := os.MkdirTemp("", "kit-harness-record-*")
		if err != nil {
			return ioError(fmt.Sprintf("create temp workdir: %v", err))
		}
		defer func() { _ = os.RemoveAll(tmp) }()
		workdir = tmp
	}

	if err := prepareOutDir(f.out, f.force); err != nil {
		return err
	}

	res, err := recorder.Run(cmd.Context(), recorder.Options{
		Scenario:        sc,
		StoryBytes:      storyBytes,
		OutDir:          f.out,
		Invoker:         &recorder.ExecInvoker{Path: absBinary, Dir: workdir, Timeout: f.stepTimeout},
		BinaryVersion:   f.binaryVersion,
		ScenarioID:      refID,
		ScenarioVersion: refVersion,
	})
	if err != nil {
		switch {
		case recorder.IsStoryHashMismatch(err):
			return output.WrapError(err, "STORY_HASH_MISMATCH", 5)
		case recorder.IsOutDirNotEmpty(err):
			return usageError(err.Error() + " (pass --force to replace it)")
		default:
			return output.WrapError(err, "RECORD_IO", 4)
		}
	}

	report := &recordReport{Result: res}
	if err := output.Dispatch(cmd, v, report); err != nil {
		if strings.Contains(err.Error(), "unknown output format") {
			return usageError(err.Error())
		}
		return err
	}
	return nil
}

// prepareOutDir enforces the overwrite policy: a non-empty out dir is
// refused unless --force, and --force only replaces a directory that
// is recognizably a prior recording (contains manifest.yaml) so a
// mistyped --out cannot delete unrelated data.
func prepareOutDir(out string, force bool) error {
	entries, err := os.ReadDir(out)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return ioError(fmt.Sprintf("read --out %q: %v", out, err))
	}
	if len(entries) == 0 {
		return nil
	}
	if !force {
		return usageError(fmt.Sprintf("--out %q is not empty; pass --force to replace a prior recording", out))
	}
	if _, err := os.Stat(filepath.Join(out, "manifest.yaml")); err != nil {
		return usageError(fmt.Sprintf("--out %q is not empty and does not look like a prior recording (no manifest.yaml); refusing to delete it even with --force", out))
	}
	if err := os.RemoveAll(out); err != nil {
		return ioError(fmt.Sprintf("remove prior recording %q: %v", out, err))
	}
	return nil
}

// splitRef parses "<ns>/<id>[@<version>]" into the manifest's
// scenario_id ("<ns>/<id>") and scenario_version. Returns ("", "")
// when malformed.
func splitRef(ref string) (id, version string) {
	id = ref
	if at := strings.LastIndex(ref, "@"); at >= 0 {
		id, version = ref[:at], ref[at+1:]
		if version == "" {
			return "", ""
		}
	}
	parts := strings.SplitN(id, "/", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", ""
	}
	return id, version
}

// recordReport wraps recorder.Result for output.Dispatch. JSON/YAML
// marshal the inner result; RenderHuman provides --format=human.
type recordReport struct {
	*recorder.Result
}

// RenderHuman writes the terminal-friendly recording summary.
func (r *recordReport) RenderHuman(w io.Writer) error {
	if r == nil || r.Result == nil {
		return nil
	}
	fmt.Fprintf(w, "recorded: %s\n", r.ScenarioID)
	fmt.Fprintf(w, "  out:    %s\n", r.OutDir)
	fmt.Fprintf(w, "  story:  %s\n", r.StoryHash)
	fmt.Fprintln(w, "  steps:")
	for _, s := range r.Steps {
		fmt.Fprintf(w, "    %-24s exit=%-3d %dms\n", s.ID, s.ExitCode, s.DurationMS)
	}
	return nil
}

// usageError mirrors the conformance tree's usage sentinel: exit 3.
func usageError(detail string) error {
	return &output.Error{
		Code:     "USAGE",
		Message:  "conformance harness record: " + detail,
		ExitCode: 3,
	}
}

// ioError maps environment/subprocess failures to the conformance
// tree's io_error slot: exit 4.
func ioError(detail string) error {
	return &output.Error{
		Code:     "IO",
		Message:  "conformance harness record: " + detail,
		ExitCode: 4,
	}
}
