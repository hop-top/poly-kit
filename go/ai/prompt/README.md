# prompt

## What it answers

How a Go tool keeps its prompt text out of code: versioned `name@N.yaml`
templates with a content hash, named system-prompt patterns, reasoning
techniques that wrap a system prompt, aliased fragments and a one-line
schema DSL. Every directory or filesystem is injected; nothing reads a
default location. Wrong package when you need to call a model
(`hop.top/kit/go/ai/llm`) or run a multi-call strategy.

## Use it when

- you render a versioned prompt → `prompt.ParseRef("brief@2")`, `prompt.DirSource(dirs...).Load(ref)`, `tpl.Render(vars)`; a missing key is an error
- you need chat messages from a template → `tpl.Messages(vars)` returns `[]llm.Message`
- you record which prompt text a run used → `tpl.Hash`, sha256 of the file bytes
- you embed prompts in the binary → `prompt.FSSource(embeddedFS, "prompts")`
- you load a named system prompt → `prompt.DirPatterns(dirs...)`, `.Load(name)`, `.List()`
- you add a reasoning technique → `prompt.Builtins()`, `prompt.DirTechniques(dirs...)`, `prompt.Wrap(t, system)`
- you splice shared snippets → `prompt.ResolveFragments(ctx, store, aliases)` over `NewMemStore()` or `NewDirStore(dir)`
- you want a JSON Schema from a field list → `prompt.CompileDSL("name, age int, tags []str")`

## Contents

| Path | What it is | Start here when |
|------|------------|-----------------|
| [`template.go`](template.go) | `Ref`, `Template`, parse and render, the three template funcs | you change the template file format |
| [`source.go`](source.go) | `Source`, `DirSource`, `FSSource` | you add a template location |
| [`pattern.go`](pattern.go) | `Pattern`, `PatternSource`, `DirPatterns` | you change how patterns resolve |
| [`technique.go`](technique.go) | `Technique`, `Wrap`, `DirTechniques`; shipped set in `builtins.go` | you add or change a technique |
| [`fragment.go`](fragment.go) | `FragmentStore`, `ResolveFragments`, `MemStore`, `DirStore` | you add a fragment backend |
| [`schema.go`](schema.go) | `CompileDSL` | you extend the DSL types |

Reference: [`docs/adopters/reference/prompt.md`](../../../docs/adopters/reference/prompt.md).
