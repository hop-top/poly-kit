# keyring

## What it answers

The OS keychain (macOS Keychain, Windows Credential Manager, Secret
Service on Linux) through `go-keyring`, one service name per store. Wrong
package on headless hosts (`go/storage/secret/file` or `env`) and for
anything that must enumerate keys: `List` is not supported.

## Use it when

- `secret.Config{Backend: "keyring", Service: "myapp"}` after a blank import of this package; `Service` defaults to `kit`
- a developer laptop where the login session unlocks the store
- `Exists` is implemented via `Get`, so a locked keychain surfaces as an error, not `false`

## Contract

- Registered as `"keyring"`. `List` returns `ErrNotSupported`.
- `Metadata` reports key, source and backend only; `go-keyring` exposes no creation date, comment or ACL.
- Tests: `//go:build !ci`; they touch the real keychain and skip when it is unavailable. No snippet: the example would prompt for keychain access.

## Neighbours

- `hop.top/kit/go/storage/secret/file`: the portable fallback with a `Keeper`.
- `hop.top/kit/go/storage/secret/composite`: route developer credentials here and CI credentials elsewhere.

## See also

- [Secret management guide](../../../../docs/adopters/guides/secret-management-guide.md)
