package template_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hop.top/kit/internal/template"
)

// installFakeGit copies testdata/fake-git.sh into a temp dir as `git`,
// puts that dir on PATH (only), and returns the log file path that the
// fake records each invocation's args into.
func installFakeGit(t *testing.T) string {
	t.Helper()
	src, err := filepath.Abs(filepath.Join("testdata", "fake-git.sh"))
	require.NoError(t, err)
	if _, err := os.Stat(src); err != nil {
		t.Skipf("fake-git.sh missing: %v", err)
	}
	binDir := t.TempDir()
	dst := filepath.Join(binDir, "git")
	data, err := os.ReadFile(src)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(dst, data, 0o755))
	logPath := filepath.Join(t.TempDir(), "git.log")
	// fake-git.sh shells out to coreutils (mkdir, cat); keep /bin:/usr/bin
	// on PATH so they resolve, but our binDir comes first so `git` => fake.
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+"/bin:/usr/bin")
	t.Setenv("FAKE_GIT_LOG", logPath)
	t.Setenv("FAKE_GIT_FAIL", "0")
	got, err := exec.LookPath("git")
	require.NoError(t, err)
	require.Equal(t, dst, got, "fake git must be first on PATH")
	return logPath
}

func TestResolve_BuiltIn(t *testing.T) {
	names, err := template.Available()
	require.NoError(t, err)
	if len(names) == 0 {
		t.Skip("internal/template/builtins/ empty; run `make builtins-sync`")
	}
	r := template.NewRegistry("", "")
	got, err := r.Resolve(context.Background(), "cli-go")
	require.NoError(t, err)
	_, err = fs.ReadFile(got, "kit-template.yaml")
	require.NoError(t, err, "built-in fs must expose cli-go/kit-template.yaml at root")
}

func TestResolve_Filesystem(t *testing.T) {
	r := template.NewRegistry("", "")
	got, err := r.Resolve(context.Background(), "./testdata/local-template")
	require.NoError(t, err)
	data, err := fs.ReadFile(got, "kit-template.yaml")
	require.NoError(t, err)
	assert.Contains(t, string(data), "name: local-fixture")
}

func TestResolve_Filesystem_Absolute(t *testing.T) {
	abs, err := filepath.Abs(filepath.Join("testdata", "local-template"))
	require.NoError(t, err)
	r := template.NewRegistry("", "")
	got, err := r.Resolve(context.Background(), abs)
	require.NoError(t, err)
	data, err := fs.ReadFile(got, "kit-template.yaml")
	require.NoError(t, err)
	assert.Contains(t, string(data), "name: local-fixture")
}

// startIndexServer returns a httptest server serving the given templates
// map as the registry index document.
func startIndexServer(t *testing.T, templates map[string]map[string]string) *httptest.Server {
	t.Helper()
	doc := map[string]any{
		"schema":    1,
		"templates": templates,
	}
	body, err := json.Marshal(doc)
	require.NoError(t, err)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestResolve_OrgName_IndexHit(t *testing.T) {
	logPath := installFakeGit(t)
	const wantURL = "https://git.example.com/acme/internal.git"
	srv := startIndexServer(t, map[string]map[string]string{
		"@acme/internal": {"git": wantURL, "default_ref": "v1.0.0"},
	})
	cache := t.TempDir()
	r := template.NewRegistry(srv.URL, cache)
	got, err := r.Resolve(context.Background(), "@acme/internal")
	require.NoError(t, err)
	_, err = fs.ReadFile(got, "kit-template.yaml")
	require.NoError(t, err, "fake-git stub must populate kit-template.yaml")
	logBytes, err := os.ReadFile(logPath)
	require.NoError(t, err)
	log := string(logBytes)
	assert.Contains(t, log, wantURL, "git invocation must reference resolved URL")
	assert.Contains(t, log, "--branch v1.0.0", "default_ref must be passed as --branch")
}

func TestResolve_OrgName_NotInIndex(t *testing.T) {
	// Index responds 200 with valid JSON, but @acme/missing isn't listed.
	srv := startIndexServer(t, map[string]map[string]string{
		"@acme/other": {"git": "https://example.com/other.git"},
	})
	r := template.NewRegistry(srv.URL, t.TempDir())
	_, err := r.Resolve(context.Background(), "@acme/missing")
	require.Error(t, err)
	assert.True(t, template.IsTemplateNotFound(err),
		"missing index entry must yield TemplateNotFound, got: %v", err)

	// Empty templates map: same outcome.
	srv2 := startIndexServer(t, map[string]map[string]string{})
	r2 := template.NewRegistry(srv2.URL, t.TempDir())
	_, err = r2.Resolve(context.Background(), "@acme/missing")
	require.Error(t, err)
	assert.True(t, template.IsTemplateNotFound(err),
		"empty templates map must yield TemplateNotFound, got: %v", err)
}

func TestResolve_DirectGit(t *testing.T) {
	logPath := installFakeGit(t)
	r := template.NewRegistry("", t.TempDir())
	got, err := r.Resolve(context.Background(), "github.com/foo/bar")
	require.NoError(t, err)
	_, err = fs.ReadFile(got, "kit-template.yaml")
	require.NoError(t, err)
	logBytes, err := os.ReadFile(logPath)
	require.NoError(t, err)
	log := string(logBytes)
	assert.Contains(t, log, "https://github.com/foo/bar",
		"clone must use https:// with bare host/owner/repo")
	assert.NotContains(t, log, "--branch", "no ref => no --branch flag")
}

func TestResolve_GitWithRef(t *testing.T) {
	logPath := installFakeGit(t)
	r := template.NewRegistry("", t.TempDir())
	_, err := r.Resolve(context.Background(), "github.com/foo/bar@v1.2.3")
	require.NoError(t, err)
	logBytes, err := os.ReadFile(logPath)
	require.NoError(t, err)
	assert.Contains(t, string(logBytes), "--branch v1.2.3")
}

func TestResolve_CacheHit(t *testing.T) {
	cache := t.TempDir()
	const gitURL = "https://github.com/foo/bar"
	const ref = ""
	sum := sha256.Sum256([]byte(gitURL + "@" + ref))
	key := hex.EncodeToString(sum[:])
	dest := filepath.Join(cache, key)
	require.NoError(t, os.MkdirAll(dest, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dest, "kit-template.yaml"),
		[]byte("name: cached\n"), 0o644))

	// PATH cleared: if Resolve attempts git, exec.LookPath returns
	// ErrNotFound and CommandContext.Run() yields "no such file" error.
	t.Setenv("PATH", "")

	r := template.NewRegistry("", cache)
	got, err := r.Resolve(context.Background(), "github.com/foo/bar")
	require.NoError(t, err, "cache hit must skip git entirely")
	data, err := fs.ReadFile(got, "kit-template.yaml")
	require.NoError(t, err)
	assert.Equal(t, "name: cached\n", string(data))
}

func TestResolve_AtomicClone(t *testing.T) {
	installFakeGit(t)
	t.Setenv("FAKE_GIT_FAIL", "1")
	cache := t.TempDir()
	r := template.NewRegistry("", cache)
	_, err := r.Resolve(context.Background(), "github.com/foo/bar")
	require.Error(t, err)

	// Derive expected paths from cache key.
	sum := sha256.Sum256([]byte("https://github.com/foo/bar" + "@"))
	key := hex.EncodeToString(sum[:])
	final := filepath.Join(cache, key)
	tmp := final + ".tmp"
	_, statErr := os.Stat(tmp)
	assert.True(t, os.IsNotExist(statErr), "tmp clone dir must be removed on failure")
	_, statErr = os.Stat(final)
	assert.True(t, os.IsNotExist(statErr), "final cache dir must not exist on failure")
}

func TestResolve_OfflineBuiltIn(t *testing.T) {
	names, err := template.Available()
	require.NoError(t, err)
	if len(names) == 0 {
		t.Skip("internal/template/builtins/ empty; run `make builtins-sync`")
	}
	// No httptest server, no git available, no index URL.
	t.Setenv("PATH", "")
	r := template.NewRegistry("", "")
	got, err := r.Resolve(context.Background(), "cli-go")
	require.NoError(t, err, "built-in resolution must work offline")
	_, err = fs.ReadFile(got, "kit-template.yaml")
	require.NoError(t, err)
	// Sanity: the network/git path would fail without these resources.
	assert.False(t, strings.Contains(os.Getenv("PATH"), string(os.PathSeparator)),
		"PATH should be empty for this test")
}
