package conformance

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
)

// Marker strings the installer writes into each shim and recognizes
// on re-runs. Bump the version suffix only when the shim body must
// change in a way that downstream caches need to invalidate.
const (
	preCommitShimMarker = "VERIFY_NO_LEAK_SHIM_V1"
	commitMsgShimMarker = "VERIFY_NO_LEAK_MSG_SHIM_V1"

	githooksDir = ".githooks"
	preCommit   = "pre-commit"
	commitMsg   = "commit-msg"
)

// preCommitShim is the body written to .githooks/pre-commit. The
// marker comment is the installer's idempotency key.
const preCommitShim = `#!/bin/sh
# verify-no-leak: kit-managed shim. do not edit; re-run
#   kit conformance install-hooks
# to refresh. Marker: VERIFY_NO_LEAK_SHIM_V1
set -e
exec kit conformance verify-no-leak --staged --format=human
`

// commitMsgShim is the body written to .githooks/commit-msg.
const commitMsgShim = `#!/bin/sh
# verify-no-leak commit-msg shim. Marker: VERIFY_NO_LEAK_MSG_SHIM_V1
set -e
exec kit conformance verify-no-leak --commit-msg-file="$1" --format=human
`

// installHooksCmd returns the "install-hooks" leaf. The installer
// writes two shim scripts under .githooks/, points core.hooksPath at
// them, and refuses to clobber an existing non-kit pre-commit hook
// without --force.
func installHooksCmd() *cobra.Command {
	var (
		dryRun bool
		force  bool
		format string
		root   string
	)
	cmd := &cobra.Command{
		Use:   "install-hooks",
		Short: "Install kit-managed git hooks (pre-commit, commit-msg)",
		Long: `Install committed shim scripts under .githooks/ that
invoke kit conformance verify-no-leak on staged content and commit
messages. Idempotent; refuses to overwrite a non-kit pre-commit hook
without --force. `,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runInstallHooks(cmd, installFlags{
				dryRun: dryRun,
				force:  force,
				format: format,
				root:   root,
			})
		},
	}
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "preview changes without writing files")
	cmd.Flags().BoolVar(&force, "force", false, "overwrite a non-kit-managed pre-commit hook")
	cmd.Flags().StringVar(&format, "format", "human", "output format: `human|json`")
	cmd.Flags().StringVar(&root, "root", "", "repository root (default: git toplevel or cwd)")
	_ = cmd.Flags().MarkHidden("root")
	return cmd
}

// installFlags groups parsed install-hooks flags.
type installFlags struct {
	dryRun bool
	force  bool
	format string
	root   string
}

// installAction describes one filesystem or git-config change the
// installer will perform. Each is rendered in both human and JSON
// output so dry-run is informative.
type installAction struct {
	Kind   string `json:"kind"`             // "write" | "skip" | "config" | "config-skip" | "diff"
	Path   string `json:"path,omitempty"`   // filesystem path or git config key
	Reason string `json:"reason,omitempty"` // why this action (or skip)
	Diff   string `json:"diff,omitempty"`   // unified-ish diff (clobber refusal only)
}

// installReport is what we emit on --format=json.
type installReport struct {
	Tool    string          `json:"tool"`
	Root    string          `json:"root"`
	DryRun  bool            `json:"dry_run"`
	Actions []installAction `json:"actions"`
}

func runInstallHooks(cmd *cobra.Command, f installFlags) error {
	if f.format != "" && f.format != "human" && f.format != "json" {
		return UsageError(fmt.Sprintf("unknown format %q (want human|json)", f.format))
	}

	root, err := resolveRoot(f.root)
	if err != nil {
		return IOError("repo root lookup failed", err.Error(), "run inside a git working tree or pass --root")
	}

	report := installReport{
		Tool:   "install-hooks",
		Root:   root,
		DryRun: f.dryRun,
	}

	// Step 1: ensure .githooks/ exists.
	hooksRoot := filepath.Join(root, githooksDir)
	if st, statErr := os.Stat(hooksRoot); statErr != nil {
		if !os.IsNotExist(statErr) {
			return IOError(".githooks stat failed", statErr.Error(), "")
		}
		report.Actions = append(report.Actions, installAction{
			Kind:   "mkdir",
			Path:   hooksRoot,
			Reason: "create missing .githooks directory",
		})
		if !f.dryRun {
			if mkErr := os.MkdirAll(hooksRoot, 0o755); mkErr != nil {
				return IOError(".githooks mkdir failed", mkErr.Error(), "")
			}
		}
	} else if !st.IsDir() {
		return IOError(".githooks exists but is not a directory", hooksRoot, "remove the file or pass --root elsewhere")
	}

	// Step 2: write the two shim files (idempotent).
	for _, shim := range []struct {
		name   string
		body   string
		marker string
	}{
		{preCommit, preCommitShim, preCommitShimMarker},
		{commitMsg, commitMsgShim, commitMsgShimMarker},
	} {
		act, writeErr := planShimWrite(hooksRoot, shim.name, shim.body, shim.marker, f.force, f.dryRun)
		// Always record the action — including clobber-refusal diffs —
		// so human/JSON output reflects exactly what happened (or didn't).
		report.Actions = append(report.Actions, act)
		if writeErr != nil {
			_ = render(cmd.OutOrStdout(), f.format, report)
			return writeErr
		}
	}

	// Step 3: belt-and-suspenders — warn if .git/hooks/pre-commit
	// exists without our marker. Refuse without --force.
	if act, clobberErr := checkLegacyHook(root, f.force); clobberErr != nil {
		// Render whatever we collected so far before bailing.
		report.Actions = append(report.Actions, act)
		_ = render(cmd.OutOrStdout(), f.format, report)
		return clobberErr
	} else if act.Kind != "" {
		report.Actions = append(report.Actions, act)
	}

	// Step 4: set core.hooksPath if not already pointing at .githooks.
	cfgAct, cfgErr := planHooksPath(root, f.dryRun)
	if cfgErr != nil {
		return cfgErr
	}
	report.Actions = append(report.Actions, cfgAct)

	return render(cmd.OutOrStdout(), f.format, report)
}

// resolveRoot picks the install root in this order: explicit --root,
// git toplevel from cwd, then cwd itself. The cwd fallback supports
// "bootstrap a brand-new repo" workflows where the user runs
// install-hooks before `git init`.
func resolveRoot(explicit string) (string, error) {
	if explicit != "" {
		abs, err := filepath.Abs(explicit)
		if err != nil {
			return "", err
		}
		return abs, nil
	}
	out, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	if err == nil {
		top := strings.TrimSpace(string(out))
		if top != "" {
			return top, nil
		}
	}
	return os.Getwd()
}

// planShimWrite decides what to do with a single shim file:
// write fresh, skip-because-identical, or refresh-marker.
func planShimWrite(hooksRoot, name, body, marker string, force, dryRun bool) (installAction, error) {
	path := filepath.Join(hooksRoot, name)
	existing, readErr := os.ReadFile(path)
	switch {
	case os.IsNotExist(readErr):
		// fresh write
		act := installAction{Kind: "write", Path: path, Reason: "create new shim"}
		if !dryRun {
			if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
				return act, IOError("shim write failed", err.Error(), path)
			}
		}
		return act, nil
	case readErr != nil:
		return installAction{}, IOError("shim read failed", readErr.Error(), path)
	}
	// File exists.
	if string(existing) == body {
		return installAction{Kind: "skip", Path: path, Reason: "identical to managed shim"}, nil
	}
	if strings.Contains(string(existing), marker) {
		// Same marker, different body: refresh to current shim.
		act := installAction{Kind: "write", Path: path, Reason: "refresh kit-managed shim"}
		if !dryRun {
			if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
				return act, IOError("shim refresh failed", err.Error(), path)
			}
		}
		return act, nil
	}
	// Non-kit file in .githooks/<name>. Honor --force, else refuse.
	if !force {
		return installAction{
				Kind:   "diff",
				Path:   path,
				Reason: "existing hook is not kit-managed; pass --force to overwrite",
				Diff:   simpleDiff(string(existing), body),
			},
			UsageError(fmt.Sprintf("%s exists and is not a kit-managed shim; pass --force to overwrite", path))
	}
	act := installAction{Kind: "write", Path: path, Reason: "overwrite non-kit hook (--force)"}
	if !dryRun {
		if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
			return act, IOError("shim overwrite failed", err.Error(), path)
		}
	}
	return act, nil
}

// checkLegacyHook inspects .git/hooks/pre-commit. If it exists and
// lacks our marker, refuse without --force. core.hooksPath makes this
// path irrelevant for git itself once we flip the setting, but
// adopters can reset core.hooksPath out from under us, so we still
// warn / refuse.
func checkLegacyHook(root string, force bool) (installAction, error) {
	legacy := filepath.Join(root, ".git", "hooks", "pre-commit")
	body, err := os.ReadFile(legacy)
	if err != nil {
		// Not a git repo, or no legacy hook installed. Both fine.
		return installAction{}, nil
	}
	if strings.Contains(string(body), preCommitShimMarker) {
		return installAction{Kind: "skip", Path: legacy, Reason: "legacy hook already kit-managed"}, nil
	}
	if !force {
		return installAction{
				Kind:   "diff",
				Path:   legacy,
				Reason: "legacy .git/hooks/pre-commit exists; pass --force to leave it untouched and proceed",
				Diff:   simpleDiff(string(body), preCommitShim),
			},
			UsageError(fmt.Sprintf("%s exists and is not a kit-managed shim; pass --force to proceed (the file will be left as-is — core.hooksPath supersedes it)", legacy))
	}
	// With --force, we leave the legacy file alone; core.hooksPath
	// pointing at .githooks/ makes it dead code. Just acknowledge.
	return installAction{Kind: "skip", Path: legacy, Reason: "legacy hook left untouched (--force acknowledged)"}, nil
}

// planHooksPath ensures core.hooksPath = .githooks; no-op if already
// set correctly.
func planHooksPath(root string, dryRun bool) (installAction, error) {
	current, _ := exec.Command("git", "-C", root, "config", "--get", "core.hooksPath").Output()
	cur := strings.TrimSpace(string(current))
	if cur == githooksDir {
		return installAction{Kind: "config-skip", Path: "core.hooksPath", Reason: "already set to .githooks"}, nil
	}
	act := installAction{
		Kind:   "config",
		Path:   "core.hooksPath",
		Reason: fmt.Sprintf("set core.hooksPath=%s (was %q)", githooksDir, cur),
	}
	if dryRun {
		return act, nil
	}
	if err := exec.Command("git", "-C", root, "config", "core.hooksPath", githooksDir).Run(); err != nil {
		// Not a git repo? Surface as IOError so adopters can see why.
		return act, IOError("git config core.hooksPath failed", err.Error(), "is "+root+" a git repo?")
	}
	return act, nil
}

// simpleDiff renders a minimal unified-style diff between two short
// shell scripts. Good enough for clobber-refusal messaging; we don't
// pull in a full diff lib for this.
func simpleDiff(have, want string) string {
	var b strings.Builder
	fmt.Fprintln(&b, "--- existing")
	fmt.Fprintln(&b, "+++ kit-managed")
	for _, line := range strings.Split(strings.TrimRight(have, "\n"), "\n") {
		fmt.Fprintf(&b, "- %s\n", line)
	}
	for _, line := range strings.Split(strings.TrimRight(want, "\n"), "\n") {
		fmt.Fprintf(&b, "+ %s\n", line)
	}
	return b.String()
}

// render writes the installer report to w in the requested format.
func render(w io.Writer, format string, r installReport) error {
	switch format {
	case "json":
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(r)
	case "human", "":
		return renderInstallHuman(w, r)
	default:
		return fmt.Errorf("unknown format %q (want human|json)", format)
	}
}

func renderInstallHuman(w io.Writer, r installReport) error {
	prefix := ""
	if r.DryRun {
		prefix = "[dry-run] "
	}
	fmt.Fprintf(w, "%sinstall-hooks: root=%s\n", prefix, r.Root)
	for _, a := range r.Actions {
		switch a.Kind {
		case "mkdir":
			fmt.Fprintf(w, "  %smkdir %s — %s\n", prefix, a.Path, a.Reason)
		case "write":
			fmt.Fprintf(w, "  %swrite %s — %s\n", prefix, a.Path, a.Reason)
		case "skip":
			fmt.Fprintf(w, "  skip %s — %s\n", a.Path, a.Reason)
		case "config":
			fmt.Fprintf(w, "  %sgit config %s — %s\n", prefix, a.Path, a.Reason)
		case "config-skip":
			fmt.Fprintf(w, "  skip git config %s — %s\n", a.Path, a.Reason)
		case "diff":
			fmt.Fprintf(w, "  refuse %s — %s\n", a.Path, a.Reason)
			if a.Diff != "" {
				for _, line := range strings.Split(strings.TrimRight(a.Diff, "\n"), "\n") {
					fmt.Fprintf(w, "    %s\n", line)
				}
			}
		}
	}
	return nil
}
