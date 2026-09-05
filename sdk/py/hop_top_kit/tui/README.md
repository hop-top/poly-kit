# tui

## What it answers

How a Python CLI paints the same terminal widgets as a Go kit CLI: status lines, badges, pills, spinner, progress bar, gradient animation, confirm and select prompts, rendered with Rich and themed from `hop_top_kit.cli.Theme`. Wrong module for structured output (`hop_top_kit.output`) and for machine-readable progress events (`hop_top_kit.progress`).

## Use it when

- prefixed status line: `status(theme, text, kind)` with `kind` in `info`, `success`, `error`, `warn`
- inline label: `badge(theme, text)`, `pills(theme, items)`
- long-running step in a TTY: `with spinner(theme, message)`, `with progress(theme, total)`
- yes/no or pick-one prompt: `confirm(theme, message)`, `select(theme, items)`

## Quick start

```python
from hop_top_kit.cli import NEON, Theme
from hop_top_kit.tui import badge, status

theme = Theme(
    palette=NEON, accent="#7ED957", secondary="#FF00FF",
    muted="#858183", error="#ED4A5E", success="#52CF84",
)
print(status(theme, "Deployed", "success"))
print(badge(theme, "beta"))
```

## Contract

- Status symbols, spinner frames and interval, and animation runes come from [`contracts/parity/parity.json`](../../../../contracts/parity/parity.json) through [`../parity.py`](../parity.py); never inline them.
- The published wheel ships a vendored `parity.json` next to `parity.py`; `tests/test_parity.py` is the block-registry guard.
- Every renderer takes `Theme` as its first argument; there is no module-level default theme.
- Parity: [`contracts/parity/README.md`](../../../../contracts/parity/README.md), table "Declared blocks", rows `status`, `spinner`, `anim`.

## Neighbours

- `hop_top_kit.cli`: `Theme`, palettes, help layout
- `hop_top_kit.output`: tables and structured formats
- `hop_top_kit.progress`: JSON progress events for non-TTY consumers
- Go reference: [`go/console/tui`](../../../../go/console/tui/README.md); TypeScript port: [`sdk/ts/src/tui`](../../../ts/src/tui/README.md)

## See also

- [`docs/adopters/guides/tui-component-gallery.md`](../../../../docs/adopters/guides/tui-component-gallery.md)
- [`docs/adopters/guides/cli-parity-guide.md`](../../../../docs/adopters/guides/cli-parity-guide.md)
