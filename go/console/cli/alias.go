package cli

import (
	"fmt"
	"maps"
	"slices"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"hop.top/kit/go/console/alias"
)

// AliasHelp selects what `<alias> --help` shows on an alias proxy.
type AliasHelp int

const (
	// AliasHelpDefault keeps the proxy's default: Go proxies own --help.
	AliasHelpDefault AliasHelp = iota
	// AliasHelpOwn shows the proxy's own help (its description).
	AliasHelpOwn
	// AliasHelpPassthrough shows the target's help; with a custom
	// dispatch or register-only, --help reaches the proxy's arguments.
	AliasHelpPassthrough
)

// AliasDispatchFunc runs an alias proxy in place of its target. cmd is
// the proxy, name the alias, target its resolved command path and args
// every argument after the alias, unparsed.
type AliasDispatchFunc func(cmd *cobra.Command, name, target string, args []string) error

// AliasOption configures how Root.Alias, LoadAliases and LoadAliasStore
// register an alias. Passing any option registers every alias as a
// proxy: a root command named after the alias, even for a direct child
// that would otherwise take a cobra alias.
type AliasOption func(*aliasConfig)

type aliasConfig struct {
	hidden       bool
	describe     func(name, target string) string
	help         AliasHelp
	dispatch     AliasDispatchFunc
	registerOnly bool
}

// WithAliasHidden sets whether proxies are hidden from help. Proxies
// are hidden by default.
func WithAliasHidden(hidden bool) AliasOption {
	return func(c *aliasConfig) { c.hidden = hidden }
}

// WithAliasDescribe sets a proxy's short description from its name and
// target path, in place of "Alias for <target>".
func WithAliasDescribe(fn func(name, target string) string) AliasOption {
	return func(c *aliasConfig) { c.describe = fn }
}

// WithAliasHelp selects what `<alias> --help` shows.
func WithAliasHelp(mode AliasHelp) AliasOption {
	return func(c *aliasConfig) { c.help = mode }
}

// WithAliasDispatch runs fn instead of the target when the alias is
// invoked. The proxy parses no flags: fn gets every argument as given.
func WithAliasDispatch(fn AliasDispatchFunc) AliasOption {
	return func(c *aliasConfig) { c.dispatch = fn; c.registerOnly = false }
}

// WithAliasRegisterOnly registers proxies that run nothing: the alias
// shows in help and completion while the tool dispatches it itself,
// say by rewriting arguments before Execute.
func WithAliasRegisterOnly() AliasOption {
	return func(c *aliasConfig) { c.registerOnly = true; c.dispatch = nil }
}

// Alias registers name as a root-level alias for target. If target is a
// direct child of Root.Cmd, cobra's Aliases field is used. For deeper
// commands a shim command is added to Root.Cmd that delegates to target.
// With any AliasOption every alias gets a proxy instead (see
// AliasOption).
//
// Returns an error on collision with an existing command or alias, or if
// name is empty / contains whitespace. alias.ValidateName applies the
// same rules without a command tree.
func (r *Root) Alias(name string, target *cobra.Command, opts ...AliasOption) error {
	if name == "" || strings.ContainsAny(name, " \t\n") {
		return fmt.Errorf("alias name %q must be non-empty without whitespace", name)
	}
	for _, c := range r.Cmd.Commands() {
		if c.Name() == name {
			return fmt.Errorf("alias %q collides with command %q", name, c.Name())
		}
	}
	if _, ok := r.aliases[name]; ok {
		return fmt.Errorf("alias %q already registered for %q", name, r.aliases[name])
	}

	path := commandPath(r.Cmd, target)

	switch {
	case len(opts) != 0:
		cfg := aliasConfig{hidden: true}
		for _, o := range opts {
			o(&cfg)
		}
		r.Cmd.AddCommand(r.aliasProxy(name, path, target, cfg))
		if !cfg.hidden || cfg.dispatch != nil {
			if r.aliasCommands == nil {
				r.aliasCommands = map[string]bool{}
			}
			r.aliasCommands[name] = true
		}
	case target.Parent() == r.Cmd:
		target.Aliases = append(target.Aliases, name)
	default:
		// The shim dispatches the target's RunE, so it carries the
		// target's annotations: the confirm gate, the dry-run policy
		// and validation read the command cobra dispatched — the
		// shim — and would otherwise treat a destructive target as
		// untagged.
		shim := &cobra.Command{
			Use:         name,
			Short:       fmt.Sprintf("Alias for %s", path),
			Hidden:      true,
			RunE:        target.RunE,
			Run:         target.Run,
			Annotations: maps.Clone(target.Annotations),
		}
		r.Cmd.AddCommand(shim)
	}

	r.aliases[name] = path
	r.registerAliasCompletions()
	return nil
}

// aliasProxy builds the root command an AliasOption registration adds.
// Like the default shim it carries the target's annotations. Dispatching
// to the target it also carries the target's flags, so they parse as on
// the target; a custom dispatch or register-only proxy parses none.
func (r *Root) aliasProxy(name, path string, target *cobra.Command, cfg aliasConfig) *cobra.Command {
	short := fmt.Sprintf("Alias for %s", path)
	if cfg.describe != nil {
		short = cfg.describe(name, path)
	}
	proxy := &cobra.Command{
		Use:         name,
		Short:       short,
		Hidden:      cfg.hidden,
		Annotations: maps.Clone(target.Annotations),
	}
	ownHelp := cfg.help != AliasHelpPassthrough

	if cfg.dispatch != nil || cfg.registerOnly {
		proxy.DisableFlagParsing = true
		proxy.Args = cobra.ArbitraryArgs
		proxy.RunE = func(cmd *cobra.Command, args []string) error {
			if ownHelp && wantsHelp(args) {
				return cmd.Help()
			}
			if cfg.registerOnly {
				return nil
			}
			return cfg.dispatch(cmd, name, path, args)
		}
		return proxy
	}

	proxy.RunE = target.RunE
	proxy.Run = target.Run
	proxy.Args = target.Args
	flags := proxy.Flags()
	flags.AddFlagSet(target.LocalFlags())
	target.InheritedFlags().VisitAll(func(f *pflag.Flag) {
		if r.Cmd.PersistentFlags().Lookup(f.Name) == nil && flags.Lookup(f.Name) == nil {
			flags.AddFlag(f)
		}
	})
	if !ownHelp {
		// Kit installs every command's help func when the tree is
		// prepared; installHelpRecursive points this one at the target.
		if r.aliasHelpTargets == nil {
			r.aliasHelpTargets = map[*cobra.Command]*cobra.Command{}
		}
		r.aliasHelpTargets[proxy] = target
	}
	return proxy
}

// wantsHelp reports whether args ask for help before any "--".
func wantsHelp(args []string) bool {
	end := slices.Index(args, "--")
	if end < 0 {
		end = len(args)
	}
	return slices.ContainsFunc(args[:end], func(a string) bool { return a == "-h" || a == "--help" })
}

// Aliases returns a copy of the registered alias-to-target mapping.
func (r *Root) Aliases() map[string]string {
	out := make(map[string]string, len(r.aliases))
	for k, v := range r.aliases {
		out[k] = v
	}
	return out
}

// LoadAliases reads the "aliases" map from Viper config and registers each
// entry with opts (see Root.Alias). Unknown target paths produce an
// error. Iteration order is sorted by alias name for deterministic
// behavior.
func (r *Root) LoadAliases(opts ...AliasOption) error {
	raw := r.Viper.GetStringMapString("aliases")
	if len(raw) == 0 {
		return nil
	}

	names := make([]string, 0, len(raw))
	for n := range raw {
		names = append(names, n)
	}
	sort.Strings(names)

	var errs []string
	for _, name := range names {
		path := raw[name]
		cmd, err := r.resolveCommand(path)
		if err != nil {
			errs = append(errs, fmt.Sprintf("%s -> %s: %v", name, path, err))
			continue
		}
		if err := r.Alias(name, cmd, opts...); err != nil {
			errs = append(errs, fmt.Sprintf("%s -> %s: %v", name, path, err))
		}
	}
	if len(errs) != 0 {
		return fmt.Errorf("alias errors:\n  %s", strings.Join(errs, "\n  "))
	}
	return nil
}

// LoadAliasStore loads aliases from an alias.Store and registers each as
// a cobra command with opts (see Root.Alias). Unknown targets are
// collected and returned as a single error.
func (r *Root) LoadAliasStore(store *alias.Store, opts ...AliasOption) error {
	all := store.All()
	if len(all) == 0 {
		return nil
	}

	names := make([]string, 0, len(all))
	for n := range all {
		names = append(names, n)
	}
	sort.Strings(names)

	var errs []string
	for _, name := range names {
		path := all[name]
		cmd, err := r.resolveCommand(path)
		if err != nil {
			errs = append(errs, fmt.Sprintf("%s -> %s: %v", name, path, err))
			continue
		}
		if err := r.Alias(name, cmd, opts...); err != nil {
			errs = append(errs, fmt.Sprintf("%s -> %s: %v", name, path, err))
		}
	}
	if len(errs) != 0 {
		return fmt.Errorf("alias errors:\n  %s", strings.Join(errs, "\n  "))
	}
	return nil
}

// resolveCommand walks r.Cmd's subcommand tree using a space-separated path.
func (r *Root) resolveCommand(path string) (*cobra.Command, error) {
	parts := strings.Fields(path)
	if len(parts) == 0 {
		return nil, fmt.Errorf("empty command path")
	}
	cur := r.Cmd
	for _, p := range parts {
		found := false
		for _, c := range cur.Commands() {
			if c.Name() == p {
				cur = c
				found = true
				break
			}
		}
		if !found {
			return nil, fmt.Errorf("command %q not found in %q", p, cur.Name())
		}
	}
	return cur, nil
}

// registerAliasCompletions sets ValidArgsFunction on Root.Cmd to include
// alias names alongside any previously registered completions. An alias
// whose proxy is a real command (visible, or with a custom dispatch) is
// left out: cobra completes it as a subcommand, with its own flags, and
// listing it here too would offer it twice.
func (r *Root) registerAliasCompletions() {
	aliases := r.aliases // capture current map ref
	original := r.Cmd.ValidArgsFunction

	// avoid re-wrapping: if we already wrapped, the closure below
	// captures r.aliases by reference so new entries are visible.
	if r.aliasCompletionSet {
		return
	}
	r.aliasCompletionSet = true

	r.Cmd.ValidArgsFunction = func(
		cmd *cobra.Command, args []string, toComplete string,
	) ([]string, cobra.ShellCompDirective) {
		var results []string
		directive := cobra.ShellCompDirectiveNoFileComp
		if original != nil {
			orig, origDir := original(cmd, args, toComplete)
			results = append(results, orig...)
			directive |= origDir
		}
		for name, target := range aliases {
			if r.aliasCommands[name] {
				continue
			}
			if strings.HasPrefix(name, toComplete) {
				results = append(results, name+"\t"+"alias for "+target)
			}
		}
		return results, directive
	}
}

// commandPath returns the target's path relative to root, e.g. "router start".
func commandPath(root, target *cobra.Command) string {
	full := target.CommandPath()
	prefix := root.Name() + " "
	return strings.TrimPrefix(full, prefix)
}
