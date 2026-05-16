# TUI Component Gallery

Cross-language component catalog for `hop.top/kit/go/console/tui`.

All components follow bubbletea's value-receiver/copy-on-write
pattern. Mutating methods return copies; originals are untouched.

Styling is driven by `cli.Theme` — pass it at construction time;
components never hardcode colors.

## Language Support

| Component | Go | TS | Python |
|-----------|:--:|:--:|:------:|
| Spinner   | x  |    |        |
| Progress  | x  |    |        |
| Badge     | x  |    |        |
| Pills     | x  |    |        |
| Status    | x  |    |        |
| Anim      | x  |    |        |
| Confirm   | x  |    |        |
| List      | x  |    |        |
| Dialog    | x  |    |        |

TS and Python TUI packages are not yet implemented.

---

## Spinner

Animated loading indicator wrapping `bubbles/v2/spinner`.

**Package:** `hop.top/kit/go/console/tui`

```go
spin := tui.NewSpinner(theme)
// Use spin.Tick as a tea.Cmd for animation frames.
// Pre-styled with theme.Accent; uses spinner.Dot pattern.
```

**Key details:**
- Returns `spinner.Model` directly (not a wrapper)
- Satisfies `Animatable` interface via `Tick()`

---

## Progress

Themed progress bar with percentage. Self-contained
implementation — `bubbles/v2` does not ship one yet.

**Package:** `hop.top/kit/go/console/tui`

```go
bar := tui.NewProgress(theme)
bar = bar.SetPercent(0.65)
bar = bar.SetWidth(40)
fmt.Println(bar.View()) // ██████████████████████████░░░░░░░░░░░░░░
```

**Key options:**
- `SetPercent(float64)` — clamp to [0, 1]
- `SetWidth(int)` — character width (default 40)
- `ViewWithColor(fill, track)` — one-off color override
- Handles `ProgressMsg` in `Update()`
- Gradient: `theme.Accent` (filled) to `theme.Muted` (track)

---

## Badge

Styled inline "update available" badge. Runs a background
`upgrade.Checker`, shows spinner while loading, renders badge
when an update is available.

**Package:** `hop.top/kit/go/console/tui`

```go
badge := tui.NewBadge(checker, theme)
// badge.Init() starts background check + spinner tick.
// badge.View() returns "" while loading or when no update.
// When update available:  "^ UPDATE  1.0.0 -> 1.1.0"
```

**Key options:**
- `Loading() bool` — check still in progress
- `Result() *upgrade.Result` — nil while loading
- Sends `CheckDoneMsg` when background check completes
- Badge styled with `theme.Accent`; notice with
  `theme.Success`

---

## Pills

Horizontal pill row with focus tracking and expand/collapse.

**Package:** `hop.top/kit/go/console/tui`

```go
pills := tui.NewPillBar(
    tui.NewPill("env", "prod"),
    tui.NewPill("region", "us-east-1"),
    tui.NewPill("nodes", "3"),
)
// pills.ViewWithTheme(theme, 80)
// Renders: [env prod] [region us-east-1] [nodes 3] ctrl+t open
```

**Key options:**
- `FocusNext()` / `FocusPrev()` — cycle focus (wraps)
- `Toggle()` — expand/collapse detail pane
- `SetExpanded(content)` — content shown below pills
- `Height()` — 1 compact, 1+lines when expanded
- Focused pill gets rounded border; blurred gets hidden
  border
- Keyboard: ctrl+t toggle, left/right when expanded

---

## Status

Status bar combining `bubbles/v2/help` keybinding display
with ephemeral info/warn/error/success messages.

**Package:** `hop.top/kit/go/console/tui`

```go
km := myKeyMap{} // implements help.KeyMap
status := tui.NewStatus(theme, km)
// Show ephemeral message:
status = status.SetInfoMsg(tui.InfoMsg{
    Type: tui.InfoTypeSuccess,
    Msg:  "Deployed v2.1",
})
// Auto-clear after TTL:
cmd := tui.ClearInfoAfter(tui.DefaultStatusTTL) // 5s
```

**Info types and indicators:**
- `InfoTypeInfo` — `i` accent
- `InfoTypeError` — `●` red
- `InfoTypeWarn` — `▲` secondary
- `InfoTypeSuccess` — `✓` green

**Key options:**
- `ToggleHelp()` — compact / expanded help views
- `SetWidth(int)` — update help model width
- `ClearInfoMsg()` — manually clear ephemeral message
- `DefaultStatusTTL` = 5 seconds

---

## Anim

Gradient-cycling character scramble animation. Characters
appear with staggered birth offsets, then cycle through
random `0-9a-fA-F~!@#$%^&*()+=_` glyphs with HCL gradient
coloring.

**Package:** `hop.top/kit/go/console/tui`

```go
anim := tui.NewAnim(tui.AnimSettings{
    Width:       10,
    Label:       " deploying",
    LabelColor:  theme.Accent,
    GradColorA:  lipgloss.Color("#7ED957"),
    GradColorB:  lipgloss.Color("#FF00FF"),
    CycleColors: true,
})
cmd := anim.Start()
// In Update: anim, cmd = anim.Animate(msg.(tui.AnimStepMsg))
```

**Key options:**
- `Width` — number of cycling characters (default 10)
- `CycleColors` — shift gradient each frame
- `SetLabel(string)` — update label text
- `MakeGradient(size, colorA, colorB)` — HCL blend utility
- Runs at 20 FPS (`animInterval`)
- Each instance has unique ID; `AnimStepMsg.ID` routes
  correctly in multi-anim scenarios
- Satisfies `Animatable` interface

---

## Confirm

Yes/no confirmation prompt. Full `tea.Model` implementation.

**Package:** `hop.top/kit/go/console/tui`

```go
c := tui.NewConfirm("Delete all pods?", theme)
// Run with tea.NewProgram(c)
// Keys: y/Y/enter → accept; n/N/esc/q → reject
// c.Accepted(), c.Done()
```

**Key details:**
- Sends `tea.Quit` on choice
- Prompt styled with `theme.Accent` bold
- Yes in `theme.Success`, No in `theme.Error`
- Hint `[y/n]` in `theme.Muted`

---

## List

Generic scrollable list of `Renderer` items. Sub-component
(returns `string`, not `tea.View`).

**Package:** `hop.top/kit/go/console/tui`

```go
list := tui.NewList(10) // 10 visible lines
list = list.SetItems(items)
list = list.SetFollow(true) // auto-scroll on append
fmt.Println(list.View(80))
```

**Key options:**
- `SetItems([]Item)` — replace items; auto-scrolls if
  follow mode + at bottom
- `SetFollow(bool)` — enable auto-scroll
- `ScrollBy(n)` / `ScrollToEnd()` — manual scroll
- `SetHeight(int)` — change visible height
- Mouse wheel support via `Update()`
- Items must implement `Renderer` (single method:
  `Render(width int) string`)

---

## Dialog (Overlay)

Modal dialog stack for layering dialogs over base TUI
content. Go only.

**Package:** `hop.top/kit/go/console/tui/dialog`

```go
ov := dialog.NewOverlay()
ov = ov.Push(myDialog)        // push onto stack
active := ov.Active()         // topmost dialog
rendered := ov.View(base, 80, 24) // centered overlay

// In Update loop:
ov, cmd = ov.Update(msg)
// Done() dialogs auto-pop from the stack.
```

**Dialog interface:**

```go
type Dialog interface {
    Update(msg tea.Msg) (Dialog, tea.Cmd)
    View(width, height int) string
    Done() bool
}
```

**Key details:**
- Stack-based — multiple dialogs can layer
- `Push()` / `Pop()` — manage stack
- `IsActive()` — check if any dialog is open
- `centerOverlay()` — centers dialog content over base
  content within width x height grid
- All methods return copies (immutable)

---

## Shared Interfaces

Defined in `hop.top/kit/go/console/tui` (`tui.go`):

| Interface   | Method                    | Purpose           |
|-------------|---------------------------|--------------------|
| Renderer    | `Render(width int) string`| Width-aware render |
| Focusable   | `Focus/Blur/Focused`     | Keyboard focus     |
| Animatable  | `Tick() tea.Cmd`          | Frame scheduling   |

## Themed Wrappers

`hop.top/kit/go/console/tui` also provides theme-aware style factories
for `bubbles/v2` built-in components:

- `TableStyles(theme)` — returns `table.Styles`
- `TextInputStyles(theme)` — returns `textinput.Styles`
- `ListStyles(theme)` — returns `list.Styles`

## Styles Engine

**Package:** `hop.top/kit/go/console/tui/styles`

`styles.NewStyles(theme)` builds semantic lipgloss styles
from `cli.Theme`. `styles.Common` threads styles + terminal
dimensions to all sub-models:

```go
common := styles.NewCommon(theme, 80, 24)
h := common.ContentHeight() // Height - header - footer
```

Layout regions: `Header`, `Sidebar`, `Main`, `Footer`.
