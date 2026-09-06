# agefile

## What it answers

A read-only store over one age-encrypted YAML file whose payload is a flat
`map[string]string`. Age gives multiple recipients, SSH-key and hardware
recipients; edits happen out of band. Wrong package when code must write
(`go/storage/secret/file`) or when one identity keys everything
(`go/storage/secret/local` with `file`).

## Use it when

- `secret.Config{Backend: "agefile", Path: "secrets.yaml.age", IdentityFile: "key.txt"}` after a blank import of this package; both fields are required
- a team commits one encrypted file and each member holds an age identity
- rotating: `age -d | $EDITOR | age -e -R recipients.txt`, then redeploy

## Contract

- Registered as `"agefile"`. `Set` and `Delete` return `ErrNotSupported`.
- Every `Get`/`List`/`Exists` decrypts the whole file; the identity file may hold several identities.
- `Metadata` reports the file mtime as `UpdatedAt` and returns `ErrNotFound` for absent keys, so it is not a presence oracle for arbitrary names.
- Tests: embedded; the package test generates an X25519 identity with `filippo.io/age`, encrypts YAML into `t.TempDir` and reads it back. No snippet here because that setup is the whole example; see `agefile_test.go`.

## Neighbours

- `hop.top/kit/go/storage/secret/file`: per-key files, writable.
- `hop.top/kit/go/storage/secret/composite`: pair agefile (CI, `RO`) with a writable member.

## See also

- [Secret management guide](../../../../docs/adopters/guides/secret-management-guide.md)
