# Spaced Showcase

Demonstrates kit/cli features using `spaced`, the
reference CLI tool.

## Default Help

```
$ spaced --help
A CLI for managing spaces

Usage:
  spaced [command] [flags]

COMMANDS:
  create      Create a new space
  list        List all spaces
  delete      Remove a space

Flags:
  -h, --help       Display help
  -v, --version    Print version and exit
      --format     Output format (table|json|yaml)
      --quiet      Suppress non-essential output
      --no-color   Disable ANSI colour
      --help-all   Show all command groups
```

Note: `config` and `toolspec` are not shown. They belong
to the MANAGEMENT group which is hidden by default.

## --help-all

```
$ spaced --help-all
A CLI for managing spaces

Usage:
  spaced [command] [flags]

COMMANDS:
  create      Create a new space
  list        List all spaces
  delete      Remove a space

MANAGEMENT COMMANDS:
  config      Manage spaced configuration
  toolspec    View tool specification

Flags:
  -h, --help       Display help
  -v, --version    Print version and exit
      --format     Output format (table|json|yaml)
      --quiet      Suppress non-essential output
      --no-color   Disable ANSI colour
      --help-all   Show all command groups
```

## Version

```
$ spaced -v
spaced 1.0.0
```

## Output Format

```
$ spaced list --format json
[{"name":"dev","created":"2026-01-15"}]
```

## Group Assignment

In the spaced source, commands are assigned to groups:

```go
// Primary commands — visible by default
createCmd.GroupID = "commands"
listCmd.GroupID = "commands"
deleteCmd.GroupID = "commands"

// Management commands — hidden, shown with --help-all
configCmd.GroupID = "management"
toolspecCmd.GroupID = "management"
```
