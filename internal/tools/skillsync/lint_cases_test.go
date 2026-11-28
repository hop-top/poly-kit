package main

import (
	"io/fs"
	"strings"
	"testing"
)

const (
	tsDir  = "packages/ts/skills/acme-kit-ts"
	pyDir  = "packages/py/skills/acme-kit-py"
	rsDir  = "packages/rs/skills/acme-kit-rs"
	phpDir = "packages/php/skills/acme-kit-php"
	goDir  = "skills/acme-kit-go"
)

const (
	tsDesc  = "  Use the Acme kit from TypeScript. Use when the project is TypeScript and needs the kit."
	tsStamp = `  version: "0.3.0-alpha.4" # x-release-please-version`
	tsMin   = `  min_version: "0.3.0-alpha.4"`
	srcLine = "\nSource of truth:"
)

func w(pairs ...string) map[string]string {
	m := map[string]string{}
	for i := 0; i < len(pairs); i += 2 {
		m[pairs[i]] = pairs[i+1]
	}
	return m
}

// addLine puts text before the "Source of truth" line of a SKILL.md.
func addLine(dir, text string) [3]string {
	return [3]string{dir + "/SKILL.md", srcLine, "\n" + text + "\n" + srcLine}
}

func lines(n int, f string) string { return strings.Repeat(f+"\n", n) }

func fence(n int) string { return "```sh\n" + lines(n, "echo hello") + "```" }

// ruleCases pins, per rule, a minimal violating variant of the clean
// fixtures and the exact finding it must yield.
var ruleCases = []lintCase{
	// S1 frontmatter
	{Name: "S1/unknown-key", Mut: mutation{Edits: [][3]string{{tsDir + "/SKILL.md", "license: MIT\n", "license: MIT\nauthor: someone\n"}}},
		Want: []want{{File: "SKILL.md", Anchor: "author:", Rule: "S1"}}},
	{Name: "S1/name-not-directory", Mut: mutation{Edits: [][3]string{{tsDir + "/SKILL.md", "name: acme-kit-ts\n", "name: acme-kit-tz\n"}}},
		Want: []want{{File: "SKILL.md", Anchor: "name: acme-kit-tz", Rule: "S1"}}},
	{Name: "S1/name-pattern", Standalone: true, Dir: "Bad_Name",
		Mut: mutation{Copy: [][2]string{{tsDir, "Bad_Name"}}, Edits: [][3]string{
			{"Bad_Name/SKILL.md", "name: acme-kit-ts\n", "name: Bad_Name\n"},
			{"Bad_Name/.claude-plugin/plugin.json", `"name": "acme-kit-ts"`, `"name": "Bad_Name"`}}},
		Want: []want{{File: "SKILL.md", Anchor: "name: Bad_Name", Rule: "S1"}}},
	{Name: "S1/name-length", Standalone: true, Dir: strings.Repeat("a", 65),
		Mut: mutation{Copy: [][2]string{{tsDir, strings.Repeat("a", 65)}}, Edits: [][3]string{
			{strings.Repeat("a", 65) + "/SKILL.md", "name: acme-kit-ts\n", "name: " + strings.Repeat("a", 65) + "\n"},
			{strings.Repeat("a", 65) + "/.claude-plugin/plugin.json", `"name": "acme-kit-ts"`, `"name": "` + strings.Repeat("a", 65) + `"`}}},
		Want: []want{{File: "SKILL.md", Anchor: "name: aaaa", Rule: "S1"}}},
	{Name: "S1/description-too-long", Mut: mutation{Edits: [][3]string{{tsDir + "/SKILL.md", tsDesc, tsDesc + strings.Repeat(" word", 220)}}},
		Want: []want{{File: "SKILL.md", Anchor: "description:", Rule: "S1"}}},
	{Name: "S1/no-use-when", Mut: mutation{Edits: [][3]string{{tsDir + "/SKILL.md", tsDesc, "  Use the Acme kit from TypeScript."}}},
		Want: []want{{File: "SKILL.md", Anchor: "description:", Rule: "S1"}}},
	{Name: "S1/flow-style", Mut: mutation{Edits: [][3]string{{tsDir + "/SKILL.md", "license: MIT\n", "license: MIT\nallowed-tools: [Read, Bash]\n"}}},
		Want: []want{{File: "SKILL.md", Anchor: "allowed-tools:", Rule: "S1"}}},
	{Name: "S1/no-license", Mut: mutation{Edits: [][3]string{{tsDir + "/SKILL.md", "license: MIT\n", ""}}},
		Want: []want{{File: "SKILL.md", Line: 1, Rule: "S1"}}},
	{Name: "S1/no-skill-md", Mut: mutation{Remove: []string{tsDir + "/SKILL.md"}},
		Want: []want{{File: "SKILL.md", Line: 1, Rule: "S1"}}},
	{Name: "S1/no-frontmatter", Mut: mutation{Edits: [][3]string{{tsDir + "/SKILL.md", "---\nname: acme-kit-ts\n", "name: acme-kit-ts\n"}}},
		Want: []want{{File: "SKILL.md", Line: 1, Rule: "S1"}}},

	// S2 caps and layout
	{Name: "S2/skill-over-cap", Mut: mutation{Edits: [][3]string{addLine(tsDir, lines(100, "filler"))}},
		Want: []want{{File: "SKILL.md", Line: 121, Rule: "S2"}}},
	{Name: "S2/reference-over-cap", Mut: mutation{Write: w(tsDir+"/references/big.md", lines(201, "filler")),
		Edits: [][3]string{addLine(tsDir, "Read references/big.md for the long form.")}},
		Want: []want{{File: "references/big.md", Line: 201, Rule: "S2"}}},
	{Name: "S2/reference-at-cap", Mut: mutation{Write: w(tsDir+"/references/big.md", lines(200, "filler")),
		Edits: [][3]string{addLine(tsDir, "Read references/big.md for the long form.")}}},
	{Name: "S2/directory", Mut: mutation{Write: w(tsDir+"/docs/notes.md", "notes\n")},
		Want: []want{{File: "docs", Rule: "S2"}}},
	{Name: "S2/top-level-file", Mut: mutation{Write: w(tsDir+"/NOTES.md", "notes\n")},
		Want: []want{{File: "NOTES.md", Rule: "S2"}}},
	{Name: "S2/references-not-flat", Mut: mutation{Write: w(tsDir+"/references/sub/x.md", "x\n"),
		Edits: [][3]string{addLine(tsDir, "Read references/sub/x.md when needed.")}},
		Want: []want{{File: "references/sub/x.md", Rule: "S2"}}},
	{Name: "S2/assets-not-snippets", Mut: mutation{Write: w(tsDir+"/assets/logo.txt", "logo\n")},
		Want: []want{{File: "assets/logo.txt", Rule: "S2"}}},
	{Name: "S2/snippet-too-deep", Mut: mutation{Write: w(tsDir+"/assets/snippets/a/b/c.txt", "c\n"),
		Edits: [][3]string{addLine(tsDir, "Copy assets/snippets/a when starting.")}},
		Want: []want{{File: "assets/snippets/a/b/c.txt", Rule: "S2"}}},
	{Name: "S2/plugin-dir-extra", Mut: mutation{Write: w(tsDir+"/.claude-plugin/extra.json", "{}\n")},
		Want: []want{{File: ".claude-plugin/extra.json", Rule: "S2"}}},

	// S3 discovery
	{Name: "S3/file-not-named", Mut: mutation{Write: w(tsDir+"/references/extra.md", "extra\n")},
		Want: []want{{File: "references/extra.md", Rule: "S3"}}},
	{Name: "S3/script-not-named", Mut: mutation{Write: w(tsDir+"/scripts/other.sh", "#!/bin/sh\nexit 0\n"), Chmod: map[string]fs.FileMode{tsDir + "/scripts/other.sh": 0o755}},
		Want: []want{{File: "scripts/other.sh", Rule: "S3"}}},
	{Name: "S3/broken-link", Mut: mutation{Edits: [][3]string{addLine(tsDir, "See [the page](references/missing.md).")}},
		Want: []want{{File: "SKILL.md", Anchor: "references/missing.md", Rule: "S3"}}},

	// S4 links
	{Name: "S4/relative-leaves-skill", Mut: mutation{Edits: [][3]string{addLine(tsDir, "Sibling: [router](../../../../../skills/acme-kit/SKILL.md).")}},
		Want: []want{{File: "SKILL.md", Anchor: "../../../../../skills", Rule: "S4"}}},
	{Name: "S4/absolute-path", Mut: mutation{Edits: [][3]string{addLine(tsDir, "See [docs](/docs/x.md).")}},
		Want: []want{{File: "SKILL.md", Anchor: "](/docs/x.md)", Rule: "S4"}}},
	{Name: "S4/docs-other-branch", Mut: mutation{Edits: [][3]string{{tsDir + "/SKILL.md", "/blob/next/parity.md", "/blob/main/parity.md"}}},
		Want: []want{{File: "SKILL.md", Anchor: "mono/blob/main", Rule: "S4"}}},
	{Name: "S4/skill-other-branch", Mut: mutation{Edits: [][3]string{{tsDir + "/SKILL.md", "kit/blob/main/skills/acme-kit/SKILL.md", "kit/blob/next/skills/acme-kit/SKILL.md"}}},
		Want: []want{{File: "SKILL.md", Anchor: "blob/next", Rule: "S4"}}},
	{Name: "S4/docs-path-missing", Mut: mutation{Edits: [][3]string{addLine(tsDir, "See https://example.test/acme/mono/blob/next/docs/missing.md for more.")}},
		Want: []want{{File: "SKILL.md", Anchor: "docs/missing.md", Rule: "S4"}}},
	{Name: "S4/not-a-skill-url", Mut: mutation{Edits: [][3]string{addLine(tsDir, "See https://example.test/acme/kit/blob/main/skills/acme-kit-go/references/coverage.md for more.")}},
		Want: []want{{File: "SKILL.md", Anchor: "acme-kit-go/references", Rule: "S4"}}},
	{Name: "S4/repository-url", Mut: mutation{Edits: [][3]string{addLine(tsDir, "Source: https://example.test/acme/kit-ts for more.")}},
		Want: []want{{File: "SKILL.md", Anchor: "acme/kit-ts for", Rule: "S4"}}},
	{Name: "S4/other-host-allowed", Mut: mutation{Edits: [][3]string{addLine(tsDir, "Format: [agentskills](https://agentskills.io/specification).")}}},

	// S5 area truth
	{Name: "S5/area-without-capability", Skill: "acme-kit-php",
		Mut:  mutation{Write: w(phpDir+"/references/serving-and-transports.md", "serving\n"), Edits: [][3]string{addLine(phpDir, "Read references/serving-and-transports.md for serving.")}},
		Want: []want{{File: "references/serving-and-transports.md", Rule: "S5"}}},
	{Name: "S5/area-in-router-is-not-checked", Skill: "acme-kit",
		Mut: mutation{Write: w("skills/acme-kit/references/serving-and-transports.md", "serving\n"), Edits: [][3]string{addLine("skills/acme-kit", "Read references/serving-and-transports.md for serving.")}}},

	// S6 versions
	{Name: "S6/stamp-differs-from-manifest", Mut: mutation{Edits: [][3]string{
		{tsDir + "/SKILL.md", tsStamp, strings.Replace(tsStamp, "alpha.4", "alpha.5", 1)},
		{tsDir + "/SKILL.md", "Written for 0.3.0-alpha.4.", "Written for 0.3.0-alpha.5."},
		{tsDir + "/.claude-plugin/plugin.json", `"version": "0.3.0-alpha.4"`, `"version": "0.3.0-alpha.5"`}}},
		Want: []want{{File: "SKILL.md", Anchor: "alpha.5", Rule: "S6"}}},
	{Name: "S6/stamp-not-annotated", Mut: mutation{Edits: [][3]string{{tsDir + "/SKILL.md", tsStamp, `  version: "0.3.0-alpha.4"`}}},
		Want: []want{{File: "SKILL.md", Anchor: `version: "0.3.0-alpha.4"`, Rule: "S6"}}},
	{Name: "S6/no-stamp", Mut: mutation{Edits: [][3]string{{tsDir + "/SKILL.md", "metadata:\n" + tsStamp + "\n" + tsMin + "\n", ""}}},
		Want: []want{{File: "SKILL.md", Line: 1, Rule: "S6"}}},
	{Name: "S6/stray-version", Mut: mutation{Edits: [][3]string{addLine(tsDir, "Needs version 2.3.4 or later.")}},
		Want: []want{{File: "SKILL.md", Anchor: "2.3.4", Rule: "S6"}}},
	{Name: "S6/version-block-missing", Mut: mutation{Fn: dropVersionBlock(tsDir)},
		Want: []want{{File: "SKILL.md", Line: 1, Rule: "S6"}}},
	{Name: "S6/version-block-hand-edited", Mut: mutation{Edits: [][3]string{{tsDir + "/SKILL.md", "`pnpm add @acme/kit@latest`", "`pnpm add @acme/kit`"}}},
		Want: []want{{File: "SKILL.md", Anchor: "skills-sync:begin version", Rule: "S6"}}},
	{Name: "S6/version-block-hand-edited-version", Mut: mutation{Edits: [][3]string{{tsDir + "/SKILL.md", "`pnpm add @acme/kit@latest`", "`pnpm add @acme/kit@2.3.4`"}}},
		Want: []want{{File: "SKILL.md", Anchor: "skills-sync:begin version", Rule: "S6"}}},
	{Name: "S6/generated-line-copied-outside-block", Mut: mutation{Edits: [][3]string{addLine(tsDir, "- Expects 0.3.0-alpha.4 or newer.")}},
		Want: []want{{File: "SKILL.md", Anchor: "- Expects", Rule: "S6"}}},
	{Name: "S6/min-missing", Mut: mutation{Edits: [][3]string{{tsDir + "/SKILL.md", tsMin + "\n", ""}}},
		Want: []want{{File: "SKILL.md", Anchor: "metadata:", Rule: "S6"}}},
	{Name: "S6/min-not-a-release", Mut: mutation{Edits: [][3]string{{tsDir + "/SKILL.md", `min_version: "0.3.0-alpha.4"`, `min_version: "latest"`}}},
		Want: []want{{File: "SKILL.md", Anchor: "min_version:", Rule: "S6"}}},
	{Name: "S6/min-differs-from-ports", Mut: mutation{Edits: [][3]string{{tsDir + "/SKILL.md", `min_version: "0.3.0-alpha.4"`, `min_version: "0.3.0-alpha.3"`}}},
		Want: []want{{File: "SKILL.md", Anchor: "min_version:", Rule: "S6"}}},
	{Name: "S6/min-above-version", Mut: mutation{Fn: func(t *testing.T, root string) {
		edit(t, root, "ports.yaml", "min_version: 0.3.0-alpha.4", "min_version: 0.3.0-alpha.9")
		resync(t, root)
	}}, Want: []want{{File: "SKILL.md", Anchor: "min_version:", Rule: "S6"}}},
	{Name: "S6/min-below-version-allowed", Mut: mutation{Fn: func(t *testing.T, root string) {
		edit(t, root, "ports.yaml", "min_version: 0.3.0-alpha.4", "min_version: 0.3.0-alpha.1")
		resync(t, root)
	}}},
	{Name: "S6/annotated-version-allowed", Mut: mutation{Edits: [][3]string{addLine(tsDir, "Needs version 2.3.4 or later. # x-release-please-version")}}},

	// S7 module names
	{Name: "S7/ts-module", Mut: mutation{Edits: [][3]string{{tsDir + "/SKILL.md", "@hop-top/kit/cli`", "@hop-top/kit/clix`"}}},
		Want: []want{{File: "SKILL.md", Anchor: "kit/clix", Rule: "S7"}}},
	{Name: "S7/py-module", Skill: "acme-kit-py", Mut: mutation{Edits: [][3]string{{pyDir + "/SKILL.md", "hop_top_kit.bus", "hop_top_kit.busx"}}},
		Want: []want{{File: "SKILL.md", Anchor: "busx", Rule: "S7"}}},
	{Name: "S7/rust-feature", Skill: "acme-kit-rs", Mut: mutation{Edits: [][3]string{{rsDir + "/SKILL.md", `"api", "serve"]`, `"api", "servr"]`}}},
		Want: []want{{File: "SKILL.md", Anchor: "servr", Rule: "S7"}}},
	{Name: "S7/rust-feature-flag", Skill: "acme-kit-rs", Mut: mutation{Edits: [][3]string{{rsDir + "/SKILL.md", "--features serve", "--features serv"}}},
		Want: []want{{File: "SKILL.md", Anchor: "--features serv`", Rule: "S7"}}},
	{Name: "S7/go-package", Skill: "acme-kit-go", Mut: mutation{Edits: [][3]string{{goDir + "/SKILL.md", "hop.top/kit/go/core/upgrade`", "hop.top/kit/go/core/upgrad`"}}},
		Want: []want{{File: "SKILL.md", Anchor: "core/upgrad`", Rule: "S7"}}},
	{Name: "S7/php-namespace", Skill: "acme-kit-php", Mut: mutation{Edits: [][3]string{{phpDir + "/SKILL.md", `HopTop\Kit\Cli\Root`, `HopTop\Kit\Cli\Rootx`}}},
		Want: []want{{File: "SKILL.md", Anchor: `Rootx`, Rule: "S7"}}},

	// S8 hygiene
	{Name: "S8/tracker-id", Mut: mutation{Edits: [][3]string{addLine(tsDir, "Tracked as "+"T"+"-1234 upstream.")}},
		Want: []want{{File: "SKILL.md", Anchor: "upstream.", Rule: "S8"}}},
	{Name: "S8/standard-id-allowed", Mut: mutation{Edits: [][3]string{addLine(tsDir, "Hashes use SHA-256 as in RFC-2119 style notes.")}}},
	{Name: "S8/tracker-link", Mut: mutation{Edits: [][3]string{addLine(tsDir, "Task "+"tlc"+"://item")}},
		Want: []want{{File: "SKILL.md", Anchor: "Task ", Rule: "S8"}}},
	{Name: "S8/long-fence", Mut: mutation{Edits: [][3]string{addLine(tsDir, fence(16))}},
		Want: []want{{File: "SKILL.md", Anchor: "```sh", Rule: "S8"}}},
	{Name: "S8/fence-at-cap", Mut: mutation{Edits: [][3]string{addLine(tsDir, fence(15))}}},

	// S9 scripts
	{Name: "S9/shebang", Mut: mutation{Edits: [][3]string{{tsDir + "/scripts/detect.sh", "#!/bin/sh", "#!/bin/bash"}}},
		Want: []want{{File: "scripts/detect.sh", Rule: "S9"}}},
	{Name: "S9/not-executable", Mut: mutation{Chmod: map[string]fs.FileMode{tsDir + "/scripts/detect.sh": 0o644}},
		Want: []want{{File: "scripts/detect.sh", Rule: "S9"}}},

	// S10 plugin manifest
	{Name: "S10/no-manifest", Mut: mutation{Remove: []string{tsDir + "/.claude-plugin/plugin.json"}},
		Want: []want{{File: ".claude-plugin/plugin.json", Rule: "S10"}}},
	{Name: "S10/name", Mut: mutation{Edits: [][3]string{{tsDir + "/.claude-plugin/plugin.json", `"name": "acme-kit-ts"`, `"name": "other"`}}},
		Want: []want{{File: ".claude-plugin/plugin.json", Anchor: `"name"`, Rule: "S10"}}},
	{Name: "S10/version", Mut: mutation{Edits: [][3]string{{tsDir + "/.claude-plugin/plugin.json", `"version": "0.3.0-alpha.4"`, `"version": "0.3.0-alpha.9"`}}},
		Want: []want{{File: ".claude-plugin/plugin.json", Anchor: `"version"`, Rule: "S10"}}},
	{Name: "S10/foreign-key", Mut: mutation{Edits: [][3]string{{tsDir + "/.claude-plugin/plugin.json", `"license"`, `"skills": "./x",` + "\n  " + `"license"`}}},
		Want: []want{{File: ".claude-plugin/plugin.json", Anchor: `"skills"`, Rule: "S10"}}},
	{Name: "S10/nested-skills-dir", Mut: mutation{Write: w(tsDir+"/skills/x/note.txt", "x\n")},
		Want: []want{{File: "skills", Rule: "S10"}}},
	{Name: "S10/bin-dir", Mut: mutation{Write: w(tsDir+"/bin/x", "x\n")},
		Want: []want{{File: "bin", Rule: "S10"}}},
	{Name: "S10/claude-md", Mut: mutation{Write: w(tsDir+"/CLAUDE.md", "x\n")},
		Want: []want{{File: "CLAUDE.md", Rule: "S10"}}},
	{Name: "S10/reserved-name", Standalone: true, Dir: "claude-ts",
		Mut: mutation{Copy: [][2]string{{tsDir, "claude-ts"}}, Edits: [][3]string{
			{"claude-ts/SKILL.md", "name: acme-kit-ts\n", "name: claude-ts\n"},
			{"claude-ts/.claude-plugin/plugin.json", `"name": "acme-kit-ts"`, `"name": "claude-ts"`}}},
		Want: []want{{File: "", Rule: "S10"}}},
	{Name: "S10/marketplace-in-skill", Mut: mutation{Write: w(tsDir+"/.claude-plugin/marketplace.json", "{}\n")},
		Want: []want{{File: ".claude-plugin/marketplace.json", Rule: "S10"}}},
	{Name: "S10/marketplace-anywhere", Mut: mutation{Write: w("tools/.claude-plugin/marketplace.json", "{}\n")},
		Want: []want{{File: "tools/.claude-plugin/marketplace.json", Abs: true, Rule: "S10"}}},
}

func TestRules(t *testing.T) { runCases(t, ruleCases) }

// Rules that read the repository are not run in standalone mode: the same
// violating trees are clean when only the skill directory is checked.
func TestStandaloneSkipsRulesThatNeedTheRepository(t *testing.T) {
	skip := map[string]bool{
		"S4/docs-other-branch": true, "S4/skill-other-branch": true, "S4/docs-path-missing": true,
		"S4/not-a-skill-url": true, "S4/repository-url": true,
		"S5/area-without-capability": true, "S6/stamp-differs-from-manifest": true, "S6/stamp-not-annotated": true,
		"S6/no-stamp": true, "S6/stray-version": true, "S6/version-block-missing": true, "S6/version-block-hand-edited": true,
		"S6/version-block-hand-edited-version": true, "S6/generated-line-copied-outside-block": true, "S6/min-missing": true,
		"S6/min-not-a-release": true, "S6/min-differs-from-ports": true, "S6/min-above-version": true, "S7/ts-module": true, "S7/py-module": true,
		"S7/rust-feature": true, "S7/go-package": true, "S7/php-namespace": true,
	}
	for _, c := range ruleCases {
		if !skip[c.Name] {
			continue
		}
		c := c
		c.Standalone, c.Want = true, nil
		t.Run(c.Name, c.check)
	}
}

// The rules standalone mode does run still find their violations.
func TestStandaloneRunsTheSkillLocalRules(t *testing.T) {
	local := map[string]bool{
		"S1/unknown-key": true, "S1/no-use-when": true, "S2/skill-over-cap": true, "S3/file-not-named": true,
		"S3/broken-link": true, "S4/relative-leaves-skill": true, "S4/absolute-path": true, "S8/tracker-id": true,
		"S8/long-fence": true, "S9/shebang": true, "S9/not-executable": true, "S10/name": true,
		"S10/nested-skills-dir": true, "S10/marketplace-in-skill": true,
	}
	for _, c := range ruleCases {
		if !local[c.Name] {
			continue
		}
		c := c
		c.Standalone = true
		t.Run(c.Name, c.check)
	}
}
