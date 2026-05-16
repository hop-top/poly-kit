# contracts

shared schemas and cross-language constants.

## Contents

- [proto/](proto/README.md): shared protobuf definitions.
- [parity/](parity/README.md): TUI constants shared across Go/TS/Py.
- [bridge.proto](bridge.proto): kit/bridge wire payload — protobuf
  schema (binary wire + semantics). Authoritative JSON schema for
  non-Go shells (Swift Share Extension, Shortcuts) lives alongside in
  `bridge.schema.json`.
