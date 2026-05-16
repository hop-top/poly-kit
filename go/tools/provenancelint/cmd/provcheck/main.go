// Command provcheck is the standalone driver for the kit/provenance
// vet-style lint. Wire via:
//
//	go install hop.top/kit/go/tools/provenancelint/cmd/provcheck
//	go vet -vettool=$(go env GOPATH)/bin/provcheck ./...
//
// Or invoke directly:
//
//	provcheck ./...
package main

import (
	"golang.org/x/tools/go/analysis/singlechecker"

	"hop.top/kit/go/tools/provenancelint"
)

func main() {
	singlechecker.Main(provenancelint.Analyzer)
}
