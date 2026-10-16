package cli

import "github.com/spf13/cobra"

// Extensions.
//
// A package outside cli adds a plane to a Root — the serve plane in
// go/console/cli/service is one — without cli importing it, so a CLI
// that does not use the plane links none of its dependencies. The
// package declares one ExtensionKey, attaches its per-Root state with
// Extend from its options, and receives the Root's lifecycle through
// whichever hook interfaces that state implements: OptionsHook,
// InitHook, PrepareHook, PolicyExemption. cli calls each hook in
// attach order.
//
// Extend and Extension are package functions rather than Root methods
// on purpose: Go has no generic methods, and the linker keeps every
// exported method of a type reachable through reflection.

// ExtensionKey names one kind of per-Root extension. Declare it once,
// as a package-level variable: its address is its identity, so two
// packages never collide whatever they call themselves.
type ExtensionKey[T any] struct{ name string }

// NewExtensionKey returns a key for extensions of type T; name appears
// only in diagnostics.
func NewExtensionKey[T any](name string) *ExtensionKey[T] {
	return &ExtensionKey[T]{name: name}
}

// String returns the key's name.
func (k *ExtensionKey[T]) String() string { return k.name }

// extEntry is one attached extension: the key that names it and the
// value Extend created.
type extEntry struct {
	key any // *ExtensionKey[T]
	val any // T
}

// Extend returns r's extension for key, calling create and attaching
// the result the first time. Call it from an option or before Execute;
// like an option, it is not safe concurrently with Execute or with a
// served invocation. It works on a Root not built by New (a literal
// &cli.Root{Cmd: c}), where no hook runs until Prepare or Execute.
func Extend[T any](r *Root, key *ExtensionKey[T], create func(*Root) T) T {
	if v, ok := Extension(r, key); ok {
		return v
	}
	v := create(r)
	r.exts = append(r.exts, extEntry{key: key, val: v})
	return v
}

// Extension returns r's extension for key and whether one is attached,
// without attaching one. A nil r has none.
func Extension[T any](r *Root, key *ExtensionKey[T]) (T, bool) {
	var zero T
	if r == nil {
		return zero, false
	}
	for _, e := range r.exts {
		if e.key == any(key) {
			return e.val.(T), true
		}
	}
	return zero, false
}

// OptionsHook is called by New once every option has run, before New
// takes the snapshot of reserved subcommands, so a command it mounts
// is kit-reserved. It is not called for an extension attached after
// New returned.
type OptionsHook interface {
	OptionsApplied(r *Root)
}

// InitHook is called by New after the identity keypair is resolved
// (WithIdentity). An error does not fail New: PersistentPreRunE returns
// it after the chdir hook and after an identity error, so --help,
// --version and completion still work on a misconfigured tool.
type InitHook interface {
	Init(r *Root) error
}

// PrepareHook is called from Execute and Prepare after kit's own
// argv-independent steps and before validation. It must be
// idempotent: both may run, and Execute runs it on every call.
type PrepareHook interface {
	PrepareTree(r *Root)
}

// PolicyExemption reports whether the delegation policy (--policy)
// skips its authorization of cmd, because cmd's --policy names the
// policy cmd serves to others rather than a gate on running cmd.
// owns=false leaves the decision to the next extension and finally to
// kit's default, which exempts a depth-1 `serve` by name.
type PolicyExemption interface {
	ExemptFromPolicy(r *Root, cmd *cobra.Command) (exempt, owns bool)
}

// extHooks calls fn for each attached extension implementing H, in
// attach order.
func extHooks[H any](r *Root, fn func(H)) {
	if r == nil {
		return
	}
	for _, e := range r.exts {
		if h, ok := e.val.(H); ok {
			fn(h)
		}
	}
}
