# styles

## What it answers

Where TUI components get their lipgloss styles and layout dimensions:
`Styles` derived from a `cli.Theme`, threaded with terminal size through
`Common`. Theme colors and palettes are defined in
`hop.top/kit/go/console/cli`; widgets themselves are in
`hop.top/kit/go/console/tui`.

## Use it when

- building the shared context once per program → `styles.NewCommon(theme, w, h)`
- sizing the main region → `common.ContentHeight()`
- styling a component → `common.Styles.Accent`, `.Header`, `.Status.ErrorIndicator`
- only styles, no dimensions → `styles.NewStyles(theme)`

## Quick start

```go
theme := cli.Theme{
    Palette: cli.Neon,
    Accent:  lipgloss.Color("#7ED957"),
    Muted:   lipgloss.Color("#6B7280"),
}

common := styles.NewCommon(theme, 80, 24)
fmt.Println("content rows:", common.ContentHeight())
fmt.Printf("%q\n", common.Styles.Main.Render("body"))
// content rows: 22
// "        \n  body  \n        "
```

## Contract

- `HeaderHeight` and `FooterHeight` are 1 row each; `ContentHeight` is
  `Height - 2`, clamped at 0
- `Common` is passed by value; sub-models render relative to `Width` and `Height`
- Semantic styles: `Accent`, `Secondary`, `Muted`, `Error`, `Success`,
  `Title`, `Subtle`, `Bold`. Layout: `Header`, `Sidebar`, `Main`, `Footer`.
  Groups: `Status` (indicator and message per level), `Pills`
- Status indicators carry their symbol via `SetString`, taken from
  `parity.Values.Status.Symbols`, so Go and other kit runtimes print the
  same glyphs
- Colored styles emit ANSI sequences on `Render` regardless of terminal;
  color-free styles such as `Main` are the deterministic ones to assert on

## Neighbours

- `hop.top/kit/go/console/cli`: `Theme`, `Palette`, `Neon`, `Dark`, `Bauhaus`
- `hop.top/kit/go/console/tui`: AppShell wires `Common` on `WindowSizeMsg`
- `hop.top/kit/contracts/parity`: status symbols shared across languages

## See also

- [TUI component gallery](../../../../docs/adopters/guides/tui-component-gallery.md)
- [Migrate to AppShell](../../../../docs/adopters/guides/migrate-to-appshell.md)
- [Status symbols contract](../../../../contracts/parity/README.md)
