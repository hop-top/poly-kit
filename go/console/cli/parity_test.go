//go:build parity

// Package cli_test contains a cross-language CLI parity test suite for spaced.
//
// It builds the Go binary, and locates the TS and Python entry points, then
// runs every hop-top CLI contract behavior against all three implementations
// asserting identical semantics (modulo language-specific formatting details).
//
// Run with:
//
//	go test -tags parity ./cli/... -v -run TestParity
//
// Prerequisites:
//   - pnpm + npx + tsx available on PATH (for TS)
//   - Python venv at sdk/py/.venv/bin/python (for Python)
package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ── TestMain — one-time build of all binaries ────────────────────────────────

var (
	parityGoBin   string
	parityTsEntry string
	parityPyEntry string
	parityPyVenv  string
	parityTmpDir  string
	parityRoot    string
)

func TestMain(m *testing.M) {
	root := findModuleRoot()
	if root == "" {
		fmt.Fprintln(os.Stderr, "parity: could not find go.mod / module root")
		os.Exit(1)
	}
	parityRoot = root

	tmp, err := os.MkdirTemp("", "parity-*")
	if err != nil {
		fmt.Fprintln(os.Stderr, "parity: MkdirTemp:", err)
		os.Exit(1)
	}
	parityTmpDir = tmp

	// ── Go binary ────────────────────────────────────────────────────────
	parityGoBin = filepath.Join(tmp, "spaced-go")
	if runtime.GOOS == "windows" {
		parityGoBin += ".exe"
	}
	build := exec.Command("go", "build", "-buildvcs=false",
		"-o", parityGoBin, filepath.Join(root, "examples", "spaced", "go"))
	build.Dir = root
	if out, err := build.CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "parity: go build failed: %v\n%s", err, out)
		os.Exit(1)
	}

	// ── TS deps — ensure node_modules are populated for esbuild ─────────
	tsDir := filepath.Join(root, "sdk", "ts")
	installCtx, installCancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer installCancel()
	pnpmInstall := exec.CommandContext(installCtx, "pnpm", "install", "--ignore-scripts")
	pnpmInstall.Dir = root

	if out, err := pnpmInstall.CombinedOutput(); err != nil {
		if installCtx.Err() == context.DeadlineExceeded {
			fmt.Fprintln(os.Stderr, "parity: pnpm install timed out after 60s")
		}
		fmt.Fprintf(os.Stderr, "parity: pnpm install (ts) failed: %v\n%s", err, out)
		os.Exit(1)
	}

	// ── TS bundle via esbuild (node startup ~50ms vs tsx ~1.5s) ──────────
	// Bundle dir mirrors the source nesting so __dirname-relative paths
	// still resolve. The TS commands use path.resolve(__dirname, "../../x")
	// so we nest the bundle two levels deep and place symlinks at the
	// root of tmp/ for any files referenced that way.
	bundleDir := filepath.Join(tmp, "a", "b")
	os.MkdirAll(bundleDir, 0o755)
	tsBundle := filepath.Join(bundleDir, "spaced.cjs")
	// import.meta.url must resolve to the original source so parity.ts
	// can locate contracts/parity/parity.json via its relative-path logic.
	parityTsSrc := filepath.Join(root, "sdk", "ts", "src", "tui", "parity.ts")
	metaURL := "file://" + parityTsSrc
	esbuildBin := filepath.Join(root, "sdk", "ts", "node_modules", ".bin", "esbuild")
	esbuild := exec.Command(esbuildBin,
		"--bundle", "--platform=node", "--format=cjs",
		"--define:import.meta.url="+fmt.Sprintf("%q", metaURL),
		"--outfile="+tsBundle,
		filepath.Join(root, "examples", "spaced", "ts", "spaced.ts"))
	esbuild.Dir = tsDir

	if out, err := esbuild.CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "parity: esbuild bundle failed: %v\n%s", err, out)
		os.Exit(1)
	}
	parityTsEntry = tsBundle

	// Symlink toolspec YAML: __dirname="tmp/a/b", resolve("../../spaced.toolspec.yaml")
	// => "tmp/spaced.toolspec.yaml"
	specSrc := filepath.Join(root, "examples", "spaced", "spaced.toolspec.yaml")
	os.Symlink(specSrc, filepath.Join(tmp, "spaced.toolspec.yaml"))

	// parity.ts's third candidate is `process.cwd() + contracts/parity/parity.json`.
	// We run node with cwd=root below so that resolves correctly.

	// ── Python paths ─────────────────────────────────────────────────────
	parityPyEntry = filepath.Join(root, "examples", "spaced", "py", "spaced.py")
	parityPyVenv = filepath.Join(root, "sdk", "py", ".venv", "bin", "python")
	if runtime.GOOS == "windows" {
		parityPyVenv = filepath.Join(root, "sdk", "py", ".venv", "Scripts", "python.exe")
	}

	code := m.Run()
	os.RemoveAll(tmp)
	os.Exit(code)
}

// findModuleRoot walks up from cwd to find go.mod (no *testing.T needed).
func findModuleRoot() string {
	dir, err := filepath.Abs(".")
	if err != nil {
		return ""
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

// ── Harness ──────────────────────────────────────────────────────────────────

// binary wraps a spaced implementation as an executable.
type binary struct {
	lang string
	run  func(args ...string) *exec.Cmd
}

// result captures the output + exit code of a spaced invocation.
type result struct {
	lang   string
	stdout string
	stderr string
	code   int
}

func (r result) combined() string { return r.stdout + r.stderr }

// parityHarness returns runners for all three langs using pre-built binaries
// from TestMain.
func parityHarness(t *testing.T) []binary {
	t.Helper()

	root := moduleRoot(t)
	pyDir := filepath.Join(root, "sdk", "py")

	return []binary{
		{lang: "go", run: func(args ...string) *exec.Cmd {
			return exec.Command(parityGoBin, args...)
		}},
		{lang: "ts", run: func(args ...string) *exec.Cmd {
			cmd := exec.Command("node", append([]string{parityTsEntry}, args...)...)
			cmd.Dir = parityRoot
			return cmd
		}},
		{lang: "py", run: func(args ...string) *exec.Cmd {
			cmd := exec.Command(parityPyVenv, append([]string{parityPyEntry}, args...)...)
			cmd.Dir = pyDir
			return cmd
		}},
	}
}

// invoke runs a binary with args and returns a result.
func invoke(b binary, args ...string) result {
	cmd := b.run(args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	// Give each invocation 30s — tsx startup can be slow.
	done := make(chan error, 1)
	_ = cmd.Start()
	go func() { done <- cmd.Wait() }()

	var runErr error
	select {
	case runErr = <-done:
	case <-time.After(30 * time.Second):
		_ = cmd.Process.Kill()
		runErr = cmd.Wait()
	}

	code := 0
	if runErr != nil {
		if exitErr, ok := runErr.(*exec.ExitError); ok {
			code = exitErr.ExitCode()
		} else {
			code = 1
		}
	}

	return result{
		lang:   b.lang,
		stdout: stdout.String(),
		stderr: stderr.String(),
		code:   code,
	}
}

// forAll runs fn against every binary and collects sub-test failures per lang.
func forAll(t *testing.T, bins []binary, fn func(t *testing.T, b binary)) {
	t.Helper()
	for _, b := range bins {
		b := b
		t.Run(b.lang, func(t *testing.T) {
			t.Parallel()
			fn(t, b)
		})
	}
}

// moduleRoot finds the worktree root (directory containing go.mod).
func moduleRoot(t *testing.T) string {
	t.Helper()
	dir, err := filepath.Abs(".")
	require.NoError(t, err)
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("could not find go.mod / module root")
		}
		dir = parent
	}
}

// ── Contract tests ────────────────────────────────────────────────────────────

// TestParityVersion: --version → "spaced v0.1.0", exit 0.
func TestParityVersion(t *testing.T) {
	bins := parityHarness(t)
	forAll(t, bins, func(t *testing.T, b binary) {
		r := invoke(b, "--version")
		assert.Equal(t, 0, r.code, "%s: --version must exit 0", b.lang)
		assert.Contains(t, stripANSI(r.combined()), "spaced v0.1.0",
			"%s: --version must contain 'spaced v0.1.0'", b.lang)
	})
}

// TestParityHelp: --help exits 0, contains name + description, no "help" or
// "completion" subcommand listed. Disclaimer must appear BEFORE the first
// Commands/Options section.
func TestParityHelp(t *testing.T) {
	bins := parityHarness(t)
	forAll(t, bins, func(t *testing.T, b binary) {
		r := invoke(b, "--help")
		assert.Equal(t, 0, r.code, "%s: --help must exit 0", b.lang)
		plain := stripANSI(r.combined())
		assert.Contains(t, plain, "spaced", "%s: --help must contain tool name", b.lang)
		// No "help" or "completion" as subcommands.
		assert.NotRegexp(t, `(?m)^\s+help\s`, plain,
			"%s: 'help' must not appear as a subcommand", b.lang)
		assert.NotRegexp(t, `(?m)^\s+completion\s`, plain,
			"%s: 'completion' must not appear as a subcommand", b.lang)
		// Disclaimer must appear BEFORE the first Commands/Options section.
		disclaimerIdx := strings.Index(plain, "Not affiliated")
		commandsIdx := -1
		for _, keyword := range []string{"COMMANDS", "Commands", "Options"} {
			if idx := strings.Index(plain, keyword); idx != -1 {
				if commandsIdx == -1 || idx < commandsIdx {
					commandsIdx = idx
				}
			}
		}
		assert.NotEqual(t, -1, disclaimerIdx,
			"%s: disclaimer 'Not affiliated' must appear in --help output", b.lang)
		assert.NotEqual(t, -1, commandsIdx,
			"%s: COMMANDS/Options section must appear in --help output", b.lang)
		if disclaimerIdx != -1 && commandsIdx != -1 {
			assert.Less(t, disclaimerIdx, commandsIdx,
				"%s: disclaimer must appear BEFORE the Commands/Options section", b.lang)
		}
	})
}

// TestParityFlagsExactSet: every lang must expose exactly the cross-lang
// contract flags — no more, no less. A new flag added in one lang fails
// here until all three match.
//
// Contract flags (sorted): --format --help --help-all --help-management
//   --no-color --no-hints --quiet --telemetry --verbose --version
func TestParityFlagsExactSet(t *testing.T) {
	bins := parityHarness(t)
	// Common flags across all three langs. Python auto-generates
	// --help-commands for the visible default group; Go/TS do not.
	// --telemetry is the kit-telemetry opt-in mode flag, mirrored
	// across all three spaced demos (adopter pattern).
	common := []string{"--format", "--help", "--help-all", "--help-management",
		"--no-color", "--no-hints", "--quiet", "--telemetry", "--verbose", "--version"}
	pyExtra := []string{"--help-commands", "--stream"}

	flagRE := regexp.MustCompile(`--[\w-]+`)

	forAll(t, bins, func(t *testing.T, b binary) {
		r := invoke(b, "--help")
		assert.Equal(t, 0, r.code, "%s: --help must exit 0", b.lang)
		plain := stripANSI(r.combined())

		// Extract only lines after the FLAGS section header.
		flagsIdx := strings.Index(plain, "FLAGS")
		require.NotEqual(t, -1, flagsIdx, "%s: FLAGS section not found", b.lang)
		flagsSection := plain[flagsIdx:]

		seen := map[string]struct{}{}
		for _, m := range flagRE.FindAllString(flagsSection, -1) {
			seen[m] = struct{}{}
		}
		got := make([]string, 0, len(seen))
		for f := range seen {
			got = append(got, f)
		}
		sort.Strings(got)

		want := make([]string, len(common))
		copy(want, common)
		if b.lang == "py" {
			want = append(want, pyExtra...)
			sort.Strings(want)
		}

		assert.Equal(t, want, got,
			"%s: FLAGS section must contain exactly the cross-lang contract flags", b.lang)
	})
}

// TestParityHelpNoColor: --help --no-color produces no ANSI escapes.
func TestParityHelpNoColor(t *testing.T) {
	bins := parityHarness(t)
	forAll(t, bins, func(t *testing.T, b binary) {
		r := invoke(b, "--no-color", "--help")
		assert.Equal(t, 0, r.code)
		assert.False(t, hasANSI(r.combined()),
			"%s: --help --no-color must not contain ANSI escapes\ngot: %q", b.lang, r.combined())
	})
}

// TestParityHelpDisclaimer: --help footer contains the sponsorship disclaimer.
func TestParityHelpDisclaimer(t *testing.T) {
	bins := parityHarness(t)
	forAll(t, bins, func(t *testing.T, b binary) {
		r := invoke(b, "--help")
		assert.Equal(t, 0, r.code)
		plain := stripANSI(r.combined())
		assert.Contains(t, plain, "Not affiliated",
			"%s: --help must contain the disclaimer", b.lang)
		assert.Contains(t, plain, "sponsorship",
			"%s: --help must mention sponsorship", b.lang)
	})
}

// TestParityNoHelpSubcommand: "spaced help" must not succeed as a subcommand.
// Contract: no registered "help" subcommand. Cobra hides it but it still
// exits 0 (shows root help); TS/Python error out. We assert it does NOT
// behave like a domain command — output must not contain any mission/fleet
// data (i.e. it's not routing to a business subcommand named "help").
func TestParityNoHelpSubcommand(t *testing.T) {
	bins := parityHarness(t)
	forAll(t, bins, func(t *testing.T, b binary) {
		r := invoke(b, "help")
		plain := stripANSI(r.combined())
		// Must not look like a domain command response.
		assert.NotContains(t, plain, "MISSION",
			"%s: 'help' must not route to a domain command", b.lang)
		assert.NotContains(t, plain, "VEHICLE",
			"%s: 'help' must not route to a domain command", b.lang)
	})
}

// TestParityCompletionSubcommand: "spaced completion" is a valid management
// command (hidden from default help). It must not dump raw shell completion
// scripts — it should show help or subcommand listing instead.
func TestParityCompletionSubcommand(t *testing.T) {
	bins := parityHarness(t)
	forAll(t, bins, func(t *testing.T, b binary) {
		r := invoke(b, "completion")
		plain := stripANSI(r.combined())
		// Must not dump raw shell completion scripts (function bodies).
		assert.NotRegexp(t, `_spaced_completions|compdef|complete -F`,
			plain, "%s: must not dump raw completion scripts", b.lang)
	})
}

// TestParityUnknownCommand: unknown subcommand must not silently succeed.
// Framework behavior varies:
//   - Go/cobra+fang: exits 0, shows root help (no domain output)
//   - Commander (TS): exits 1, prints error to stderr
//   - Typer (Python): exits 2, prints error
//
// Contract: output must not contain domain data (missions, vehicles) —
// the unknown command must not have been routed to a business handler.
func TestParityUnknownCommand(t *testing.T) {
	bins := parityHarness(t)
	forAll(t, bins, func(t *testing.T, b binary) {
		r := invoke(b, "bogus-command-xyz")
		plain := stripANSI(r.combined())
		// Must produce some output.
		assert.NotEmpty(t, strings.TrimSpace(plain),
			"%s: unknown command must produce output", b.lang)
		// Must not route to a domain handler — no mission/fleet data.
		assert.NotRegexp(t, `(?i)(MARKET MOOD|VEHICLE.*Type|mission.*inspect)`, plain,
			"%s: unknown command must not route to domain handler\ngot: %q", b.lang, plain)
		// Must not contain the unknown command name as a successful result.
		assert.NotContains(t, strings.ToLower(plain), "bogus-command-xyz launched",
			"%s: unknown command must not appear as a successful launch", b.lang)
	})
}

// TestParityMissionList: mission list exits 0, output contains mission names.
func TestParityMissionList(t *testing.T) {
	bins := parityHarness(t)
	forAll(t, bins, func(t *testing.T, b binary) {
		r := invoke(b, "mission", "list")
		assert.Equal(t, 0, r.code, "%s: mission list must exit 0\nstderr: %s", b.lang, r.stderr)
		plain := stripANSI(r.combined())
		assert.Contains(t, plain, "Starman", "%s: mission list must contain Starman", b.lang)
		assert.Contains(t, plain, "RUD", "%s: mission list must contain RUD", b.lang)
	})
}

// TestParityFormatJSON: mission list --format json produces valid JSON.
// Accepts either a bare array or a provenance envelope {"data": [...], "_meta": {...}}.
func TestParityFormatJSON(t *testing.T) {
	bins := parityHarness(t)
	forAll(t, bins, func(t *testing.T, b binary) {
		r := invoke(b, "mission", "list", "--format", "json")
		assert.Equal(t, 0, r.code, "%s: --format json must exit 0\nstderr: %s", b.lang, r.stderr)
		plain := strings.TrimSpace(stripANSI(r.stdout))

		// Try bare array first.
		var arr []map[string]any
		if err := json.Unmarshal([]byte(plain), &arr); err == nil {
			require.NotEmpty(t, arr, "%s: JSON array must not be empty", b.lang)
			return
		}

		// Try provenance envelope {"data": [...], "_meta": {...}}.
		var envelope struct {
			Data []map[string]any `json:"data"`
			Meta map[string]any   `json:"_meta"`
		}
		err := json.Unmarshal([]byte(plain), &envelope)
		require.NoError(t, err,
			"%s: --format json must produce a valid JSON array or {data,_meta} envelope\ngot: %q",
			b.lang, plain)
		require.NotEmpty(t, envelope.Data,
			"%s: envelope .data must not be empty", b.lang)
		require.NotNil(t, envelope.Meta,
			"%s: envelope ._meta must be present", b.lang)
	})
}

// TestParityFormatYAML: mission list --format yaml produces YAML output.
func TestParityFormatYAML(t *testing.T) {
	bins := parityHarness(t)
	forAll(t, bins, func(t *testing.T, b binary) {
		r := invoke(b, "mission", "list", "--format", "yaml")
		assert.Equal(t, 0, r.code, "%s: --format yaml must exit 0\nstderr: %s", b.lang, r.stderr)
		plain := stripANSI(r.stdout)
		// YAML arrays start with "- " lines.
		assert.Contains(t, plain, "-", "%s: --format yaml must contain YAML list markers", b.lang)
	})
}

// TestParityNoColor: mission list --no-color produces no ANSI escapes.
func TestParityNoColor(t *testing.T) {
	bins := parityHarness(t)
	forAll(t, bins, func(t *testing.T, b binary) {
		r := invoke(b, "--no-color", "mission", "list")
		assert.Equal(t, 0, r.code, "%s: --no-color must exit 0", b.lang)
		assert.False(t, hasANSI(r.combined()),
			"%s: --no-color must strip all ANSI escapes\ngot: %q", b.lang, r.combined())
	})
}

// TestParityPositionalArg: mission inspect <name> exits 0, shows mission data.
func TestParityPositionalArg(t *testing.T) {
	bins := parityHarness(t)
	forAll(t, bins, func(t *testing.T, b binary) {
		r := invoke(b, "mission", "inspect", "starman")
		assert.Equal(t, 0, r.code, "%s: mission inspect starman must exit 0\nstderr: %s", b.lang, r.stderr)
		plain := stripANSI(r.combined())
		assert.Contains(t, plain, "Starman", "%s: inspect output must contain mission name", b.lang)
		assert.Contains(t, plain, "Falcon Heavy", "%s: inspect output must contain vehicle", b.lang)
	})
}

// TestParityUnknownMission: mission inspect <bogus> → exit 1, stderr message.
func TestParityUnknownMission(t *testing.T) {
	bins := parityHarness(t)
	forAll(t, bins, func(t *testing.T, b binary) {
		r := invoke(b, "mission", "inspect", "bogus-mission-xyz")
		assert.Equal(t, 1, r.code,
			"%s: unknown mission must exit 1", b.lang)
		assert.NotEmpty(t, strings.TrimSpace(r.combined()),
			"%s: unknown mission must produce an error message", b.lang)
	})
}

// TestParityDryRun: launch <mission> --dry-run exits 0, output mentions dry run.
func TestParityDryRun(t *testing.T) {
	bins := parityHarness(t)
	forAll(t, bins, func(t *testing.T, b binary) {
		r := invoke(b, "launch", "starman", "--dry-run")
		assert.Equal(t, 0, r.code, "%s: --dry-run must exit 0\nstderr: %s", b.lang, r.stderr)
		plain := stripANSI(r.combined())
		assert.Regexp(t, `(?i)dry.?run`, plain,
			"%s: --dry-run output must mention dry run", b.lang)
	})
}

// TestParityCommaListFlag: launch --payload cargo,crew shows both values.
func TestParityCommaListFlag(t *testing.T) {
	bins := parityHarness(t)
	forAll(t, bins, func(t *testing.T, b binary) {
		r := invoke(b, "launch", "starman", "--payload", "cargo,crew", "--dry-run")
		assert.Equal(t, 0, r.code, "%s: --payload comma-list must exit 0\nstderr: %s", b.lang, r.stderr)
		plain := stripANSI(r.combined())
		assert.Contains(t, plain, "cargo", "%s: comma-list must contain 'cargo'", b.lang)
		assert.Contains(t, plain, "crew", "%s: comma-list must contain 'crew'", b.lang)
	})
}

// TestParityShortFlag: launch -o <file> writes a file.
func TestParityShortFlag(t *testing.T) {
	bins := parityHarness(t)
	forAll(t, bins, func(t *testing.T, b binary) {
		outFile := filepath.Join(t.TempDir(), b.lang+"-report.json")
		r := invoke(b, "launch", "starman", "-o", outFile)
		assert.Equal(t, 0, r.code, "%s: -o must exit 0\nstderr: %s", b.lang, r.stderr)
		_, err := os.Stat(outFile)
		assert.NoError(t, err, "%s: -o must write output file at %s", b.lang, outFile)
	})
}

// TestParityNestedSubcommand: telemetry get <mission> exits 0.
func TestParityNestedSubcommand(t *testing.T) {
	bins := parityHarness(t)
	forAll(t, bins, func(t *testing.T, b binary) {
		r := invoke(b, "telemetry", "get", "starman")
		assert.Equal(t, 0, r.code, "%s: telemetry get must exit 0\nstderr: %s", b.lang, r.stderr)
		plain := stripANSI(r.combined())
		assert.Contains(t, plain, "Starman", "%s: telemetry get must contain mission name", b.lang)
	})
}

// TestParityTelemetryJSON: telemetry get --format json produces valid JSON
// (object or array — implementations differ in shape).
func TestParityTelemetryJSON(t *testing.T) {
	bins := parityHarness(t)
	forAll(t, bins, func(t *testing.T, b binary) {
		r := invoke(b, "telemetry", "get", "starman", "--format", "json")
		assert.Equal(t, 0, r.code, "%s: telemetry get --format json must exit 0\nstderr: %s", b.lang, r.stderr)
		plain := strings.TrimSpace(stripANSI(r.stdout))
		// Accept either a JSON object or array.
		var asObj map[string]any
		var asArr []any
		objErr := json.Unmarshal([]byte(plain), &asObj)
		arrErr := json.Unmarshal([]byte(plain), &asArr)
		assert.True(t, objErr == nil || arrErr == nil,
			"%s: telemetry JSON must be a valid object or array\ngot: %q", b.lang, plain)
	})
}

// TestParityDeepNestedSubcommand: fleet vehicle inspect <name> exits 0.
func TestParityDeepNestedSubcommand(t *testing.T) {
	bins := parityHarness(t)
	forAll(t, bins, func(t *testing.T, b binary) {
		r := invoke(b, "fleet", "vehicle", "inspect", "Falcon 9")
		assert.Equal(t, 0, r.code, "%s: fleet vehicle inspect must exit 0\nstderr: %s", b.lang, r.stderr)
		plain := stripANSI(r.combined())
		assert.Contains(t, plain, "Falcon", "%s: fleet vehicle inspect must mention vehicle", b.lang)
	})
}

// TestParitySystemsCommaList: fleet vehicle inspect --systems p1,p2 filters output.
func TestParitySystemsCommaList(t *testing.T) {
	bins := parityHarness(t)
	forAll(t, bins, func(t *testing.T, b binary) {
		r := invoke(b, "fleet", "vehicle", "inspect", "Falcon 9", "--systems", "propulsion,landing")
		assert.Equal(t, 0, r.code, "%s: --systems comma-list must exit 0\nstderr: %s", b.lang, r.stderr)
		plain := stripANSI(r.combined())
		// At least one of the requested systems should appear in output.
		hasProp := strings.Contains(strings.ToLower(plain), "propulsion")
		hasLand := strings.Contains(strings.ToLower(plain), "landing")
		assert.True(t, hasProp || hasLand,
			"%s: --systems output must mention at least one requested system\ngot: %s", b.lang, plain)
	})
}

// TestParityDaemonList: daemon list exits 0, shows known daemons.
func TestParityDaemonList(t *testing.T) {
	bins := parityHarness(t)
	forAll(t, bins, func(t *testing.T, b binary) {
		r := invoke(b, "daemon", "list")
		assert.Equal(t, 0, r.code, "%s: daemon list must exit 0\nstderr: %s", b.lang, r.stderr)
		plain := stripANSI(r.combined())
		assert.Contains(t, plain, "funding-secured",
			"%s: daemon list must contain funding-secured", b.lang)
		assert.Contains(t, plain, "RUNNING",
			"%s: daemon list must show RUNNING status", b.lang)
	})
}

// TestParityDaemonStatus: daemon status <id> exits 0, shows media references.
func TestParityDaemonStatus(t *testing.T) {
	bins := parityHarness(t)
	forAll(t, bins, func(t *testing.T, b binary) {
		r := invoke(b, "daemon", "status", "funding-secured")
		assert.Equal(t, 0, r.code, "%s: daemon status must exit 0\nstderr: %s", b.lang, r.stderr)
		plain := stripANSI(r.combined())
		// Must show at least one media reference field.
		hasSEC := strings.Contains(plain, "SEC")
		hasNYT := strings.Contains(plain, "Times") || strings.Contains(plain, "NYT")
		assert.True(t, hasSEC || hasNYT,
			"%s: daemon status must show media references\ngot: %s", b.lang, plain)
	})
}

// TestParityDaemonStopFails: daemon stop <id> explains it cannot be stopped.
// Exit code is intentionally 0 — the daemon is narratively unstoppable, not erroneous.
// The joke: STOP FAILED but the CLI exits fine. This is by design.
// Go/Py need --force to bypass SafetyGuard in non-TTY; TS has no guard.
func TestParityDaemonStopFails(t *testing.T) {
	bins := parityHarness(t)
	forAll(t, bins, func(t *testing.T, b binary) {
		args := []string{"daemon", "stop", "funding-secured"}
		if b.lang != "ts" {
			args = append(args, "--force")
		}
		r := invoke(b, args...)
		plain := stripANSI(r.combined())
		// Must explain why it cannot be stopped.
		assert.Regexp(t, `(?i)(stop|fail|cannot|running|sec)`, plain,
			"%s: daemon stop must explain the failure\ngot: %s", b.lang, plain)
		// Must not be empty.
		assert.NotEmpty(t, strings.TrimSpace(plain),
			"%s: daemon stop must produce output", b.lang)
	})
}

// TestParityDaemonStopAll: daemon stop --all explains all daemons survived
// and spawns a meta-daemon. Exit 0 by design (see TestParityDaemonStopFails).
func TestParityDaemonStopAll(t *testing.T) {
	bins := parityHarness(t)
	forAll(t, bins, func(t *testing.T, b binary) {
		args := []string{"daemon", "stop", "--all"}
		if b.lang != "ts" {
			args = append(args, "--force")
		}
		r := invoke(b, args...)
		plain := stripANSI(r.combined())
		// Must mention the spawned counter-daemon.
		assert.Contains(t, plain, "musk-response-to-this-cli",
			"%s: daemon stop --all must spawn musk-response-to-this-cli\ngot: %s", b.lang, plain)
	})
}

// TestParityElonStatus: elon status exits 0, shows known fields.
func TestParityElonStatus(t *testing.T) {
	bins := parityHarness(t)
	forAll(t, bins, func(t *testing.T, b binary) {
		r := invoke(b, "elon", "status")
		assert.Equal(t, 0, r.code, "%s: elon status must exit 0\nstderr: %s", b.lang, r.stderr)
		plain := stripANSI(r.combined())
		assert.Contains(t, plain, "Elon", "%s: elon status must mention Elon", b.lang)
		assert.Contains(t, plain, "SpaceX", "%s: elon status must mention SpaceX", b.lang)
	})
}

// TestParityIPOStatus: ipo status exits 0.
func TestParityIPOStatus(t *testing.T) {
	bins := parityHarness(t)
	forAll(t, bins, func(t *testing.T, b binary) {
		r := invoke(b, "ipo", "status")
		assert.Equal(t, 0, r.code, "%s: ipo status must exit 0\nstderr: %s", b.lang, r.stderr)
		plain := stripANSI(r.combined())
		assert.Regexp(t, `(?i)(ipo|public|valuation)`, plain,
			"%s: ipo status must mention IPO/valuation\ngot: %s", b.lang, plain)
	})
}

// TestParityCompetitorCompare: competitor compare <name> exits 0.
func TestParityCompetitorCompare(t *testing.T) {
	bins := parityHarness(t)
	forAll(t, bins, func(t *testing.T, b binary) {
		r := invoke(b, "competitor", "compare", "boeing")
		assert.Equal(t, 0, r.code, "%s: competitor compare must exit 0\nstderr: %s", b.lang, r.stderr)
		plain := stripANSI(r.combined())
		assert.Contains(t, plain, "Boeing", "%s: competitor compare must mention Boeing", b.lang)
	})
}

// TestParityStarshipStatus: starship status exits 0, mentions IFT flights.
func TestParityStarshipStatus(t *testing.T) {
	bins := parityHarness(t)
	forAll(t, bins, func(t *testing.T, b binary) {
		r := invoke(b, "starship", "status")
		assert.Equal(t, 0, r.code, "%s: starship status must exit 0\nstderr: %s", b.lang, r.stderr)
		plain := stripANSI(r.combined())
		assert.Contains(t, plain, "Starship", "%s: starship status must mention Starship", b.lang)
	})
}

// TestParityQuiet: launch --quiet suppresses non-essential output.
// We verify the command still exits 0 — quiet mode must not break execution.
func TestParityQuiet(t *testing.T) {
	bins := parityHarness(t)
	forAll(t, bins, func(t *testing.T, b binary) {
		r := invoke(b, "--quiet", "mission", "list")
		assert.Equal(t, 0, r.code,
			"%s: --quiet must not cause non-zero exit\nstderr: %s", b.lang, r.stderr)
	})
}

// TestParityHelpCommandOrder: all 11 commands appear in --help output in
// alphabetical order.
func TestParityHelpCommandOrder(t *testing.T) {
	bins := parityHarness(t)
	commands := []string{
		"abort", "competitor", "countdown", "daemon", "elon",
		"fleet", "ipo", "launch", "mission", "starship", "telemetry",
	}
	forAll(t, bins, func(t *testing.T, b binary) {
		r := invoke(b, "--help")
		assert.Equal(t, 0, r.code, "%s: --help must exit 0", b.lang)
		plain := stripANSI(r.combined())

		// Search within COMMANDS section only to avoid false matches
		// in description text (e.g. "daemon" in "every daemon").
		cmdsIdx := strings.Index(plain, "COMMANDS")
		require.NotEqual(t, -1, cmdsIdx, "%s: COMMANDS section not found", b.lang)
		cmdsSection := plain[cmdsIdx:]

		prevIdx := -1
		for _, cmd := range commands {
			idx := strings.Index(cmdsSection, "\n")
			// Find cmd as a line-leading token (indented).
			lineIdx := -1
			for i, line := range strings.Split(cmdsSection, "\n") {
				trimmed := strings.TrimSpace(line)
				if strings.HasPrefix(trimmed, cmd+" ") || trimmed == cmd {
					lineIdx = i
					break
				}
			}
			_ = idx
			assert.NotEqual(t, -1, lineIdx,
				"%s: command %q must appear in COMMANDS section", b.lang, cmd)
			if lineIdx != -1 {
				assert.Greater(t, lineIdx, prevIdx,
					"%s: command %q must appear after previous command (alphabetical order)",
					b.lang, cmd)
				prevIdx = lineIdx
			}
		}
	})
}

// TestParityHelpCommandLines: each command entry in COMMANDS must show the same
// name, arg pattern, and description across all three languages.
//
// Go (fang) is the source of truth. Format per command:
//
//	name [args...]  Description text
//
// The test normalizes whitespace and extracts (name, args, description) tuples,
// then asserts all three languages produce the same set.
func TestParityHelpCommandLines(t *testing.T) {
	bins := parityHarness(t)

	// Expected command entries — Go is source of truth.
	// Each entry: name, description (args vary by framework — tested separately).
	wantCmds := []struct {
		name string
		desc string
	}{
		{"abort", "Abort a mission"},
		{"competitor", "Compare SpaceX against its competitors"},
		{"countdown", "Show countdown status for a mission"},
		{"daemon", "Manage background controversy processes"},
		{"elon", "Elon Musk current status"},
		{"fleet", "Inspect the SpaceX vehicle fleet"},
		{"ipo", "Spacex IPO status tracker"},
		{"launch", "Launch a mission"},
		{"mission", "Query mission history"},
		{"starship", "Starship program status and history"},
		{"telemetry", "Mission telemetry streams"},
	}

	forAll(t, bins, func(t *testing.T, b binary) {
		r := invoke(b, "--help")
		assert.Equal(t, 0, r.code)
		plain := stripANSI(r.combined())

		for _, want := range wantCmds {
			// Command name must appear in help.
			assert.Contains(t, plain, want.name,
				"%s: command %q must appear in help", b.lang, want.name)
			// Description must appear on the same conceptual line.
			assert.Contains(t, plain, want.desc,
				"%s: command %q must have description %q", b.lang, want.name, want.desc)
		}
	})
}

// TestParityHelpCommandTerms: each command entry must show the correct arg
// pattern matching Go/fang convention:
//   - commands with args + flags: "name <arg> [--flags]"
//   - commands with subcommands:  "name [command]"
//   - commands with just args:    "name <arg>"
func TestParityHelpCommandTerms(t *testing.T) {
	bins := parityHarness(t)

	// Expected term patterns — Go/fang is source of truth.
	// launch mission arg is optional (--interactive mode). Go/fang renders
	// <mission>, TS/Py render [mission]. Checked via regex below.
	wantTerms := map[string]string{
		"abort":      "abort <mission>",
		"competitor": "competitor [command]",
		"countdown":  "countdown <mission>",
		"daemon":     "daemon [command]",
		"elon":       "elon [command]",
		"fleet":      "fleet [command]",
		"ipo":        "ipo [command]",
		"mission":    "mission [command]",
		"starship":   "starship [command]",
		"telemetry":  "telemetry [command]",
	}
	launchRE := regexp.MustCompile(`launch\s+[<\[]mission[>\]]`)

	forAll(t, bins, func(t *testing.T, b binary) {
		r := invoke(b, "--help")
		assert.Equal(t, 0, r.code)
		plain := stripANSI(r.combined())

		// Extract COMMANDS section.
		cmdsIdx := strings.Index(plain, "COMMANDS")
		require.NotEqual(t, -1, cmdsIdx, "%s: COMMANDS not found", b.lang)
		flagsIdx := strings.Index(plain[cmdsIdx:], "FLAGS")
		var cmdsSection string
		if flagsIdx != -1 {
			cmdsSection = plain[cmdsIdx : cmdsIdx+flagsIdx]
		} else {
			cmdsSection = plain[cmdsIdx:]
		}

		// Check launch separately with regex (arg bracket style varies).
		assert.Regexp(t, launchRE, cmdsSection,
			"%s: launch must show mission arg", b.lang)

		for name, wantPrefix := range wantTerms {
			// Find line starting with command name.
			found := false
			for _, line := range strings.Split(cmdsSection, "\n") {
				trimmed := strings.TrimSpace(line)
				if strings.HasPrefix(trimmed, name+" ") || trimmed == name {
					// Line must contain the expected term prefix.
					assert.Contains(t, trimmed, wantPrefix,
						"%s: command %q term must contain %q\n  got: %q",
						b.lang, name, wantPrefix, trimmed)
					found = true
					break
				}
			}
			assert.True(t, found, "%s: command %q not found in COMMANDS section", b.lang, name)
		}
	})
}

// TestParityHelpSectionHeaders: section headers must use parity titles.
// Go renders "COMMANDS" and "FLAGS" (no colon); TS/Python must match with
// colon allowed (Commander/Click convention) but title must be uppercase.
func TestParityHelpSectionHeaders(t *testing.T) {
	bins := parityHarness(t)
	forAll(t, bins, func(t *testing.T, b binary) {
		r := invoke(b, "--help")
		assert.Equal(t, 0, r.code)
		plain := stripANSI(r.combined())

		// Must have COMMANDS (with or without colon).
		assert.Regexp(t, `(?m)^\s*COMMANDS:?\s*$`, plain,
			"%s: must have COMMANDS section header (uppercase)", b.lang)
		// Must have FLAGS (with or without colon).
		assert.Regexp(t, `(?m)^\s*FLAGS:?\s*$`, plain,
			"%s: must have FLAGS section header (uppercase)", b.lang)
		// Must NOT have lowercase variants.
		assert.NotRegexp(t, `(?m)^\s*Commands:\s*$`, plain,
			"%s: must not have 'Commands:' (use COMMANDS)", b.lang)
		assert.NotRegexp(t, `(?m)^\s*Options:\s*$`, plain,
			"%s: must not have 'Options:' (use FLAGS)", b.lang)
	})
}

// TestParityHelpFlagDescriptions: flag descriptions must match across languages.
func TestParityHelpFlagDescriptions(t *testing.T) {
	bins := parityHarness(t)

	wantFlags := []struct {
		flag string
		desc string
	}{
		{"--format", "Output format"},
		{"--quiet", "Suppress non-essential output"},
		{"--no-color", "Disable ANSI colo"}, // "colour" vs "color" — prefix match
		{"--no-hints", "Suppress next-step hints"},
	}

	forAll(t, bins, func(t *testing.T, b binary) {
		r := invoke(b, "--help")
		assert.Equal(t, 0, r.code)
		plain := stripANSI(r.combined())

		for _, want := range wantFlags {
			assert.Contains(t, plain, want.flag,
				"%s: flag %s must appear in help", b.lang, want.flag)
			assert.Contains(t, plain, want.desc,
				"%s: flag %s must have description containing %q", b.lang, want.flag, want.desc)
		}
	})
}

// TestParityHelpSectionHeadersNoColor: section headers must be uppercase even
// with --no-color or NO_COLOR=1.
func TestParityHelpSectionHeadersNoColor(t *testing.T) {
	bins := parityHarness(t)
	forAll(t, bins, func(t *testing.T, b binary) {
		r := invoke(b, "--no-color", "--help")
		assert.Equal(t, 0, r.code)
		plain := r.combined()
		assert.Regexp(t, `(?m)^\s*COMMANDS:?\s*$`, plain,
			"%s: COMMANDS header must be uppercase even with --no-color", b.lang)
		assert.Regexp(t, `(?m)^\s*FLAGS:?\s*$`, plain,
			"%s: FLAGS header must be uppercase even with --no-color", b.lang)
		assert.NotRegexp(t, `(?m)^\s*Commands:\s*$`, plain,
			"%s: must not show 'Commands:' with --no-color", b.lang)
		assert.NotRegexp(t, `(?m)^\s*Options:\s*$`, plain,
			"%s: must not show 'Options:' with --no-color", b.lang)
	})
}

// TestParityHelpDescription: --help output contains the exact one-liner description.
func TestParityHelpDescription(t *testing.T) {
	const oneLiner = "satirical SpaceX CLI historian"
	bins := parityHarness(t)
	forAll(t, bins, func(t *testing.T, b binary) {
		r := invoke(b, "--help")
		assert.Equal(t, 0, r.code, "%s: --help must exit 0", b.lang)
		plain := stripANSI(r.combined())
		assert.Contains(t, plain, oneLiner,
			"%s: --help must contain the exact description one-liner", b.lang)
	})
}

// TestParityWizardInteractive: launch --interactive runs headless wizard with
// defaults, prints results. Exit 0, output contains "WIZARD RESULTS" and
// default values (Starlink-42, leo, 60x Starlink v2 Mini).
func TestParityWizardInteractive(t *testing.T) {
	bins := parityHarness(t)
	forAll(t, bins, func(t *testing.T, b binary) {
		r := invoke(b, "launch", "--interactive")
		assert.Equal(t, 0, r.code,
			"%s: launch --interactive must exit 0\nstderr: %s", b.lang, r.stderr)
		plain := stripANSI(r.combined())
		assert.Contains(t, plain, "WIZARD RESULTS",
			"%s: wizard output must contain 'WIZARD RESULTS'", b.lang)
		assert.Contains(t, plain, "Starlink-42",
			"%s: wizard output must contain default mission 'Starlink-42'", b.lang)
		assert.Contains(t, plain, "leo",
			"%s: wizard output must contain default orbit 'leo'", b.lang)
		assert.Contains(t, plain, "60x Starlink v2 Mini",
			"%s: wizard output must contain default payload '60x Starlink v2 Mini'",
			b.lang)
	})
}

// TestParityErrorToStderr: errors must appear in stderr (not only stdout).
func TestParityErrorToStderr(t *testing.T) {
	bins := parityHarness(t)
	forAll(t, bins, func(t *testing.T, b binary) {
		r := invoke(b, "mission", "inspect", "totally-bogus-xyz-mission")
		assert.Equal(t, 1, r.code, "%s: bad mission must exit 1", b.lang)
		// stderr must be non-empty OR stdout must contain an error indicator.
		// (Commander puts errors to stderr; some frameworks mix them.)
		hasErr := len(strings.TrimSpace(r.stderr)) > 0 ||
			strings.Contains(strings.ToLower(stripANSI(r.stdout)), "error") ||
			strings.Contains(strings.ToLower(stripANSI(r.stdout)), "not found")
		assert.True(t, hasErr,
			"%s: error output must be non-empty or contain error keyword\nstdout: %q\nstderr: %q",
			b.lang, r.stdout, r.stderr)
	})
}

// TestParityBusLaunchEvent: launch --dry-run must emit a bus event line for
// kit.spaced.launch.initiated. Bus output may go to stdout or stderr depending on lang.
func TestParityBusLaunchEvent(t *testing.T) {
	bins := parityHarness(t)
	forAll(t, bins, func(t *testing.T, b binary) {
		r := invoke(b, "launch", "starman", "--dry-run")
		assert.Equal(t, 0, r.code,
			"%s: launch --dry-run must exit 0\nstderr: %s", b.lang, r.stderr)
		combined := stripANSI(r.combined())
		assert.Contains(t, combined, "[bus]",
			"%s: launch --dry-run must emit bus event marker [bus]", b.lang)
		assert.Contains(t, combined, "kit.spaced.launch.initiated",
			"%s: launch --dry-run must emit kit.spaced.launch.initiated event", b.lang)
	})
}

// TestParityBusDaemonEvent: daemon stop must emit a bus event line for
// kit.spaced.daemon.stopped. Bus output may go to stdout or stderr depending on lang.
// Go/Py need --force to bypass SafetyGuard in non-TTY; TS has no guard.
func TestParityBusDaemonEvent(t *testing.T) {
	bins := parityHarness(t)
	forAll(t, bins, func(t *testing.T, b binary) {
		args := []string{"daemon", "stop", "funding-secured"}
		if b.lang != "ts" {
			args = append(args, "--force")
		}
		r := invoke(b, args...)
		combined := stripANSI(r.combined())
		assert.Contains(t, combined, "[bus]",
			"%s: daemon stop must emit bus event marker [bus]", b.lang)
		assert.Contains(t, combined, "kit.spaced.daemon.stopped",
			"%s: daemon stop must emit kit.spaced.daemon.stopped event", b.lang)
	})
}

// ── Log parity tests ─────────────────────────────────────────────────────────

// TestParityLogOutput: launch starman --dry-run emits structured log lines to
// stderr containing INFO-level entries about resolving and launch parameters.
func TestParityLogOutput(t *testing.T) {
	bins := parityHarness(t)
	forAll(t, bins, func(t *testing.T, b binary) {
		r := invoke(b, "launch", "starman", "--dry-run")
		assert.Equal(t, 0, r.code,
			"%s: launch --dry-run must exit 0\nstderr: %s", b.lang, r.stderr)
		stderr := stripANSI(r.stderr)
		assert.Contains(t, stderr, "INFO",
			"%s: stderr must contain INFO log level", b.lang)
		assert.Regexp(t, `(?i)resolving mission`, stderr,
			"%s: stderr must log 'resolving mission'", b.lang)
		assert.Regexp(t, `(?i)launch parameters`, stderr,
			"%s: stderr must log 'launch parameters'", b.lang)
	})
}

// TestParityLogQuiet: --quiet suppresses INFO-level log output on stderr.
func TestParityLogQuiet(t *testing.T) {
	bins := parityHarness(t)
	forAll(t, bins, func(t *testing.T, b binary) {
		r := invoke(b, "--quiet", "launch", "starman", "--dry-run")
		assert.Equal(t, 0, r.code,
			"%s: --quiet launch --dry-run must exit 0\nstderr: %s",
			b.lang, r.stderr)
		stderr := stripANSI(r.stderr)
		assert.NotContains(t, stderr, "INFO",
			"%s: --quiet must suppress INFO log lines on stderr", b.lang)
	})
}

// TestParityLogWarnOnDryRun: launch --dry-run emits a WARN about dry run mode.
func TestParityLogWarnOnDryRun(t *testing.T) {
	bins := parityHarness(t)
	forAll(t, bins, func(t *testing.T, b binary) {
		r := invoke(b, "launch", "starman", "--dry-run")
		assert.Equal(t, 0, r.code,
			"%s: launch --dry-run must exit 0\nstderr: %s", b.lang, r.stderr)
		stderr := stripANSI(r.stderr)
		assert.Contains(t, stderr, "WARN",
			"%s: stderr must contain WARN log level for dry run", b.lang)
		assert.Regexp(t, `(?i)dry.?run`, stderr,
			"%s: stderr WARN must mention dry run", b.lang)
	})
}

// ── SetFlag parity ───────────────────────────────────────────────────────────

// parseTags extracts the Tags value from dry-run output.
// Looks for a line like "Tags: crew, cargo" and returns the part after ":".
func parseTags(output string) string {
	for _, line := range strings.Split(output, "\n") {
		trimmed := strings.TrimSpace(line)
		lower := strings.ToLower(trimmed)
		if strings.HasPrefix(lower, "tags") {
			idx := strings.Index(trimmed, ":")
			if idx != -1 {
				return strings.TrimSpace(trimmed[idx+1:])
			}
		}
	}
	return ""
}

// TestParitySetFlagAppend: --tag crew --tag cargo → Tags contains both.
func TestParitySetFlagAppend(t *testing.T) {
	bins := parityHarness(t)
	forAll(t, bins, func(t *testing.T, b binary) {
		r := invoke(b, "launch", "starman",
			"--tag", "crew", "--tag", "cargo", "--dry-run")
		assert.Equal(t, 0, r.code,
			"%s: --tag append must exit 0\nstderr: %s", b.lang, r.stderr)
		plain := stripANSI(r.combined())
		tags := parseTags(plain)
		assert.Contains(t, tags, "crew",
			"%s: Tags must contain 'crew'\ngot: %q", b.lang, tags)
		assert.Contains(t, tags, "cargo",
			"%s: Tags must contain 'cargo'\ngot: %q", b.lang, tags)
	})
}

// TestParitySetFlagRemove: --tag crew --tag cargo --tag -crew → cargo only.
func TestParitySetFlagRemove(t *testing.T) {
	bins := parityHarness(t)
	forAll(t, bins, func(t *testing.T, b binary) {
		r := invoke(b, "launch", "starman",
			"--tag", "crew", "--tag", "cargo", "--tag", "-crew", "--dry-run")
		assert.Equal(t, 0, r.code,
			"%s: --tag remove must exit 0\nstderr: %s", b.lang, r.stderr)
		plain := stripANSI(r.combined())
		tags := parseTags(plain)
		assert.Contains(t, tags, "cargo",
			"%s: Tags must contain 'cargo'\ngot: %q", b.lang, tags)
		assert.NotContains(t, tags, "crew",
			"%s: Tags must NOT contain 'crew' after removal\ngot: %q",
			b.lang, tags)
	})
}

// TestParitySetFlagReplace: --tag old --tag =new1,new2 → new1+new2, not old.
func TestParitySetFlagReplace(t *testing.T) {
	bins := parityHarness(t)
	forAll(t, bins, func(t *testing.T, b binary) {
		r := invoke(b, "launch", "starman",
			"--tag", "old", "--tag", "=new1,new2", "--dry-run")
		assert.Equal(t, 0, r.code,
			"%s: --tag replace must exit 0\nstderr: %s", b.lang, r.stderr)
		plain := stripANSI(r.combined())
		tags := parseTags(plain)
		assert.Contains(t, tags, "new1",
			"%s: Tags must contain 'new1'\ngot: %q", b.lang, tags)
		assert.Contains(t, tags, "new2",
			"%s: Tags must contain 'new2'\ngot: %q", b.lang, tags)
		assert.NotContains(t, tags, "old",
			"%s: Tags must NOT contain 'old' after replace\ngot: %q",
			b.lang, tags)
	})
}

// TestParitySetFlagClear: --tag something --tag = → Tags shows "none" (empty).
func TestParitySetFlagClear(t *testing.T) {
	bins := parityHarness(t)
	forAll(t, bins, func(t *testing.T, b binary) {
		r := invoke(b, "launch", "starman",
			"--tag", "something", "--tag", "=", "--dry-run")
		assert.Equal(t, 0, r.code,
			"%s: --tag clear must exit 0\nstderr: %s", b.lang, r.stderr)
		plain := stripANSI(r.combined())
		tags := strings.ToLower(parseTags(plain))
		assert.Contains(t, tags, "none",
			"%s: Tags must show 'none' after clear\ngot: %q", b.lang, tags)
	})
}

// ── Toolspec parity ─────────────────────────────────────────────────────────

// TestParityToolspec: toolspec → exit 0, output contains "spaced" (the tool
// name from spaced.toolspec.yaml) and shows no validation errors.
func TestParityToolspec(t *testing.T) {
	bins := parityHarness(t)

	// Set env so the Go binary can locate the YAML from any cwd.
	root := moduleRoot(t)
	specPath := filepath.Join(root, "examples", "spaced", "spaced.toolspec.yaml")
	t.Setenv("SPACED_TOOLSPEC_PATH", specPath)

	forAll(t, bins, func(t *testing.T, b binary) {
		r := invoke(b, "toolspec")
		assert.Equal(t, 0, r.code,
			"%s: toolspec must exit 0\nstderr: %s", b.lang, r.stderr)
		plain := stripANSI(r.combined())
		assert.Contains(t, plain, "spaced",
			"%s: toolspec output must contain tool name 'spaced'", b.lang)
		assert.Contains(t, plain, "valid",
			"%s: toolspec output must show 'valid' status", b.lang)
		assert.NotContains(t, plain, "Errors",
			"%s: toolspec must not report validation errors", b.lang)
	})
}

// TestParityHintSuppression: --no-hints flag is accepted and does not break
// command execution. Verifies:
//   - --no-hints --help exits 0 (flag wiring)
//   - --no-hints with a data command (mission list) exits 0, still produces output
//   - without --no-hints, same command exits 0 (baseline sanity)
func TestParityHintSuppression(t *testing.T) {
	bins := parityHarness(t)

	t.Run("help_accepts_flag", func(t *testing.T) {
		forAll(t, bins, func(t *testing.T, b binary) {
			r := invoke(b, "--no-hints", "--help")
			assert.Equal(t, 0, r.code,
				"%s: --no-hints --help must exit 0\nstderr: %s", b.lang, r.stderr)
			plain := stripANSI(r.combined())
			assert.Contains(t, plain, "COMMANDS",
				"%s: --no-hints --help must still show COMMANDS section", b.lang)
		})
	})

	t.Run("data_command_without_flag", func(t *testing.T) {
		forAll(t, bins, func(t *testing.T, b binary) {
			r := invoke(b, "mission", "list")
			assert.Equal(t, 0, r.code,
				"%s: mission list (no --no-hints) must exit 0\nstderr: %s",
				b.lang, r.stderr)
			plain := stripANSI(r.combined())
			assert.Contains(t, plain, "Starman",
				"%s: mission list must contain Starman", b.lang)
		})
	})

	t.Run("data_command_with_flag", func(t *testing.T) {
		forAll(t, bins, func(t *testing.T, b binary) {
			r := invoke(b, "--no-hints", "mission", "list")
			assert.Equal(t, 0, r.code,
				"%s: --no-hints mission list must exit 0\nstderr: %s",
				b.lang, r.stderr)
			plain := stripANSI(r.combined())
			assert.Contains(t, plain, "Starman",
				"%s: --no-hints must not suppress primary output (Starman)",
				b.lang)
		})
	})
}

// ── Config / XDG parity tests ───────────────────────────────────────────────

// TestParityConfigShow: config command exits 0, shows config keys and path info.
// Python uses "config show"; Go/TS use bare "config".
func TestParityConfigShow(t *testing.T) {
	bins := parityHarness(t)
	forAll(t, bins, func(t *testing.T, b binary) {
		var r result
		if b.lang == "py" {
			r = invoke(b, "config", "show")
		} else {
			r = invoke(b, "config")
		}
		assert.Equal(t, 0, r.code,
			"%s: config must exit 0\nstderr: %s", b.lang, r.stderr)
		plain := stripANSI(r.combined())
		assert.Contains(t, plain, "SPACED CONFIG",
			"%s: config output must contain 'SPACED CONFIG' header", b.lang)
		assert.Contains(t, plain, "PATHS",
			"%s: config output must contain 'PATHS' header", b.lang)
		assert.Regexp(t, `Config\s*:`, plain,
			"%s: config output must show Config path", b.lang)
		assert.Regexp(t, `Data\s*:`, plain,
			"%s: config output must show Data path", b.lang)
		assert.Contains(t, plain, "Default Pad",
			"%s: config output must show Default Pad key", b.lang)
	})
}

// TestParityXDGPaths: setting XDG_CONFIG_HOME and XDG_DATA_HOME overrides the
// paths shown in config output.
func TestParityXDGPaths(t *testing.T) {
	bins := parityHarness(t)
	forAll(t, bins, func(t *testing.T, b binary) {
		tmp := t.TempDir()
		customConfig := filepath.Join(tmp, "xdg-config")
		customData := filepath.Join(tmp, "xdg-data")

		var cmd *exec.Cmd
		if b.lang == "py" {
			cmd = b.run("config", "show")
		} else {
			cmd = b.run("config")
		}
		cmd.Env = append(os.Environ(),
			"XDG_CONFIG_HOME="+customConfig,
			"XDG_DATA_HOME="+customData,
		)
		var stdout, stderr bytes.Buffer
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr

		err := cmd.Run()
		code := 0
		if err != nil {
			if exitErr, ok := err.(*exec.ExitError); ok {
				code = exitErr.ExitCode()
			} else {
				code = 1
			}
		}

		assert.Equal(t, 0, code,
			"%s: config with XDG overrides must exit 0\nstderr: %s",
			b.lang, stderr.String())
		plain := stripANSI(stdout.String() + stderr.String())
		assert.Contains(t, plain, filepath.Join(customConfig, "spaced"),
			"%s: config path must respect XDG_CONFIG_HOME\ngot: %s",
			b.lang, plain)
		assert.Contains(t, plain, filepath.Join(customData, "spaced"),
			"%s: data path must respect XDG_DATA_HOME\ngot: %s",
			b.lang, plain)
	})
}

// TestParityHelpFlagsAlwaysLast: FLAGS must be the last section in help output,
// after all command groups (COMMANDS, MANAGEMENT, etc.).
func TestParityHelpFlagsAlwaysLast(t *testing.T) {
	bins := parityHarness(t)
	forAll(t, bins, func(t *testing.T, b binary) {
		r := invoke(b, "--help-all")
		assert.Equal(t, 0, r.code)
		plain := stripANSI(r.combined())

		// Find last occurrence of FLAGS header.
		flagsIdx := strings.LastIndex(plain, "FLAGS")
		require.NotEqual(t, -1, flagsIdx, "%s: FLAGS not found", b.lang)

		// No section headers after FLAGS (flag descriptions may mention
		// group names like "MANAGEMENT" — only line-leading headers count).
		afterFlags := plain[flagsIdx:]
		for _, line := range strings.Split(afterFlags[5:], "\n") {
			trimmed := strings.TrimSpace(line)
			if trimmed == "" {
				continue
			}
			// Skip flag lines (start with -).
			if strings.HasPrefix(trimmed, "-") || strings.HasPrefix(trimmed, "--") {
				continue
			}
			// Skip non-flag content lines (descriptions wrapping, default values, etc.).
			if matched, _ := regexp.MatchString(`^[A-Z][A-Z ]+:?$`, trimmed); matched {
				t.Errorf("%s: found section header %q after FLAGS", b.lang, trimmed)
			}
		}
	})
}
