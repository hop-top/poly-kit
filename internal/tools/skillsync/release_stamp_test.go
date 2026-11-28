package main

import (
	"encoding/json"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// The release PR of a component stamps its version into the files its
// release-please package lists as extra-files. A skill file missing from that
// list keeps the old version after the release, and S6 then fails on main.
// This test holds every SKILL.md and skill plugin manifest to a registration.

type rpExtraFile struct {
	Type     string `json:"type"`
	Path     string `json:"path"`
	JSONPath string `json:"jsonpath"`
}

type rpConfig struct {
	Packages map[string]struct {
		Component  string        `json:"component"`
		ExtraFiles []rpExtraFile `json:"extra-files"`
	} `json:"packages"`
}

// pruneDirs are never walked: vendored trees, fixtures and sibling worktrees.
var pruneDirs = map[string]bool{
	".git": true, "node_modules": true, "testdata": true, "hops": true,
	"vendor": true, "target": true, ".venv": true,
}

// skillStampFiles lists the repository-relative SKILL.md and skill
// .claude-plugin/plugin.json files under root: those inside skills/<name>/.
func skillStampFiles(t *testing.T, root string) (skillMD, plugin []string) {
	t.Helper()
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if pruneDirs[d.Name()] && p != root {
				return filepath.SkipDir
			}
			return nil
		}
		rel, _ := filepath.Rel(root, p)
		rel = filepath.ToSlash(rel)
		parts := strings.Split(rel, "/")
		n := len(parts)
		switch {
		case n >= 3 && parts[n-1] == "SKILL.md" && parts[n-3] == "skills":
			skillMD = append(skillMD, rel)
		case n >= 4 && parts[n-1] == "plugin.json" && parts[n-2] == ".claude-plugin" && parts[n-4] == "skills":
			plugin = append(plugin, rel)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(skillMD)
	sort.Strings(plugin)
	return skillMD, plugin
}

// unregisteredStampFiles returns, one message per file, every skill file the
// config does not stamp: SKILL.md needs a generic extra-file, plugin.json a
// json extra-file at $.version, both on the package that owns the file.
func unregisteredStampFiles(cfg rpConfig, skillMD, plugin []string) []string {
	owner := func(rel string) string {
		best := "."
		for pkg := range cfg.Packages {
			if pkg != "." && (rel == pkg || strings.HasPrefix(rel, pkg+"/")) && len(pkg) > len(best) {
				best = pkg
			}
		}
		if best == "." {
			// the root package owns what no other package contains
			if _, ok := cfg.Packages["."]; !ok {
				return ""
			}
		}
		return best
	}
	registered := func(pkg, rel, typ string) bool {
		for _, ef := range cfg.Packages[pkg].ExtraFiles {
			p := ef.Path
			if strings.HasPrefix(p, "/") {
				p = strings.TrimPrefix(p, "/")
			} else if pkg != "." {
				p = path.Join(pkg, p)
			}
			if path.Clean(p) != rel || ef.Type != typ {
				continue
			}
			if typ == "json" && ef.JSONPath != "$.version" {
				continue
			}
			return true
		}
		return false
	}
	var out []string
	check := func(rels []string, typ, what string) {
		for _, rel := range rels {
			pkg := owner(rel)
			switch {
			case pkg == "":
				out = append(out, rel+": no release-please package contains it")
			case !registered(pkg, rel, typ):
				out = append(out, rel+": not a "+what+" extra-file of package "+pkg)
			}
		}
	}
	check(skillMD, "generic", "generic")
	check(plugin, "json", "json $.version")
	return out
}

func loadReleaseConfig(t *testing.T, root string) rpConfig {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(root, ".github", "release-please-config.json"))
	if err != nil {
		t.Fatal(err)
	}
	var cfg rpConfig
	if err := json.Unmarshal(b, &cfg); err != nil {
		t.Fatal(err)
	}
	return cfg
}

// TestEverySkillFileIsStampedByItsComponent is the guard: the repository's
// own skills against the repository's own release-please config.
func TestEverySkillFileIsStampedByItsComponent(t *testing.T) {
	root := filepath.Join("..", "..", "..")
	skillMD, plugin := skillStampFiles(t, root)
	if len(skillMD) == 0 || len(plugin) == 0 {
		t.Fatalf("found %d SKILL.md and %d plugin.json files; the walk is broken", len(skillMD), len(plugin))
	}
	if got := unregisteredStampFiles(loadReleaseConfig(t, root), skillMD, plugin); len(got) > 0 {
		t.Errorf("skill files release-please does not stamp (add them to extra-files of the owning package in .github/release-please-config.json):\n%s", strings.Join(got, "\n"))
	}
}

func TestUnregisteredStampFilesNamesEachGap(t *testing.T) {
	var cfg rpConfig
	if err := json.Unmarshal([]byte(`{"packages":{
	  ".":{"extra-files":[
	    {"type":"generic","path":"skills/a/SKILL.md"},
	    {"type":"json","path":"skills/a/.claude-plugin/plugin.json","jsonpath":"$.version"}]},
	  "sdk/ts":{"extra-files":[
	    {"type":"generic","path":"skills/b/SKILL.md"},
	    {"type":"json","path":"/sdk/ts/skills/b/.claude-plugin/plugin.json","jsonpath":"$.name"}]}}}`), &cfg); err != nil {
		t.Fatal(err)
	}
	got := unregisteredStampFiles(cfg,
		[]string{"skills/a/SKILL.md", "skills/c/SKILL.md", "sdk/ts/skills/b/SKILL.md", "sdk/ts/skills/d/SKILL.md"},
		[]string{"skills/a/.claude-plugin/plugin.json", "sdk/ts/skills/b/.claude-plugin/plugin.json"})
	want := []string{
		"skills/c/SKILL.md: not a generic extra-file of package .",
		"sdk/ts/skills/d/SKILL.md: not a generic extra-file of package sdk/ts",
		"sdk/ts/skills/b/.claude-plugin/plugin.json: not a json $.version extra-file of package sdk/ts",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("got:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

// rpVersion is release-please 17.11.2's generic-updater pattern, verbatim: the
// prerelease group `[\w.]+` takes a period that follows the version.
var rpVersion = regexp.MustCompile(`(\d+)\.(\d+)\.(\d+)(-[\w.]+)?(\+[-\w.]+)?`)

// stampLikeRelease rewrites, as the generic updater does, the first version
// on every line that carries the annotation.
func stampLikeRelease(t *testing.T, root, rel, version string) {
	t.Helper()
	p := filepath.Join(root, rel)
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(string(b), "\n")
	for i, l := range lines {
		if strings.Contains(l, versionAnnotation) {
			lines[i] = rpVersion.ReplaceAllString(l, version)
		}
	}
	if err := os.WriteFile(p, []byte(strings.Join(lines, "\n")), 0o644); err != nil {
		t.Fatal(err)
	}
}

// A release PR rewrites the annotated lines with release-please's own regex;
// the generated block must still be what the generator writes, for a
// prerelease replacing a prerelease as much as for any other bump.
func TestReleaseStampingLeavesSyncCheckClean(t *testing.T) {
	root := stage(t, "full")
	if code, _, e := runCLI(t, root, "sync"); code != 0 {
		t.Fatal(e)
	}
	ts := "packages/ts/skills/acme-kit-ts/SKILL.md"
	pj := "packages/ts/skills/acme-kit-ts/.claude-plugin/plugin.json"
	for _, v := range []string{"1.1.0-alpha.1", "1.1.0-alpha.2", "1.1.0", "1.1.1"} {
		stampLikeRelease(t, root, ts, v)
		edit(t, root, pj, `"version": "`+readJSON(t, root, pj)["version"].(string)+`"`, `"version": "`+v+`"`)
		edit(t, root, "manifest.json", `"packages/ts": "`+readJSON(t, root, "manifest.json")["packages/ts"].(string)+`"`, `"packages/ts": "`+v+`"`)
		if code, out, e := runCLI(t, root, "sync", "--check"); code != 0 {
			t.Fatalf("after a release to %s, sync --check fails:\n%s%s", v, out, e)
		}
	}
}
