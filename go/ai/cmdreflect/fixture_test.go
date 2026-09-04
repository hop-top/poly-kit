package cmdreflect

import (
	"github.com/spf13/cobra"
)

// fixtureRoot builds a command tree that exercises every rule the
// walker applies. Each command's name says which rule it is there to
// trip, so a failure names the rule directly.
//
// The tree deliberately mixes concerns on some nodes (hidden AND
// deprecated, destructive AND named "delete") so the precedence
// order is exercised rather than assumed.
func fixtureRoot() *cobra.Command {
	root := &cobra.Command{Use: "fix", Short: "fixture root"}
	root.PersistentFlags().String("config", "", "config file")
	root.PersistentFlags().Bool("verbose", false, "verbose output")

	// Plain read leaf: the baseline invocable command.
	read := &cobra.Command{
		Use:   "list",
		Short: "list things",
		Long:  "list every thing in the store",
		Args:  cobra.NoArgs,
		Run:   func(*cobra.Command, []string) {},
		Annotations: map[string]string{
			annSideEffect: "read",
			annIdempotent: "yes",
			annExitCodes:  "OK,NOT_FOUND",
		},
	}
	read.Flags().StringP("filter", "f", "none", "filter expression")
	read.Flags().Int("limit", 10, "max rows")
	_ = read.MarkFlagRequired("filter")
	read.Flags().String("legacy", "", "old flag")
	_ = read.Flags().MarkDeprecated("legacy", "use --filter")
	read.Flags().String("secret", "", "internal")
	_ = read.Flags().MarkHidden("secret")
	root.AddCommand(read)

	// Write leaf with positional args and a network annotation.
	write := &cobra.Command{
		Use:   "add",
		Short: "add a thing",
		Run:   func(*cobra.Command, []string) {},
		Annotations: map[string]string{
			annSideEffect: "write-local",
			annArgs:       "name,description?",
			annNetwork:    "egress:public",
			annExec:       "true",
			annFlagSince:  "force=1.2",
		},
	}
	write.Flags().Bool("force", false, "skip checks")
	root.AddCommand(write)

	// Destructive leaf, explicitly annotated.
	root.AddCommand(&cobra.Command{
		Use:   "purge-store",
		Short: "purge the store",
		Run:   func(*cobra.Command, []string) {},
		Annotations: map[string]string{
			annSideEffect:       "destructive-shared",
			annAuthRequired:     "true",
			annDestructiveToken: "required",
			annBusPublish:       "true",
		},
	})

	// Unannotated command whose NAME trips the destructive
	// heuristic. No kit/side-effect at all.
	root.AddCommand(&cobra.Command{
		Use:   "drop",
		Short: "drop a thing",
		Run:   func(*cobra.Command, []string) {},
	})

	// Interactive leaf.
	root.AddCommand(&cobra.Command{
		Use:         "shell",
		Short:       "open a shell",
		Run:         func(*cobra.Command, []string) {},
		Annotations: map[string]string{annSideEffect: "interactive"},
	})

	// Hidden leaf.
	root.AddCommand(&cobra.Command{
		Use:         "internal-dump",
		Short:       "dump internals",
		Hidden:      true,
		Run:         func(*cobra.Command, []string) {},
		Annotations: map[string]string{annSideEffect: "read"},
	})

	// Deprecated leaf, via cobra's own field.
	root.AddCommand(&cobra.Command{
		Use:         "old-list",
		Short:       "old listing",
		Deprecated:  "use list",
		Run:         func(*cobra.Command, []string) {},
		Annotations: map[string]string{annSideEffect: "read"},
	})

	// Deprecated leaf, via the kit annotation instead.
	root.AddCommand(&cobra.Command{
		Use:   "sunset",
		Short: "on its way out",
		Run:   func(*cobra.Command, []string) {},
		Annotations: map[string]string{
			annSideEffect:      "read",
			annDeprecatedSince: "1.4",
			annRemovalTarget:   "2.0",
			annReplacedBy:      "list",
		},
	})

	// Malformed side-effect value.
	root.AddCommand(&cobra.Command{
		Use:         "typo",
		Short:       "misannotated",
		Run:         func(*cobra.Command, []string) {},
		Annotations: map[string]string{annSideEffect: "destrutive"},
	})

	// Malformed output schema: declared but not valid JSON.
	root.AddCommand(&cobra.Command{
		Use:   "badschema",
		Short: "broken schema",
		Run:   func(*cobra.Command, []string) {},
		Annotations: map[string]string{
			annSideEffect:               "read",
			"kit/output-schema":         "{not json",
			"kit/output-schema-version": "1.0",
		},
	})

	// Management-only: the spec subcommand.
	root.AddCommand(&cobra.Command{
		Use:   "spec",
		Short: "emit manifest",
		Run:   func(*cobra.Command, []string) {},
		Annotations: map[string]string{
			annSideEffect:  "read",
			annSpecCommand: "true",
		},
	})

	// Group with a runnable child: the group itself is
	// not-runnable, the child is invocable.
	group := &cobra.Command{Use: "widget", Short: "widget commands"}
	group.AddCommand(&cobra.Command{
		Use:         "rename",
		Short:       "rename a widget",
		Run:         func(*cobra.Command, []string) {},
		Annotations: map[string]string{annSideEffect: "write-shared"},
	})
	root.AddCommand(group)

	// A depth-1 verb a tool reserves for kit management, with a
	// child that inherits the reservation.
	mgmt := &cobra.Command{Use: "mgmt", Short: "kit management"}
	mgmt.AddCommand(&cobra.Command{
		Use:         "status",
		Short:       "management status",
		Run:         func(*cobra.Command, []string) {},
		Annotations: map[string]string{annSideEffect: "read"},
	})
	root.AddCommand(mgmt)

	// The serve hierarchy: runnable supervisor with a runnable child
	// and a hidden child. Self-hosting by position, whatever the
	// reserved lookup says.
	serve := &cobra.Command{
		Use:         "serve",
		Short:       "run services",
		Run:         func(*cobra.Command, []string) {},
		Annotations: map[string]string{annSideEffect: "write-shared"},
	}
	serve.AddCommand(&cobra.Command{
		Use:         "api",
		Short:       "run the api",
		Run:         func(*cobra.Command, []string) {},
		Annotations: map[string]string{annSideEffect: "write-shared"},
	})
	serve.AddCommand(&cobra.Command{
		Use:         "internal",
		Short:       "hidden server",
		Hidden:      true,
		Run:         func(*cobra.Command, []string) {},
		Annotations: map[string]string{annSideEffect: "write-shared"},
	})
	root.AddCommand(serve)

	// Declares it listens: self-hosting by network class.
	root.AddCommand(&cobra.Command{
		Use:   "listen",
		Short: "accept connections",
		Run:   func(*cobra.Command, []string) {},
		Annotations: map[string]string{
			annSideEffect: "write-local",
			annNetwork:    "ingress",
		},
	})

	// Explicitly marked: self-hosting by annotation.
	root.AddCommand(&cobra.Command{
		Use:   "upgrade",
		Short: "replace the binary",
		Run:   func(*cobra.Command, []string) {},
		Annotations: map[string]string{
			annSideEffect:  "write-local",
			annSelfHosting: "true",
		},
	})

	// Framework built-ins.
	root.AddCommand(&cobra.Command{Use: "help", Short: "help", Run: func(*cobra.Command, []string) {}})
	completion := &cobra.Command{Use: "completion", Short: "completion"}
	completion.AddCommand(&cobra.Command{
		Use: "bash", Short: "bash completion",
		Run: func(*cobra.Command, []string) {},
	})
	root.AddCommand(completion)

	// Hidden AND deprecated: precedence check.
	root.AddCommand(&cobra.Command{
		Use:         "hidden-and-old",
		Short:       "both",
		Hidden:      true,
		Deprecated:  "gone soon",
		Run:         func(*cobra.Command, []string) {},
		Annotations: map[string]string{annSideEffect: "read"},
	})

	return root
}

// fakeReserved is a reservedLookup that reserves a fixed name set.
type fakeReserved map[string]bool

func (f fakeReserved) IsReserved(name string) bool { return f[name] }
