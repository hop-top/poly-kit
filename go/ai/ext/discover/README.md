# discover

## What it answers

Which external plugin binaries exist for this tool (`<prefix>-*` on PATH)
and what each one says about itself via `--ext-info`. Wrong package when the
extension is compiled in (`hop.top/kit/go/ai/ext/registry`) or when you want
the plugins mounted as subcommands (`hop.top/kit/go/ai/ext/dispatch`).

## Use it when

- you list plugins: `(&discover.Scanner{Prefix: "kit-"}).Scan()`; set `Paths` to scan specific directories instead of `$PATH`
- you need name, version and description: `found.Enrich()` then `found.Meta()`
- you interrogate one binary directly: `discover.Interrogate(path)`
- you run a plugin as an `ext.Extension`: `found.Init(ctx)` executes the binary

## Quick start

```go
dir, _ := os.MkdirTemp("", "demo-plugins")
defer os.RemoveAll(dir)
_ = os.WriteFile(filepath.Join(dir, "demo-hello"), []byte("#!/bin/sh\necho hello\n"), 0o755)

s := &discover.Scanner{Prefix: "demo-", Paths: []string{dir}}
found, _ := s.Scan()
for _, f := range found {
    fmt.Println(f.Name, f.Meta().Name, f.Capabilities())
}
// Output:
// hello hello discover
```

Verified by `example_test.go` in this directory.

## Contract

- `Scan` returns executables only, deduplicated by name, ordered by first occurrence across the scanned directories; `Name` has the prefix stripped.
- `Interrogate` and `Enrich` execute the binary with `--ext-info` under a 5s timeout and parse JSON; on failure the `Found` stays usable with metadata synthesized from its name.
- Every `Found` reports `ext.CapDiscover`; `Close` is a no-op.
- Wire shape and lifecycle: [ext-discover protocol](../../../../docs/contracts/ext-discover-protocol.md).

## Neighbours

- `hop.top/kit/go/ai/ext`: `Extension`, `Metadata`, `Capability`.
- `hop.top/kit/go/ai/ext/dispatch`: cobra bridge built on this scanner.
- `hop.top/kit/go/ai/ext/config`: enable or disable a discovered plugin by name.

## See also

- [ext-discover protocol](../../../../docs/contracts/ext-discover-protocol.md)
