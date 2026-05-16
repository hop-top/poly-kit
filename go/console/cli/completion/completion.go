// Package completion provides dynamic value completion for cobra CLI commands.
package completion

import (
	"context"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// Item is a single completion suggestion.
type Item struct {
	Value       string
	Description string
}

// Completer produces completion suggestions for a given prefix.
type Completer interface {
	Complete(ctx context.Context, prefix string) ([]Item, error)
}

// --- Static ---

type staticCompleter struct {
	items []Item
}

// Static returns a Completer that filters fixed items by prefix (case-insensitive).
func Static(items ...Item) Completer {
	return &staticCompleter{items: items}
}

// StaticValues is shorthand for Static with description-less items.
func StaticValues(values ...string) Completer {
	items := make([]Item, len(values))
	for i, v := range values {
		items[i] = Item{Value: v}
	}
	return Static(items...)
}

func (c *staticCompleter) Complete(_ context.Context, prefix string) ([]Item, error) {
	return filterPrefix(c.items, prefix), nil
}

// --- Func ---

type funcCompleter struct {
	fn func(ctx context.Context, prefix string) ([]Item, error)
}

// Func wraps a callback as a Completer.
func Func(fn func(ctx context.Context, prefix string) ([]Item, error)) Completer {
	return &funcCompleter{fn: fn}
}

func (c *funcCompleter) Complete(ctx context.Context, prefix string) ([]Item, error) {
	return c.fn(ctx, prefix)
}

// --- Prefixed ---

type prefixedCompleter struct {
	dimension string
	values    Completer
}

// Prefixed completes key:value patterns. Suggests "dimension:" when no colon
// is present, then delegates to values completer for the part after ":".
func Prefixed(dimension string, values Completer) Completer {
	return &prefixedCompleter{dimension: dimension, values: values}
}

func (c *prefixedCompleter) Complete(ctx context.Context, prefix string) ([]Item, error) {
	idx := strings.IndexByte(prefix, ':')
	if idx < 0 {
		// No colon — suggest dimension prefix if it matches.
		dim := c.dimension + ":"
		if strings.HasPrefix(strings.ToLower(dim), strings.ToLower(prefix)) {
			return []Item{{Value: dim, Description: c.dimension + " values"}}, nil
		}
		return nil, nil
	}

	// Has colon — check dimension matches.
	dim := prefix[:idx]
	if !strings.EqualFold(dim, c.dimension) {
		return nil, nil
	}

	valPrefix := prefix[idx+1:]
	items, err := c.values.Complete(ctx, valPrefix)
	if err != nil {
		return nil, err
	}
	// Prepend dimension: to each value.
	out := make([]Item, len(items))
	for i, it := range items {
		out[i] = Item{
			Value:       c.dimension + ":" + it.Value,
			Description: it.Description,
		}
	}
	return out, nil
}

// --- ConfigKeys ---

type configKeysCompleter struct {
	v *viper.Viper
}

// ConfigKeys returns all viper config keys as completions.
func ConfigKeys(v *viper.Viper) Completer {
	return &configKeysCompleter{v: v}
}

func (c *configKeysCompleter) Complete(_ context.Context, prefix string) ([]Item, error) {
	keys := c.v.AllKeys()
	items := make([]Item, len(keys))
	for i, k := range keys {
		items[i] = Item{Value: k}
	}
	return filterPrefix(items, prefix), nil
}

// --- File / Dir ---

type fileCompleter struct {
	extensions []string
	dirOnly    bool
}

// File returns a Completer that signals cobra to filter by file extensions.
func File(extensions ...string) Completer {
	return &fileCompleter{extensions: extensions}
}

// Dir returns a Completer that signals cobra to complete directories only.
func Dir() Completer {
	return &fileCompleter{dirOnly: true}
}

func (c *fileCompleter) Complete(_ context.Context, _ string) ([]Item, error) {
	return nil, nil // items unused; directive drives shell behavior
}

// --- Registry ---

// Registry maps flag names and arg positions to Completers.
type Registry struct {
	flags map[string]Completer
	args  map[string]map[int]Completer // cmd name → pos → completer
}

// NewRegistry creates an empty Registry.
func NewRegistry() *Registry {
	return &Registry{
		flags: make(map[string]Completer),
		args:  make(map[string]map[int]Completer),
	}
}

// Register associates a Completer with a flag name.
func (r *Registry) Register(flag string, c Completer) {
	r.flags[flag] = c
}

// RegisterArg associates a Completer with a command name and arg position.
func (r *Registry) RegisterArg(cmd string, pos int, c Completer) {
	if r.args[cmd] == nil {
		r.args[cmd] = make(map[int]Completer)
	}
	r.args[cmd][pos] = c
}

// ForFlag returns the Completer for the given flag, or nil.
func (r *Registry) ForFlag(flag string) Completer {
	return r.flags[flag]
}

// ForArg returns the Completer for the given command and position, or nil.
func (r *Registry) ForArg(cmd string, pos int) Completer {
	if m, ok := r.args[cmd]; ok {
		return m[pos]
	}
	return nil
}

// --- Cobra bridge ---

// BindFlag registers a Completer as cobra's flag completion function.
func BindFlag(cmd *cobra.Command, flag string, c Completer) {
	_ = cmd.RegisterFlagCompletionFunc(flag,
		func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
			return resolve(cmd.Context(), c, toComplete)
		},
	)
}

// BindArgs sets cmd.ValidArgsFunction using the given Completer.
func BindArgs(cmd *cobra.Command, c Completer) {
	cmd.ValidArgsFunction = func(
		cmd *cobra.Command, args []string, toComplete string,
	) ([]string, cobra.ShellCompDirective) {
		return resolve(cmd.Context(), c, toComplete)
	}
}

// resolve converts Completer output to cobra's format.
func resolve(ctx context.Context, c Completer, toComplete string) ([]string, cobra.ShellCompDirective) {
	// File/Dir completers use directives, not items.
	if fc, ok := c.(*fileCompleter); ok {
		if fc.dirOnly {
			return nil, cobra.ShellCompDirectiveFilterDirs
		}
		return nil, cobra.ShellCompDirectiveFilterFileExt
	}

	items, err := c.Complete(ctx, toComplete)
	if err != nil {
		return nil, cobra.ShellCompDirectiveError
	}

	out := make([]string, len(items))
	for i, it := range items {
		if it.Description != "" {
			out[i] = it.Value + "\t" + it.Description
		} else {
			out[i] = it.Value
		}
	}
	return out, cobra.ShellCompDirectiveNoFileComp
}

// --- helpers ---

func filterPrefix(items []Item, prefix string) []Item {
	if prefix == "" {
		cp := make([]Item, len(items))
		copy(cp, items)
		return cp
	}
	lp := strings.ToLower(prefix)
	var out []Item
	for _, it := range items {
		if strings.HasPrefix(strings.ToLower(it.Value), lp) {
			out = append(out, it)
		}
	}
	return out
}
