// Code generation directive for the parity tables. Run
// `go generate ./go/core/uxp/...` to regenerate them in
// docs/adopters/reference/uxp.md.
package uxp

//go:generate go run ./internal/parityreadme -update -readme ../../../docs/adopters/reference/uxp.md
