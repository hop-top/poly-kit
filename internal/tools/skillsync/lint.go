package main

import (
	"bytes"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

// Finding is one lint result: a rule id, a file and a line.
type Finding struct {
	Path string // slash-separated
	Line int
	Rule string
	Msg  string
}

func (f Finding) String() string {
	return fmt.Sprintf("%s:%d: %s: %s", f.Path, f.Line, f.Rule, f.Msg)
}

// lintFile is one regular file of a skill directory.
type lintFile struct {
	Rel  string // slash-separated, relative to the skill directory
	Mode fs.FileMode
	Data []byte
	Text bool // no NUL byte: safe to scan line by line
}

func (f *lintFile) isMarkdown() bool { return strings.HasSuffix(f.Rel, ".md") }

// generatedChannelFile reports whether rel is written by the generator for
// distribution channels rather than authored.
func generatedChannelFile(rel string) bool {
	return rel == "gym.json" || strings.HasPrefix(rel, ".claude-plugin/")
}

// lintSkill is one skill directory under lint, with everything the rules
// read: its files, its parsed frontmatter and, outside standalone mode, the
// generator's view of it.
type lintSkill struct {
	g    *gen   // nil in standalone mode
	s    *skill // nil in standalone mode
	disp string // directory as printed in findings
	name string // directory name

	files    []*lintFile
	byRel    map[string]*lintFile
	dirs     map[string]bool // relative directories, "" excluded
	front    *frontInfo      // nil when SKILL.md is missing or unreadable
	frontErr error
	skillMD  *lintFile
	out      *[]Finding
	cache    *treeCache
}

func (l *lintSkill) add(rel string, line int, rule, format string, a ...any) {
	if line < 1 {
		line = 1
	}
	p := l.disp
	if rel != "" {
		p = path.Join(l.disp, rel)
	}
	*l.out = append(*l.out, Finding{Path: p, Line: line, Rule: rule, Msg: fmt.Sprintf(format, a...)})
}

func (l *lintSkill) standalone() bool { return l.g == nil }

func loadLintSkill(abs, disp string, g *gen, s *skill, out *[]Finding, cache *treeCache) (*lintSkill, error) {
	l := &lintSkill{g: g, s: s, disp: disp, name: filepath.Base(abs), out: out, cache: cache,
		byRel: map[string]*lintFile{}, dirs: map[string]bool{}}
	err := filepath.WalkDir(abs, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(abs, p)
		rel = filepath.ToSlash(rel)
		if rel == "." {
			return nil
		}
		if d.IsDir() {
			l.dirs[rel] = true
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		f := &lintFile{Rel: rel, Mode: info.Mode()}
		if info.Mode()&fs.ModeSymlink == 0 {
			if f.Data, err = os.ReadFile(p); err != nil {
				return err
			}
			f.Text = bytes.IndexByte(f.Data, 0) < 0
		}
		l.files = append(l.files, f)
		l.byRel[rel] = f
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(l.files, func(i, j int) bool { return l.files[i].Rel < l.files[j].Rel })
	if f := l.byRel["SKILL.md"]; f != nil {
		l.skillMD = f
		l.front, l.frontErr = parseFrontInfo(f.Data)
	}
	return l, nil
}

// under lists the files below prefix ("references/").
func (l *lintSkill) under(prefix string) []*lintFile {
	var out []*lintFile
	for _, f := range l.files {
		if strings.HasPrefix(f.Rel, prefix) {
			out = append(out, f)
		}
	}
	return out
}

// textFiles are the files whose content rules scan: everything but the
// generated channel files.
func (l *lintSkill) textFiles() []*lintFile {
	var out []*lintFile
	for _, f := range l.files {
		if f.Text && f.Data != nil && !generatedChannelFile(f.Rel) {
			out = append(out, f)
		}
	}
	return out
}

// lintRules runs every rule on one skill. Standalone mode runs only the
// rules that need nothing outside the skill directory.
func (l *lintSkill) lintRules() error {
	l.ruleS1()
	l.ruleS2()
	l.ruleS3()
	l.ruleS4Relative()
	l.ruleS8()
	l.ruleS9()
	l.ruleS10()
	if l.standalone() {
		return nil
	}
	l.ruleS4URLs()
	l.ruleS5()
	l.ruleS6()
	return l.ruleS7()
}

// LintOptions selects what Lint checks.
type LintOptions struct {
	Standalone bool
	Dirs       []string // skill directories; empty means every one ports.yaml names
}

// Lint checks skill directories and returns findings sorted by path, line,
// rule and message. A non-nil error is an input problem, not a finding.
func Lint(o Options, lo LintOptions) ([]Finding, error) {
	var out []Finding
	cache := &treeCache{}
	if lo.Standalone && len(lo.Dirs) > 0 {
		for _, d := range lo.Dirs {
			abs := d
			if !filepath.IsAbs(abs) {
				abs = filepath.Join(o.Root, filepath.FromSlash(d))
			}
			if err := lintOne(abs, filepath.ToSlash(filepath.Clean(d)), nil, nil, &out, cache); err != nil {
				return nil, err
			}
		}
		return finish(out), nil
	}
	g, err := loadInputs(o, !lo.Standalone)
	if err != nil {
		return nil, &inputError{err}
	}
	cache.g = g
	targets := g.skills
	if len(lo.Dirs) > 0 {
		targets = nil
		for _, d := range lo.Dirs {
			s, err := g.skillAt(d)
			if err != nil {
				return nil, err
			}
			targets = append(targets, s)
		}
	}
	for _, s := range targets {
		abs := g.abs(s.Dir)
		if st, err := os.Stat(abs); err != nil || !st.IsDir() {
			if len(lo.Dirs) > 0 {
				return nil, fmt.Errorf("%s: not a directory", s.Dir)
			}
			continue // a skill that has not been written yet
		}
		gg, ss := g, s
		if lo.Standalone {
			gg, ss = nil, nil
		}
		if err := lintOne(abs, s.Dir, gg, ss, &out, cache); err != nil {
			return nil, err
		}
	}
	if !lo.Standalone {
		if err := ruleS10Marketplace(g, &out); err != nil {
			return nil, err
		}
	}
	return finish(out), nil
}

// inputError marks an error caused by inconsistent inputs, as opposed to a
// usage or I/O failure.
type inputError struct{ error }

func lintOne(abs, disp string, g *gen, s *skill, out *[]Finding, cache *treeCache) error {
	st, err := os.Stat(abs)
	if err != nil {
		return err
	}
	if !st.IsDir() {
		return fmt.Errorf("%s: not a directory", disp)
	}
	l, err := loadLintSkill(abs, disp, g, s, out, cache)
	if err != nil {
		return err
	}
	return l.lintRules()
}

// skillAt maps a directory argument to the skill ports.yaml names there.
func (g *gen) skillAt(d string) (*skill, error) {
	rel := d
	if filepath.IsAbs(d) {
		r, err := filepath.Rel(g.o.Root, d)
		if err != nil {
			return nil, err
		}
		rel = r
	}
	rel = path.Clean(filepath.ToSlash(rel))
	for _, s := range g.skills {
		if s.Dir == rel {
			return s, nil
		}
	}
	return nil, fmt.Errorf("%s is not a skill directory ports.yaml names (use --standalone to check any directory)", d)
}

// finish sorts findings and drops exact duplicates.
func finish(in []Finding) []Finding {
	sort.SliceStable(in, func(i, j int) bool {
		a, b := in[i], in[j]
		switch {
		case a.Path != b.Path:
			return a.Path < b.Path
		case a.Line != b.Line:
			return a.Line < b.Line
		case a.Rule != b.Rule:
			return a.Rule < b.Rule
		}
		return a.Msg < b.Msg
	})
	out := in[:0]
	for i, f := range in {
		if i > 0 && f == in[i-1] {
			continue
		}
		out = append(out, f)
	}
	return out
}
