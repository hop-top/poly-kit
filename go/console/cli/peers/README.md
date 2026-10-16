# peers

## What it answers

How does a kit CLI find and trust other instances of itself? `peers.With`
adds the peer mesh, its registry and trust manager, and the `peer`
commands to a `cli.Root`. Wrong package for the mesh itself, discovery
and TOFU trust (`go/runtime/peer`).

## Use it when

- your CLI discovers peers → `peers.With(peers.Config{})`, beside `cli.WithIdentity`
- custom discovery or mDNS service name → `peers.Config{Discovery: d, Service: "_mytool._tcp"}`
- code needs the mesh, registry or trust manager → `peers.Of(root).Mesh`, `.Registry`, `.Trust`

## Quick start

```go
dir, _ := os.MkdirTemp("", "peers")
defer os.RemoveAll(dir)

root := cli.New(cli.Config{Name: "mytool", Version: "0.1.0", Short: "peers example"},
    cli.WithIdentity(cli.IdentityConfig{Dir: dir}),
    peers.With(peers.Config{DataDir: dir}))

p := peers.Of(root)
fmt.Println("mesh:", p.Mesh != nil)
known, _ := p.Registry.List()
fmt.Println("known peers:", len(known))

bare := cli.New(cli.Config{Name: "other", Version: "0.1.0", Short: "no peers"})
fmt.Println("mesh without peers.With:", peers.Of(bare).Mesh != nil)
// Output:
// mesh: true
// known peers: 0
// mesh without peers.With: false
```

Verified by `example_test.go` in this directory.

## Contract

- `peers.With` requires `cli.WithIdentity`, in either order. Without it,
  `cli.New` still returns and the root's `PersistentPreRunE` fails with
  `peer: peers.With requires cli.WithIdentity`, after `--chdir` ran;
  `--help` and completion keep working. A store that fails to open
  surfaces the same way.
- `peers.Of` never returns nil and attaches nothing. Its fields are nil
  until `cli.New` initialized them, so test `peers.Of(root).Mesh != nil`,
  never `peers.Of(root) != nil`.
- The registry lives in `<DataDir>/peers.db`, default
  `$XDG_DATA_HOME/kit/peers/`. The `peer` group is not kit-reserved.
- The mesh is built, not started: call `Mesh.Start(ctx)` where the tool
  runs; the kit binary starts it when it serves.

## Neighbours

- `hop.top/kit/go/runtime/peer`: mesh, discoverers (mDNS), registry, trust manager.
- `hop.top/kit/go/console/cli`: `WithIdentity`, and the extension slot (`cli.Extend`, `cli.InitHook`) this package attaches through.

## See also

- [Migrate to go/console/cli/service](../../../../docs/adopters/guides/migrate-to-cli-service.md#peer-discovery): moving from `cli.WithPeers`
