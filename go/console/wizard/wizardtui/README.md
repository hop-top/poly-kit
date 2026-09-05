# wizardtui

## What it answers

How a `wizard.Wizard` renders as a full-screen bubbletea program. Kept in a
subpackage so tools that only need the line or headless frontends of
`hop.top/kit/go/console/wizard` never pull bubbletea in. Step definitions,
validation, and the frontend selection order live in the parent package,
not here.

## Use it when

- a tool wants the TUI when stdin is a terminal → pass
  `wizard.WithTUI(func(ctx context.Context, w *wizard.Wizard) error { return wizardtui.RunTUI(ctx, w, theme) })`
  to `wizard.Run`; the closure adapts `RunTUI`'s extra `cli.Theme` argument
  to the `wizard.TUIFrontend` signature
- you hold a `TUIRunner`-shaped dependency → `wizardtui.NewFrontend().Run(ctx, w, theme)`
- you need to drive the program yourself → `wizardtui.RunTUI(ctx, w, theme)`
  builds and runs the `tea.Program` and returns the wizard's error
- tests or CI → do not use this package; `wizard.RunHeadless` or
  `wizard.WithAnswers` cover the same steps without a terminal

No quick start: `RunTUI` needs a terminal.

## Contract

- Keys: `enter` submit, `esc` back, `ctrl+c` abort (returns
  `*wizard.AbortError`), `up`/`k` and `down`/`j` move, `space` toggles a
  multi-select item, `y`/`n` set a confirm, `backspace` on empty text goes back.
- Text input submitted empty falls back to `Step.DefaultValue`.
- A `*wizard.ValidationError` from `Advance` stays on the step and is shown
  under the body; any other error quits the program and is returned.
- An `ActionRequest` runs off the UI thread with a spinner; keys are ignored
  until it resolves.
- Summary steps use `Step.FormatFn` when set, otherwise sorted keys with
  `__`-prefixed keys hidden.
- `RunTUI` does not call `w.Complete()`; `wizard.Run` does after the frontend
  returns.

## Neighbours

- `hop.top/kit/go/console/wizard`: `Wizard`, `Step`, `Run`, `RunLine`,
  `RunHeadless`, `WithTUI`, `ForceTUI`
- `hop.top/kit/go/console/cli`: `Theme` the view colors come from
- `hop.top/kit/go/console/tui`: `NewSpinner` and the shared components

## See also

- [Wizard API reference](../../../../docs/adopters/reference/wizard-api.md)
- [Go primitives reference](../../../../docs/adopters/reference/go-primitives.md)
