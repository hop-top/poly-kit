package main

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"unicode/utf8"

	"gopkg.in/yaml.v3"
)

// Frontmatter limits of the authoring contract.
const marketplaceMsg = "no .claude-plugin/marketplace.json anywhere in this repository: the marketplace is shared and lives elsewhere"

const (
	maxNameLen        = 64
	maxDescriptionLen = 1024
	versionAnnotation = "# x-release-please-version"
)

var (
	allowedFrontKeys = []string{"name", "description", "license", "compatibility", "metadata", "allowed-tools"}
	namePattern      = regexp.MustCompile(`^[a-z0-9-]+$`)
	reservedPrefixes = []string{"claude-", "anthropic-", "anthropics-", "cc-plugin-"}
)

type fmKey struct {
	Name string
	Line int // line in SKILL.md
	Val  *yaml.Node
}

// frontInfo is a parsed SKILL.md frontmatter with line numbers.
type frontInfo struct {
	FM   Frontmatter
	Root *yaml.Node
	Keys []fmKey
	Text []string // SKILL.md lines
}

func parseFrontInfo(src []byte) (*frontInfo, error) {
	fm, err := parseFrontmatter(src)
	if err != nil {
		return nil, err
	}
	text, _ := splitFrontmatter(src) // cannot fail after parseFrontmatter
	fi := &frontInfo{FM: fm, Text: strings.Split(string(src), "\n")}
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(text), &doc); err != nil {
		return nil, fmt.Errorf("frontmatter: %w", err)
	}
	if doc.Kind == yaml.DocumentNode && len(doc.Content) == 1 {
		fi.Root = doc.Content[0]
	}
	if fi.Root != nil && fi.Root.Kind == yaml.MappingNode {
		for i := 0; i+1 < len(fi.Root.Content); i += 2 {
			k := fi.Root.Content[i]
			fi.Keys = append(fi.Keys, fmKey{Name: k.Value, Line: k.Line + 1, Val: fi.Root.Content[i+1]})
		}
	}
	return fi, nil
}

func (fi *frontInfo) key(name string) (fmKey, bool) {
	for _, k := range fi.Keys {
		if k.Name == name {
			return k, true
		}
	}
	return fmKey{}, false
}

func (fi *frontInfo) line(name string) int {
	if k, ok := fi.key(name); ok {
		return k.Line
	}
	return 1
}

// metaValue returns metadata.<key>, the line of that key, and whether it
// exists. When it does not, the line is the metadata key's (else 1).
func (fi *frontInfo) metaValue(key string) (string, int, bool) {
	meta, ok := fi.key("metadata")
	if !ok || meta.Val.Kind != yaml.MappingNode {
		return "", 1, false
	}
	for i := 0; i+1 < len(meta.Val.Content); i += 2 {
		if meta.Val.Content[i].Value == key {
			return meta.Val.Content[i+1].Value, meta.Val.Content[i].Line + 1, true
		}
	}
	return "", meta.Line, false
}

// metaVersion returns metadata.version and the line of that key.
func (fi *frontInfo) metaVersion() (string, int, bool) { return fi.metaValue("version") }

// ruleS1 checks the frontmatter: keys, name, description, block style.
func (l *lintSkill) ruleS1() {
	if l.skillMD == nil {
		l.add("SKILL.md", 1, "S1", "SKILL.md is missing")
		return
	}
	if l.frontErr != nil {
		l.add("SKILL.md", 1, "S1", "%v", l.frontErr)
		return
	}
	fi := l.front
	for _, k := range fi.Keys {
		if !contains(allowedFrontKeys, k.Name) {
			l.add("SKILL.md", k.Line, "S1", "frontmatter key %q is not allowed (allowed: %s)", k.Name, strings.Join(allowedFrontKeys, ", "))
		}
	}
	if p := frontmatterProblem(l.skillName(), fi.FM); p != "" {
		key := "license"
		switch {
		case fi.FM.Name != l.skillName():
			key = "name"
		case fi.FM.Description == "":
			key = "description"
		}
		l.add("SKILL.md", fi.line(key), "S1", "%s", p)
	}
	if n := fi.FM.Name; n != "" && (!namePattern.MatchString(n) || len(n) > maxNameLen) {
		l.add("SKILL.md", fi.line("name"), "S1", "name %q must be lowercase letters, digits and hyphens, at most %d characters", n, maxNameLen)
	}
	if d := fi.FM.Description; d != "" {
		if n := utf8.RuneCountInString(d); n > maxDescriptionLen {
			l.add("SKILL.md", fi.line("description"), "S1", "description is %d characters, the cap is %d", n, maxDescriptionLen)
		}
		if !strings.Contains(d, "Use when") {
			l.add("SKILL.md", fi.line("description"), "S1", "description must contain \"Use when\"")
		}
	}
	walkFlow(fi.Root, func(n *yaml.Node) {
		l.add("SKILL.md", n.Line+1, "S1", "frontmatter uses flow-style YAML; write it in block style")
	})
}

// skillName is the name the SKILL.md must carry: ports.yaml's, which the
// directory name equals, or the directory's own name when standalone.
func (l *lintSkill) skillName() string {
	if l.s != nil {
		return l.s.Spec.Skill
	}
	return l.name
}

func walkFlow(n *yaml.Node, report func(*yaml.Node)) {
	if n == nil {
		return
	}
	if (n.Kind == yaml.MappingNode || n.Kind == yaml.SequenceNode) && n.Style&yaml.FlowStyle != 0 {
		report(n)
	}
	for _, c := range n.Content {
		walkFlow(c, report)
	}
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// ruleS6 checks the version stamp against the release manifest, the minimum
// release against ports.yaml, the generated version block, and that every
// other three-part version sits on an annotated line. Lines judged here are
// not scanned again: the stamp, the minimum, and the block when it equals
// what the generator writes (so only those exact generated lines may carry a
// version outside an annotated line).
func (l *lintSkill) ruleS6() {
	if l.front == nil {
		return // S1 reports the frontmatter; there is nothing to judge a version against
	}
	judged := map[int]bool{} // SKILL.md lines already judged
	ver, line, ok := l.front.metaVersion()
	if ok {
		judged[line] = true
	}
	switch {
	case !ok:
		l.add("SKILL.md", line, "S6", "metadata.version is missing")
	case !strings.Contains(l.front.Text[line-1], versionAnnotation):
		l.add("SKILL.md", line, "S6", "metadata.version line lacks the %q annotation", versionAnnotation)
	default:
		want, has := l.g.manifest[l.s.Comp.Dir]
		switch {
		case !has:
			l.add("SKILL.md", line, "S6", "the release manifest has no entry for %q", l.s.Comp.Dir)
		case ver != want:
			l.add("SKILL.md", line, "S6", "metadata.version %q differs from the release manifest's %q for %q", ver, want, l.s.Comp.Dir)
		}
	}
	if _, hasMeta := l.front.key("metadata"); hasMeta {
		l.checkMinVersion(ver, ok, judged)
	}
	l.checkVersionBlock(ver, judged)
	for _, f := range l.textFiles() {
		for i, text := range strings.Split(string(f.Data), "\n") {
			if strings.Contains(text, versionAnnotation) || f.Rel == "SKILL.md" && judged[i+1] {
				continue
			}
			for _, v := range threePartVersions(text) {
				l.add(f.Rel, i+1, "S6", "version %q on a line without the %q annotation; say \"the current release\" instead", v, versionAnnotation)
			}
		}
	}
}

// checkMinVersion judges metadata.min_version: present, a release, the one
// ports.yaml gives this skill, and not newer than metadata.version.
func (l *lintSkill) checkMinVersion(ver string, haveVer bool, judged map[int]bool) {
	min, line, ok := l.front.metaValue("min_version")
	if !ok {
		l.add("SKILL.md", line, "S6", "metadata.min_version is missing (make skills-sync writes it from ports.yaml)")
		return
	}
	judged[line] = true
	switch {
	case !validRelease(min):
		l.add("SKILL.md", line, "S6", "metadata.min_version %q is not a release (x.y.z or x.y.z-prerelease)", min)
		return
	case min != l.s.Spec.MinVersion:
		l.add("SKILL.md", line, "S6", "metadata.min_version %q differs from ports.yaml's %q; run: make skills-sync", min, l.s.Spec.MinVersion)
	}
	if haveVer && validRelease(ver) && compareRelease(min, ver) > 0 {
		l.add("SKILL.md", line, "S6", "metadata.min_version %q is newer than metadata.version %q", min, ver)
	}
}

// checkVersionBlock requires the `version` managed block to exist and to be
// exactly what the generator writes for this skill.
func (l *lintSkill) checkVersionBlock(ver string, judged map[int]bool) {
	lines := l.front.Text
	b, e := -1, -1
	for i, t := range lines {
		switch strings.TrimRight(t, "\r") {
		case beginMarker("version"):
			if b < 0 {
				b = i
			}
		case endMarker("version"):
			if e < 0 {
				e = i
			}
		}
	}
	if b < 0 || e < b {
		l.add("SKILL.md", 1, "S6", "the %s ... %s block is missing or misordered (make skills-sync writes it)", beginMarker("version"), endMarker("version"))
		return
	}
	for i := b + 1; i < e; i++ {
		judged[i+1] = true
	}
	if ver == "" {
		return
	}
	s := *l.s
	s.FM.Metadata.Version = ver
	want, err := l.g.versionBlock(&s)
	if err != nil {
		l.add("SKILL.md", b+1, "S6", "%v", err)
		return
	}
	have := strings.Join(lines[b+1:e], "\n")
	if strings.TrimRight(have, "\n") != strings.TrimRight(want, "\n") {
		l.add("SKILL.md", b+1, "S6", "the version block differs from what the generator writes; do not edit it by hand, run: make skills-sync")
	}
}

var versionRe = regexp.MustCompile(`\d+\.\d+\.\d+`)

// threePartVersions finds x.y.z numbers that are not part of a longer dotted
// number or an identifier.
func threePartVersions(line string) []string {
	var out []string
	for _, m := range versionRe.FindAllStringIndex(line, -1) {
		b, e := m[0], m[1]
		if e < len(line) && (line[e] == '.' && e+1 < len(line) && isDigit(line[e+1]) || isAlnum(line[e])) {
			continue
		}
		if b > 0 {
			p := line[b-1]
			if p == '.' || isAlnum(p) || p == '_' {
				if p != 'v' || (b >= 2 && isAlnum(line[b-2])) {
					continue
				}
			}
		}
		out = append(out, line[b:e])
	}
	return out
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }
func isAlnum(c byte) bool {
	return isDigit(c) || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
}

// pluginKeys are the keys the generator writes into plugin.json, taken from
// the struct it marshals so the two cannot disagree.
func pluginKeys() []string {
	var keys []string
	t := reflect.TypeOf(pluginJSON{})
	for i := 0; i < t.NumField(); i++ {
		keys = append(keys, strings.Split(t.Field(i).Tag.Get("json"), ",")[0])
	}
	return keys
}

// jsonKeyLine is the line of the first `"key":` in a JSON document.
func jsonKeyLine(data []byte, key string) int {
	re := regexp.MustCompile(`^\s*"` + regexp.QuoteMeta(key) + `"\s*:`)
	for i, l := range strings.Split(string(data), "\n") {
		if re.MatchString(l) {
			return i + 1
		}
	}
	return 1
}

// ruleS10 checks the plugin manifest and the plugin layout rules that need
// nothing outside the skill directory.
func (l *lintSkill) ruleS10() {
	for _, pre := range reservedPrefixes {
		if strings.HasPrefix(l.name, pre) {
			l.add("", 1, "S10", "name %q starts with %q, which Claude Code reserves", l.name, pre)
		}
	}
	for rel := range l.dirs {
		if b := baseOf(rel); b == "skills" || b == "bin" {
			l.add(rel, 1, "S10", "a skill is a plugin root: no nested %s/ directory", b)
		}
	}
	for _, f := range l.files {
		switch baseOf(f.Rel) {
		case "CLAUDE.md":
			l.add(f.Rel, 1, "S10", "a skill carries no CLAUDE.md")
		case "marketplace.json":
			if strings.HasSuffix(f.Rel, ".claude-plugin/marketplace.json") {
				l.add(f.Rel, 1, "S10", marketplaceMsg)
			}
		}
	}
	const rel = ".claude-plugin/plugin.json"
	f := l.byRel[rel]
	if f == nil {
		l.add(rel, 1, "S10", "plugin manifest is missing (make skills-sync writes it)")
		return
	}
	var m map[string]any
	if err := json.Unmarshal(f.Data, &m); err != nil {
		l.add(rel, 1, "S10", "plugin manifest is not valid JSON: %v", err)
		return
	}
	allowed := pluginKeys()
	for k := range m {
		if !contains(allowed, k) {
			l.add(rel, jsonKeyLine(f.Data, k), "S10", "plugin manifest key %q is not one the generator writes (%s)", k, strings.Join(allowed, ", "))
		}
	}
	if n, _ := m["name"].(string); n != l.name {
		l.add(rel, jsonKeyLine(f.Data, "name"), "S10", "plugin name %q must equal the directory and SKILL.md name %q", n, l.name)
	}
	if l.front != nil {
		if want, _, ok := l.front.metaVersion(); ok {
			if v, _ := m["version"].(string); v != want {
				l.add(rel, jsonKeyLine(f.Data, "version"), "S10", "plugin version %q must equal metadata.version %q", v, want)
			}
		}
	}
}

func baseOf(rel string) string {
	if i := strings.LastIndex(rel, "/"); i >= 0 {
		return rel[i+1:]
	}
	return rel
}

// ruleS10Marketplace fails for every .claude-plugin/marketplace.json in the
// repository: tracked files when the root is a git checkout, else any file
// found on disk.
func ruleS10Marketplace(g *gen, out *[]Finding) error {
	files, err := repoFiles(g.o.Root)
	if err != nil {
		return err
	}
	for _, f := range files {
		if f == ".claude-plugin/marketplace.json" || strings.HasSuffix(f, "/.claude-plugin/marketplace.json") {
			*out = append(*out, Finding{Path: f, Line: 1, Rule: "S10", Msg: marketplaceMsg})
		}
	}
	return nil
}

// repoFiles lists the repository's files, slash-separated and relative to root.
func repoFiles(root string) ([]string, error) {
	if _, err := os.Stat(filepath.Join(root, ".git")); err == nil {
		cmd := exec.Command("git", "-C", root, "ls-files", "-z")
		if b, err := cmd.Output(); err == nil {
			var files []string
			for _, f := range strings.Split(string(b), "\x00") {
				if f != "" {
					files = append(files, f)
				}
			}
			return files, nil
		}
	}
	var files []string
	skip := map[string]bool{".git": true, "node_modules": true, ".venv": true, "target": true, "vendor": true}
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if skip[d.Name()] && p != root {
				return filepath.SkipDir
			}
			return nil
		}
		rel, _ := filepath.Rel(root, p)
		files = append(files, filepath.ToSlash(rel))
		return nil
	})
	return files, err
}
