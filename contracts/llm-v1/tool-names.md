# llm-v1: tool names

Normative for every port. Providers restrict tool names; callers use
ids such as `pane.open` or `capability:files.read`. Each tool-calling
adapter checks the names of a request's tools, sends names the provider
accepts, and returns the caller's names.

## Rules

A rule allows 1 to `max` bytes, each an ASCII letter, digit, `_`, `-`
or one of `extra`; with `lead`, the first byte is a letter or `_`.

| Rule | Adapters | `max` | `extra` | `lead` | Source |
|------|----------|-------|---------|--------|--------|
| `anthropic` | anthropic | 128 | | no | Messages API `tools[].name`: `^[a-zA-Z0-9_-]{1,128}$` |
| `openai` | openai and every scheme it serves (openrouter, groq, xai, together, fireworks, deepseek, mistral, lmstudio, catalog providers by protocol), routellm through it | 64 | | no | Chat Completions `function.name`: a-z, A-Z, 0-9, `_`, `-`, max 64 |
| `gemini` | google, gemini | 128 | `.:` | yes | `FunctionDeclaration.name`: starts with a letter or `_`; a-z, A-Z, 0-9, `_`, `.`, `:`, `-`; max 128 |

ollama documents no rule and has no tool-calling entry point; it sends
replayed names as given. triton is a scorer without tools.

## Sanitizer

`sanitize(rule, name)`, over the UTF-8 bytes of `name`:

1. `name` empty: error `empty`. Not valid UTF-8 (a lone surrogate in a
   UTF-16 or Python string included): error `invalid_utf8`.
2. `name` valid for the rule: return it unchanged.
3. Replace every maximal run of bytes the rule does not allow with one
   `_` (U+005F). Every allowed byte is ASCII, so a multi-byte character
   is always inside a run.
4. With `lead`, when the first byte is not an ASCII letter or `_`,
   prepend `_`.
5. Let `h` be the first 8 lowercase hex digits of SHA-256 of the
   original UTF-8 bytes. When the result is longer than `max`, or holds
   no ASCII letter or digit, keep its first `min(len, max - 9)` bytes
   and append `_` and `h`; the result is then at most `max` bytes.
6. Return the result: never empty, always valid for the rule.

Examples (`openai` unless noted): `pane.open` -> `pane_open`;
`mcp://fs/read_file` -> `mcp_fs_read_file`; `café.menu` -> `caf_menu`;
`検索` -> `__1b6b1a6f`; 65 × `a` -> 55 × `a` + `_635361c4`; on
`gemini`, `pane.open` stays and `1tool` -> `_1tool`.

## Per request

For the tools of one request, in order, the first failing tool fails
the request before any network call with a typed, non-fallbackable
error carrying `reason`, `name` and `other`:

| `reason` | when |
|----------|------|
| `empty` | the name is empty |
| `invalid_utf8` | the name is not valid UTF-8 |
| `invalid` | mode `strict` and the name is not valid for the rule |
| `duplicate` | an earlier tool has the same name |
| `collision` | an earlier tool (`other`) has the same wire name |

Otherwise the wire name is `sanitize(rule, name)` (mode `sanitize`) or
the name itself (mode `strict`), and the request keeps a reverse table
wire -> original.

- Tool definitions go out under their wire names.
- Replayed history goes out mapped the same way: a call's name (and any
  reference by name, such as Gemini's `functionResponse.name`) is the
  tool's wire name; a name no tool of the request defines goes out
  sanitized (mode `sanitize`) or unchanged (mode `strict`, or a name
  step 1 refuses). This holds for every entry point, tools or not.
- Every tool call the adapter returns, whole or from a stream outcome,
  carries the original name; a name the table does not hold comes
  back unchanged.

## Mode

The provider param `tool_names` picks the mode:
`scheme://model?tool_names=strict`. `sanitize` is the default;
`strict` is for callers whose names are already valid and want them
checked, never rewritten. Any other value fails adapter construction.
Strict still fails invalid names before sending: the provider would
reject them anyway, and a typed error names the tool.

## See also

- [README](README.md): the llm-v1 contract and its runners
