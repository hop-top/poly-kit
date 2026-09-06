# tui

## What it answers

How a TS CLI paints the same terminal widgets as a Go kit CLI: status lines, badges, pills, spinner, progress bar, gradient animation, confirm and list prompts, all themed from `@hop-top/kit/cli`'s `Theme`. Wrong module for structured output (`@hop-top/kit/output`) and for machine-readable progress events (`@hop-top/kit/progress`).

## Use it when

- prefixed status line: `status(theme, text, 'info' | 'success' | 'error' | 'warn')`
- inline label: `badge(theme, text)`, `pills(theme, items)`
- long-running step in a TTY: `spinner(theme)`, `progress(theme)`
- yes/no or pick-one prompt: `confirm(theme, ...)`, `list(theme, ...)`

## Quick start

```ts
import { buildTheme } from '@hop-top/kit/cli';
import { status, badge } from '@hop-top/kit/tui';

const theme = buildTheme();
console.log(status(theme, 'Deployed', 'success'));
console.log(badge(theme, 'beta'));
```

## Contract

- Status symbols, spinner frames and interval, and animation runes come from [`contracts/parity/parity.json`](../../../../contracts/parity/parity.json) through [`parity.ts`](parity.ts); never inline them.
- [`parity.test.ts`](parity.test.ts) is the block-registry guard: a block declared in the contract without a loader here fails.
- Every renderer takes `Theme` as its first argument; there is no module-level default theme.
- Parity: [`contracts/parity/README.md`](../../../../contracts/parity/README.md), table "Declared blocks", rows `status`, `spinner`, `anim`.

## Neighbours

- `@hop-top/kit/cli`: `Theme`, `buildTheme`, help layout
- `@hop-top/kit/output`: tables and structured formats
- `@hop-top/kit/progress`: JSON progress events for non-TTY consumers
- Go reference: [`go/console/tui`](../../../../go/console/tui/README.md); Python port: [`hop_top_kit/tui`](../../../py/hop_top_kit/tui/README.md)

## See also

- [`docs/adopters/guides/tui-component-gallery.md`](../../../../docs/adopters/guides/tui-component-gallery.md)
- [`docs/adopters/guides/cli-parity-guide.md`](../../../../docs/adopters/guides/cli-parity-guide.md)
