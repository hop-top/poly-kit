// Command rootprobe is the smallest program that builds a cli.Root and
// executes it.
//
// The vulnerability-scan workflow builds it and runs govulncheck in
// binary mode over the result. A standard library or dependency flaw
// that every kit CLI links then fails the build even when no kit source
// path calls it, which the source-mode scan cannot see.
//
// It is not shipped: it sits under testdata, which the go tool leaves
// out of ./... patterns, and no release build names it. Keep it to a
// bare root: more commands or options link more code than the
// smallest adopter does.
//
// Build:
//
//	go build -buildvcs=false -o rootprobe ./go/console/cli/testdata/rootprobe
package main

import (
	"context"
	"os"

	"hop.top/kit/go/console/cli"
)

func main() {
	// WithStatus adds the reserved status verb every kit CLI must carry;
	// without it Execute rejects a root that has no commands at all.
	root := cli.New(cli.Config{
		Name:    "rootprobe",
		Version: "0.0.0",
		Short:   "Smallest kit CLI, built to be scanned",
	}, cli.WithStatus(cli.StatusConfig{}))
	if err := root.Execute(context.Background()); err != nil {
		os.Exit(1)
	}
}
