package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSyncGolden(t *testing.T) {
	for _, name := range []string{"full", "partial", "none"} {
		t.Run(name, func(t *testing.T) {
			root := stage(t, name)
			before := snapshot(t, root)
			code, out, errOut := runCLI(t, root, "sync")
			if code != 0 {
				t.Fatalf("sync exit %d\nstdout: %s\nstderr: %s", code, out, errOut)
			}
			compareGolden(t, name, changed(before, snapshot(t, root)))
		})
	}
}

func TestSyncReportsSkillCount(t *testing.T) {
	for name, want := range map[string]string{"none": "0 of 6 skills present", "partial": "2 of 6 skills present", "full": "6 of 6 skills present"} {
		t.Run(name, func(t *testing.T) {
			root := stage(t, name)
			code, out, errOut := runCLI(t, root, "sync")
			if code != 0 || !strings.Contains(out, want) {
				t.Errorf("exit %d, want %q in output:\n%s%s", code, want, out, errOut)
			}
			code, out, errOut = runCLI(t, root, "sync", "--check")
			if code != 0 || !strings.Contains(out, want) {
				t.Errorf("check: exit %d, want %q in output:\n%s%s", code, want, out, errOut)
			}
		})
	}
}

func TestSyncIdempotent(t *testing.T) {
	for _, name := range []string{"full", "partial", "none"} {
		t.Run(name, func(t *testing.T) {
			root := stage(t, name)
			if code, _, e := runCLI(t, root, "sync"); code != 0 {
				t.Fatal(e)
			}
			first := snapshot(t, root)
			code, out, e := runCLI(t, root, "sync")
			if code != 0 {
				t.Fatal(e)
			}
			if d := changed(first, snapshot(t, root)); len(d) != 0 {
				t.Fatalf("second run changed %v", sortedKeys(d))
			}
			if !strings.Contains(out, "wrote 0 files") {
				t.Errorf("second run should report 0 files written, got %q", out)
			}
			if code, _, e := runCLI(t, root, "sync", "--check"); code != 0 {
				t.Fatalf("check after sync: %s", e)
			}
		})
	}
}

func TestCheckDriftNamesStaleFile(t *testing.T) {
	cases := []struct {
		name  string
		stale string
		mut   func(t *testing.T, root string)
	}{
		{"managed block", "packages/ts/skills/acme-kit-ts/SKILL.md", func(t *testing.T, root string) {
			edit(t, root, "packages/ts/skills/acme-kit-ts/SKILL.md", "Tier: supported.", "Tier: made up.")
		}},
		{"router block", "skills/acme-kit/SKILL.md", func(t *testing.T, root string) {
			edit(t, root, "skills/acme-kit/SKILL.md", "| Go |", "| Golang |")
		}},
		{"generated reference", "skills/acme-kit/references/capability-matrix.md", func(t *testing.T, root string) {
			edit(t, root, "skills/acme-kit/references/capability-matrix.md", "| Job queue |", "| Job queues |")
		}},
		{"other-ports", "packages/py/skills/acme-kit-py/references/other-ports.md", func(t *testing.T, root string) {
			edit(t, root, "packages/py/skills/acme-kit-py/references/other-ports.md", "Go only", "Go-only")
		}},
		{"plugin manifest", "packages/rs/skills/acme-kit-rs/.claude-plugin/plugin.json", func(t *testing.T, root string) {
			edit(t, root, "packages/rs/skills/acme-kit-rs/.claude-plugin/plugin.json", `"keywords"`, `"tags"`)
		}},
		{"gym", "skills/acme-kit-go/gym.json", func(t *testing.T, root string) {
			edit(t, root, "skills/acme-kit-go/gym.json", "public", "private")
		}},
		{"skills.json", "skills.json", func(t *testing.T, root string) {
			edit(t, root, "skills.json", "acme-kit-php", "acme-kit-pph")
		}},
		{"marketplace entries", "claude-marketplace-entries.json", func(t *testing.T, root string) {
			edit(t, root, "claude-marketplace-entries.json", "git-subdir", "github")
		}},
		{"copy", "packages/php/skills/acme-kit-php/scripts/detect.sh", func(t *testing.T, root string) {
			edit(t, root, "packages/php/skills/acme-kit-php/scripts/detect.sh", "prints the port", "prints a port")
		}},
		{"deleted file", "skills/acme-kit/references/go-only.md", func(t *testing.T, root string) {
			if err := os.Remove(filepath.Join(root, "skills/acme-kit/references/go-only.md")); err != nil {
				t.Fatal(err)
			}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := stage(t, "full")
			if code, _, e := runCLI(t, root, "sync"); code != 0 {
				t.Fatal(e)
			}
			if code, _, e := runCLI(t, root, "sync", "--check"); code != 0 {
				t.Fatalf("clean tree must pass --check: %s", e)
			}
			tc.mut(t, root)
			before := snapshot(t, root)
			code, out, errOut := runCLI(t, root, "sync", "--check")
			if code != 1 {
				t.Fatalf("exit %d, want 1\n%s%s", code, out, errOut)
			}
			all := out + errOut
			if !strings.Contains(all, tc.stale) {
				t.Errorf("output must name %s:\n%s", tc.stale, all)
			}
			if !strings.Contains(all, "run: make skills-sync") {
				t.Errorf("output must say how to fix:\n%s", all)
			}
			if d := changed(before, snapshot(t, root)); len(d) != 0 {
				t.Errorf("--check wrote %v", sortedKeys(d))
			}
			// the fix works
			if code, _, e := runCLI(t, root, "sync"); code != 0 {
				t.Fatal(e)
			}
			if code, _, e := runCLI(t, root, "sync", "--check"); code != 0 {
				t.Fatalf("after resync: %s", e)
			}
		})
	}
}

func readJSON(t *testing.T, root, rel string) map[string]any {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(root, rel))
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("%s: %v", rel, err)
	}
	return m
}

func TestVersionWrittenOnceThenPreserved(t *testing.T) {
	root := stage(t, "full")
	if code, _, e := runCLI(t, root, "sync"); code != 0 {
		t.Fatal(e)
	}
	pj := "packages/ts/skills/acme-kit-ts/.claude-plugin/plugin.json"
	if v := readJSON(t, root, pj)["version"]; v != "0.3.0-alpha.4" {
		t.Fatalf("first write takes the release manifest version, got %v", v)
	}
	if v := readJSON(t, root, "skills/acme-kit-go/.claude-plugin/plugin.json")["version"]; v != "1.4.0-beta.2" {
		t.Fatalf("root component version, got %v", v)
	}
	// a release PR bumps versions
	edit(t, root, pj, "0.3.0-alpha.4", "0.3.0-alpha.5")
	edit(t, root, "skills.json", `"version": "0.3.0-alpha.4"`, `"version": "0.3.0-alpha.5"`)
	edit(t, root, "manifest.json", "0.3.0-alpha.4", "0.3.0-alpha.9")
	// release tooling bumps the annotated stamp and the annotated block line
	// that copies it; it never touches min_version
	ts := "packages/ts/skills/acme-kit-ts/SKILL.md"
	edit(t, root, ts, `version: "1.0.0" # x-release-please-version`, `version: "1.0.1" # x-release-please-version`)
	edit(t, root, ts, "Written for 1.0.0 # x-release-please-version", "Written for 1.0.1 # x-release-please-version")
	if code, out, e := runCLI(t, root, "sync", "--check"); code != 0 {
		t.Fatalf("a release PR must not fail --check:\n%s%s", out, e)
	}
	before := snapshot(t, root)
	if code, _, e := runCLI(t, root, "sync"); code != 0 {
		t.Fatal(e)
	}
	if d := changed(before, snapshot(t, root)); len(d) != 0 {
		t.Fatalf("sync rewrote versions: %v", sortedKeys(d))
	}
	if v := readJSON(t, root, pj)["version"]; v != "0.3.0-alpha.5" {
		t.Fatalf("version regenerated: %v", v)
	}
}

// A release that bumps metadata.version but not the block line that copies it
// leaves drift: the annotation on that line is what keeps release PRs green.
func TestReleaseBumpOfStampAloneIsDriftInTheBlock(t *testing.T) {
	root := stage(t, "full")
	if code, _, e := runCLI(t, root, "sync"); code != 0 {
		t.Fatal(e)
	}
	ts := "packages/ts/skills/acme-kit-ts/SKILL.md"
	edit(t, root, ts, `version: "1.0.0" # x-release-please-version`, `version: "1.0.1" # x-release-please-version`)
	code, out, e := runCLI(t, root, "sync", "--check")
	if code != 1 || !strings.Contains(out+e, ts) {
		t.Fatalf("exit %d, want 1 naming %s:\n%s%s", code, ts, out, e)
	}
}

func TestSkillsJSONOnlyListsPresentSkillsInPortsOrder(t *testing.T) {
	root := stage(t, "partial")
	if code, _, e := runCLI(t, root, "sync"); code != 0 {
		t.Fatal(e)
	}
	m := readJSON(t, root, "skills.json")
	var names []string
	for _, s := range m["skills"].([]any) {
		names = append(names, s.(map[string]any)["name"].(string))
	}
	if got := strings.Join(names, ","); got != "acme-kit,acme-kit-py" {
		t.Fatalf("skills.json names = %s", got)
	}
}

func TestCopyKeepsExecutableBit(t *testing.T) {
	root := stage(t, "full")
	if code, _, e := runCLI(t, root, "sync"); code != 0 {
		t.Fatal(e)
	}
	for _, p := range []string{"skills/acme-kit-go", "packages/php/skills/acme-kit-php"} {
		info, err := os.Stat(filepath.Join(root, p, "scripts/detect.sh"))
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm()&0o111 == 0 {
			t.Errorf("%s: copy lost the executable bit (%v)", p, info.Mode())
		}
	}
	if _, err := os.Stat(filepath.Join(root, "skills/acme-kit/scripts/detect.sh.bak")); err == nil {
		t.Error("stray file")
	}
}

func TestNoMarketplaceFileIsWritten(t *testing.T) {
	root := stage(t, "full")
	if code, _, e := runCLI(t, root, "sync"); code != 0 {
		t.Fatal(e)
	}
	for p := range snapshot(t, root) {
		if strings.HasSuffix(p, ".claude-plugin/marketplace.json") {
			t.Errorf("must not write %s", p)
		}
	}
}

func TestPluginManifestShape(t *testing.T) {
	root := stage(t, "full")
	if code, _, e := runCLI(t, root, "sync"); code != 0 {
		t.Fatal(e)
	}
	m := readJSON(t, root, "packages/py/skills/acme-kit-py/.claude-plugin/plugin.json")
	for _, bad := range []string{"skills", "commands", "hooks", "mcpServers", "agents"} {
		if _, ok := m[bad]; ok {
			t.Errorf("plugin manifest carries component key %q", bad)
		}
	}
	want := map[string]any{
		"name":        "acme-kit-py",
		"description": "Skill acme-kit-py. Use when testing.",
		"license":     "MIT",
		"homepage":    "https://example.test/acme/kit-py/blob/main/skills/acme-kit-py/SKILL.md",
		"repository":  "https://example.test/acme/kit-py",
	}
	for k, v := range want {
		if m[k] != v {
			t.Errorf("%s = %v, want %v", k, m[k], v)
		}
	}
	if len(m) != 8 { // name version description author homepage repository license keywords
		t.Errorf("unexpected keys: %v", m)
	}
}

func TestDocsBranchIsOneValue(t *testing.T) {
	root := stage(t, "full")
	edit(t, root, "ports.yaml", "branch: next", "branch: trunk")
	if code, _, e := runCLI(t, root, "sync"); code != 0 {
		t.Fatal(e)
	}
	seen := 0
	for p, c := range snapshot(t, root) {
		if strings.HasPrefix(p, "manifest") || p == "ports.yaml" || p == "parity.md" || p == "tiers.md" || p == "publish.yml" {
			continue
		}
		if strings.Contains(c, "example.test/acme/mono/blob/next/") {
			t.Errorf("%s still links the old docs branch", p)
		}
		if strings.Contains(c, "example.test/acme/mono/blob/trunk/parity.md") {
			seen++
		}
	}
	if seen == 0 {
		t.Fatal("no generated file links the docs branch")
	}
	// skill and mirror links keep the mirror branch
	b, _ := os.ReadFile(filepath.Join(root, "skills/acme-kit/SKILL.md"))
	if !strings.Contains(string(b), "example.test/acme/kit-ts/blob/main/skills/acme-kit-ts/SKILL.md") {
		t.Errorf("mirror links must keep mirror_branch:\n%s", b)
	}
}

func TestRequireAll(t *testing.T) {
	root := stage(t, "partial")
	code, _, e := runCLI(t, root, "sync", "--require-all")
	if code != 1 {
		t.Fatalf("exit %d", code)
	}
	for _, n := range []string{"acme-kit-go", "acme-kit-ts", "acme-kit-rs", "acme-kit-php"} {
		if !strings.Contains(e, n) {
			t.Errorf("must name missing skill %s: %s", n, e)
		}
	}
	root = stage(t, "full")
	if code, _, e := runCLI(t, root, "sync", "--require-all"); code != 0 {
		t.Fatal(e)
	}
}

func TestExpected(t *testing.T) {
	root := stage(t, "none")
	cases := []struct {
		component string
		want      string
		code      int
	}{
		{"kit-py", "acme/kit-py\tpackages/py/skills/acme-kit-py\tskills/acme-kit-py\n", 0},
		{"kit", "acme/kit\tskills/acme-kit\tskills/acme-kit\nacme/kit\tskills/acme-kit-go\tskills/acme-kit-go\n", 0},
		{"tasks", "", 0},
		{"nope", "", 2},
	}
	for _, tc := range cases {
		code, out, e := runCLI(t, root, "expected", "--component", tc.component)
		if code != tc.code || out != tc.want {
			t.Errorf("%s: exit %d out %q err %q; want exit %d out %q", tc.component, code, out, e, tc.code, tc.want)
		}
	}
	if code, _, _ := runCLI(t, root, "expected"); code != 2 {
		t.Errorf("missing --component exits %d, want 2", code)
	}
}

func TestUsage(t *testing.T) {
	var o, e strings.Builder
	if code := run(nil, &o, &e); code != 2 {
		t.Errorf("no command exits %d", code)
	}
	if code := run([]string{"frobnicate"}, &o, &e); code != 2 {
		t.Errorf("unknown command exits %d", code)
	}
}

func TestGuards(t *testing.T) {
	cases := []struct {
		name string
		mut  func(t *testing.T, root string)
		want func(t *testing.T, root string) []string
	}{
		{"summary disagrees with rows",
			func(t *testing.T, r string) {
				edit(t, r, "parity.md", "| TypeScript | 4 none, 2 done, 1 unverified, 1 n/a |", "| TypeScript | 5 none, 2 done, 1 unverified, 1 n/a |")
			},
			func(t *testing.T, r string) []string {
				return []string{fmt.Sprintf("parity.md:%d", lineOf(t, r, "parity.md", "| TypeScript |")), "Summary", "TypeScript", "4 none", "5 none"}
			}},
		{"summary missing a port",
			func(t *testing.T, r string) {
				edit(t, r, "parity.md", "| PHP | 5 none, 1 done, 1 partial, 1 n/a |\n", "")
			},
			func(t *testing.T, r string) []string { return []string{"Summary", "PHP"} }},
		{"summary unknown word",
			func(t *testing.T, r string) { edit(t, r, "parity.md", "| Go | 8 done |", "| Go | 8 finished |") },
			func(t *testing.T, r string) []string {
				return []string{fmt.Sprintf("parity.md:%d", lineOf(t, r, "parity.md", "| Go | 8 finished")), "finished"}
			}},
		{"unknown status word",
			func(t *testing.T, r string) {
				edit(t, r, "parity.md", "| Logging | done | unverified", "| Logging | done | verified")
			},
			func(t *testing.T, r string) []string {
				return []string{fmt.Sprintf("parity.md:%d", lineOf(t, r, "parity.md", "| Logging |")), `unknown status "verified"`}
			}},
		{"six columns",
			func(t *testing.T, r string) {
				edit(t, r, "parity.md", "| Job queue | done | none | none | none | none |", "| Job queue | done | none | none | none |")
			},
			func(t *testing.T, r string) []string {
				return []string{fmt.Sprintf("parity.md:%d", lineOf(t, r, "parity.md", "| Job queue |")), "7 columns", "got 6"}
			}},
		{"eight columns",
			func(t *testing.T, r string) {
				edit(t, r, "parity.md", "| Job queue | done | none | none | none | none |", "| Job queue | done | none | none | none | none | extra |")
			},
			func(t *testing.T, r string) []string {
				return []string{fmt.Sprintf("parity.md:%d", lineOf(t, r, "parity.md", "| Job queue |")), "7 columns", "got 8"}
			}},
		{"header differs between tables",
			func(t *testing.T, r string) {
				edit(t, r, "parity.md", "| Capability | Go | TS | Py | Rust | PHP | Evidence |\n|---|---|---|---|---|---|---|\n| Serve", "| Capability | Go | TS | Rust | Py | PHP | Evidence |\n|---|---|---|---|---|---|---|\n| Serve")
			},
			func(t *testing.T, r string) []string { return []string{"parity.md:", "header"} }},
		{"status vocabulary changed",
			func(t *testing.T, r string) {
				edit(t, r, "parity.md", "| unverified | A module exists but nothing pins it to Go |\n", "")
			},
			func(t *testing.T, r string) []string { return []string{"vocabulary", "unverified"} }},
		{"duplicate capability",
			func(t *testing.T, r string) { edit(t, r, "parity.md", "| Job queue | done", "| Logging | done") },
			func(t *testing.T, r string) []string { return []string{"duplicate", "Logging"} }},
		{"publish entry in neither ports nor no_skill",
			func(t *testing.T, r string) {
				edit(t, r, "publish.yml", "        kit-ts:\n", "        kit-new:\n          dir: packages/new\n          ecosystem: go\n          mirror: acme/kit-new\n        kit-ts:\n")
			},
			func(t *testing.T, r string) []string { return []string{"kit-new", "no_skill"} }},
		{"ports entry absent from publish",
			func(t *testing.T, r string) {
				edit(t, r, "publish.yml", "        kit-php:\n          dir: packages/php\n          ecosystem: php\n          package: acme/kit\n          mirror: acme/kit-php\n", "")
			},
			func(t *testing.T, r string) []string { return []string{"kit-php", "publish"} }},
		{"no_skill entry absent from publish",
			func(t *testing.T, r string) {
				edit(t, r, "ports.yaml", "no_skill: [tasks]", "no_skill: [tasks, ghost]")
			},
			func(t *testing.T, r string) []string { return []string{"ghost", "no_skill"} }},
		{"component in ports and no_skill",
			func(t *testing.T, r string) {
				edit(t, r, "ports.yaml", "no_skill: [tasks]", "no_skill: [tasks, kit-ts]")
			},
			func(t *testing.T, r string) []string { return []string{"kit-ts", "no_skill"} }},
		{"parity column claimed by no port",
			func(t *testing.T, r string) { edit(t, r, "ports.yaml", "column: TS", "column: Ts") },
			func(t *testing.T, r string) []string { return []string{"column", "TS"} }},
		{"summary label differs from language",
			func(t *testing.T, r string) { edit(t, r, "ports.yaml", "language: Python", "language: Snake") },
			func(t *testing.T, r string) []string { return []string{"Summary", "Snake"} }},
		{"no tier for a port",
			func(t *testing.T, r string) { edit(t, r, "tiers.md", ", Rust (`packages/rs`) *(proposed)*", "") },
			func(t *testing.T, r string) []string { return []string{"kit-rs", "tier"} }},
		{"tier given twice",
			func(t *testing.T, r string) {
				edit(t, r, "ports.yaml", "    language: Python\n", "    language: Python\n    tier: supported\n")
			},
			func(t *testing.T, r string) []string { return []string{"kit-py", "tier"} }},
		{"release manifest lacks a component",
			func(t *testing.T, r string) {
				edit(t, r, "manifest.json", `  "packages/ts": "0.3.0-alpha.4"`, `  "packages/zz": "0.3.0-alpha.4"`)
			},
			func(t *testing.T, r string) []string { return []string{"packages/ts", "release manifest"} }},
		{"markers missing",
			func(t *testing.T, r string) {
				edit(t, r, "packages/py/skills/acme-kit-py/SKILL.md", "<!-- skills-sync:begin coverage -->\n", "")
			},
			func(t *testing.T, r string) []string {
				return []string{"packages/py/skills/acme-kit-py/SKILL.md", "skills-sync:begin coverage"}
			}},
		{"markers out of order",
			func(t *testing.T, r string) {
				edit(t, r, "skills/acme-kit/SKILL.md", "<!-- skills-sync:begin ports -->\nstale\n<!-- skills-sync:end ports -->", "<!-- skills-sync:end ports -->\nstale\n<!-- skills-sync:begin ports -->")
			},
			func(t *testing.T, r string) []string { return []string{"skills/acme-kit/SKILL.md", "ports"} }},
		{"frontmatter name differs",
			func(t *testing.T, r string) {
				edit(t, r, "skills/acme-kit-go/SKILL.md", "name: acme-kit-go", "name: other")
			},
			func(t *testing.T, r string) []string {
				return []string{"skills/acme-kit-go/SKILL.md", "acme-kit-go", "other"}
			}},
		{"frontmatter license missing",
			func(t *testing.T, r string) { edit(t, r, "skills/acme-kit-go/SKILL.md", "license: MIT\n", "") },
			func(t *testing.T, r string) []string { return []string{"skills/acme-kit-go/SKILL.md", "license"} }},
		{"copy source missing",
			func(t *testing.T, r string) {
				if err := os.Remove(filepath.Join(r, "skills/acme-kit/scripts/detect.sh")); err != nil {
					t.Fatal(err)
				}
			},
			func(t *testing.T, r string) []string { return []string{"detect.sh"} }},
	}
	for _, tc := range cases {
		for _, cmd := range [][]string{{"sync"}, {"sync", "--check"}} {
			t.Run(tc.name+"/"+strings.Join(cmd, " "), func(t *testing.T) {
				root := stage(t, "full")
				tc.mut(t, root)
				before := snapshot(t, root)
				code, out, errOut := runCLI(t, root, cmd...)
				if code != 1 {
					t.Fatalf("exit %d, want 1\n%s%s", code, out, errOut)
				}
				for _, w := range tc.want(t, root) {
					if !strings.Contains(errOut, w) {
						t.Errorf("stderr lacks %q:\n%s", w, errOut)
					}
				}
				if d := changed(before, snapshot(t, root)); len(d) != 0 {
					t.Errorf("a failed run wrote %v", sortedKeys(d))
				}
			})
		}
	}
}

// The real tree: the parity page parses, agrees with its own Summary, and
// ports.yaml agrees with the publish matrix.
func TestRealTree(t *testing.T) {
	root := filepath.Join("..", "..", "..")
	o := DefaultOptions(root)
	plan, err := BuildPlan(o)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Expected == 0 {
		t.Fatal("no expected skills")
	}
	par, err := loadParity(filepath.Join(root, o.Parity))
	if err != nil {
		t.Fatal(err)
	}
	rows, goOnly := 0, 0
	for _, s := range par.Sections {
		rows += len(s.Caps)
	}
	for _, s := range par.Sections {
		for _, c := range s.Caps {
			if c.goOnly(0) {
				goOnly++
			}
		}
	}
	if len(par.Sections) != 7 || rows != 74 || goOnly != 24 {
		t.Errorf("sections %d rows %d go-only %d, want 7, 74, 24", len(par.Sections), rows, goOnly)
	}
}
