package sideeffect

import "context"

// dryRunCtxKey is a private key type for storing the dry-run flag
// on a context. The unexported type guarantees no other package can
// shadow or collide with the value.
type dryRunCtxKey struct{}

// WithDryRun returns a copy of ctx carrying the dry-run flag. The
// kit cli wrapper installs this on the root context when the
// --dry-run global flag is set.
//
// Pass v=true to mark the context as dry-run; v=false explicitly
// clears any inherited dry-run flag (rare; useful when re-entering
// real-effect code from inside a dry-run scope).
func WithDryRun(ctx context.Context, v bool) context.Context {
	return context.WithValue(ctx, dryRunCtxKey{}, v)
}

// IsDryRun reports whether ctx was tagged dry-run via WithDryRun.
// The helper is package-level (rather than a method on a Root or
// cli value) so library code can branch on dry-run without taking
// a cli dependency.
//
// nil ctx returns false. A context that has not had WithDryRun
// applied also returns false.
func IsDryRun(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	v, ok := ctx.Value(dryRunCtxKey{}).(bool)
	return ok && v
}
