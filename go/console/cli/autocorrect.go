package cli

import (
	"os"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// Opt-in flag autocorrect.
//
// A parse-time flag error already comes back as a USAGE envelope carrying a
// concrete suggested_fix (see errcorrect.go). That is the whole contract by
// default: kit suggests, the caller retries, the exit code stays 2.
//
// Two opt-in modes let a caller skip that round trip. Both are off unless
// the operator asks for them, because the same seam serves destructive
// verbs and a wrong guess there costs far more than the retry it saves:
//
//   - read: apply the correction and run, for read-only leaves only.
//   - prompt: ask on a TTY; degrade to suggest-only anywhere else.
//
// Modeled on git's help.autocorrect and on kit's own --confirm precedent
// ("prompt on a TTY, no otherwise"). Deliberately a SEPARATE key from
// --confirm: consenting to a destructive action and accepting a spelling
// correction are different consents, so --confirm=yes in a script must
// never imply an autocorrect the operator did not ask for.

// AutocorrectMode is the resolved cli.autocorrect policy.
type AutocorrectMode string

const (
	// AutocorrectOff is the default: suggest, never apply. The
	// envelope carries the fix and the process exits USAGE (2).
	AutocorrectOff AutocorrectMode = "off"
	// AutocorrectPrompt asks before applying, on a TTY only. Anywhere
	// else it is indistinguishable from AutocorrectOff — a prompt that
	// cannot be answered must never block a pipeline.
	AutocorrectPrompt AutocorrectMode = "prompt"
	// AutocorrectRead applies the correction without asking, and only
	// on a leaf whose kit/side-effect is read. A write or destructive
	// leaf stays suggest-only in this mode: the side-effect gate is
	// not something the mode can lift.
	AutocorrectRead AutocorrectMode = "read"
)

// autocorrectFlag is the global that carries the policy at invocation
// time. Hidden like the other kit-owned plumbing globals.
const autocorrectFlag = "autocorrect"

// autocorrectViperKey is the config key the flag binds to, so a value
// set in a config file resolves through the same precedence chain every
// other kit global uses.
const autocorrectViperKey = "cli.autocorrect"

// autocorrectEnv is the environment override. Named explicitly rather
// than left to viper's AutomaticEnv because kit's roots do not enable
// it: the env rung would silently not exist otherwise.
const autocorrectEnv = "KIT_AUTOCORRECT"

// parseAutocorrectMode maps a raw string onto the closed set, returning
// false for anything unrecognized so a caller can fall through to the
// next rung of the precedence chain rather than silently reading a typo
// as "off". An empty string is not a value; it is an unset rung.
func parseAutocorrectMode(raw string) (AutocorrectMode, bool) {
	switch AutocorrectMode(strings.ToLower(strings.TrimSpace(raw))) {
	case AutocorrectOff:
		return AutocorrectOff, true
	case AutocorrectPrompt:
		return AutocorrectPrompt, true
	case AutocorrectRead:
		return AutocorrectRead, true
	}
	return AutocorrectOff, false
}

// autocorrectMode resolves the active policy for cmd.
//
// Precedence, highest first:
//
//  1. --autocorrect on the command line.
//  2. KIT_AUTOCORRECT in the environment.
//  3. cli.autocorrect from the config chain (viper).
//  4. off.
//
// The flag is read through Changed rather than through viper's bound
// value so an explicitly-passed --autocorrect=off beats an env var that
// says otherwise. viper's BindPFlag would collapse those two rungs.
//
// An unrecognized value at any rung falls through to the next rather
// than failing the invocation: this policy decides whether to offer a
// convenience, and refusing to run over a mistyped convenience setting
// would be worse than ignoring it.
func (r *Root) autocorrectMode(cmd *cobra.Command) AutocorrectMode {
	if f := lookupPersistentFlag(cmd, autocorrectFlag); f != nil && f.Changed {
		if m, ok := parseAutocorrectMode(f.Value.String()); ok {
			return m
		}
	}
	if m, ok := parseAutocorrectMode(os.Getenv(autocorrectEnv)); ok {
		return m
	}
	if r != nil && r.Viper != nil {
		if m, ok := parseAutocorrectMode(r.Viper.GetString(autocorrectViperKey)); ok {
			return m
		}
	}
	return AutocorrectOff
}

// lookupPersistentFlag finds a flag visible to cmd, walking up the
// parent chain. Mirrors flagValue's search but returns the *pflag.Flag
// so a caller can read Changed as well as the value.
func lookupPersistentFlag(cmd *cobra.Command, name string) *pflag.Flag {
	for c := cmd; c != nil; c = c.Parent() {
		if f := c.PersistentFlags().Lookup(name); f != nil {
			return f
		}
		if f := c.Flags().Lookup(name); f != nil {
			return f
		}
	}
	return nil
}
