package kitinit_test

import (
	"os"
	"testing"
)

// TestMain scrubs GIT_* env vars from the entire test process before any
// test runs. This matters under a pre-push hook context, where git
// exports GIT_DIR / GIT_WORK_TREE / GIT_INDEX_FILE so its hooks see the
// repo they were invoked from. Without scrubbing, every `exec.Command("git", ...)`
// in this package's tests (Init, InitialCommit, etc.) inherits those
// env vars and operates against the host repo instead of the tempdir
// under test — leading to spurious commits on the dev's feature branch
// authored by the bare repo's local user identity ("Test").
//
// Tests can still set their own GIT_CONFIG_GLOBAL or other GIT_* values
// via t.Setenv — TestMain only neutralizes what was inherited at process
// start.
func TestMain(m *testing.M) {
	for _, k := range []string{
		"GIT_DIR", "GIT_WORK_TREE", "GIT_INDEX_FILE",
		"GIT_NAMESPACE", "GIT_COMMON_DIR", "GIT_PREFIX",
		"GIT_OBJECT_DIRECTORY",
	} {
		_ = os.Unsetenv(k)
	}
	os.Exit(m.Run())
}
