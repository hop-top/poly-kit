// symlink.go wires the `kit symlink` cobra command. It walks $PATH,
// intersects with a platform-specific list of candidate user-bin
// directories, and links (or shims, on native Windows) the built
// binary into the first writable hit. The intent is parity with the
// auto-deploy property a `make build` workflow expects: rebuild the
// binary, the live entry point keeps pointing at the fresh artifact
// without a second install step.
//
// Spec: tlc T-0214.

package main

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/spf13/cobra"
	"hop.top/kit/go/console/cli"
	kitlog "hop.top/kit/go/console/log"
	"hop.top/kit/go/runtime/sideeffect"
	"hop.top/kit/go/runtime/sideeffect/dryrun"
	"hop.top/kit/go/runtime/sideeffect/real"
)

// symlinkOpts captures every input that drives the link/shim
// decision. It is populated from cobra flags and (when applicable)
// platform discovery.
type symlinkOpts struct {
	target string
	name   string
	dir    string
	force  bool
}

// symlinkCmd builds the `kit symlink` subcommand.
func symlinkCmd(root *cli.Root) *cobra.Command {
	opts := &symlinkOpts{}

	cmd := &cobra.Command{
		Use:   "symlink",
		Short: "Link a built binary into the user's PATH",
		Long: `Walks $PATH, intersects with the candidate user-bin dir list,
and links the target binary into the first writable hit.

On Unix (macOS, Linux, *BSD, plus MSYS2/Git Bash/WSL) the link is a
real symlink. On native Windows the command writes a .cmd shim that
forwards to the target with full args; this avoids the Admin /
Developer Mode requirement Windows places on os.Symlink.

Idempotent: re-running with the same target is a no-op. An existing
link or shim with a different target is refused unless --force.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			logger := kitlog.New(root.Viper)
			fs := pickFS(cmd)
			symlinker := pickSymlinker(cmd)

			if opts.target == "" {
				opts.target = defaultTarget()
			}
			abs, err := filepath.Abs(opts.target)
			if err != nil {
				return fmt.Errorf("resolve target %q: %w", opts.target, err)
			}
			opts.target = abs

			if opts.name == "" {
				opts.name = strings.TrimSuffix(filepath.Base(opts.target), ".exe")
			}

			info, err := os.Stat(opts.target)
			if err != nil {
				return fmt.Errorf("stat target %q: %w (run `make build` first?)", opts.target, err)
			}
			if info.IsDir() {
				return fmt.Errorf("target %q is a directory", opts.target)
			}

			dir := opts.dir
			if dir == "" {
				dir, err = pickCandidateDir(os.Getenv("PATH"), candidateDirs())
				if err != nil {
					return err
				}
			}
			if err := ensureWritableDir(dir); err != nil {
				return err
			}

			linkPath := filepath.Join(dir, opts.name+linkSuffix())
			result, err := installLink(linkPath, opts.target, opts.force, fs, symlinker)
			if err != nil {
				return err
			}

			out := cmd.OutOrStdout()
			switch result {
			case linkResultCreated:
				fmt.Fprintf(out, "linked %s -> %s\n", linkPath, opts.target)
			case linkResultUnchanged:
				fmt.Fprintf(out, "already linked: %s -> %s\n", linkPath, opts.target)
			case linkResultReplaced:
				fmt.Fprintf(out, "relinked %s -> %s (was a different target)\n", linkPath, opts.target)
			}
			logger.Debug("kit symlink complete",
				"link", linkPath,
				"target", opts.target,
				"result", result.String(),
			)
			return nil
		},
	}

	cmd.Flags().StringVar(&opts.target, "target", "", "Path to the binary to link (default: ./bin/<dir-basename>)")
	cmd.Flags().StringVar(&opts.name, "name", "", "Link name (default: basename of target)")
	cmd.Flags().StringVar(&opts.dir, "dir", "", "Override the candidate-dir search (skips PATH walk)")
	cmd.Flags().BoolVar(&opts.force, "force", false, "Replace an existing link with a different target")

	// kit symlink mutates filesystem state (links/shims) — declare
	// the side-effect tier per cli-conventions §3.5. ADR-0020 drives
	// --dry-run support off this tier: write|destructive leaves
	// accept --dry-run by default. The FS impl chosen by pickFS
	// substitutes describing impls when sideeffect.IsDryRun(ctx)=true.
	cli.SetSideEffect(cmd, cli.SideEffectWrite)
	cli.SetIdempotency(cmd, cli.IdempotencyYes)
	cli.SetTopLevelVerb(cmd)
	return cmd
}

// pickFS returns the sideeffect.FS impl appropriate for the
// command's context: dryrun.FS when --dry-run is set; real.FS
// otherwise. The dryrun impl writes its description lines to the
// command's stderr stream rather than the global default so help
// rendering and tests both observe the output.
func pickFS(cmd *cobra.Command) sideeffect.FS {
	if sideeffect.IsDryRun(cmd.Context()) {
		return dryrun.NewFS(dryrun.WithWriter(cmd.ErrOrStderr()))
	}
	return real.FS{}
}

// symlinkAdapter is the narrow seam for os.Symlink. The kit
// sideeffect.FS interface deliberately omits Symlink (the verb is
// rare; the kit-wide surface stays small). Pilot commands that need
// it use this local interface and swap a dry-run impl when needed.
type symlinkAdapter interface {
	Symlink(oldname, newname string) error
}

// realSymlink delegates to os.Symlink.
type realSymlink struct{}

func (realSymlink) Symlink(oldname, newname string) error {
	return os.Symlink(oldname, newname)
}

// dryRunSymlink prints what would happen to its writer and returns nil.
type dryRunSymlink struct {
	wi interface{ Write(p []byte) (int, error) }
}

func (d dryRunSymlink) Symlink(oldname, newname string) error {
	w := d.wi
	if w == nil {
		w = os.Stderr
	}
	fmt.Fprintf(w, "[dry-run] would symlink %s -> %s\n", newname, oldname)
	return nil
}

func pickSymlinker(cmd *cobra.Command) symlinkAdapter {
	if sideeffect.IsDryRun(cmd.Context()) {
		return dryRunSymlink{wi: cmd.ErrOrStderr()}
	}
	return realSymlink{}
}

// linkResult is a small enum describing which branch installLink took.
type linkResult int

const (
	linkResultCreated linkResult = iota
	linkResultUnchanged
	linkResultReplaced
)

// String returns a stable token for the result for logging.
func (r linkResult) String() string {
	switch r {
	case linkResultCreated:
		return "created"
	case linkResultUnchanged:
		return "unchanged"
	case linkResultReplaced:
		return "replaced"
	default:
		return "unknown"
	}
}

// defaultTarget returns ./bin/<basename(cwd)> — the convention the
// scaffolded Makefile produces with `go build -o bin/<name>`.
func defaultTarget() string {
	cwd, err := os.Getwd()
	if err != nil {
		return ""
	}
	name := filepath.Base(cwd)
	if runtime.GOOS == "windows" {
		return filepath.Join(cwd, "bin", name+".exe")
	}
	return filepath.Join(cwd, "bin", name)
}

// linkSuffix returns the platform-correct file extension for the
// link path itself. Native Windows shims are .cmd files; everywhere
// else (including MSYS2/Git Bash/WSL where runtime.GOOS reports
// linux/darwin) it's empty.
func linkSuffix() string {
	if runtime.GOOS == "windows" {
		return ".cmd"
	}
	return ""
}

// candidateDirs returns the priority-ordered list of user-bin
// directories for the current platform. /usr/local/bin is omitted on
// Unix because it typically requires sudo.
func candidateDirs() []string {
	if runtime.GOOS == "windows" {
		return windowsCandidateDirs()
	}
	return unixCandidateDirs()
}

// unixCandidateDirs evaluates $XDG_BIN_HOME → ~/.local/bin → ~/bin.
func unixCandidateDirs() []string {
	var out []string
	if v := os.Getenv("XDG_BIN_HOME"); v != "" {
		out = append(out, v)
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		out = append(out, filepath.Join(home, ".local", "bin"))
		out = append(out, filepath.Join(home, "bin"))
	}
	return out
}

// windowsCandidateDirs evaluates %USERPROFILE%\bin →
// %USERPROFILE%\.local\bin → %LOCALAPPDATA%\Programs\<tool>\bin.
// The third entry's trailing tool segment is left for the caller to
// fill; we surface the parent so PATH membership can match either
// the parent or a tool-specific child.
func windowsCandidateDirs() []string {
	var out []string
	if home := os.Getenv("USERPROFILE"); home != "" {
		out = append(out, filepath.Join(home, "bin"))
		out = append(out, filepath.Join(home, ".local", "bin"))
	}
	if local := os.Getenv("LOCALAPPDATA"); local != "" {
		out = append(out, filepath.Join(local, "Programs"))
	}
	return out
}

// pickCandidateDir walks pathEnv (PATH-separator joined), keeps only
// entries that also appear in candidates, and returns the first one
// that exists and is writable. The match is path-equality after
// filepath.Clean; case is preserved.
func pickCandidateDir(pathEnv string, candidates []string) (string, error) {
	if len(candidates) == 0 {
		return "", errSymlinkNoCandidate
	}
	pathEntries := splitPath(pathEnv)
	canonical := make(map[string]struct{}, len(pathEntries))
	for _, e := range pathEntries {
		if e == "" {
			continue
		}
		canonical[filepath.Clean(e)] = struct{}{}
	}
	for _, c := range candidates {
		clean := filepath.Clean(c)
		if _, ok := canonical[clean]; !ok {
			continue
		}
		if err := ensureWritableDir(clean); err == nil {
			return clean, nil
		}
	}
	return "", fmt.Errorf("no candidate user-bin dir is on $PATH (tried %s); "+
		"add $HOME/.local/bin to PATH or pass --dir <path>",
		strings.Join(candidates, ", "))
}

// errSymlinkNoCandidate signals the platform exposed no candidate
// dirs at all (no $HOME, no $USERPROFILE). Kept as a sentinel so
// callers can branch.
var errSymlinkNoCandidate = errors.New("no user-bin candidate dirs available on this platform")

// splitPath splits a $PATH-style string on the OS-appropriate
// separator. We don't use filepath.SplitList directly to keep the
// behavior explicit and testable.
func splitPath(s string) []string {
	sep := ":"
	if runtime.GOOS == "windows" {
		sep = ";"
	}
	return strings.Split(s, sep)
}

// ensureWritableDir verifies dir exists, is a directory, and is
// writable by the current user. Missing directories return a clear
// "does not exist" error so the caller can suggest mkdir-p.
func ensureWritableDir(dir string) error {
	info, err := os.Stat(dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("dir %q does not exist (mkdir -p first)", dir)
		}
		return fmt.Errorf("stat %q: %w", dir, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("%q is not a directory", dir)
	}
	probe, err := os.CreateTemp(dir, ".kit-symlink-probe-*")
	if err != nil {
		return fmt.Errorf("dir %q is not writable: %w", dir, err)
	}
	probeName := probe.Name()
	_ = probe.Close()
	_ = os.Remove(probeName)
	return nil
}

// installLink creates linkPath pointing at target, deferring to the
// platform-appropriate strategy. The result describes whether the
// disk state changed.
func installLink(linkPath, target string, force bool, fs sideeffect.FS, sym symlinkAdapter) (linkResult, error) {
	if runtime.GOOS == "windows" {
		return installShimWindows(linkPath, target, force, fs)
	}
	return installSymlinkUnix(linkPath, target, force, fs, sym)
}

// installSymlinkUnix is the os.Symlink path. Idempotent when the
// existing link already points at target. Mutating calls (Remove,
// Symlink) flow through the supplied sideeffect.FS / symlinkAdapter
// so --dry-run can swap describing impls; reads (Readlink, Lstat)
// stay on stdlib because they are safe and dry-run does not pretend
// reads are unsafe (ADR-0019).
func installSymlinkUnix(linkPath, target string, force bool, sfs sideeffect.FS, sym symlinkAdapter) (linkResult, error) {
	existing, err := os.Readlink(linkPath)
	switch {
	case err == nil:
		if existing == target {
			return linkResultUnchanged, nil
		}
		if !force {
			return 0, fmt.Errorf("link %q already points at %q (pass --force to replace)",
				linkPath, existing)
		}
		if err := sfs.Remove(linkPath); err != nil {
			return 0, fmt.Errorf("remove existing link: %w", err)
		}
		if err := sym.Symlink(target, linkPath); err != nil {
			return 0, fmt.Errorf("create symlink: %w", err)
		}
		return linkResultReplaced, nil
	case errors.Is(err, fs.ErrNotExist):
		if err := sym.Symlink(target, linkPath); err != nil {
			return 0, fmt.Errorf("create symlink: %w", err)
		}
		return linkResultCreated, nil
	default:
		// linkPath exists but is not a symlink — refuse without --force.
		if info, statErr := os.Lstat(linkPath); statErr == nil && info.Mode()&os.ModeSymlink == 0 {
			if !force {
				return 0, fmt.Errorf("path %q exists and is not a symlink (pass --force to replace)", linkPath)
			}
			if rmErr := sfs.Remove(linkPath); rmErr != nil {
				return 0, fmt.Errorf("remove existing file: %w", rmErr)
			}
			if symErr := sym.Symlink(target, linkPath); symErr != nil {
				return 0, fmt.Errorf("create symlink: %w", symErr)
			}
			return linkResultReplaced, nil
		}
		return 0, fmt.Errorf("readlink %q: %w", linkPath, err)
	}
}

// shimTemplate is the .cmd file body. %~dp0 is the script's own
// directory, but we expand to the absolute target at write time so
// rebuilds via `make build` continue to land at a stable path. %*
// forwards every arg.
const shimTemplate = "@echo off\r\n\"%s\" %%*\r\n"

// installShimWindows writes a .cmd shim. Idempotent when the
// existing shim's target line matches. The mutating WriteFile flows
// through the supplied sideeffect.FS so --dry-run can swap a
// describing impl; reads stay on stdlib (ADR-0019).
func installShimWindows(linkPath, target string, force bool, sfs sideeffect.FS) (linkResult, error) {
	desired := fmt.Sprintf(shimTemplate, target)
	existing, err := os.ReadFile(linkPath)
	switch {
	case err == nil:
		if string(existing) == desired {
			return linkResultUnchanged, nil
		}
		if !force {
			return 0, fmt.Errorf("shim %q already exists with a different target (pass --force to replace)", linkPath)
		}
		if err := sfs.WriteFile(linkPath, []byte(desired), 0o755); err != nil {
			return 0, fmt.Errorf("write shim: %w", err)
		}
		return linkResultReplaced, nil
	case errors.Is(err, fs.ErrNotExist):
		if err := sfs.WriteFile(linkPath, []byte(desired), 0o755); err != nil {
			return 0, fmt.Errorf("write shim: %w", err)
		}
		return linkResultCreated, nil
	default:
		return 0, fmt.Errorf("read shim %q: %w", linkPath, err)
	}
}
