package cmdsurface

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"hop.top/kit/go/transport/api"
)

// withholdTree is a root with one leaf withheld from the served
// surfaces ("follow"), one withheld from the socket as well
// ("tail"), one carrying a value that does not parse ("typo"), and
// one plain leaf ("ping").
func withholdTree() *cobra.Command {
	root := &cobra.Command{Use: "root"}
	leaf := func(use, withhold string) *cobra.Command {
		c := &cobra.Command{
			Use:         use,
			RunE:        func(*cobra.Command, []string) error { return nil },
			Annotations: map[string]string{"kit/side-effect": "read"},
		}
		if withhold != "" {
			c.Annotations[AnnotationWithholdFrom] = withhold
		}
		return c
	}
	root.AddCommand(
		leaf("follow", "rest, sse,rpc,mcp"),
		leaf("tail", "rest,socket"),
		leaf("typo", "rest,mpc"),
		leaf("ping", ""),
	)
	return root
}

// servedSurfaces are the surfaces "follow" withholds itself from.
var servedSurfaces = []Surface{SurfaceREST, SurfaceSSE, SurfaceRPC, SurfaceMCP}

func TestWithholdFrom_RefusedOnEveryNamedSurfaceDespiteExpose(t *testing.T) {
	for _, s := range servedSurfaces {
		t.Run(string(s), func(t *testing.T) {
			calls := 0
			b := New(withholdTree(), WithRunner(countingRunner(&calls, nil)))
			// Every kit-shipped service exposes the whole tree; the
			// command's declaration is a ceiling that does not lift.
			b.Expose("*", s)
			b.Expose("follow", s)

			leaf := leafNamed(t, b, "follow")
			if leaf.Enabled[s] {
				t.Fatalf("follow is enabled on %s; want it withheld", s)
			}
			if !leaf.Withheld(s) {
				t.Fatalf("Withheld(%s) = false", s)
			}
			_, err := b.Invoke(context.Background(), Invocation{
				Path: []string{"follow"}, Meta: Meta{Surface: s},
			})
			if !errors.Is(err, ErrSurfaceNotEnabled) {
				t.Fatalf("err = %v, want ErrSurfaceNotEnabled", err)
			}
			if !strings.Contains(err.Error(), AnnotationWithholdFrom) {
				t.Fatalf("err = %v, want it to name %s", err, AnnotationWithholdFrom)
			}
			if calls != 0 {
				t.Fatalf("runner reached: calls = %d", calls)
			}

			// The plain leaf is untouched.
			if _, err := b.Invoke(context.Background(), Invocation{
				Path: []string{"ping"}, Meta: Meta{Surface: s},
			}); err != nil {
				t.Fatalf("ping on %s: %v", s, err)
			}
		})
	}
}

func TestWithholdFrom_SocketKeepsTheCommandUnlessNamed(t *testing.T) {
	calls := 0
	b := New(withholdTree(), WithRunner(countingRunner(&calls, nil)))
	b.Expose("*", SurfaceSocket)

	if _, err := b.Invoke(context.Background(), Invocation{
		Path: []string{"follow"}, Meta: Meta{Surface: SurfaceSocket},
	}); err != nil {
		t.Fatalf("follow over the socket: %v", err)
	}
	if calls != 1 {
		t.Fatalf("calls = %d, want 1", calls)
	}

	// Named, the socket is withheld like any other surface.
	_, err := b.Invoke(context.Background(), Invocation{
		Path: []string{"tail"}, Meta: Meta{Surface: SurfaceSocket},
	})
	if !errors.Is(err, ErrSurfaceNotEnabled) {
		t.Fatalf("tail over the socket: err = %v, want ErrSurfaceNotEnabled", err)
	}
}

func TestWithholdFrom_DefaultEnabledSetAndConfigDoNotLiftIt(t *testing.T) {
	// MCP is in the default enabled set: the declaration removes it
	// at discovery.
	b := New(withholdTree())
	if leafNamed(t, b, "follow").Enabled[SurfaceMCP] {
		t.Fatal("follow is enabled on mcp by default")
	}
	if !leafNamed(t, b, "ping").Enabled[SurfaceMCP] {
		t.Fatal("ping lost the default mcp enablement")
	}

	// The YAML per-command list is no stronger than Expose.
	b, err := FromConfig(withholdTree(), Config{Surfaces: SurfacesConfig{
		Commands: map[string]CommandConfig{"follow": {Enabled: []Surface{SurfaceREST, SurfaceSocket}}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	leaf := leafNamed(t, b, "follow")
	if leaf.Enabled[SurfaceREST] {
		t.Fatal("surfaces.commands.follow.enabled lifted the withholding on rest")
	}
	if !leaf.Enabled[SurfaceSocket] {
		t.Fatal("surfaces.commands.follow.enabled lost the socket")
	}

	// Hide and Expose of other surfaces still work around it.
	b = New(withholdTree())
	b.Expose("follow", SurfaceSocket, SurfaceREST)
	b.Hide("follow", SurfaceSocket)
	leaf = leafNamed(t, b, "follow")
	if leaf.Enabled[SurfaceSocket] || leaf.Enabled[SurfaceREST] {
		t.Fatalf("enabled = %v, want neither socket nor rest", leaf.Enabled)
	}
}

func TestWithholdFrom_UnparseableValueWithholdsFromEveryRemoteSurface(t *testing.T) {
	b := New(withholdTree())
	b.Expose("*", AllSurfaces()...)
	leaf := leafNamed(t, b, "typo")
	for _, s := range AllSurfaces() {
		want := s == SurfaceCLI || s == SurfaceLib
		if leaf.Enabled[s] != want {
			t.Errorf("typo enabled on %s = %v, want %v", s, leaf.Enabled[s], want)
		}
	}

	err := ValidateWithholdFrom(withholdTree())
	if err == nil {
		t.Fatal("ValidateWithholdFrom accepted an unknown surface")
	}
	for _, want := range []string{"root typo", `"mpc"`, AnnotationWithholdFrom} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not name %s", err, want)
		}
	}
}

func TestParseWithholdFrom(t *testing.T) {
	got, err := ParseWithholdFrom(" rest , mcp,rest,")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0] != SurfaceREST || got[1] != SurfaceMCP {
		t.Fatalf("got %v, want [rest mcp]", got)
	}
	if got, err := ParseWithholdFrom(""); err != nil || len(got) != 0 {
		t.Fatalf("empty: got %v, %v", got, err)
	}
	if _, err := ParseWithholdFrom("rest,REST"); err == nil {
		t.Fatal("accepted an upper-case surface name")
	}
}

func TestWithholdFrom_ProjectionDescribesItWithItsOwnReason(t *testing.T) {
	b := New(withholdTree())
	b.Expose("*", SurfaceREST)
	b.Hide("ping", SurfaceREST)
	pcfg, err := Projection(b)
	if err != nil {
		t.Fatal(err)
	}
	reasons := map[string]string{}
	for _, d := range pcfg.Descriptors {
		if d.Invocable {
			t.Errorf("%s is invocable over REST", d.PathKey())
		}
		reasons[d.PathKey()] = d.Reason
	}
	if reasons["follow"] != ReasonWithheldByCommand {
		t.Errorf("follow reason = %q, want %q", reasons["follow"], ReasonWithheldByCommand)
	}
	if reasons["ping"] != ReasonWithheldByConfig {
		t.Errorf("ping reason = %q, want %q", reasons["ping"], ReasonWithheldByConfig)
	}
	doc := api.BuildDiscoveryDocument(pcfg)
	for _, e := range doc.Commands {
		if e.Name == "follow" && e.Route != "" {
			t.Errorf("follow advertises route %q", e.Route)
		}
	}
}

func leafNamed(t *testing.T, b *Bridge, path string) *Leaf {
	t.Helper()
	for _, l := range b.Leaves() {
		if l.PathKey() == path {
			return l
		}
	}
	t.Fatalf("no leaf %q", path)
	return nil
}
