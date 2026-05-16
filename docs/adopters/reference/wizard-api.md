# Wizard API Reference

`hop.top/kit/go/console/wizard` -- headless, sequential wizard engine for
interactive CLI flows. Pure-logic core with pluggable frontends
(TUI, line-mode, headless).

> Go-only. No TS/Python bindings yet.

## Step Kinds

| Kind           | Constant          | Value type   | Description             |
|----------------|-------------------|--------------|-------------------------|
| TextInput      | `KindTextInput`   | `string`     | Free-text prompt        |
| Select         | `KindSelect`      | `string`     | Single-choice list      |
| Confirm        | `KindConfirm`     | `bool`       | Yes/no question         |
| MultiSelect    | `KindMultiSelect` | `[]string`   | Multi-choice list       |
| Action         | `KindAction`      | *(none)*     | Runs arbitrary function |
| Summary        | `KindSummary`     | *(none)*     | Read-only review step   |

## Builders

Each builder returns a `Step` value. Chain modifiers via value
receivers (each returns a copy).

```go
wizard.TextInput(key, label string) Step
wizard.Select(key, label string, opts []Option) Step
wizard.Confirm(key, label string) Step
wizard.MultiSelect(key, label string, opts []Option) Step
wizard.Action(key, label string, fn func(ctx, results) error) Step
wizard.Summary(label string) Step  // key auto-generated
```

## Option

```go
type Option struct {
    Value       string
    Label       string
    Description string
}
```

Used by `Select` and `MultiSelect` steps. `Value` is stored in
results; `Label` is displayed to the user.

## Chainable Modifiers

All return a new `Step` (value semantics).

| Modifier                             | Effect                       |
|--------------------------------------|------------------------------|
| `.WithRequired()`                    | Mark step as required        |
| `.WithDefault(v any)`               | Set default value            |
| `.WithDescription(d string)`        | Add description text         |
| `.WithGroup(g string)`              | Visual grouping label        |
| `.WithWhen(key, pred)`              | Conditional visibility       |
| `.WithOnError(action ErrorAction)`  | Action-step failure policy   |
| `.WithValidateText(fn)`             | Validator for TextInput      |
| `.WithValidateChoice(fn)`           | Validator for Select         |
| `.WithValidateChoices(fn)`          | Validator for MultiSelect    |
| `.WithFormat(fn)`                   | Custom summary formatter     |

### Example

```go
wizard.TextInput("name", "Project name").
    WithRequired().
    WithDefault("my-app").
    WithValidateText(func(s string) error {
        if len(s) < 3 { return errors.New("too short") }
        return nil
    })
```

## Wizard Engine

### Constructor

```go
w, err := wizard.New(steps ...Step)
```

Validates steps: unique keys, correct default types, action
steps have `ActionFn`, select steps have options. Returns error
on invalid configuration.

### Core Methods

| Method             | Signature                          | Description                         |
|--------------------|------------------------------------|-------------------------------------|
| `Current()`        | `*Step`                            | Current visible step (skips When=false) |
| `Advance(value)`   | `(any, error)`                     | Submit value; returns `*ActionRequest` for actions |
| `Back()`           | --                                 | Move to previous visible step       |
| `Results()`        | `map[string]any`                   | Copy of collected results           |
| `Done()`           | `bool`                             | True when past last step            |
| `StepCount()`      | `int`                              | Count of currently visible steps    |
| `StepIndex()`      | `int`                              | 0-based index among visible steps   |

### DryRun Mode

```go
w.SetDryRun(true)
w.DryRun() // -> true
```

When enabled, `Complete()` skips the `OnComplete` callback.
Useful for previewing wizard output without side effects.

### OnComplete Callback

```go
w.SetOnComplete(func(results map[string]any) error {
    // persist config, create files, etc.
    return nil
})
w.Complete() // invokes callback (unless DryRun)
```

## Action Steps

`Advance()` on an action step returns `*ActionRequest`:

```go
type ActionRequest struct {
    StepKey string
    Run     func(ctx context.Context, results map[string]any) error
}
```

The frontend calls `Run()`, then reports outcome via
`w.ResolveAction(err)`.

### Error Handling

`ErrorAction` controls behavior when `Run` returns an error:

| Constant      | Behavior                            |
|---------------|-------------------------------------|
| `ActionAbort` | Return `*ActionError`; stop wizard  |
| `ActionRetry` | Stay on current step; retry         |
| `ActionSkip`  | Advance past the failed step        |

Set via `.WithOnError(wizard.ActionRetry)`.

## Error Types

| Type              | When                                   |
|-------------------|----------------------------------------|
| `ValidationError` | Value fails type/required/custom check |
| `ActionError`     | Action step fails + policy = abort     |
| `AbortError`      | Wizard cancelled (context, interrupt)  |

All implement `error`; `ValidationError` and `ActionError` also
implement `Unwrap()`.

## Result Accessors

Type-safe helpers for reading `map[string]any` results:

```go
wizard.String(results, "name")    // string or ""
wizard.Bool(results, "verbose")   // bool or false
wizard.Strings(results, "tags")   // []string or nil
wizard.Choice(results, "region")  // alias for String
```

## Conditional Steps

Gate step visibility on prior answers:

```go
wizard.TextInput("db_host", "Database host").
    WithWhen("use_db", func(v any) bool {
        b, _ := v.(bool)
        return b
    })
```

Step is skipped (and its result cleared) when predicate returns
false. `StepCount()` and `StepIndex()` reflect only visible steps.

## Frontends

### Run (auto-select)

```go
wizard.Run(ctx, w,
    wizard.OnComplete(saveFn),
    wizard.WithDryRun(),
)
```

Selection priority:
1. `ForceLine()` -- line-mode stdio
2. `ForceTUI()` -- bubbletea TUI (requires `WithTUI()`)
3. `WithAnswers(map)` -- headless
4. stdin is pipe -- headless with empty answers
5. TUI registered -- TUI
6. fallback -- line-mode

### RunOptions

| Option             | Effect                              |
|--------------------|-------------------------------------|
| `WithInput(r)`     | Set reader for line mode            |
| `WithOutput(w)`    | Set writer for line mode            |
| `WithAnswers(m)`   | Pre-filled answers; selects headless|
| `WithTUI(fn)`      | Register TUI frontend function      |
| `ForceLine()`      | Force line-mode frontend            |
| `ForceTUI()`       | Force TUI frontend                  |
| `OnComplete(fn)`   | Register completion callback        |
| `WithDryRun()`     | Enable dry-run mode                 |

### Headless Driver

Drive wizard programmatically with pre-supplied answers:

```go
results, err := wizard.RunHeadless(ctx, w, map[string]any{
    "name":    "my-project",
    "region":  "us-east-1",
    "confirm": true,
})
```

Missing keys fall back to `DefaultValue`, then zero values.
Required steps with no answer and no default produce
`ValidationError`.

### LoadAnswers

```go
answers, err := wizard.LoadAnswers("answers.yaml")
```

Reads a YAML file into `map[string]any` suitable for
`RunHeadless` or `WithAnswers`.

### Line-Mode Frontend

```go
wizard.RunLine(ctx, w, os.Stdin, os.Stdout)
```

Interactive stdio driver with:
- Group headers (`-- Group Name --`)
- Pagination for large option lists (20 per page)
- `b`/`back` to go back
- Summary rendering with custom formatters

### TUI Frontend

Provided by `kit/go/console/wizard/wizardtui` (separate import to avoid
bubbletea dependency in core):

```go
import "hop.top/kit/go/console/wizard/wizardtui"

wizard.Run(ctx, w, wizard.WithTUI(wizardtui.Run))
```
