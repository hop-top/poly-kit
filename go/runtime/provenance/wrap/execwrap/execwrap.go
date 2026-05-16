// Package execwrap wraps os/exec so subprocess invocations record a
// Provenance entry against the JSON-pointer path of the output field
// they populate. The recorded URL is "exec://<argv0>"; Version is a
// short SHA of the argv slice so concurrent invocations of the same
// binary with different args are distinguishable.
package execwrap

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os/exec"
	"strings"
	"time"

	"hop.top/kit/go/runtime/provenance"
)

// Exec is the wrapper handle. Stateless aside from the Mode it inherits
// from CurrentModeFromContext at call time.
type Exec struct{}

// New returns a ready Exec.
func New() *Exec { return &Exec{} }

// Output runs cmd and records a Provenance entry against path. Returns
// the combined output, the Provenance, and any error from exec.
func (e *Exec) Output(ctx context.Context, path string, cmd *exec.Cmd) ([]byte, provenance.Provenance, error) {
	out, err := cmd.Output()
	prov := e.stamp(ctx, path, cmd)
	return out, prov, err
}

// Run runs cmd to completion and records a Provenance entry against
// path. Returns the Provenance and any exec error.
func (e *Exec) Run(ctx context.Context, path string, cmd *exec.Cmd) (provenance.Provenance, error) {
	err := cmd.Run()
	return e.stamp(ctx, path, cmd), err
}

func (e *Exec) stamp(ctx context.Context, path string, cmd *exec.Cmd) provenance.Provenance {
	if provenance.CurrentModeFromContext(ctx) == provenance.ModeOff {
		return provenance.Provenance{}
	}
	argv0 := cmd.Path
	if len(cmd.Args) > 0 {
		argv0 = cmd.Args[0]
	}
	prov := provenance.Provenance{
		SchemaVersion: provenance.SchemaVersion,
		Source:        provenance.SourceAuthoritative,
		URL:           "exec://" + argv0,
		FetchedAt:     time.Now().UTC(),
		Version:       hashArgs(cmd.Args),
	}
	tr := provenance.Track(ctx)
	if path != "" {
		_ = tr.Authoritative(path, prov)
	}
	return prov
}

// hashArgs returns the first 12 hex chars of the SHA-256 of the argv
// slice (joined with NULs to disambiguate "a b" from "ab"). Short
// enough to fit in a Version slot, long enough to discriminate
// re-runs.
func hashArgs(args []string) string {
	h := sha256.New()
	h.Write([]byte(strings.Join(args, "\x00")))
	sum := h.Sum(nil)
	return hex.EncodeToString(sum)[:12]
}
