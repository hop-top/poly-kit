// Package kitinit — releaseplease_seed.go renders the starter
// release-please config + manifest kit init writes when a repo has
// neither (`.github/release-please-config.json`,
// `.github/.release-please-manifest.json`, the reusable workflow's
// defaults).
//
// Conventions (the hop-top release-please adopters and poly-kit's own
// config; checked by hop-top/.github's release-please-preflight.yml):
//   - tags `<component>/v<version>`: include-component-in-tag + "/";
//   - release PR title "chore(release): ${component} ${version}" with
//     component-no-space;
//   - labels status:release-pending / status:release-tagged;
//   - one release PR per package; poly-kit's changelog sections;
//   - prerelease channel per package: prerelease, prerelease-type
//     "alpha.0" (the .0 makes the counter start at 0),
//     versioning "prerelease", bump-minor-pre-major,
//     initial-version 0.1.0-alpha.0;
//   - components: the project name for the root / Go package,
//     `<name>-<runtime>` for ports in `<runtime>/` directories, which
//     the root Go package excludes.
//
// Manifest seeding (release-please 17.x source): with no manifest key a
// package's first version comes from its strategy's
// initialReleaseVersion(). The base strategy (go, node, php) honors
// `initial-version`, so `{}` yields 0.1.0-alpha.0. python and rust
// override it with a fixed stable 0.1.0, so a repo with either gets
// every package seeded at 0.1.0-alpha.0 (a seeded key is read as
// already released: first release 0.1.0-alpha.1, consistent across
// packages).
//
// The seeds belong to the repo from the moment they are written:
// release-please rewrites the manifest on every release, so kit does
// not track either file in `.kit/generated.json` and never touches
// them again.
package kitinit

import (
	"bytes"
	"encoding/json"
	"sort"
	"strings"
)

const (
	releasePleaseSchema         = "https://raw.githubusercontent.com/googleapis/release-please/main/schemas/config.json"
	releasePleaseInitialVersion = "0.1.0-alpha.0"
	releasePleaseLabelPending   = "status:release-pending"
	releasePleaseLabelTagged    = "status:release-tagged"
)

// releaseTypes maps kit runtimes to release-please release types, in
// the package order the config lists them.
var releaseTypes = []struct{ runtime, releaseType string }{
	{"go", "go"},
	{"ts", "node"},
	{"py", "python"},
	{"rs", "rust"},
	{"php", "php"},
}

// releaseTypesNeedingSeed ignore `initial-version` (fixed stable 0.1.0).
var releaseTypesNeedingSeed = map[string]bool{"python": true, "rust": true}

type rpSeedSection struct {
	Type    string `json:"type"`
	Section string `json:"section"`
	Hidden  bool   `json:"hidden,omitempty"`
}

type rpSeedPackage struct {
	ReleaseType       string   `json:"release-type"`
	Component         string   `json:"component"`
	ChangelogPath     string   `json:"changelog-path"`
	BumpMinorPreMajor bool     `json:"bump-minor-pre-major"`
	ExcludePaths      []string `json:"exclude-paths,omitempty"`
	Prerelease        bool     `json:"prerelease"`
	PrereleaseType    string   `json:"prerelease-type"`
	Versioning        string   `json:"versioning"`
	InitialVersion    string   `json:"initial-version"`
}

type rpSeedConfig struct {
	Schema                string                   `json:"$schema"`
	SeparatePullRequests  bool                     `json:"separate-pull-requests"`
	PullRequestTitle      string                   `json:"pull-request-title-pattern"`
	ComponentNoSpace      bool                     `json:"component-no-space"`
	IncludeComponentInTag bool                     `json:"include-component-in-tag"`
	TagSeparator          string                   `json:"tag-separator"`
	Label                 string                   `json:"label"`
	ReleaseLabel          string                   `json:"release-label"`
	ChangelogSections     []rpSeedSection          `json:"changelog-sections"`
	Packages              map[string]rpSeedPackage `json:"packages"`
}

// renderReleasePleaseSeed returns the starter config and manifest for a
// project named name shipping runtimes (unknown runtimes ignored; none
// known = go).
func renderReleasePleaseSeed(name string, runtimes []string) (config, manifest []byte) {
	want := map[string]bool{}
	for _, r := range runtimes {
		want[strings.ToLower(r)] = true
	}
	var selected []struct{ runtime, releaseType string }
	for _, rt := range releaseTypes {
		if want[rt.runtime] {
			selected = append(selected, rt)
		}
	}
	if len(selected) == 0 {
		selected = releaseTypes[:1]
	}

	component := sanitizeComponent(name)
	packages := map[string]rpSeedPackage{}
	seed := false
	var ports []string
	for _, rt := range selected {
		path, comp := ".", component
		if len(selected) > 1 && rt.runtime != "go" {
			path, comp = rt.runtime, component+"-"+rt.runtime
			ports = append(ports, path)
		}
		packages[path] = rpSeedPackage{
			ReleaseType:       rt.releaseType,
			Component:         comp,
			ChangelogPath:     "CHANGELOG.md",
			BumpMinorPreMajor: true,
			Prerelease:        true,
			PrereleaseType:    "alpha.0",
			Versioning:        "prerelease",
			InitialVersion:    releasePleaseInitialVersion,
		}
		seed = seed || releaseTypesNeedingSeed[rt.releaseType]
	}
	if root, ok := packages["."]; ok && len(ports) > 0 {
		sort.Strings(ports)
		root.ExcludePaths = ports
		packages["."] = root
	}

	cfg := rpSeedConfig{
		Schema:                releasePleaseSchema,
		SeparatePullRequests:  true,
		PullRequestTitle:      "chore(release): ${component} ${version}",
		ComponentNoSpace:      true,
		IncludeComponentInTag: true,
		TagSeparator:          "/",
		Label:                 releasePleaseLabelPending,
		ReleaseLabel:          releasePleaseLabelTagged,
		ChangelogSections: []rpSeedSection{
			{Type: "feat", Section: "Features"},
			{Type: "fix", Section: "Bug Fixes"},
			{Type: "perf", Section: "Performance"},
			{Type: "refactor", Section: "Refactored", Hidden: true},
			{Type: "chore", Section: "Miscellaneous", Hidden: true},
			{Type: "docs", Section: "Documentation", Hidden: true},
			{Type: "test", Section: "Tests", Hidden: true},
			{Type: "ci", Section: "CI", Hidden: true},
			{Type: "build", Section: "Build", Hidden: true},
		},
		Packages: packages,
	}
	man := map[string]string{}
	if seed {
		for path := range packages {
			man[path] = releasePleaseInitialVersion
		}
	}
	return marshalSeed(cfg), marshalSeed(man)
}

// marshalSeed: 2-space indented JSON, `$`/`<`/`>` kept literal,
// trailing newline. Map keys sort, so output is deterministic.
func marshalSeed(v any) []byte {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		panic(err) // static types only; cannot fail
	}
	return buf.Bytes()
}

// sanitizeComponent turns a project name into a single-segment
// component (release-please-preflight rejects `/`; the `*/v*` tag glob
// does not match nested prefixes).
func sanitizeComponent(name string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(name) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-', r == '_', r == '.':
			b.WriteRune(r)
		default:
			b.WriteRune('-')
		}
	}
	out := strings.Trim(b.String(), "-.")
	if out == "" {
		return "app"
	}
	return out
}
