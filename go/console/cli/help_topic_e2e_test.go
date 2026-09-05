package cli_test

// E2E coverage for the `help <topic>` contract.
//
// Exit codes are the contract here — a help topic that names a command
// must exit 0, one that names nothing must be a usage error at exit 2 —
// and `go run` masks a binary's exit code, so these run a real built
// binary and read its status. The in-process cases live in cli_test.go;
// this file exists for the codes.

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var (
	helpStubOnce sync.Once
	helpStubPath string
	helpStubErr  error
)

// buildHelpStub compiles testdata/stub-help-topics once per package
// run and returns the binary's path. -buildvcs=false keeps the build
// working inside a bare worktree, matching the other stub builders in
// this repo.
func buildHelpStub(t *testing.T) string {
	t.Helper()
	helpStubOnce.Do(func() {
		dir, err := os.MkdirTemp("", "cli-help-stub-*")
		if err != nil {
			helpStubErr = fmt.Errorf("mkdir tmpdir: %w", err)
			return
		}
		path := filepath.Join(dir, "stubhelp")
		build := exec.Command("go", "build", "-buildvcs=false", "-o", path,
			"./testdata/stub-help-topics")
		if out, err := build.CombinedOutput(); err != nil {
			helpStubErr = fmt.Errorf("build stub: %w\n%s", err, out)
			return
		}
		helpStubPath = path
	})
	if helpStubErr != nil {
		t.Fatalf("buildHelpStub: %v", helpStubErr)
	}
	return helpStubPath
}

var helpStubANSI = regexp.MustCompile(`\x1b\[[^m]*m`)

// hermeticStubEnv builds the stub's environment from nothing rather
// than inheriting the runner's, so these assertions cannot be moved by
// a developer's or CI's ambient configuration.
//
// That matters more here than it would for most exec-based tests: the
// stub is a full kit CLI, and kit resolves config from the environment
// on several paths. viper's AutomaticEnv binds any STUBHELP_* variable
// to a matching flag; the XDG dirs steer config, data, state and cache
// lookup; NO_COLOR and HOP_QUIET_HINTS change what is written. Any of
// those could alter an exit code or a first line, which is exactly
// what these tests read.
//
// Every entry below is present for a reason:
//
//   - HOME because Go and the OS both consult it, and a stub with none
//     may fall back to reading the real user's home.
//   - TMPDIR pointed at the test's own dir so anything written lands
//     there and is cleaned up with it.
//   - XDG_{CONFIG,DATA,STATE,CACHE}_HOME pointed at empty test-scoped
//     dirs, so config discovery finds nothing rather than the
//     developer's real files.
//   - NO_COLOR so output is plain regardless of the terminal.
//
// Nothing is inherited, not even PATH: the stub execs no child, and it
// was verified to run every case this file asserts under a completely
// empty environment with identical exit codes and output. So no
// ambient variable is silently load-bearing, and STUBHELP_*, HOP_*,
// KIT_*, TERM and the rest are deliberately absent.
func hermeticStubEnv(t *testing.T) []string {
	t.Helper()
	dir := t.TempDir()
	return []string{
		"HOME=" + dir,
		"TMPDIR=" + dir,
		"XDG_CONFIG_HOME=" + filepath.Join(dir, "config"),
		"XDG_DATA_HOME=" + filepath.Join(dir, "data"),
		"XDG_STATE_HOME=" + filepath.Join(dir, "state"),
		"XDG_CACHE_HOME=" + filepath.Join(dir, "cache"),
		"NO_COLOR=1",
	}
}

// runHelpStub invokes the built stub and returns its exit code and its
// combined, ANSI-stripped output.
func runHelpStub(t *testing.T, args ...string) (int, string) {
	t.Helper()
	cmd := exec.Command(buildHelpStub(t), args...)
	cmd.Env = hermeticStubEnv(t)
	out, err := cmd.CombinedOutput()
	code := 0
	if err != nil {
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) {
			t.Fatalf("run stub %v: %v", args, err)
		}
		code = exitErr.ExitCode()
	}
	return code, helpStubANSI.ReplaceAllString(string(out), "")
}

// TestHelpTopicE2E_CommandExitsZero: `help <command>` prints that
// command's help and exits 0. This is the defect the contract exists
// for — a valid command name used to be read as a help group ID and
// refused with the generic code.
func TestHelpTopicE2E_CommandExitsZero(t *testing.T) {
	t.Parallel()
	code, out := runHelpStub(t, "help", "init")
	assert.Equal(t, 0, code, "help for a command must exit 0")
	assert.Contains(t, out, "Initialize things",
		"help for a command must render that command's own help")
	assert.NotContains(t, out, "unknown help",
		"a command name must not be diagnosed as an unknown topic")
}

// TestHelpTopicE2E_NestedCommandPath: the operand is a command path,
// not a single word, so a nested command is reachable to whatever
// depth the tree goes.
func TestHelpTopicE2E_NestedCommandPath(t *testing.T) {
	t.Parallel()
	code, out := runHelpStub(t, "help", "widget", "add")
	assert.Equal(t, 0, code, "help for a nested command path must exit 0")
	assert.Contains(t, out, "Add a widget",
		"help must render the leaf command's own help")
}

// TestHelpTopicE2E_GroupExitsZero: a registered group ID that names no
// command still renders the group's commands.
func TestHelpTopicE2E_GroupExitsZero(t *testing.T) {
	t.Parallel()
	code, out := runHelpStub(t, "help", "management")
	assert.Equal(t, 0, code, "help for a group must exit 0")
	assert.Contains(t, out, "MANAGEMENT", "the group title must head the listing")
	assert.Contains(t, out, "config", "the group's command must be listed")
	assert.NotContains(t, out, "Initialize things",
		"a group listing must not carry other groups' commands")
}

// TestHelpTopicE2E_All: "help all" reveals every group.
func TestHelpTopicE2E_All(t *testing.T) {
	t.Parallel()
	code, out := runHelpStub(t, "help", "all")
	assert.Equal(t, 0, code, "help all must exit 0")
	assert.Contains(t, out, "MANAGEMENT", "management group must appear")
	assert.Contains(t, out, "EXTRAS", "custom group must appear")
	assert.Contains(t, out, "init", "default group command must appear")
}

// TestHelpTopicE2E_UnknownIsUsage: an operand naming neither a command
// nor a group is a usage error — exit 2 per the taxonomy, the same
// code an unknown subcommand gets, not the generic 1.
func TestHelpTopicE2E_UnknownIsUsage(t *testing.T) {
	t.Parallel()
	code, out := runHelpStub(t, "help", "nosuch")
	assert.Equal(t, 2, code, "an unresolvable help topic must exit 2 (usage)")
	assert.NotEqual(t, 1, code, "it must not fall back to the generic code")
	assert.Contains(t, strings.ToLower(out), "unknown help topic",
		"the refusal must name what it could not resolve")
}

// TestHelpTopicE2E_PartialPathIsUsage: a path that resolves only part
// way is unresolvable, not help for the ancestor it stopped at. Cobra's
// stock help command would print the ancestor's help and exit 0; this
// contract refuses instead.
func TestHelpTopicE2E_PartialPathIsUsage(t *testing.T) {
	t.Parallel()
	code, out := runHelpStub(t, "help", "widget", "nosuch")
	assert.Equal(t, 2, code, "a partially-resolving path must exit 2 (usage)")
	assert.Contains(t, out, "widget nosuch",
		"the refusal must quote the whole topic it could not resolve")
}

// TestHelpTopicE2E_BareHelp: `help` with no operand keeps working and
// exits 0.
func TestHelpTopicE2E_BareHelp(t *testing.T) {
	t.Parallel()
	code, _ := runHelpStub(t, "help")
	assert.Equal(t, 0, code, "bare help must exit 0")
}

// TestHelpTopicE2E_CommandWinsOverGroup pins the documented tie-break:
// when a group ID and a command share a name, `help <name>` is the
// command's help. The stub's "extras" command shadows the "extras"
// group for exactly this assertion.
func TestHelpTopicE2E_CommandWinsOverGroup(t *testing.T) {
	t.Parallel()
	code, out := runHelpStub(t, "help", "extras")
	require.Equal(t, 0, code, "the shadowing command's help must exit 0")
	assert.Contains(t, out, "Extras command shadowing the group ID",
		"the command must win the name")
	assert.NotContains(t, out, "bonus",
		"the group listing must not be what a shadowed name renders")
}

// TestHelpTopicE2E_ShadowedGroupReachableByFlag: the tie-break costs a
// shadowed group nothing, because the --help-<id> flag form still
// reaches it.
func TestHelpTopicE2E_ShadowedGroupReachableByFlag(t *testing.T) {
	t.Parallel()
	code, out := runHelpStub(t, "--help-extras")
	assert.Equal(t, 0, code, "--help-<id> must exit 0")
	assert.Contains(t, out, "EXTRAS", "the group title must head the listing")
	assert.Contains(t, out, "bonus", "the group's command must be listed")
}

// TestHelpTopicE2E_HelpAllFlag: the --help-all flag form is untouched
// by the topic classification, and additionally reveals the kit-owned
// plumbing flags that default --help suppresses.
func TestHelpTopicE2E_HelpAllFlag(t *testing.T) {
	t.Parallel()
	code, out := runHelpStub(t, "--help-all")
	assert.Equal(t, 0, code, "--help-all must exit 0")
	assert.Contains(t, out, "MANAGEMENT", "management group must appear")
	assert.Contains(t, out, "EXTRAS", "custom group must appear")
	assert.Contains(t, out, "--config",
		"--help-all reveals the hidden plumbing flags")
}

// TestHelpTopicE2E_HelpAllSubcommandKeepsFlagsHidden guards the one
// documented asymmetry between the two "everything" forms: `help all`
// reveals the groups but not the plumbing flags. Long-standing shipped
// behavior, pinned so a later tidy-up is a deliberate choice.
func TestHelpTopicE2E_HelpAllSubcommandKeepsFlagsHidden(t *testing.T) {
	t.Parallel()
	_, out := runHelpStub(t, "help", "all")
	assert.NotContains(t, out, "--config",
		"help all must not reveal the plumbing flags --help-all does")
}
