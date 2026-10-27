// Package serving lets an option that puts a Root in front of other
// processes without the serve plane — the peer mesh — count as a serve
// option for the kit token command, without linking the plane.
//
// The option calls Mark. When go/console/cli/service is linked it sets
// Mount, and the marker runs it once every option of cli.New has run,
// before the snapshot of reserved subcommands, so what it mounts stays
// kit-reserved. A binary that does not link the plane runs nothing.
package serving

import "hop.top/kit/go/console/cli"

// Mount completes the kit token command on a marked Root with the
// serve plane's leaves; nil until go/console/cli/service is linked.
var Mount func(r *cli.Root)

type marker struct{}

var key = cli.NewExtensionKey[*marker]("serving")

var _ cli.OptionsHook = (*marker)(nil)

// OptionsApplied runs Mount on the marked Root.
func (*marker) OptionsApplied(r *cli.Root) {
	if Mount != nil {
		Mount(r)
	}
}

// Mark records that an option made r reachable by other processes.
// It is idempotent.
func Mark(r *cli.Root) {
	cli.Extend(r, key, func(*cli.Root) *marker { return &marker{} })
}

// Marked reports whether an option called Mark on r.
func Marked(r *cli.Root) bool {
	_, ok := cli.Extension(r, key)
	return ok
}
