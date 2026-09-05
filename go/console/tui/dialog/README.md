# dialog

## What it answers

How to layer modal dialogs over base TUI content: a `Dialog` interface and an
immutable `Overlay` stack that routes messages to the top dialog and pops it
when done. Concrete widgets (confirm, list, pills) live in
`hop.top/kit/go/console/tui`; this package holds only the stack and centering.

## Use it when

- opening a modal → `ov = ov.Push(d)`
- forwarding a Bubble Tea message → `ov, cmd = ov.Update(msg)`
- composing the frame → `ov.View(base, width, height)`
- checking whether input should go to the app or the dialog → `ov.IsActive()`

## Quick start

`banner` is any type implementing `Dialog`; here it completes on its first
message.

```go
base := "..........\n..........\n..........\n..........\n.........."

ov := dialog.NewOverlay().Push(banner{text: "[ok]"})
fmt.Println(ov.View(base, 10, 5))

ov, _ = ov.Update(tea.KeyPressMsg{})
fmt.Println(ov.IsActive())
// ..........
// ..........
// ...[ok]...
// ..........
// ..........
// false
```

## Contract

- `Dialog`: `Update(tea.Msg) (Dialog, tea.Cmd)`, `View(width, height int) string`,
  `Done() bool`
- Value receivers: `Push`, `Pop`, `Update` return copies; the original
  `Overlay` is unchanged
- `Update` routes only to the topmost dialog and pops it when `Done()`
  reports true after the update
- `View` returns `base` unchanged when the stack is empty; otherwise the base
  is padded or truncated to exactly `width` x `height` runes and the dialog is
  centered over it, clipped at the edges
- `Pop` and `Update` on an empty stack are no-ops; `Active()` returns nil

## Neighbours

- `hop.top/kit/go/console/tui`: AppShell and the widgets that go on the stack
- `hop.top/kit/go/console/tui/styles`: styles and dimensions for dialog views

## See also

- [TUI component gallery](../../../../docs/adopters/guides/tui-component-gallery.md)
- [Go primitives reference](../../../../docs/adopters/reference/go-primitives.md)
