// Package kitinit — publish_workflow.go renders `.github/workflows/publish.yml`:
// the single caller of hop-top/.github's publish-on-tag.yml, which reads a
// `<component>/v<version>` tag and routes it to its registry through the
// `ecosystems` map. It replaces the per-runtime release-<lang>-caller.yml
// stubs (retired in workflows.go), which fired on every tag and left out
// the reusable workflows' required inputs.
//
// The ecosystems keys are the release-please components: read from an
// existing release-please config, else the starter layout
// (planReleasePackages), so the two files agree on names. Go packages are
// left out — their bare v<version> tags are served by proxy.golang.org —
// so a pure-Go repo gets no publish.yml.
//
// Package names and mirror repos follow the hop-top conventions for an
// owner resolved from --org, the Go module path or the origin remote
// (`OWNER` placeholder otherwise); the summary asks for a review.
//
// A repo that already publishes through hop-top/.github from another
// workflow only gets publish.yml as a `.kit-suggested` sibling, so no tag
// publishes twice.
package kitinit

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

const publishWorkflowRel = ".github/workflows/publish.yml"

// ownerPlaceholder stands in for an owner kit cannot resolve.
const ownerPlaceholder = "OWNER"

// releaseTypeEcosystems maps release-please release types to
// publish-on-tag.yml ecosystems that publish to a registry.
var releaseTypeEcosystems = map[string]string{"node": "ts", "python": "py", "rust": "rs", "php": "php"}

// publishReusables are the hop-top/.github workflows that publish a
// release; any other workflow calling one already publishes.
var publishReusables = []string{"publish-on-tag.yml", "publish-ts.yml", "publish-py.yml", "publish-rs.yml"}

type publishEntry struct {
	Component, Dir, Ecosystem string
}

// planPublishWorkflow returns the planned publish.yml, the review note
// for its action, and false when the repo has nothing to publish.
func planPublishWorkflow(target string, in Inputs) (plannedWorkflow, string, bool) {
	entries, total := publishEntries(target, in)
	if len(entries) == 0 {
		return plannedWorkflow{}, "", false
	}
	owner, known := publishOwner(target, in)
	content := renderPublishWorkflow(entries, owner, known, total == 1, projectName(target, in))
	note := "package names and mirror repos follow the " + owner + " conventions; check them, and the " +
		"registry credentials, before the first release tag"
	if !known {
		note = "owner unknown: replace " + ownerPlaceholder + " in package names and mirror repos " +
			"(or rerun with --org / --module) before the first release tag"
	}
	return plannedWorkflow{
		RelPath:     publishWorkflowRel,
		AbsPath:     filepath.Join(target, filepath.FromSlash(publishWorkflowRel)),
		Content:     content,
		ContentHash: sha256Hex([]byte(content)),
	}, note, true
}

// publishEntries lists the registry-publishing packages (sorted by
// component) and the total package count, Go included.
func publishEntries(target string, in Inputs) ([]publishEntry, int) {
	var entries []publishEntry
	total := 0
	if pkgs, ok := configPackages(target); ok {
		total = len(pkgs)
		for _, p := range pkgs {
			if eco, ok := releaseTypeEcosystems[p.ReleaseType]; ok && p.Component != "" {
				entries = append(entries, publishEntry{Component: p.Component, Dir: p.Path, Ecosystem: eco})
			}
		}
	} else {
		pkgs := planReleasePackages(projectName(target, in), in.Runtime)
		total = len(pkgs)
		for _, p := range pkgs {
			if eco, ok := releaseTypeEcosystems[p.ReleaseType]; ok {
				entries = append(entries, publishEntry{Component: p.Component, Dir: p.Path, Ecosystem: eco})
			}
		}
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Component < entries[j].Component })
	return entries, total
}

// configPackages reads the packages of an existing release-please
// config (org path first, then the action's root default).
func configPackages(target string) ([]releasePackage, bool) {
	for _, rel := range []string{releasePleaseDefaultConfig, actionDefaultConfig} {
		data, err := os.ReadFile(filepath.Join(target, filepath.FromSlash(rel)))
		if err != nil {
			continue
		}
		var cfg struct {
			ReleaseType string `json:"release-type"`
			Packages    map[string]struct {
				ReleaseType string `json:"release-type"`
				Component   string `json:"component"`
			} `json:"packages"`
		}
		if json.Unmarshal(data, &cfg) != nil || len(cfg.Packages) == 0 {
			continue
		}
		out := make([]releasePackage, 0, len(cfg.Packages))
		for path, p := range cfg.Packages {
			rt := p.ReleaseType
			if rt == "" {
				rt = cfg.ReleaseType
			}
			out = append(out, releasePackage{Path: path, ReleaseType: rt, Component: p.Component})
		}
		return out, true
	}
	return nil, false
}

func projectName(target string, in Inputs) string {
	if in.Name != "" {
		return in.Name
	}
	return filepath.Base(target)
}

var (
	moduleOwnerRe = regexp.MustCompile(`^github\.com/([^/]+)/`)
	remoteOwnerRe = regexp.MustCompile(`github\.com[:/]([^/]+)/`)
)

// publishOwner resolves the GitHub owner: --org, the Go module path,
// then the origin remote.
func publishOwner(target string, in Inputs) (string, bool) {
	if in.Org != "" {
		return strings.ToLower(in.Org), true
	}
	if m := moduleOwnerRe.FindStringSubmatch(in.Module); m != nil {
		return strings.ToLower(m[1]), true
	}
	if isGitWorkTree(target) {
		if url, err := runGitIn(target, "config", "--get", "remote.origin.url"); err == nil {
			if m := remoteOwnerRe.FindStringSubmatch(url); m != nil {
				return strings.ToLower(m[1]), true
			}
		}
	}
	return ownerPlaceholder, false
}

// renderPublishWorkflow produces publish.yml. single: the repo has one
// package, so ts / py / rs publish without a mirror repo (php always
// needs one: publish-on-tag.yml notifies Packagist from the mirror job).
func renderPublishWorkflow(entries []publishEntry, owner string, ownerKnown, single bool, name string) string {
	mirrored := !single
	needs := map[string]bool{}
	for _, e := range entries {
		needs[e.Ecosystem] = true
		if e.Ecosystem == "php" {
			mirrored = true
		}
	}

	var b strings.Builder
	b.WriteString("# Generated by `kit init`. Edits will not be overwritten;\n")
	b.WriteString("# kit will surface conflicting refreshes as `.kit-suggested` siblings.\n")
	b.WriteString("# Publishes each release-please component tag (<component>/v<version>)\n")
	b.WriteString("# through the hop-top/.github publish-on-tag reusable workflow, which\n")
	b.WriteString("# routes it to its registry by the ecosystems map below. Go is not\n")
	b.WriteString("# listed: its bare v<version> tags are served by proxy.golang.org.\n")
	b.WriteString("# Review package names and mirror repos before the first release tag.\n\n")
	b.WriteString("name: publish\n\non:\n  push:\n    tags: ['*/v*']\n  workflow_dispatch: {}\n\n")
	b.WriteString("jobs:\n  publish:\n    permissions:\n      contents: read\n")
	b.WriteString("      id-token: write  # PyPI trusted publishing, npm provenance\n")
	fmt.Fprintf(&b, "    uses: hop-top/.github/.github/workflows/publish-on-tag.yml@%s\n", workflowCallerRef)
	b.WriteString("    secrets:\n")
	for _, s := range []struct{ eco, name string }{
		{"ts", "NPM_REGISTRY_TOKEN"},
		{"rs", "CARGO_REGISTRY_TOKEN"},
		{"php", "PACKAGIST_USERNAME"},
		{"php", "PACKAGIST_TOKEN"},
	} {
		if needs[s.eco] {
			fmt.Fprintf(&b, "      %s: ${{ secrets.%s }}\n", s.name, s.name)
		}
	}
	b.WriteString("      GH_MIRROR_PAT: ${{ secrets.GH_MIRROR_PAT }}\n")
	b.WriteString("    with:\n")
	if ownerKnown {
		fmt.Fprintf(&b, "      homepage: https://github.com/%s/%s\n", owner, sanitizeComponent(name))
	}
	if !mirrored {
		b.WriteString("      enable-mirror: false\n")
	}
	b.WriteString("      ecosystems: |\n")
	for _, e := range entries {
		base := strings.TrimSuffix(e.Component, "-"+e.Ecosystem)
		fmt.Fprintf(&b, "        %s:\n", yamlScalar(e.Component))
		fmt.Fprintf(&b, "          dir: %s\n", yamlScalar(e.Dir))
		fmt.Fprintf(&b, "          ecosystem: %s\n", e.Ecosystem)
		fmt.Fprintf(&b, "          package: %s\n", yamlScalar(registryPackage(e.Ecosystem, owner, base)))
		if mirrored {
			mirror := e.Component
			if !strings.HasSuffix(mirror, "-"+e.Ecosystem) {
				mirror += "-" + e.Ecosystem
			}
			fmt.Fprintf(&b, "          mirror: %s\n", yamlScalar(owner+"/"+mirror))
		}
	}
	return b.String()
}

// registryPackage: hop-top naming — @<owner>/<name> (npm),
// <owner>-<name> (PyPI, crates.io), <owner>/<name> (Packagist).
func registryPackage(eco, owner, base string) string {
	switch eco {
	case "ts":
		return "@" + owner + "/" + base
	case "php":
		return owner + "/" + base
	default:
		return owner + "-" + base
	}
}

// existingPublishers lists workflows (other than publish.yml and the
// paths in skip) that already call a hop-top/.github publish workflow.
func existingPublishers(target string, skip map[string]bool) ([]string, error) {
	dir := filepath.Join(target, ".github", "workflows")
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("kit init: list %q: %w", dir, err)
	}
	var out []string
	for _, e := range entries {
		name := e.Name()
		rel := ".github/workflows/" + name
		if e.IsDir() || rel == publishWorkflowRel || skip[rel] ||
			(filepath.Ext(name) != ".yml" && filepath.Ext(name) != ".yaml") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			return nil, fmt.Errorf("kit init: read %q: %w", rel, err)
		}
		if callsPublishReusable(data) {
			out = append(out, rel)
		}
	}
	sort.Strings(out)
	return out, nil
}

func callsPublishReusable(data []byte) bool {
	var doc struct {
		Jobs map[string]struct {
			Uses string `yaml:"uses"`
		} `yaml:"jobs"`
	}
	if yaml.Unmarshal(data, &doc) != nil {
		// Unreadable: treat any mention as publishing (never double up).
		for _, r := range publishReusables {
			if strings.Contains(string(data), "hop-top/.github/.github/workflows/"+r+"@") {
				return true
			}
		}
		return false
	}
	for _, j := range doc.Jobs {
		for _, r := range publishReusables {
			if strings.HasPrefix(j.Uses, "hop-top/.github/.github/workflows/"+r+"@") {
				return true
			}
		}
	}
	return false
}
