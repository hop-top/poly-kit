package conformance

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"hop.top/kit/go/console/cli/conformance/verifynoleak/rules"
	"hop.top/kit/go/console/cli/conformance/verifynoleak/scanner"
	"hop.top/kit/go/console/cli/conformance/verifynoleak/source"
	"hop.top/kit/go/console/cli/conformance/verifynoleak/suppress"
	"hop.top/kit/go/console/output"
)

// verifyNoLeakCmd wires the rules engine + extractor + source
// resolver into the user-facing verify-no-leak leaf. The command
// shape mirrors design.md §6; flag interactions are enforced at the
// top of RunE. Rendering routes through output.Dispatch — the
// kit-wide --format flag (including the "human" key registered by
// the output package) is the single source of truth.
func verifyNoLeakCmd() *cobra.Command {
	var (
		staged       bool
		audit        bool
		diff         string
		paths        []string
		commitRange  string
		commitMsg    string
		prBody       int
		rulesFile    string
		maxFileSize  int64
		quietOnClean bool
	)
	// Leaf-local viper so tests that drive Cmd() in isolation still get
	// a wired --format flag set. When mounted under a kit Root the leaf
	// shadows the root's persistent --format on its own subtree, but
	// the value resolution path (output.Dispatch) is identical.
	v := viper.New()
	cmd := &cobra.Command{
		Use:   "verify-no-leak",
		Short: "Scan for scenario YAML leakage",
		Long: `Detect scenario-rubric YAML in files, fenced markdown
blocks, commit messages, and PR bodies before they ship. See
`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			// Preserve leaf-default-human behavior: when the caller did
			// not explicitly pass --format, default to "human" rather
			// than the kit-wide "table" default (which would render
			// nothing useful for a finding stream).
			if pf := cmd.Flags().Lookup("format"); pf != nil && !pf.Changed {
				v.Set("format", output.Human)
			}
			return runVerifyNoLeak(cmd, v, vnlFlags{
				staged:       staged,
				audit:        audit,
				diff:         diff,
				paths:        paths,
				commitRange:  commitRange,
				commitMsg:    commitMsg,
				prBody:       prBody,
				rulesFile:    rulesFile,
				maxFileSize:  maxFileSize,
				quietOnClean: quietOnClean,
			})
		},
	}
	cmd.Flags().BoolVar(&staged, "staged", false, "scan only files staged for commit (Tier A)")
	cmd.Flags().BoolVar(&audit, "audit", false, "scan the entire working tree (Tier C)")
	cmd.Flags().StringVar(&diff, "diff", "", "scan files in `<base>...HEAD` diff (Tier B)")
	cmd.Flags().StringSliceVar(&paths, "paths", nil, "scan an explicit list of paths")

	cmd.Flags().StringVar(&commitRange, "commit-range", "", "additionally scan commit messages in `<base>..HEAD`")
	cmd.Flags().StringVar(&commitMsg, "commit-msg-file", "", "additionally scan a single commit message from file (for commit-msg hook)")
	cmd.Flags().IntVar(&prBody, "pr-body", 0, "additionally scan PR `<number>` body via gh api (requires GH_TOKEN)")

	cmd.Flags().StringVar(&rulesFile, "rules-file", "", "override embedded contracts/scenario-rules.json")
	cmd.Flags().Int64Var(&maxFileSize, "max-file-size", scanner.DefaultMaxFileSize, "skip files larger than `n` bytes")

	output.RegisterFlags(cmd, v)
	cmd.Flags().BoolVar(&quietOnClean, "quiet-on-clean", false, "suppress output when no findings")
	return cmd
}

// vnlFlags groups the parsed flags for clarity. Not exported.
type vnlFlags struct {
	staged       bool
	audit        bool
	diff         string
	paths        []string
	commitRange  string
	commitMsg    string
	prBody       int
	rulesFile    string
	maxFileSize  int64
	quietOnClean bool
}

// scanSourcesSet reports how many of the mutually-exclusive
// scan-source flags are set. Used by the usage-error guard.
func (f vnlFlags) scanSourcesSet() int {
	n := 0
	if f.staged {
		n++
	}
	if f.audit {
		n++
	}
	if f.diff != "" {
		n++
	}
	if len(f.paths) > 0 {
		n++
	}
	return n
}

func runVerifyNoLeak(cmd *cobra.Command, v *viper.Viper, f vnlFlags) error {
	if n := f.scanSourcesSet(); n > 1 {
		return UsageError("scan-source flags (--staged, --audit, --diff, --paths) are mutually exclusive")
	}
	if f.prBody < 0 {
		return UsageError("--pr-body requires a positive PR number")
	}

	// Load rules: --rules-file overrides the embedded default. Override
	// is logged to stderr per design.md §3 so an adopter can't silently
	// neuter the detector.
	var (
		set *rules.Set
		err error
	)
	if f.rulesFile != "" {
		fmt.Fprintf(cmd.ErrOrStderr(), "verify-no-leak: using rules from %s (overrides embedded copy)\n", f.rulesFile)
		set, err = rules.LoadFromPath(f.rulesFile)
	} else {
		set, err = rules.LoadDefault()
	}
	if err != nil {
		return ConfigError("rules load failed", err.Error(), "verify the JSON file matches schema_version=1")
	}

	cwd, err := os.Getwd()
	if err != nil {
		return IOError("cwd lookup failed", err.Error(), "run from a readable directory")
	}

	// Load .verifynoleak.allow (no-op if missing). Layer kit-internal
	// defaults on top only when scanning the kit repo itself or when
	// KIT_INTERNAL_ALLOWLIST is set — design.md §5.
	allowlist, err := suppress.LoadAllowlist(cwd)
	if err != nil {
		return ConfigError(".verifynoleak.allow load failed", err.Error(), "verify the file's syntax (gitignore-style)")
	}
	if isKitInternal(cwd) {
		allowlist.Add(suppress.DefaultKitInternalGlobs()...)
	}

	// Resolve scan source. Default: --staged inside a git repo,
	// --audit outside. The CLI middleware doesn't know which is
	// which, so resolve here.
	var (
		filePaths  []string
		commitMsgs []source.CommitMessage
	)
	switch {
	case f.staged:
		filePaths, err = source.Staged(cwd)
	case f.audit:
		filePaths, err = source.Audit(cwd)
	case f.diff != "":
		filePaths, err = source.Diff(cwd, f.diff)
	case len(f.paths) > 0:
		filePaths, err = source.Paths(cwd, f.paths)
	default:
		// auto-detect
		filePaths, err = source.Staged(cwd)
		if errors.Is(err, source.ErrNotAGitRepo) {
			filePaths, err = source.Audit(cwd)
		}
	}
	if err != nil {
		if errors.Is(err, source.ErrNotAGitRepo) {
			return IOError("scan-source needs a git repo", err.Error(), "run inside a git working tree or use --paths")
		}
		return IOError("scan-source resolution failed", err.Error(), "")
	}

	// Additive: --commit-range. Each message becomes a synthetic
	// markdown document fed through the same scanner pipeline.
	if f.commitRange != "" {
		commitMsgs, err = source.CommitRange(cwd, f.commitRange)
		if err != nil {
			return IOError("commit-range resolution failed", err.Error(), "")
		}
	}

	// Additive: --commit-msg-file. Single message read from disk
	// (the commit-msg hook passes the COMMIT_EDITMSG path here).
	if f.commitMsg != "" {
		body, readErr := os.ReadFile(f.commitMsg)
		if readErr != nil {
			return IOError("commit-msg-file read failed", readErr.Error(), "verify the file path")
		}
		commitMsgs = append(commitMsgs, source.CommitMessage{SHA: "(staged)", Body: body})
	}

	// Additive: --pr-body. Fetch the body via `gh api` and feed it
	// through the markdown scanner. PR number 0 is the no-op default
	// so install-hooks + scan-only invocations are unaffected.
	var prBodyDoc *prBodyDocument
	if f.prBody > 0 {
		body, fetchErr := source.PRBody(cwd, f.prBody)
		if fetchErr != nil {
			if errors.Is(fetchErr, source.ErrGHNotFound) {
				return IOError("gh binary not found", fetchErr.Error(), "install GitHub CLI (https://cli.github.com) or drop --pr-body")
			}
			return IOError("gh api failed", fetchErr.Error(), "verify GH_TOKEN is set and you have access to the repo")
		}
		prBodyDoc = &prBodyDocument{
			path: source.PRBodyPathLabel(f.prBody),
			body: body,
		}
	}

	// Run the scanner.
	scanOpts := scanner.Options{Rules: set, MaxFileSize: f.maxFileSize, Allowlist: allowlist}
	results, err := scanner.Scan(filePaths, scanOpts)
	if err != nil {
		return IOError("scanner failed", err.Error(), "")
	}

	// Bare-ignore findings: surface as config errors so the operator
	// is forced to add a reason. The scanner records them on
	// FileResult.ParseError; promote to a hard exit.
	for _, r := range results {
		if r.ParseError != nil && errors.Is(r.ParseError, suppress.ErrBareIgnoreRejected) {
			return ConfigError("bare verify-no-leak: ignore comment without reason", r.ParseError.Error(), "add a reason: # verify-no-leak: ignore — <reason>")
		}
	}

	// Run commit-message bodies through the markdown scanner — they
	// frequently contain fenced YAML blocks (design.md §6).
	for _, m := range commitMsgs {
		path := "commit:" + m.SHA
		results = append(results, scanner.ReaderScanFile(path, "md", strings.NewReader(string(m.Body)), scanOpts))
	}

	// PR body — same pipeline as commit messages.
	if prBodyDoc != nil {
		results = append(results, scanner.ReaderScanFile(prBodyDoc.path, "md", strings.NewReader(string(prBodyDoc.body)), scanOpts))
	}

	// Output + exit.
	count := scanner.CountFindings(results)
	if count == 0 && f.quietOnClean {
		return nil
	}
	report := newVNLReport(results, set)
	if err := output.Dispatch(cmd, v, report); err != nil {
		// Map unknown-format errors to UsageError so the conformance
		// exit-code contract holds.
		if strings.Contains(err.Error(), "unknown output format") {
			return UsageError(err.Error())
		}
		return IOError("render failed", err.Error(), "")
	}
	if count > 0 {
		return LeakDetectedError(fmt.Sprintf("%d finding(s) across %d file(s)", count, countWithFindings(results)))
	}
	return nil
}

// prBodyDocument carries the path label + fetched body for the
// --pr-body source. Kept as a struct so the fetched body lives next
// to its synthetic path through the scanner call.
type prBodyDocument struct {
	path string
	body []byte
}

// isKitInternal reports whether the current scan is running against
// the kit repo itself. The kit-internal default allowlist (which
// exempts our own threat-model docs from tripping the detector)
// should fire only here — adopters who mirror our .tlc/tracks/12fcc*/
// layout in their own repos should still get full coverage.
//
// Detection order:
//
//  1. KIT_INTERNAL_ALLOWLIST=1 explicit opt-in.
//  2. git remote origin URL contains "hop-top/kit".
func isKitInternal(cwd string) bool {
	if os.Getenv("KIT_INTERNAL_ALLOWLIST") == "1" {
		return true
	}
	out, err := exec.Command("git", "-C", cwd, "config", "--get", "remote.origin.url").Output()
	if err != nil {
		return false
	}
	return strings.Contains(string(out), "hop-top/kit")
}

func countWithFindings(rs []scanner.FileResult) int {
	n := 0
	for _, r := range rs {
		if len(r.Findings) > 0 {
			n++
		}
	}
	return n
}

// vnlFinding is the wire shape for a single scanner finding. Both the
// JSON encoder and the human renderer read from this struct so adopters
// who parse one format always see the same fields in the other.
type vnlFinding struct {
	File           string   `json:"file"`
	Line           int      `json:"line"`
	Rule           string   `json:"rule"`
	Description    string   `json:"description"`
	MatchedKeys    []string `json:"matched_keys,omitempty"`
	BlockStartLine int      `json:"block_start_line,omitempty"`
}

// vnlSkip records a file that was scanned but skipped (binary, too
// large, unsupported extension) along with the reason.
type vnlSkip struct {
	File   string `json:"file"`
	Reason string `json:"reason"`
}

// vnlReport is the canonical output document. JSON/YAML render
// directly from struct tags; output.HumanRenderer ships the bespoke
// per-finding rendering the leak track requires.
type vnlReport struct {
	Tool         string       `json:"tool"`
	RulesVersion string       `json:"rules_version"`
	ScannedFiles int          `json:"scanned_files"`
	Findings     []vnlFinding `json:"findings"`
	Skipped      []vnlSkip    `json:"skipped,omitempty"`
}

// newVNLReport collects scanner results into the wire shape. Skipped
// files are surfaced separately so adopters can audit what was
// actually scanned.
func newVNLReport(results []scanner.FileResult, set *rules.Set) *vnlReport {
	o := &vnlReport{
		Tool:         "verify-no-leak",
		RulesVersion: set.RulesVersion,
		Findings:     []vnlFinding{},
		Skipped:      []vnlSkip{},
	}
	scanned := 0
	for _, r := range results {
		if r.Skipped {
			o.Skipped = append(o.Skipped, vnlSkip{File: r.Path, Reason: r.SkipReason})
			continue
		}
		scanned++
		for _, f := range r.Findings {
			o.Findings = append(o.Findings, vnlFinding{
				File:           f.Path,
				Line:           f.Line,
				Rule:           f.RuleID,
				Description:    f.Description,
				MatchedKeys:    f.MatchedKeys,
				BlockStartLine: f.BlockStartLine,
			})
		}
	}
	o.ScannedFiles = scanned
	return o
}

// RenderHuman writes the per-finding terminal-friendly view used by
// the verify-no-leak human format. Implements output.HumanRenderer
// so output.Dispatch routes here when --format=human.
func (o *vnlReport) RenderHuman(w io.Writer) error {
	total := len(o.Findings)
	if total == 0 {
		fmt.Fprintln(w, "verify-no-leak: 0 findings")
		return nil
	}
	fmt.Fprintf(w, "verify-no-leak: %d finding(s)\n\n", total)
	for _, f := range o.Findings {
		fmt.Fprintf(w, "  ● %s:%d\n", f.File, f.Line)
		fmt.Fprintf(w, "    rule: %s — %s\n", f.Rule, f.Description)
		if len(f.MatchedKeys) > 0 {
			fmt.Fprintf(w, "    matched: %s\n", strings.Join(f.MatchedKeys, ", "))
		}
		if f.BlockStartLine > 0 {
			fmt.Fprintf(w, "    inside fenced block opened on line %d\n", f.BlockStartLine)
		}
		fmt.Fprintln(w)
	}
	fmt.Fprintln(w, "if this is a real scenario rubric, MOVE IT OUT of this repo —")
	fmt.Fprintln(w, "  scenarios belong in the private grader repo.")
	fmt.Fprintln(w, "if illustrative, add an opt-out (see verify-no-leak guide).")
	return nil
}
