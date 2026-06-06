// Package id is kit's entity-ID primitive: a thin wrapper around
// [go.jetify.com/typeid] (v1.3.0) implementing the cross-language
// kit API shape per ADR 0001.
//
// A TypeID is a self-describing identifier of the form
// "prefix_<26-char-base32>", where the suffix is a UUIDv7 encoded in
// Crockford base32. Prefixes match ^[a-z]([a-z0-9_]*[a-z0-9])?$ and
// are limited to 63 characters. The canonical string round-trips
// losslessly through [Parse] and is the JSON wire form.
//
// # Surface
//
//   - [New], [MustNew]: generate a new TypeID for a runtime-supplied
//     prefix; returns the canonical string.
//   - [Parse]: round-trip from canonical string to a [Parsed] struct
//     containing the prefix and the underlying [github.com/google/uuid.UUID].
//   - [Typed]: a generic newtype parameterised by a [Prefixer] type T.
//     Use it to get compile-time prefix safety per entity (e.g. TaskID,
//     InvoiceID). [Typed] marshals to and from the bare canonical
//     string in JSON.
//
// URI composition (e.g. "tlc://task/task_01j…") is intentionally
// out of scope: callers compose poly-URIs via hop.top/cite.
//
// # Glossary
//
//   - prefix: the human-readable type tag (e.g. "task", "invoice"). Owned
//     by the calling tool; not registered centrally.
//   - suffix: 26-char Crockford-base32 encoding of a UUIDv7.
//   - canonical string: "prefix_suffix" (or bare "suffix" if prefix
//     is empty). The only wire form this package emits.
//   - Prefixer: an interface with a single Prefix() string method; the
//     type-level token that locks a [Typed] to a fixed prefix.
//
// # Cross-language parity
//
// The same UUIDv7 fed to the Go, Rust, TypeScript, Python, and PHP
// SDKs produces the same canonical string. The fixtures in id_test.go
// are pinned by UUID input so other language agents can compare
// against the same expected outputs (see tlc T-0753).
package id
