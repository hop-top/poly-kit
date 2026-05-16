// symlink_dryrun_test.go pins the pilot migration of `kit symlink`
// onto sideeffect.FS / symlinkAdapter (T-0474, ADR-0019). When
// installLink runs with the dryrun impls it MUST NOT touch disk.
//
//go:build !windows

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"hop.top/kit/go/runtime/sideeffect/dryrun"
)

// TestInstallLink_DryRun_DoesNotTouchDisk pins the dry-run swap.
// With dryrun.NewFS + dryRunSymlink, installLink reports the
// described action without creating a symlink on disk.
func TestInstallLink_DryRun_DoesNotTouchDisk(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "demo")
	if err := os.WriteFile(target, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("write target: %v", err)
	}
	link := filepath.Join(dir, "demo-link")

	var buf bytes.Buffer
	fs := dryrun.NewFS(dryrun.WithWriter(&buf))
	sym := dryRunSymlink{wi: &buf}

	res, err := installLink(link, target, false, fs, sym)
	if err != nil {
		t.Fatalf("installLink dry-run: %v", err)
	}
	if res != linkResultCreated {
		t.Fatalf("res: got %s want created", res)
	}
	if _, err := os.Lstat(link); err == nil {
		t.Fatalf("dry-run must not create %s on disk", link)
	}
	if !strings.Contains(buf.String(), "would symlink") {
		t.Fatalf("dry-run output missing symlink description:\n%s", buf.String())
	}
}
