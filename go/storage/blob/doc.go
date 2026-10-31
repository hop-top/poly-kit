// Package blob defines an abstract interface for object/blob storage.
//
// Implementations live in sub-packages (e.g. blob/local for filesystem).
// Delete of a missing key succeeds. List's Object.ContentType is
// best-effort (empty where the provider cannot report it); implement and
// type-assert the optional Stater (see Stat) to read it reliably.
// Providers are checked by the shared suite in blob/blobtest.
package blob
