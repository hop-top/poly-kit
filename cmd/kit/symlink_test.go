// symlink_test.go covers the platform-agnostic surfaces of the
// `kit symlink` subcommand: PATH/candidate intersection, idempotent
// re-runs, force-override, and refusal-on-mismatch. The Unix
// behavior is exercised directly via os.Symlink; the Windows shim
// branch is covered by a dedicated //go:build windows test (see
// symlink_windows_test.go).

package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"hop.top/kit/go/runtime/sideeffect/real"
)

// touch creates an empty file at path with mode 0o755 so it looks
// like a buildable binary to symlinkCmd.
func touch(t *testing.T, path string) {
	t.Helper()
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("touch %s: %v", path, err)
	}
}

// installLinkHere is a thin wrapper that exercises the same code
// path the cobra RunE uses, minus the cobra plumbing. We test
// installLink directly so the Unix branch is reachable on macOS/
// Linux CI without booting cobra.
//
// Pre-pilot the helper called installLink with three args; the
// pilot migration (T-0474, ADR-0019) added sideeffect.FS +
// symlinkAdapter to the signature so dry-run can swap impls. The
// helper passes the production impls so existing tests cover the
// real-effect path.
func installLinkHere(t *testing.T, linkPath, target string, force bool) (linkResult, error) {
	t.Helper()
	return installLink(linkPath, target, force, real.FS{}, realSymlink{})
}

func TestPickCandidateDir_FirstHitWins(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("PATH separator is platform-specific; covered separately on Windows")
	}
	a := t.TempDir()
	b := t.TempDir()
	c := t.TempDir()

	pathEnv := strings.Join([]string{"/somewhere/else", a, b}, ":")
	got, err := pickCandidateDir(pathEnv, []string{c, b, a})
	if err != nil {
		t.Fatalf("pickCandidateDir err: %v", err)
	}
	// b appears earlier in candidate order than a.
	if got != b {
		t.Fatalf("want %q, got %q", b, got)
	}
}

func TestPickCandidateDir_NoneOnPath(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix-only path string")
	}
	a := t.TempDir()
	pathEnv := "/somewhere/else:/another/place"
	_, err := pickCandidateDir(pathEnv, []string{a})
	if err == nil {
		t.Fatalf("expected error when no candidate is on PATH")
	}
	if !strings.Contains(err.Error(), "PATH") {
		t.Fatalf("error should mention PATH: %v", err)
	}
}

func TestInstallLink_FreshSymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix symlink branch")
	}
	dir := t.TempDir()
	target := filepath.Join(dir, "tool")
	touch(t, target)
	link := filepath.Join(dir, "tool-link")

	result, err := installLinkHere(t, link, target, false)
	if err != nil {
		t.Fatalf("installLink: %v", err)
	}
	if result != linkResultCreated {
		t.Fatalf("want created, got %s", result)
	}
	got, err := os.Readlink(link)
	if err != nil {
		t.Fatalf("readlink: %v", err)
	}
	if got != target {
		t.Fatalf("readlink want %q, got %q", target, got)
	}
}

func TestInstallLink_IdempotentSameTarget(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix symlink branch")
	}
	dir := t.TempDir()
	target := filepath.Join(dir, "tool")
	touch(t, target)
	link := filepath.Join(dir, "tool-link")

	if _, err := installLinkHere(t, link, target, false); err != nil {
		t.Fatalf("first installLink: %v", err)
	}
	result, err := installLinkHere(t, link, target, false)
	if err != nil {
		t.Fatalf("second installLink: %v", err)
	}
	if result != linkResultUnchanged {
		t.Fatalf("want unchanged, got %s", result)
	}
}

func TestInstallLink_RefusesDifferentTarget(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix symlink branch")
	}
	dir := t.TempDir()
	t1 := filepath.Join(dir, "tool1")
	t2 := filepath.Join(dir, "tool2")
	touch(t, t1)
	touch(t, t2)
	link := filepath.Join(dir, "tool-link")

	if _, err := installLinkHere(t, link, t1, false); err != nil {
		t.Fatalf("install first: %v", err)
	}
	if _, err := installLinkHere(t, link, t2, false); err == nil {
		t.Fatalf("expected refusal when changing target without --force")
	}
}

func TestInstallLink_ForceReplacesDifferentTarget(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix symlink branch")
	}
	dir := t.TempDir()
	t1 := filepath.Join(dir, "tool1")
	t2 := filepath.Join(dir, "tool2")
	touch(t, t1)
	touch(t, t2)
	link := filepath.Join(dir, "tool-link")

	if _, err := installLinkHere(t, link, t1, false); err != nil {
		t.Fatalf("install first: %v", err)
	}
	result, err := installLinkHere(t, link, t2, true)
	if err != nil {
		t.Fatalf("force replace: %v", err)
	}
	if result != linkResultReplaced {
		t.Fatalf("want replaced, got %s", result)
	}
	got, err := os.Readlink(link)
	if err != nil {
		t.Fatalf("readlink after force: %v", err)
	}
	if got != t2 {
		t.Fatalf("link should point at t2 after force; got %q", got)
	}
}

func TestEnsureWritableDir_RejectsMissing(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "does-not-exist")
	if err := ensureWritableDir(missing); err == nil {
		t.Fatalf("expected error for missing dir")
	}
}

func TestEnsureWritableDir_AcceptsTempDir(t *testing.T) {
	if err := ensureWritableDir(t.TempDir()); err != nil {
		t.Fatalf("temp dir should be writable: %v", err)
	}
}

func TestUnixCandidateDirs_HonorsXDG(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix-only env var set")
	}
	t.Setenv("XDG_BIN_HOME", "/custom/bin")
	t.Setenv("HOME", "/home/test")
	got := unixCandidateDirs()
	if len(got) == 0 || got[0] != "/custom/bin" {
		t.Fatalf("expected XDG_BIN_HOME first, got %v", got)
	}
}

func TestSymlinkCmd_EndToEnd(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("end-to-end uses Unix symlink semantics")
	}
	// Lay out a fake project with bin/<tool>.
	proj := t.TempDir()
	binDir := filepath.Join(proj, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatalf("mkdir bin: %v", err)
	}
	target := filepath.Join(binDir, "demo")
	touch(t, target)

	// Candidate dir + PATH it.
	userBin := t.TempDir()
	t.Setenv("PATH", userBin+":"+os.Getenv("PATH"))

	dir, err := pickCandidateDir(os.Getenv("PATH"), []string{userBin})
	if err != nil {
		t.Fatalf("pickCandidateDir: %v", err)
	}
	link := filepath.Join(dir, "demo")
	res, err := installLink(link, target, false, real.FS{}, realSymlink{})
	if err != nil {
		t.Fatalf("installLink: %v", err)
	}
	if res != linkResultCreated {
		t.Fatalf("want created, got %s", res)
	}
	if _, err := os.Lstat(link); err != nil {
		t.Fatalf("link should exist: %v", err)
	}
}
