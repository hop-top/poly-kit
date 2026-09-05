package cli

import (
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"hop.top/kit/go/console/output"
)

// checkUnknownSubcommand resolves the invocation against the tree and
// refuses a leading word that names no child of a non-runnable
// command, returning an [output.UsageError] so the taxonomy gives
// exit 2 and every surface maps it (REST 400, socket exit_code 2).
//
// Cobra hands a non-runnable command straight to its help renderer:
// there is no RunE, so no Args validator and none of kit's RunE
// middleware ever runs, and the invocation exits 0 as if it had
// succeeded. A runnable command needs no help here — cobra validates
// its Args, and kit's middleware classifies what that returns — so
// this check is aimed only at the gap.
//
// It reports nil for everything that is not that gap: a resolved
// runnable command (its own Args rules the operands), a bare
// non-runnable command (help, exit 0, the documented behavior), a
// completion request or a leading `help`, and a malformed flag,
// which is the first thing wrong with the command line and is
// diagnosed as the flag error it is.
//
// A help flag is honored on cobra's own terms, which are positional
// rather than "a help flag anywhere wins". Cobra tests the flag
// inside the resolved command's execute, so it is only reached once
// Find has routed: `tool --help bogus` prints help, because
// stripFlags drops the leading flag and no word precedes it, while
// `tool bogus --help` is refused before any execute runs. Real cobra
// trees agree — gh and kubectl both exit non-zero on `bogus --help`.
// So a help flag standing ahead of the unknown word suppresses the
// refusal; one standing behind it does not.
func (r *Root) checkUnknownSubcommand(args []string) error {
	if r == nil || r.Cmd == nil {
		return nil
	}

	// Completion drives itself through hidden commands that are not
	// children of the root, and `help` is cobra's own command taking
	// a command path as its operand. Neither is a dispatch this
	// check has anything to say about.
	if len(args) > 0 && isReservedDispatchWord(args[0]) {
		return nil
	}

	target, rest, _ := r.Cmd.Find(args)
	if target == nil || target.Runnable() {
		return nil
	}

	// Find leaves flags in rest, so strip them the way cobra does
	// before parsing. Two outcomes short-circuit the check:
	//
	//   - ErrHelp: the leftovers carry a help flag, so this is a help
	//     request. Cobra reaches its own help check inside the
	//     resolved command's execute, which is exactly the point this
	//     stands in for.
	//   - any other parse failure: a malformed flag is the first thing
	//     wrong with the line, so let the flag machinery name it.
	words, err := strippedWords(target, rest)
	switch {
	case errors.Is(err, pflag.ErrHelp):
		return nil
	case err != nil:
		return nil
	case len(words) == 0:
		return nil
	}

	// A help flag standing ahead of the unknown word is a help
	// request too, on cobra's positional terms.
	if helpFlagPrecedes(target, rest, words[0]) {
		return nil
	}

	return output.UsageError(fmt.Sprintf("unknown command %q for %q%s",
		words[0], target.CommandPath(), suggestionsFor(target, words[0])))
}

// helpFlagPrecedes reports whether a help flag appears in args before
// word — the position at which cobra would have reached its own help
// check and never looked at the word. It matches --help, -h, and the
// --help-<id> family this package registers (--help-all and one per
// command group), including their --flag=value spellings, and stops
// at "--", after which nothing is a flag.
func helpFlagPrecedes(cmd *cobra.Command, args []string, word string) bool {
	for _, a := range args {
		if a == "--" {
			return false
		}
		if a == word {
			return false
		}
		if isHelpFlagToken(cmd, a) {
			return true
		}
	}
	return false
}

// isHelpFlagToken reports whether tok is a help flag: the long or
// short help flag, or any --help-<id> variant registered on cmd's
// root. The value half of a --flag=value token is ignored.
func isHelpFlagToken(cmd *cobra.Command, tok string) bool {
	if tok == "-h" || tok == "--help" {
		return true
	}
	name, _, _ := strings.Cut(tok, "=")
	switch {
	case strings.HasPrefix(name, "--"):
		name = strings.TrimPrefix(name, "--")
	case strings.HasPrefix(name, "-"):
		return strings.Contains(strings.TrimPrefix(name, "-"), "h")
	default:
		return false
	}
	if name == "help" {
		return true
	}
	// The --help-<id> family is registered on the root, so look
	// there rather than on the command Find resolved to.
	return strings.HasPrefix(name, "help-") && cmd.Root().Flags().Lookup(name) != nil
}

// refuseUnknownSubcommand renders err as the active --format
// demands, the way every other kit error reaches stderr, and silences
// cobra's own printer so the envelope is not doubled. The envelope is
// returned so Execute's caller — and the in-process runner behind the
// served surfaces — reads the taxonomy code off it.
func (r *Root) refuseUnknownSubcommand(err error) error {
	ce := toCLIError(err)
	ce.SuggestedFix = "run '" + r.Cmd.CommandPath() + " --help' for usage"
	_ = output.RenderError(r.Cmd.ErrOrStderr(), activeFormat(r.Cmd), ce)
	r.Cmd.SilenceErrors = true
	r.Cmd.SilenceUsage = true
	return ce
}

// isReservedDispatchWord reports whether w is a leading word cobra
// routes itself: the two hidden completion request commands, and
// `help`, whose operand is a command path rather than a subcommand
// of the command being resolved.
func isReservedDispatchWord(w string) bool {
	switch w {
	case cobra.ShellCompRequestCmd, cobra.ShellCompNoDescRequestCmd, "help":
		return true
	}
	return false
}

// strippedWords returns the non-flag words of args as cobra's own
// resolution sees them, and reports a parse error when a flag in
// args is malformed — including pflag.ErrHelp when the args carry a
// help flag, which the caller reads as a help request. Parsing runs
// against a throwaway copy of the command's flag set so the real
// flags keep their values for the parse cobra is about to do.
func strippedWords(cmd *cobra.Command, args []string) ([]string, error) {
	probe := pflag.NewFlagSet(cmd.Name(), pflag.ContinueOnError)
	probe.ParseErrorsAllowlist.UnknownFlags = true
	probe.SetOutput(discardWriter{})
	cmd.Flags().VisitAll(func(f *pflag.Flag) {
		probe.AddFlag(&pflag.Flag{
			Name:        f.Name,
			Shorthand:   f.Shorthand,
			Usage:       f.Usage,
			Value:       noopValue{typ: f.Value.Type(), def: f.DefValue},
			DefValue:    f.DefValue,
			NoOptDefVal: f.NoOptDefVal,
		})
	})
	if err := probe.Parse(args); err != nil {
		return nil, err
	}
	return probe.Args(), nil
}

// suggestionsFor returns cobra's own "did you mean" block for word,
// so a refusal reads exactly the way cobra's does at the root. The
// candidate set and the distance rule are cobra's, not a second
// implementation of them.
func suggestionsFor(cmd *cobra.Command, word string) string {
	if cmd.DisableSuggestions {
		return ""
	}
	if cmd.SuggestionsMinimumDistance <= 0 {
		cmd.SuggestionsMinimumDistance = 2
	}
	names := cmd.SuggestionsFor(word)
	if len(names) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n\nDid you mean this?\n")
	for _, n := range names {
		fmt.Fprintf(&b, "\t%v\n", n)
	}
	return b.String()
}

// noopValue stands in for a real flag value while the probe parse
// walks the command line: it accepts anything and stores nothing, so
// the probe learns where the words are without disturbing the flags
// cobra is about to parse for real.
type noopValue struct {
	typ string
	def string
}

func (v noopValue) String() string   { return v.def }
func (v noopValue) Set(string) error { return nil }
func (v noopValue) Type() string     { return v.typ }

// discardWriter swallows pflag's own error rendering; the caller
// reports the parse failure by returning it.
type discardWriter struct{}

func (discardWriter) Write(p []byte) (int, error) { return len(p), nil }
