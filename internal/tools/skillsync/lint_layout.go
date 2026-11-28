package main

import (
	"io/fs"
	"path"
	"regexp"
	"strings"
)

// Caps of the authoring contract.
const (
	maxSkillLines     = 120
	maxReferenceLines = 200
	maxFenceLines     = 15
)

func lineCount(data []byte) int {
	if len(data) == 0 {
		return 0
	}
	n := strings.Count(string(data), "\n")
	if data[len(data)-1] != '\n' {
		n++
	}
	return n
}

// ruleS2 checks the caps and that only the contract's layout exists.
func (l *lintSkill) ruleS2() {
	if f := l.skillMD; f != nil {
		if n := lineCount(f.Data); n > maxSkillLines {
			l.add(f.Rel, maxSkillLines+1, "S2", "SKILL.md has %d lines, the cap is %d", n, maxSkillLines)
		}
	}
	for _, f := range l.under("references/") {
		if n := lineCount(f.Data); n > maxReferenceLines {
			l.add(f.Rel, maxReferenceLines+1, "S2", "reference has %d lines, the cap is %d; split it by topic", n, maxReferenceLines)
		}
	}
	// S10 owns skills/, bin/ and CLAUDE.md; the manifest dir is checked by S10.
	topDirs := map[string]bool{"references": true, "scripts": true, "assets": true, ".claude-plugin": true, "skills": true, "bin": true}
	for rel := range l.dirs {
		if !strings.Contains(rel, "/") && !topDirs[rel] {
			l.add(rel, 1, "S2", "directory %q is not allowed (allowed: references/, scripts/, assets/snippets/, .claude-plugin/)", rel)
		}
	}
	for _, f := range l.files {
		parts := strings.Split(f.Rel, "/")
		top := parts[0]
		if f.Mode&fs.ModeSymlink != 0 {
			l.add(f.Rel, 1, "S2", "symlinks are not allowed: an installer copies one directory")
			continue
		}
		switch {
		case len(parts) == 1:
			if f.Rel != "SKILL.md" && f.Rel != "gym.json" && f.Rel != "CLAUDE.md" {
				l.add(f.Rel, 1, "S2", "top-level file %q is not allowed (only SKILL.md and generated gym.json)", f.Rel)
			}
		case !topDirs[top] || top == "skills" || top == "bin":
			// reported once as a directory, or owned by S10
		case top == "references" || top == "scripts":
			if len(parts) > 2 {
				l.add(f.Rel, 1, "S2", "%s/ is flat: no subdirectories", top)
			}
		case top == "assets":
			switch {
			case parts[1] != "snippets":
				l.add(f.Rel, 1, "S2", "only assets/snippets/ is allowed under assets/")
			case len(parts) == 3:
				l.add(f.Rel, 1, "S2", "a snippet is a directory: assets/snippets/<name>/<file>")
			case len(parts) > 4:
				l.add(f.Rel, 1, "S2", "nothing deeper than assets/snippets/<name>/")
			}
		case top == ".claude-plugin":
			if f.Rel != ".claude-plugin/plugin.json" && baseOf(f.Rel) != "marketplace.json" {
				l.add(f.Rel, 1, "S2", ".claude-plugin/ holds only plugin.json")
			}
		}
	}
}

// mentions reports whether text names p as a whole path.
func mentions(text, p string) bool {
	for from := 0; ; {
		i := strings.Index(text[from:], p)
		if i < 0 {
			return false
		}
		b, e := from+i, from+i+len(p)
		okBefore := b == 0 || !isNameChar(text[b-1])
		okAfter := e == len(text) || !isNameChar(text[e])
		if okBefore && okAfter {
			return true
		}
		from = b + 1
	}
}

func isNameChar(c byte) bool { return isAlnum(c) || c == '_' || c == '-' }

// ruleS3 checks that SKILL.md names every deeper file and that every
// relative link target exists.
func (l *lintSkill) ruleS3() {
	if l.skillMD != nil {
		text := string(l.skillMD.Data)
		for _, prefix := range []string{"references/", "scripts/", "assets/snippets/"} {
			for _, f := range l.under(prefix) {
				if f.Mode&fs.ModeSymlink != 0 {
					continue
				}
				named := mentions(text, f.Rel)
				if prefix == "assets/snippets/" {
					if parts := strings.Split(f.Rel, "/"); len(parts) > 3 {
						named = named || mentions(text, strings.Join(parts[:3], "/"))
					}
				}
				if !named {
					l.add(f.Rel, 1, "S3", "not named in SKILL.md: add it to the Open when table with the cue that makes an agent open it")
				}
			}
		}
	}
	for _, lk := range l.relativeLinks() {
		if !lk.Outside && !l.exists(lk.Resolved) {
			l.add(lk.File, lk.Line, "S3", "link target %q does not exist", lk.Target)
		}
	}
}

func (l *lintSkill) exists(rel string) bool {
	return l.byRel[rel] != nil || l.dirs[rel] || rel == "."
}

// ruleS4Relative checks that no relative link leaves the skill directory.
func (l *lintSkill) ruleS4Relative() {
	for _, lk := range l.relativeLinks() {
		if lk.Outside {
			l.add(lk.File, lk.Line, "S4", "link %q leaves the skill directory; use an absolute URL (an installer copies one skill alone)", lk.Target)
		}
	}
}

// ruleS8 keeps tracker ids and long code blocks out of a skill.
func (l *lintSkill) ruleS8() {
	for _, f := range l.textFiles() {
		for i, text := range strings.Split(string(f.Data), "\n") {
			if strings.Contains(text, "tlc://") {
				l.add(f.Rel, i+1, "S8", "tracker link tlc:// does not belong in a skill")
			}
			for _, id := range trackerIDs(text) {
				l.add(f.Rel, i+1, "S8", "tracker id %q does not belong in a skill", id)
			}
		}
		if !f.isMarkdown() || strings.HasPrefix(f.Rel, "assets/snippets/") {
			continue
		}
		for _, b := range fences(f.Data) {
			if b.Lines > maxFenceLines {
				l.add(f.Rel, b.Open, "S8", "fenced code block of %d lines (max %d); move it to a file under assets/snippets/", b.Lines, maxFenceLines)
			}
		}
	}
}

var trackerRe = regexp.MustCompile(`\b[A-Z]{1,5}-\d{2,6}\b`)

// notTrackers are standards and registries whose ids have the same shape.
var notTrackers = []string{"AES", "BIP", "CVE", "CWE", "ECMA", "ED", "ES", "FIPS", "GHSA", "IEEE", "ISO", "MD", "NIST", "PEP", "RFC", "RS", "SHA", "SSL", "TLS", "UTC", "UTF"}

func trackerIDs(line string) []string {
	var out []string
	for _, m := range trackerRe.FindAllString(line, -1) {
		if !contains(notTrackers, m[:strings.Index(m, "-")]) {
			out = append(out, m)
		}
	}
	return out
}

// ruleS9 checks scripts: POSIX shebang and the executable bit.
func (l *lintSkill) ruleS9() {
	for _, f := range l.under("scripts/") {
		if f.Mode&fs.ModeSymlink != 0 {
			continue
		}
		first, _, _ := strings.Cut(string(f.Data), "\n")
		if strings.TrimRight(first, "\r") != "#!/bin/sh" {
			l.add(f.Rel, 1, "S9", "first line must be #!/bin/sh")
		}
		if f.Mode&0o111 == 0 {
			l.add(f.Rel, 1, "S9", "script is not executable")
		}
	}
}

// fenceBlock is one fenced code block of a markdown file.
type fenceBlock struct {
	Open  int // line of the opening fence
	Lines int // lines between the fences
}

var fenceRe = regexp.MustCompile("^ {0,3}(`{3,}|~{3,})")

// scanMarkdown calls visit for each line outside fenced code and returns the
// fenced blocks. A block left open ends at the end of the file.
func scanMarkdown(data []byte, visit func(no int, text string)) []fenceBlock {
	var blocks []fenceBlock
	var open string
	var cur fenceBlock
	for i, text := range strings.Split(strings.TrimSuffix(string(data), "\n"), "\n") {
		m := fenceRe.FindStringSubmatch(text)
		switch {
		case open == "" && m != nil:
			open, cur = m[1], fenceBlock{Open: i + 1}
		case open != "" && m != nil && m[1][0] == open[0] && len(m[1]) >= len(open) && strings.TrimSpace(text[strings.Index(text, m[1])+len(m[1]):]) == "":
			blocks = append(blocks, cur)
			open = ""
		case open != "":
			cur.Lines++
		case visit != nil:
			visit(i+1, text)
		}
	}
	if open != "" {
		blocks = append(blocks, cur)
	}
	return blocks
}

func fences(data []byte) []fenceBlock { return scanMarkdown(data, nil) }

// relLink is a link whose target is a path inside or beside a skill.
type relLink struct {
	File     string // skill-relative file holding the link
	Line     int
	Target   string // as written
	Resolved string // skill-relative, cleaned; meaningful when !Outside
	Outside  bool
}

var (
	inlineLinkRe = regexp.MustCompile(`\]\(\s*(<[^>]*>|[^)\s]*)`)
	refDefRe     = regexp.MustCompile(`^ {0,3}\[[^\]]+\]:\s*(<[^>]*>|\S+)`)
	codeSpanRe   = regexp.MustCompile("`[^`]*`")
	schemeRe     = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9+.-]*:`)
)

// linkTargets lists the link destinations on one markdown line.
func linkTargets(text string) []string {
	text = codeSpanRe.ReplaceAllStringFunc(text, func(s string) string { return strings.Repeat(" ", len(s)) })
	var out []string
	for _, m := range inlineLinkRe.FindAllStringSubmatch(text, -1) {
		out = append(out, strings.Trim(m[1], "<>"))
	}
	if m := refDefRe.FindStringSubmatch(text); m != nil {
		out = append(out, strings.Trim(m[1], "<>"))
	}
	return out
}

// relativeLinks returns every non-URL link of the skill's markdown files.
func (l *lintSkill) relativeLinks() []relLink {
	var out []relLink
	for _, f := range l.textFiles() {
		if !f.isMarkdown() {
			continue
		}
		scanMarkdown(f.Data, func(no int, text string) {
			for _, t := range linkTargets(text) {
				if t == "" || strings.HasPrefix(t, "#") || schemeRe.MatchString(t) {
					continue
				}
				p := t
				if i := strings.IndexAny(p, "#?"); i >= 0 {
					p = p[:i]
				}
				lk := relLink{File: f.Rel, Line: no, Target: t}
				if path.IsAbs(p) {
					lk.Outside = true
				} else {
					lk.Resolved = path.Join(path.Dir(f.Rel), p)
					lk.Outside = lk.Resolved == ".." || strings.HasPrefix(lk.Resolved, "../")
				}
				out = append(out, lk)
			}
		})
	}
	return out
}
