package output

import (
	"strings"
	"sync"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// registryStash maps *cobra.Command → *Registry for commands wired with
// WithRegistry. Dispatch uses this to recover the active registry when it
// differs from Default.
var registryStash sync.Map

// Viper keys bound by RegisterFlagsWith. Callers that read these keys
// after flag parsing should use these constants to avoid typos.
const (
	flagFormat     = "format"
	flagFormatOpt  = "format-opt"
	flagFormatHelp = "format-help"
	flagCols       = "cols"
	flagColumns    = "columns"
	flagTemplate   = "template"
	flagOutput     = "output"
)

// RegistryOption customizes RegisterFlagsWith. Use the With* / Disable*
// helpers below; do not construct registryOptions directly.
type RegistryOption func(*registryOptions)

type registryOptions struct {
	registry      *Registry
	disableOutput bool
}

// WithRegistry binds Dispatch / format-help to a specific Registry rather
// than the package-level Default. Useful for tests and multi-CLI binaries
// that want isolated formatter sets.
func WithRegistry(r *Registry) RegistryOption {
	return func(o *registryOptions) { o.registry = r }
}

// DisableOutputFlag suppresses registration of the --output / -o flag. Use
// for commands or sub-CLIs that must always write to stdout (e.g. when
// stdout is part of a pipeline contract).
func DisableOutputFlag() RegistryOption {
	return func(o *registryOptions) { o.disableOutput = true }
}

// RegisterFlagsWith adds the output persistent flags to cmd and binds
// them to viper keys. Pass options to swap registries or suppress
// individual flags.
//
// Flags registered (all persistent on cmd):
//   - --format <key>           default "table"; bound to "format"
//   - --format-opt key=value   StringSlice, repeatable; bound to "format-opt"
//   - --format-help [format]   bool toggle; bound to "format-help"
//   - --cols, --columns        StringSlice, repeatable + comma-split;
//     bound to "cols" (and "columns" alias)
//   - --template <go-tmpl>     string; bound to "template"
//   - --output, -o <path>      string; bound to "output"; "" or "-" = stdout
//
// The registry used for format lookup defaults to Default; pass
// WithRegistry to override. Pass DisableOutputFlag to suppress -o.
func RegisterFlagsWith(cmd *cobra.Command, v *viper.Viper, opts ...RegistryOption) {
	o := &registryOptions{registry: Default}
	for _, apply := range opts {
		apply(o)
	}

	pf := cmd.PersistentFlags()

	pf.String(flagFormat, Table, "Output format ("+strings.Join(o.registry.Keys(), ", ")+")")
	_ = v.BindPFlag(flagFormat, pf.Lookup(flagFormat))

	pf.StringSlice(flagFormatOpt, nil,
		"Per-format option as key=value (repeatable; bool keys may omit =value)")
	_ = v.BindPFlag(flagFormatOpt, pf.Lookup(flagFormatOpt))

	pf.Bool(flagFormatHelp, false,
		"Show available formats and their options (use --format <key> --format-help for one)")
	_ = v.BindPFlag(flagFormatHelp, pf.Lookup(flagFormatHelp))

	pf.StringSlice(flagCols, nil,
		"Restrict columns to this comma-separated list (repeatable)")
	_ = v.BindPFlag(flagCols, pf.Lookup(flagCols))

	// --columns is a strict alias of --cols. Both bind to the same viper
	// key so callers can read either.
	pf.StringSlice(flagColumns, nil, "Alias for --cols")
	_ = v.BindPFlag(flagColumns, pf.Lookup(flagColumns))

	pf.String(flagTemplate, "",
		"Go text/template applied to results (mutually exclusive with --cols)")
	_ = v.BindPFlag(flagTemplate, pf.Lookup(flagTemplate))

	if !o.disableOutput {
		pf.StringP(flagOutput, "o", "",
			"Write output to path (use - or empty for stdout)")
		_ = v.BindPFlag(flagOutput, pf.Lookup(flagOutput))
	}

	// Stash the resolved registry so Dispatch can recover it without
	// threading RegistryOption through every callsite.
	if o.registry != Default {
		registryStash.Store(cmd, o.registry)
	}
}

// registryFor walks cmd's parent chain and returns the registry stashed
// on the nearest ancestor (or Default if none).
func registryFor(cmd *cobra.Command) *Registry {
	for c := cmd; c != nil; c = c.Parent() {
		if v, ok := registryStash.Load(c); ok {
			if r, ok := v.(*Registry); ok {
				return r
			}
		}
	}
	return Default
}

// resolveCols merges --cols + --columns values into a single ordered,
// deduped slice. Each value may itself be comma-separated (cobra
// StringSlice does not split on comma when values arrive via viper
// config). When cmd is non-nil and the flag was changed on the command
// line, that takes precedence over viper — keeping behavior consistent
// with adopters that pass an unbound viper to Dispatch.
func resolveCols(cmd *cobra.Command, v *viper.Viper) []string {
	raw := append([]string{}, lookupStringSlice(cmd, v, flagCols)...)
	raw = append(raw, lookupStringSlice(cmd, v, flagColumns)...)
	seen := make(map[string]struct{}, len(raw))
	out := make([]string, 0, len(raw))
	for _, item := range raw {
		for _, part := range strings.Split(item, ",") {
			p := strings.TrimSpace(part)
			if p == "" {
				continue
			}
			if _, dup := seen[p]; dup {
				continue
			}
			seen[p] = struct{}{}
			out = append(out, p)
		}
	}
	return out
}
