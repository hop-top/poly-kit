# SetFlag / TextFlag API Reference

> Unified multi-value flag types for CLI tools across Go, TS,
> Python. Replace per-field `--add-X` / `--remove-X` pairs with
> a single `--flag` accepting prefix operators.

## SetFlag

Ordered, deduplicated set. Comma-split input.

### Operators

| Prefix | Action   | Example              | Result          |
|--------|----------|----------------------|-----------------|
| (none) | append   | `--tag urgent`       | adds "urgent"   |
| `+`    | append   | `--tag +urgent`      | adds "urgent"   |
| `-`    | remove   | `--tag -draft`       | removes "draft" |
| `=`    | replace  | `--tag =final`       | set = {"final"} |
| `=`    | clear    | `--tag =`            | set = {}        |

The operator is read from the **first character of the whole
argument** and applies to everything after it. It is not a per-value
prefix:

```
--tag a,b,c        => {"a","b","c"}     append, comma-split
--tag +a,b         => {"a","b"}         append, comma-split
--tag +a,+b,-c     => {"a","+b","-c"}   one operator; "+b" is literal
--tag -b           => removes "b"
--tag -a,b         => removes nothing; the literal "a,b" is not a member
```

Append and replace comma-split their remainder; **remove does not** —
`-x` deletes exactly one member named `x`. Use a repeated flag to
remove several: `--tag -a --tag -b`.

Deduplication applies to appends: a repeated `+x` keeps one entry and
order is preserved.

### Escaping

A leading `=` is the replace operator, so its remainder is taken
literally — this doubles as the escape for a value that itself
starts with an operator char:

```
--tag =+ppl      => set = {"+ppl"}
--tag =-dash     => set = {"-dash"}
--tag ==equals   => set = {"=equals"}
```

Escaping replaces the set; it does not append to it.

### Direct Methods

| Method   | Description              |
|----------|--------------------------|
| `Add`    | append value(s)          |
| `Remove` | remove value(s)          |
| `Clear`  | empty the set            |
| `Values` | copy of the current set (ordered) |
| `String` | comma-joined display     |
| `Type`   | pflag type name (`"set"`) |

### Registration

| Language | Function                                         |
|----------|--------------------------------------------------|
| Go       | `cli.RegisterSetFlag(cmd, name, usage string, display FlagDisplay) *SetFlag` |
| TS       | `registerSetFlag(program, name, usage)`          |
| Python   | `register_set_flag(parser, name, usage)`         |

Returns the flag value holder; caller reads `.Values()` after
parse.

## TextFlag

Mutable text block. Supports append, prepend, replace, clear.

### Operators

| Prefix | Action          | Example                         |
|--------|-----------------|---------------------------------|
| `+`    | newline append  | `--desc +second line`           |
| `+=`   | inline append   | `--desc +=...continued`         |
| `^`    | newline prepend | `--desc ^header`                |
| `^=`   | inline prepend  | `--desc ^=prefix`               |
| `=`    | replace         | `--desc =new full text`         |
| `=`    | clear           | `--desc =`                      |

### Escaping

Same `=` replace-as-escape as SetFlag:

```
--desc =+literal plus   => text = "+literal plus"
```

Note the operator match order: `+=` and `^=` are tested before the
bare `+` and `^`, and `=` last.

### Direct Methods

| Method           | Description                          |
|------------------|--------------------------------------|
| `Append`         | add text on a new line               |
| `AppendInline`   | concatenate without a newline        |
| `Prepend`        | add text before existing, new line   |
| `PrependInline`  | concatenate before existing, inline  |
| `Value`          | current text                         |
| `String`         | current text (pflag display)         |
| `Type`           | pflag type name (`"text"`)           |

There is no `Replace` or `Clear` method: replace is `Set("=new")`
and clear is `Set("")` or `Set("=")`, both through the flag form.

### Registration

| Language | Function                                          |
|----------|---------------------------------------------------|
| Go       | `cli.RegisterTextFlag(cmd, name, usage string, display FlagDisplay) *TextFlag` |
| TS       | `registerTextFlag(program, name, usage)`          |
| Python   | `register_text_flag(parser, name, usage)`         |

## FlagDisplay

Controls which flag *forms* are registered and shown in help. It does
not change how values render. Every form always parses regardless of
the setting; a form that is not displayed is registered hidden or not
at all.

| Mode                  | Forms shown for `--tag` / `--desc`                          |
|-----------------------|-------------------------------------------------------------|
| `FlagDisplayPrefix`   | `--tag` only (prefix operators in the usage string)          |
| `FlagDisplayVerbose`  | SetFlag: `--add-tag`, `--remove-tag`, `--clear-tag`; TextFlag: `--desc` plus `--desc-append`, `--desc-append-inline`, `--desc-prepend`, `--desc-prepend-inline` |
| `FlagDisplayBoth`     | both of the above                                            |

`FlagDisplayPrefix` is the zero value. For `RegisterSetFlag` the
prefix form is registered under every mode and hidden when the mode
is `FlagDisplayVerbose`; for `RegisterTextFlag` the base `--name` is
always visible.

Passed as the fourth argument to `RegisterSetFlag` / `RegisterTextFlag`.

## Migration Guide

Tools using separate `--add-X` / `--remove-X` flags migrate
to unified `--X` with prefix operators.

**Before:**

```
cmd --add-tag urgent --remove-tag draft
```

**After:**

```
cmd --tag +urgent --tag -draft
```

One operator per flag occurrence: `--tag +urgent,-draft` would add
the literal values `urgent` and `-draft`, not remove `draft`.

### Steps

1. Register via `RegisterSetFlag` / `RegisterTextFlag`
2. Remove old `--add-*` / `--remove-*` flag definitions
3. Map old flag values to new prefix operators in
   backwards-compat shim (if needed)
4. Update help text + shell completions

## Cross-Language Parity

All three runtimes implement identical operator parsing and
escaping. Tests are ported across languages to ensure parity.

| Feature           | Go  | TS  | Python |
|-------------------|-----|-----|--------|
| SetFlag           | yes | yes | yes    |
| TextFlag          | yes | yes | yes    |
| FlagDisplay       | yes | yes | yes    |
| Registration      | yes | yes | yes    |
| Comma-split       | yes | yes | yes    |
| Escape via `=`    | yes | yes | yes    |

Comma-split applies to append and replace only. Parity tests live in
`go/console/cli/parity_textflag_test.go` and the SDK equivalents.
