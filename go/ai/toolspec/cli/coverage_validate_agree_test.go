package cli_test

import (
	"bytes"
	"errors"
	"sort"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	speccli "hop.top/kit/go/ai/toolspec/cli"
	kitcli "hop.top/kit/go/console/cli"
)

// TestCoverageAgreesWithValidate pins that `spec coverage` and
// Root.Validate name the same commands: every path coverage reports
// unannotated or malformed is one Validate refuses (a leaf) or warns
// about (a runnable group), and nothing else.
func TestCoverageAgreesWithValidate(t *testing.T) {
	r := coverageRoot(t, "demo", map[string]string{
		"show":   "read",
		"silent": "",
		"typo":   "wrte",
	})
	require.NoError(t, speccli.RegisterSpecCommand(r, "1.1"))
	group := func(name, se string, runnable bool) {
		g := &cobra.Command{Use: name, Short: name}
		if se != "" {
			g.Annotations = map[string]string{"kit/side-effect": se}
		}
		if runnable {
			g.RunE = func(*cobra.Command, []string) error { return nil }
		}
		g.AddCommand(&cobra.Command{
			Use: "list", Short: "list",
			RunE:        func(*cobra.Command, []string) error { return nil },
			Annotations: map[string]string{"kit/side-effect": "read"},
		})
		r.Cmd.AddCommand(g)
	}
	group("item", "", true)
	group("odd", "destrutive", true)
	group("fine", "read", true)
	group("pure", "", false)
	// A kit-reserved verb is not the adopter's to annotate: coverage
	// skips it, so Validate must not warn about it either.
	group("adm", "", true)
	r.MarkReserved("adm")

	var errBuf bytes.Buffer
	r.Cmd.SetErr(&errBuf)
	var ve *kitcli.ValidationError
	require.True(t, errors.As(r.Validate(), &ve), "the unannotated leaves are refused")

	var warned []string
	for _, line := range strings.Split(errBuf.String(), "\n") {
		if strings.HasPrefix(line, "  ") {
			warned = append(warned, strings.TrimSpace(line))
		}
	}

	var named, malformed []string
	for _, p := range append(append(append([]string(nil), ve.Missing...), ve.Invalid...), warned...) {
		path, _, isMalformed := strings.Cut(p, "=")
		named = append(named, path)
		if isMalformed {
			malformed = append(malformed, p)
		}
	}
	sort.Strings(named)
	sort.Strings(malformed)

	rep := speccli.CoverageOf(r)
	assert.Equal(t, rep.UnannotatedPaths, named)
	assert.Equal(t, rep.MalformedPaths, malformed)
	assert.Equal(t, []string{`demo odd="destrutive"`, `demo typo="wrte"`}, rep.MalformedPaths)
	assert.Contains(t, rep.UnannotatedPaths, "demo item")
}
