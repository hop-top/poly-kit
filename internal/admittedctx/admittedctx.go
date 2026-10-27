// Package admittedctx holds the context key under which
// [hop.top/kit/go/transport/cmdsurface] stamps the caller of the
// invocation it admitted, and nothing else.
//
// The bridge writes the value; [hop.top/kit/go/transport/cmdsurface/admitted]
// reads it with CallerFrom. Keeping the key here, rather than in the
// public leaf, means no code outside this module can depend on a
// setter, so the bridge stays free to change what it stamps. It is API
// hygiene, not a security boundary: an adopter can still produce an
// admitted context through public API (Bridge.Invoke on the library
// surface stamps whatever Meta it is handed). Inside the module only
// cmdsurface writes the key; a guard test holds every other importer
// to _test.go files.
package admittedctx

// CallerKey is the context key for the admitted caller. The value
// stored under it is an admitted.Caller.
type CallerKey struct{}
