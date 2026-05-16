package kitinit_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	kitinit "hop.top/kit/cmd/kit/init"
)

func setupStubGh(t *testing.T) (logPath string) {
	t.Helper()
	fixtureSrc := "testdata/stub-gh.sh"
	if _, err := os.Stat(fixtureSrc); err != nil {
		t.Fatalf("stub-gh fixture missing: %v", err)
	}
	binDir := t.TempDir()
	dst := filepath.Join(binDir, "gh")
	src, err := os.ReadFile(fixtureSrc)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(dst, src, 0o755))

	logPath = filepath.Join(t.TempDir(), "gh.log")
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+"/usr/bin:/bin")
	t.Setenv("STUB_GH_LOG", logPath)
	return logPath
}

func readLog(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return string(data)
}

func TestCreate_None_NoOp(t *testing.T) {
	logPath := setupStubGh(t)
	dir := t.TempDir()
	info, err := kitinit.Create(context.Background(), dir, kitinit.RepoConfig{
		AccountType: "none",
		Name:        "foo",
	})
	require.NoError(t, err)
	assert.Equal(t, kitinit.RepoInfo{}, info)
	assert.Empty(t, readLog(t, logPath), "gh should not be invoked for AccountType=none")
}

func TestCreate_Personal_PublicArgs(t *testing.T) {
	logPath := setupStubGh(t)
	dir := t.TempDir()
	_, err := kitinit.Create(context.Background(), dir, kitinit.RepoConfig{
		AccountType: "personal",
		Name:        "mytool",
		Visibility:  "public",
	})
	require.NoError(t, err)
	log := readLog(t, logPath)
	assert.Contains(t, log, "repo create mytool --public --source="+dir+" --remote=origin --push")
}

func TestCreate_Org_InternalArgs(t *testing.T) {
	logPath := setupStubGh(t)
	dir := t.TempDir()
	_, err := kitinit.Create(context.Background(), dir, kitinit.RepoConfig{
		AccountType: "org",
		Owner:       "acme",
		Name:        "mytool",
		Visibility:  "internal",
	})
	require.NoError(t, err)
	log := readLog(t, logPath)
	assert.Contains(t, log, "repo create acme/mytool --internal --source="+dir+" --remote=origin --push")
}

func TestCreate_NoPush(t *testing.T) {
	logPath := setupStubGh(t)
	dir := t.TempDir()
	_, err := kitinit.Create(context.Background(), dir, kitinit.RepoConfig{
		AccountType: "personal",
		Name:        "mytool",
		Visibility:  "public",
		NoPush:      true,
	})
	require.NoError(t, err)
	log := readLog(t, logPath)
	assert.NotContains(t, log, "--push")
}

func TestCreate_GhMissing(t *testing.T) {
	t.Setenv("PATH", "/nonexistent")
	dir := t.TempDir()
	_, err := kitinit.Create(context.Background(), dir, kitinit.RepoConfig{
		AccountType: "personal",
		Name:        "x",
		Visibility:  "public",
	})
	require.Error(t, err)
	msg := err.Error()
	assert.Contains(t, msg, "gh not found")
	assert.Contains(t, msg, "https://cli.github.com")
}

func TestProtectMain_BuildsCorrectAPICall(t *testing.T) {
	logPath := setupStubGh(t)
	err := kitinit.ProtectMain(context.Background(), "foo/bar")
	require.NoError(t, err)
	log := readLog(t, logPath)
	assert.Contains(t, log, "api -X PUT /repos/foo/bar/branches/main/protection")
	assert.True(t,
		strings.Contains(log, "required_pull_request_reviews") ||
			strings.Contains(log, "enforce_admins"),
		"expected required_pull_request_reviews or enforce_admins in log, got: %s", log)
}

func TestCreate_ParsesURL(t *testing.T) {
	setupStubGh(t)
	t.Setenv("STUB_GH_OUT", "https://github.com/owner/repo")
	dir := t.TempDir()
	info, err := kitinit.Create(context.Background(), dir, kitinit.RepoConfig{
		AccountType: "personal",
		Name:        "repo",
		Visibility:  "public",
	})
	require.NoError(t, err)
	assert.Equal(t, "https://github.com/owner/repo", info.URL)
}

// TestCreate_SourceArg covers the cwd-vs-non-cwd branching of `--source`:
// when the target dir is the current working directory we want `--source=.`
// (parity with `gh repo create --source . --push --private`); otherwise we
// pass the absolute path through unchanged.
func TestCreate_SourceArg(t *testing.T) {
	cases := []struct {
		name      string
		setupDir  func(t *testing.T) string // returns dir passed to Create
		chdirToIt bool                      // whether to t.Chdir(dir) first
		wantArg   func(dir string) string   // expected --source=<value>
	}{
		{
			name: "dir_equals_cwd_uses_dot",
			setupDir: func(t *testing.T) string {
				return t.TempDir()
			},
			chdirToIt: true,
			wantArg: func(_ string) string {
				return "--source=."
			},
		},
		{
			name: "dir_differs_from_cwd_uses_absolute",
			setupDir: func(t *testing.T) string {
				return t.TempDir()
			},
			chdirToIt: false,
			wantArg: func(dir string) string {
				return "--source=" + dir
			},
		},
		{
			name: "relative_dir_resolving_to_cwd_uses_dot",
			setupDir: func(t *testing.T) string {
				// Caller passes "." (relative) while cwd is a temp dir.
				t.Chdir(t.TempDir())
				return "."
			},
			chdirToIt: false, // already chdir'd above
			wantArg: func(_ string) string {
				return "--source=."
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			logPath := setupStubGh(t)
			dir := tc.setupDir(t)
			if tc.chdirToIt {
				t.Chdir(dir)
			}
			_, err := kitinit.Create(context.Background(), dir, kitinit.RepoConfig{
				AccountType: "personal",
				Name:        "mytool",
				Visibility:  "private",
			})
			require.NoError(t, err)
			assert.Contains(t, readLog(t, logPath), tc.wantArg(dir))
		})
	}
}
