# Migrate to AppShell

You're here because you have a hand-rolled bubbletea TUI in your tool
(aps, tlc, dpkms, …) and want to switch to `kit/console/tui.AppShell`.
This page walks the diff.

## Why migrate

Every kit-adjacent TUI implements the same five things:

1. A `tea.Model` with a header/main/footer view.
2. A `tea.NewProgram(model).Run()` entry point.
3. A `tea.WindowSizeMsg` branch that mutates a width/height field.
4. A `q`/`esc`/`ctrl+c` keypress branch that returns `tea.Quit`.
5. A footer help line that reminds users which keys do what.

`AppShell` ships all five. Apps supply only the part that's actually
unique: the main-region content and any extra keys.

## Before you start

- kit version: `AppShell` lives in `hop.top/kit/go/console/tui` —
  available since the kit-tui-appshell track shipped.
- Bubbletea: AppShell uses bubbletea v2 (`charm.land/bubbletea/v2`).
  If you're on v1, upgrade first.
- Theme: AppShell expects a `cli.Theme`. If your CLI already uses
  `kit/cli` you can pass `root.Theme` directly via
  `NewAppShellFromRoot`.

## The mechanical migration

### Step 1 — Replace the model with an AppRenderer

Your old `tea.Model`:

```go
type Model struct {
    width, height int
    items         []item
    cursor        int
    err           error
}

func (m Model) Init() tea.Cmd { return nil }

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
    switch msg := msg.(type) {
    case tea.WindowSizeMsg:
        m.width, m.height = msg.Width, msg.Height
        return m, nil
    case tea.KeyPressMsg:
        switch msg.String() {
        case "q", "esc", "ctrl+c":
            return m, tea.Quit
        case "j":
            if m.cursor < len(m.items)-1 { m.cursor++ }
        case "k":
            if m.cursor > 0 { m.cursor-- }
        }
    }
    return m, nil
}

func (m Model) View() tea.View {
    header := titleStyle.Render("my tool")
    footer := helpStyle.Render("j/k navigate  q quit")
    main := renderItems(m.items, m.cursor)
    v := tea.NewView(header + "\n" + main + "\n" + footer)
    v.AltScreen = true
    return v
}
```

becomes an `AppRenderer`:

```go
type App struct {
    items  []item
    cursor int
    width  int
    height int
}

// Required.
func (a App) Render(width, height int) string {
    return renderItems(a.items, a.cursor)
}

// Optional. Drop the WindowSize branch.
func (a App) Resize(w, h int) tui.AppRenderer {
    a.width, a.height = w, h
    return a
}

// Optional. Drop the q/esc/ctrl+c branch — AppShell handles it.
func (a App) Update(msg tea.Msg) (tui.AppRenderer, tea.Cmd) {
    if key, ok := msg.(tea.KeyPressMsg); ok {
        switch key.String() {
        case "j":
            if a.cursor < len(a.items)-1 { a.cursor++ }
        case "k":
            if a.cursor > 0 { a.cursor-- }
        }
    }
    return a, nil
}

// Optional. Replace the header/footer literal strings.
func (a App) Header(width int) string { return "my tool" }
func (a App) Footer(width int) string { return "j/k navigate  q quit  ? help" }
```

### Step 2 — Replace `tea.NewProgram` with AppShell

Old entry point:

```go
func Run(ctx context.Context) error {
    p := tea.NewProgram(initialModel(), tea.WithAltScreen())
    _, err := p.Run()
    return err
}
```

New entry point:

```go
func Run(ctx context.Context, root *cli.Root) error {
    shell := tui.NewAppShellFromRoot(initialApp(), root)
    _, err := shell.Run(ctx)
    return err
}
```

If your tool doesn't have a `*cli.Root`, build the theme yourself
(or pass any `cli.Theme` value):

```go
shell := tui.NewAppShell(initialApp(), myTheme)
```

### Step 3 — Drop the styling boilerplate

Your old `titleStyle`, `helpStyle`, `boxStyle` package vars often
rebuild what `styles.Common` already gives you. After migration you
can usually delete them and pull from `shell.Common().Styles`.

## Concrete diffs

### aps — `internal/tui/`

The aps TUI is a five-state navigator (profile list → profile detail →
capability list → action list → execution). Migration in three patches:

1. Move `Model` → `App` and rename the methods:
   - `Update(tea.Msg) (tea.Model, tea.Cmd)` → `Update(tea.Msg) (tui.AppRenderer, tea.Cmd)`
   - `View() tea.View` → split: top-level shell wraps the screens via
     `Render(w, h int) string`.
2. Drop the `tea.WindowSizeMsg` branch in `update.go`. Replace the
   `m.width`/`m.height` writes with a `Resize` method on `App`.
3. Replace `Run()`'s `tea.NewProgram(InitialModel())` with
   `tui.NewAppShellFromRoot(initialApp(), root).Run(ctx)`.

The five `update*` helpers stay; they just consume an `App` value
instead of a `Model`.

### tlc — `internal/tui/`

The tlc TUI is more complex: it tracks a viewport, multiple list
modes, and a glamour markdown renderer. Migration order:

1. Move the model. The internal `view` discriminator and viewport
   stay on `App`; the dimension fields become resync targets in
   `Resize`.
2. Replace the per-view `headerView` / `helpView` calls with
   `Header(width)` and `Footer(width)` on `App`. Both take width so
   they can pre-wrap pills.
3. Replace `m.viewString()` with `Render(width, height)`. The
   `effectiveViewportHeight` calc moves into `Resize` (height arg
   is already the post-frame content height).
4. Drop the q/esc handling from each `handle*Update` — let the
   shell's keymap handle it. Keep the per-view shortcuts (`/`,
   `f`, `a`, etc.).

### dpkms

dpkms's TUI follows the aps shape; same three steps as aps.

## Common pitfalls

### State ownership

`AppShell` stores the `AppRenderer` by value and replaces it after
every `Update`. If your renderer holds pointers to long-lived
resources (DB handle, channel), put them in a wrapper struct that's
captured by closure or store them as exported fields the renderer
reads from.

### Message routing

The shell handles `tea.WindowSizeMsg` and the canonical keys *before*
forwarding to your `Updater`. If you want to react to a key the shell
already owns (for example to clear an internal state on `esc`), use
`WithKeyMap` to reclaim that key:

```go
km := tui.DefaultKeyMap()
km.Quit = []string{"q", "ctrl+c"} // remove "esc"
shell := tui.NewAppShell(app, theme, tui.WithKeyMap(km))
```

Then handle `esc` in your `Update` as you used to.

### Help footer vs. app footer

When `?`/`h` is pressed, the shell flips to the help footer
(`WithHelpText(...)`). Your `Footer(width)` is suppressed during
help mode but resumes when help is toggled off. Don't try to render
help yourself in `Footer`.

### Init delegation

If your renderer implements `Initer`, the shell will call
`renderer.Init()` and forward the returned `tea.Cmd`. If you also
need an extra command at startup (for example, `tea.RequestWindowSize`
on systems that need it), batch it inside your `Init`:

```go
func (a App) Init() tea.Cmd {
    return tea.Batch(tea.RequestWindowSize, a.fetchInitial)
}
```

The shell does **not** add `tea.RequestWindowSize` automatically;
some terminals send the initial size unprompted.

### Alt-screen for inline TUIs

By default `AppShell` runs in alt-screen mode (the canonical TUI
look). For tools that should render inline below the prompt (for
example, a one-shot status bar), opt out:

```go
tui.NewAppShell(app, theme, tui.WithAltScreen(false))
```

## Test it

The `AppShell` value is itself a `tea.Model`, so unit tests can
drive it with table-driven `Update` calls — no real terminal:

```go
func TestApp_NavigateDown(t *testing.T) {
    shell := tui.NewAppShell(myApp{items: items}, theme,
        tui.WithSize(80, 24))
    updated, _ := shell.Update(tea.KeyPressMsg{Code: 'j', Text: "j"})
    got := updated.(tui.AppShell).Renderer().(myApp)
    assert.Equal(t, 1, got.cursor)
}
```

See `kit/go/console/tui/appshell_test.go` and
`kit/go/console/tui/example_appshell_test.go` for more patterns.

## Reference

- [`kit/go/console/tui/appshell.go`](../../../go/console/tui/appshell.go) — implementation.
- [`kit/go/console/tui/README.md`](../../../go/console/tui/README.md) — quick reference.
- [`docs/tui-component-gallery.md`](tui-component-gallery.md) — the components AppShell consumers compose.
