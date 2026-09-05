# ts

## What it answers

Where a TypeScript copy of the `crud.v1` protobuf and Connect stubs sits under `contracts/`. It is not the copy the SDK builds against: `sdk/ts/src/rpc.ts` imports `sdk/ts/src/gen/crud_pb.js`, and that is where `buf generate` writes.

## Use it when

- you look for the TypeScript stubs to import: use `sdk/ts/src/gen/` instead
- you regenerate after editing `../proto/crud/v1/crud.proto`: `make proto` updates `sdk/ts/src/gen/`, not this directory

## Contract

`src/gen/` holds `crud_pb.ts` and `crud_connect.ts`. `../proto/crud/v1/buf.gen.yaml` targets `../../../sdk/ts/src/gen` for the `bufbuild/es` and `connectrpc/es` plugins, so nothing regenerates this directory and no package manifest references it. `crud_connect.ts` matches the SDK copy; `crud_pb.ts` does not.

## Neighbours

- `../proto/crud/v1/`: the `.proto` source and the Go stubs
- `sdk/ts/src/gen/`: the stubs the TypeScript SDK ships

## See also

- [`contracts/proto/crud/v1/README.md`](../proto/crud/v1/README.md)
- [`sdk/ts/README.md`](../../sdk/ts/README.md)
