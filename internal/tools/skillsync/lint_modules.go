package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"
)

// treeCache holds what the module-name rule reads from the SDK trees, loaded
// on first use.
type treeCache struct {
	g        *gen
	tsExport []string
	tsLoaded bool
	features map[string]bool
	featLoad bool
}

// ecoDir returns the directory of the component that publishes the first
// port of an ecosystem.
func (g *gen) ecoDir(eco string) (string, bool) {
	for _, p := range g.ports.Ports {
		if c := g.pub[p.Component]; c.Ecosystem == eco {
			return path.Clean(filepath.ToSlash(c.Dir)), true
		}
	}
	return "", false
}

func (c *treeCache) tsExports() ([]string, error) {
	if c.tsLoaded {
		return c.tsExport, nil
	}
	dir, _ := c.g.ecoDir("ts")
	b, err := os.ReadFile(c.g.abs(path.Join(dir, "package.json")))
	if err != nil {
		return nil, err
	}
	var pkg struct {
		Exports map[string]json.RawMessage `json:"exports"`
	}
	if err := json.Unmarshal(b, &pkg); err != nil {
		return nil, fmt.Errorf("%s: %w", path.Join(dir, "package.json"), err)
	}
	for k := range pkg.Exports {
		c.tsExport = append(c.tsExport, k)
	}
	sort.Strings(c.tsExport)
	c.tsLoaded = true
	return c.tsExport, nil
}

func (c *treeCache) cargoFeatures(dir string) (map[string]bool, error) {
	if c.featLoad {
		return c.features, nil
	}
	file := path.Join(dir, "Cargo.toml")
	var cargo struct {
		Features map[string]any `toml:"features"`
	}
	if _, err := toml.DecodeFile(c.g.abs(file), &cargo); err != nil {
		return nil, fmt.Errorf("%s: %w", file, err)
	}
	c.features = map[string]bool{}
	for k := range cargo.Features {
		c.features[k] = true
	}
	c.featLoad = true
	return c.features, nil
}

// exportMatches reports whether the exports key covers the subpath.
func exportMatches(key, sub string) bool {
	if i := strings.Index(key, "*"); i >= 0 {
		return strings.HasPrefix(sub, key[:i]) && strings.HasSuffix(sub, key[i+1:])
	}
	return key == sub
}

var (
	tsModRe   = regexp.MustCompile(`@hop-top/kit/([A-Za-z0-9_./-]+)`)
	pyModRe   = regexp.MustCompile(`\bhop_top_kit\.([A-Za-z_][A-Za-z0-9_]*)`)
	goPkgRe   = regexp.MustCompile(`\bhop\.top/kit/go/([A-Za-z0-9_/-]+)`)
	phpNsRe   = regexp.MustCompile(`HopTop\\{1,2}Kit\\{1,2}([A-Za-z0-9_]+(?:\\{1,2}[A-Za-z0-9_]+)*)`)
	featArrRe = regexp.MustCompile(`\bfeatures\s*=\s*\[([^\]]*)\]`)
	quotedRe  = regexp.MustCompile(`"([^"]+)"`)
	featFlagR = regexp.MustCompile(`--features[= ]\s*"?([A-Za-z0-9_,-]+)`)
)

// ruleS7 checks that every module a skill names exists in its port's source.
func (l *lintSkill) ruleS7() error {
	g := l.g
	tsDir, hasTS := g.ecoDir("ts")
	pyDir, hasPy := g.ecoDir("py")
	phpDir, hasPHP := g.ecoDir("php")
	goDir, hasGo := g.ecoDir("go")
	for _, f := range l.textFiles() {
		for i, text := range strings.Split(string(f.Data), "\n") {
			at := func(format string, a ...any) { l.add(f.Rel, i+1, "S7", format, a...) }
			if hasTS {
				for _, m := range tsModRe.FindAllStringSubmatch(text, -1) {
					sub := strings.TrimRight(m[1], "./-")
					exports, err := l.cache.tsExports()
					if err != nil {
						return err
					}
					if !anyExport(exports, "./"+sub) {
						at("@hop-top/kit/%s is not a key of exports in %s", sub, path.Join(tsDir, "package.json"))
					}
				}
			}
			if hasPy {
				for _, m := range pyModRe.FindAllStringSubmatch(text, -1) {
					base := g.abs(path.Join(pyDir, "hop_top_kit", m[1]))
					if !isFile(base+".py") && !isDir(base) {
						at("hop_top_kit.%s does not resolve under %s/hop_top_kit/", m[1], pyDir)
					}
				}
			}
			if hasGo {
				for _, m := range goPkgRe.FindAllStringSubmatch(text, -1) {
					pkg := strings.TrimRight(m[1], "/-")
					if !isDir(g.abs(path.Join(goDir, "go", pkg))) {
						at("hop.top/kit/go/%s is not a directory", pkg)
					}
				}
			}
			if hasPHP {
				for _, m := range phpNsRe.FindAllStringSubmatch(text, -1) {
					if bad := phpMissing(g.abs(path.Join(phpDir, "src")), m[1]); bad != "" {
						at("HopTop\\Kit\\%s: %s has no directory or file under %s/src/", strings.ReplaceAll(m[1], `\\`, `\`), bad, phpDir)
					}
				}
			}
			if l.s.Comp.Ecosystem == "rs" {
				names := rustFeatures(text)
				if len(names) == 0 {
					continue
				}
				have, err := l.cache.cargoFeatures(l.s.Comp.Dir)
				if err != nil {
					return err
				}
				for _, n := range names {
					if !have[n] {
						at("Cargo feature %q is not in [features] of %s", n, path.Join(path.Clean(filepath.ToSlash(l.s.Comp.Dir)), "Cargo.toml"))
					}
				}
			}
		}
	}
	return nil
}

func anyExport(keys []string, sub string) bool {
	for _, k := range keys {
		if exportMatches(k, sub) {
			return true
		}
	}
	return false
}

func isFile(p string) bool { st, err := os.Stat(p); return err == nil && !st.IsDir() }
func isDir(p string) bool  { st, err := os.Stat(p); return err == nil && st.IsDir() }

// phpMissing resolves a namespace under src the way PSR-4 maps it and
// returns the first segment that has neither a directory nor a file.
func phpMissing(src, ns string) string {
	cur := src
	for _, seg := range strings.Split(strings.ReplaceAll(ns, `\\`, `\`), `\`) {
		switch {
		case isDir(filepath.Join(cur, seg)):
			cur = filepath.Join(cur, seg)
		case isFile(filepath.Join(cur, seg+".php")):
			return ""
		default:
			return seg
		}
	}
	return ""
}

// rustFeatures lists plain feature names a line passes to cargo: a
// features = [...] array or a --features flag.
func rustFeatures(line string) []string {
	var out []string
	for _, m := range featArrRe.FindAllStringSubmatch(line, -1) {
		for _, q := range quotedRe.FindAllStringSubmatch(m[1], -1) {
			if !strings.ContainsAny(q[1], "/:") { // crate/feature and dep:crate are not ours
				out = append(out, q[1])
			}
		}
	}
	for _, m := range featFlagR.FindAllStringSubmatch(line, -1) {
		for _, n := range strings.Split(m[1], ",") {
			if n != "" {
				out = append(out, n)
			}
		}
	}
	return out
}
