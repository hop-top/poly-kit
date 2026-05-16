# SetFlag / TextFlag API Reference

> Unified multi-value flag types for CLI tools across Go, TS,
> Python. Replace per-field `--add-X` / `--remove-X` pairs with
> a single `--flag` accepting prefix operators.

## SetFlag

Ordered, deduplicated set. Comma-split input.

### Operators

| Prefix | Action   | Example              | Result          |
|--------|----------|----------------------|-----------------|
| `+`    | append   | `--tag +urgent`      | adds "urgent"   |
| `-`    | remove   | `--tag -draft`       | removes "draft" |
| `=`    | replace  | `--tag =final`       | set = {"final"} |
| `=`    | clear    | `--tag =`            | set = {}        |

Multiple values comma-separated: `--tag +a,+b,-c`.

Deduplication: repeated `+x` keeps one entry; order preserved.

### Escaping

Leading `=` escapes a literal prefix char:

```
--tag =+ppl      => literal "+ppl"
--tag =-dash     => literal "-dash"
--tag ==equals   => literal "=equals"
```

### Direct Methods

| Method   | Description              |
|----------|--------------------------|
| `Add`    | append value(s)          |
| `Remove` | remove value(s)          |
| `Clear`  | empty the set            |
| `Values` | current set (ordered)    |
| `String` | comma-joined display     |

### Registration

| Language | Function                                         |
|----------|--------------------------------------------------|
| Go       | `cli.RegisterSetFlag(cmd, name, usage)`          |
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

Same `=` escape as SetFlag:

```
--desc =+literal plus   => text = "+literal plus"
```

### Direct Methods

| Method    | Description                    |
|-----------|--------------------------------|
| `Append`  | add text on new line           |
| `Prepend` | add text before existing       |
| `Inline`  | append without newline         |
| `Replace` | replace entire text            |
| `Clear`   | empty the text                 |
| `String`  | current text                   |

### Registration

| Language | Function                                          |
|----------|---------------------------------------------------|
| Go       | `cli.RegisterTextFlag(cmd, name, usage)`          |
| TS       | `registerTextFlag(program, name, usage)`          |
| Python   | `register_text_flag(parser, name, usage)`         |

## FlagDisplay

Controls how flag values render in help / status output.

| Mode      | Output                     |
|-----------|----------------------------|
| `Prefix`  | `+urgent,-draft`           |
| `Verbose` | `add urgent, remove draft` |
| `Both`    | `+urgent (add urgent)`     |

Set via option on registration or per-flag method.

## Migration Guide

Tools using separate `--add-X` / `--remove-X` flags migrate
to unified `--X` with prefix operators.

**Before:**

```
cmd --add-tag urgent --remove-tag draft
```

**After:**

```
cmd --tag +urgent,-draft
```

### Tracked migrations

- `tlc` task tags: `tlc#T-0665`
- `aps` profile tags: `aps#T-0327`

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
