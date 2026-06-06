package cli_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/console/cli"
	"hop.top/cite/handle/generate"
)

func TestWithURI_MountsConfiguredCommand(t *testing.T) {
	r := cli.New(cli.Config{Name: "fixture", Short: "test", DisableValidate: true}, cli.WithURI(cli.URIConfig{
		CommandName: "link",
		Handler: cli.URIHandlerConfig{
			Vendor:   "hop-top",
			App:      "fixture",
			Language: generate.LanguageGo,
			Scheme:   "fixture",
			AppPath:  "/usr/local/bin/fixture",
		},
	}))

	cmd, _, err := r.Cmd.Find([]string{"link", "handler", "id"})
	require.NoError(t, err)
	assert.Equal(t, "id", cmd.Name())
}

func TestWithURI_ValidatePasses(t *testing.T) {
	r := cli.New(cli.Config{Name: "fixture", Short: "test", DisableValidate: true}, cli.WithURI(cli.URIConfig{
		Handler: cli.URIHandlerConfig{
			Vendor:   "hop-top",
			App:      "fixture",
			Language: generate.LanguageGo,
			Scheme:   "fixture",
			AppPath:  "/usr/local/bin/fixture",
		},
	}))

	require.NoError(t, r.Validate())
}
