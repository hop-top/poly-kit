package conformance_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	kitconformance "hop.top/kit/go/conformance"
	"hop.top/kit/go/console/cli"
)

// stubTB implements kitconformance.TB without escalating failures
// to the test framework. Sad-path tests pass this in so they can
// assert on the resulting *ValidationError without the outer test
// itself failing.
type stubTB struct {
	errors []string
	fatal  []string
}

func (s *stubTB) Helper() {}
func (s *stubTB) Errorf(format string, args ...any) {
	s.errors = append(s.errors, fmt.Sprintf(format, args...))
}
func (s *stubTB) Fatalf(format string, args ...any) {
	s.fatal = append(s.fatal, fmt.Sprintf(format, args...))
}

// rootFixture returns a kit Root with a single canonical noun-verb
// subtree plus the reserved status leaf. All checks pass under
// AssertCLI with default options.
func rootFixture() *cli.Root {
	r := cli.New(cli.Config{
		Name:            "ftool",
		Version:         "0.1.0",
		Short:           "conformance fixture tool",
		DisableValidate: true,
	})
	statusLeaf := &cobra.Command{
		Use:   "status",
		Short: "Show tool state",
		Long:  "Show the tool's current state (workspace, profile, ...).",
		Run:   func(*cobra.Command, []string) {},
	}
	cli.SetSideEffect(statusLeaf, cli.SideEffectRead)
	cli.SetIdempotency(statusLeaf, cli.IdempotencyYes)
	cli.SetTopLevelVerb(statusLeaf)
	r.Cmd.AddCommand(statusLeaf)

	foo := &cobra.Command{Use: "foo", Short: "foo group"}
	create := &cobra.Command{
		Use: "create", Short: "Create a foo",
		Long: "Create a new foo with the given options.",
		Run:  func(*cobra.Command, []string) {},
	}
	cli.SetSideEffect(create, cli.SideEffectWrite)
	cli.SetIdempotency(create, cli.IdempotencyNo)
	list := &cobra.Command{
		Use: "list", Short: "List foos",
		Long: "List all foos visible to the current identity.",
		Run:  func(*cobra.Command, []string) {},
	}
	cli.SetSideEffect(list, cli.SideEffectRead)
	cli.SetIdempotency(list, cli.IdempotencyYes)
	foo.AddCommand(create, list)
	r.Cmd.AddCommand(foo)

	return r
}

func TestAssertCLI_HappyPath_FullyAnnotatedRootPasses(t *testing.T) {
	r := rootFixture()
	ve := kitconformance.AssertCLI(t, r)
	if ve != nil {
		t.Fatalf("expected nil validation error, got: %s", ve.Error())
	}
}

func TestAssertCLI_HappyPath_RestoresEnforceValidate(t *testing.T) {
	r := rootFixture()
	prev := r.Config.EnforceValidate
	kitconformance.AssertCLI(t, r)
	if r.Config.EnforceValidate != prev {
		t.Fatalf("Config.EnforceValidate must be restored after the assertion (was %v, now %v)",
			prev, r.Config.EnforceValidate)
	}
}

func TestAssertCLI_SadPath_MissingStatusSubcommand(t *testing.T) {
	r := cli.New(cli.Config{
		Name:            "ftool",
		Version:         "0.1.0",
		Short:           "no-status fixture",
		DisableValidate: true,
	})
	leaf := &cobra.Command{
		Use: "ping", Short: "Ping",
		Long: "Ping a peer.",
		Run:  func(*cobra.Command, []string) {},
	}
	cli.SetSideEffect(leaf, cli.SideEffectRead)
	cli.SetIdempotency(leaf, cli.IdempotencyYes)
	cli.SetTopLevelVerb(leaf)
	r.Cmd.AddCommand(leaf)

	stub := &stubTB{}
	ve := kitconformance.AssertCLI(stub, r)

	if ve == nil {
		t.Fatal("expected AssertCLI to surface a non-nil ValidationError")
	}
	if len(ve.MissingStatusSubcommand) == 0 {
		t.Fatalf("expected MissingStatusSubcommand bucket; got: %s", ve.Error())
	}
	if len(stub.errors) == 0 {
		t.Fatal("expected AssertCLI to call t.Errorf with a useful message")
	}
	joined := strings.Join(stub.errors, " | ")
	if !strings.Contains(joined, "status") {
		t.Fatalf("expected message to mention 'status'; got: %s", joined)
	}
}

func TestAssertCLI_SadPath_GuidanceMissing(t *testing.T) {
	r := rootFixture()
	stub := &stubTB{}
	ve := kitconformance.AssertCLIWithOptions(stub, r,
		kitconformance.Options{EnforceGuidance: true})

	if ve == nil {
		t.Fatal("expected AssertCLI to surface a non-nil ValidationError")
	}
	if len(ve.MissingExamples) == 0 {
		t.Fatalf("expected MissingExamples bucket; got: %s", ve.Error())
	}
	if len(stub.errors) == 0 {
		t.Fatal("expected AssertCLI to call t.Errorf")
	}
}

func TestAssertCLI_SadPath_MissingShort(t *testing.T) {
	r := rootFixture()
	bad := &cobra.Command{
		Use: "bare", Long: "no short",
		Run: func(*cobra.Command, []string) {},
	}
	cli.SetSideEffect(bad, cli.SideEffectRead)
	cli.SetIdempotency(bad, cli.IdempotencyYes)
	cli.SetTopLevelVerb(bad)
	r.Cmd.AddCommand(bad)

	stub := &stubTB{}
	ve := kitconformance.AssertCLI(stub, r)

	if ve == nil {
		t.Fatal("expected AssertCLI to surface a non-nil ValidationError")
	}
	if len(ve.MissingShort) == 0 {
		t.Fatalf("expected MissingShort bucket; got: %s", ve.Error())
	}
}

func TestAssertCLI_NilRoot_CallsFatalf(t *testing.T) {
	stub := &stubTB{}
	ve := kitconformance.AssertCLI(stub, nil)
	if ve != nil {
		t.Fatalf("expected nil VE for nil root; got: %s", ve.Error())
	}
	if len(stub.fatal) == 0 {
		t.Fatal("expected nil root to trigger Fatalf")
	}
}

// TestAssertCLI_AcceptsRealTestingT confirms the TB interface
// accepts *testing.T at the call site — i.e. adopters can pass
// the testing.T they receive from a top-level test function
// directly, without any wrapper.
func TestAssertCLI_AcceptsRealTestingT(t *testing.T) {
	r := rootFixture()
	var tb kitconformance.TB = t
	if ve := kitconformance.AssertCLI(tb, r); ve != nil {
		t.Fatalf("happy path must return nil; got: %s", ve.Error())
	}
}
