# Prompt Reference

> Reference for `hop.top/kit/go/ai/prompt`: versioned prompt templates,
> patterns, techniques, fragments and a schema DSL. Callers inject every
> path.

## Templates

A template is the file `name@N.yaml`:

```yaml
system: Write a one-page brief from the facts given.
template: |
  # Brief: {{ .event.title }}
  Where: {{ default "not set" .event.location }}
  Tags: {{ join ", " .tags }}
```

```go
ref, _ := prompt.ParseRef("brief@1")
tpl, err := prompt.DirSource("./prompts", sharedDir).Load(ref)
text, err := tpl.Render(map[string]any{...})
msgs, err := tpl.Messages(vars) // []llm.Message: system (if set), then user
```

Rules, identical to flo's `internal/prompts`:

- **Ref.** `name@N`: lowercase `[a-z0-9][a-z0-9._-]*`, version from 1, no
  leading zero, no path separator. `ParseRef` rejects the rest.
- **Strict file.** YAML with `system` and `template` only; an unknown key is
  an error. `template` is required and non-blank; `system` is used verbatim
  (not rendered).
- **Body.** Go `text/template` with `missingkey=error`: a key the body reads
  and `vars` lacks fails the render, a nil `vars` included.
- **Functions.** The FuncMap adds exactly `join sep v`, `trunc n s` (runes)
  and `default d v` (sprig argument order). A body calling any other
  function fails in `ParseTemplate`. The predeclared `text/template`
  builtins (`len`, `index`, `printf`, `eq`, ...) stay.
- **Hash.** `Template.Hash` is the hex sha256 of the raw file bytes, so a
  recorded run can name the exact prompt text it used.

Sources:

| Source | Lookup |
|--------|--------|
| `DirSource(dirs...)` | `dir/name@N.yaml`; first dir holding the file wins, layers are not merged |
| `FSSource(fsys, root)` | `root/name@N.yaml` in an `fs.FS`, for embedded prompts |

A present but broken file is an error; it never falls through to a later
dir. A missing template matches `prompt.ErrNotFound`; malformed input
matches `prompt.ErrInvalid`.

## Patterns

A pattern is a named system prompt, the file `<dir>/<name>/system.md`.
`DirPatterns(dirs...)` returns a `PatternSource` with `Load(name)` and
`List()`; the first dir holding a name wins, `List` is sorted and
deduplicated, a missing dir is skipped. A name is one path segment.

## Techniques

A `Technique` (`Name`, `Description`, `Prefix`, `Suffix`) wraps a system
prompt: `Wrap(t, system)` joins prefix, system and suffix with a blank
line, dropping empty parts. `Builtins()` returns `cot`, `tot`, `aot`,
`reflexion` and `step-back`. `DirTechniques(dirs...)` reads `<name>.md`
files (optional `# description` first line, prefix, then a `---` line and
the suffix) and falls back to the builtins; a file shadows a builtin of the
same name.

"Strategy" is reserved for multi-call strategies; a technique is one
prompt transform.

## Fragments

```go
store := prompt.NewDirStore("./fragments") // or NewMemStore()
text, err := prompt.ResolveFragments(ctx, store, []string{"style", "glossary"})
```

`FragmentStore` is `Get(ctx, alias) ([]byte, error)`. `ResolveFragments`
joins contents with `"\n---\n"` (`FragmentSeparator`) and names the failing
alias in its error. `DirStore` reads `dir/<alias>`; `MemStore` is
concurrent-safe and copies on `Set` and `Get`.

## Schema DSL

```go
schema, err := prompt.CompileDSL("name, age int, tags []str")
```

`field [type], ...`; types `str` (default), `int`, `float`, `bool`,
`[]str`, `[]int`. The result is a JSON Schema object with every field
required and `additionalProperties: false`. An empty DSL, an unknown type,
a repeated field or a segment with more than a name and a type is
`ErrInvalid`.
