# Tui

## What it answers

Which status symbols, spinner frames and help-section titles does a PHP CLI print so its terminal
output matches the other ports? This namespace currently holds an empty placeholder class, `Tui`,
with no members. The values themselves live in `contracts/parity/parity.json`; no PHP loader reads
that file yet. Rendering data as tables or JSON is not a TUI concern: that is `HopTop\Kit\Output`.

## Contract

- The `status`, `spinner`, `anim` and `help` blocks in `parity.json` are the cross-language constants
  a PHP implementation must load rather than copy
- The block-registry guards described in the parity README apply once a PHP loader exists
- Parity: [`contracts/parity/parity.json`](../../../../../contracts/parity/parity.json), no PHP
  loader recorded; Go `go/console/tui` is the reference

## Neighbours

- `HopTop\Kit\Output`: formatter-driven rendering of command results
- `HopTop\Kit\Cli`: the command base class that will own terminal presentation hooks

## See also

- [Parity contract README](../../../../../contracts/parity/README.md)
- [TUI component gallery](../../../../../docs/adopters/guides/tui-component-gallery.md)
- [Go reference: `go/console/tui`](../../../../../go/console/tui/README.md)
