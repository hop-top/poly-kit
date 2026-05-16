# tui

Terminal UI components and interactive elements for kit-based CLIs.

Two layers ship in this package:

1. **Components** — pre-themed widgets (anim, badge, confirm, list,
   pills, progress, status, dialog) and themed wrappers for
   `bubbles/v2` types (table, textinput, list).
2. **AppShell** — a top-level `tea.Model` that owns `tea.NewProgram`
   and frames the screen as header / main / footer.

## AppShell

AppShell hoists the boilerplate every adopter (aps, tlc, dpkms, …)
hand-rolls: a `tea.Model`, a `tea.NewProgram(...)` call, the
WindowSize handling, q/esc-to-quit keymap, and a header/main/footer
composition.

Apps stop owning a `tea.Model`. Instead they implement `AppRenderer`:

```go
type AppRenderer interface {
    Render(width, height int) string
}
```

…plus any of the optional capabilities they need:

| Interface         | Purpose                                |
|-------------------|----------------------------------------|
| `Initer`          | `Init() tea.Cmd` — initial commands    |
| `Updater`         | `Update(msg) (AppRenderer, tea.Cmd)`   |
| `Resizer`         | `Resize(w, h) AppRenderer` — viewport  |
| `HeaderRenderer`  | `Header(width) string` — dynamic header|
| `FooterRenderer`  | `Footer(width) string` — dynamic footer|

Wire it up:

```go
shell := tui.NewAppShellFromRoot(myApp{}, root,
    tui.WithHeader("my tool"),
    tui.WithHelpText("  q quit  ?/h help  /j navigate"),
)
if _, err := shell.Run(ctx); err != nil { ... }
```

The shell owns:

- `tea.NewProgram(model, tea.WithContext(ctx))`
- `tea.WindowSizeMsg` → updates `styles.Common`, calls `Resize` if
  the renderer implements it
- canonical keymap (overridable via `WithKeyMap`):
  - `q` / `esc` / `ctrl+c` → quit
  - `?` / `h` → toggle help footer
- header / main / footer composition padded to terminal size

See [`example_appshell_test.go`](example_appshell_test.go) for a
runnable example.
