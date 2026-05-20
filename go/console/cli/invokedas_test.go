package cli_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"hop.top/kit/go/console/cli"
)

// newRoot builds a minimal Root for InvokedAs tests. DisableValidate
// keeps the fixture small — invoked-as has no annotation dependency.
func newInvokedAsRoot(t *testing.T) *cli.Root {
	t.Helper()
	return cli.New(cli.Config{
		Name:            "fixture",
		Version:         "0.0.0",
		Short:           "invoked-as test fixture",
		DisableValidate: true,
	})
}

func TestInvokedAs_Empty(t *testing.T) {
	// Guard against a stray ambient value in the test process — the
	// contract is "unset → empty string". t.Setenv to "" is equivalent
	// to unset for os.Getenv and auto-restores any prior value.
	t.Setenv("KIT_INVOKED_AS", "")
	r := newInvokedAsRoot(t)
	assert.Equal(t, "", r.InvokedAs(),
		"InvokedAs must be empty when KIT_INVOKED_AS is unset")
}

func TestInvokedAs_Set(t *testing.T) {
	t.Setenv("KIT_INVOKED_AS", "tlc")
	r := newInvokedAsRoot(t)
	assert.Equal(t, "tlc", r.InvokedAs(),
		"InvokedAs must return the env var value verbatim")
}

func TestInvokedAs_TrimsWhitespace(t *testing.T) {
	t.Setenv("KIT_INVOKED_AS", "  hop  ")
	r := newInvokedAsRoot(t)
	assert.Equal(t, "hop", r.InvokedAs(),
		"InvokedAs must trim surrounding whitespace")
}
