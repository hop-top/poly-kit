// Command skillsync derives agent-skill content and channel metadata from
// the capability parity page, the publish workflow's ecosystems map, the SDK
// tiers page, each skill's SKILL.md frontmatter and ports.yaml, and fails
// when the committed output has drifted from them.
//
// Usage:
//
//	go run ./internal/tools/skillsync sync [--check] [--require-all]
//	go run ./internal/tools/skillsync expected [--component <name>]
//	go run ./internal/tools/skillsync lint [--standalone] [dir...]
//
// Input locations default to the repository's and are overridable with
// --root, --parity, --publish, --tiers, --manifest and --ports.
//
// sync writes generated files and managed blocks into skill directories
// whose SKILL.md exists, plus the repository-root skills.json and
// marketplace entries file. sync --check regenerates in memory, prints each
// path that differs and exits 1 without writing. expected prints, per skill
// of a component, the mirror, the skill directory here and the skill
// directory in the mirror, tab separated; nothing for a component without
// a skill.
//
// lint checks skill directories against the authoring contract (rules S1 to
// S10, listed in README.md). With no dir it checks every skill directory
// ports.yaml names that exists; --standalone runs only the rules that need
// nothing outside the skill directory, on any directory. A finding prints as
// path:line: rule: message, one per line, sorted.
//
// Exit status: 0 success, 1 drift, findings or inconsistent inputs, 2 usage
// or I/O failure.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
)

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: skillsync sync [--check] [--require-all] | expected --component <name> | lint [--standalone] [dir...]")
		return 2
	}
	cmd, rest := args[0], args[1:]
	if cmd != "sync" && cmd != "expected" && cmd != "lint" {
		fmt.Fprintf(stderr, "skillsync: unknown command %q (want sync, expected or lint)\n", cmd)
		return 2
	}
	fs := flag.NewFlagSet(cmd, flag.ContinueOnError)
	fs.SetOutput(stderr)
	d := DefaultOptions(".")
	o := Options{}
	fs.StringVar(&o.Root, "root", ".", "repository root")
	fs.StringVar(&o.Parity, "parity", d.Parity, "capability parity page, relative to the root")
	fs.StringVar(&o.Publish, "publish", d.Publish, "publish workflow, relative to the root")
	fs.StringVar(&o.Tiers, "tiers", d.Tiers, "SDK tiers page, relative to the root")
	fs.StringVar(&o.Manifest, "manifest", d.Manifest, "release manifest, relative to the root")
	fs.StringVar(&o.Ports, "ports", d.Ports, "ports.yaml, relative to the root")
	check := fs.Bool("check", false, "sync: report drift, write nothing")
	fs.BoolVar(&o.RequireAll, "require-all", false, "sync: a skill ports.yaml names without a SKILL.md is an error")
	component := fs.String("component", "", "expected: publish matrix component")
	standalone := fs.Bool("standalone", false, "lint: run only the rules that need nothing outside the skill directory")
	var dirs []string
	for {
		if err := fs.Parse(rest); err != nil {
			return 2
		}
		if fs.NArg() == 0 {
			break
		}
		if cmd != "lint" {
			fmt.Fprintf(stderr, "skillsync: unexpected argument %q\n", fs.Arg(0))
			return 2
		}
		dirs = append(dirs, fs.Arg(0)) // lint takes dirs among its flags
		rest = fs.Args()[1:]
	}
	if cmd == "lint" {
		return runLint(o, LintOptions{Standalone: *standalone, Dirs: dirs}, stdout, stderr)
	}
	if cmd == "expected" {
		return runExpected(o, *component, stdout, stderr)
	}
	return runSync(o, *check, stdout, stderr)
}

func runExpected(o Options, component string, stdout, stderr io.Writer) int {
	if component == "" {
		fmt.Fprintln(stderr, "skillsync expected: --component is required")
		return 2
	}
	list, err := Expected(o, component)
	if err != nil {
		fmt.Fprintf(stderr, "skillsync: %v\n", err)
		return 2
	}
	for _, e := range list {
		fmt.Fprintf(stdout, "%s\t%s\t%s\n", e.Mirror, e.Dir, e.MirrorDir)
	}
	return 0
}

func runSync(o Options, check bool, stdout, stderr io.Writer) int {
	pl, err := BuildPlan(o)
	if err != nil {
		fmt.Fprintf(stderr, "skillsync: %v\n", err)
		return 1
	}
	present := fmt.Sprintf("%d of %d skills present", len(pl.Present), pl.Expected)
	if check {
		stale, err := pl.Drift(o.Root)
		if err != nil {
			fmt.Fprintf(stderr, "skillsync: %v\n", err)
			return 2
		}
		if len(stale) > 0 {
			fmt.Fprintf(stderr, "skillsync: generated files out of date:\n  %s\nrun: make skills-sync\n", strings.Join(stale, "\n  "))
			return 1
		}
		fmt.Fprintf(stdout, "skillsync: in sync (%s)\n", present)
		return 0
	}
	wrote, err := pl.Write(o.Root)
	if err != nil {
		fmt.Fprintf(stderr, "skillsync: %v\n", err)
		return 2
	}
	for _, p := range wrote {
		fmt.Fprintf(stdout, "wrote %s\n", p)
	}
	fmt.Fprintf(stdout, "skillsync: wrote %d files (%s)\n", len(wrote), present)
	return 0
}

func runLint(o Options, lo LintOptions, stdout, stderr io.Writer) int {
	found, err := Lint(o, lo)
	if err != nil {
		fmt.Fprintf(stderr, "skillsync: %v\n", err)
		var in *inputError
		if errors.As(err, &in) {
			return 1
		}
		return 2
	}
	for _, f := range found {
		fmt.Fprintln(stdout, f)
	}
	if len(found) > 0 {
		fmt.Fprintf(stderr, "skillsync: %d lint findings\n", len(found))
		return 1
	}
	return 0
}
