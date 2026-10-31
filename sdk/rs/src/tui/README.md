# tui

## What it answers

How a Rust CLI reports progress and asks a yes/no question the way a Go kit CLI does, without a terminal framework: per-phase progress events (JSONL or human lines), a themed bar, spinner frames and status lines, a confirm prompt, and the header / main / footer frame with kit's canonical keymap. Port of `go/console/progress` and the adopter-facing part of `go/console/tui`. Feature `tui`, no dependencies. Wrong module for data output (`output`).

## Use it when

- a command has phases worth reporting: `progress::select(quiet, format, data_is_json, std::io::stderr())` once, then `reporter.emit(Event::new("download").item(name).bytes(n, total))`
- a long step should look alive in a terminal: `Spinner::frame(tick)`, `Bar::new(styler).set_percent(p).view()`
- a message needs a symbol and a colour: `status_line(&styler, InfoType::Success, "done")`
- a step needs consent: `Confirm::new("Delete?", styler).default_answer(false).ask_stdio()?`
- a full-screen app wants the kit frame: implement `AppRenderer`, wrap it in `AppShell`, drive `update`/`view` from your own event loop

## Quick start

```rust
use hop_top_kit::tui::progress::{self, Event};
use hop_top_kit::tui::{status_line, ColorMode, InfoType, Styler, Theme};

let reporter = progress::select(false, None, false, std::io::stderr());
reporter.emit(Event::new("resolve").item("github.com/foo/bar"));

let styler = Styler::detect(Theme::default(), ColorMode::Auto, &std::io::stderr());
eprintln!("{}", status_line(&styler, InfoType::Success, "Deployed"));
```

## Contract

- Colour: `Styler::detect` is on only for a terminal, never with `NO_COLOR` set and non-empty, never with `TERM=dumb`. `Styler::plain()` and `Styler::resolve(..)` take no environment, which is what tests use.
- Streams: progress, status and prompts go to stderr; stdout stays the data envelope.
- Non-interactive: `Confirm::ask` with `interactive = false` reads and writes nothing and returns the default answer, so CI never hangs; end of input while interactive is an `UnexpectedEof` error, never a guessed answer. `ask_stdio` is interactive only when stdin and stderr are both terminals.
- Progress: `emit` is best effort, never panics, never blocks on a failed write; reporters are `Send + Sync`, one line per lock. JSONL keys are `phase`, `at`, `item`, `bytes`, `total`, `ok`, `extra`, zero optional fields omitted, `at` RFC 3339 UTC. `select`: quiet wins, then an explicit `--progress-format`, then `--format json` implies JSONL, else human.
- Status symbols, spinner frames and interval are copied from [`contracts/parity/parity.json`](../../../../contracts/parity/parity.json); a unit test fails when a copy drifts.
- Every renderer returns a `String` and every prompt takes its reader and writer: no test needs a TTY.
- Text safety: every caller-supplied string a renderer interpolates (progress human `phase` and `item`, status text, confirm prompt, app shell header and footer) has control chars (C0, DEL, C1 including ESC, CSI U+009B, CR, LF) replaced with U+FFFD, so a remote-derived name cannot move the cursor or inject escapes. JSONL is already JSON-escaped and unchanged. Go writes these raw; this is a deliberate divergence. The app shell main region is the renderer's own output and passes through untouched, as does the shell's own styling; a styled header must come from the `Styler`, not from ANSI in the text.
- Widths count chars outside ANSI escapes. Wide glyphs are not measured and long lines are not wrapped.

## Decision: plain ANSI on std, not ratatui or crossterm

The Go package is bubbletea: a program loop, raw-mode key input and a renderer. What an adopter calls is small: report phases, draw a bar, print a status line, ask yes/no, frame a screen. None of it needs a widget framework.

| Option | Cost | Verdict |
|--------|------|---------|
| ratatui | crossterm, a buffer/diff renderer, ~30 crates, owns the terminal | A framework the adopter would have to pick for their own app anyway. Rejected: the library should not choose it for them |
| crossterm only | raw mode for single-key confirm, ~15 crates, platform code | Rejected: a line-based prompt (`y` then enter) is the CLI norm, works over pipes and `script`, and needs no raw mode to restore on panic or signal |
| plain ANSI on std | none | Chosen. `std::io::IsTerminal` covers the TTY probe; SGR truecolor is one `format!` |

Consequences: the confirm prompt reads a line, not a key (`ConfirmState` keeps the Go key machine for callers who own raw input); `AppShell` is a pure `update(msg) -> Cmd` / `view() -> String` pair, so the loop, raw mode and screen switching belong to the app (ratatui or crossterm there is the app's call, and `AppShell` composes with either). Revisit only if an adopter needs list pickers or text input inside kit.

## Parity with Go

| Go (`console/tui`, `console/progress`) | Rust | Status |
|----------------------------------------|------|--------|
| `progress.Event`, `Reporter`, `JSONL`, `Human`, `Discard` | `progress::{Event, Reporter, jsonl, human, discard}` | ported; `extra` values are JSON scalars (`Value`) rather than `any` |
| `--quiet` / `--progress-format` / `--format json` selection (`console/cli`) | `progress::select` | ported |
| `progress.WithReporter` / `FromContext` | pass `Arc<dyn Reporter>` | N/A: no `context.Context`; no ambient reporter, so no silent `Discard` fallback |
| `tui.Progress` (`SetPercent`, `SetWidth`, `View`) | `Bar` | ported; `ProgressMsg` and `ViewWithColor` N/A (bubbletea message, per-call colour override) |
| `tui.NewSpinner` | `Spinner` | ported as frames and interval; the tick loop is the caller's |
| `tui.InfoMsg` / `InfoType` rendering | `status_line`, `InfoType` | ported |
| `tui.Confirm` | `ConfirmState` (key machine), `Confirm` (line prompt) | ported; single-key raw input N/A, see decision |
| `tui.AppShell`, `KeyMap`, `Initer`/`Updater`/`Resizer`/`HeaderRenderer`/`FooterRenderer` | `AppShell`, `KeyMap`, `AppRenderer` (default methods) | ported as a state machine; `Run` / `tea.NewProgram` N/A |
| `AppShell` alt-screen option | `alt_screen`, `wants_alt_screen` | ported as a flag the caller's loop reads |
| `cli.Theme` | `Theme`, `Styler` (NEON default) | minimal: five colours; no `cli` theme module in Rust yet |
| `Status` help bar (`bubbles/help`), `ClearInfoAfter` | none | N/A: bubbles widget and bubbletea timer |
| `Badge`, `CheckDoneMsg` | none | deferred: needs the `upgrade` self-update checker, which the Rust crate does not port |
| `List`, `table`/`textinput`/`list` style helpers (`styles.go`) | none | N/A: wrappers over bubbles widgets |
| `Pills`, `Anim` | none | deferred: pure renderers, no adopter demand yet |
| `Renderer`, `Focusable`, `Animatable` | none | N/A: bubbletea capability interfaces |
| `tui/dialog`, `Overlay` | none | N/A: modal stack for bubbletea models |
| `tui/styles` (`Common`, lipgloss styles) | `Styler` | N/A: lipgloss; header and footer are one row each, as in Go |

## Neighbours

- `output`: the stdout data envelope and `--format`
- Go reference: [`go/console/tui`](../../../../go/console/tui/README.md), [`go/console/progress`](../../../../go/console/progress/README.md)
