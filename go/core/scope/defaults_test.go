package scope_test

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"hop.top/kit/go/core/scope"
)

func TestSecretPaths_HasCommonAndPlatform(t *testing.T) {
	paths := scope.SecretPaths()
	assert.NotEmpty(t, paths, "SecretPaths must include the embedded common set")

	// At least one canonical common pattern must be present.
	var sawSSH bool
	for _, p := range paths {
		if string(p) == "~/.ssh/**" {
			sawSSH = true
			break
		}
	}
	assert.True(t, sawSSH, "expected ~/.ssh/** in default deny list")
}

func TestSecretPaths_DefaultDeniesSSHKey(t *testing.T) {
	home := withHome(t)

	// Use a fresh policy seeded with SecretPaths to avoid touching the
	// process-global Default singleton.
	p := scope.New().Deny(scope.SecretPaths()...)
	dec, err := p.Check(scope.Path(filepath.Join(home, ".ssh", "id_rsa")), scope.Read)
	require.NoError(t, err)
	assert.Equal(t, scope.Denied, dec)
}

func TestSecretPaths_DenyDotEnvAnywhere(t *testing.T) {
	withHome(t)
	p := scope.New().Deny(scope.SecretPaths()...)
	dec, err := p.Check("/tmp/anywhere/.env", scope.Read)
	require.NoError(t, err)
	assert.Equal(t, scope.Denied, dec)
}

func TestDefault_HasSecretsDeniedAtInit(t *testing.T) {
	home := withHome(t)
	// Default is mutated by defaults.go init(); SecretPaths must already be denied.
	dec, err := scope.Default().Check(scope.Path(filepath.Join(home, ".ssh", "id_rsa")), scope.Read)
	require.NoError(t, err)
	assert.Equal(t, scope.Denied, dec)
}

func TestUserDocs_RespectsXDGOverride(t *testing.T) {
	t.Setenv("XDG_DOCUMENTS_DIR", "/custom/docs")
	got := scope.UserDocs()
	require.Len(t, got, 1)
	assert.Equal(t, scope.Pattern("/custom/docs/**"), got[0])
}

func TestUserDocs_FallsBackToHome(t *testing.T) {
	home := withHome(t)
	os.Unsetenv("XDG_DOCUMENTS_DIR")
	got := scope.UserDocs()
	require.Len(t, got, 1)
	assert.Equal(t, scope.Pattern(filepath.Join(home, "Documents", "**")), got[0])
}

func TestUserHome_RecursivePattern(t *testing.T) {
	home := withHome(t)
	got := scope.UserHome()
	require.Len(t, got, 1)
	assert.Equal(t, scope.Pattern(filepath.Join(home, "**")), got[0])
}

func TestToolConfig_RespectsXDG(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "/tmp/xdg-config")
	got := scope.ToolConfig("mytool")
	require.Len(t, got, 1)
	assert.Equal(t, scope.Pattern("/tmp/xdg-config/mytool/**"), got[0])
}

func TestToolData_RespectsXDG(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", "/tmp/xdg-data")
	got := scope.ToolData("mytool")
	require.Len(t, got, 1)
	assert.Equal(t, scope.Pattern("/tmp/xdg-data/mytool/**"), got[0])
}

func TestToolCache_RespectsXDG(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", "/tmp/xdg-cache")
	got := scope.ToolCache("mytool")
	require.Len(t, got, 1)
	assert.Equal(t, scope.Pattern("/tmp/xdg-cache/mytool/**"), got[0])
}

func TestToolState_RespectsXDG(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", "/tmp/xdg-state")
	got := scope.ToolState("mytool")
	require.Len(t, got, 1)
	assert.Equal(t, scope.Pattern("/tmp/xdg-state/mytool/**"), got[0])
}

func TestToolRuntime_RespectsXDGRuntime(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", "/run/user/1000")
	got := scope.ToolRuntime("mytool")
	require.Len(t, got, 1)
	assert.Equal(t, scope.Pattern("/run/user/1000/mytool/**"), got[0])
}

func TestSystemDirs_NonEmpty(t *testing.T) {
	got := scope.SystemDirs()
	assert.NotEmpty(t, got)

	switch runtime.GOOS {
	case "darwin":
		assert.Contains(t, got, scope.Pattern("/etc/**"))
		assert.Contains(t, got, scope.Pattern("/System/**"))
	case "windows":
		assert.Contains(t, got, scope.Pattern("C:/Windows/**"))
	default:
		assert.Contains(t, got, scope.Pattern("/etc/**"))
		assert.Contains(t, got, scope.Pattern("/proc/**"))
	}
}

func TestParityWithContract(t *testing.T) {
	// scope-defaults.json is the single source of truth; the embedded copy
	// must match the canonical contracts/parity/scope-defaults.json byte-for-byte.
	embedded, err := os.ReadFile("scope-defaults.json")
	require.NoError(t, err)
	canonical, err := os.ReadFile("../../../contracts/parity/scope-defaults.json")
	require.NoError(t, err)
	assert.Equal(t, string(canonical), string(embedded),
		"go/core/scope/scope-defaults.json drifted from contracts/parity/scope-defaults.json -- re-sync")
}
