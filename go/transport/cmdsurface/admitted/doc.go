// Package admitted is the part of an admitted invocation that the
// command running it can read without linking the bridge: the surface
// names, the caller the transport established, the scope an
// --idempotency-key is confined to, and the flag state each
// invocation starts from.
//
// It imports only the standard library, pflag and a module-internal
// context key, so [hop.top/kit/go/console/cli] can read the caller and
// scope a key in its RunE middleware while a CLI that serves nothing
// links none of [hop.top/kit/go/transport/cmdsurface]. cmdsurface
// declares every name here under its existing spelling
// (cmdsurface.Surface and cmdsurface.Establishment are aliases of the
// types below), and its bridge is the only writer of the caller
// [CallerFrom] reads.
//
// It is not the served example (examples/served), not the "served
// commands" guides and reference pages, and not the
// go/console/cli/service package that runs the services: it is the
// smallest piece of that feature, the one a command reads.
package admitted
