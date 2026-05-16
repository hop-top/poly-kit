// Package stage provides the shared `stage` Cobra subcommand tree
// adopters mount once into their root CLI:
//
//	rootCmd.AddCommand(stagecmd.New(stagecmd.Config{
//	    ProjectResolver: func() string { return cfg.Project.ID },
//	    Bus:             bus.Default(),
//	}))
//
// Subcommands:
//
//	<tool> stage show [scope]    — print current State
//	<tool> stage set  <mode>     — propose + persist
//	<tool> stage why  [scope]    — list active rules + recent violations
//	<tool> stage list            — every scope in projects.yaml
//
// Output honors kit/output --format (table | json | yaml | csv).
//
// The factory follows the same pattern as toolspec/cli.RegisterSpecCommand
// — adopters wire once at boot and every kit-based CLI gets the
// uniform stage operating-mode surface for free.
package stage
