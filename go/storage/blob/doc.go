// Package blob defines an abstract interface for object/blob storage.
//
// Implementations live in sub-packages (e.g. blob/local for filesystem).
// Delete of a missing key succeeds; List leaves Object.ContentType empty.
// Providers are checked by the shared suite in blob/blobtest.
package blob
