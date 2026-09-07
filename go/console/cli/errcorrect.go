package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"hop.top/kit/go/console/output"
)

// CorrectedError is a structured error with guidance for recovery.
// Agents and humans get classification, cause, fix, and alternatives.
type CorrectedError struct {
	Code         string   `json:"code"`
	Message      string   `json:"message"`
	Cause        string   `json:"cause"`
	Fix          string   `json:"fix"`
	Alternatives []string `json:"alternatives"`
	Retryable    bool     `json:"retryable"`
}

func (e *CorrectedError) Error() string { return e.Message }

// MarshalJSON renders all fields for agent consumption.
func (e *CorrectedError) MarshalJSON() ([]byte, error) {
	type raw CorrectedError // avoid recursion
	return json.Marshal((*raw)(e))
}

// FormatError renders the error for terminal output:
//
//	ERROR  mission not found
//	Cause: no mission matches "bogux"
//	Fix:   spaced mission list
//	Try:   spaced mission search <partial>
func FormatError(err error, w io.Writer, noColor bool) {
	if err == nil {
		return
	}
	var ce *CorrectedError
	if !errors.As(err, &ce) {
		fmt.Fprintf(w, "ERROR  %s\n", err.Error())
		return
	}
	fmt.Fprintf(w, "ERROR  %s\n", ce.Message)
	if ce.Cause != "" {
		fmt.Fprintf(w, "Cause: %s\n", ce.Cause)
	}
	if ce.Fix != "" {
		fmt.Fprintf(w, "Fix:   %s\n", ce.Fix)
	}
	for _, alt := range ce.Alternatives {
		fmt.Fprintf(w, "Try:   %s\n", alt)
	}
}

// --- Parse-time flag errors ---------------------------------------------
//
// cobra reports a bad flag by returning pflag's error from Execute. That
// happens BEFORE any RunE runs, so the flag-validator middleware and the
// error-envelope middleware never see it: the tool prints "unknown flag:
// --count" and exits, leaving the caller to go fetch --help.
//
// Everything needed to correct the caller is already in hand at that
// moment. pflag's typed errors carry the offending name and, for a missing
// value, the *pflag.Flag itself; the command carries its whole flag table
// and each flag's enum annotation. installFlagErrorFunc turns that into a
// structured envelope so the correction arrives in the first response.
//
// Deliberately NOT done: auto-applying a fuzzy match. The same path serves
// destructive verbs, where silently running a guessed --force or a wrong
// --status costs far more than the round trip it saves. Suggest, exit
// non-zero.

// flagErrorMaxDistance caps how far a typo may stray from a real flag name
// before kit stops guessing. Two edits covers transposition, a dropped or
// doubled character, and a wrong one; beyond that the "suggestion" is
// noise, and a wrong suggestion on a destructive verb is worse than none.
const flagErrorMaxDistance = 2

// installFlagErrorFunc sets a FlagErrorFunc on cmd and its whole subtree
// so parse failures come back as structured *output.Error envelopes.
//
// cobra resolves the error func by walking up to the root, so setting it
// on the root would suffice for inherited resolution — but a subcommand
// that sets its own must not be silently overridden, so the walk skips any
// command that already has one.
func (r *Root) installFlagErrorFunc() {
	if r == nil || r.Cmd == nil {
		return
	}
	installFlagErrorFuncTree(r.Cmd)
}

func installFlagErrorFuncTree(cmd *cobra.Command) {
	if cmd.Annotations == nil || cmd.Annotations[flagErrorFuncAnnotation] != "true" {
		cmd.SetFlagErrorFunc(flagParseError)
		if cmd.Annotations == nil {
			cmd.Annotations = make(map[string]string)
		}
		cmd.Annotations[flagErrorFuncAnnotation] = "true"
	}
	for _, c := range cmd.Commands() {
		installFlagErrorFuncTree(c)
	}
}

// flagErrorFuncAnnotation marks a command whose FlagErrorFunc kit already
// installed, so a repeated Execute (tests, nested harnesses) is a no-op
// rather than a re-wrap.
const flagErrorFuncAnnotation = "kit.cli.flagError.installed"

// flagParseError maps a pflag parse failure to a structured envelope.
//
// Unrecognized error types fall through to the original error untouched:
// kit must never swallow a parse failure it does not understand, because
// the exit code and the message are the only things the caller has left.
func flagParseError(cmd *cobra.Command, err error) error {
	if err == nil {
		return nil
	}
	var notExist *pflag.NotExistError
	if errors.As(err, &notExist) {
		return unknownFlagError(cmd, notExist)
	}
	var valueRequired *pflag.ValueRequiredError
	if errors.As(err, &valueRequired) {
		return missingFlagValueError(cmd, valueRequired)
	}
	return err
}

// missingFlagValueError renders `--status` (no value) with the flag's legal
// values when it declares an enum, and with its usage otherwise.
//
// This is the case the observed bug made worst: guessing a wrong value got
// the caller the enum, while omitting the value got them nothing.
func missingFlagValueError(cmd *cobra.Command, e *pflag.ValueRequiredError) error {
	name := e.GetSpecifiedName()
	flag := e.GetFlag()
	if flag == nil {
		flag = lookupFlag(cmd, name)
	}
	dashed := flagRef(name, e.GetSpecifiedShortnames())

	out := &output.Error{
		Code:     output.CodeUsage,
		Message:  "missing value for " + dashed,
		Cause:    dashed + " requires a value",
		ExitCode: int(ExitUsage),
	}

	values := flagEnumValues(flag)
	switch {
	case len(values) > 0:
		// The whole point of the enum registry: the caller gets the set
		// without a --help round trip.
		out.SuggestedFix = dashed + " " + values[0]
		out.Alternatives = flagValueAlternatives(dashed, values)
	case flag != nil && flag.Usage != "":
		out.SuggestedFix = dashed + " <" + flagValuePlaceholder(flag) + ">"
		out.Cause = dashed + " requires a value: " + flag.Usage
	default:
		out.SuggestedFix = dashed + " <value>"
	}
	// Retaining keeps the pflag error matchable through errors.As, so a
	// caller testing for *pflag.ValueRequiredError does not lose that
	// because kit added guidance. The envelope itself is what comes back,
	// so errors.As for *output.Error works too — an adopter main reads
	// ExitCode straight off it.
	return out.Retaining(e)
}

// unknownFlagError renders `--count` with the correction when exactly one
// real flag is close enough, and with the candidates when several are.
func unknownFlagError(cmd *cobra.Command, e *pflag.NotExistError) error {
	name := e.GetSpecifiedName()
	dashed := flagRef(name, e.GetSpecifiedShortnames())
	helpPath := helpInvocation(cmd)

	out := &output.Error{
		Code:     output.CodeUsage,
		Message:  "unknown flag " + dashed,
		Cause:    "no flag named " + dashed + " on " + cmd.CommandPath(),
		ExitCode: int(ExitUsage),
	}

	// Shorthand groups (-xyz) carry no name to match against; a single
	// character is within edit distance of far too much to guess from.
	if e.GetSpecifiedShortnames() != "" {
		out.SuggestedFix = helpPath
		return out.Retaining(e)
	}

	switch matches := suggestFlags(name, sortedFlagNames(cmd)); len(matches) {
	case 0:
		out.SuggestedFix = helpPath
	case 1:
		out.SuggestedFix = "--" + matches[0]
	default:
		// Ambiguous. Naming a single winner here would be a guess, and
		// the caller can pick correctly from the shortlist in one step.
		out.SuggestedFix = helpPath
		out.Alternatives = make([]string, 0, len(matches))
		for _, m := range matches {
			out.Alternatives = append(out.Alternatives, "--"+m)
		}
	}
	return out.Retaining(e)
}

// suggestFlags returns the candidate flag names for a mistyped name,
// ordered best-first.
//
// Two signals, in priority order:
//
//  1. Prefix. `--count` against `--counters` is not a typo, it is an
//     abbreviation, and a unique prefix match is the strongest signal
//     available. Prefix matches shadow distance matches entirely — a
//     nearer-by-edits flag is not a better answer than the flag the
//     caller was demonstrably starting to type.
//  2. Levenshtein distance within flagErrorMaxDistance, nearest first.
//
// A one-element result is a correction; a longer one is a shortlist.
func suggestFlags(name string, candidates []string) []string {
	if name == "" || len(candidates) == 0 {
		return nil
	}
	lower := strings.ToLower(name)

	var prefixed []string
	for _, c := range candidates {
		if c != name && strings.HasPrefix(strings.ToLower(c), lower) {
			prefixed = append(prefixed, c)
		}
	}
	if len(prefixed) > 0 {
		return prefixed
	}

	type scored struct {
		name string
		dist int
	}
	var near []scored
	for _, c := range candidates {
		d := levenshtein(lower, strings.ToLower(c))
		if d > 0 && d <= flagErrorMaxDistance {
			near = append(near, scored{name: c, dist: d})
		}
	}
	if len(near) == 0 {
		return nil
	}
	sort.SliceStable(near, func(i, j int) bool {
		if near[i].dist != near[j].dist {
			return near[i].dist < near[j].dist
		}
		return near[i].name < near[j].name
	})
	// Only the nearest tier competes. A distance-1 match next to a
	// distance-2 one is not ambiguous, and padding the shortlist with
	// worse guesses makes the good one harder to see.
	best := near[0].dist
	out := make([]string, 0, len(near))
	for _, s := range near {
		if s.dist != best {
			break
		}
		out = append(out, s.name)
	}
	return out
}

// levenshtein returns the edit distance between a and b using a
// single-row DP (O(min(len)) space). Flag names are short; no dependency
// is warranted for this.
func levenshtein(a, b string) int {
	ar, br := []rune(a), []rune(b)
	if len(ar) < len(br) {
		ar, br = br, ar
	}
	if len(br) == 0 {
		return len(ar)
	}
	prev := make([]int, len(br)+1)
	curr := make([]int, len(br)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(ar); i++ {
		curr[0] = i
		for j := 1; j <= len(br); j++ {
			cost := 1
			if ar[i-1] == br[j-1] {
				cost = 0
			}
			curr[j] = min(prev[j]+1, min(curr[j-1]+1, prev[j-1]+cost))
		}
		prev, curr = curr, prev
	}
	return prev[len(br)]
}

// flagRef renders the flag the way the user typed it, so the error names
// the same token they can find in their own command line.
func flagRef(name, shorthands string) string {
	if shorthands != "" {
		return "-" + name
	}
	return "--" + name
}

// flagValueAlternatives renders one "--flag VALUE" line per legal value.
// Whole invocations rather than bare values: the caller can paste one.
func flagValueAlternatives(dashed string, values []string) []string {
	out := make([]string, 0, len(values))
	for _, v := range values {
		out = append(out, dashed+" "+v)
	}
	return out
}

// flagValuePlaceholder names the value slot for a non-enum flag, using
// pflag's own type name (string, int, duration) so the hint matches what
// --help prints.
func flagValuePlaceholder(flag *pflag.Flag) string {
	if t := flag.Value.Type(); t != "" {
		return t
	}
	return "value"
}

// helpInvocation is the --help command the caller should run when kit has
// nothing better to offer. Full path so it works pasted as-is from a
// nested subcommand.
func helpInvocation(cmd *cobra.Command) string {
	if cmd == nil {
		return "--help"
	}
	return cmd.CommandPath() + " --help"
}
