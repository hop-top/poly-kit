package service

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/go/console/cli"
)

const legacyBody = "services:\n  api:\n    body_limit:\n      max_bytes: 4096\n"

func legacyRoot(t *testing.T, legacy ...string) *cli.Root {
	t.Helper()
	return cli.New(cli.Config{
		Name:                  "tool",
		Version:               "1.0.0",
		DisableValidate:       true,
		LegacyUserConfigPaths: legacy,
	}, WithAPI(APIConfig{}))
}

func writeLegacy(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "old", "settings.yaml")
	require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
	require.NoError(t, os.WriteFile(p, []byte(body), 0o600))
	return p
}

func TestServiceConfigLegacyUserFileLoads(t *testing.T) {
	isolateHome(t)
	r := legacyRoot(t, writeLegacy(t, legacyBody))
	require.NoError(t, loadServiceConfig(r))
	got, err := apiSvc(t, r).maxBodyBytes()
	require.NoError(t, err)
	assert.Equal(t, int64(4096), got)
}

func TestServiceConfigLegacyUnsetIgnoresFile(t *testing.T) {
	isolateHome(t)
	writeLegacy(t, legacyBody)
	r := legacyRoot(t)
	require.NoError(t, loadServiceConfig(r))
	assert.False(t, r.Viper.IsSet("services.api.body_limit.max_bytes"), "no field, no legacy lookup")
}

func TestServiceConfigCanonicalShadowsLegacy(t *testing.T) {
	isolateHome(t)
	userConfig(t, "services:\n  api:\n    body_limit:\n      max_bytes: 1024\n")
	r := legacyRoot(t, writeLegacy(t, legacyBody))
	require.NoError(t, loadServiceConfig(r))
	got, err := apiSvc(t, r).maxBodyBytes()
	require.NoError(t, err)
	assert.Equal(t, int64(1024), got)
}

func TestServiceConfigBadLegacyFileIsAUsageError(t *testing.T) {
	isolateHome(t)
	bad := writeLegacy(t, "services: [unclosed\n")
	r := legacyRoot(t, bad, writeLegacy(t, legacyBody))
	oe := usageErr(t, loadServiceConfig(r))
	assert.Contains(t, oe.Message, bad)

	dir := t.TempDir()
	r = legacyRoot(t, dir)
	oe = usageErr(t, loadServiceConfig(r))
	assert.Contains(t, oe.Message, dir)
}
