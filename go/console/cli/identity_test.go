package cli_test

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"hop.top/kit/go/console/cli"
	"hop.top/kit/go/core/identity"
)

func TestWithIdentity_AutoGenerates(t *testing.T) {
	dir := t.TempDir()
	root := cli.New(cli.Config{Name: "test", Version: "0.1.0", Short: "t", DisableValidate: true},
		cli.WithIdentity(cli.IdentityConfig{Dir: dir}))

	require.NotNil(t, root.Identity, "keypair should be generated")
	assert.NotEmpty(t, root.Identity.PublicKeyID())
}

func TestWithIdentity_LoadsExisting(t *testing.T) {
	dir := t.TempDir()

	// Pre-generate a keypair.
	store, err := identity.NewStore(dir)
	require.NoError(t, err)
	kp, err := identity.Generate()
	require.NoError(t, err)
	require.NoError(t, store.Save(kp))

	root := cli.New(cli.Config{Name: "test", Version: "0.1.0", Short: "t", DisableValidate: true},
		cli.WithIdentity(cli.IdentityConfig{Dir: dir}))

	require.NotNil(t, root.Identity)
	assert.Equal(t, kp.PublicKeyID(), root.Identity.PublicKeyID())
}

func TestWithIdentity_NoAutoInit_ErrorsWhenMissing(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "empty")
	root := cli.New(cli.Config{Name: "test", Version: "0.1.0", Short: "t", DisableValidate: true},
		cli.WithIdentity(cli.IdentityConfig{Dir: dir, NoAutoInit: true}))

	// Identity should be nil; error deferred to PersistentPreRunE.
	assert.Nil(t, root.Identity)
	assert.NotNil(t, root.Cmd.PersistentPreRunE)
}

func TestWithoutIdentity_NilKeypair(t *testing.T) {
	root := cli.New(cli.Config{Name: "test", Version: "0.1.0", Short: "t", DisableValidate: true})
	assert.Nil(t, root.Identity)
}

func TestWithIdentity_CustomDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "custom", "sub")
	root := cli.New(cli.Config{Name: "test", Version: "0.1.0", Short: "t", DisableValidate: true},
		cli.WithIdentity(cli.IdentityConfig{Dir: dir}))

	require.NotNil(t, root.Identity)
	// Verify files exist in custom dir.
	store, err := identity.NewStore(dir)
	require.NoError(t, err)
	kp, err := store.Load()
	require.NoError(t, err)
	assert.Equal(t, root.Identity.PublicKeyID(), kp.PublicKeyID())
}
