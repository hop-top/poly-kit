# osnotifysink

OS-native desktop notification sink for kit bus events. Renders a
title + text against the `bus.Event`, optionally redacts, then
shells out via a breaker-wrapped runner.

## Constructor

```go
func New(opts ...Option) (bus.Sink, error)
```

The exception case to spec decision #9: construction CAN fail
because the constructor probes platform tooling. Returning
`(bus.Sink, error)` lets callers fail-fast on startup rather than
discovering "notify-send not on PATH" on the first event.

## Platform support

| Platform | Behaviour |
|----------|-----------|
| `darwin` | `osascript -e 'display notification ... with title ...'`. `osascript` ships with macOS; no probe, no install needed. |
| `linux` | `notify-send <title> <text>`. **Probed via `exec.LookPath` at construction; missing → `New` returns an error.** |
| `windows` | `New` returns `errors.New("osnotify: not supported on windows in MVP")`. Tracked for follow-up. |
| other | `New` returns `errors.New("osnotify: unsupported platform <GOOS>")`. |

The probe runs ONCE at construction. A `notify-send` installed
later is not picked up — matches kit's fail-fast-on-misconfiguration
pattern.

## Options

| Option | Default | Effect |
|--------|---------|--------|
| `WithTitle(t)` | required | `Template` rendered as the notification title. Drain errors if unset. |
| `WithText(t)` | required | `Template` rendered as the notification body. Drain errors if unset. |
| `WithRedactor(r)` | `nil` | `r.Apply` runs on rendered title AND text before egress. |
| `WithBreaker(b)` | `nil` | Wraps `runner.Run` via `breaker.WrapCtx`. Open circuit short-circuits before exec. |

## Templates

Same interface as `emailsink.Template`:

```go
type Template interface {
    Render(e bus.Event) (string, error)
}
```

Helpers: `TextTemplate(src)` (parses `text/template`) and
`LiteralTemplate(s)` (fixed string).

## Runner injection

The internal `runner` interface (`runner.Run(ctx, name, args...)`)
abstracts `os/exec.CommandContext` so unit tests can assert command
construction without shelling out. The injection option
(`withRunner`) is **unexported by design**: production callers
never need to override the production `execRunner`. Only tests in
the same package use it via package-internal access. There is no
public extensibility path for swapping the exec layer; alternative
notification mechanisms should ship as separate sinks.

## Pipeline

Per [`go/runtime/notify/guardrails.go`](../../guardrails.go):

```
render(title) + render(text) → redactor.Apply on each → breaker.WrapCtx(runner.Run)
```

`breaker.ErrBrokenCircuit` is returned unwrapped by
`breaker.WrapCtx`; `RetrySink` treats it as terminal.

AppleScript escaping (darwin): the rendered title/text are wrapped
in AppleScript double-quote literals with `"` and `\` escaped.
notify-send (linux) takes title/text as positional `argv` entries
(no shell interpretation), so no escaping is needed beyond what
`exec.CommandContext` already does.

## Usage

Desktop alert on warn-and-above, fronted by a filter:

```go
title, _ := osnotifysink.TextTemplate("[{{.Topic}}]")
text, _ := osnotifysink.TextTemplate("{{.Source}}: {{.Payload}}")

osSink, err := osnotifysink.New(
    osnotifysink.WithTitle(title),
    osnotifysink.WithText(text),
    osnotifysink.WithRedactor(redact.Default()),
    osnotifysink.WithBreaker(breaker.New("notify-osnotify")),
)
if err != nil {
    return fmt.Errorf("init os notify: %w", err)
}

desktop := notify.NewFilterSink(
    osSink,
    notify.WithMinSeverity(notify.SeverityWarn),
)
```

## See also

- [`go/runtime/notify/README.md`](../../README.md) — package overview
- [`go/runtime/notify/guardrails.go`](../../guardrails.go) — pipeline convention godoc
- [`go/core/breaker/README.md`](../../../../core/breaker/README.md) — `WrapCtx` semantics
- [`go/core/redact/README.md`](../../../../core/redact/README.md) — `Apply` semantics
