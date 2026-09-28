package policy

// KitDefaultName is the name of the policy kit ships, reserved: a
// --policy of this name is always [KitDefault], never a file.
const KitDefaultName = "kit-default"

// KitDefault returns the policy kit ships, the one a served surface
// beyond loopback enforces when no --policy is named:
//
//	name: kit-default
//	unannotated: write            # a command declaring no side effect is a write
//	allow:                        # a caller nobody established reads only
//	  write: []
//	  destructive: []
//	callers:
//	  - allow:                    # an established principal reads and writes
//	      write: ["*"]
//	      destructive: ["*"]      # ... and runs a destructive command
//	    require_declared_scope:   # only one declaring kit/permissions,
//	      [destructive]           # whose scopes it must hold
//
// A destructive command also keeps its own confirmation — the confirm
// flag, a typed token, the surface's confirmation for
// kit/requires-confirmation — and the bridge's destructive ceiling,
// which the policy never waives. Every refusal names the policy and
// how to get past it.
func KitDefault() Policy {
	return Policy{
		Name:        KitDefaultName,
		Unannotated: SideEffectWrite,
		Allow: map[SideEffect][]string{
			SideEffectWrite:       {},
			SideEffectDestructive: {},
		},
		Callers: []CallerRule{{
			Allow: map[SideEffect][]string{
				SideEffectWrite:       {"*"},
				SideEffectDestructive: {"*"},
			},
			RequireDeclaredScope: []SideEffect{SideEffectDestructive},
		}},
		Remedy: "kit's default beyond loopback: writes need an authenticated caller, " +
			"destructive commands a declared kit/permissions scope; name a --policy to choose otherwise",
	}
}
