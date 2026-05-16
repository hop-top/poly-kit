package cli_test

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"hop.top/kit/go/console/cli"
	"hop.top/kit/go/runtime/sideeffect"
)

// TestSupportsDryRun_Annotation covers the legacy ADR-0019 entry
// point. Retained as a back-compat synonym under ADR-0020: setting
// the annotation still flips IsDryRunSupported to true regardless
// of side-effect tier.
func TestSupportsDryRun_Annotation(t *testing.T) {

	cmd := &cobra.Command{Use: "foo"}
	if cli.IsDryRunSupported(cmd) {
		t.Fatalf("fresh cmd must not report dry-run supported")
	}
	cli.SupportsDryRun(cmd)
	if !cli.IsDryRunSupported(cmd) {
		t.Fatalf("after SupportsDryRun, cmd must report supported")
	}
}

func TestGlobalDryRunFlag_RegisteredByDefault(t *testing.T) {

	r := cli.New(cli.Config{Name: "t", Version: "0.1.0", Short: "t", DisableValidate: true})
	pf := r.Cmd.PersistentFlags()
	if f := pf.Lookup("dry-run"); f == nil {
		t.Fatalf("--dry-run must be registered by default")
	}
}

func TestGlobalDryRunFlag_DisabledByConfig(t *testing.T) {

	r := cli.New(cli.Config{
		Name: "t", Version: "0.1.0", Short: "t",
		Disable:         cli.Disable{DryRun: true},
		DisableValidate: true,
	})
	pf := r.Cmd.PersistentFlags()
	if f := pf.Lookup("dry-run"); f != nil {
		t.Fatalf("--dry-run must be suppressed when Disable.DryRun=true")
	}
}

// TestGlobalDryRun_RefusedOnUntaggedLeaf: a leaf with no
// kit/side-effect tag has no tier the resolver can use and no
// legacy SupportsDryRun annotation. The hook rejects --dry-run
// with a diagnostic pointing at the missing tag (Root.Validate is
// the canonical fix; the hook is a backstop).
func TestGlobalDryRun_RefusedOnUntaggedLeaf(t *testing.T) {

	r := cli.New(cli.Config{Name: "t", Version: "0.1.0", Short: "t", DisableValidate: true})
	leaf := &cobra.Command{
		Use:  "do",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			t.Fatalf("RunE must not be reached when --dry-run is refused")
			return nil
		},
	}
	r.Cmd.AddCommand(leaf)
	r.Cmd.SetArgs([]string{"do", "--dry-run"})
	var buf bytes.Buffer
	r.Cmd.SetOut(&buf)
	r.Cmd.SetErr(&buf)
	err := r.Execute(context.Background())
	require.Error(t, err, "untagged leaf must reject --dry-run")
	if !strings.Contains(err.Error(), "kit/side-effect") {
		t.Fatalf("error must mention missing tag: %v", err)
	}
}

// TestGlobalDryRun_AcceptedOnLegacySupportsDryRun preserves the
// ADR-0019 path: SupportsDryRun(cmd) opts the leaf in even when
// the tier is unset. The deprecation warning fires once at
// startup, which is asserted in TestGlobalDryRun_LegacyAnnotation_LogsDeprecation.
func TestGlobalDryRun_AcceptedOnLegacySupportsDryRun(t *testing.T) {

	r := cli.New(cli.Config{Name: "t", Version: "0.1.0", Short: "t", DisableValidate: true})
	var ranWithDryRun bool
	leaf := &cobra.Command{
		Use:  "do",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ranWithDryRun = sideeffect.IsDryRun(cmd.Context())
			return nil
		},
	}
	cli.SupportsDryRun(leaf)
	r.Cmd.AddCommand(leaf)
	r.Cmd.SetArgs([]string{"do", "--dry-run"})
	var buf bytes.Buffer
	r.Cmd.SetOut(&buf)
	r.Cmd.SetErr(&buf)
	err := r.Execute(context.Background())
	require.NoError(t, err)
	assert.True(t, ranWithDryRun, "RunE must observe IsDryRun(ctx)=true")
}

func TestGlobalDryRun_OptedInLeaf_NoFlag_NoDryRun(t *testing.T) {

	r := cli.New(cli.Config{Name: "t", Version: "0.1.0", Short: "t", DisableValidate: true})
	var seenDryRun bool
	leaf := &cobra.Command{
		Use:  "do",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			seenDryRun = sideeffect.IsDryRun(cmd.Context())
			return nil
		},
	}
	cli.SetSideEffect(leaf, cli.SideEffectWrite)
	r.Cmd.AddCommand(leaf)
	r.Cmd.SetArgs([]string{"do"})
	var buf bytes.Buffer
	r.Cmd.SetOut(&buf)
	r.Cmd.SetErr(&buf)
	err := r.Execute(context.Background())
	require.NoError(t, err)
	assert.False(t, seenDryRun, "without --dry-run, ctx must not be tagged")
}

func TestGlobalDryRun_HelpAddendumOnOptedInLeaf(t *testing.T) {

	r := cli.New(cli.Config{Name: "t", Version: "0.1.0", Short: "t", DisableValidate: true})
	leaf := &cobra.Command{
		Use:   "do",
		Short: "do something",
		Long:  "Do something useful.",
		Run:   func(*cobra.Command, []string) {},
	}
	cli.SetSideEffect(leaf, cli.SideEffectWrite)
	r.Cmd.AddCommand(leaf)
	r.Cmd.SetArgs([]string{"do", "--help"})
	var buf bytes.Buffer
	r.Cmd.SetOut(&buf)
	r.Cmd.SetErr(&buf)
	_ = r.Execute(context.Background())
	out := buf.String()
	if !strings.Contains(out, "Dry-run support") {
		t.Fatalf("help output missing dry-run support line:\n%s", out)
	}
}
