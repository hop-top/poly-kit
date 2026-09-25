package cli

// usageTemplate replaces cobra's default usage template on the root (and,
// by inheritance, every subcommand). It only matters to adopters that run
// r.Cmd.Execute() instead of r.Execute(): fang renders help on the latter
// path and never reads it.
//
// It is cobra v1.10's default template with one rule added for the
// command sections: a hidden command is never listed, and a section is
// rendered only when it lists at least one command. Cobra prints every
// registered group's title, so kit's built-in MANAGEMENT group (hidden by
// default) and any group with no visible commands left a bare header.
//
// Cobra lists "help" even when it is not an available command, for its
// own default help command; kit's help subcommand is Hidden by design and
// stays unlisted like any other hidden command.
const usageTemplate = `Usage:{{if .Runnable}}
  {{.UseLine}}{{end}}{{if .HasAvailableSubCommands}}
  {{.CommandPath}} [command]{{end}}{{if gt (len .Aliases) 0}}

Aliases:
  {{.NameAndAliases}}{{end}}{{if .HasExample}}

Examples:
{{.Example}}{{end}}{{if .HasAvailableSubCommands}}{{$cmds := .Commands}}{{if eq (len .Groups) 0}}

Available Commands:{{range $cmds}}{{if (or .IsAvailableCommand (and (eq .Name "help") (not .Hidden)))}}
  {{rpad .Name .NamePadding }} {{.Short}}{{end}}{{end}}{{else}}{{range $group := .Groups}}{{$listed := false}}{{range $cmds}}{{if (and (eq .GroupID $group.ID) (or .IsAvailableCommand (and (eq .Name "help") (not .Hidden))))}}{{$listed = true}}{{end}}{{end}}{{if $listed}}

{{$group.Title}}{{range $cmds}}{{if (and (eq .GroupID $group.ID) (or .IsAvailableCommand (and (eq .Name "help") (not .Hidden))))}}
  {{rpad .Name .NamePadding }} {{.Short}}{{end}}{{end}}{{end}}{{end}}{{$listed := false}}{{range $cmds}}{{if (and (eq .GroupID "") (or .IsAvailableCommand (and (eq .Name "help") (not .Hidden))))}}{{$listed = true}}{{end}}{{end}}{{if $listed}}

Additional Commands:{{range $cmds}}{{if (and (eq .GroupID "") (or .IsAvailableCommand (and (eq .Name "help") (not .Hidden))))}}
  {{rpad .Name .NamePadding }} {{.Short}}{{end}}{{end}}{{end}}{{end}}{{end}}{{if .HasAvailableLocalFlags}}

Flags:
{{.LocalFlags.FlagUsages | trimTrailingWhitespaces}}{{end}}{{if .HasAvailableInheritedFlags}}

Global Flags:
{{.InheritedFlags.FlagUsages | trimTrailingWhitespaces}}{{end}}{{if .HasHelpSubCommands}}

Additional help topics:{{range .Commands}}{{if .IsAdditionalHelpTopicCommand}}
  {{rpad .CommandPath .CommandPathPadding}} {{.Short}}{{end}}{{end}}{{end}}{{if .HasAvailableSubCommands}}

Use "{{.CommandPath}} [command] --help" for more information about a command.{{end}}
`
