# Render markdown in the terminal

> Audience: Go adopters whose command prints markdown (help topics, release notes, a README
> viewer) and wants styled terminal output that honours `--no-color`. Package:
> `hop.top/kit/go/console/markdown`, a thin wrapper over glamour.

The package is two stateless functions. There is no renderer struct to build or keep.

| Function | Style | Use when |
|---|---|---|
| `markdown.Render(src, noColor)` | kit's dark style with white headings and links | default |
| `markdown.RenderWith(src, style, noColor)` | an `ansi.StyleConfig` you supply | your tool has its own palette |

Both return `(string, error)`. Neither writes anything; print the result yourself.

## 1. Render with the default style

```go
package main

import (
    "context"
    "fmt"
    "os"

    "github.com/spf13/cobra"

    "hop.top/kit/go/console/cli"
    "hop.top/kit/go/console/markdown"
)

const notes = "# Release notes\n\nThis is **bold** and a [link](https://example.com).\n"

func main() {
    root := cli.New(cli.Config{Name: "demo", Version: "0.0.1", Short: "demo"})
    root.Cmd.AddCommand(&cobra.Command{
        Use:   "notes",
        Short: "Print release notes",
        RunE: func(cmd *cobra.Command, _ []string) error {
            out, err := markdown.Render(notes, root.Viper.GetBool("no-color"))
            if err != nil {
                return err
            }
            fmt.Fprint(cmd.OutOrStdout(), out)
            return nil
        },
    })
    if err := root.Execute(context.Background()); err != nil {
        os.Exit(1)
    }
}
```

A shipping tool must also pass `Root.Validate` (a `Long`, a `kit/side-effect` tag, and so on); see
[getting-started-cli](getting-started-cli.md). The example keeps only the markdown call.

`root.Viper.GetBool("no-color")` reads the global `--no-color` flag that `cli.New` registers
(unless `Config.Disable.NoColor` is set). This is the wiring the package's integration test
uses.

## 2. Know what `noColor` does

`noColor` is the only switch. Passing `true` selects glamour's plain "NoTTY" style: no ANSI
escapes at all, but still laid out (indented, bullets as `•`, links as text plus URL). Inline
markers such as `**bold**` stay literal in that mode.

`Render` does not look at the terminal. With `noColor == false` it emits escapes even when
stdout is a pipe or a file. If your command can be piped, decide before calling:

```go
noColor := root.Viper.GetBool("no-color") || !isTerminal(cmd.OutOrStdout())
```

`isTerminal` is yours to write, for example over `github.com/mattn/go-isatty`.

## 3. Use your own palette

`RenderWith` takes a glamour `ansi.StyleConfig`. Start from a built-in and change what you need:

```go
import (
    "charm.land/glamour/v2/ansi"
    "charm.land/glamour/v2/styles"
)

func brand() ansi.StyleConfig {
    s := styles.DarkStyleConfig
    c := "#FF5F87"
    s.H1.Color = &c
    s.Link.Color = &c
    return s
}

out, err := markdown.RenderWith(notes, brand(), noColor)
```

When `noColor` is `true`, `RenderWith` ignores your style and uses the NoTTY style, the same as
`Render`. You do not need a second code path for plain output.

## Pitfalls

- Do not render markdown into the data envelope. Structured results belong to
  [`output`](../reference/output.md) (`--format json|yaml`); markdown is for human text. If a
  caller asked for `--format json`, skip `Render`.
- Line wrapping follows glamour defaults. The package exposes no width option.
- `Render` builds a new glamour renderer on every call; the package caches nothing.

## See also

- Package source: `go/console/markdown/markdown.go` (README there is a one-liner).
- [Log API](../reference/log-api.md): the other `--no-color`-aware primitive.
