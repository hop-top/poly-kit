// Package dek serves a tool's command tree as the core side of the
// dek contract, under /v1/dek: the hello, the capability manifest and
// invoke. The manifest is generated from the same reflected commands
// the REST projection serves, and calls run through the same
// executor, so the permission gate, audit and idempotency every other
// surface has apply unchanged.
//
// See docs/adopters/reference/dek-surface.md for the mapping, what is
// served, and what is not yet.
//
// The wire types mirror the contract's JSON Schemas; the tests check
// every body against copies of those schemas in testdata/contract.
package dek
