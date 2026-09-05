# shim

## What it answers

What an adapter may do when the target CLI lacks a universal option's
exact equivalent. The catalog is closed; a gap that none of these covers
is `MappingUnsupported` in the adapter, not a new helper here.

## Use it when

- the CLI takes directories but not files: `ExpandToParentDirs(files)` (S-1)
- the CLI takes files but the caller gave directories: `EnumerateDirFiles(dir, max, filter)` (S-2)
- the CLI has no file or directory scoping at all: `FormatFileBlock(files, cwd)` as a prompt prefix (S-3)
- a native flag exists but widens authority: `RefuseDangerousDegradation(option, requested, nativeFlag, safer)` builds the error diagnostic
- a `Config` value is a list: `SplitConfigList(value)`

## Quick start

```go
dirs := shim.ExpandToParentDirs([]string{
    "src/app/main.go",
    "src/app/util.go",
    "docs/README.md",
})
fmt.Println(dirs)
// Output: [docs src/app]
```

## Contract

- `ExpandToParentDirs` works on path strings only (no stat), drops a directory when an ancestor is present, and returns a sorted, deduplicated set. Pass cleaned paths.
- `EnumerateDirFiles` returns regular files only; `overflow` is true past `max`, and adapters refuse the build rather than truncate. `.gitignore` is not applied; pass a `filter` for that. `max <= 0` means no cap.
- `FormatFileBlock` renders paths relative to `cwd` when they lie under it, and returns `""` for no files.
- `SplitConfigList` splits on commas with backslash escapes, trims whitespace, drops empty items.
- S-4 (builtin-agent), S-5 (recipe) and S-6 (sandbox plus approval cross-shim) are adapter-local combinations of native flags; see the `vibe`, `goose` and `codex` READMEs.
- No dependencies beyond the standard library and `invoke`; keep it that way.

## Neighbours

- [`../adapters/`](../adapters/README.md): the callers.
- `go/core/uxp/invoke`: `Diagnostic`, `MappingSupport`.

## See also

- [invoke/README.md](../README.md)
