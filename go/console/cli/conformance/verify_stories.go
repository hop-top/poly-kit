package conformance

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"hop.top/kit/go/conformance/scenariorules"
	"hop.top/kit/go/conformance/story/parser"
	"hop.top/kit/go/conformance/story/validator"
	"hop.top/kit/go/console/output"
)

// verifyStoriesCmd wires the story parser + validator into the
// user-facing verify-stories leaf. The shape mirrors verify-no-leak's
// envelope: scan-source flag (--paths), rules override (--rules-file),
// human|json output, sentinel-driven exit codes.
//
// design.md §7 owns the flag set; runVerifyStories owns the
// orchestration.
func verifyStoriesCmd() *cobra.Command {
	var (
		paths          []string
		toolspecPath   string
		strictToolspec bool
		rulesFile      string
		maxSteps       int
		quietOnClean   bool
	)
	v := viper.New()
	cmd := &cobra.Command{
		Use:   "verify-stories [paths...]",
		Short: "Validate kit story YAML files",
		Long: `Validate adopter-authored stories under e2e/stories/ (or
the path(s) passed via --paths / positional args).

Stories are the in-repo, agent-visible companion to scenario rubrics:
plain-English intent + a command sequence, no assertions. This leaf
applies a three-tier check:

  1. schema validity (closed-key YAML; rejects scenario-shaped keys
     like scenario_id, assertions, judge, cassette_must_*).
  2. referential validity (story_id uniqueness, invoke[0] matches
     binary, references[].url parses).
  3. toolspec semantic validity (opt-in via --strict-toolspec; every
     invoked command + flag must be declared in the toolspec).

A metadata-key denylist sourced from contracts/scenario-rules.json
rejects the same vocabulary the leak detector flags, so a valid
story always round-trips clean through verify-no-leak.

 and ADR-0026.`,
		Args: cobra.ArbitraryArgs,
		Example: `  kit conformance verify-stories
  kit conformance verify-stories --paths=e2e/stories
  kit conformance verify-stories --strict-toolspec
  kit conformance verify-stories --format=json --quiet-on-clean`,
		RunE: func(cmd *cobra.Command, args []string) error {
			// Default to "human" when the caller did not explicitly pass
			// --format: the kit-wide "table" default would render an
			// empty payload for a finding stream.
			if pf := cmd.Flags().Lookup("format"); pf != nil && !pf.Changed {
				v.Set("format", output.Human)
			}
			// Positional args are additive to --paths.
			combined := append([]string(nil), paths...)
			combined = append(combined, args...)
			return runVerifyStories(cmd, v, vsFlags{
				paths:          combined,
				toolspecPath:   toolspecPath,
				strictToolspec: strictToolspec,
				rulesFile:      rulesFile,
				maxSteps:       maxSteps,
				quietOnClean:   quietOnClean,
			})
		},
	}
	cmd.Flags().StringSliceVar(&paths, "paths", nil, "paths to scan (file or directory); defaults to e2e/stories/")
	cmd.Flags().StringVar(&toolspecPath, "toolspec", "", "explicit toolspec path (overrides per-story toolspec_ref)")
	cmd.Flags().BoolVar(&strictToolspec, "strict-toolspec", false, "enable tier 3 + error on missing toolspec")
	cmd.Flags().StringVar(&rulesFile, "rules-file", "", "override embedded contracts/scenario-rules.json")
	output.RegisterFlags(cmd, v)
	cmd.Flags().IntVar(&maxSteps, "max-steps", 50, "cap on steps[] length")
	cmd.Flags().BoolVar(&quietOnClean, "quiet-on-clean", false, "suppress output when no findings")
	return cmd
}

type vsFlags struct {
	paths          []string
	toolspecPath   string
	strictToolspec bool
	rulesFile      string
	maxSteps       int
	quietOnClean   bool
}

func runVerifyStories(cmd *cobra.Command, v *viper.Viper, f vsFlags) error {
	// Load rules. --rules-file overrides the embedded default.
	var (
		doc *scenariorules.Document
		err error
	)
	if f.rulesFile != "" {
		fmt.Fprintf(cmd.ErrOrStderr(), "verify-stories: using rules from %s (overrides embedded copy)\n", f.rulesFile)
		doc, err = scenariorules.LoadFromPath(f.rulesFile)
	} else {
		doc, err = scenariorules.LoadDefault()
	}
	if err != nil {
		return ConfigError("rules load failed", err.Error(), "verify the JSON file matches schema_version=1")
	}

	// Resolve scan paths: explicit paths or default to e2e/stories/.
	cwd, err := os.Getwd()
	if err != nil {
		return IOError("cwd lookup failed", err.Error(), "run from a readable directory")
	}
	scanRoots := f.paths
	if len(scanRoots) == 0 {
		scanRoots = []string{"e2e/stories"}
	}

	// Collect candidate story files.
	files, err := expandPaths(cwd, scanRoots)
	if err != nil {
		if errors.Is(err, errNoStoriesDir) {
			return ConfigError("no stories to scan",
				fmt.Sprintf("default path e2e/stories/ does not exist under %s", cwd),
				"create e2e/stories/ or pass --paths=<dir>")
		}
		return IOError("path expansion failed", err.Error(), "verify the path(s) exist and are readable")
	}

	// Parse every file. Parse errors are validator findings, not
	// fatal — we still want to report the rest.
	var (
		parsed        []*parser.ParsedStory
		parseFindings []validator.Finding
	)
	for _, p := range files {
		ps, perr := parser.ParseFile(p)
		if perr != nil {
			parseFindings = append(parseFindings, validator.Finding{
				File:         p,
				Rule:         "parse-failed",
				Severity:     validator.SeverityError,
				Message:      perr.Error(),
				SuggestedFix: "fix the YAML; closed-key violations (e.g. scenario_id at root) often surface here",
			})
			continue
		}
		parsed = append(parsed, ps)
	}

	opts := validator.Options{
		StrictToolspec:   f.strictToolspec,
		ToolspecOverride: f.toolspecPath,
		RepoRoot:         cwd,
		MaxSteps:         f.maxSteps,
		Rules:            doc,
	}
	findings := validator.ValidateAll(parsed, opts)
	findings = append(parseFindings, findings...)

	errCount, warnCount := splitFindings(findings)
	if errCount == 0 && warnCount == 0 && f.quietOnClean {
		return nil
	}
	report := newStoryReport(files, findings, doc)
	if err := output.Dispatch(cmd, v, report); err != nil {
		if strings.Contains(err.Error(), "unknown output format") {
			return UsageError(err.Error())
		}
		return IOError("render failed", err.Error(), "")
	}
	if errCount > 0 {
		return LeakDetectedError(fmt.Sprintf("verify-stories: %d error(s) across %d file(s)", errCount, countErrFiles(findings)))
	}
	return nil
}

// errNoStoriesDir signals that --paths was unset and the default
// e2e/stories/ directory is absent. The caller maps this to
// ConfigError ("create the directory or pass --paths").
var errNoStoriesDir = errors.New("default stories directory missing")

// expandPaths walks each scan root and returns the deduplicated,
// sorted list of .yaml / .yml files. Directories are recursed; bare
// files are taken verbatim. Missing entries produce an error when
// they are explicit user input but the special errNoStoriesDir when
// the default e2e/stories/ is absent.
func expandPaths(cwd string, roots []string) ([]string, error) {
	seen := map[string]struct{}{}
	var out []string
	for _, r := range roots {
		info, err := os.Stat(r)
		if err != nil {
			if os.IsNotExist(err) && r == "e2e/stories" {
				return nil, errNoStoriesDir
			}
			return nil, fmt.Errorf("stat %s: %w", r, err)
		}
		if info.IsDir() {
			walked, derr := walkDir(r)
			if derr != nil {
				return nil, derr
			}
			for _, p := range walked {
				if _, ok := seen[p]; !ok {
					seen[p] = struct{}{}
					out = append(out, p)
				}
			}
			continue
		}
		if _, ok := seen[r]; !ok {
			seen[r] = struct{}{}
			out = append(out, r)
		}
	}
	return out, nil
}

func walkDir(dir string) ([]string, error) {
	var out []string
	err := walkYAMLs(dir, &out)
	return out, err
}

func walkYAMLs(dir string, out *[]string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		full := dir + "/" + e.Name()
		if e.IsDir() {
			if strings.HasPrefix(e.Name(), ".") {
				continue
			}
			if err := walkYAMLs(full, out); err != nil {
				return err
			}
			continue
		}
		ext := strings.ToLower(extOf(e.Name()))
		if ext == ".yaml" || ext == ".yml" {
			*out = append(*out, full)
		}
	}
	return nil
}

func extOf(name string) string {
	for i := len(name) - 1; i >= 0; i-- {
		if name[i] == '.' {
			return name[i:]
		}
	}
	return ""
}

func splitFindings(fs []validator.Finding) (errCount, warnCount int) {
	for _, f := range fs {
		switch f.Severity {
		case validator.SeverityError:
			errCount++
		case validator.SeverityWarn:
			warnCount++
		}
	}
	return
}

func countErrFiles(fs []validator.Finding) int {
	files := map[string]struct{}{}
	for _, f := range fs {
		if f.Severity == validator.SeverityError {
			files[f.File] = struct{}{}
		}
	}
	return len(files)
}

// vsSummary is the headline error/warning counter included with
// every verify-stories report.
type vsSummary struct {
	Errors   int `json:"errors"`
	Warnings int `json:"warnings"`
}

// vsReport is the canonical verify-stories output document. JSON/YAML
// render directly from struct tags; output.HumanRenderer ships the
// finding-stream rendering with severity markers and suggestions.
type vsReport struct {
	Tool         string              `json:"tool"`
	RulesVersion string              `json:"rules_version,omitempty"`
	ScannedFiles int                 `json:"scanned_files"`
	Summary      vsSummary           `json:"summary"`
	Findings     []validator.Finding `json:"findings"`

	// files retains the scanned path list so RenderHuman can print the
	// "N file(s) scanned" headline without re-counting.
	files []string `json:"-"`
}

// newStoryReport collects the validator output into the wire shape.
func newStoryReport(files []string, fs []validator.Finding, doc *scenariorules.Document) *vsReport {
	errCount, warnCount := splitFindings(fs)
	o := &vsReport{
		Tool:         "verify-stories",
		ScannedFiles: len(files),
		Summary:      vsSummary{Errors: errCount, Warnings: warnCount},
		Findings:     fs,
		files:        files,
	}
	if doc != nil {
		o.RulesVersion = doc.RulesVersion
	}
	if o.Findings == nil {
		o.Findings = []validator.Finding{}
	}
	return o
}

// RenderHuman writes the per-finding terminal-friendly view used by
// the verify-stories human format. Implements output.HumanRenderer.
func (o *vsReport) RenderHuman(w io.Writer) error {
	if o.Summary.Errors == 0 && o.Summary.Warnings == 0 {
		fmt.Fprintf(w, "verify-stories: %d file(s) scanned, 0 findings\n", o.ScannedFiles)
		return nil
	}
	fmt.Fprintf(w, "verify-stories: %d error(s), %d warning(s) across %d file(s)\n\n",
		o.Summary.Errors, o.Summary.Warnings, o.ScannedFiles)
	for _, f := range o.Findings {
		marker := "✘"
		if f.Severity == validator.SeverityWarn {
			marker = "!"
		}
		loc := f.File
		if f.Line > 0 {
			loc = fmt.Sprintf("%s:%d", f.File, f.Line)
		}
		fmt.Fprintf(w, "  %s %s\n", marker, loc)
		fmt.Fprintf(w, "    rule: %s — %s\n", f.Rule, f.Message)
		if f.MatchedToken != "" {
			fmt.Fprintf(w, "    matched: %s\n", f.MatchedToken)
		}
		if f.SuggestedFix != "" {
			fmt.Fprintf(w, "    suggestion: %s\n", f.SuggestedFix)
		}
		fmt.Fprintln(w)
	}
	if o.Summary.Errors > 0 {
		fmt.Fprintln(w, "fix the errors above. warnings are advisory and do not change the exit code.")
	}
	return nil
}
