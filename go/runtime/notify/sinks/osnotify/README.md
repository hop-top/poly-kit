# osnotifysink

## What it answers

How does a bus event reach the operator's own screen? Renders a title
and text against the `bus.Event`, optionally redacts, then shells out
to the platform notifier (`osascript` on darwin, `notify-send` on
linux) through a breaker-wrapped runner. Wrong package for anything
that must reach another machine (`../webhook`, `../email`).

## Use it when

- a desktop alert on warn-and-above → `osnotifysink.New(osnotifysink.WithTitle(t), osnotifysink.WithText(t))` behind `notify.NewFilterSink`
- a fixed title → `osnotifysink.LiteralTemplate("kit alert")`; event-driven text → `osnotifysink.TextTemplate(src)`

## Quick start

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

## Contract

- `New` returns `(bus.Sink, error)`: the one exception to spec
  decision #9, because construction probes platform tooling so a
  missing `notify-send` fails at startup, not on the first event.
- Platforms: darwin via `osascript` (no probe); linux via
  `notify-send`, probed once with `exec.LookPath` at construction (a
  binary installed later is not picked up); windows and other
  platforms return an error from `New`.
- Title and text are required; `Drain` errors when either is unset.
- Pipeline: `render(title) + render(text) → redactor.Apply on each →
  breaker.WrapCtx(runner.Run)`; `breaker.ErrBrokenCircuit` is
  terminal for `RetrySink`.
- Escaping: darwin wraps title/text in AppleScript double-quote
  literals with `"` and `\` escaped; linux passes them as positional
  argv, no shell.
- The exec runner is not swappable from outside the package; an
  alternative mechanism ships as a separate sink.

## Neighbours

- `../webhook`, `../email`: the other reference sinks.
- `go/runtime/notify`: `FilterSink`, `RetrySink`, severity.

## See also

- [Notify sink reference](../../../../../docs/adopters/reference/notify-sinks.md#osnotify-osnotifysink): platform table, options, runner injection, escaping
- [`go/runtime/notify/guardrails.go`](../../guardrails.go): pipeline convention godoc
- [`go/core/breaker/README.md`](../../../../core/breaker/README.md): `WrapCtx` semantics
- [`go/core/redact/README.md`](../../../../core/redact/README.md): `Apply` semantics
