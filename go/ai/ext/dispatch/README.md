# dispatch

## What it answers

How discovered plugin binaries appear as subcommands of a cobra tool under a
"Plugins" group and run transparently with the caller's stdin, stdout and
stderr. Wrong package when you only need the list of plugins
(`hop.top/kit/go/ai/ext/discover`).

## Use it when

- your binary mounts plugins: `dispatch.Register(root, "kit", "")` scans `$PATH` for `kit-*`; pass a directory as the third argument to scan only there

## Quick start

```go
dir, _ := os.MkdirTemp("", "demo-plugins")
defer os.RemoveAll(dir)
_ = os.WriteFile(filepath.Join(dir, "demo-hello"), []byte("#!/bin/sh\necho hello\n"), 0o755)

root := &cobra.Command{Use: "demo"}
dispatch.Register(root, "demo", dir)
for _, c := range root.Commands() {
    fmt.Println(c.Name(), c.GroupID)
}
// Output:
// hello plugins
```

Verified by `example_test.go` in this directory.

## Contract

- Mounted subcommands disable flag parsing: every argument is forwarded verbatim to the plugin binary.
- `--ext-info` enrichment is deferred until help is rendered for that subcommand, so `Register` never executes plugins.
- An empty prefix, a scan error or zero matches leaves the tree untouched.
- Import only in binaries that need plugin dispatch; it pulls in `ext/discover`.

## Neighbours

- `hop.top/kit/go/ai/ext/discover`: the scanner and `--ext-info` interrogation.
- `hop.top/kit/go/console/cli`: kit `Root` the mounted group attaches to.

## See also

- [ext-discover protocol](../../../../docs/contracts/ext-discover-protocol.md)
