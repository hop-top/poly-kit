// symlink_dryrun_e2e_test.go pins the end-to-end contract for
// `kit symlink --dry-run`: the cobra command runs, the kit-global
// flag flips the ctx, the FS / symlinker swap to dryrun impls,
// and no real disk side effects happen (T-0475, ADR-0019).
//
//go:build !windows

package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"hop.top/kit/go/console/cli"
)

func TestE2E_KitSymlink_DryRun_NoDiskSideEffects(t *testing.T) {
	// Lay out a fake project with bin/<tool>.
	proj := t.TempDir()
	binDir := filepath.Join(proj, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatalf("mkdir bin: %v", err)
	}
	target := filepath.Join(binDir, "demo")
	if err := os.WriteFile(target, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("write target: %v", err)
	}
	userBin := t.TempDir()
	t.Setenv("PATH", userBin+":"+os.Getenv("PATH"))

	// Build a kit Root and add the symlink subcommand.
	r := cli.New(cli.Config{
		Name:            "kit",
		Version:         "0.0.0",
		Short:           "kit",
		DisableValidate: true,
	})
	r.Cmd.AddCommand(symlinkCmd(r))

	link := filepath.Join(userBin, "demo")
	if _, err := os.Lstat(link); err == nil {
		t.Fatalf("test bug: link already exists at %s", link)
	}

	r.Cmd.SetArgs([]string{
		"symlink",
		"--dry-run",
		"--target", target,
		"--name", "demo",
		"--dir", userBin,
	})
	var stdout, stderr bytes.Buffer
	r.Cmd.SetOut(&stdout)
	r.Cmd.SetErr(&stderr)
	if err := r.Execute(context.Background()); err != nil {
		t.Fatalf("Execute: %v\nstderr=%s\nstdout=%s",
			err, stderr.String(), stdout.String())
	}
	// The link MUST NOT exist on disk: dry-run swap was effective.
	if _, err := os.Lstat(link); err == nil {
		t.Fatalf("dry-run created %s on disk", link)
	}
	// Stderr must mention the would-be symlink.
	if got := stderr.String() + stdout.String(); !strings.Contains(got, "would symlink") {
		t.Fatalf("output missing 'would symlink' description:\nstderr=%s\nstdout=%s",
			stderr.String(), stdout.String())
	}
}

func TestE2E_KitSymlink_NoDryRun_OptInDoesNotChangeBehaviour(t *testing.T) {
	// Verifies the opt-in registry does not break the no-flag path:
	// running `kit symlink` (no --dry-run) still writes a real link.
	proj := t.TempDir()
	binDir := filepath.Join(proj, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatalf("mkdir bin: %v", err)
	}
	target := filepath.Join(binDir, "demo2")
	if err := os.WriteFile(target, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("write target: %v", err)
	}
	userBin := t.TempDir()
	t.Setenv("PATH", userBin+":"+os.Getenv("PATH"))

	r := cli.New(cli.Config{Name: "kit", Version: "0.0.0", Short: "kit", DisableValidate: true})
	r.Cmd.AddCommand(symlinkCmd(r))
	r.Cmd.SetArgs([]string{
		"symlink",
		"--target", target,
		"--name", "demo2",
		"--dir", userBin,
	})
	var stdout, stderr bytes.Buffer
	r.Cmd.SetOut(&stdout)
	r.Cmd.SetErr(&stderr)
	if err := r.Execute(context.Background()); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	link := filepath.Join(userBin, "demo2")
	if _, err := os.Lstat(link); err != nil {
		t.Fatalf("link must exist on disk after non-dry-run run: %v", err)
	}
}
