package cmdreflect

import (
	"testing"

	"github.com/spf13/cobra"

	"hop.top/kit/go/ai/toolspec"
)

// TestReflectReasons is the reason table. Every value in
// AllReasons must appear in the want column at least once — the
// coverage check at the bottom enforces that, so adding a reason
// without a case here fails the suite.
func TestReflectReasons(t *testing.T) {
	// serve is reserved here on purpose: the row below pins that
	// self-hosting outranks management-only for the one command
	// that is both.
	tree := Reflect(fixtureRoot(), WithReserved(fakeReserved{"mgmt": true, "serve": true}))

	tests := []struct {
		name      string
		path      string
		invocable bool
		want      NonInvocableReason
	}{
		{"plain read leaf is invocable", "list", true, ReasonNone},
		{"write leaf is invocable", "add", true, ReasonNone},
		{"annotated destructive stays invocable by default", "purge-store", true, ReasonNone},
		{"heuristic destructive stays invocable by default", "drop", true, ReasonNone},
		{"child of a group is invocable", "widget rename", true, ReasonNone},

		{"command group has no action of its own", "widget", false, ReasonNotRunnable},
		{"help is a framework built-in", "help", false, ReasonBuiltin},
		{"completion tree is a framework built-in", "completion bash", false, ReasonBuiltin},
		{"hidden is withheld", "internal-dump", false, ReasonHiddenInternal},
		{"cobra deprecation is withheld", "old-list", false, ReasonDeprecated},
		{"annotated deprecation is withheld", "sunset", false, ReasonDeprecated},
		{"interactive needs a terminal", "shell", false, ReasonInteractive},
		{"unresolvable side-effect is a defect", "typo", false, ReasonMalformedSchema},
		{"invalid output schema is a defect", "badschema", false, ReasonMalformedSchema},
		{"spec subcommand is management-only", "spec", false, ReasonManagementOnly},
		{"child of a reserved verb is management-only", "mgmt status", false, ReasonManagementOnly},
		{"serve is self-hosting by position", "serve", false, ReasonSelfHosting},
		{"child of serve is self-hosting", "serve api", false, ReasonSelfHosting},
		{"nested serve is self-hosting by name at any depth", "svc serve", false, ReasonSelfHosting},
		{"ingress listener is self-hosting by network class", "listen", false, ReasonSelfHosting},
		{"kit/self-hosting marks a self-modifying command", "upgrade", false, ReasonSelfHosting},

		// Precedence: hidden outranks deprecated and self-hosting;
		// self-hosting outranks management-only (serve is reserved
		// above).
		{"hidden outranks deprecated", "hidden-and-old", false, ReasonHiddenInternal},
		{"hidden outranks self-hosting", "serve internal", false, ReasonHiddenInternal},
	}

	seen := map[NonInvocableReason]bool{}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			d := tree.Lookup(tc.path)
			if d == nil {
				t.Fatalf("no descriptor at %q", tc.path)
			}
			if d.Invocable != tc.invocable {
				t.Errorf("Invocable = %v, want %v (reason %q)",
					d.Invocable, tc.invocable, d.Reason)
			}
			if d.Reason != tc.want {
				t.Errorf("Reason = %q, want %q", d.Reason, tc.want)
			}
			if !d.Reason.IsValid() {
				t.Errorf("Reason %q is not in the defined set", d.Reason)
			}
			if d.Invocable && d.Reason != ReasonNone {
				t.Errorf("invocable descriptor carries reason %q", d.Reason)
			}
			if !d.Invocable && d.Reason == ReasonNone {
				t.Error("non-invocable descriptor carries no reason")
			}
		})
		seen[tc.want] = true
	}

	// The unauthorized-destructive reason needs a denying config,
	// so it gets its own tree rather than a row above.
	denied := Reflect(fixtureRoot(), DenyDestructive())
	for _, path := range []string{"purge-store", "drop"} {
		d := denied.Lookup(path)
		if d == nil {
			t.Fatalf("no descriptor at %q", path)
		}
		if d.Invocable {
			t.Errorf("%s: Invocable = true under DenyDestructive", path)
		}
		if d.Reason != ReasonUnauthorizedDestructive {
			t.Errorf("%s: Reason = %q, want %q",
				path, d.Reason, ReasonUnauthorizedDestructive)
		}
	}
	seen[ReasonUnauthorizedDestructive] = true

	for _, r := range AllReasons() {
		if !seen[r] {
			t.Errorf("reason %q has no case in the table", r)
		}
	}
}

// TestReflectDropsNothing is the core invariant: every command in
// the source tree gets a descriptor. A walker that silently skips
// is the defect this package exists to remove.
func TestReflectDropsNothing(t *testing.T) {
	root := fixtureRoot()
	tree := Reflect(root)

	var count int
	var walk func(c *cobra.Command)
	walk = func(c *cobra.Command) {
		count++
		for _, ch := range c.Commands() {
			walk(ch)
		}
	}
	walk(root)

	if tree.Len() != count {
		t.Fatalf("reflected %d descriptors for %d commands; "+
			"a command was dropped", tree.Len(), count)
	}
	if got := len(tree.Invocable()) + len(tree.NonInvocable()); got != count {
		t.Fatalf("Invocable+NonInvocable = %d, want %d", got, count)
	}
	if tree.Root == nil || tree.Root.Use != "fix" {
		t.Fatalf("Root = %+v, want the fixture root", tree.Root)
	}
}

func TestReflectNilRoot(t *testing.T) {
	tree := Reflect(nil)
	if tree == nil {
		t.Fatal("Reflect(nil) returned nil; want an empty tree")
	}
	if tree.Len() != 0 || len(tree.Invocable()) != 0 {
		t.Fatalf("Reflect(nil) is not empty: %d descriptors", tree.Len())
	}
	if tree.Lookup("anything") != nil {
		t.Error("Lookup on an empty tree returned a descriptor")
	}
}

// TestAllowOptions covers the surface-specific relaxations. Each
// option lifts exactly one exclusion and leaves the others in
// place.
func TestAllowOptions(t *testing.T) {
	tests := []struct {
		name string
		opt  Option
		path string
	}{
		{"AllowHidden lifts hidden-internal", AllowHidden(), "internal-dump"},
		{"AllowDeprecated lifts deprecated", AllowDeprecated(), "old-list"},
		{"AllowInteractive lifts interactive", AllowInteractive(), "shell"},
		{"AllowReserved lifts management-only", AllowReserved(), "spec"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			base := Reflect(fixtureRoot()).Lookup(tc.path)
			if base == nil {
				t.Fatalf("no descriptor at %q; the walker dropped it", tc.path)
			}
			if base.Invocable {
				t.Fatalf("%s is already invocable without the option", tc.path)
			}
			got := Reflect(fixtureRoot(), tc.opt).Lookup(tc.path)
			if got == nil {
				t.Fatalf("no descriptor at %q under the option", tc.path)
			}
			if !got.Invocable {
				t.Errorf("%s stayed non-invocable (%q) with the option",
					tc.path, got.Reason)
			}
		})
	}
}

// TestReflectSkipsNamelessCommands pins that a command with no name
// is not described: cobra mounts one when a tool disables its default
// help command with SetHelpCommand(&cobra.Command{Hidden: true}), and
// a descriptor with an empty path segment would reach every projected
// surface as a command nobody can address.
func TestReflectSkipsNamelessCommands(t *testing.T) {
	root := &cobra.Command{Use: "fix", Short: "fixture root"}
	root.AddCommand(&cobra.Command{
		Use:         "list",
		Short:       "list things",
		Run:         func(*cobra.Command, []string) {},
		Annotations: map[string]string{annSideEffect: "read"},
	})
	root.SetHelpCommand(&cobra.Command{Hidden: true})
	root.InitDefaultHelpCmd()
	root.AddCommand(&cobra.Command{Hidden: true})

	tree := Reflect(root)
	for _, d := range tree.Descriptors {
		for _, seg := range d.Path {
			if seg == "" {
				t.Errorf("descriptor %q has an empty path segment", d.Path)
			}
		}
	}
	if d := tree.Lookup("list"); d == nil || !d.Invocable {
		t.Errorf("list must still be described and invocable")
	}
}

// TestSelfHostingSurvivesEveryRelaxation pins that no option lifts
// self-hosting: a consumer reflecting with every relaxation it has
// still cannot project the server, the listener, or the upgrader.
func TestSelfHostingSurvivesEveryRelaxation(t *testing.T) {
	tree := Reflect(fixtureRoot(),
		WithReserved(fakeReserved{"serve": true}),
		AllowHidden(), AllowDeprecated(), AllowInteractive(), AllowReserved(),
	)
	for _, path := range []string{"serve", "serve api", "svc serve", "listen", "upgrade"} {
		d := tree.Lookup(path)
		if d == nil {
			t.Fatalf("no descriptor at %q", path)
		}
		if !d.Surface.SelfHosting {
			t.Errorf("%s: Surface.SelfHosting = false", path)
		}
		if d.Invocable {
			t.Errorf("%s: Invocable = true under every relaxation", path)
		}
		if d.Reason != ReasonSelfHosting {
			t.Errorf("%s: Reason = %q, want %q", path, d.Reason, ReasonSelfHosting)
		}
	}
	for _, path := range []string{"list", "shell", "spec", "mgmt status"} {
		if d := tree.Lookup(path); d == nil || d.Surface.SelfHosting {
			t.Errorf("%s: reported self-hosting", path)
		}
	}
}

// TestDescribe pins that the per-command entry point records the
// same facts and verdict the tree walk does, so a consumer holding
// one resolved command gets an answer consistent with discovery.
func TestDescribe(t *testing.T) {
	root := fixtureRoot()
	tree := Reflect(root, WithReserved(fakeReserved{"mgmt": true}))

	for _, path := range []string{"list", "shell", "serve api", "listen", "mgmt status", "widget rename"} {
		want := tree.Lookup(path)
		if want == nil {
			t.Fatalf("no descriptor at %q", path)
		}
		got := Describe(root, want.Cmd, WithReserved(fakeReserved{"mgmt": true}))
		if got == nil {
			t.Fatalf("Describe(%q) = nil", path)
		}
		if got.PathKey() != want.PathKey() {
			t.Errorf("%s: PathKey = %q, want %q", path, got.PathKey(), want.PathKey())
		}
		if got.Invocable != want.Invocable || got.Reason != want.Reason {
			t.Errorf("%s: verdict = (%v, %q), want (%v, %q)",
				path, got.Invocable, got.Reason, want.Invocable, want.Reason)
		}
		if got.Safety.Tier != want.Safety.Tier || got.Surface.SelfHosting != want.Surface.SelfHosting {
			t.Errorf("%s: Safety/Surface diverge from the tree walk", path)
		}
		if got.Output.Schema == nil != (want.Output.Schema == nil) {
			t.Errorf("%s: Output.Schema presence diverges from the tree walk", path)
		}
	}

	// A nil root anchors on the command's own root.
	leaf := tree.Lookup("serve api").Cmd
	if d := Describe(nil, leaf); d == nil || d.PathKey() != "serve api" || d.Reason != ReasonSelfHosting {
		t.Errorf("Describe(nil, serve api) = %+v", d)
	}
	if Describe(root, nil) != nil {
		t.Error("Describe of a nil command returned a descriptor")
	}
}

// TestDenyDestructiveFunc covers selective denial: the callback
// sees a fully-resolved descriptor and may deny only some.
func TestDenyDestructiveFunc(t *testing.T) {
	tree := Reflect(fixtureRoot(), DenyDestructiveFunc(func(d *Descriptor) bool {
		return d.Safety.Tier == TierDestructiveShared && !d.Safety.TierInferred
	}))

	if d := tree.Lookup("purge-store"); d == nil {
		t.Fatal("no descriptor at purge-store")
	} else if d.Invocable {
		t.Error("annotated destructive was not denied")
	} else if d.Reason != ReasonUnauthorizedDestructive {
		t.Errorf("Reason = %q, want %q", d.Reason, ReasonUnauthorizedDestructive)
	}
	if d := tree.Lookup("drop"); d == nil {
		t.Fatal("no descriptor at drop")
	} else if !d.Invocable {
		t.Errorf("heuristic destructive was denied (%q); "+
			"the callback excluded it", d.Reason)
	}
}

// TestReasonExplain checks every reason carries a rationale, so a
// consumer rendering a "why not" column never prints an empty cell.
func TestReasonExplain(t *testing.T) {
	for _, r := range AllReasons() {
		if r.Explain() == "" {
			t.Errorf("reason %q has no explanation", r)
		}
	}
	if ReasonNone.Explain() != "" {
		t.Error("ReasonNone should have no explanation")
	}
	if !ReasonNone.IsValid() {
		t.Error("ReasonNone should be valid")
	}
	if NonInvocableReason("invented").IsValid() {
		t.Error("an undefined reason reported itself valid")
	}
}

// TestPathAndKey covers the path shape both consumers depend on:
// toolspec wants the root segment included, cmdsurface wants it
// stripped.
func TestPathAndKey(t *testing.T) {
	tree := Reflect(fixtureRoot())

	d := tree.Lookup("widget rename")
	if got, want := len(d.Path), 3; got != want {
		t.Fatalf("Path = %v, want %d segments", d.Path, want)
	}
	if d.Path[0] != "fix" {
		t.Errorf("Path[0] = %q, want the root segment", d.Path[0])
	}
	if d.PathKey() != "widget rename" {
		t.Errorf("PathKey = %q, want %q", d.PathKey(), "widget rename")
	}
	if !tree.Root.IsRoot() || tree.Root.PathKey() != "" {
		t.Errorf("root PathKey = %q, want empty", tree.Root.PathKey())
	}
	if d.IsRoot() {
		t.Error("a leaf reported IsRoot")
	}
}

// TestSafetyMapping is the tier → level/permission table. It pins
// the mapping rather than the current output: each row states the
// rule from the ladder.
func TestSafetyMapping(t *testing.T) {
	tests := []struct {
		raw   string
		tier  Tier
		level toolspec.SafetyLevel
		fs    toolspec.Permission
	}{
		{"read", TierRead, toolspec.SafetyLevelSafe, toolspec.PermFSRead},
		{"write-local", TierWriteLocal, toolspec.SafetyLevelCaution, toolspec.PermFSWriteLocal},
		{"write-shared", TierWriteShared, toolspec.SafetyLevelCaution, toolspec.PermFSWriteShared},
		{"destructive-local", TierDestructiveLocal, toolspec.SafetyLevelDangerous, toolspec.PermFSDestructiveLocal},
		{"destructive-shared", TierDestructiveShared, toolspec.SafetyLevelDangerous, toolspec.PermFSDestructiveShared},
		{"interactive", TierInteractive, toolspec.SafetyLevelCaution, toolspec.PermFSRead},
		// Legacy values resolve conservatively to the shared band.
		{"write", TierWriteShared, toolspec.SafetyLevelCaution, toolspec.PermFSWriteShared},
		{"destructive", TierDestructiveShared, toolspec.SafetyLevelDangerous, toolspec.PermFSDestructiveShared},
	}
	for _, tc := range tests {
		t.Run(tc.raw, func(t *testing.T) {
			if got := resolveTier(tc.raw); got != tc.tier {
				t.Fatalf("resolveTier(%q) = %q, want %q", tc.raw, got, tc.tier)
			}
			if got := safetyLevel(tc.tier); got != tc.level {
				t.Errorf("safetyLevel(%q) = %q, want %q", tc.tier, got, tc.level)
			}
			if got := fsPermission(tc.tier); got != tc.fs {
				t.Errorf("fsPermission(%q) = %q, want %q", tc.tier, got, tc.fs)
			}
		})
	}
	if got := resolveTier("nonsense"); got != TierUnknown {
		t.Errorf("resolveTier of an unknown value = %q, want unknown", got)
	}
}

// TestNetworkMapping pins the kit/network axis.
func TestNetworkMapping(t *testing.T) {
	tests := map[string]toolspec.Permission{
		"":               toolspec.PermNetworkNone,
		"none":           toolspec.PermNetworkNone,
		"egress:public":  toolspec.PermNetworkEgressPublic,
		"egress:private": toolspec.PermNetworkEgressPrivate,
		"ingress":        toolspec.PermNetworkIngress,
		"garbage":        toolspec.PermNetworkNone,
	}
	for raw, want := range tests {
		if got := resolveNetwork(raw); got != want {
			t.Errorf("resolveNetwork(%q) = %q, want %q", raw, got, want)
		}
	}
}

// TestSafetyResolution covers the per-command safety projection on
// the fixture tree, including the heuristic and the permission set.
func TestSafetyResolution(t *testing.T) {
	tree := Reflect(fixtureRoot())

	t.Run("read leaf", func(t *testing.T) {
		s := tree.Lookup("list").Safety
		if s.Tier != TierRead || s.TierInferred {
			t.Errorf("Tier = %q inferred=%v, want read declared", s.Tier, s.TierInferred)
		}
		if s.RequiresConfirmation {
			t.Error("a read command requires confirmation")
		}
		if s.Idempotent != "yes" {
			t.Errorf("Idempotent = %q, want yes", s.Idempotent)
		}
		want := []string{"OK", "NOT_FOUND"}
		if len(s.ExitCodes) != len(want) {
			t.Fatalf("ExitCodes = %v, want %v", s.ExitCodes, want)
		}
		for i := range want {
			if s.ExitCodes[i] != want[i] {
				t.Errorf("ExitCodes[%d] = %q, want %q", i, s.ExitCodes[i], want[i])
			}
		}
	})

	t.Run("write leaf permissions", func(t *testing.T) {
		s := tree.Lookup("add").Safety
		if s.Tier != TierWriteLocal {
			t.Fatalf("Tier = %q, want write-local", s.Tier)
		}
		got := s.PermissionStrings()
		want := []string{
			toolspec.PermFSWriteLocal.String(),
			toolspec.PermNetworkEgressPublic.String(),
			toolspec.PermExecSubprocess.String(),
		}
		if len(got) != len(want) {
			t.Fatalf("Permissions = %v, want %v", got, want)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Errorf("Permissions[%d] = %q, want %q", i, got[i], want[i])
			}
		}
		if s.RequiresConfirmation {
			t.Error("public egress alone should not force confirmation")
		}
	})

	t.Run("destructive leaf", func(t *testing.T) {
		s := tree.Lookup("purge-store").Safety
		if !s.Destructive() {
			t.Error("Destructive() = false for destructive-shared")
		}
		if !s.RequiresConfirmation {
			t.Error("a destructive command does not require confirmation")
		}
		if !s.AuthRequired {
			t.Error("kit/auth-required was not read")
		}
		if !s.DestructiveTokenRequired {
			t.Error("kit/destructive-token was not read")
		}
		perms := s.PermissionStrings()
		if perms[0] != toolspec.PermFSDestructiveShared.String() {
			t.Errorf("fs permission = %q", perms[0])
		}
		if perms[len(perms)-1] != toolspec.PermBusPublish.String() {
			t.Errorf("bus permission missing: %v", perms)
		}
	})

	t.Run("destructive-name heuristic", func(t *testing.T) {
		s := tree.Lookup("drop").Safety
		if s.DeclaredSideEffect != "" {
			t.Fatalf("fixture declared a side effect: %q", s.DeclaredSideEffect)
		}
		if !s.TierInferred {
			t.Error("TierInferred = false; the heuristic did not fire")
		}
		if s.Tier != TierDestructiveShared {
			t.Errorf("Tier = %q, want destructive-shared", s.Tier)
		}
		if !s.RequiresConfirmation {
			t.Error("the heuristic did not force confirmation")
		}
	})

	t.Run("declared value is preserved verbatim", func(t *testing.T) {
		if got := tree.Lookup("purge-store").Safety.DeclaredSideEffect; got != "destructive-shared" {
			t.Errorf("DeclaredSideEffect = %q", got)
		}
	})
}

// TestFlagReflection covers flag extraction: type, default,
// required, hidden, deprecated, and since-version.
func TestFlagReflection(t *testing.T) {
	tree := Reflect(fixtureRoot())
	flags := map[string]Flag{}
	for _, f := range tree.Lookup("list").Flags {
		flags[f.Name] = f
	}

	t.Run("required with shorthand and default", func(t *testing.T) {
		f, ok := flags["filter"]
		if !ok {
			t.Fatal("filter flag was not reflected")
		}
		if f.Short != "f" || f.Type != "string" || f.Default != "none" {
			t.Errorf("got %+v", f)
		}
		if !f.Required {
			t.Error("Required = false for a MarkFlagRequired flag")
		}
	})

	t.Run("typed default", func(t *testing.T) {
		if f := flags["limit"]; f.Type != "int" || f.Default != "10" {
			t.Errorf("limit = %+v, want int/10", f)
		}
	})

	t.Run("deprecated flags are reflected", func(t *testing.T) {
		f, ok := flags["legacy"]
		if !ok {
			t.Fatal("deprecated flag was dropped")
		}
		if !f.Deprecated {
			t.Error("Deprecated = false")
		}
	})

	t.Run("hidden flags are reflected, not dropped", func(t *testing.T) {
		f, ok := flags["secret"]
		if !ok {
			t.Fatal("hidden flag was dropped; the walker must record it")
		}
		if !f.Hidden {
			t.Error("Hidden = false")
		}
		if f.Deprecated {
			t.Error("a hidden non-deprecated flag reported Deprecated")
		}
	})

	t.Run("flag-since is threaded through", func(t *testing.T) {
		for _, f := range tree.Lookup("add").Flags {
			if f.Name == "force" {
				if f.SinceVersion != "1.2" {
					t.Errorf("SinceVersion = %q, want 1.2", f.SinceVersion)
				}
				return
			}
		}
		t.Fatal("force flag was not reflected")
	})

	t.Run("root persistent flags land in GlobalFlags", func(t *testing.T) {
		names := map[string]bool{}
		for _, f := range tree.GlobalFlags {
			names[f.Name] = true
		}
		for _, want := range []string{"config", "verbose"} {
			if !names[want] {
				t.Errorf("GlobalFlags missing %q: %v", want, names)
			}
		}
	})
}

// TestArgReflection covers the kit/args parse, including the
// optional marker.
func TestArgReflection(t *testing.T) {
	tree := Reflect(fixtureRoot())

	args := tree.Lookup("add").Args
	if len(args) != 2 {
		t.Fatalf("Args = %+v, want 2", args)
	}
	if args[0].Name != "name" || !args[0].Required {
		t.Errorf("Args[0] = %+v, want required name", args[0])
	}
	if args[1].Name != "description" || args[1].Required {
		t.Errorf("Args[1] = %+v, want optional description", args[1])
	}
	if got := tree.Lookup("list").Args; got != nil {
		t.Errorf("a command with no kit/args got %+v", got)
	}
}

// TestSurfaceMetadata covers the presentation fields.
func TestSurfaceMetadata(t *testing.T) {
	tree := Reflect(fixtureRoot(), WithReserved(fakeReserved{"mgmt": true}))

	t.Run("deprecation detail", func(t *testing.T) {
		m := tree.Lookup("sunset").Surface
		if !m.Deprecated {
			t.Fatal("Deprecated = false")
		}
		if m.DeprecatedSince != "1.4" {
			t.Errorf("DeprecatedSince = %q, want 1.4", m.DeprecatedSince)
		}
		if m.RemovalTarget != "2.0" {
			t.Errorf("RemovalTarget = %q, want 2.0", m.RemovalTarget)
		}
		if m.ReplacedBy != "list" {
			t.Errorf("ReplacedBy = %q, want list", m.ReplacedBy)
		}
	})

	t.Run("cobra deprecation string becomes the since value", func(t *testing.T) {
		m := tree.Lookup("old-list").Surface
		if !m.Deprecated || m.DeprecatedSince != "use list" {
			t.Errorf("got %+v", m)
		}
	})

	t.Run("group node", func(t *testing.T) {
		m := tree.Lookup("widget").Surface
		if m.Runnable {
			t.Error("a group reported Runnable")
		}
		if !m.HasSubCommands {
			t.Error("a group reported no subcommands")
		}
	})

	t.Run("reserved lookup drives Reserved", func(t *testing.T) {
		if !tree.Lookup("mgmt status").Surface.Reserved {
			t.Error("a child under a reserved verb is not Reserved")
		}
		if tree.Lookup("list").Surface.Reserved {
			t.Error("an unreserved verb reported Reserved")
		}
	})

	t.Run("no reserved lookup means nothing is reserved", func(t *testing.T) {
		bare := Reflect(fixtureRoot())
		if bare.Lookup("mgmt status").Surface.Reserved {
			t.Error("Reserved was set without a lookup")
		}
	})

	t.Run("builtin flag", func(t *testing.T) {
		if !tree.Lookup("help").Surface.Builtin {
			t.Error("help is not marked Builtin")
		}
		if tree.Lookup("list").Surface.Builtin {
			t.Error("an adopter command is marked Builtin")
		}
	})

	t.Run("self-hosting does not need the reserved lookup", func(t *testing.T) {
		bare := Reflect(fixtureRoot())
		for _, path := range []string{"serve", "serve api", "listen", "upgrade"} {
			if !bare.Lookup(path).Surface.SelfHosting {
				t.Errorf("%s: SelfHosting = false on a bare reflection", path)
			}
		}
		if bare.Lookup("add").Surface.SelfHosting {
			t.Error("a plain write command reported SelfHosting")
		}
	})
}

// TestOutputReflection covers the schema and guidance projection.
func TestOutputReflection(t *testing.T) {
	tree := Reflect(fixtureRoot())

	o := tree.Lookup("badschema").Output
	if !o.SchemaMalformed {
		t.Error("SchemaMalformed = false for invalid JSON")
	}
	if len(o.Schema) == 0 {
		t.Error("the malformed schema bytes were discarded; " +
			"a diagnostic cannot show what was written")
	}
	if o.SchemaVersion != "1.0" {
		t.Errorf("SchemaVersion = %q, want 1.0", o.SchemaVersion)
	}
	if tree.Lookup("list").Output.SchemaMalformed {
		t.Error("a command with no schema reported one malformed")
	}
}
