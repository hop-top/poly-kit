package cli

import (
	"fmt"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"hop.top/kit/go/console/alias"
	"hop.top/kit/go/console/output"
)

type aliasEntry struct {
	Alias  string `table:"ALIAS"  json:"alias"  yaml:"alias"`
	Target string `table:"TARGET" json:"target" yaml:"target"`
}

// AliasesCmd returns a hidden subcommand that lists active aliases.
func (r *Root) AliasesCmd() *cobra.Command {
	return &cobra.Command{
		Use:    "aliases",
		Short:  "List active command aliases",
		Hidden: true,
		Args:   cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			entries := make([]aliasEntry, 0, len(r.aliases))
			for name, target := range r.aliases {
				entries = append(entries, aliasEntry{Alias: name, Target: target})
			}
			sort.Slice(entries, func(i, j int) bool {
				return entries[i].Alias < entries[j].Alias
			})
			return output.Dispatch(cmd, r.Viper, entries)
		},
	}
}

// AliasCmd returns a command group for managing aliases backed by an
// alias.Store. Includes list (default), add, and remove subcommands.
//
// The returned command is self-annotated for §4 (Layer-A)
// conformance: every leaf carries kit/side-effect + Long, and the
// group node itself is marked kit/top-level-verb because it sits at
// depth-1 under adopter roots. Auto-applied verb defaults handle
// kit/idempotent (list→yes, add→no, delete→yes).
func (r *Root) AliasCmd(store *alias.Store) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "alias",
		Short: "Manage command aliases",
		Long: "Manage user-defined command aliases stored in a YAML " +
			"file. Aliases are also registered as runtime shims so the " +
			"aliased name dispatches to its target subcommand. " +
			"Invoking `<tool> alias` with no subcommand lists the " +
			"active aliases, equivalent to `<tool> alias list`.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return r.listAliases(cmd, store)
		},
	}
	SetSideEffect(cmd, SideEffectRead)
	SetIdempotency(cmd, IdempotencyYes)
	SetTopLevelVerb(cmd)

	cmd.AddCommand(r.aliasListCmd(store))
	cmd.AddCommand(r.aliasAddCmd(store))
	cmd.AddCommand(r.aliasRemoveCmd(store))
	return cmd
}

func (r *Root) aliasListCmd(store *alias.Store) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List aliases",
		Long: "Print the active alias table — both YAML-backed entries " +
			"and runtime-registered shims — in the active --format. " +
			"Read-only: no state mutation.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return r.listAliases(cmd, store)
		},
	}
	SetSideEffect(cmd, SideEffectRead)
	return cmd
}

func (r *Root) listAliases(cmd *cobra.Command, store *alias.Store) error {
	all := store.All()
	// merge runtime aliases from r.aliases
	for k, v := range r.aliases {
		if _, ok := all[k]; !ok {
			all[k] = v
		}
	}
	entries := make([]aliasEntry, 0, len(all))
	for name, target := range all {
		entries = append(entries, aliasEntry{Alias: name, Target: target})
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Alias < entries[j].Alias
	})
	return output.Dispatch(cmd, r.Viper, entries)
}

func (r *Root) aliasAddCmd(store *alias.Store) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "add <name> <target...>",
		Short: "Add or update an alias",
		Long: "Persist a new alias (or replace an existing one) in the " +
			"YAML store and register a runtime shim so subsequent " +
			"invocations dispatch to <target>. <target> may include " +
			"flags, captured as a single shell-quoted string.",
		Args: cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			target := strings.Join(args[1:], " ")
			if err := store.Set(name, target); err != nil {
				return err
			}
			if err := store.Save(); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "alias %s → %s\n", name, target)
			return nil
		},
	}
	SetSideEffect(cmd, SideEffectWriteLocal)
	return cmd
}

func (r *Root) aliasRemoveCmd(store *alias.Store) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "delete <name>",
		Aliases: []string{"remove", "rm"},
		Short:   "Delete an alias",
		Long: "Remove an alias from the YAML store and tear down its " +
			"runtime shim. Idempotent: deleting a missing alias " +
			"returns success. The legacy `remove`/`rm` aliases keep " +
			"working through the deprecation window.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			if err := store.Remove(name); err != nil {
				return err
			}
			if err := store.Save(); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "deleted alias %s\n", name)
			return nil
		},
	}
	SetSideEffect(cmd, SideEffectWriteLocal)
	return cmd
}
